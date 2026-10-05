package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"regexp"
	"strings"
	"time"
)

// logInput 兼容现有 /hook/sls 字段；TraceID 只作证据。
type logInput struct {
	Namespace string `json:"namespace_name"` // 命名空间。
	Container string `json:"container_name"` // 容器名。
	Class     string `json:"class"`          // 异常类。
	Message   string `json:"message"`        // 完整异常消息，分组前不截断。
	Level     string `json:"level"`          // ERROR 或历史 notify。
	TraceID   string `json:"traceId"`        // 请求追踪号。
	Timestamp string `json:"@timestamp"`     // RFC3339 原始发生时间。
	SLSTime   string `json:"sls_time_raw"`   // SLS 优先发生时间。
	ID        string `json:"_id"`            // 来源内事件 ID。
	Index     string `json:"_index"`         // 来源索引。
}

// metricInput 保留监控周期原始时间精度；labels 与 annotations 支持扩展。
type metricInput struct {
	Status      string            `json:"status"`      // firing 或 resolved。
	StartsAt    string            `json:"startsAt"`    // 必填 RFC3339Nano 周期。
	EndsAt      string            `json:"endsAt"`      // resolved 必填结束时刻。
	Fingerprint string            `json:"fingerprint"` // 可选指纹，不替代周期。
	Labels      map[string]string `json:"labels"`      // 指标维度。
	Annotations map[string]string `json:"annotations"` // 文案与旧 summary。
}

// eventInput 是来源适配后的可信事件。
type eventInput struct {
	Kind, Type, Resource, TimeRaw, SourceID string
	Occurred                                int64
	Payload                                 Row
	Detail                                  Row
}

var summaryPattern = regexp.MustCompile(`(?s)InstanceId=(.*?)\s+InstanceName=(.*?)\s+MetricKey=(.*?)\s+MetricValue=(.*?)\s+MetricRule=(.*)`)

// parseLog 先完整验证再产生任何持久副作用。
func parseLog(data []byte, delivery string) ([]eventInput, error) {
	var in logInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fail(400, "日志 Hook JSON 无效")
	}
	for _, s := range []string{in.Namespace, in.Container, in.Class, in.Level, in.Message} {
		if strings.TrimSpace(s) == "" {
			return nil, fail(400, "日志缺少 namespace_name、container_name、class、level 或 message")
		}
	}
	if len(in.Namespace) > 180 || len(in.Container) > 180 || len(in.Message) > 256*1024 || len(in.Class) > 255 || len(in.TraceID) > 255 || len(in.Level) > 50 {
		return nil, fail(400, "日志字段超过长度限制")
	}
	raw := in.SLSTime
	if raw == "" {
		raw = in.Timestamp
	}
	occurred := int64(0)
	if raw != "" {
		tm, e := time.Parse(time.RFC3339Nano, raw)
		if e != nil {
			return nil, fail(400, "日志发生时间格式错误")
		}
		occurred = tm.UnixMilli()
	}
	kind := "LOG"
	if strings.EqualFold(in.Level, "notify") || strings.EqualFold(in.Level, "NOTICE") {
		kind = "NOTICE"
	}
	sid := delivery
	if sid == "" && in.ID != "" && in.Index != "" {
		sid = in.Index + ":" + in.ID
	}
	if len(sid) > 512 {
		return nil, fail(400, "事件标识过长")
	}
	detail := Row{"namespace_name": in.Namespace, "container_name": in.Container, "service_key": in.Namespace + "_" + in.Container, "trace_id": in.TraceID, "class_name": in.Class, "message_sha": digest(in.Message), "message": in.Message, "source_level": in.Level}
	return []eventInput{{Kind: kind, Type: kind, Resource: detail.S("service_key"), TimeRaw: raw, SourceID: sid, Occurred: occurred, Detail: detail, Payload: Row{"detail": detail, "source_time_raw": raw}}}, nil
}

// parseMetric 接受旧 summary 和标签格式，整批验证失败不会留下半批数据。
func parseMetric(data []byte, delivery string) ([]eventInput, error) {
	var batch struct {
		Alerts []metricInput `json:"alerts"`
	}
	if e := json.Unmarshal(data, &batch); e != nil || len(batch.Alerts) == 0 || len(batch.Alerts) > 100 {
		return nil, fail(400, "指标 Hook 必须包含 1 至 100 条 alerts")
	}
	out := []eventInput{}
	for i, in := range batch.Alerts {
		kind := strings.ToUpper(in.Status)
		if kind != "FIRING" && kind != "RESOLVED" {
			return nil, fail(400, "指标状态必须为 firing 或 resolved")
		}
		start, e := time.Parse(time.RFC3339Nano, in.StartsAt)
		if e != nil || start.UnixMilli() <= 0 {
			return nil, fail(400, "指标 startsAt 无效")
		}
		end := int64(0)
		if kind == "RESOLVED" {
			et, e := time.Parse(time.RFC3339Nano, in.EndsAt)
			if e != nil || et.Before(start) {
				return nil, fail(400, "指标恢复 endsAt 无效")
			}
			end = et.UnixMilli()
		}
		instance, name, key, value, rule := in.Labels["instanceId"], in.Labels["instanceName"], in.Labels["metricKey"], in.Labels["metricValue"], in.Labels["metricRuleId"]
		if parts := summaryPattern.FindStringSubmatch(in.Annotations["summary"]); len(parts) == 6 {
			instance, name, key, value, rule = parts[1], parts[2], parts[3], parts[4], parts[5]
		}
		if instance == "" || key == "" || len(instance) > 400 || len(key) > 255 || len(rule) > 1024 || len(value) > 512 || len(name) > 255 {
			return nil, fail(400, "指标实例、指标键或规则字段无效")
		}
		valueStatus := "AVAILABLE"
		if strings.TrimSpace(value) == "" {
			valueStatus = "MISSING"
		}
		detail := Row{"instance_id": instance, "instance_name": name, "metric_key": key, "metric_rule": rule, "metric_value_raw": value, "value_status": valueStatus, "fingerprint": in.Fingerprint, "starts_at_raw": in.StartsAt, "ends_at_raw": in.EndsAt, "starts_at": start.UnixMilli(), "ends_at": end, "labels": encode(in.Labels), "annotations": encode(in.Annotations)}
		sid := ""
		if delivery != "" {
			sid = fmt.Sprintf("%s:%d", delivery, i)
		}
		occurred := time.Now().UnixMilli()
		raw := ""
		if kind == "RESOLVED" {
			occurred = end
			raw = in.EndsAt
		}
		out = append(out, eventInput{Kind: "METRIC", Type: kind, Resource: instance, SourceID: sid, Occurred: occurred, TimeRaw: raw, Detail: detail, Payload: Row{"detail": detail, "status": kind}})
	}
	return out, nil
}

// ingest 同一事务保存事件和详情，提交后才确认 Hook。
func (a *App) ingest(ctx context.Context, source Row, events []eventInput) ([]string, error) {
	ids := []string{}
	err := a.transaction(ctx, func(tx *sql.Tx) error {
		ids = []string{}
		if _, e := one(ctx, tx, "SELECT id FROM alert_source WHERE id=? FOR UPDATE", source.I("id")); e != nil {
			return e
		}
		for _, event := range events {
			key, quality := "receipt:"+nonce(), "NONE"
			if event.SourceID != "" {
				key = "source:" + digest(event.SourceID)
				quality = "STRONG"
			}
			hash := digest(encode(event.Payload))
			old, e := one(ctx, tx, "SELECT * FROM alert_event WHERE alert_source_id=? AND dedup_key=?", source.I("id"), key)
			if e != nil {
				return e
			}
			if old != nil {
				if old.S("payload_hash") != hash {
					return fail(409, "相同来源事件标识携带不同内容")
				}
				ids = append(ids, old.S("id"))
				continue
			}
			rid := int64(0)
			rtype := "SERVICE"
			if event.Kind == "METRIC" {
				rtype = "INSTANCE"
			}
			resource, e := one(ctx, tx, "SELECT * FROM alert_resource WHERE alert_source_id=? AND resource_type=? AND resource_key=? AND config_status='ENABLED'", source.I("id"), rtype, event.Resource)
			if e != nil {
				return e
			}
			if resource != nil {
				rid = resource.I("id")
			}
			eid, e := a.insert(ctx, tx, "alert_event", Row{"alert_source_id": source.I("id"), "alert_resource_id": rid, "alert_round_id": 0, "alert_type": event.Kind, "event_type": event.Type, "dedup_key": key, "dedup_quality": quality, "source_event_id": event.SourceID, "payload_hash": hash, "occurred_time": event.Occurred, "received_time": time.Now().UnixMilli(), "source_time_raw": event.TimeRaw, "payload": encode(event.Payload), "process_status": "RECEIVED", "process_error": ""})
			if e != nil {
				return e
			}
			detail := Row{}
			for k, v := range event.Detail {
				detail[k] = v
			}
			detail["alert_event_id"] = eid
			table := "alert_log_detail"
			if event.Kind == "METRIC" {
				table = "alert_metric_detail"
			}
			if _, e = a.insert(ctx, tx, table, detail); e != nil {
				return e
			}
			ids = append(ids, fmt.Sprint(eid))
		}
		return nil
	})
	return ids, err
}

// ProcessEvents 恢复持久接入队列；未匹配恢复事件保留待核对，绝不猜测最新轮次。
func (a *App) ProcessEvents(ctx context.Context, limit int) error {
	pending, e := rows(ctx, a.DB, "SELECT id,alert_source_id FROM alert_event WHERE process_status='RECEIVED' ORDER BY received_time,id LIMIT ?", limit)
	if e != nil {
		return e
	}
	for _, p := range pending {
		err := a.transaction(ctx, func(tx *sql.Tx) error {
			if _, e := one(ctx, tx, "SELECT id FROM alert_source WHERE id=? FOR UPDATE", p.I("alert_source_id")); e != nil {
				return e
			}
			ev, e := one(ctx, tx, "SELECT * FROM alert_event WHERE id=? FOR UPDATE", p.I("id"))
			if e != nil {
				return e
			}
			if ev == nil || ev.S("process_status") != "RECEIVED" {
				return nil
			}
			return a.applyEvent(ctx, tx, ev)
		})
		if err != nil {
			var me *mysql.MySQLError
			if errors.As(err, &me) && (me.Number == 1406 || me.Number == 1264 || me.Number == 3819) {
				if e := update(ctx, a.DB, "alert_event", p.I("id"), Row{"process_status": "NEEDS_REVIEW", "process_error": "事件数据不满足持久化约束，等待接入核对"}); e != nil {
					return e
				}
				continue
			}
			return err
		}
	}
	return nil
}

// eventReview 保留无法安全归属的事件及明确原因。
func eventReview(ctx context.Context, tx *sql.Tx, ev Row, message string) error {
	return update(ctx, tx, "alert_event", ev.I("id"), Row{"process_status": "NEEDS_REVIEW", "process_error": message})
}

// applyEvent 在问题锁下判定轮次，保持历史终态和精确监控周期。
func (a *App) applyEvent(ctx context.Context, tx *sql.Tx, ev Row) error {
	resource, e := one(ctx, tx, "SELECT * FROM alert_resource WHERE id=? AND config_status='ENABLED'", ev.I("alert_resource_id"))
	if e != nil {
		return e
	}
	if resource == nil {
		return eventReview(ctx, tx, ev, "未配置可信来源资源映射")
	}
	route, e := one(ctx, tx, "SELECT r.* FROM alert_route r JOIN alert_group g ON g.id=r.alert_group_id WHERE r.alert_resource_id=? AND r.alert_type=? AND r.config_status='ENABLED' AND g.config_status='ENABLED'", resource.I("id"), ev.S("alert_type"))
	if e != nil {
		return e
	}
	if route == nil {
		return eventReview(ctx, tx, ev, "未配置固定告警群路由")
	}
	if ev.S("alert_type") == "NOTICE" {
		if _, e = a.newCard(ctx, tx, 0, ev.I("id"), route.I("alert_group_id"), "NOTICE", "notice:"+ev.S("id"), 1); e != nil {
			return e
		}
		return update(ctx, tx, "alert_event", ev.I("id"), Row{"process_status": "APPLIED", "process_error": ""})
	}
	table := "alert_log_detail"
	if ev.S("alert_type") == "METRIC" {
		table = "alert_metric_detail"
	}
	detail, e := one(ctx, tx, "SELECT * FROM "+table+" WHERE alert_event_id=?", ev.I("id"))
	if e != nil {
		return e
	}
	if detail == nil {
		return errors.New("持久事件缺少类型详情")
	}
	parts := []string{detail.S("service_key"), detail.S("message_sha")}
	title := detail.S("class_name") + " · " + detail.S("service_key")
	if ev.S("alert_type") == "METRIC" {
		parts = []string{detail.S("instance_id"), detail.S("metric_key")}
		title = detail.S("instance_name") + " · " + detail.S("metric_key")
	}
	data := encode(parts)
	key := digest(data)
	problem, e := one(ctx, tx, "SELECT * FROM alert_problem WHERE alert_resource_id=? AND alert_type=? AND key_version=1 AND grouping_key=? FOR UPDATE", resource.I("id"), ev.S("alert_type"), key)
	if e != nil {
		return e
	}
	if problem == nil {
		id, e := a.insert(ctx, tx, "alert_problem", Row{"alert_resource_id": resource.I("id"), "alert_type": ev.S("alert_type"), "key_version": 1, "grouping_key": key, "grouping_data": data, "title": truncate(title, 500)})
		if e != nil {
			return e
		}
		problem = Row{"id": id, "grouping_data": parts}
	}
	if problem.S("grouping_data") != data {
		return eventReview(ctx, tx, ev, "分组摘要冲突")
	}
	var round Row
	cycle := ""
	when := ev.I("occurred_time")
	if ev.S("alert_type") == "METRIC" {
		start, e := time.Parse(time.RFC3339Nano, detail.S("starts_at_raw"))
		if e != nil {
			return e
		}
		cycle = start.UTC().Format(time.RFC3339Nano)
		round, e = one(ctx, tx, "SELECT * FROM alert_round WHERE alert_problem_id=? AND cycle_key=? FOR UPDATE", problem.I("id"), cycle)
		if e != nil {
			return e
		}
		if round == nil && ev.S("event_type") == "RESOLVED" {
			return eventReview(ctx, tx, ev, "等待匹配 startsAt 的告警轮次")
		}
		if round == nil {
			open, e := one(ctx, tx, "SELECT id FROM alert_round WHERE alert_problem_id=? AND open_marker=0", problem.I("id"))
			if e != nil {
				return e
			}
			if open != nil {
				return eventReview(ctx, tx, ev, "旧周期尚未恢复，新周期重叠待核对")
			}
			when = start.UnixMilli()
		}
	} else {
		last, e := one(ctx, tx, "SELECT * FROM alert_round WHERE alert_problem_id=? ORDER BY round_no DESC LIMIT 1 FOR UPDATE", problem.I("id"))
		if e != nil {
			return e
		}
		if last != nil {
			if when == 0 && last.I("round_no") > 1 || when == 0 && last.I("open_marker") != 0 {
				return eventReview(ctx, tx, ev, "缺少发生时间，无法判定已结束轮次后的归属")
			}
			if when > 0 {
				old, e := one(ctx, tx, "SELECT * FROM alert_round WHERE alert_problem_id=? AND open_marker<>0 AND first_event_time<=? AND end_time>=? ORDER BY round_no DESC LIMIT 1 FOR UPDATE", problem.I("id"), when, when)
				if e != nil {
					return e
				}
				if old != nil {
					round = old
				} else if when < last.I("first_event_time") {
					return eventReview(ctx, tx, ev, "早于已知轮次的迟到日志待核对")
				}
			}
			if round == nil && last.I("open_marker") == 0 {
				round = last
			}
		}
	}
	fresh := round == nil
	if fresh {
		if when == 0 {
			when = ev.I("received_time")
		}
		rno := int64(1)
		last, e := one(ctx, tx, "SELECT round_no FROM alert_round WHERE alert_problem_id=? ORDER BY round_no DESC LIMIT 1", problem.I("id"))
		if e != nil {
			return e
		}
		if last != nil {
			rno = last.I("round_no") + 1
		}
		rid, e := a.nextID()
		if e != nil {
			return e
		}
		if cycle == "" {
			cycle = fmt.Sprint(rid)
		}
		quality := "EXACT"
		if ev.S("dedup_quality") != "STRONG" {
			quality = "BEST_EFFORT"
		}
		round = Row{"alert_problem_id": problem.I("id"), "alert_group_id": route.I("alert_group_id"), "alert_project_id": resource.I("alert_project_id"), "alert_resource_id": resource.I("id"), "alert_type": ev.S("alert_type"), "alert_no": "AL" + fmt.Sprint(rid), "round_no": rno, "round_status": "PENDING", "open_marker": 0, "cycle_key": cycle, "cycle_data": encode(Row{"source_time": detail.S("starts_at_raw")}), "event_count": 0, "count_quality": quality, "first_event_time": when, "last_event_time": when, "last_receive_time": ev.I("received_time"), "end_time": 0, "end_record_time": 0, "handled_sys_user_id": 0, "result_text": "", "version": 0}
		id, e := a.insert(ctx, tx, "alert_round", round)
		if e != nil {
			return e
		}
		round["id"] = id
		round["alert_no"] = "AL" + fmt.Sprint(id)
		if e = update(ctx, tx, "alert_round", id, Row{"alert_no": round.S("alert_no")}); e != nil {
			return e
		}
		if ev.S("alert_type") == "LOG" {
			if _, e = a.newAITask(ctx, tx, round); e != nil {
				return e
			}
		}
		if _, e = a.newCard(ctx, tx, id, 0, round.I("alert_group_id"), round.S("alert_type"), "round:"+fmt.Sprint(id), 1); e != nil {
			return e
		}
	}
	if when == 0 {
		when = ev.I("received_time")
	}
	patch := Row{"event_count": round.I("event_count") + 1, "first_event_time": min(round.I("first_event_time"), when), "last_event_time": max(round.I("last_event_time"), when), "last_receive_time": max(round.I("last_receive_time"), ev.I("received_time"))}
	if ev.S("dedup_quality") != "STRONG" {
		patch["count_quality"] = "BEST_EFFORT"
	}
	if ev.S("event_type") == "RESOLVED" && round.I("open_marker") == 0 {
		patch["round_status"] = "RECOVERED"
		patch["open_marker"] = round.I("id")
		patch["end_time"] = detail.I("ends_at")
		patch["end_record_time"] = time.Now().UnixMilli()
		if _, e = a.record(ctx, tx, round.I("id"), 0, "SYSTEM", "监控系统", "METRIC_RECOVERED", "HOOK", "recovery:"+ev.S("id"), "监控确认本轮已恢复", detail); e != nil {
			return e
		}
	}
	if e = update(ctx, tx, "alert_round", round.I("id"), patch); e != nil {
		return e
	}
	if e = update(ctx, tx, "alert_event", ev.I("id"), Row{"alert_round_id": round.I("id"), "process_status": "APPLIED", "process_error": ""}); e != nil {
		return e
	}
	if e = a.touchRound(ctx, tx, round.I("id")); e != nil {
		return e
	}
	if fresh && ev.S("alert_type") == "METRIC" {
		return mustExec(ctx, tx, "UPDATE alert_event SET process_status='RECEIVED',process_error='' WHERE alert_source_id=? AND event_type='RESOLVED' AND process_status='NEEDS_REVIEW' AND process_error='等待匹配 startsAt 的告警轮次'", ev.I("alert_source_id"))
	}
	return nil
}

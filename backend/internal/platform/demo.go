package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ResetDemo 重建隔离演示库；只允许显式 Demo 且库名带 demo 或 test。
func (a *App) ResetDemo(ctx context.Context) error {
	var database string
	if e := a.DB.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); e != nil {
		return e
	}
	if !a.Config.Demo || !strings.Contains(database, "test") && !strings.Contains(database, "demo") {
		return errors.New("重置仅允许隔离演示数据库")
	}
	tables, e := rows(ctx, a.DB, "SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE()")
	if e != nil {
		return e
	}
	return a.transaction(ctx, func(tx *sql.Tx) error {
		for _, t := range tables {
			table := t.S("TABLE_NAME")
			if table == "" {
				table = t.S("table_name")
			}
			if !strings.HasPrefix(table, "alert_") && !strings.HasPrefix(table, "sys_") {
				return errors.New("演示库包含未知表")
			}
			if e := mustExec(ctx, tx, "DELETE FROM `"+table+"`"); e != nil {
				return e
			}
		}
		pid, e := a.insert(ctx, tx, "alert_project", Row{"project_code": "demo", "name": "智享云演示项目", "workspace_root": "/tmp/alertops-demo-project", "runner_image": "alertops-runner:local", "credential_ref": "", "config_status": "ENABLED"})
		if e != nil {
			return e
		}
		for n := 1; n <= 3; n++ {
			_, e = a.insert(ctx, tx, "alert_project_workspace", Row{"alert_project_id": pid, "slot_no": n, "workspace_path": fmt.Sprintf("/tmp/alertops-demo-project/slot-%d", n), "workspace_status": "READY", "active_alert_ai_task_id": 0, "assignment_version": 0, "blocked_reason": "", "last_check_time": 0})
			if e != nil {
				return e
			}
		}
		gid, e := a.insert(ctx, tx, "alert_group", Row{"corp_id": a.Config.CorpID, "app_id": a.Config.AppID, "conversation_id": "demo-group", "name": "AlertOps 专用验证群", "config_status": "ENABLED", "integration_status": "READY", "last_error": ""})
		if e != nil {
			return e
		}
		for _, kind := range []string{"LOG", "METRIC"} {
			adapter, rtype, key, name := "SLS", "SERVICE", "demo_api", "订单服务"
			if kind == "METRIC" {
				adapter, rtype, key, name = "ALERTMANAGER", "INSTANCE", "demo-server", "演示服务器"
			}
			sid, e := a.insert(ctx, tx, "alert_source", Row{"source_code": "demo-" + strings.ToLower(kind), "source_type": kind, "environment": "demo", "adapter_type": adapter, "auth_ref": "DEMO_HOOK_SECRET", "config_status": "ENABLED"})
			if e != nil {
				return e
			}
			rid, e := a.insert(ctx, tx, "alert_resource", Row{"alert_source_id": sid, "alert_project_id": pid, "resource_type": rtype, "resource_key": key, "name": name, "config_status": "ENABLED"})
			if e != nil {
				return e
			}
			kinds := []string{kind}
			if kind == "LOG" {
				kinds = append(kinds, "NOTICE")
			}
			for _, typ := range kinds {
				if _, e = a.insert(ctx, tx, "alert_route", Row{"alert_resource_id": rid, "alert_group_id": gid, "alert_type": typ, "config_status": "ENABLED"}); e != nil {
					return e
				}
			}
		}
		return nil
	})
}

// DemoScenario 通过与 Hook 相同的适配、鉴权来源和持久接入链路生成受控测试事件。
func (a *App) DemoScenario(ctx context.Context, kind string) error {
	if !a.Config.Demo {
		return fail(404, "演示入口未启用")
	}
	sourceCode := "demo-log"
	var events []eventInput
	var e error
	now := time.Now().UTC().Format(time.RFC3339Nano)
	switch kind {
	case "log", "notice":
		level, message := "ERROR", "订单提交失败：库存服务返回空结果"
		if kind == "notice" {
			level, message = "NOTICE", "订单服务发布完成，开始观察运行状态"
		}
		events, e = parseLog([]byte(encode(Row{"namespace_name": "demo", "container_name": "api", "class": "OrderService.submit", "level": level, "message": message, "traceId": "demo-" + nonce()[:12], "@timestamp": now})), "demo:"+nonce())
	case "metric", "recover":
		sourceCode = "demo-metric"
		starts := now
		active, er := one(ctx, a.DB, "SELECT d.starts_at_raw FROM alert_round r JOIN alert_event e ON e.alert_round_id=r.id JOIN alert_metric_detail d ON d.alert_event_id=e.id WHERE r.alert_type='METRIC' AND r.open_marker=0 ORDER BY r.id DESC,e.id LIMIT 1")
		if er != nil {
			return er
		}
		if active != nil {
			starts = active.S("starts_at_raw")
		}
		status, value, ends := "firing", "95%", ""
		if kind == "recover" {
			if active == nil {
				return fail(409, "当前没有可恢复的演示指标轮次")
			}
			status, value, ends = "resolved", "", now
		}
		events, e = parseMetric([]byte(encode(Row{"alerts": []Row{{"status": status, "startsAt": starts, "endsAt": ends, "labels": Row{"instanceId": "demo-server", "instanceName": "演示应用服务器", "metricKey": "CPU 使用率", "metricValue": value, "metricRuleId": "连续 5 分钟 > 90%"}}}})), "demo:"+nonce())
	default:
		return fail(400, "未知模拟场景")
	}
	if e != nil {
		return e
	}
	source, e := one(ctx, a.DB, "SELECT * FROM alert_source WHERE source_code=?", sourceCode)
	if e != nil {
		return e
	}
	if source == nil {
		return errors.New("演示来源尚未初始化")
	}
	_, e = a.ingest(ctx, source, events)
	return e
}

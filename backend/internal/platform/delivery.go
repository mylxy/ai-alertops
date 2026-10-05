package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// withExternalLock 持有独立连接的 MySQL advisory lock，外部调用期间不持有业务行锁。
// 租约过期不会绕过此锁；连接中断后的未知结果仍须按稳定外部 ID 核对。
func (a *App) withExternalLock(ctx context.Context, key string, fn func() error) error {
	conn, e := a.lockDB.Conn(ctx)
	if e != nil {
		return e
	}
	defer conn.Close()
	name := "alertops:" + digest(key)[:48]
	var acquired sql.NullInt64
	if e = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", name).Scan(&acquired); e != nil {
		return e
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return fail(409, "目标正在同步")
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var released sql.NullInt64
		if err := conn.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK(?)", name).Scan(&released); err != nil {
			_ = conn.Close()
		}
	}()
	return fn()
}

// ProcessJobs 执行已持久化 outbox；业务保存与消息失败彼此独立。
func (a *App) ProcessJobs(ctx context.Context, limit int) error {
	now := time.Now().UnixMilli()
	jobs, e := rows(ctx, a.DB, "SELECT * FROM alert_job WHERE (job_status IN ('PENDING','FAILED','UNKNOWN') AND next_run_time<=?) OR (job_status='RUNNING' AND lease_until<=?) ORDER BY create_time,id LIMIT ?", now, now, limit)
	if e != nil {
		return e
	}
	for _, j := range jobs {
		e = a.withExternalLock(ctx, "job:"+j.S("id"), func() error {
			current, e := one(ctx, a.DB, "SELECT * FROM alert_job WHERE id=?", j.I("id"))
			if e != nil {
				return e
			}
			if current == nil || current.S("job_status") == "SUCCEEDED" {
				return nil
			}
			owner := nonce()
			if e = update(ctx, a.DB, "alert_job", j.I("id"), Row{"job_status": "RUNNING", "lease_owner": owner, "lease_until": time.Now().Add(time.Minute).UnixMilli(), "attempt_count": current.I("attempt_count") + 1}); e != nil {
				return e
			}
			var effectErr error
			switch j.S("job_type") {
			case "CARD_SEND":
				effectErr = a.syncCard(ctx, j.I("target_id"))
			case "ROUND_CARD_SYNC":
				cards, er := rows(ctx, a.DB, "SELECT id FROM alert_card WHERE alert_round_id=? ORDER BY id", j.I("target_id"))
				effectErr = er
				if er == nil {
					for _, c := range cards {
						if er = a.syncCard(ctx, c.I("id")); er != nil {
							// 单实例待核对不阻断同轮其他正常消息。
							effectErr = errors.Join(effectErr, er)
						}
					}
				}
			case "TOPBOX_SYNC":
				effectErr = a.syncTopbox(ctx, j.I("target_id"))
			case "AI_PROGRESS_APPLY":
				effectErr = a.applyProgressJob(ctx, j)
			case "MR_RECONCILE":
				effectErr = a.reconcileMR(ctx, j)
			default:
				effectErr = errors.New("不支持的持久任务类型")
			}
			state, message, next := "SUCCEEDED", "", int64(0)
			if effectErr != nil {
				state = "FAILED"
				var unknown *UncertainEffectError
				if errors.As(effectErr, &unknown) {
					state = "UNKNOWN"
				}
				message = truncate(effectErr.Error(), 240)
				delay := min(int64(300), int64(1)<<min(current.I("attempt_count"), 8))
				next = time.Now().Add(time.Duration(delay) * time.Second).UnixMilli()
			}
			return mustExec(ctx, a.DB, "UPDATE alert_job SET job_status=?,last_error=?,next_run_time=?,lease_owner='',lease_until=0,update_time=? WHERE id=? AND lease_owner=?", state, message, next, time.Now().UnixMilli(), j.I("id"), owner)
		})
		var pe *problemError
		if e != nil && (!errors.As(e, &pe) || pe.Status != 409) {
			return e
		}
	}
	return nil
}

// truncate 限制外部错误文案，避免超长写入反向阻断重试。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// syncCard 目标级串行读取最新轮次，迟到任务不能把旧内容覆盖到新卡片。
func (a *App) syncCard(ctx context.Context, id int64) error {
	return a.withExternalLock(ctx, fmt.Sprintf("card:%d", id), func() error {
		card, e := one(ctx, a.DB, "SELECT c.*,g.conversation_id FROM alert_card c JOIN alert_group g ON g.id=c.alert_group_id WHERE c.id=?", id)
		if e != nil {
			return e
		}
		if card == nil {
			return errors.New("卡片不存在")
		}
		if card.S("delivery_status") == "SENT" && card.I("sent_version") >= card.I("desired_version") {
			return nil
		}
		if card.S("delivery_status") == "SENDING" || card.S("delivery_status") == "UNKNOWN" {
			if e = a.resolvePending(ctx, "CARD", card.S("out_track_id")); e != nil {
				return e
			}
		}
		view := Row{}
		for k, v := range card {
			view[k] = v
		}
		version := card.I("desired_version")
		if card.I("alert_round_id") > 0 {
			detail, e := a.roundDetail(ctx, card.I("alert_round_id"))
			if e != nil {
				return e
			}
			r := detail["round"].(Row)
			for k, v := range r {
				if k != "id" {
					view[k] = v
				}
			}
			version = r.I("version")
			view["detail"] = detail
			view["h5_url"] = a.Config.BaseURL + "/h5/groups/" + r.S("alert_group_id") + "?round=" + r.S("id")
			records := detail["records"].([]Row)
			if len(records) > 3 {
				records = records[len(records)-3:]
			}
			view["recent_records"] = records
			view["merge_requests"] = detail["merge_requests"]
			view["all_merged"] = detail["all_merged"]
		} else {
			detail, e := one(ctx, a.DB, "SELECT e.*,l.service_key,l.class_name,l.message,p.name AS project_name FROM alert_event e JOIN alert_log_detail l ON l.alert_event_id=e.id JOIN alert_resource r ON r.id=e.alert_resource_id JOIN alert_project p ON p.id=r.alert_project_id WHERE e.id=?", card.I("alert_event_id"))
			if e != nil {
				return e
			}
			view["notice"] = detail
		}
		view["version"] = version
		if e = update(ctx, a.DB, "alert_card", id, Row{"delivery_status": "SENDING"}); e != nil {
			return e
		}
		if e = a.Messenger.Card(ctx, view); e != nil {
			state := "FAILED"
			var unknown *UncertainEffectError
			if errors.As(e, &unknown) {
				state = "UNKNOWN"
			}
			if saveErr := update(ctx, a.DB, "alert_card", id, Row{"delivery_status": state, "last_error": truncate(e.Error(), 240)}); saveErr != nil {
				return saveErr
			}
			return e
		}
		return update(ctx, a.DB, "alert_card", id, Row{"delivery_status": "SENT", "sent_version": version, "last_error": ""})
	})
}

// syncTopbox 按当前轮次计数，包含卡片投递失败的待处理和处理中事项。
func (a *App) syncTopbox(ctx context.Context, gid int64) error {
	return a.withExternalLock(ctx, fmt.Sprintf("topbox:%d", gid), func() error {
		group, e := one(ctx, a.DB, "SELECT * FROM alert_group WHERE id=?", gid)
		if e != nil {
			return e
		}
		if group == nil {
			return errors.New("群不存在")
		}
		counts, e := rows(ctx, a.DB, "SELECT alert_type,round_status,COUNT(*) AS total FROM alert_round WHERE alert_group_id=? AND open_marker=0 GROUP BY alert_type,round_status", gid)
		if e != nil {
			return e
		}
		total := int64(0)
		for _, c := range counts {
			total += c.I("total")
		}
		desired := "CLOSED"
		if total > 0 {
			desired = "OPEN"
		}
		top, e := one(ctx, a.DB, "SELECT * FROM alert_topbox WHERE alert_group_id=?", gid)
		if e != nil {
			return e
		}
		if top == nil {
			id, e := a.insert(ctx, a.DB, "alert_topbox", Row{"alert_group_id": gid, "out_track_id": "alertops-top-" + nonce(), "desired_status": desired, "observed_status": "UNKNOWN", "desired_version": 1, "sent_version": 0, "last_error": "", "last_sync_time": 0})
			if e != nil {
				return e
			}
			top, e = one(ctx, a.DB, "SELECT * FROM alert_topbox WHERE id=?", id)
			if e != nil {
				return e
			}
		}
		if top.S("sync_state") == "SENDING" || top.S("sync_state") == "UNKNOWN" {
			if e = a.resolvePending(ctx, "TOPBOX", top.S("out_track_id")); e != nil {
				return e
			}
		}
		if desired == "OPEN" && top.S("observed_status") == "CLOSED" {
			top["out_track_id"] = "alertops-top-" + nonce()
			top["observed_status"] = "UNKNOWN"
			if e = update(ctx, a.DB, "alert_topbox", top.I("id"), Row{"out_track_id": top.S("out_track_id"), "observed_status": "UNKNOWN"}); e != nil {
				return e
			}
		}
		version := top.I("desired_version") + 1
		view := Row{"out_track_id": top.S("out_track_id"), "conversation_id": group.S("conversation_id"), "desired_status": desired, "observed_status": top.S("observed_status"), "counts": counts, "total": total, "template_id": a.Config.Templates["TOPBOX"], "h5_url": a.Config.BaseURL + "/h5/groups/" + fmt.Sprint(gid), "version": version}
		if e = update(ctx, a.DB, "alert_topbox", top.I("id"), Row{"desired_status": desired, "desired_version": version, "sync_state": "SENDING"}); e != nil {
			return e
		}
		if e = a.Messenger.Topbox(ctx, view); e != nil {
			state, observed := "FAILED", top.S("observed_status")
			var unknown *UncertainEffectError
			if errors.As(e, &unknown) {
				state = "UNKNOWN"
				observed = "UNKNOWN"
			}
			if saveErr := update(ctx, a.DB, "alert_topbox", top.I("id"), Row{"sync_state": state, "observed_status": observed, "last_error": truncate(e.Error(), 240)}); saveErr != nil {
				return saveErr
			}
			return e
		}
		return update(ctx, a.DB, "alert_topbox", top.I("id"), Row{"observed_status": desired, "sync_state": "SYNCED", "sent_version": version, "last_error": "", "last_sync_time": time.Now().UnixMilli()})
	})
}

// resolvePending 没有提供方完成证据时保守暂停这个目标，其他告警与人工保存继续。
func (a *App) resolvePending(ctx context.Context, kind, id string) error {
	if resolver, ok := a.Messenger.(PendingResolver); ok {
		return resolver.ResolvePending(ctx, kind, id)
	}
	return &UncertainEffectError{"外部请求结果未知，已暂停此目标写入；须先由提供方核实原请求完成"}
}

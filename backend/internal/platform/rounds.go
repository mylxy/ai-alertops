package platform

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// enqueue 在业务事务中追加唯一 outbox；同键不同内容由调用方稳定生成。
func (a *App) enqueue(ctx context.Context, tx *sql.Tx, kind, key, target string, id, version int64, payload any) error {
	old, e := one(ctx, tx, "SELECT id,payload FROM alert_job WHERE job_key=?", key)
	if e != nil {
		return e
	}
	if old != nil {
		if old.S("payload") != encode(payload) {
			return fail(409, "回调幂等键的内容冲突")
		}
		return nil
	}
	_, e = a.insert(ctx, tx, "alert_job", Row{"job_type": kind, "job_key": key, "target_type": target, "target_id": id, "target_version": version, "payload": encode(payload), "job_status": "PENDING", "attempt_count": 0, "next_run_time": time.Now().UnixMilli(), "lease_owner": "", "lease_until": 0, "last_error": ""})
	return e
}

// newCard 为首次或有效 H5 操作分配稳定投递 ID，网络重试不会换 ID。
func (a *App) newCard(ctx context.Context, tx *sql.Tx, rid, eid, gid int64, kind, key string, version int64) (int64, error) {
	old, e := one(ctx, tx, "SELECT id FROM alert_card WHERE delivery_key=?", key)
	if e != nil {
		return 0, e
	}
	if old != nil {
		return old.I("id"), nil
	}
	id, e := a.insert(ctx, tx, "alert_card", Row{"alert_round_id": rid, "alert_event_id": eid, "alert_group_id": gid, "card_type": kind, "delivery_key": key, "out_track_id": "alertops-" + nonce(), "template_id": a.Config.Templates[kind], "template_version": "1", "delivery_status": "PENDING", "desired_version": version, "sent_version": 0, "last_error": ""})
	if e != nil {
		return 0, e
	}
	e = a.enqueue(ctx, tx, "CARD_SEND", "send:"+key, "CARD", id, version, Row{})
	return id, e
}

// touchRound 将可见变更与所有卡片和吊顶同步需求一起提交。
func (a *App) touchRound(ctx context.Context, tx *sql.Tx, rid int64) error {
	if e := mustExec(ctx, tx, "UPDATE alert_round SET version=version+1,update_time=? WHERE id=?", time.Now().UnixMilli(), rid); e != nil {
		return e
	}
	r, e := one(ctx, tx, "SELECT * FROM alert_round WHERE id=?", rid)
	if e != nil {
		return e
	}
	if e = mustExec(ctx, tx, "UPDATE alert_card SET desired_version=? WHERE alert_round_id=?", r.I("version"), rid); e != nil {
		return e
	}
	if e = a.enqueue(ctx, tx, "ROUND_CARD_SYNC", fmt.Sprintf("round:%d:%d", rid, r.I("version")), "ROUND", rid, r.I("version"), Row{}); e != nil {
		return e
	}
	return a.enqueue(ctx, tx, "TOPBOX_SYNC", fmt.Sprintf("top:%d:%d:%d", r.I("alert_group_id"), rid, r.I("version")), "GROUP", r.I("alert_group_id"), r.I("version"), Row{})
}

// record 追加不可编辑历史，操作摘要绑定入口、身份、类型和内容。
func (a *App) record(ctx context.Context, tx *sql.Tx, rid, uid int64, actor, name, kind, entry, key, content string, structured any) (int64, error) {
	hash := digest(encode([]any{uid, actor, kind, entry, content, structured}))
	old, e := one(ctx, tx, "SELECT * FROM alert_record WHERE alert_round_id=? AND operation_key=?", rid, key)
	if e != nil {
		return 0, e
	}
	if old != nil {
		if old.S("request_hash") != hash {
			return 0, fail(409, "相同操作键不能用于不同内容或操作者")
		}
		return old.I("id"), nil
	}
	return a.insert(ctx, tx, "alert_record", Row{"alert_round_id": rid, "actor_sys_user_id": uid, "actor_type": actor, "actor_name": name, "record_type": kind, "entry_point": entry, "operation_key": key, "request_hash": hash, "content": content, "structured_data": encode(structured), "occurred_time": time.Now().UnixMilli()})
}

// humanAction 提交人工备注或日志结果，锁内重新校验账号及终态。
func (a *App) humanAction(ctx context.Context, user Row, rid int64, entry, kind, key, content string) (Row, error) {
	content = strings.TrimSpace(content)
	if content == "" || len(content) > 8000 || key == "" || len(key) > 90 {
		return nil, fail(400, "请输入有效内容及操作幂等键")
	}
	round, e := one(ctx, a.DB, "SELECT * FROM alert_round WHERE id=?", rid)
	if e != nil {
		return nil, e
	}
	if round == nil {
		return nil, fail(404, "告警不存在")
	}
	if e = a.verifyMember(ctx, user.I("id"), round.I("alert_group_id")); e != nil {
		return nil, e
	}
	var saved int64
	e = a.transaction(ctx, func(tx *sql.Tx) error {
		r, e := one(ctx, tx, "SELECT * FROM alert_round WHERE id=? FOR UPDATE", rid)
		if e != nil {
			return e
		}
		u, e := one(ctx, tx, "SELECT * FROM sys_user WHERE id=? FOR UPDATE", user.I("id"))
		if e != nil {
			return e
		}
		if u == nil || u.S("account_status") != "ACTIVE" {
			return fail(403, "账号已停用")
		}
		opkey := entry + ":" + key
		old, e := one(ctx, tx, "SELECT * FROM alert_record WHERE alert_round_id=? AND operation_key=?", rid, opkey)
		if e != nil {
			return e
		}
		if old != nil {
			saved, e = a.record(ctx, tx, rid, u.I("id"), "USER", u.S("display_name"), kind, entry, opkey, content, Row{})
			return e
		}
		if r.I("open_marker") != 0 {
			return fail(409, "本轮已经结束，不能追加人工处理")
		}
		if kind == "LOG_HANDLED" && r.S("alert_type") != "LOG" {
			return fail(409, "指标告警只能由监控恢复")
		}
		saved, e = a.record(ctx, tx, rid, u.I("id"), "USER", u.S("display_name"), kind, entry, opkey, content, Row{})
		if e != nil {
			return e
		}
		patch := Row{"round_status": "PROCESSING"}
		if kind == "LOG_HANDLED" {
			now := time.Now().UnixMilli()
			patch = Row{"round_status": "HANDLED", "open_marker": rid, "handled_sys_user_id": u.I("id"), "result_text": content, "end_time": now, "end_record_time": now}
			if e = mustExec(ctx, tx, "UPDATE alert_ai_task SET execution_status='SKIPPED',active_marker=id,finished_time=?,error_code='HUMAN_CLOSED' WHERE alert_round_id=? AND execution_status='QUEUED'", now, rid); e != nil {
				return e
			}
		}
		if e = update(ctx, tx, "alert_round", rid, patch); e != nil {
			return e
		}
		if entry == "H5" {
			if _, e = a.newCard(ctx, tx, rid, 0, r.I("alert_group_id"), r.S("alert_type"), fmt.Sprintf("record:%d", saved), r.I("version")+1); e != nil {
				return e
			}
		}
		return a.touchRound(ctx, tx, rid)
	})
	return Row{"saved": e == nil, "record_id": fmt.Sprint(saved), "sync_status": "PENDING"}, e
}

// newAITask 创建唯一排队任务，执行器不得自动重跑。
func (a *App) newAITask(ctx context.Context, tx *sql.Tx, r Row) (int64, error) {
	task := Row{"alert_round_id": r.I("id"), "alert_project_id": r.I("alert_project_id"), "alert_project_workspace_id": 0, "execution_status": "QUEUED", "active_marker": 0, "workspace_version": 0, "phase": "WAITING", "business_result": "UNDETERMINED", "delivery_status": "PENDING", "delivery_set_status": "OPEN", "branch_name": "", "context_snapshot": "{}", "runner_image": "", "workspace_path": "", "executor_id": "", "execution_token_hash": "", "container_id": "", "container_name": "", "last_sequence": 0, "heartbeat_time": 0, "artifact_path": "", "error_code": "", "error_message": "", "queued_time": time.Now().UnixMilli(), "claimed_time": 0, "started_time": 0, "finished_time": 0}
	id, e := a.nextID()
	if e != nil {
		return 0, e
	}
	task["active_marker"] = id
	created, e := a.insert(ctx, tx, "alert_ai_task", task)
	if e != nil {
		return 0, e
	}
	e = update(ctx, tx, "alert_ai_task", created, Row{"active_marker": created})
	return created, e
}

// roundDetail 汇集查询所需事实；后台与 H5 共用投影，不复制告警状态。
func (a *App) roundDetail(ctx context.Context, rid int64) (Row, error) {
	r, e := one(ctx, a.DB, "SELECT r.*,p.title,p.grouping_data,g.name AS group_name,j.name AS project_name,s.name AS resource_name FROM alert_round r JOIN alert_problem p ON p.id=r.alert_problem_id JOIN alert_group g ON g.id=r.alert_group_id JOIN alert_project j ON j.id=r.alert_project_id JOIN alert_resource s ON s.id=r.alert_resource_id WHERE r.id=?", rid)
	if e != nil {
		return nil, e
	}
	if r == nil {
		return nil, fail(404, "告警不存在")
	}
	out := Row{"round": r}
	for key, query := range map[string]string{"records": "SELECT * FROM alert_record WHERE alert_round_id=? ORDER BY occurred_time,id", "events": "SELECT * FROM alert_event WHERE alert_round_id=? ORDER BY received_time DESC,id DESC LIMIT 100", "cards": "SELECT * FROM alert_card WHERE alert_round_id=? ORDER BY id", "ai_tasks": "SELECT * FROM alert_ai_task WHERE alert_round_id=?"} {
		rs, e := rows(ctx, a.DB, query, rid)
		if e != nil {
			return nil, e
		}
		for _, row := range rs {
			delete(row, "execution_token_hash")
		}
		out[key] = rs
	}
	mrs, e := rows(ctx, a.DB, "SELECT m.* FROM alert_merge_request m JOIN alert_ai_task_repo x ON x.id=m.alert_ai_task_repo_id JOIN alert_ai_task t ON t.id=x.alert_ai_task_id WHERE t.alert_round_id=? ORDER BY m.id", rid)
	if e != nil {
		return nil, e
	}
	out["merge_requests"] = mrs
	repos, e := rows(ctx, a.DB, "SELECT x.*,p.repo_code FROM alert_ai_task_repo x JOIN alert_project_repo p ON p.id=x.alert_project_repo_id JOIN alert_ai_task t ON t.id=x.alert_ai_task_id WHERE t.alert_round_id=? ORDER BY x.id", rid)
	if e != nil {
		return nil, e
	}
	out["task_repos"] = repos
	evidenceQuery := "SELECT * FROM alert_event WHERE alert_round_id=? ORDER BY occurred_time DESC,received_time DESC,id DESC LIMIT 1"
	if r.S("alert_type") == "METRIC" && r.S("round_status") == "RECOVERED" {
		evidenceQuery = "SELECT * FROM alert_event WHERE alert_round_id=? AND event_type='RESOLVED' ORDER BY received_time,id LIMIT 1"
	}
	latest, e := one(ctx, a.DB, evidenceQuery, rid)
	if e != nil {
		return nil, e
	}
	if latest != nil {
		table := "alert_log_detail"
		if r.S("alert_type") == "METRIC" {
			table = "alert_metric_detail"
		}
		d, e := one(ctx, a.DB, "SELECT * FROM "+table+" WHERE alert_event_id=?", latest.I("id"))
		if e != nil {
			return nil, e
		}
		out["evidence"] = d
	}
	out["all_merged"] = allMerged(out)
	return out, nil
}

// allMerged 必须同时验证封口、所有必要仓库与全部 MR，空集合不算全部合并。
func allMerged(detail Row) bool {
	tasks, _ := detail["ai_tasks"].([]Row)
	repos, _ := detail["task_repos"].([]Row)
	mrs, _ := detail["merge_requests"].([]Row)
	if len(tasks) != 1 || tasks[0].S("delivery_set_status") != "SEALED" || tasks[0].S("delivery_status") != "COMPLETE" {
		return false
	}
	required := 0
	for _, r := range repos {
		if r.S("scope_status") != "REQUIRED" {
			continue
		}
		if r.S("delivery_status") != "DELIVERED" || r.I("required_mr_count") < 1 {
			return false
		}
		count := 0
		for _, m := range mrs {
			if m.I("alert_ai_task_repo_id") == r.I("id") {
				if m.S("merge_status") != "MERGED" || m.S("merge_revision") == "" {
					return false
				}
				count++
			}
		}
		if int64(count) != r.I("required_mr_count") {
			return false
		}
		required += count
	}
	return required > 0
}

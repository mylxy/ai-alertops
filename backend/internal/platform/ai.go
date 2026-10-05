package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

// ClaimTask 在项目、工作区、轮次和任务锁内按 FIFO 领取一次任务。
// 返回的 execution_token 仅传给宿主执行器，不会通过查询 API 暴露。
func (a *App) ClaimTask(ctx context.Context, projectID, executor string) (Row, error) {
	pid, e := positiveID(projectID)
	if e != nil {
		return nil, e
	}
	if executor == "" {
		return nil, fail(400, "执行器身份不能为空")
	}
	var claimed Row
	e = a.transaction(ctx, func(tx *sql.Tx) error {
		claimed = nil
		project, e := one(ctx, tx, "SELECT * FROM alert_project WHERE id=? FOR UPDATE", pid)
		if e != nil {
			return e
		}
		if project == nil || project.S("config_status") != "ENABLED" {
			return nil
		}
		workspace, e := one(ctx, tx, "SELECT * FROM alert_project_workspace WHERE alert_project_id=? AND workspace_status='READY' AND active_alert_ai_task_id=0 ORDER BY slot_no LIMIT 1 FOR UPDATE", pid)
		if e != nil || workspace == nil {
			return e
		}
		task, e := one(ctx, tx, "SELECT * FROM alert_ai_task WHERE alert_project_id=? AND execution_status='QUEUED' ORDER BY queued_time,id LIMIT 1", pid)
		if e != nil || task == nil {
			return e
		}
		round, e := one(ctx, tx, "SELECT * FROM alert_round WHERE id=? FOR UPDATE", task.I("alert_round_id"))
		if e != nil {
			return e
		}
		task, e = one(ctx, tx, "SELECT * FROM alert_ai_task WHERE id=? FOR UPDATE", task.I("id"))
		if e != nil {
			return e
		}
		if task.S("execution_status") != "QUEUED" {
			return nil
		}
		if round.I("open_marker") != 0 {
			return update(ctx, tx, "alert_ai_task", task.I("id"), Row{"execution_status": "SKIPPED", "active_marker": task.I("id"), "finished_time": time.Now().UnixMilli(), "error_code": "HUMAN_CLOSED"})
		}
		repos, e := rows(ctx, tx, "SELECT * FROM alert_project_repo WHERE alert_project_id=? AND config_status='ENABLED' ORDER BY id", pid)
		if e != nil {
			return e
		}
		token := nonce() + nonce()
		version := workspace.I("assignment_version") + 1
		now := time.Now().UnixMilli()
		patch := Row{"alert_project_workspace_id": workspace.I("id"), "execution_status": "STARTING", "active_marker": 0, "workspace_version": version, "context_snapshot": encode(Row{"project": project, "repos": repos, "round": round}), "runner_image": project.S("runner_image"), "workspace_path": workspace.S("workspace_path"), "executor_id": executor, "execution_token_hash": digest(token), "container_name": "alertops-task-" + task.S("id"), "artifact_path": filepath.Join(a.Config.ArtifactRoot, task.S("id")), "claimed_time": now, "heartbeat_time": now}
		if e = update(ctx, tx, "alert_ai_task", task.I("id"), patch); e != nil {
			return e
		}
		if e = update(ctx, tx, "alert_project_workspace", workspace.I("id"), Row{"workspace_status": "OCCUPIED", "active_alert_ai_task_id": task.I("id"), "assignment_version": version}); e != nil {
			return e
		}
		claimed, e = one(ctx, tx, "SELECT * FROM alert_ai_task WHERE id=?", task.I("id"))
		if e != nil {
			return e
		}
		delete(claimed, "execution_token_hash")
		claimed["execution_token"] = token
		return nil
	})
	return claimed, e
}

// RepoProgress 是执行器报告的单仓库事实，不接受任意外部仓库路径。
type RepoProgress struct {
	Code                string `json:"repo_code"`            // 配置中的仓库编码。
	Base                string `json:"base_revision"`        // 从 origin/master 分出的基线。
	Head                string `json:"head_revision"`        // 已验证修复提交。
	Verification        string `json:"verification_status"`  // PASSED、FAILED、UNAVAILABLE。
	VerificationSummary string `json:"verification_summary"` // 测试结论。
	RequiredMRCount     int    `json:"required_mr_count"`    // 封口时必要 MR 数。
}

// Progress 是版本化业务事件；Codex 原生 JSONL 不直接转换为业务结论。
type Progress struct {
	Sequence       int64          `json:"sequence"`        // 从 1 开始的连续序号。
	Event          string         `json:"event"`           // 明确的业务事件类型。
	Summary        string         `json:"summary"`         // 有界业务说明。
	Repos          []RepoProgress `json:"repos"`           // 受影响仓库或阶段事实。
	BusinessResult string         `json:"business_result"` // FIX_PROPOSED、NO_ISSUE、UNRESOLVED。
}

// receiveProgress 以任务令牌鉴权并先持久化回调，再确认接收。
func (a *App) receiveProgress(ctx context.Context, tid int64, token string, p Progress) (Row, error) {
	if p.Sequence <= 0 || len(p.Summary) > 16000 || len(p.Repos) > 50 {
		return nil, fail(400, "AI 事件格式无效")
	}
	switch p.Event {
	case "EXECUTION_STARTED", "ROOT_CAUSE_LOCATED", "REPAIR_PROGRESS", "VERIFICATION_FINISHED", "EXECUTION_FINISHED", "EXECUTION_FAILED":
	default:
		return nil, fail(400, "不支持的 AI 业务事件")
	}
	task, e := one(ctx, a.DB, "SELECT * FROM alert_ai_task WHERE id=?", tid)
	if e != nil {
		return nil, e
	}
	if task == nil || !secretEqual(task.S("execution_token_hash"), digest(token)) {
		return nil, fail(401, "AI 任务令牌无效")
	}
	key := fmt.Sprintf("ai:%d:%d", tid, p.Sequence)
	e = a.transaction(ctx, func(tx *sql.Tx) error {
		return a.enqueue(ctx, tx, "AI_PROGRESS_APPLY", key, "TASK", tid, p.Sequence, p)
	})
	if e != nil {
		return nil, e
	}
	// 首次启动需要同步判定与人工关闭的竞争；CLI 在拿到确认前不得提交任务提示。
	if p.Event == "EXECUTION_STARTED" {
		job, e := one(ctx, a.DB, "SELECT * FROM alert_job WHERE job_key=?", key)
		if e != nil {
			return nil, e
		}
		if e = a.applyProgressJob(ctx, job); e != nil {
			return nil, e
		}
		if e = update(ctx, a.DB, "alert_job", job.I("id"), Row{"job_status": "SUCCEEDED"}); e != nil {
			return nil, e
		}
	}
	return Row{"accepted": true, "sequence": p.Sequence}, nil
}

// applyProgressJob 按连续序号锁定任务与原轮次，迟到事件不重开人工终态。
func (a *App) applyProgressJob(ctx context.Context, j Row) error {
	var p Progress
	if e := decodeJSON(j["payload"], &p); e != nil {
		return e
	}
	return a.transaction(ctx, func(tx *sql.Tx) error {
		ref, e := one(ctx, tx, "SELECT alert_round_id FROM alert_ai_task WHERE id=?", j.I("target_id"))
		if e != nil {
			return e
		}
		if ref == nil {
			return errors.New("AI 任务不存在")
		}
		r, e := one(ctx, tx, "SELECT * FROM alert_round WHERE id=? FOR UPDATE", ref.I("alert_round_id"))
		if e != nil {
			return e
		}
		task, e := one(ctx, tx, "SELECT * FROM alert_ai_task WHERE id=? FOR UPDATE", j.I("target_id"))
		if e != nil {
			return e
		}
		if p.Sequence <= task.I("last_sequence") {
			return nil
		}
		if p.Sequence != task.I("last_sequence")+1 {
			return fail(409, "等待前序 AI 事件")
		}
		state := task.S("execution_status")
		if state != "STARTING" && state != "RUNNING" && state != "FINALIZING" {
			return fail(409, "AI 任务已结束")
		}
		patch := Row{"last_sequence": p.Sequence, "heartbeat_time": time.Now().UnixMilli()}
		recordType := "AI_PROGRESS"
		switch p.Event {
		case "EXECUTION_STARTED":
			if state != "STARTING" || p.Sequence != 1 {
				return fail(409, "不允许重复启动 AI")
			}
			if r.I("open_marker") != 0 {
				if e = update(ctx, tx, "alert_ai_task", task.I("id"), Row{"execution_status": "FINALIZING", "error_code": "HUMAN_CLOSED", "error_message": "人工关闭先于 CLI 启动，停止执行"}); e != nil {
					return e
				}
				return nil
			}
			patch["execution_status"] = "RUNNING"
			patch["phase"] = "DIAGNOSING"
			patch["started_time"] = time.Now().UnixMilli()
			if r.S("round_status") == "PENDING" {
				if e = update(ctx, tx, "alert_round", r.I("id"), Row{"round_status": "PROCESSING"}); e != nil {
					return e
				}
			}
		case "ROOT_CAUSE_LOCATED":
			if state != "RUNNING" || p.Summary == "" || task.S("delivery_set_status") != "OPEN" {
				return fail(409, "定位报告不满足任务状态")
			}
			patch["phase"] = "LOCATED"
			branch := task.S("branch_name")
			if len(p.Repos) > 0 && branch == "" {
				zone := time.FixedZone("Asia/Shanghai", 8*3600)
				now := time.Now().In(zone)
				branch = fmt.Sprintf("hotfix-%s%03d-%s", now.Format("20060102-150405"), now.Nanosecond()/int(time.Millisecond), r.S("alert_no"))
				patch["branch_name"] = branch
			}
			for _, repo := range p.Repos {
				configured, e := one(ctx, tx, "SELECT * FROM alert_project_repo WHERE alert_project_id=? AND repo_code=?", task.I("alert_project_id"), repo.Code)
				if e != nil {
					return e
				}
				if configured == nil {
					return fail(400, "定位仓库不属于任务项目")
				}
				old, e := one(ctx, tx, "SELECT id FROM alert_ai_task_repo WHERE alert_ai_task_id=? AND alert_project_repo_id=?", task.I("id"), configured.I("id"))
				if e != nil {
					return e
				}
				if old != nil {
					continue
				}
				_, e = a.insert(ctx, tx, "alert_ai_task_repo", Row{"alert_ai_task_id": task.I("id"), "alert_project_repo_id": configured.I("id"), "scope_status": "REQUIRED", "base_revision": "", "head_revision": "", "branch_name": branch, "repair_status": "PENDING", "verification_status": "NOT_RUN", "delivery_status": "PENDING", "required_mr_count": 0, "verification_summary": "", "error_message": ""})
				if e != nil {
					return e
				}
			}
		case "REPAIR_PROGRESS", "VERIFICATION_FINISHED":
			if state != "RUNNING" {
				return fail(409, "任务未处于运行阶段")
			}
			patch["phase"] = "REPAIRING"
			if p.Event == "VERIFICATION_FINISHED" {
				patch["phase"] = "VERIFYING"
			}
			for _, rp := range p.Repos {
				repo, e := one(ctx, tx, "SELECT x.* FROM alert_ai_task_repo x JOIN alert_project_repo c ON c.id=x.alert_project_repo_id WHERE x.alert_ai_task_id=? AND c.repo_code=?", task.I("id"), rp.Code)
				if e != nil {
					return e
				}
				if repo == nil {
					return fail(400, "先登记受影响仓库再修改")
				}
				rpPatch := Row{"repair_status": "RUNNING"}
				if rp.Base != "" {
					if !revisionPattern.MatchString(rp.Base) {
						return fail(400, "基线提交格式错误")
					}
					if repo.S("base_revision") != "" && repo.S("base_revision") != rp.Base {
						return fail(409, "同任务不能改写分支基线")
					}
					rpPatch["base_revision"] = rp.Base
				}
				if rp.Head != "" {
					if !revisionPattern.MatchString(rp.Head) {
						return fail(400, "修复提交格式错误")
					}
					rpPatch["head_revision"] = rp.Head
				}
				if p.Event == "VERIFICATION_FINISHED" {
					if rp.Verification != "PASSED" && rp.Verification != "FAILED" && rp.Verification != "UNAVAILABLE" {
						return fail(400, "验证结论无效")
					}
					rpPatch["verification_status"] = rp.Verification
					rpPatch["verification_summary"] = rp.VerificationSummary
					rpPatch["repair_status"] = "DONE"
				}
				if e = update(ctx, tx, "alert_ai_task_repo", repo.I("id"), rpPatch); e != nil {
					return e
				}
			}
		case "EXECUTION_FINISHED":
			if state != "RUNNING" || p.Summary == "" {
				return fail(409, "缺少有效最终结果")
			}
			if p.BusinessResult != "FIX_PROPOSED" && p.BusinessResult != "NO_ISSUE" && p.BusinessResult != "UNRESOLVED" {
				return fail(400, "AI 最终业务结论无效")
			}
			repos, e := rows(ctx, tx, "SELECT x.*,c.repo_code FROM alert_ai_task_repo x JOIN alert_project_repo c ON c.id=x.alert_project_repo_id WHERE x.alert_ai_task_id=? AND x.scope_status='REQUIRED'", task.I("id"))
			if e != nil {
				return e
			}
			seen := map[string]RepoProgress{}
			for _, rp := range p.Repos {
				if _, ok := seen[rp.Code]; ok {
					return fail(400, "最终仓库列表重复")
				}
				seen[rp.Code] = rp
			}
			if len(seen) != len(repos) {
				return fail(409, "最终结果必须覆盖全部必要仓库")
			}
			delivery := "NOT_REQUIRED"
			if p.BusinessResult == "FIX_PROPOSED" {
				delivery = "COMPLETE"
				if len(repos) == 0 {
					return fail(400, "修复结论缺少必要仓库")
				}
			} else if len(repos) > 0 {
				delivery = "PARTIAL_FAILED"
			}
			for _, repo := range repos {
				rp, ok := seen[repo.S("repo_code")]
				if !ok || rp.RequiredMRCount < 1 || rp.RequiredMRCount > 100 {
					return fail(400, "必要仓库未明确 MR 数量")
				}
				count, e := one(ctx, tx, "SELECT COUNT(*) AS total FROM alert_merge_request WHERE alert_ai_task_repo_id=?", repo.I("id"))
				if e != nil {
					return e
				}
				repoDelivery := "DELIVERED"
				if count.I("total") != int64(rp.RequiredMRCount) || repo.S("verification_status") != "PASSED" || repo.S("base_revision") == "" || repo.S("head_revision") == "" {
					delivery = "PARTIAL_FAILED"
					repoDelivery = "FAILED"
				}
				if e = update(ctx, tx, "alert_ai_task_repo", repo.I("id"), Row{"required_mr_count": rp.RequiredMRCount, "delivery_status": repoDelivery}); e != nil {
					return e
				}
			}
			patch["delivery_set_status"] = "SEALED"
			patch["delivery_status"] = delivery
			patch["business_result"] = p.BusinessResult
			patch["phase"] = "FINISHED"
			patch["execution_status"] = "FINALIZING"
			recordType = "AI_RESULT"
		case "EXECUTION_FAILED":
			patch["execution_status"] = "FINALIZING"
			patch["error_code"] = "EXECUTION_FAILED"
			patch["error_message"] = p.Summary
			recordType = "AI_RESULT"
		}
		if e = update(ctx, tx, "alert_ai_task", task.I("id"), patch); e != nil {
			return e
		}
		if _, e = a.record(ctx, tx, r.I("id"), 0, "AI", "Codex", recordType, "RUNNER", fmt.Sprintf("ai:%d:%d", task.I("id"), p.Sequence), p.Summary, p); e != nil {
			return e
		}
		return a.touchRound(ctx, tx, r.I("id"))
	})
}

var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40,64}$`)

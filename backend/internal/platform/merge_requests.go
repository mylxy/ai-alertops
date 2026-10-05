package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// registerCodeup 接受校验过的 Codeup Hook 和执行器交付请求，全部先持久化。
func (a *App) registerCodeup(mux *http.ServeMux) {
	mux.HandleFunc("POST /hook/codeup", jsonRoute(func(w http.ResponseWriter, r *http.Request) (any, error) {
		if !secretEqual(r.Header.Get("X-Codeup-Token"), a.Config.CodeupHookToken) {
			return nil, fail(401, "Codeup Hook 鉴权失败")
		}
		var in struct {
			Kind       string `json:"object_kind"`
			Attributes Row    `json:"object_attributes"`
		}
		if e := jsonDecoder(r, &in); e != nil {
			return nil, e
		}
		if in.Kind != "merge_request" {
			return nil, fail(400, "仅接收合并请求事件")
		}
		attrs := in.Attributes
		repo := attrs.S("target_project_id")
		local := attrs.S("local_id")
		if local == "" {
			local = attrs.S("iid")
		}
		if _, e := positiveID(repo); e != nil {
			return nil, e
		}
		if _, e := positiveID(local); e != nil {
			return nil, e
		}
		configs, e := rows(r.Context(), a.DB, "SELECT organization_id FROM alert_project_repo WHERE repository_id=?", repo)
		if e != nil {
			return nil, e
		}
		if len(configs) == 0 {
			return nil, fail(400, "Codeup 仓库未配置")
		}
		org := configs[0].S("organization_id")
		for _, c := range configs {
			if c.S("organization_id") != org {
				return nil, fail(409, "仓库组织映射不唯一")
			}
		}
		payload := Row{"organization_id": org, "repository_id": repo, "local_id": local}
		key := "mr-hook:" + digest(encode([]any{org, repo, local, attrs}))
		e = a.transaction(r.Context(), func(tx *sql.Tx) error { return a.enqueue(r.Context(), tx, "MR_RECONCILE", key, "MR", 0, 0, payload) })
		return Row{"accepted": true}, e
	}))
	mux.HandleFunc("POST /api/ai/tasks/{id}/merge-requests", jsonRoute(func(w http.ResponseWriter, r *http.Request) (any, error) {
		task, e := a.taskToken(r)
		if e != nil {
			return nil, e
		}
		if task.S("execution_status") != "RUNNING" || task.S("delivery_set_status") != "OPEN" {
			return nil, fail(409, "当前任务不可新增交付")
		}
		var in struct {
			Code        string `json:"repo_code"`
			LocalID     string `json:"local_id"`
			Key         string `json:"request_key"`
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if e = bodyJSON(r, &in); e != nil {
			return nil, e
		}
		if in.Key == "" || len(in.Key) > 64 || len(in.Title) > 180 || len(in.Description) > 9000 {
			return nil, fail(400, "MR 请求格式无效")
		}
		repo, e := one(r.Context(), a.DB, "SELECT x.*,c.organization_id,c.repository_id,c.repo_code FROM alert_ai_task_repo x JOIN alert_project_repo c ON c.id=x.alert_project_repo_id WHERE x.alert_ai_task_id=? AND c.repo_code=?", task.I("id"), in.Code)
		if e != nil {
			return nil, e
		}
		if repo == nil || repo.S("base_revision") == "" || repo.S("head_revision") == "" || repo.S("verification_status") != "PASSED" {
			return nil, fail(409, "先登记分支基线、修复提交和通过的验证结果")
		}
		if in.LocalID != "" {
			if _, e = positiveID(in.LocalID); e != nil {
				return nil, e
			}
		}
		payload := Row{"task_repo_id": repo.S("id"), "organization_id": repo.S("organization_id"), "repository_id": repo.S("repository_id"), "local_id": in.LocalID, "request_key": in.Key, "title": in.Title, "description": in.Description}
		key := fmt.Sprintf("mr-create:%s:%s", repo.S("id"), in.Key)
		e = a.transaction(r.Context(), func(tx *sql.Tx) error {
			return a.enqueue(r.Context(), tx, "MR_RECONCILE", key, "TASK", task.I("id"), 0, payload)
		})
		return Row{"accepted": true, "job_key": key}, e
	}))
}

// jsonDecoder 允许来源 Hook 携带额外官方字段，同时保持数字标识精度。
func jsonDecoder(r *http.Request, out any) error {
	d := json.NewDecoder(r.Body)
	d.UseNumber()
	if e := d.Decode(out); e != nil {
		return fail(400, "Hook JSON 无效")
	}
	return nil
}

// reconcileMR 先查权威外部状态，再锁原轮次更新；早到 Hook 等待明确关联。
func (a *App) reconcileMR(ctx context.Context, j Row) error {
	var p Row
	if e := decodeJSON(j["payload"], &p); e != nil {
		return e
	}
	org, repo, local := p.S("organization_id"), p.S("repository_id"), p.S("local_id")
	var taskRepo Row
	var e error
	if p.I("task_repo_id") > 0 {
		taskRepo, e = one(ctx, a.DB, "SELECT x.*,t.alert_round_id,t.execution_status,t.delivery_set_status FROM alert_ai_task_repo x JOIN alert_ai_task t ON t.id=x.alert_ai_task_id WHERE x.id=?", p.I("task_repo_id"))
		if e != nil {
			return e
		}
		if taskRepo == nil {
			return errors.New("必要仓库关联不存在")
		}
	} else {
		taskRepo, e = one(ctx, a.DB, "SELECT x.*,t.alert_round_id,t.execution_status,t.delivery_set_status FROM alert_merge_request m JOIN alert_ai_task_repo x ON x.id=m.alert_ai_task_repo_id JOIN alert_ai_task t ON t.id=x.alert_ai_task_id WHERE m.organization_id=? AND m.repository_id=? AND m.display_number=?", org, repo, local)
		if e != nil {
			return e
		}
		if taskRepo == nil {
			return errors.New("等待 AI 报告可验证 MR 关联")
		}
	}
	return a.withExternalLock(ctx, "mr:"+org+":"+repo+":"+taskRepo.S("id"), func() error {
		var external Row
		var err error
		if local == "" {
			marker := "[AlertOps:" + taskRepo.S("id") + ":" + p.S("request_key") + "]"
			external, err = a.Codeup.Find(ctx, org, repo, marker)
			if err != nil {
				return err
			}
			if external == nil {
				freshJob, e := one(ctx, a.DB, "SELECT effect_state FROM alert_job WHERE id=?", j.I("id"))
				if e != nil {
					return e
				}
				if freshJob.S("effect_state") == "REQUESTED" {
					return errors.New("MR 创建结果待核对：尚未查到原请求，禁止重复创建")
				}
				if taskRepo.S("delivery_set_status") != "OPEN" || taskRepo.S("execution_status") != "RUNNING" {
					return errors.New("任务已封口，不能新建 MR")
				}
				// 提交意图必须先持久化；即使进程在响应前退出，也只能查找原请求。
				if err = update(ctx, a.DB, "alert_job", j.I("id"), Row{"effect_state": "REQUESTED"}); err != nil {
					return err
				}
				external, err = a.Codeup.Create(ctx, org, repo, taskRepo.S("branch_name"), p.S("title")+" "+marker, p.S("description"))
				if err != nil {
					return err
				}
			}
			local = external.S("localId")
		} else {
			external, err = a.Codeup.Get(ctx, org, repo, local)
			if err != nil {
				return err
			}
		}
		patch, err := verifiedMR(external, org, repo, local, taskRepo)
		if err != nil {
			return err
		}
		return a.transaction(ctx, func(tx *sql.Tx) error {
			r, e := one(ctx, tx, "SELECT * FROM alert_round WHERE id=? FOR UPDATE", taskRepo.I("alert_round_id"))
			if e != nil {
				return e
			}
			old, e := one(ctx, tx, "SELECT * FROM alert_merge_request WHERE provider='CODEUP' AND organization_id=? AND repository_id=? AND merge_request_id=? FOR UPDATE", org, repo, local)
			if e != nil {
				return e
			}
			if old != nil && old.I("alert_ai_task_repo_id") != taskRepo.I("id") {
				return fail(409, "同一 MR 不能关联其他修复任务")
			}
			if old != nil && old.S("merge_status") == "MERGED" && patch.S("merge_status") != "MERGED" {
				return nil
			}
			if old != nil && old.I("provider_update_time") > patch.I("provider_update_time") {
				return nil
			}
			changed := old == nil || old.S("merge_status") != patch.S("merge_status") || old.S("review_status") != patch.S("review_status") || old.S("head_revision") != patch.S("head_revision")
			if old == nil {
				patch["alert_ai_task_repo_id"] = taskRepo.I("id")
				patch["provider"] = "CODEUP"
				patch["organization_id"] = org
				patch["repository_id"] = repo
				patch["merge_request_id"] = local
				if _, e = a.insert(ctx, tx, "alert_merge_request", patch); e != nil {
					return e
				}
			} else {
				if e = update(ctx, tx, "alert_merge_request", old.I("id"), patch); e != nil {
					return e
				}
			}
			deliveryChanged, e := a.recalculateDelivery(ctx, tx, taskRepo.I("alert_ai_task_id"))
			if e != nil {
				return e
			}
			if !changed {
				if deliveryChanged {
					return a.touchRound(ctx, tx, r.I("id"))
				}
				return nil
			}
			key := "mr:" + digest(encode([]any{org, repo, local, patch.S("merge_status"), patch.S("review_status"), patch.S("head_revision"), patch.I("provider_update_time")}))
			if _, e = a.record(ctx, tx, r.I("id"), 0, "SYSTEM", "Codeup", "MR_PROGRESS", "HOOK", key, fmt.Sprintf("合并请求 #%s：%s；评审：%s", local, patch.S("merge_status"), patch.S("review_status")), patch); e != nil {
				return e
			}
			return a.touchRound(ctx, tx, r.I("id"))
		})
	})
}

// verifiedMR 严格核对组织、仓库、分支、外部 ID 和提交，评审意见独立保存。
func verifiedMR(external Row, org, repo, local string, taskRepo Row) (Row, error) {
	if external.S("localId") != local || external.S("targetProjectId") != repo || external.S("sourceProjectId") != repo || external.S("sourceBranch") != taskRepo.S("branch_name") || external.S("targetBranch") != "master" {
		return nil, fail(409, "MR 归属、分支或外部编号核对失败")
	}
	link := external.S("webUrl")
	if link == "" {
		link = external.S("detailUrl")
	}
	u, e := url.Parse(link)
	if e != nil || u.Scheme != "https" || u.Host != "codeup.aliyun.com" || u.User != nil || !strings.HasPrefix(u.Path, "/"+org+"/") {
		return nil, fail(409, "MR 页面地址不属于可信 Codeup 组织")
	}
	status := external.S("status")
	merge := "OPEN"
	switch status {
	case "MERGED":
		merge = "MERGED"
		if !revisionPattern.MatchString(external.S("mergedRevision")) {
			return nil, errors.New("合并状态缺少有效提交")
		}
	case "CLOSED":
		merge = "CLOSED"
	case "UNDER_DEV", "UNDER_REVIEW", "TO_BE_MERGED":
	default:
		return nil, errors.New("Codeup 返回未知 MR 状态")
	}
	head := external.S("headRevision")

	if !revisionPattern.MatchString(head) || head != taskRepo.S("head_revision") {
		return nil, errors.New("MR 源提交与已验证修复提交不一致")
	}
	review := "PENDING"
	reviewers, _ := external["reviewers"].([]any)
	if len(reviewers) > 0 {
		review = "APPROVED"
		for _, item := range reviewers {
			r, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("评审响应格式错误")
			}
			v := Row(r).S("reviewOpinionStatus")
			if v == "NOT_PASS" {
				review = "REJECTED"
				break
			}
			if v != "PASS" {
				review = "PENDING"
			}
		}
	}
	updated, e := time.Parse(time.RFC3339Nano, external.S("updateTime"))
	if e != nil {
		return nil, errors.New("MR 更新时间无效")
	}
	merged := int64(0)
	if merge == "MERGED" {
		merged = updated.UnixMilli()
	}
	return Row{"display_number": local, "url": link, "title": external.S("title"), "source_branch": taskRepo.S("branch_name"), "target_branch": "master", "head_revision": head, "merge_revision": external.S("mergedRevision"), "merge_status": merge, "review_status": review, "provider_status": status, "provider_update_time": updated.UnixMilli(), "last_verified_time": time.Now().UnixMilli(), "merged_time": merged}, nil
}

// recalculateDelivery 按已封口的必要集合重算迟到关联，不把验证失败或缺仓库掩盖成完整交付。
func (a *App) recalculateDelivery(ctx context.Context, tx *sql.Tx, tid int64) (bool, error) {
	task, e := one(ctx, tx, "SELECT * FROM alert_ai_task WHERE id=? FOR UPDATE", tid)
	if e != nil {
		return false, e
	}
	if task.S("delivery_set_status") != "SEALED" {
		return false, nil
	}
	repos, e := rows(ctx, tx, "SELECT x.*,(SELECT COUNT(*) FROM alert_merge_request m WHERE m.alert_ai_task_repo_id=x.id) AS mr_count FROM alert_ai_task_repo x WHERE x.alert_ai_task_id=? AND x.scope_status='REQUIRED'", tid)
	if e != nil {
		return false, e
	}
	if len(repos) == 0 {
		return false, nil
	}
	complete := task.S("business_result") == "FIX_PROPOSED"
	changed := false
	for _, repo := range repos {
		state := "FAILED"
		if repo.I("required_mr_count") > 0 && repo.I("mr_count") == repo.I("required_mr_count") && repo.S("verification_status") == "PASSED" && repo.S("base_revision") != "" && repo.S("head_revision") != "" {
			state = "DELIVERED"
		} else {
			complete = false
		}
		if state != repo.S("delivery_status") {
			changed = true
			if e = update(ctx, tx, "alert_ai_task_repo", repo.I("id"), Row{"delivery_status": state}); e != nil {
				return false, e
			}
		}
	}
	status := "PARTIAL_FAILED"
	if complete {
		status = "COMPLETE"
	}
	if status != task.S("delivery_status") {
		changed = true
		if e = update(ctx, tx, "alert_ai_task", tid, Row{"delivery_status": status}); e != nil {
			return false, e
		}
	}
	return changed, nil
}

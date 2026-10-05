package platform

import (
	"net/http"
	"strings"
)

// taskToken 验证仅属于当前任务的执行令牌，不能访问其他告警任务。
func (a *App) taskToken(r *http.Request) (Row, error) {
	id, e := positiveID(r.PathValue("id"))
	if e != nil {
		return nil, e
	}
	task, e := one(r.Context(), a.DB, "SELECT * FROM alert_ai_task WHERE id=?", id)
	if e != nil {
		return nil, e
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if task == nil || token == "" || !secretEqual(task.S("execution_token_hash"), digest(token)) {
		return nil, fail(401, "AI 任务令牌无效")
	}
	return task, nil
}

// registerAI 公开只读任务查询；执行回调和状态查询使用每任务独立令牌。
func (a *App) registerAI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tasks", a.authorized(false, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
		items, e := rows(r.Context(), a.DB, "SELECT t.id,t.alert_round_id,t.alert_project_id,t.execution_status,t.phase,t.queued_time,t.started_time,t.finished_time,t.error_message,t.branch_name,r.alert_no,p.name AS project_name FROM alert_ai_task t JOIN alert_round r ON r.id=t.alert_round_id JOIN alert_project p ON p.id=t.alert_project_id ORDER BY t.queued_time DESC,t.id DESC LIMIT 100")
		return Row{"items": items}, e
	}))
	mux.HandleFunc("POST /hook/ai/{id}", jsonRoute(func(w http.ResponseWriter, r *http.Request) (any, error) {
		task, e := a.taskToken(r)
		if e != nil {
			return nil, e
		}
		var p Progress
		if e = bodyJSON(r, &p); e != nil {
			return nil, e
		}
		result, e := a.receiveProgress(r.Context(), task.I("id"), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), p)
		if e != nil {
			return nil, e
		}
		if p.Event == "EXECUTION_STARTED" {
			fresh, e := one(r.Context(), a.DB, "SELECT execution_status FROM alert_ai_task WHERE id=?", task.I("id"))
			if e != nil {
				return nil, e
			}
			if fresh.S("execution_status") != "RUNNING" {
				return nil, fail(409, "人工关闭先于启动，执行器必须退出")
			}
		}
		return result, nil
	}))
	mux.HandleFunc("GET /api/ai/tasks/{id}", jsonRoute(func(w http.ResponseWriter, r *http.Request) (any, error) {
		task, e := a.taskToken(r)
		if e != nil {
			return nil, e
		}
		delete(task, "execution_token_hash")
		repos, e := rows(r.Context(), a.DB, "SELECT x.*,c.repo_code,c.relative_path,c.organization_id,c.repository_id,c.git_url FROM alert_ai_task_repo x JOIN alert_project_repo c ON c.id=x.alert_project_repo_id WHERE x.alert_ai_task_id=? ORDER BY x.id", task.I("id"))
		if e != nil {
			return nil, e
		}
		jobs, e := rows(r.Context(), a.DB, "SELECT job_key,job_status,last_error FROM alert_job WHERE job_type='MR_RECONCILE' AND target_type='TASK' AND target_id=?", task.I("id"))
		if e != nil {
			return nil, e
		}
		mrs, e := rows(r.Context(), a.DB, "SELECT m.* FROM alert_merge_request m JOIN alert_ai_task_repo x ON x.id=m.alert_ai_task_repo_id WHERE x.alert_ai_task_id=?", task.I("id"))
		if e != nil {
			return nil, e
		}
		return Row{"task": task, "repos": repos, "delivery_jobs": jobs, "merge_requests": mrs}, nil
	}))
}

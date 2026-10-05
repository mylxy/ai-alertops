package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// configField 声明服务端可编辑字段与合法值；不是任意表写入通道。
type configField struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Options  []string `json:"options,omitempty"`
	Optional bool     `json:"optional"`
}

// configuration 明确允许管理员维护的实体；历史事实不能经过此入口修改。
var configuration = map[string][]configField{
	"alert_project":           {{"project_code", "项目编码", "text", nil, false}, {"name", "名称", "text", nil, false}, {"workspace_root", "工作区父目录", "text", nil, false}, {"runner_image", "固定镜像版本", "text", nil, false}, {"credential_ref", "运行凭据环境变量名", "text", nil, true}, {"config_status", "状态", "select", []string{"ENABLED", "DISABLED"}, false}},
	"alert_project_workspace": {{"alert_project_id", "项目 ID", "id", nil, false}, {"slot_no", "槽位", "select", []string{"1", "2", "3"}, false}, {"workspace_path", "固定目录", "text", nil, false}},
	"alert_project_repo":      {{"alert_project_id", "项目 ID", "id", nil, false}, {"repo_code", "仓库编码", "text", nil, false}, {"relative_path", "工作区内相对目录", "text", nil, false}, {"provider", "托管平台", "select", []string{"CODEUP"}, false}, {"organization_id", "Codeup 组织 ID", "text", nil, false}, {"repository_id", "Codeup 仓库 ID", "text", nil, false}, {"git_url", "Git URL", "text", nil, false}, {"base_branch", "基线分支", "select", []string{"master"}, false}, {"credential_ref", "凭据环境变量名", "text", nil, true}, {"config_status", "状态", "select", []string{"ENABLED", "DISABLED"}, false}},
	"alert_source":            {{"source_code", "来源编码", "text", nil, false}, {"source_type", "类型", "select", []string{"LOG", "METRIC"}, false}, {"environment", "环境", "text", nil, false}, {"adapter_type", "适配器", "select", []string{"SLS", "ALERTMANAGER"}, false}, {"auth_ref", "Hook 密钥环境变量名", "text", nil, false}, {"config_status", "状态", "select", []string{"ENABLED", "DISABLED"}, false}},
	"alert_resource":          {{"alert_source_id", "来源 ID", "id", nil, false}, {"alert_project_id", "项目 ID", "id", nil, false}, {"resource_type", "资源类型", "select", []string{"SERVICE", "INSTANCE"}, false}, {"resource_key", "来源内资源键", "text", nil, false}, {"name", "显示名称", "text", nil, false}, {"config_status", "状态", "select", []string{"ENABLED", "DISABLED"}, false}},
	"alert_group":             {{"conversation_id", "openConversationId", "text", nil, false}, {"name", "群名称", "text", nil, false}, {"config_status", "状态", "select", []string{"ENABLED", "DISABLED"}, false}},
	"alert_route":             {{"alert_resource_id", "资源 ID", "id", nil, false}, {"alert_group_id", "固定群 ID", "id", nil, false}, {"alert_type", "类型", "select", []string{"LOG", "METRIC", "NOTICE"}, false}, {"config_status", "状态", "select", []string{"ENABLED", "DISABLED"}, false}},
	"sys_user":                {{"role", "权限", "select", []string{"USER", "ADMIN"}, false}, {"account_status", "账号状态", "select", []string{"ACTIVE", "DISABLED"}, false}},
}
var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,100}$`)

// registerAdmin 管理员配置、账号和审计接口；配置值不接受明文密钥。
func (a *App) registerAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/config", a.authorized(true, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
		out := Row{"fields": configuration}
		for table := range configuration {
			items, e := rows(r.Context(), a.DB, "SELECT * FROM "+table+" ORDER BY id")
			if e != nil {
				return nil, e
			}
			out[table] = items
		}
		return out, nil
	}))
	mux.HandleFunc("PUT /api/admin/config/{entity}", a.authorized(true, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
		var in Row
		if e := bodyJSON(r, &in); e != nil {
			return nil, e
		}
		return a.saveConfig(r.Context(), u, r.PathValue("entity"), in)
	}))
	mux.HandleFunc("POST /api/admin/workspaces/{id}/check", a.authorized(true, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
		id, e := positiveID(r.PathValue("id"))
		if e != nil {
			return nil, e
		}
		return a.checkWorkspace(r.Context(), u, id)
	}))
	mux.HandleFunc("GET /api/admin/audit", a.authorized(true, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
		items, e := rows(r.Context(), a.DB, "SELECT * FROM sys_audit_log ORDER BY occurred_time DESC,id DESC LIMIT 100")
		return Row{"items": items}, e
	}))
	mux.HandleFunc("GET /api/jobs", a.authorized(false, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
		items, e := rows(r.Context(), a.DB, "SELECT id,job_type,target_type,target_id,job_status,attempt_count,next_run_time,last_error FROM alert_job WHERE job_status<>'SUCCEEDED' ORDER BY create_time,id LIMIT 100")
		return Row{"items": items}, e
	}))
	if a.Config.Demo {
		mux.HandleFunc("GET /api/demo/messages", a.authorized(false, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
			d, ok := a.Messenger.(*DemoMessenger)
			if !ok {
				return nil, fail(404, "没有演示消息服务")
			}
			return d.Snapshot(), nil
		}))
	}
}

// saveConfig 使用字段白名单、关联校验、项目空闲条件及审计事务保存配置。
func (a *App) saveConfig(ctx context.Context, user Row, table string, in Row) (Row, error) {
	fields, ok := configuration[table]
	if !ok {
		return nil, fail(404, "不支持的配置类型")
	}
	allowed := map[string]bool{"id": true}
	patch := Row{}
	for _, f := range fields {
		allowed[f.Name] = true
		value := strings.TrimSpace(in.S(f.Name))
		if value == "" && !f.Optional {
			return nil, fail(400, f.Label+"不能为空")
		}
		if len(value) > 700 {
			return nil, fail(400, f.Label+"过长")
		}
		if f.Options != nil {
			valid := false
			for _, v := range f.Options {
				valid = valid || v == value
			}
			if !valid {
				return nil, fail(400, f.Label+"取值无效")
			}
		}
		if f.Kind == "id" {
			id, e := positiveID(value)
			if e != nil {
				return nil, e
			}
			patch[f.Name] = id
		} else {
			patch[f.Name] = value
		}
		if strings.HasSuffix(f.Name, "_ref") && value != "" && !envName.MatchString(value) {
			return nil, fail(400, "凭据只允许填写环境变量名称")
		}
	}
	for k := range in {
		if !allowed[k] {
			return nil, fail(400, "不允许修改字段："+k)
		}
	}
	id := int64(0)
	if in.S("id") != "" && in.S("id") != "0" {
		var e error
		id, e = positiveID(in.S("id"))
		if e != nil {
			return nil, e
		}
	}
	if table == "sys_user" && id == 0 {
		return nil, fail(400, "用户必须通过钉钉登录建立")
	}
	var saved Row
	err := a.withExternalLock(ctx, "configuration", func() error {
		return a.transaction(ctx, func(tx *sql.Tx) error {
			var old Row
			var e error
			if id > 0 {
				old, e = one(ctx, tx, "SELECT * FROM "+table+" WHERE id=? FOR UPDATE", id)
				if e != nil {
					return e
				}
				if old == nil {
					return fail(404, "配置不存在")
				}
			}
			pid := patch.I("alert_project_id")
			if table == "alert_project" {
				pid = id
			}
			if old != nil && old.I("alert_project_id") != 0 && old.I("alert_project_id") != pid {
				return fail(409, "已有配置不能更换所属项目")
			}
			if pid > 0 {
				project, e := one(ctx, tx, "SELECT * FROM alert_project WHERE id=? FOR UPDATE", pid)
				if e != nil {
					return e
				}
				if project == nil {
					return fail(400, "项目不存在")
				}
				busy, e := one(ctx, tx, "SELECT id FROM alert_ai_task WHERE alert_project_id=? AND execution_status IN ('STARTING','RUNNING','FINALIZING') LIMIT 1", pid)
				if e != nil {
					return e
				}
				if busy != nil {
					return fail(409, "项目有活动任务，暂不能修改配置")
				}
			}
			for field, target := range map[string]string{"alert_source_id": "alert_source", "alert_resource_id": "alert_resource", "alert_group_id": "alert_group"} {
				if patch.I(field) > 0 {
					r, e := one(ctx, tx, "SELECT id FROM "+target+" WHERE id=?", patch.I(field))
					if e != nil {
						return e
					}
					if r == nil {
						return fail(400, "关联配置不存在："+field)
					}
				}
			}
			if e = a.validateConfig(ctx, tx, table, id, old, patch); e != nil {
				return e
			}
			if id == 0 {
				id, e = a.insert(ctx, tx, table, patch)
			} else {
				e = update(ctx, tx, table, id, patch)
			}
			if e != nil {
				return e
			}
			saved, e = one(ctx, tx, "SELECT * FROM "+table+" WHERE id=?", id)
			if e != nil {
				return e
			}
			return a.audit(ctx, tx, user, "CONFIG_SAVE", table, id, old, saved)
		})
	})
	return saved, err
}

// validateConfig 校验固定路由与物理工作区，运行状态由系统维护。
func (a *App) validateConfig(ctx context.Context, tx *sql.Tx, table string, id int64, old, patch Row) error {
	// 身份与来源归属一旦创建即不可换壳，防止历史事件和旧 MR 关联到新配置。
	immutable := map[string][]string{
		"alert_source":       {"source_code", "source_type", "environment", "adapter_type"},
		"alert_group":        {"conversation_id"},
		"alert_resource":     {"alert_source_id", "alert_project_id", "resource_type", "resource_key"},
		"alert_route":        {"alert_resource_id", "alert_group_id", "alert_type"},
		"alert_project_repo": {"alert_project_id", "repo_code", "provider", "organization_id", "repository_id", "git_url", "base_branch"},
		"alert_project":      {"project_code"},
	}
	if old != nil {
		for _, field := range immutable[table] {
			if old.S(field) != patch.S(field) {
				return fail(409, "已有配置的身份字段不可更换："+field)
			}
		}
	}
	switch table {
	case "sys_user":
		if old.S("role") == "ADMIN" && (patch.S("role") != "ADMIN" || patch.S("account_status") != "ACTIVE") {
			admins, e := rows(ctx, tx, "SELECT id FROM sys_user WHERE role='ADMIN' AND account_status='ACTIVE' FOR UPDATE")
			if e != nil {
				return e
			}
			if len(admins) <= 1 {
				return fail(409, "至少保留一个可用管理员")
			}
		}
	case "alert_source":
		if patch.S("source_type") == "LOG" && patch.S("adapter_type") != "SLS" || patch.S("source_type") == "METRIC" && patch.S("adapter_type") != "ALERTMANAGER" {
			return fail(400, "来源类型与适配器不匹配")
		}
	case "alert_group":
		if id == 0 {
			patch["corp_id"] = a.Config.CorpID
			patch["app_id"] = a.Config.AppID
			patch["integration_status"] = "UNVERIFIED"
			patch["last_error"] = ""
		}
	case "alert_resource":
		s, e := one(ctx, tx, "SELECT source_type FROM alert_source WHERE id=?", patch.I("alert_source_id"))
		if e != nil {
			return e
		}
		if s.S("source_type") == "LOG" && patch.S("resource_type") != "SERVICE" || s.S("source_type") == "METRIC" && patch.S("resource_type") != "INSTANCE" {
			return fail(400, "资源类型与来源不匹配")
		}
	case "alert_route":
		s, e := one(ctx, tx, "SELECT resource_type FROM alert_resource WHERE id=?", patch.I("alert_resource_id"))
		if e != nil {
			return e
		}
		if s.S("resource_type") == "INSTANCE" && patch.S("alert_type") != "METRIC" || s.S("resource_type") == "SERVICE" && patch.S("alert_type") == "METRIC" {
			return fail(400, "路由类型与资源不匹配")
		}
	case "alert_project":
		path, e := canonicalDirectory(patch.S("workspace_root"))
		if e != nil {
			return e
		}
		patch["workspace_root"] = path
		others, e := rows(ctx, tx, "SELECT id,workspace_root FROM alert_project WHERE id<>?", id)
		if e != nil {
			return e
		}
		for _, o := range others {
			if overlaps(path, o.S("workspace_root")) {
				return fail(409, "项目工作目录不能重叠")
			}
		}
		if strings.HasSuffix(patch.S("runner_image"), ":latest") {
			return fail(400, "运行镜像应固定版本或摘要")
		}
	case "alert_project_workspace":
		if old != nil && old.I("active_alert_ai_task_id") != 0 {
			return fail(409, "工作区仍有未确认停止的任务")
		}
		path, e := canonicalDirectory(patch.S("workspace_path"))
		if e != nil {
			return e
		}
		project, e := one(ctx, tx, "SELECT workspace_root FROM alert_project WHERE id=?", patch.I("alert_project_id"))
		if e != nil {
			return e
		}
		if !within(project.S("workspace_root"), path) {
			return fail(400, "工作区必须位于项目根目录下")
		}
		others, e := rows(ctx, tx, "SELECT id,workspace_path FROM alert_project_workspace WHERE id<>?", id)
		if e != nil {
			return e
		}
		for _, o := range others {
			if overlaps(path, o.S("workspace_path")) {
				return fail(409, "固定工作区不能相同、嵌套或共享")
			}
		}
		patch["workspace_path"] = path
		patch["workspace_status"] = "BLOCKED"
		patch["blocked_reason"] = "配置已保存，等待目录检查"
		patch["last_check_time"] = 0
		if id == 0 {
			patch["active_alert_ai_task_id"] = 0
			patch["assignment_version"] = 0
		}
	case "alert_project_repo":
		relative := patch.S("relative_path")
		if filepath.IsAbs(relative) || relative == "." || filepath.Clean(relative) != relative || strings.HasPrefix(relative, "..") {
			return fail(400, "仓库相对路径无效")
		}
		other, e := rows(ctx, tx, "SELECT relative_path FROM alert_project_repo WHERE alert_project_id=? AND id<>?", patch.I("alert_project_id"), id)
		if e != nil {
			return e
		}
		for _, o := range other {
			if overlaps(relative, o.S("relative_path")) {
				return fail(409, "仓库目录不能重叠")
			}
		}
		gitURL := patch.S("git_url")
		if strings.HasPrefix(gitURL, "https://") {
			u, e := url.Parse(gitURL)
			if e != nil || u.Hostname() != "codeup.aliyun.com" || u.User != nil || u.RawQuery != "" {
				return fail(400, "Git 地址必须是无凭据的 Codeup HTTPS URL")
			}
		} else if !strings.HasPrefix(gitURL, "git@codeup.aliyun.com:") {
			return fail(400, "仅支持 Codeup HTTPS 或 SSH 地址")
		}
	}
	return nil
}

// audit 与配置变更同事务保存不含密钥的前后快照。
func (a *App) audit(ctx context.Context, tx *sql.Tx, u Row, action, table string, id int64, before, after any) error {
	_, e := a.insert(ctx, tx, "sys_audit_log", Row{"actor_sys_user_id": u.I("id"), "actor_type": "USER", "action_type": action, "target_type": table, "target_id": id, "request_key": nonce(), "before_data": encode(before), "after_data": encode(after), "result_status": "SUCCESS", "occurred_time": time.Now().UnixMilli()})
	return e
}

// canonicalDirectory 拒绝符号链接别名，保证配置目录就是实际可写目录。
func canonicalDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fail(400, "目录必须为绝对路径")
	}
	clean := filepath.Clean(path)
	real, e := filepath.EvalSymlinks(clean)
	if e != nil {
		return "", fail(400, "目录不存在或不可读取")
	}
	if clean != real {
		return "", fail(400, "工作目录不能通过符号链接别名共享")
	}
	st, e := os.Stat(real)
	if e != nil || !st.IsDir() {
		return "", fail(400, "工作目录不是目录")
	}
	return real, nil
}

// within 判断严格位于父目录中，防止路径前缀混淆。
func within(parent, child string) bool {
	rel, e := filepath.Rel(parent, child)
	return e == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// overlaps 检查同目录或父子嵌套。
func overlaps(a, b string) bool { return a == b || within(a, b) || within(b, a) }

// workspaceHealth 只读取目录与 Git 状态，不自动清理或覆盖人工现场。
func workspaceHealth(ctx context.Context, path string, repos []Row) error {
	if _, e := canonicalDirectory(path); e != nil {
		return e
	}
	if len(repos) == 0 {
		return errors.New("项目尚未配置仓库")
	}
	for _, repo := range repos {
		full := filepath.Join(path, repo.S("relative_path"))
		real, e := canonicalDirectory(full)
		if e != nil {
			return e
		}
		if !within(path, real) {
			return errors.New("仓库逃逸工作区")
		}
		st, e := os.Lstat(filepath.Join(full, ".git"))
		if e != nil || !st.IsDir() {
			return errors.New("仓库必须是独立 clone，不能是 worktree 或共享 .git")
		}
		status, e := gitProbe(ctx, full, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=all")
		if e != nil {
			return errors.New("无法检查仓库状态")
		}
		if len(status) > 0 {
			return errors.New("仓库包含未提交文件，保留现场并阻塞该工作区")
		}
	}
	return nil
}

// checkWorkspace 仅在确认无占用时解除阻塞，其他工作区不受影响。
func (a *App) checkWorkspace(ctx context.Context, u Row, id int64) (Row, error) {
	var saved Row
	err := a.withExternalLock(ctx, "configuration", func() error {
		return a.transaction(ctx, func(tx *sql.Tx) error {
			w, e := one(ctx, tx, "SELECT * FROM alert_project_workspace WHERE id=? FOR UPDATE", id)
			if e != nil {
				return e
			}
			if w == nil {
				return fail(404, "工作区不存在")
			}
			if w.I("active_alert_ai_task_id") != 0 {
				return fail(409, "旧任务尚未确认退出，不能解除占用")
			}
			repos, e := rows(ctx, tx, "SELECT * FROM alert_project_repo WHERE alert_project_id=? AND config_status='ENABLED'", w.I("alert_project_id"))
			if e != nil {
				return e
			}
			state, reason := "READY", ""
			if e = workspaceHealth(ctx, w.S("workspace_path"), repos); e != nil {
				state, reason = "BLOCKED", truncate(e.Error(), 240)
			}
			if e = update(ctx, tx, "alert_project_workspace", id, Row{"workspace_status": state, "blocked_reason": reason, "last_check_time": time.Now().UnixMilli()}); e != nil {
				return e
			}
			saved, e = one(ctx, tx, "SELECT * FROM alert_project_workspace WHERE id=?", id)
			if e != nil {
				return e
			}
			return a.audit(ctx, tx, u, "WORKSPACE_CHECK", "alert_project_workspace", id, w, saved)
		})
	})
	return saved, err
}

// configError 让配置约束冲突成为可诊断业务错误。
func configError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("配置保存失败：%w", err)
}

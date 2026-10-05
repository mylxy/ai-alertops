package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// endpoint 统一返回 JSON 和业务错误。
type endpoint func(http.ResponseWriter, *http.Request) (any, error)

// jsonRoute 限制请求体并隐藏数据库与凭据错误。
func jsonRoute(fn endpoint) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		v, e := fn(w, r)
		status := 200
		if e != nil {
			status = 500
			message := "服务暂时无法完成请求"
			var pe *problemError
			if errors.As(e, &pe) {
				status = pe.Status
				message = pe.Message
			}
			v = Row{"error": message}
			if status == 500 {
				fmt.Fprintln(os.Stderr, "请求处理失败：", r.Method, r.URL.Path, redactText(e.Error()))
			}
		}
		w.WriteHeader(status)
		if e = json.NewEncoder(w).Encode(redact(v)); e != nil {
			fmt.Fprintln(os.Stderr, "响应写入失败")
		}
	}
}

// bodyJSON 严格约束表单字段；Hook 使用兼容适配器。
func bodyJSON(r *http.Request, out any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	d.UseNumber()
	if e := d.Decode(out); e != nil {
		return fail(400, "请求 JSON 或字段无效")
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return fail(400, "请求只能包含一个 JSON 对象")
	}
	return nil
}

// positiveID 验证外部十进制 ID。
func positiveID(value string) (int64, error) {
	id, e := strconv.ParseInt(value, 10, 64)
	if e != nil || id <= 0 {
		return 0, fail(400, "实体编号无效")
	}
	return id, nil
}

// authorized 每次验证账号现态、管理员角色和浏览器 CSRF。
func (a *App) authorized(admin bool, fn func(http.ResponseWriter, *http.Request, Row) (any, error)) http.HandlerFunc {
	return jsonRoute(func(w http.ResponseWriter, r *http.Request) (any, error) {
		u, e := a.currentUser(r)
		if e != nil {
			return nil, e
		}
		if admin && u.S("role") != "ADMIN" {
			return nil, fail(403, "仅管理员可以修改配置")
		}
		if r.Method != "GET" {
			if r.Header.Get("X-AlertOps-Request") != "1" {
				return nil, fail(403, "缺少请求保护标识")
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				parsed, e := url.Parse(origin)
				if e != nil || parsed.Host != r.Host {
					return nil, fail(403, "请求来源不匹配")
				}
			}
		}
		return fn(w, r, u)
	})
}

// Handler 暴露 Hook、身份、后台只读查询与群 H5 处理；后台没有告警写接口。
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", jsonRoute(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return Row{"status": "ok"}, a.DB.PingContext(r.Context())
	}))
	mux.HandleFunc("GET /api/auth/options", jsonRoute(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return Row{"demo": a.Config.Demo, "corp_id": a.Config.CorpID, "app_id": a.Config.AppID}, nil
	}))
	mux.HandleFunc("POST /api/auth/demo", jsonRoute(func(w http.ResponseWriter, r *http.Request) (any, error) {
		if !a.Config.Demo {
			return nil, fail(404, "不存在的入口")
		}
		var in struct {
			Identity string `json:"identity"`
		}
		if e := bodyJSON(r, &in); e != nil {
			return nil, e
		}
		identity, e := a.Identity.VerifyCode(r.Context(), in.Identity)
		if e != nil {
			return nil, fail(401, "演示身份无效")
		}
		u, e := a.login(r.Context(), identity)
		if e != nil {
			return nil, e
		}
		http.SetCookie(w, a.sessionCookie(u.I("id")))
		return u, nil
	}))
	mux.HandleFunc("GET /api/auth/start", jsonRoute(func(w http.ResponseWriter, r *http.Request) (any, error) {
		state := nonce()
		http.SetCookie(w, &http.Cookie{Name: "alertops_oauth", Value: state + "." + a.signature(state), Path: "/api/auth", HttpOnly: true, Secure: strings.HasPrefix(a.Config.BaseURL, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: 300})
		q := url.Values{"redirect_uri": {a.Config.BaseURL + "/auth/callback"}, "response_type": {"code"}, "client_id": {a.Config.AppID}, "scope": {"openid corpid"}, "state": {state}, "prompt": {"consent"}}
		return Row{"url": "https://login.dingtalk.com/oauth2/auth?" + q.Encode()}, nil
	}))
	mux.HandleFunc("POST /api/auth/exchange", jsonRoute(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Code  string `json:"code"`
			State string `json:"state"`
		}
		if e := bodyJSON(r, &in); e != nil {
			return nil, e
		}
		c, e := r.Cookie("alertops_oauth")
		if e != nil || in.State == "" || !secretEqual(c.Value, in.State+"."+a.signature(in.State)) {
			return nil, fail(403, "登录 state 校验失败")
		}
		identity, e := a.Identity.VerifyCode(r.Context(), in.Code)
		if e != nil {
			return nil, fail(401, "钉钉身份验证失败")
		}
		u, e := a.login(r.Context(), identity)
		if e != nil {
			return nil, e
		}
		http.SetCookie(w, a.sessionCookie(u.I("id")))
		http.SetCookie(w, &http.Cookie{Name: "alertops_oauth", Value: "", Path: "/api/auth", MaxAge: -1, HttpOnly: true})
		return u, nil
	}))
	mux.HandleFunc("POST /api/auth/logout", a.authorized(false, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
		c := a.sessionCookie(u.I("id"))
		c.MaxAge = -1
		http.SetCookie(w, c)
		return Row{"ok": true}, nil
	}))
	mux.HandleFunc("GET /api/me", a.authorized(false, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) { return u, nil }))
	for path, kind := range map[string]string{"/hook/sls": "LOG", "/hook/alert": "METRIC"} {
		mux.HandleFunc("POST "+path, jsonRoute(func(w http.ResponseWriter, r *http.Request) (any, error) {
			source, e := one(r.Context(), a.DB, "SELECT * FROM alert_source WHERE source_code=? AND source_type=? AND config_status='ENABLED'", r.Header.Get("X-Alert-Source"), kind)
			if e != nil {
				return nil, e
			}
			if source == nil {
				return nil, fail(401, "来源鉴权失败")
			}
			secret := os.Getenv(source.S("auth_ref"))
			if a.Config.Demo && source.S("auth_ref") == "DEMO_HOOK_SECRET" {
				secret = "demo-hook-secret"
			}
			if !secretEqual(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), secret) {
				return nil, fail(401, "来源鉴权失败")
			}
			data, e := io.ReadAll(r.Body)
			if e != nil {
				return nil, fail(400, "Hook 内容过大")
			}
			var events []eventInput
			if kind == "LOG" {
				events, e = parseLog(data, r.Header.Get("Idempotency-Key"))
			} else {
				events, e = parseMetric(data, r.Header.Get("Idempotency-Key"))
			}
			if e != nil {
				return nil, e
			}
			ids, e := a.ingest(r.Context(), source, events)
			return Row{"status": 1, "message": "success", "data": Row{"event_ids": ids}}, e
		}))
	}
	mux.HandleFunc("GET /api/rounds", a.authorized(false, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) { return a.listRounds(r) }))
	mux.HandleFunc("GET /api/rounds/{id}", a.authorized(false, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
		id, e := positiveID(r.PathValue("id"))
		if e != nil {
			return nil, e
		}
		return a.roundDetail(r.Context(), id)
	}))
	mux.HandleFunc("GET /api/groups", a.authorized(false, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
		items, e := rows(r.Context(), a.DB, "SELECT g.*,SUM(CASE WHEN r.open_marker=0 THEN 1 ELSE 0 END) AS unfinished FROM alert_group g LEFT JOIN alert_round r ON r.alert_group_id=g.id GROUP BY g.id ORDER BY g.name")
		return Row{"items": items}, e
	}))
	mux.HandleFunc("GET /api/notices", a.authorized(false, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
		items, e := rows(r.Context(), a.DB, "SELECT e.*,l.service_key,l.message,l.class_name FROM alert_event e JOIN alert_log_detail l ON l.alert_event_id=e.id WHERE e.alert_type='NOTICE' ORDER BY e.received_time DESC,e.id DESC LIMIT 100")
		return Row{"items": items}, e
	}))
	mux.HandleFunc("GET /api/events", a.authorized(false, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
		items, e := rows(r.Context(), a.DB, "SELECT * FROM alert_event WHERE process_status<>'APPLIED' ORDER BY received_time DESC,id DESC LIMIT 100")
		return Row{"items": items}, e
	}))
	mux.HandleFunc("GET /api/stats", a.authorized(false, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) { return a.statistics(r) }))
	for suffix, kind := range map[string]string{"notes": "NOTE", "handle": "LOG_HANDLED"} {
		mux.HandleFunc("POST /api/h5/rounds/{id}/"+suffix, a.authorized(false, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
			id, e := positiveID(r.PathValue("id"))
			if e != nil {
				return nil, e
			}
			var in struct {
				Content string `json:"content"`
				Key     string `json:"operation_key"`
			}
			if e = bodyJSON(r, &in); e != nil {
				return nil, e
			}
			return a.humanAction(r.Context(), u, id, "H5", kind, in.Key, in.Content)
		}))
	}
	mux.HandleFunc("GET /api/h5/groups/{id}/membership", a.authorized(false, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
		id, e := positiveID(r.PathValue("id"))
		if e != nil {
			return nil, e
		}
		if e = a.verifyMember(r.Context(), u.I("id"), id); e != nil {
			return nil, e
		}
		return Row{"member": true}, nil
	}))
	if a.Config.Demo {
		mux.HandleFunc("POST /api/demo/scenarios", a.authorized(true, func(w http.ResponseWriter, r *http.Request, u Row) (any, error) {
			var in struct {
				Kind string `json:"kind"`
			}
			if e := bodyJSON(r, &in); e != nil {
				return nil, e
			}
			return Row{"accepted": true}, a.DemoScenario(r.Context(), in.Kind)
		}))
	}
	a.registerAdmin(mux)
	a.registerAI(mux)
	a.registerCodeup(mux)
	if a.Config.FrontendDir != "" {
		fs := http.FileServer(http.Dir(a.Config.FrontendDir))
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "same-origin")
			if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/hook/") {
				http.NotFound(w, r)
				return
			}
			if _, e := os.Stat(a.Config.FrontendDir + r.URL.Path); e != nil {
				http.ServeFile(w, r, a.Config.FrontendDir+"/index.html")
				return
			}
			fs.ServeHTTP(w, r)
		})
	}
	slots := make(chan struct{}, 64)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			mux.ServeHTTP(w, r)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":"请求较多，请稍后重试"}`)
		}
	})
}

// listRounds 使用时间和 ID 稳定游标；所有可用用户可查全平台。
func (a *App) listRounds(r *http.Request) (Row, error) {
	q := r.URL.Query()
	where := []string{"1=1"}
	args := []any{}
	for param, column := range map[string]string{"alert_no": "r.alert_no", "type": "r.alert_type", "status": "r.round_status", "group_id": "r.alert_group_id", "project_id": "r.alert_project_id", "resource_id": "r.alert_resource_id"} {
		if v := q.Get(param); v != "" {
			where = append(where, column+"=?")
			args = append(args, v)
		}
	}
	if q.Get("unfinished") == "1" {
		where = append(where, "r.open_marker=0")
	}
	for _, p := range []struct{ k, op string }{{"from", ">="}, {"to", "<"}} {
		if v := q.Get(p.k); v != "" {
			n, e := strconv.ParseInt(v, 10, 64)
			if e != nil {
				return nil, fail(400, "时间范围无效")
			}
			where = append(where, "r.first_event_time"+p.op+"?")
			args = append(args, n)
		}
	}
	if cursor := q.Get("cursor"); cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 2 {
			return nil, fail(400, "分页游标无效")
		}
		ms, e := strconv.ParseInt(parts[0], 10, 64)
		if e != nil {
			return nil, fail(400, "分页时间无效")
		}
		id, e := positiveID(parts[1])
		if e != nil {
			return nil, e
		}
		where = append(where, "(r.first_event_time,r.id)<(?,?)")
		args = append(args, ms, id)
	}
	items, e := rows(r.Context(), a.DB, "SELECT r.*,p.title,g.name AS group_name,j.name AS project_name FROM alert_round r JOIN alert_problem p ON p.id=r.alert_problem_id JOIN alert_group g ON g.id=r.alert_group_id JOIN alert_project j ON j.id=r.alert_project_id WHERE "+strings.Join(where, " AND ")+" ORDER BY r.first_event_time DESC,r.id DESC LIMIT 51", args...)
	if e != nil {
		return nil, e
	}
	cursor := ""
	if len(items) > 50 {
		items = items[:50]
		last := items[49]
		cursor = fmt.Sprintf("%d:%s", last.I("first_event_time"), last.S("id"))
	}
	return Row{"items": items, "next_cursor": cursor}, nil
}

// statistics 区分当前未完成、期间新增和期间结束，时间范围左闭右开。
func (a *App) statistics(r *http.Request) (Row, error) {
	now := time.Now()
	from := now.AddDate(0, 0, -7).UnixMilli()
	to := now.UnixMilli() + 1
	for key, dst := range map[string]*int64{"from": &from, "to": &to} {
		if v := r.URL.Query().Get(key); v != "" {
			n, e := strconv.ParseInt(v, 10, 64)
			if e != nil {
				return nil, fail(400, "统计时间无效")
			}
			*dst = n
		}
	}
	if from >= to {
		return nil, fail(400, "统计范围无效")
	}
	items, e := rows(r.Context(), a.DB, "SELECT alert_type,SUM(open_marker=0) AS unfinished,SUM(first_event_time>=? AND first_event_time<?) AS created,SUM(end_time>=? AND end_time<? AND open_marker<>0) AS completed,AVG(CASE WHEN end_time>=? AND end_time<? AND open_marker<>0 THEN end_time-first_event_time END) AS average_processing_ms FROM alert_round GROUP BY alert_type", from, to, from, to, from, to)
	if e != nil {
		return nil, e
	}
	ai, e := rows(r.Context(), a.DB, "SELECT execution_status,COUNT(*) AS total,AVG(CASE WHEN finished_time>0 AND started_time>0 THEN finished_time-started_time END) AS average_execution_ms FROM alert_ai_task WHERE queued_time>=? AND queued_time<? GROUP BY execution_status", from, to)
	if e != nil {
		return nil, e
	}
	mrs, e := rows(r.Context(), a.DB, "SELECT merge_status,COUNT(*) AS total FROM alert_merge_request WHERE create_time>=? AND create_time<? GROUP BY merge_status", from, to)
	return Row{"from": from, "to": to, "timezone": "Asia/Shanghai", "items": items, "ai": ai, "merge_requests": mrs}, e
}

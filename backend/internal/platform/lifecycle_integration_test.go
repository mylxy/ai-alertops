//go:build integration

package platform_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/mylxy/ai-alertops/backend/internal/platform"
)

// testPlatform 只准备独立测试数据库和服务，不封装业务行为。
func testPlatform(t *testing.T) (*platform.App, *httptest.Server) {
	t.Helper()
	dsn := os.Getenv("ALERTOPS_TEST_DSN")
	if dsn == "" {
		t.Fatal("缺少 ALERTOPS_TEST_DSN；仅使用本任务专用数据库")
	}
	app, err := platform.Open(context.Background(), platform.Config{DSN: dsn, NodeID: 1, SessionKey: "integration-session-key-32-bytes-long", CorpID: "test-corp", AppID: "test-app", BootstrapUnionID: "admin", Demo: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	if err := app.ResetDemo(context.Background()); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	return app, server
}

// requestJSON 只负责 HTTP 传输和解码，让输入与业务断言保留在场景中。
func requestJSON(t *testing.T, server *httptest.Server, method, path string, body any, cookie *http.Cookie, headers map[string]string) (int, map[string]any, []*http.Cookie) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AlertOps-Request", "1")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, result, resp.Cookies()
}

// TestLogLifecycle 验证 Hook 持久化、重复投递、人工处理与再次发生的完整链路。
func TestLogLifecycle(t *testing.T) {
	// 准备：通过受控外部身份登录，并使用旧日志 Hook 报文。
	app, server := testPlatform(t)
	status, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "admin"}, nil, nil)
	if status != 200 || len(cookies) == 0 {
		t.Fatalf("登录失败：%d", status)
	}
	headers := map[string]string{"X-Alert-Source": "demo-log", "Authorization": "Bearer demo-hook-secret", "Idempotency-Key": "delivery-1"}
	log := map[string]any{"namespace_name": "demo", "container_name": "api", "class": "OrderService", "level": "ERROR", "message": "订单提交失败", "traceId": "trace-1", "@timestamp": "2026-10-05T01:00:00Z"}
	// 执行：同次投递两遍，驱动可恢复的持久事件处理。
	for range 2 {
		status, body, _ := requestJSON(t, server, "POST", "/hook/sls", log, nil, headers)
		if status != 200 {
			t.Fatalf("Hook 失败：%d %v", status, body)
		}
	}
	if err := app.ProcessEvents(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	status, list, _ := requestJSON(t, server, "GET", "/api/rounds", nil, cookies[0], nil)
	if status != 200 {
		t.Fatal(list)
	}
	items := list["items"].([]any)
	// 验证：同轮一次计数，编号独立，人工操作立即持久化且幂等。
	if len(items) != 1 {
		t.Fatalf("轮次数 = %d，期望 1", len(items))
	}
	round := items[0].(map[string]any)
	if round["event_count"] != float64(1) || round["round_status"] != "PENDING" {
		t.Errorf("轮次 = %v", round)
	}
	id := round["id"].(string)
	for range 2 {
		status, body, _ := requestJSON(t, server, "POST", "/api/h5/rounds/"+id+"/notes", map[string]string{"content": "已排查日志", "operation_key": "note-1"}, cookies[0], nil)
		if status != 200 {
			t.Fatalf("保存备注：%d %v", status, body)
		}
	}
	status, body, _ := requestJSON(t, server, "POST", "/api/h5/rounds/"+id+"/handle", map[string]string{"content": "人工确认已生效", "operation_key": "close-1"}, cookies[0], nil)
	if status != 200 {
		t.Fatalf("关闭失败：%d %v", status, body)
	}
	status, _, _ = requestJSON(t, server, "POST", "/api/h5/rounds/"+id+"/notes", map[string]string{"content": "过期页面备注", "operation_key": "note-late"}, cookies[0], nil)
	if status != 409 {
		t.Errorf("终态备注响应 = %d，期望 409", status)
	}
	_, detail, _ := requestJSON(t, server, "GET", "/api/rounds/"+id, nil, cookies[0], nil)
	if detail["round"].(map[string]any)["round_status"] != "HANDLED" {
		t.Error(detail)
	}
	if len(detail["records"].([]any)) != 2 {
		t.Errorf("幂等备注应只有两条人工记录：%v", detail)
	}
	if len(detail["cards"].([]any)) != 3 {
		t.Errorf("首发、备注、关闭应有三张卡：%v", detail)
	}
}

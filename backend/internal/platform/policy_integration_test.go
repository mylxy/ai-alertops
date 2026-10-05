//go:build integration

package platform_test

import (
	"context"
	"testing"
)

// TestMetricCycles 验证纳秒周期隔离、旧恢复归属和指标不能人工关闭。
func TestMetricCycles(t *testing.T) {
	// 准备：已登录普通用户与同实例同指标、纳秒不同的两个周期。
	app, server := testPlatform(t)
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "member"}, nil, nil)
	headers := map[string]string{"X-Alert-Source": "demo-metric", "Authorization": "Bearer demo-hook-secret"}
	first := map[string]any{"status": "firing", "startsAt": "2026-10-05T01:00:00.000000001Z", "labels": map[string]string{"instanceId": "demo-server", "instanceName": "服务器", "metricKey": "cpu", "metricValue": "95"}}
	// 执行：重复周期采样允许更新值，重叠周期待核对，精确恢复结束对应周期。
	for _, alert := range []map[string]any{first, {"status": "firing", "startsAt": "2026-10-05T01:00:00.000000002Z", "labels": map[string]string{"instanceId": "demo-server", "metricKey": "cpu", "metricValue": "96"}}, {"status": "resolved", "startsAt": "2026-10-05T01:00:00.000000001Z", "endsAt": "2026-10-05T01:10:00Z", "labels": map[string]string{"instanceId": "demo-server", "metricKey": "cpu"}}} {
		status, body, _ := requestJSON(t, server, "POST", "/hook/alert", map[string]any{"alerts": []any{alert}}, nil, headers)
		if status != 200 {
			t.Fatal(body)
		}
		if e := app.ProcessEvents(context.Background(), 100); e != nil {
			t.Fatal(e)
		}
	}
	_, list, _ := requestJSON(t, server, "GET", "/api/rounds", nil, cookies[0], nil)
	// 验证：只有原轮次恢复；重叠 firing 不得创建或自动关闭另一轮。
	items := list["items"].([]any)
	if len(items) != 1 {
		t.Fatal(list)
	}
	round := items[0].(map[string]any)
	if round["round_status"] != "RECOVERED" {
		t.Error(round)
	}
	_, detail, _ := requestJSON(t, server, "GET", "/api/rounds/"+round["id"].(string), nil, cookies[0], nil)
	if detail["evidence"].(map[string]any)["value_status"] != "MISSING" {
		t.Error("恢复缺值不能复用异常值", detail)
	}
	_, events, _ := requestJSON(t, server, "GET", "/api/events", nil, cookies[0], nil)
	if len(events["items"].([]any)) != 1 {
		t.Error(events)
	}
	if len(detail["ai_tasks"].([]any)) != 0 {
		t.Error("指标不得启动 AI")
	}
}

// TestPermissionsAndNotice 验证普通用户全局可读、非群成员不可写、NOTICE 不产生告警事项。
func TestPermissionsAndNotice(t *testing.T) {
	// 准备：群外普通用户与 NOTICE。
	app, server := testPlatform(t)
	_, user, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "outsider"}, nil, nil)
	if user["role"] != "USER" {
		t.Fatal(user)
	}
	// 执行：通知和异常日志都通过相同 Hook 接入。
	for _, level := range []string{"NOTICE", "ERROR"} {
		status, body, _ := requestJSON(t, server, "POST", "/hook/sls", map[string]string{"namespace_name": "demo", "container_name": "api", "class": "Order", "level": level, "message": "状态变化"}, nil, map[string]string{"X-Alert-Source": "demo-log", "Authorization": "Bearer demo-hook-secret"})
		if status != 200 {
			t.Fatal(body)
		}
	}
	if e := app.ProcessEvents(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	status, list, _ := requestJSON(t, server, "GET", "/api/rounds", nil, cookies[0], nil)
	// 验证：平台全局可读，但管理权限和群内写操作分别拒绝。
	if status != 200 || len(list["items"].([]any)) != 1 {
		t.Fatal(list)
	}
	id := list["items"].([]any)[0].(map[string]any)["id"].(string)
	status, _, _ = requestJSON(t, server, "POST", "/api/h5/rounds/"+id+"/notes", map[string]string{"content": "越权备注", "operation_key": "outside"}, cookies[0], nil)
	if status != 403 {
		t.Errorf("非群成员写入返回 %d", status)
	}
	status, _, _ = requestJSON(t, server, "GET", "/api/admin/config", nil, cookies[0], nil)
	if status != 403 {
		t.Errorf("普通用户管理入口返回 %d", status)
	}
	status, _, _ = requestJSON(t, server, "GET", "/api/rounds", nil, nil, nil)
	if status != 401 {
		t.Errorf("未登录查询返回 %d", status)
	}
	_, notices, _ := requestJSON(t, server, "GET", "/api/notices", nil, cookies[0], nil)
	if len(notices["items"].([]any)) != 1 {
		t.Error(notices)
	}
}

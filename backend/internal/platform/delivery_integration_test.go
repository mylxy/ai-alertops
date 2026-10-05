//go:build integration

package platform_test

import (
	"context"
	"testing"
)

// TestCardsAndTopbox 验证有效人工操作刷新全部卡片，零未完成时关闭真实吊顶。
func TestCardsAndTopbox(t *testing.T) {
	// 准备：一个日志告警与已登录群成员。
	app, server := testPlatform(t)
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "member"}, nil, nil)
	status, body, _ := requestJSON(t, server, "POST", "/hook/sls", map[string]any{"namespace_name": "demo", "container_name": "api", "class": "Payment", "level": "ERROR", "message": "付款失败", "@timestamp": "2026-10-05T02:00:00Z"}, nil, map[string]string{"X-Alert-Source": "demo-log", "Authorization": "Bearer demo-hook-secret"})
	if status != 200 {
		t.Fatal(body)
	}
	if e := app.ProcessEvents(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	// 执行：持久任务投递首次卡片，并在 H5 关闭告警后同步。
	if e := app.ProcessJobs(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	_, messages, _ := requestJSON(t, server, "GET", "/api/demo/messages", nil, cookies[0], nil)
	if len(messages["cards"].([]any)) != 1 {
		t.Fatalf("首发卡片：%v", messages)
	}
	if messages["topboxes"].([]any)[0].(map[string]any)["desired_status"] != "OPEN" {
		t.Fatal(messages)
	}
	_, list, _ := requestJSON(t, server, "GET", "/api/rounds", nil, cookies[0], nil)
	id := list["items"].([]any)[0].(map[string]any)["id"].(string)
	status, body, _ = requestJSON(t, server, "POST", "/api/h5/rounds/"+id+"/handle", map[string]string{"content": "已确认修复", "operation_key": "handled"}, cookies[0], nil)
	if status != 200 {
		t.Fatal(body)
	}
	if e := app.ProcessJobs(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	_, messages, _ = requestJSON(t, server, "GET", "/api/demo/messages", nil, cookies[0], nil)
	// 验证：同轮两条消息均显示终态，吊顶关闭且历史卡片保留。
	cards := messages["cards"].([]any)
	if len(cards) != 2 {
		t.Errorf("卡片数量=%d，期望 2", len(cards))
	}
	for _, card := range cards {
		if card.(map[string]any)["round_status"] != "HANDLED" {
			t.Errorf("旧卡片未同步：%v", card)
		}
	}
	if messages["topboxes"].([]any)[0].(map[string]any)["desired_status"] != "CLOSED" {
		t.Error(messages)
	}
}

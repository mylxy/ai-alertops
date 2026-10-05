//go:build integration

package platform_test

import (
	"context"
	"errors"
	"github.com/mylxy/ai-alertops/backend/internal/platform"
	"strings"
	"testing"
)

// TestLateMetricEvidence 验证恢复后迟到的 firing 只保留历史，不覆盖明确缺失的恢复值。
func TestLateMetricEvidence(t *testing.T) {
	// 准备：相同纳秒周期的触发、恢复和迟到触发。
	app, server := testPlatform(t)
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "member"}, nil, nil)
	headers := map[string]string{"X-Alert-Source": "demo-metric", "Authorization": "Bearer demo-hook-secret"}
	for _, state := range []string{"firing", "resolved", "firing"} {
		labels := map[string]string{"instanceId": "demo-server", "metricKey": "cpu", "metricValue": "99"}
		if state == "resolved" {
			delete(labels, "metricValue")
		}
		alert := map[string]any{"status": state, "startsAt": "2026-10-05T01:00:00.000000001Z", "endsAt": "2026-10-05T01:10:00Z", "labels": labels}
		// 执行：每次 Hook 持久化后处理，再接受下一条乱序事实。
		status, body, _ := requestJSON(t, server, "POST", "/hook/alert", map[string]any{"alerts": []any{alert}}, nil, headers)
		if status != 200 {
			t.Fatal(body)
		}
		if e := app.ProcessEvents(context.Background(), 100); e != nil {
			t.Fatal(e)
		}
	}
	// 验证：终态值仍来自 resolved，事件证据保存三条。
	_, list, _ := requestJSON(t, server, "GET", "/api/rounds", nil, cookies[0], nil)
	id := list["items"].([]any)[0].(map[string]any)["id"].(string)
	_, detail, _ := requestJSON(t, server, "GET", "/api/rounds/"+id, nil, cookies[0], nil)
	if detail["round"].(map[string]any)["round_status"] != "RECOVERED" || detail["evidence"].(map[string]any)["value_status"] != "MISSING" || len(detail["events"].([]any)) != 3 {
		t.Fatal("恢复证据被迟到 firing 覆盖")
	}
}

// uncertainMessenger 模拟外部已接收但尚无完成证据的写请求，不提供协调能力。
type uncertainMessenger struct{ calls int }

func (d *uncertainMessenger) Card(context.Context, platform.Row) error {
	d.calls++
	return &platform.UncertainEffectError{Reason: "请求超时，结果未知"}
}
func (d *uncertainMessenger) Topbox(context.Context, platform.Row) error { return nil }

// TestUnknownDeliveryPauses 验证未知旧写入不会与新版本竞态，人工保存仍然有效。
func TestUnknownDeliveryPauses(t *testing.T) {
	// 准备：新日志，第一次消息投递进入未知状态。
	app, server := testPlatform(t)
	messenger := &uncertainMessenger{}
	app.Messenger = messenger
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "member"}, nil, nil)
	if e := app.DemoScenario(context.Background(), "log"); e != nil {
		t.Fatal(e)
	}
	if e := app.ProcessEvents(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	if e := app.ProcessJobs(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	_, list, _ := requestJSON(t, server, "GET", "/api/rounds", nil, cookies[0], nil)
	id := list["items"].([]any)[0].(map[string]any)["id"].(string)
	// 执行：投递未知后仍可填写结果，产生独立的新通知，但旧目标必须暂停。
	code, body, _ := requestJSON(t, server, "POST", "/api/h5/rounds/"+id+"/handle", map[string]string{"operation_key": "close", "content": "结果已确认"}, cookies[0], nil)
	if code != 200 {
		t.Fatal(body)
	}
	for range 2 {
		if _, e := app.DB.Exec("UPDATE alert_job SET next_run_time=0"); e != nil {
			t.Fatal(e)
		}
		if e := app.ProcessJobs(context.Background(), 100); e != nil {
			t.Fatal(e)
		}
	}
	// 验证：两个目标各只发出过一次，旧目标没有被当作一般失败重复更新。
	if messenger.calls != 2 {
		t.Fatalf("外部调用 %d，期望2个目标各1次", messenger.calls)
	}
	_, detail, _ := requestJSON(t, server, "GET", "/api/rounds/"+id, nil, cookies[0], nil)
	if detail["round"].(map[string]any)["round_status"] != "HANDLED" {
		t.Fatal("外部失败回滚了人工结果")
	}
}

// TestLongEvidenceAndKnownRetry 验证长展示内容不堵队列，明确失败可补偿且复用消息身份。
func TestLongEvidenceAndKnownRetry(t *testing.T) {
	// 准备：两个问题，其中一个异常类和资源键组合接近字段边界。
	app, server := testPlatform(t)
	ctx := context.Background()
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "member"}, nil, nil)
	for _, class := range []string{strings.Repeat("异", 85), "Next"} {
		code, body, _ := requestJSON(t, server, "POST", "/hook/sls", map[string]string{"namespace_name": "demo", "container_name": "api", "class": class, "level": "ERROR", "message": class + " 证据 password=secret-value"}, nil, map[string]string{"X-Alert-Source": "demo-log", "Authorization": "Bearer demo-hook-secret"})
		if code != 200 {
			t.Fatal(body)
		}
	}
	code, body, _ := requestJSON(t, server, "POST", "/hook/alert", map[string]any{"alerts": []any{map[string]any{"status": "firing", "startsAt": "2026-10-05T01:00:00Z", "labels": map[string]string{"instanceId": "demo-server", "instanceName": strings.Repeat("n", 255), "metricKey": strings.Repeat("m", 255)}}}}, nil, map[string]string{"X-Alert-Source": "demo-metric", "Authorization": "Bearer demo-hook-secret"})
	if code != 200 {
		t.Fatal(body)
	}
	// 执行：明确未成功的外部失败后恢复服务，重新驱动持久任务。
	if e := app.ProcessEvents(ctx, 100); e != nil {
		t.Fatal(e)
	}
	messenger := app.Messenger.(*platform.DemoMessenger)
	messenger.Failure = errors.New("已明确拒绝本次投递")
	if e := app.ProcessJobs(ctx, 100); e != nil {
		t.Fatal(e)
	}
	messenger.Failure = nil
	if _, e := app.DB.Exec("UPDATE alert_job SET next_run_time=0"); e != nil {
		t.Fatal(e)
	}
	if e := app.ProcessJobs(ctx, 100); e != nil {
		t.Fatal(e)
	}
	// 验证：全部事件可查询，普通响应脱敏，两个实例没有因失败新增副本。
	_, list, _ := requestJSON(t, server, "GET", "/api/rounds", nil, cookies[0], nil)
	if len(list["items"].([]any)) != 3 {
		t.Fatal(list)
	}
	_, messages, _ := requestJSON(t, server, "GET", "/api/demo/messages", nil, cookies[0], nil)
	if len(messages["cards"].([]any)) != 3 {
		t.Fatal("重试重复发卡")
	}
	for _, item := range list["items"].([]any) {
		id := item.(map[string]any)["id"].(string)
		_, detail, _ := requestJSON(t, server, "GET", "/api/rounds/"+id, nil, cookies[0], nil)
		message, _ := detail["evidence"].(map[string]any)["message"].(string)
		if strings.Contains(message, "secret-value") {
			t.Fatal("证据未脱敏")
		}
	}
}

// isolatedCardFailure 只让一个消息目标进入未知，其他外部消息正常完成。
type isolatedCardFailure struct{ blocked string }

func (d *isolatedCardFailure) Card(_ context.Context, r platform.Row) error {
	if r.S("out_track_id") == d.blocked {
		return &platform.UncertainEffectError{Reason: "单卡响应未知"}
	}
	return nil
}
func (d *isolatedCardFailure) Topbox(context.Context, platform.Row) error { return nil }

// TestUnknownCardDoesNotBlockSiblings 验证暂停范围是消息实例，不能阻断同轮其他正常卡片。
func TestUnknownCardDoesNotBlockSiblings(t *testing.T) {
	// 准备：一轮三条已经送达的群消息。
	app, server := testPlatform(t)
	messenger := &isolatedCardFailure{}
	app.Messenger = messenger
	ctx := context.Background()
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "member"}, nil, nil)
	payload := map[string]string{"namespace_name": "demo", "container_name": "api", "class": "Service", "message": "单卡失败场景", "level": "ERROR"}
	headers := map[string]string{"X-Alert-Source": "demo-log", "Authorization": "Bearer demo-hook-secret"}
	code, body, _ := requestJSON(t, server, "POST", "/hook/sls", payload, nil, headers)
	if code != 200 {
		t.Fatal(body)
	}
	if e := app.ProcessEvents(ctx, 100); e != nil {
		t.Fatal(e)
	}
	_, list, _ := requestJSON(t, server, "GET", "/api/rounds", nil, cookies[0], nil)
	id := list["items"].([]any)[0].(map[string]any)["id"].(string)
	for _, key := range []string{"first", "second"} {
		code, body, _ = requestJSON(t, server, "POST", "/api/h5/rounds/"+id+"/notes", map[string]string{"content": "同轮备注", "operation_key": key}, cookies[0], nil)
		if code != 200 {
			t.Fatal(body)
		}
	}
	if e := app.ProcessJobs(ctx, 100); e != nil {
		t.Fatal(e)
	}
	_, detail, _ := requestJSON(t, server, "GET", "/api/rounds/"+id, nil, cookies[0], nil)
	cards := detail["cards"].([]any)
	messenger.blocked = cards[0].(map[string]any)["out_track_id"].(string)
	// 执行：后续自动事件更新本轮，第一张外部响应未知，再来一次更新仍只能暂停第一张。
	for range 2 {
		code, body, _ = requestJSON(t, server, "POST", "/hook/sls", payload, nil, headers)
		if code != 200 {
			t.Fatal(body)
		}
		if e := app.ProcessEvents(ctx, 100); e != nil {
			t.Fatal(e)
		}
		if e := app.ProcessJobs(ctx, 100); e != nil {
			t.Fatal(e)
		}
	}
	// 验证：其他两张达到最新版本，未知目标仍保留现场，无新的消息身份。
	_, detail, _ = requestJSON(t, server, "GET", "/api/rounds/"+id, nil, cookies[0], nil)
	cards = detail["cards"].([]any)
	if len(cards) != 3 {
		t.Fatal("自动事件新增了消息")
	}
	for _, item := range cards {
		card := item.(map[string]any)
		if card["out_track_id"] == messenger.blocked {
			if card["delivery_status"] != "UNKNOWN" {
				t.Fatal(card)
			}
			continue
		}
		if card["delivery_status"] != "SENT" || card["sent_version"] != card["desired_version"] {
			t.Fatal("同轮健康卡片被阻塞", card)
		}
	}
}

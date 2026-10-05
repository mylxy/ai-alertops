//go:build integration

package platform_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mylxy/ai-alertops/backend/internal/platform"
)

// TestConcurrentCloseAndNotes 验证同时备注与关闭有确定保存边界，终态结果不能覆盖。
func TestConcurrentCloseAndNotes(t *testing.T) {
	// 准备：两个已验证的群成员与待处理日志。
	app, server := testPlatform(t)
	ctx := context.Background()
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "member"}, nil, nil)
	if e := app.DemoScenario(ctx, "log"); e != nil {
		t.Fatal(e)
	}
	if e := app.ProcessEvents(ctx, 100); e != nil {
		t.Fatal(e)
	}
	_, list, _ := requestJSON(t, server, "GET", "/api/rounds", nil, cookies[0], nil)
	id := list["items"].([]any)[0].(map[string]any)["id"].(string)
	// 执行：同步起跑，允许先到的备注保存，关闭提交始终可以成功。
	start := make(chan struct{})
	results := make(chan int, 10)
	var wg sync.WaitGroup
	for n := range 10 {
		wg.Go(func() {
			<-start
			action, content := "notes", "并发排查记录"
			if n == 0 {
				action = "handle"
				content = "确定的关闭结论"
			}
			status, _, _ := requestJSON(t, server, "POST", "/api/h5/rounds/"+id+"/"+action, map[string]string{"operation_key": fmt.Sprintf("race-%d", n), "content": content}, cookies[0], nil)
			results <- status
		})
	}
	close(start)
	wg.Wait()
	close(results)
	saved := 0
	for status := range results {
		if status == 200 {
			saved++
		} else if status != 409 {
			t.Fatalf("并发请求返回 %d", status)
		}
	}
	// 验证：每个成功响应都有一条记录及一个 H5 通知，失败没有残留记录。
	_, detail, _ := requestJSON(t, server, "GET", "/api/rounds/"+id, nil, cookies[0], nil)
	r := detail["round"].(map[string]any)
	if r["round_status"] != "HANDLED" || r["result_text"] != "确定的关闭结论" || len(detail["records"].([]any)) != saved || len(detail["cards"].([]any)) != saved+1 {
		t.Fatal("并发保存缺少原子边界")
	}
	status, _, _ := requestJSON(t, server, "POST", "/api/h5/rounds/"+id+"/handle", map[string]string{"operation_key": "race-0", "content": "确定的关闭结论"}, cookies[0], nil)
	if status != 200 {
		t.Fatal("关闭重试未返回原结果")
	}
}

// TestStrongDedupAndRecurrence 验证强标识冲突、无标识真实重复、明确复发和旧轮迟到。
func TestStrongDedupAndRecurrence(t *testing.T) {
	// 准备：携带发生时间的历史日志。
	app, server := testPlatform(t)
	ctx := context.Background()
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "member"}, nil, nil)
	source := map[string]string{"X-Alert-Source": "demo-log", "Authorization": "Bearer demo-hook-secret", "Idempotency-Key": "first"}
	log := map[string]string{"namespace_name": "demo", "container_name": "api", "class": "Service", "level": "ERROR", "message": "可复发异常", "@timestamp": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)}
	status, _, _ := requestJSON(t, server, "POST", "/hook/sls", log, nil, source)
	if status != 200 {
		t.Fatal(status)
	}
	log["message"] = "同键不同内容"
	status, _, _ = requestJSON(t, server, "POST", "/hook/sls", log, nil, source)
	if status != 409 {
		t.Fatal("未识别强事件键冲突")
	}
	log["message"] = "可复发异常"
	delete(source, "Idempotency-Key")
	for range 2 {
		status, _, _ = requestJSON(t, server, "POST", "/hook/sls", log, nil, source)
		if status != 200 {
			t.Fatal(status)
		}
	}
	if e := app.ProcessEvents(ctx, 100); e != nil {
		t.Fatal(e)
	}
	_, list, _ := requestJSON(t, server, "GET", "/api/rounds", nil, cookies[0], nil)
	old := list["items"].([]any)[0].(map[string]any)
	id := old["id"].(string)
	if old["event_count"] != float64(3) {
		t.Fatal("无稳定标识被误去重")
	}
	// 执行：关闭后发送明确更晚的一次异常，再补投旧发生时间。
	status, _, _ = requestJSON(t, server, "POST", "/api/h5/rounds/"+id+"/handle", map[string]string{"operation_key": "close", "content": "旧轮结束"}, cookies[0], nil)
	if status != 200 {
		t.Fatal(status)
	}
	oldTime := log["@timestamp"]
	log["@timestamp"] = time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
	status, _, _ = requestJSON(t, server, "POST", "/hook/sls", log, nil, source)
	if status != 200 {
		t.Fatal(status)
	}
	if e := app.ProcessEvents(ctx, 100); e != nil {
		t.Fatal(e)
	}
	log["@timestamp"] = oldTime
	status, _, _ = requestJSON(t, server, "POST", "/hook/sls", log, nil, source)
	if status != 200 {
		t.Fatal(status)
	}
	if e := app.ProcessEvents(ctx, 100); e != nil {
		t.Fatal(e)
	}
	// 验证：两个不可复用的 AL 编号，新轮等待，旧轮仍已处理且收纳迟到证据。
	_, list, _ = requestJSON(t, server, "GET", "/api/rounds", nil, cookies[0], nil)
	items := list["items"].([]any)
	if len(items) != 2 {
		t.Fatal("复发未建立新轮")
	}
	for _, x := range items {
		r := x.(map[string]any)
		if !strings.HasPrefix(r["alert_no"].(string), "AL") {
			t.Fatal(r)
		}
		if r["id"] == id {
			if r["round_status"] != "HANDLED" || r["event_count"] != float64(4) {
				t.Fatal("旧轮迟到归属错误")
			}
		} else if r["round_status"] != "PENDING" || r["event_count"] != float64(1) {
			t.Fatal("新轮被旧事件影响")
		}
	}
}

// TestCloseBeforeCLIStart 验证人工关闭赢得启动门槛后，CLI 不接收任务提示。
func TestCloseBeforeCLIStart(t *testing.T) {
	// 准备：已领取但尚未发出实际启动事件的任务。
	app, server := testPlatform(t)
	ctx := context.Background()
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "admin"}, nil, nil)
	if e := app.DemoScenario(ctx, "log"); e != nil {
		t.Fatal(e)
	}
	if e := app.ProcessEvents(ctx, 100); e != nil {
		t.Fatal(e)
	}
	_, cfg, _ := requestJSON(t, server, "GET", "/api/admin/config", nil, cookies[0], nil)
	pid := cfg["alert_project"].([]any)[0].(map[string]any)["id"].(string)
	task, e := app.ClaimTask(ctx, pid, "test")
	if e != nil {
		t.Fatal(e)
	}
	// 执行：人工先关闭，再收到容器实际创建的 CLI 启动回调。
	status, _, _ := requestJSON(t, server, "POST", "/api/h5/rounds/"+task.S("alert_round_id")+"/handle", map[string]string{"operation_key": "close", "content": "启动前结束"}, cookies[0], nil)
	if status != 200 {
		t.Fatal(status)
	}
	status, _, _ = requestJSON(t, server, "POST", "/hook/ai/"+task.S("id"), platform.Progress{Sequence: 1, Event: "EXECUTION_STARTED", Summary: "CLI 已启动等待门槛"}, nil, map[string]string{"Authorization": "Bearer " + task.S("execution_token")})
	// 验证：启动被明确拒绝，工作区仍占用等待宿主确认进程退出。
	if status != 409 {
		t.Fatalf("启动门槛返回 %d", status)
	}
	var occupied int
	if e = app.DB.QueryRow("SELECT COUNT(*) FROM alert_project_workspace WHERE active_alert_ai_task_id=?", task.S("id")).Scan(&occupied); e != nil || occupied != 1 {
		t.Fatalf("提前释放目录 %d %v", occupied, e)
	}
}

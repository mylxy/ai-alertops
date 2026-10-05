//go:build integration

package platform_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/mylxy/ai-alertops/backend/internal/platform"
)

// TestProjectQueueLimits 验证并发领取五个告警时只有三个独立工作区执行，其余继续排队。
func TestProjectQueueLimits(t *testing.T) {
	// 准备：同项目五个不同日志问题。
	app, server := testPlatform(t)
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "admin"}, nil, nil)
	for n := 1; n <= 5; n++ {
		status, body, _ := requestJSON(t, server, "POST", "/hook/sls", map[string]string{"namespace_name": "demo", "container_name": "api", "class": "Service", "level": "ERROR", "message": fmt.Sprintf("故障 %d", n)}, nil, map[string]string{"X-Alert-Source": "demo-log", "Authorization": "Bearer demo-hook-secret"})
		if status != 200 {
			t.Fatal(body)
		}
	}
	if e := app.ProcessEvents(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	_, configuration, _ := requestJSON(t, server, "GET", "/api/admin/config", nil, cookies[0], nil)
	pid := configuration["alert_project"].([]any)[0].(map[string]any)["id"].(string)
	// 执行：五个执行器同时领取任务，模拟重叠调度周期。
	var wg sync.WaitGroup
	claimed := make(chan platform.Row, 5)
	failures := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			task, e := app.ClaimTask(context.Background(), pid, "test-runner")
			if e != nil {
				failures <- e
				return
			}
			if task != nil {
				claimed <- task
			}
		})
	}
	wg.Wait()
	close(claimed)
	close(failures)
	// 验证：恰好三个活动任务，三个目录不同；创建容器前状态仍为待处理。
	for e := range failures {
		t.Error(e)
	}
	if len(claimed) != 3 {
		t.Fatalf("领取数量 = %d，期望 3", len(claimed))
	}
	paths := map[string]bool{}
	for task := range claimed {
		if paths[task.S("workspace_path")] {
			t.Error("重复分配工作区")
		}
		paths[task.S("workspace_path")] = true
	}
	_, tasks, _ := requestJSON(t, server, "GET", "/api/tasks", nil, cookies[0], nil)
	queued, starting := 0, 0
	for _, x := range tasks["items"].([]any) {
		switch x.(map[string]any)["execution_status"] {
		case "QUEUED":
			queued++
		case "STARTING":
			starting++
		}
	}
	if queued != 2 || starting != 3 {
		t.Errorf("排队 %d，启动中 %d", queued, starting)
	}
	_, list, _ := requestJSON(t, server, "GET", "/api/rounds", nil, cookies[0], nil)
	for _, x := range list["items"].([]any) {
		if x.(map[string]any)["round_status"] != "PENDING" {
			t.Error("未运行 CLI 就把告警改为处理中")
		}
	}
}

// TestAIStartAndProgress 验证创建容器不推进主状态，真实启动与定位事件可审计，关闭后不重开。
func TestAIStartAndProgress(t *testing.T) {
	// 准备：新日志与一项领取后的任务。
	app, server := testPlatform(t)
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "admin"}, nil, nil)
	status, body, _ := requestJSON(t, server, "POST", "/hook/sls", map[string]string{"namespace_name": "demo", "container_name": "api", "class": "Service", "level": "ERROR", "message": "AI 排查目标"}, nil, map[string]string{"X-Alert-Source": "demo-log", "Authorization": "Bearer demo-hook-secret"})
	if status != 200 {
		t.Fatal(body)
	}
	if e := app.ProcessEvents(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	_, config, _ := requestJSON(t, server, "GET", "/api/admin/config", nil, cookies[0], nil)
	task, e := app.ClaimTask(context.Background(), config["alert_project"].([]any)[0].(map[string]any)["id"].(string), "test-runner")
	if e != nil {
		t.Fatal(e)
	}
	headers := map[string]string{"Authorization": "Bearer " + task.S("execution_token")}
	path := "/hook/ai/" + task.S("id")
	// 执行：模拟 CLI 已启动的明确事件；随后人工关闭，AI 再报告无需修复。
	status, body, _ = requestJSON(t, server, "POST", path, platform.Progress{Sequence: 1, Event: "EXECUTION_STARTED", Summary: "Codex CLI 已启动"}, nil, headers)
	if status != 200 {
		t.Fatalf("启动回调 %d %v", status, body)
	}
	_, detail, _ := requestJSON(t, server, "GET", "/api/rounds/"+task.S("alert_round_id"), nil, cookies[0], nil)
	if detail["round"].(map[string]any)["round_status"] != "PROCESSING" {
		t.Fatal(detail)
	}
	status, body, _ = requestJSON(t, server, "POST", "/api/h5/rounds/"+task.S("alert_round_id")+"/handle", map[string]string{"operation_key": "close-running", "content": "人工确认无需继续等待"}, cookies[0], nil)
	if status != 200 {
		t.Fatal(body)
	}
	for _, p := range []platform.Progress{{Sequence: 2, Event: "ROOT_CAUSE_LOCATED", Summary: "定位为临时网络问题"}, {Sequence: 3, Event: "EXECUTION_FINISHED", Summary: "无需修改代码", BusinessResult: "NO_ISSUE"}} {
		status, body, _ = requestJSON(t, server, "POST", path, p, nil, headers)
		if status != 200 {
			t.Fatal(body)
		}
	}
	if e := app.ProcessJobs(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	// 验证：AI 留在原轮追加记录，人工结果不可覆盖，完成等待真实容器退出。
	_, detail, _ = requestJSON(t, server, "GET", "/api/rounds/"+task.S("alert_round_id"), nil, cookies[0], nil)
	if detail["round"].(map[string]any)["round_status"] != "HANDLED" {
		t.Error(detail)
	}
	ai := detail["ai_tasks"].([]any)[0].(map[string]any)
	if ai["execution_status"] != "FINALIZING" || ai["business_result"] != "NO_ISSUE" {
		t.Error(ai)
	}
	status, _, _ = requestJSON(t, server, "POST", path, platform.Progress{Sequence: 3, Event: "EXECUTION_FINISHED", Summary: "伪造不同结论", BusinessResult: "FIX_PROPOSED"}, nil, headers)
	if status != 409 {
		t.Errorf("同序号不同内容返回 %d，期望 409", status)
	}
}

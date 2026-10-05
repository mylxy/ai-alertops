//go:build integration

package platform_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mylxy/ai-alertops/backend/internal/platform"
)

// uncertainCodeup 模拟服务端已接收创建、客户端超时且查询暂不可见的外部边界。
type uncertainCodeup struct {
	*platform.DemoCodeup
	creates int
	hidden  bool
}

func (c *uncertainCodeup) Find(ctx context.Context, org, repo, marker string) (platform.Row, error) {
	if c.hidden {
		return nil, nil
	}
	return c.DemoCodeup.Find(ctx, org, repo, marker)
}
func (c *uncertainCodeup) Create(ctx context.Context, org, repo, branch, title, description string) (platform.Row, error) {
	c.creates++
	_, e := c.DemoCodeup.Create(ctx, org, repo, branch, title, description)
	if e != nil {
		return nil, e
	}
	return nil, errors.New("模拟响应丢失")
}

// TestMRReconciliation 验证三条动态 MR、评审通过不算合并、早到 Hook 与创建未知结果补偿。
func TestMRReconciliation(t *testing.T) {
	// 准备：日志任务、一个受影响仓库和服务端创建超时的 Codeup。
	app, server := testPlatform(t)
	ctx := context.Background()
	app.Config.CodeupHookToken = "test-codeup-hook"
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "admin"}, nil, nil)
	_, cfg, _ := requestJSON(t, server, "GET", "/api/admin/config", nil, cookies[0], nil)
	pid := cfg["alert_project"].([]any)[0].(map[string]any)["id"].(string)
	for n := 1; n <= 3; n++ {
		status, body, _ := requestJSON(t, server, "PUT", "/api/admin/config/alert_project_repo", map[string]any{"alert_project_id": pid, "provider": "CODEUP", "base_branch": "master", "repo_code": fmt.Sprintf("repo%d", n), "organization_id": "test-org", "repository_id": fmt.Sprint(n), "git_url": fmt.Sprintf("https://codeup.aliyun.com/test-org/repo%d.git", n), "relative_path": fmt.Sprintf("repo%d", n), "credential_ref": "", "config_status": "ENABLED"}, cookies[0], nil)
		if status != 200 {
			t.Fatalf("配置仓库 %d %v", status, body)
		}
	}
	status, body, _ := requestJSON(t, server, "POST", "/hook/sls", map[string]string{"namespace_name": "demo", "container_name": "api", "level": "ERROR", "message": "多仓库修复", "class": "Service"}, nil, map[string]string{"X-Alert-Source": "demo-log", "Authorization": "Bearer demo-hook-secret"})
	if status != 200 {
		t.Fatal(body)
	}
	if e := app.ProcessEvents(ctx, 100); e != nil {
		t.Fatal(e)
	}
	task, e := app.ClaimTask(ctx, pid, "test")
	if e != nil || task == nil {
		t.Fatal(e)
	}
	auth := map[string]string{"Authorization": "Bearer " + task.S("execution_token")}
	path := "/hook/ai/" + task.S("id")
	repos := []platform.RepoProgress{}
	for n := 1; n <= 3; n++ {
		repos = append(repos, platform.RepoProgress{Code: fmt.Sprintf("repo%d", n), Base: strings.Repeat("b", 40), Head: strings.Repeat("a", 40), Verification: "PASSED", VerificationSummary: "项目测试已通过", RequiredMRCount: 1})
	}
	for _, p := range []platform.Progress{{Sequence: 1, Event: "EXECUTION_STARTED", Summary: "启动"}, {Sequence: 2, Event: "ROOT_CAUSE_LOCATED", Summary: "定位三个相关仓库", Repos: repos}, {Sequence: 3, Event: "VERIFICATION_FINISHED", Summary: "验证修复", Repos: repos}} {
		status, body, _ = requestJSON(t, server, "POST", path, p, nil, auth)
		if status != 200 {
			t.Fatal(body)
		}
		if e = app.ProcessJobs(ctx, 100); e != nil {
			t.Fatal(e)
		}
	}
	codeup := &uncertainCodeup{DemoCodeup: platform.NewDemoCodeup(), hidden: true}
	app.Codeup = codeup
	// 执行：早到的 Hook 没有已知关联，必须等待；创建响应丢失后即使查询暂不可见也不能再创建。
	hookHeaders := map[string]string{"X-Codeup-Token": "test-codeup-hook"}
	status, body, _ = requestJSON(t, server, "POST", "/hook/codeup", map[string]any{"object_kind": "merge_request", "object_attributes": map[string]string{"local_id": "1", "target_project_id": "1", "action": "approved"}}, nil, hookHeaders)
	if status != 200 {
		t.Fatal(body)
	}
	for n := 1; n <= 3; n++ {
		status, body, _ = requestJSON(t, server, "POST", "/api/ai/tasks/"+task.S("id")+"/merge-requests", map[string]string{"repo_code": fmt.Sprintf("repo%d", n), "request_key": "delivery", "title": "修复异常"}, nil, auth)
		if status != 200 {
			t.Fatal(body)
		}
	}
	for range 2 {
		if _, e = app.DB.Exec("UPDATE alert_job SET next_run_time=0"); e != nil {
			t.Fatal(e)
		}
		if e = app.ProcessJobs(ctx, 100); e != nil {
			t.Fatal(e)
		}
	}
	if codeup.creates != 3 {
		t.Fatalf("创建请求 %d，期望每仓库仅一次", codeup.creates)
	}
	status, body, _ = requestJSON(t, server, "POST", path, platform.Progress{Sequence: 4, Event: "EXECUTION_FINISHED", Summary: "修复交付完成", BusinessResult: "FIX_PROPOSED", Repos: repos}, nil, auth)
	if status != 200 {
		t.Fatal(body)
	}
	if e = app.ProcessJobs(ctx, 100); e != nil {
		t.Fatal(e)
	}
	codeup.hidden = false
	if _, e = app.DB.Exec("UPDATE alert_job SET next_run_time=0"); e != nil {
		t.Fatal(e)
	}
	if e = app.ProcessJobs(ctx, 100); e != nil {
		t.Fatal(e)
	}
	_, detail, _ := requestJSON(t, server, "GET", "/api/rounds/"+task.S("alert_round_id"), nil, cookies[0], nil)
	mrs := detail["merge_requests"].([]any)
	if len(mrs) != 3 || detail["all_merged"] != false {
		t.Fatalf("动态 MR 或汇总错误 %v", detail)
	}
	// 验证：通过仅更新评审；三个权威合并事实才显示全部已合并，告警仍为处理中。
	for _, state := range []string{"UNDER_REVIEW", "MERGED"} {
		for _, item := range mrs {
			mr := item.(map[string]any)
			repo := mr["repository_id"].(string)
			local := mr["display_number"].(string)
			external, er := codeup.Get(ctx, "test-org", repo, local)
			if er != nil {
				t.Fatal(er)
			}
			patch := platform.Row{}
			for k, v := range external {
				patch[k] = v
			}
			patch["status"] = state
			patch["reviewers"] = []any{map[string]any{"reviewOpinionStatus": "PASS"}}
			patch["updateTime"] = time.Now().UTC().Format(time.RFC3339Nano)
			if state == "MERGED" {
				patch["mergedRevision"] = strings.Repeat("c", 40)
			}
			codeup.Set("test-org", repo, local, patch)
			status, body, _ = requestJSON(t, server, "POST", "/hook/codeup", map[string]any{"object_kind": "merge_request", "object_attributes": map[string]string{"local_id": local, "target_project_id": repo, "action": state}}, nil, hookHeaders)
			if status != 200 {
				t.Fatal(body)
			}
		}
		if _, e = app.DB.Exec("UPDATE alert_job SET next_run_time=0"); e != nil {
			t.Fatal(e)
		}
		if e = app.ProcessJobs(ctx, 100); e != nil {
			t.Fatal(e)
		}
		_, detail, _ = requestJSON(t, server, "GET", "/api/rounds/"+task.S("alert_round_id"), nil, cookies[0], nil)
		if detail["all_merged"] != (state == "MERGED") {
			t.Fatalf("评审/合并混淆 %s %v", state, detail)
		}
		if detail["round"].(map[string]any)["round_status"] != "PROCESSING" {
			t.Fatal("MR 不得自动关闭告警")
		}
	}
}

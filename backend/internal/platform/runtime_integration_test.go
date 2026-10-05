//go:build integration

package platform_test

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mylxy/ai-alertops/backend/internal/platform"
)

// TestDockerLifecycle 使用真实 Docker 与确定性 CLI 替身，验证宿主重启核对和产物收尾。
func TestDockerLifecycle(t *testing.T) {
	if os.Getenv("ALERTOPS_TEST_DOCKER") != "1" {
		t.Skip("Docker 链路单独通过 ALERTOPS_TEST_DOCKER=1 显式验收")
	}
	for _, mode := range []string{"success", "nonzero", "missing", "invalid", "unresolved", "log-link"} {
		t.Run(mode, func(t *testing.T) { dockerLifecycle(t, mode) })
	}
}

// dockerLifecycle 复用真实环境准备，断言仍覆盖完整容器与平台收尾协议。
func dockerLifecycle(t *testing.T, mode string) {
	// 准备：本次测试独立目录、镜像和真实数据库，不接触现有项目。
	app, server := testPlatform(t)
	ctx := context.Background()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	app.Config.ArtifactRoot = filepath.Join(root, "artifacts")
	sentinel := filepath.Join(root, "host-sentinel")
	if e = os.WriteFile(sentinel, []byte("宿主原始内容"), 0600); e != nil {
		t.Fatal(e)
	}
	endpoint, e := url.Parse(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	app.Config.RunnerCallbackURL = "http://host.docker.internal:" + endpoint.Port()
	app.Runtime = &platform.DockerRuntime{}
	image := "alertops-runner-fixture:local"
	command := exec.Command("docker", "build", "-q", "-t", image, "../../../runner/fixtures")
	if output, e := command.CombinedOutput(); e != nil {
		t.Fatalf("构建确定性 CLI 容器：%v %s", e, output)
	}
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "admin"}, nil, nil)
	_, cfg, _ := requestJSON(t, server, "GET", "/api/admin/config", nil, cookies[0], nil)
	pid := cfg["alert_project"].([]any)[0].(map[string]any)["id"].(string)
	origin := filepath.Join(root, "origin.git")
	if out, e := exec.Command("git", "init", "--bare", origin).CombinedOutput(); e != nil {
		t.Fatalf("建立测试仓库：%v %s", e, out)
	}
	code, body, _ := requestJSON(t, server, "PUT", "/api/admin/config/alert_project_repo", map[string]string{"alert_project_id": pid, "repo_code": "fixture", "relative_path": "repo", "provider": "CODEUP", "organization_id": "test-org", "repository_id": "1", "git_url": "https://codeup.aliyun.com/test-org/fixture.git", "base_branch": "master", "credential_ref": "", "config_status": "ENABLED"}, cookies[0], nil)
	if code != 200 {
		t.Fatal(body)
	}
	for _, entry := range cfg["alert_project_workspace"].([]any) {
		w := entry.(map[string]any)
		path := filepath.Join(root, w["id"].(string))
		if e = os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(path, ".fixture-sentinel"), []byte(sentinel), 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(path, ".fixture-mode"), []byte(mode), 0600); e != nil {
			t.Fatal(e)
		}
		if out, e := exec.Command("git", "clone", origin, filepath.Join(path, "repo")).CombinedOutput(); e != nil {
			t.Fatalf("建立独立克隆：%v %s", e, out)
		}
		if _, e = app.DB.Exec("UPDATE alert_project_workspace SET workspace_path=? WHERE id=?", path, w["id"]); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = app.DB.Exec("UPDATE alert_project SET workspace_root=?,runner_image=? WHERE id=?", root, image, pid); e != nil {
		t.Fatal(e)
	}
	if e = app.DemoScenario(ctx, "log"); e != nil {
		t.Fatal(e)
	}
	if e = app.ProcessEvents(ctx, 100); e != nil {
		t.Fatal(e)
	}
	// 执行：首次调度创建容器，后续采用一个全新的 Runtime 实例核对，不能重新启动 CLI。
	if e = app.RunSchedulerOnce(ctx); e != nil {
		t.Fatal(e)
	}
	_, items, _ := requestJSON(t, server, "GET", "/api/tasks", nil, cookies[0], nil)
	task := items["items"].([]any)[0].(map[string]any)
	name := "alertops-task-" + task["id"].(string)
	t.Cleanup(func() {
		if out, e := exec.Command("docker", "rm", "-f", name).CombinedOutput(); e != nil {
			t.Errorf("清理本次容器：%v %s", e, out)
		}
	})
	app.Runtime = &platform.DockerRuntime{}
	deadline := time.Now().Add(40 * time.Second)
	state := ""
	for time.Now().Before(deadline) {
		if e = app.ProcessJobs(ctx, 100); e != nil {
			t.Fatal(e)
		}
		if e = app.RunSchedulerOnce(ctx); e != nil {
			t.Fatal(e)
		}
		_, items, _ = requestJSON(t, server, "GET", "/api/tasks", nil, cookies[0], nil)
		task = items["items"].([]any)[0].(map[string]any)
		state = task["execution_status"].(string)
		if state == "SUCCEEDED" || state == "FAILED" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// 验证：真实退出才结束，最终结果、原始 JSONL 和容器日志保留，短期凭据清除。
	want := "FAILED"
	if mode == "success" || mode == "unresolved" || mode == "log-link" {
		want = "SUCCEEDED"
	}
	if state != want {
		out, _ := exec.Command("docker", "logs", name).CombinedOutput()
		t.Fatalf("容器未成功收尾 %v %s", task, out)
	}
	artifact := filepath.Join(app.Config.ArtifactRoot, task["id"].(string))
	for _, file := range []string{"context.json", "events.jsonl", "codex.jsonl", "cli-exit.json", "cli-version.txt", "container.log"} {
		if _, e = os.Stat(filepath.Join(artifact, file)); e != nil {
			t.Error(file, e)
		}
	}
	for _, file := range []string{"runtime.env", "git-credentials.json"} {
		if _, e = os.Stat(filepath.Join(artifact, file)); !os.IsNotExist(e) {
			t.Errorf("凭据未清理：%s %v", file, e)
		}
	}
	data, e := os.ReadFile(filepath.Join(artifact, "events.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	if mode == "unresolved" {
		_, detail, _ := requestJSON(t, server, "GET", "/api/rounds/"+task["alert_round_id"].(string), nil, cookies[0], nil)
		if detail["ai_tasks"].([]any)[0].(map[string]any)["business_result"] != "UNRESOLVED" {
			t.Fatal("业务无法定位未保留")
		}
	}
	if strings.Count(string(data), "EXECUTION_STARTED") != 1 {
		t.Fatal("重启核对重复启动了任务")
	}
	if mode == "log-link" {
		data, e := os.ReadFile(sentinel)
		if e != nil || string(data) != "宿主原始内容" {
			t.Fatal("宿主日志收尾跟随了容器链接")
		}
		info, e := os.Lstat(filepath.Join(artifact, "container.log"))
		if e != nil || !info.Mode().IsRegular() {
			t.Fatal("容器日志未原子替换为普通文件")
		}
	}
	var occupied int
	if e = app.DB.QueryRow("SELECT COUNT(*) FROM alert_project_workspace WHERE active_alert_ai_task_id>0").Scan(&occupied); e != nil || occupied != 0 {
		t.Fatalf("槽位未释放 %d %v", occupied, e)
	}
}

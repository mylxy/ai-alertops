package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// DockerRuntime 只在宿主服务使用 Docker CLI，固定目录挂载给单个容器。
type DockerRuntime struct {
	CallbackURL string
	Credential  string
}

// Inspect 区分不存在、正在运行和已退出；Docker 不可用时不能假定容器已停止。
func (d *DockerRuntime) Inspect(ctx context.Context, name string) (Row, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	data, e := exec.CommandContext(ctx, "docker", "inspect", "--type", "container", name).Output()
	if e != nil {
		var ee *exec.ExitError
		if errors.As(e, &ee) && strings.Contains(string(ee.Stderr), "No such") {
			return Row{"exists": false}, nil
		}
		return nil, errors.New("无法确认 Docker 容器状态")
	}
	var containers []struct {
		ID    string `json:"Id"`
		State struct {
			Running  bool
			Status   string
			ExitCode int
		}
		Config struct{ Labels map[string]string }
	}
	if e = json.Unmarshal(data, &containers); e != nil || len(containers) != 1 {
		return nil, errors.New("Docker 状态响应不完整")
	}
	c := containers[0]
	return Row{"exists": true, "container_id": c.ID, "running": c.State.Running, "status": c.State.Status, "exit_code": c.State.ExitCode, "task_id": c.Config.Labels["alertops.task"], "workspace_version": c.Config.Labels["alertops.assignment"]}, nil
}

// Start 使用稳定容器名，重启调度器只核对同一个容器，不重复启动已执行的 CLI。
func (d *DockerRuntime) Start(ctx context.Context, task Row) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	state, e := d.Inspect(ctx, task.S("container_name"))
	if e != nil {
		return e
	}
	if exists, _ := state["exists"].(bool); exists {
		if state.S("task_id") != task.S("id") || state.S("workspace_version") != task.S("workspace_version") {
			return errors.New("容器名已被其他任务占用")
		}
		if state.S("status") != "created" {
			return nil
		}
	} else {
		path := task.S("workspace_path")
		if _, e = canonicalDirectory(path); e != nil {
			return e
		}
		artifact := task.S("artifact_path")
		if !filepath.IsAbs(artifact) || overlaps(path, artifact) {
			return errors.New("产物目录必须独立于修复工作区")
		}
		args := []string{"create", "--name", task.S("container_name"), "--label", "alertops.task=" + task.S("id"), "--label", "alertops.assignment=" + task.S("workspace_version"), "--init", "--cpus", "2", "--memory", "4g", "--pids-limit", "512", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only", "--tmpfs", "/tmp:rw,size=512m", "--tmpfs", "/root/.codex:rw,size=256m", "--log-opt", "max-size=10m", "--log-opt", "max-file=3", "--add-host", "host.docker.internal:host-gateway", "--mount", "type=bind,source=" + path + ",target=/workspace", "--mount", "type=bind,source=" + artifact + ",target=/task", "--env", "ALERTOPS_RUNTIME_ENV=/task/runtime.env", task.S("runner_image")}
		if _, e = exec.CommandContext(ctx, "docker", args...).Output(); e != nil {
			return errors.New("Docker 创建失败，保留工作区等待核对")
		}
	}
	if _, e = exec.CommandContext(ctx, "docker", "start", task.S("container_name")).Output(); e != nil {
		return errors.New("Docker 启动结果未知，等待下一次核对")
	}
	return nil
}

// prepareRuntime 在首次容器调用前保存快照与令牌，服务崩溃后可继续同一启动过程。
func (a *App) prepareRuntime(ctx context.Context, task Row) error {
	var snapshot struct {
		Project Row   `json:"project"`
		Repos   []Row `json:"repos"`
	}
	if e := decodeJSON(task["context_snapshot"], &snapshot); e != nil {
		return e
	}
	if e := workspaceHealth(ctx, task.S("workspace_path"), snapshot.Repos); e != nil {
		return e
	}
	root, e := filepath.Abs(a.Config.ArtifactRoot)
	if e != nil || a.Config.ArtifactRoot == "" {
		return errors.New("未配置独立产物目录")
	}
	artifact := task.S("artifact_path")
	if !within(root, artifact) {
		return errors.New("任务产物目录不合法")
	}
	if e = os.MkdirAll(artifact, 0700); e != nil {
		return e
	}
	if e = os.Chmod(artifact, 0700); e != nil {
		return e
	}
	detail, e := a.roundDetail(ctx, task.I("alert_round_id"))
	if e != nil {
		return e
	}
	metadata, e := runtimeMetadata(ctx, task.S("workspace_path"), snapshot.Repos)
	if e != nil {
		return e
	}
	contextData := Row{"task_id": task.S("id"), "snapshot": task["context_snapshot"], "alert": detail, "runtime_metadata": metadata}
	if e = os.WriteFile(filepath.Join(artifact, "context.json"), []byte(encode(redact(contextData))), 0600); e != nil {
		return e
	}
	callback := a.Config.RunnerCallbackURL
	if callback == "" {
		callback = a.Config.BaseURL
	}
	if strings.ContainsAny(callback, "\n\r") || callback == "" {
		return errors.New("容器回调地址未配置")
	}
	env := "GIT_AUTHOR_NAME=AlertOps Codex\nGIT_AUTHOR_EMAIL=alertops-bot@localhost\nGIT_COMMITTER_NAME=AlertOps Codex\nGIT_COMMITTER_EMAIL=alertops-bot@localhost\nALERTOPS_URL=" + callback + "\nALERTOPS_TASK_ID=" + task.S("id") + "\nALERTOPS_TASK_TOKEN=" + task.S("execution_token") + "\n"
	gitCredentials := map[string]string{}
	for _, repo := range snapshot.Repos {
		if value := os.Getenv(repo.S("credential_ref")); value != "" {
			gitCredentials[repo.S("repo_code")] = value
		}
	}
	if e = os.WriteFile(filepath.Join(artifact, "git-credentials.json"), []byte(encode(gitCredentials)), 0600); e != nil {
		return e
	}
	credential := os.Getenv(snapshot.Project.S("credential_ref"))
	if credential != "" {
		if strings.ContainsAny(credential, "\n\r") {
			return errors.New("Codex API 凭据格式无效")
		}
		env += "CODEX_API_KEY=" + credential + "\n"
	}
	if e = os.WriteFile(filepath.Join(artifact, "runtime.env"), []byte(env), 0600); e != nil {
		return e
	}
	return nil
}

// RunSchedulerOnce 先核对遗留容器，再领取新任务；每项目最多三个固定槽位。
func (a *App) RunSchedulerOnce(ctx context.Context) error {
	if a.Runtime == nil {
		return nil
	}
	if err := os.MkdirAll(a.Config.ArtifactRoot, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(a.Config.ArtifactRoot, "scheduler.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil
		}
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	tasks, e := rows(ctx, a.DB, "SELECT * FROM alert_ai_task WHERE execution_status IN ('STARTING','RUNNING','FINALIZING') ORDER BY claimed_time,id")
	if e != nil {
		return e
	}
	for _, task := range tasks {
		if e = a.reconcileTask(ctx, task); e != nil {
			if save := update(ctx, a.DB, "alert_ai_task", task.I("id"), Row{"error_message": truncate(e.Error(), 2000)}); save != nil {
				return save
			}
		}
	}
	projects, e := rows(ctx, a.DB, "SELECT id FROM alert_project WHERE config_status='ENABLED' ORDER BY id")
	if e != nil {
		return e
	}
	for _, project := range projects {
		// 先隔离脏槽位，再领取 FIFO 任务；没有健康槽位时任务仍留在队列。
		repos, err := rows(ctx, a.DB, "SELECT * FROM alert_project_repo WHERE alert_project_id=? AND config_status='ENABLED'", project.I("id"))
		if err != nil {
			return err
		}
		idle, err := rows(ctx, a.DB, "SELECT * FROM alert_project_workspace WHERE alert_project_id=? AND workspace_status='READY' AND active_alert_ai_task_id=0", project.I("id"))
		if err != nil {
			return err
		}
		for _, w := range idle {
			if err = workspaceHealth(ctx, w.S("workspace_path"), repos); err != nil {
				if err = mustExec(ctx, a.DB, "UPDATE alert_project_workspace SET workspace_status='BLOCKED',blocked_reason=?,last_check_time=? WHERE id=? AND assignment_version=? AND active_alert_ai_task_id=0", truncate(err.Error(), 240), time.Now().UnixMilli(), w.I("id"), w.I("assignment_version")); err != nil {
					return err
				}
			}
		}
		for range 3 {
			task, e := a.ClaimTask(ctx, project.S("id"), "docker-local")
			if e != nil {
				return e
			}
			if task == nil {
				break
			}
			if e = a.prepareRuntime(ctx, task); e != nil {
				if e = a.finishTask(ctx, task, false, e.Error()); e != nil {
					return e
				}
				continue
			}
			if e = a.Runtime.Start(ctx, task); e != nil {
				if save := update(ctx, a.DB, "alert_ai_task", task.I("id"), Row{"error_message": truncate(e.Error(), 2000)}); save != nil {
					return save
				}
			}
		}
	}
	return nil
}

// reconcileTask 不以心跳超时判定退出，依据 Docker 事实收尾并补投已落盘业务事件。
func (a *App) reconcileTask(ctx context.Context, task Row) error {
	return a.withExternalLock(ctx, "runtime:"+task.S("id"), func() error {
		state, e := a.Runtime.Inspect(ctx, task.S("container_name"))
		if e != nil {
			return e
		}
		exists, _ := state["exists"].(bool)
		if !exists {
			if task.I("started_time") > 0 {
				return errors.New("已执行任务的容器缺失，保留目录占用待核对")
			}
			if _, e = os.Stat(filepath.Join(task.S("artifact_path"), "runtime.env")); e != nil {
				return a.finishTask(ctx, task, false, "容器创建前执行快照缺失，禁止自动重跑")
			}
			return a.Runtime.Start(ctx, task)
		}
		if state.S("task_id") != task.S("id") || state.S("workspace_version") != task.S("workspace_version") {
			return errors.New("容器标签与任务分配版本不一致")
		}
		if e = update(ctx, a.DB, "alert_ai_task", task.I("id"), Row{"container_id": state.S("container_id"), "heartbeat_time": time.Now().UnixMilli()}); e != nil {
			return e
		}
		if state.S("status") == "created" && task.I("started_time") == 0 {
			return a.Runtime.Start(ctx, task)
		}
		if running, _ := state["running"].(bool); running {
			return nil
		}
		if state.S("status") != "exited" && state.S("status") != "dead" {
			return errors.New("容器尚未确认退出")
		}
		artifact := task.S("artifact_path")
		if _, e = canonicalDirectory(artifact); e != nil || !within(a.Config.ArtifactRoot, artifact) {
			return errors.New("任务产物实际目录不在受控根目录内")
		}
		if _, ok := a.Runtime.(*DockerRuntime); ok {
			root, e := os.OpenRoot(artifact)
			if e != nil {
				return e
			}
			defer root.Close()
			temporary := ".container-log-" + nonce()
			f, e := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			defer root.Remove(temporary)
			cmd := exec.CommandContext(ctx, "docker", "logs", task.S("container_name"))
			cmd.Stdout, cmd.Stderr = f, f
			runErr := cmd.Run()
			closeErr := f.Close()
			if runErr != nil {
				return errors.New("保留容器日志失败，暂不释放目录")
			}
			if closeErr != nil {
				return closeErr
			}
			// 原子替换目录项，不打开容器可能放置的链接目标。
			if e = root.Rename(temporary, "container.log"); e != nil {
				return e
			}
		}
		// 文件来自受限任务容器；只接受当前任务同序号的既有事件协议。
		if data, e := readBoundedFile(filepath.Join(artifact, "events.jsonl"), 4<<20); e == nil {
			if len(data) > 4<<20 {
				return errors.New("业务事件产物超过限制")
			}
			for _, line := range strings.Split(string(data), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				var p Progress
				if e = json.Unmarshal([]byte(line), &p); e != nil {
					return a.finishTask(ctx, task, false, "业务事件产物损坏")
				}
				job, e := one(ctx, a.DB, "SELECT * FROM alert_job WHERE job_key=?", fmt.Sprintf("ai:%s:%d", task.S("id"), p.Sequence))
				if e != nil {
					return e
				}
				if job == nil {
					e = a.transaction(ctx, func(tx *sql.Tx) error {
						return a.enqueue(ctx, tx, "AI_PROGRESS_APPLY", fmt.Sprintf("ai:%s:%d", task.S("id"), p.Sequence), "TASK", task.I("id"), p.Sequence, p)
					})
					if e != nil {
						return e
					}
					job, e = one(ctx, a.DB, "SELECT * FROM alert_job WHERE job_key=?", fmt.Sprintf("ai:%s:%d", task.S("id"), p.Sequence))
					if e != nil {
						return e
					}
				}
				if e = a.applyProgressJob(ctx, job); e != nil {
					return a.finishTask(ctx, task, false, "最终结果或业务事件不满足协议："+e.Error())
				}
				if e = update(ctx, a.DB, "alert_job", job.I("id"), Row{"job_status": "SUCCEEDED"}); e != nil {
					return e
				}
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		fresh, e := one(ctx, a.DB, "SELECT * FROM alert_ai_task WHERE id=?", task.I("id"))
		if e != nil {
			return e
		}
		valid := fresh.S("execution_status") == "FINALIZING" && fresh.S("delivery_set_status") == "SEALED" && fresh.S("error_code") == "" && state.I("exit_code") == 0
		reason := fresh.S("error_message")
		if valid {
			reason = ""
		}
		if !valid && reason == "" {
			reason = "容器退出但缺少有效最终结果，或 CLI 非零退出"
		}
		return a.finishTask(ctx, fresh, valid, reason)
	})
}

// finishTask 仅在宿主确认未启动或已退出后调用，按原分配版本释放；脏目录进入 BLOCKED。
func (a *App) finishTask(ctx context.Context, task Row, success bool, reason string) error {
	var snapshot struct {
		Repos []Row `json:"repos"`
	}
	if e := decodeJSON(task["context_snapshot"], &snapshot); e != nil {
		return e
	}
	// 容器已确认停止，移除短期凭据；失败时保持占用，不能带着凭据释放目录。
	for _, name := range []string{"runtime.env", "git-credentials.json"} {
		if e := os.Remove(filepath.Join(task.S("artifact_path"), name)); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	health := workspaceHealth(ctx, task.S("workspace_path"), snapshot.Repos)
	state, blocked := "READY", ""
	if health != nil {
		state, blocked = "BLOCKED", truncate(health.Error(), 240)
	}
	execution := "FAILED"
	if success {
		execution = "SUCCEEDED"
	}
	if task.S("error_code") == "HUMAN_CLOSED" {
		execution = "SKIPPED"
	}
	return a.transaction(ctx, func(tx *sql.Tx) error {
		if _, e := one(ctx, tx, "SELECT id FROM alert_project WHERE id=? FOR UPDATE", task.I("alert_project_id")); e != nil {
			return e
		}
		w, e := one(ctx, tx, "SELECT * FROM alert_project_workspace WHERE id=? FOR UPDATE", task.I("alert_project_workspace_id"))
		if e != nil {
			return e
		}
		if w == nil || w.I("active_alert_ai_task_id") != task.I("id") || w.I("assignment_version") != task.I("workspace_version") {
			return fail(409, "工作区分配已变化，旧执行器不能释放")
		}
		r, e := one(ctx, tx, "SELECT * FROM alert_round WHERE id=? FOR UPDATE", task.I("alert_round_id"))
		if e != nil {
			return e
		}
		if e = update(ctx, tx, "alert_ai_task", task.I("id"), Row{"execution_status": execution, "active_marker": task.I("id"), "finished_time": time.Now().UnixMilli(), "error_message": reason, "execution_token_hash": ""}); e != nil {
			return e
		}
		if e = update(ctx, tx, "alert_project_workspace", w.I("id"), Row{"workspace_status": state, "active_alert_ai_task_id": 0, "blocked_reason": blocked, "last_check_time": time.Now().UnixMilli()}); e != nil {
			return e
		}
		if _, e = a.record(ctx, tx, r.I("id"), 0, "SYSTEM", "宿主执行器", "AI_RESULT", "RUNNER", "exit:"+task.S("id"), "AI 任务结束："+execution+" "+reason, Row{"workspace_status": state}); e != nil {
			return e
		}
		return a.touchRound(ctx, tx, r.I("id"))
	})
}

// RunWorkers 驱动持久事件、消息补偿和宿主执行器，取消时保留运行容器供重启核对。
func (a *App) RunWorkers(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if e := a.ProcessEvents(ctx, 100); e != nil {
			onError(e)
		}
		if e := a.ProcessJobs(ctx, 100); e != nil {
			onError(e)
		}
		if e := a.RunSchedulerOnce(ctx); e != nil {
			onError(e)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// readBoundedFile 防止异常产物耗尽宿主内存。
func readBoundedFile(path string, max int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("产物必须为普通文件，拒绝链接或特殊文件")
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("业务事件产物超过限制")
	}
	return data, nil
}

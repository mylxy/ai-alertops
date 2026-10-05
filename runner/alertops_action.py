#!/usr/bin/env python3
"""Codex 的有限业务工具：进度先落盘，Git 操作限定当前工作区配置仓库。"""
import argparse
import fcntl
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.error
import urllib.request

ARTIFACTS = Path(os.environ.get("ALERTOPS_ARTIFACTS", "/task"))
WORKSPACE = Path(os.environ.get("ALERTOPS_WORKSPACE", "/workspace")).resolve()
BASE = os.environ.get("ALERTOPS_URL", "").rstrip("/")
TASK = os.environ.get("ALERTOPS_TASK_ID", "")
TOKEN = os.environ.get("ALERTOPS_TASK_TOKEN", "")


def request(method, path, data=None):
    """只向部署指定的平台发送任务协议，网络失败不重跑 Codex。"""
    body = None if data is None else json.dumps(data, ensure_ascii=False).encode()
    req = urllib.request.Request(BASE + path, data=body, method=method, headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"})
    last = None
    for attempt in range(3):
        try:
            with urllib.request.urlopen(req, timeout=20) as response:
                return json.load(response)
        except urllib.error.HTTPError as exc:
            if exc.code < 500:
                raise RuntimeError(f"平台拒绝业务事件，HTTP {exc.code}") from None
            last = exc
        except (urllib.error.URLError, TimeoutError) as exc:
            last = exc
        time.sleep(min(attempt + 1, 3))
    raise RuntimeError("平台暂不可达，事件已经保存在本任务产物中") from last


def status():
    """查询当前任务，不访问其他任务或数据库。"""
    return request("GET", f"/api/ai/tasks/{TASK}")


def emit(event, summary, repos=None, business_result="", wait=True):
    """分配持久连续序号；同一文件锁覆盖落盘和发送顺序。"""
    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    with (ARTIFACTS / "sequence.lock").open("a+") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        spool = ARTIFACTS / "events.jsonl"
        previous = [json.loads(line) for line in spool.read_text().splitlines()] if spool.exists() else []
        sequence = len(previous) + 1
        data = {"sequence": sequence, "event": event, "summary": summary, "repos": repos or [], "business_result": business_result}
        with spool.open("a") as output:
            output.write(json.dumps(data, ensure_ascii=False) + "\n")
            output.flush()
            os.fsync(output.fileno())
        request("POST", f"/hook/ai/{TASK}", data)
        if wait:
            deadline = time.monotonic() + 90
            while time.monotonic() < deadline:
                current = status()
                if int(current["task"]["last_sequence"]) >= sequence:
                    return current
                time.sleep(0.5)
            raise RuntimeError("业务事件尚未应用，停止依赖此事件的后续操作")
        return None


def repo_path(repo):
    """拒绝符号链接逃逸和非独立 Git 克隆。"""
    raw = WORKSPACE / repo["relative_path"]
    path = raw.resolve(strict=True)
    if path == WORKSPACE or WORKSPACE not in path.parents or path != raw or not (path / ".git").is_dir():
        raise RuntimeError("仓库路径不是当前工作区的独立克隆")
    return path


def git(repo, *args):
    """所有 Git 参数由业务命令组成，禁止 shell 拼接。"""
    env = os.environ.copy()
    env["GIT_TERMINAL_PROMPT"] = "0"
    secrets_file = ARTIFACTS / "git-credentials.json"
    if secrets_file.exists():
        token = json.loads(secrets_file.read_text()).get(repo["repo_code"], "")
        if token:
            env["ALERTOPS_GIT_TOKEN"] = token
            env["GIT_ASKPASS"] = "/opt/alertops/git_askpass.py"
    result = subprocess.run(["git", "-C", str(repo_path(repo)), *args], capture_output=True, text=True, env=env, timeout=180)
    if result.returncode:
        raise RuntimeError("Git 操作失败：" + args[0] + "；现场保留在原工作区")
    return result.stdout.strip()


def locate(args):
    """定位后先登记必要仓库，再从最新 origin/master 建普通分支。"""
    codes = args.repos.split(",") if args.repos else []
    current = emit("ROOT_CAUSE_LOCATED", args.summary, [{"repo_code": code} for code in codes])
    for repo in current["repos"]:
        if repo["repo_code"] not in codes:
            continue
        if repo["base_revision"]:
            continue
        if git(repo, "remote", "get-url", "origin") != repo["git_url"]:
            raise RuntimeError("origin 与配置仓库不一致")
        if git(repo, "status", "--porcelain"):
            raise RuntimeError("仓库存在未保存修改，不能自动覆盖")
        git(repo, "fetch", "origin", "master")
        base = git(repo, "rev-parse", "origin/master")
        git(repo, "switch", "-c", current["task"]["branch_name"], "origin/master")
        emit("REPAIR_PROGRESS", "已从最新 master 建立修复分支", [{"repo_code": repo["repo_code"], "base_revision": base}])
    return status()


def verify(args):
    """记录已执行测试的结论与当前提交，不替代项目自己的测试。"""
    repo = next((r for r in status()["repos"] if r["repo_code"] == args.repo), None)
    if not repo:
        raise RuntimeError("尚未登记该必要仓库")
    head = git(repo, "rev-parse", "HEAD")
    if git(repo, "branch", "--show-current") != repo["branch_name"]:
        raise RuntimeError("当前分支不属于本任务")
    return emit("VERIFICATION_FINISHED", args.summary, [{"repo_code": args.repo, "head_revision": head, "verification_status": args.result, "verification_summary": args.summary}])


def deliver(args):
    """先核对本地已验证提交再推送；MR 创建由宿主持有的 Codeup 凭据完成。"""
    repo = next((r for r in status()["repos"] if r["repo_code"] == args.repo), None)
    if not repo or repo["verification_status"] != "PASSED":
        raise RuntimeError("只有验证通过的必要仓库可以交付")
    if git(repo, "status", "--porcelain") or git(repo, "rev-parse", "HEAD") != repo["head_revision"]:
        raise RuntimeError("验证后代码又有变化，需要重新验证")
    branch = repo["branch_name"]
    remote = git(repo, "ls-remote", "--heads", "origin", branch)
    if remote and remote.split()[0] != repo["head_revision"]:
        raise RuntimeError("远端修复分支提交不一致，禁止覆盖")
    if not remote:
        git(repo, "push", "origin", f"{repo['head_revision']}:refs/heads/{branch}")
    accepted = request("POST", f"/api/ai/tasks/{TASK}/merge-requests", {"repo_code": args.repo, "local_id": "", "request_key": args.key, "title": args.title, "description": args.description})
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        current = status()
        job = next((j for j in current.get("delivery_jobs", []) if j["job_key"] == accepted["job_key"]), None)
        if job and job["job_status"] == "SUCCEEDED":
            return current
        time.sleep(1)
    raise RuntimeError("MR 请求已经保存，外部结果待核对；不要换幂等键重建")


def main():
    """只开放定位、验证和交付三类有限操作。"""
    parser = argparse.ArgumentParser()
    commands = parser.add_subparsers(dest="command", required=True)
    p = commands.add_parser("located"); p.add_argument("--summary", required=True); p.add_argument("--repos", default="")
    p = commands.add_parser("verify"); p.add_argument("--repo", required=True); p.add_argument("--result", choices=["PASSED", "FAILED", "UNAVAILABLE"], required=True); p.add_argument("--summary", required=True)
    p = commands.add_parser("deliver"); p.add_argument("--repo", required=True); p.add_argument("--key", default="primary"); p.add_argument("--title", required=True); p.add_argument("--description", default="")
    args = parser.parse_args()
    result = {"located": locate, "verify": verify, "deliver": deliver}[args.command](args)
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(str(exc), file=sys.stderr)
        sys.exit(1)

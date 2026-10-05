"""真实临时 Git 和本地复现验收；平台/MR 协议为替身，不调用模型或 Codeup。"""
import argparse
import copy
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import alertops_action as action


class RepairWorkflowTest(unittest.TestCase):
    def test_reproduce_fix_verify_and_request_mr(self):
        """异常证据定位后修复实际代码，只有已验证提交可以交付。"""
        with tempfile.TemporaryDirectory() as temporary:
            # 准备：独占 bare 仓库和具有除零缺陷的 master，无外部网络。
            root = Path(temporary).resolve()
            workspace = root / "workspace"
            workspace.mkdir()
            artifacts = root / "artifacts"
            origin = root / "origin.git"
            repo = workspace / "repo"
            def git(*args):
                return subprocess.check_output(["git", "-C", str(repo), *args], text=True).strip()
            subprocess.run(["git", "init", "--bare", str(origin)], check=True, capture_output=True)
            subprocess.run(["git", "clone", str(origin), str(repo)], check=True, capture_output=True)
            git("config", "user.name", "本地验收")
            git("config", "user.email", "fixture@localhost")
            git("checkout", "-b", "master")
            (repo / "app.py").write_text("def average(total, count):\n    return total / count\n")
            (repo / "check.py").write_text("from app import average\nassert average(8, 2) == 4\nassert average(0, 0) == 0\n")
            git("add", ".")
            git("commit", "-m", "测试缺陷基线")
            git("push", "origin", "master")
            base = git("rev-parse", "HEAD")
            failed = subprocess.run(["python3", "-B", "check.py"], cwd=repo, capture_output=True, text=True)
            self.assertNotEqual(failed.returncode, 0)
            self.assertIn("ZeroDivisionError", failed.stderr)
            report = {"evidence": "ERROR ZeroDivisionError; " + failed.stderr, "location": "fixture/app.py:2", "reproduction": "python3 -B check.py: exit 1", "limitations": "仅本地受控样本，未读取生产日志"}
            (workspace / "diagnosis.json").write_text(json.dumps(report))
            state = {"task": {"last_sequence": 0, "branch_name": "hotfix-fixture-AL1"}, "repos": [{"repo_code": "fixture", "relative_path": "repo", "git_url": str(origin), "base_revision": "", "branch_name": "hotfix-fixture-AL1", "verification_status": ""}], "delivery_jobs": []}
            requests = []
            events = []
            def request(method, path, data=None):
                if method == "GET":
                    return copy.deepcopy(state)
                if path.endswith("merge-requests"):
                    requests.append(data)
                    state["delivery_jobs"] = [{"job_key": "local-mr", "job_status": "SUCCEEDED"}]
                    return {"job_key": "local-mr"}
                events.append(data)
                state["task"]["last_sequence"] = data["sequence"]
                for progress in data["repos"]:
                    state["repos"][0].update(progress)
                return {"accepted": True}
            with patch.multiple(action, WORKSPACE=workspace, ARTIFACTS=artifacts, TASK="local-task"), patch.object(action, "request", request):
                # 执行：实际建分支、修复、测试、提交，并调用平台交付协议。
                action.locate(argparse.Namespace(summary="零样本未保护除法", repos="fixture", evidence="diagnosis.json"))
                self.assertEqual(git("branch", "--show-current"), "hotfix-fixture-AL1")
                self.assertEqual(state["repos"][0]["base_revision"], base)
                deliver = argparse.Namespace(repo="fixture", key="primary", title="保护零样本", description="已通过本地复现和修复回归")
                with self.assertRaisesRegex(RuntimeError, "验证通过"):
                    action.deliver(deliver)
                (repo / "app.py").write_text("def average(total, count):\n    return total / count if count else 0\n")
                passed = subprocess.run(["python3", "-B", "check.py"], cwd=repo, capture_output=True, text=True)
                self.assertEqual(passed.returncode, 0, passed.stderr)
                git("add", "app.py")
                git("commit", "-m", "修复零样本除法")
                action.verify(argparse.Namespace(repo="fixture", result="PASSED", summary="python3 -B check.py: exit 0"))
                action.deliver(deliver)
                # 验证：原 master 不变，远端 hotfix 是验证提交，证据进入产物和业务记录。
                self.assertEqual(git("rev-parse", "origin/master"), base)
                self.assertEqual(git("ls-remote", "origin", "hotfix-fixture-AL1").split()[0], state["repos"][0]["head_revision"])
                self.assertEqual(json.loads((artifacts / "diagnosis.json").read_text()), report)
                self.assertIn("ZeroDivisionError", events[0]["summary"])
                self.assertEqual(len(requests), 1)
                self.assertEqual(requests[0]["request_key"], "primary")
                (repo / "app.py").write_text("changed after validation")
                with self.assertRaisesRegex(RuntimeError, "重新验证"):
                    action.deliver(deliver)
                self.assertEqual(len(requests), 1)

    def test_invalid_evidence_has_no_progress_or_git_effect(self):
        """缺失、非法、越界、链接、超限证据都在上报和 Git 操作前拒绝。"""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            workspace = root / "workspace"
            workspace.mkdir()
            (root / "outside.json").write_text("{}")
            (workspace / "invalid.json").write_text("{}")
            (workspace / "broken.json").write_text("not json")
            (workspace / "directory.json").mkdir()
            valid = {key: "证据" for key in ["evidence", "location", "reproduction", "limitations"]}
            (workspace / "valid.json").write_text(json.dumps(valid))
            (workspace / "large.json").write_text("x" * 12001)
            (workspace / "link.json").symlink_to(root / "outside.json")
            with patch.multiple(action, WORKSPACE=workspace, ARTIFACTS=root / "artifacts"), patch.object(action, "emit") as emit, patch.object(action, "git") as git:
                for filename in ["missing.json", "invalid.json", "large.json", "link.json", "../outside.json", "broken.json", "directory.json"]:
                    with self.subTest(evidence=filename):
                        with self.assertRaises((RuntimeError, FileNotFoundError, IsADirectoryError, json.JSONDecodeError)):
                            action.locate(argparse.Namespace(summary="结论", repos="fixture", evidence=filename))
                with self.assertRaisesRegex(RuntimeError, "长度限制"):
                    action.locate(argparse.Namespace(summary="中" * 5400, repos="fixture", evidence="valid.json"))
                emit.assert_not_called()
                git.assert_not_called()


if __name__ == "__main__":
    unittest.main()

"""校验设计稿的关键约束与可复现性；不替代业务服务或钉钉客户端验收。"""

import hashlib
import json
from datetime import datetime
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parent
GENERATED = ["log-alert-card-design.json", "design-preview-data.json", "卡片流程对照.md"]


def read_json(name):
    return json.loads((ROOT / name).read_text())


def walk(node):
    yield node
    for child in node.get("children", []):
        yield from walk(child)


def verify():
    # 防止只编辑生成文件，造成预览、源数据和文档不一致。
    before = {name: (ROOT / name).read_bytes() for name in GENERATED}
    subprocess.run([sys.executable, str(ROOT / "build_design.py")], check=True)
    assert all((ROOT / name).read_bytes() == data for name, data in before.items()), "生成结果已变化，请核对并保存新版本"

    flow = read_json("card-flow.json")
    editor = json.loads(read_json("log-alert-card-design.json")["editorData"])
    data = editor["mockData"]["cardData"]
    scenes = {item["id"]: item for item in flow["scenarios"]}
    assert len(scenes) == len(flow["scenarios"]), "场景 ID 重复"
    assert {s["code"] for s in flow["states"]} == {"new", "processing", "closed"}
    assert {s["phase"] for s in flow["states"]} == {"red", "orange", "green"}
    assert scenes["first"]["ai_active"] is False, "首次告警不能误称 AI 正在运行"
    assert scenes["start_failed"]["state"] == "processing"

    for scene in scenes.values():
        assert "can_close" not in scene, "不能按 AI/MR 场景另设关闭门槛"
        # 这里校验的是 2026 年模拟数据，业务接入必须保留完整时间戳。
        first = datetime.strptime("2026-" + scene.get("first_seen", data["first_seen"]), "%Y-%m-%d %H:%M")
        latest = datetime.strptime("2026-" + scene["latest"], "%Y-%m-%d %H:%M")
        assert int((latest - first).total_seconds() // 60) == scene["duration_minutes"] >= 0
        assert int(scene["count"]) >= 1
        assert all(note["author"] and note["created_at"] and note["body"].strip() for note in scene["notes"])
        assert scene["notes"] == sorted(scene["notes"], key=lambda n: n["created_at"])

    for name in ("manual_released_closed", "closed_ai_running", "closed_ai_finished"):
        assert scenes[name]["state"] == "closed"
    assert not scenes["manual_released_closed"]["merge_requests"], "无 MR 的人工关闭路径必须保留"
    assert scenes["closed_ai_running"]["ai_active"] is True
    assert len(scenes["closed_ai_finished"]["merge_requests"]) == 2
    recurrence = scenes["recurrence"]
    assert recurrence["state"] == "new" and recurrence["count"] == "1"
    assert not recurrence["notes"] and not recurrence["merge_requests"]
    assert recurrence["alert_id"] != data["alert_id"]
    assert recurrence["first_seen"] == recurrence["latest"] and recurrence["duration_minutes"] == 0

    nodes = list(walk(editor["schema"]["componentsTree"][0]))
    assert len({n["id"] for n in nodes}) == len(nodes), "组件 ID 重复"
    by_title = {n["title"]: n for n in nodes}
    operations = by_title["处理操作"]["props"]
    close = next(b for b in operations["buttons"]["list"] if b["text"]["content"] == "确认关闭")
    assert close["visible"]["valueType"] == "fixed" and close["visible"]["value"] is True
    conditions = operations["visible"]["condition"]["conditions"]
    assert {(c["variable"], c["value"]) for c in conditions} == {("view_open", "yes"), ("note_editor_open", "false")}
    assert "view_can_close" not in json.dumps(editor)

    for title in ("追加人工备注", "备注提交与取消"):
        conditions = by_title[title]["props"]["visible"]["condition"]["conditions"]
        assert ("view_open", "yes") in {(c["variable"], c["value"]) for c in conditions}
    assert editor["mockData"]["localData"]["note_editor_open"] is False

    trace = by_title["点击 Trace ID 复制"]["props"]
    assert trace["actionType"] == "copy" and trace["copyType"] == "common"
    assert trace["copyValue"]["content"] == "${view_trace_id}"
    assert by_title["点击复制完整 Trace ID"]["props"]["text"]["content"] == "${view_trace_id}"
    mr_rows = [n for n in nodes if n["title"].startswith("独立 MR 入口")]
    assert len(mr_rows) == 2 and len({n["props"]["url"]["variable"] for n in mr_rows}) == 2

    # 预览只允许本地交互或说明提示，不能夹带真实服务写入。
    actions = []
    for node in nodes:
        actions.append(node["props"].get("actionType", "none"))
        actions.extend(b.get("actionType", "none") for b in node["props"].get("buttons", {}).get("list", []))
    assert set(actions) <= {"none", "toast", "copy", "setLocalState"}

    print(f"通过：{len(scenes)} 个场景、{len(nodes)} 个原生组件；状态、关闭、备注、复制和新轮次约束一致。")
    print("模板 SHA256：" + hashlib.sha256((ROOT / GENERATED[0]).read_bytes()).hexdigest())


if __name__ == "__main__":
    verify()

"""复用已确认设计的官方组件，生成连接后端参数与 Stream 回调的部署模板。"""
import copy
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
REF = ROOT / "docs/design/dingtalk-log-alert"
nodes, maps = {}, {}


def collect(node):
    nodes.setdefault(node["componentName"], node)
    for child in node.get("children", []):
        collect(child)


for name in ["审批模板-reference.json", "交互组件的使用与本地更新-reference.json", "official-reference.json"]:
    data = json.loads(json.loads((REF / name).read_text())["editorData"])
    for item in data["schema"]["componentsTree"]:
        collect(item)
    maps.update({item["componentName"]: item for item in data["schema"]["componentsMap"]})

seq = 0


def dynamic(value):
    return {"type": "dynamicString", "content": value, "i18n": False}


def fixed(kind, value):
    return {"type": kind, "valueType": "fixed", "value": value, "variable": "", "variableType": "global"}


def visible(field=None, value=None):
    result = {"type": "dynamicVisible", "value": True, "valueType": "fixed", "condition": {"op": "and", "conditions": []}}
    if field:
        result.update(valueType="condition", condition={"op": "and", "conditions": [{"variable": field, "variableType": "global", "type": "variable", "op": "equal", "value": value, "valueType": "fixed", "valueVariableType": "global"}]})
    return result


def node(kind, title, props=None, children=None):
    global seq
    seq += 1
    result = copy.deepcopy(nodes[kind])
    result.update(id=f"alertops_{seq}", title=title, hidden=False, isLocked=False, condition=True, conditionGroup="")
    result.pop("children", None)
    result["props"].update(visible=visible(), actionType="none", marginLeft=16, marginRight=16, marginTop=10, marginBottom=0)
    result["props"].update(props or {})
    if children is not None:
        result["children"] = children
    return result


def text(content, bold=False):
    return node("BaseText", content, {"text": dynamic(content), "bold": bold, "fontSizeType": "Custom", "customFontSize": 14, "customFontLineHeight": 20, "color": "black", "maxLine": fixed("dynamicNumber", 20), "enableIcon": False})


def button(label, action, vis=None):
    return node("SingleButton", label, {"text": dynamic(label), "actionType": action, "status": fixed("dynamicSelect", "normal"), "color": fixed("dynamicSelect", "blue"), "visible": vis or visible(), "params": [], "urlType": "all", "url": {"type": "dynamicLink", "valueType": "variable", "variable": "h5_url", "variableType": "global", "value": ""}})


def build(kind):
    children = []
    title = {"LOG": "日志告警", "METRIC": "指标告警", "NOTICE": "业务提示", "TOPBOX": "本群未完成告警"}[kind]
    for phase, color in [("red", "#FFF0ED"), ("orange", "#FFF7E8"), ("green", "#EDF8F0"), ("blue", "#EEF5FF")]:
        children.append(node("Grid", title + "状态色", {"direction": "vertical", "hasBackground": True, "hasBorder": False, "backgroundType": "Custom", "backgroundColor": color, "darkModeBackgroundColor": "#242932", "cornerRadius": 8, "visible": visible("phase", phase)}, [text(title + " · ${status}", True), text("${card_title}", True)]))
    children += [text("${context}"), text("${time_summary}"), text("${evidence}")]
    if kind in ("LOG", "METRIC"):
        if kind == "LOG":
            children.append(text("${ai_summary}"))
            children.append(node("Markdown", "动态合并请求", {"content": {"type": "dynamicMarkdown", "valueType": "variable", "variable": "mr_markdown", "variableType": "global", "value": ""}}))
        children += [text("${result}", True), text("人工备注 · ${note_count} 条", True)]
        for n in range(1, 4):
            children += [text(f"${{note_{n}_meta}}"), text(f"${{note_{n}_body}}")]
        # 文本只写本地状态；只有显式提交才发出带幂等身份的 Stream 请求。
        children.append(node("Input", "备注或处理结果", {"id": "content", "title": dynamic("备注或处理结果（必填）"), "placeholder": dynamic("填写排查进展或处理结果"), "currentValue": dynamic("${draft}"), "status": fixed("dynamicSelect", "normal"), "actionType": "setLocalState", "localVarAction": {"type": "variableValue", "variable": "draft", "variableType": "global", "varType": "string"}, "params": [], "visible": visible("is_open", "yes")}))
        for label, action in [("提交备注", "note")] + ([("标记已处理", "handle")] if kind == "LOG" else []):
            item = button(label, "request", visible("is_open", "yes"))
            item["props"]["params"] = [{"id": "action", "name": "action", "type": "fixed", "value": action, "variableType": "global"}, {"id": "content", "name": "content", "type": "variable", "variable": "draft", "variableType": "global"}, {"id": "operation_key", "name": "operation_key", "type": "variable", "variable": "operation_key", "variableType": "global"}]
            children.append(item)
        cancel = button("取消填写", "setLocalState", visible("is_open", "yes"))
        cancel["props"].update(enableCustomLocalData=True, customLocalData=json.dumps({"draft": ""}))
        children += [cancel, text("告警编号 ${alert_no}")]
    if kind == "TOPBOX":
        children = [text("本群未完成告警", True), text("日志 ${log_count} · 待处理 ${log_pending} / 处理中 ${log_processing}"), text("指标 ${metric_count} · 待处理 ${metric_pending} / 处理中 ${metric_processing}")]
    if kind != "NOTICE":
        children.append(button("打开处理工作台 / 完整历史 ↗", "url"))
    root = node("Card", title, {"marginLeft": 0, "marginRight": 0}, children)
    fields = "title status body h5_url alert_no version lastMessage operation_key card_title kind phase is_open context time_summary evidence ai_summary result note_count mr_markdown total_count log_count log_pending log_processing metric_count metric_pending metric_processing count".split()
    fields += [f"note_{n}_{part}" for n in range(1, 4) for part in ["meta", "body"]]
    variables = [{"id": field, "name": field, "private": field == "operation_key", "type": "string", "description": "由告警平台生成", "editorVarType": "variables"} for field in fields]
    editor = {"schemaVersion": "3.0.0", "schema": {"version": "1.0.0", "componentsMap": list(maps.values()), "componentsTree": [root], "i18n": {}}, "mockData": {"cardData": {field: "" for field in fields}, "cardPrivateData": {}, "localData": {"draft": ""}, "richTextData": {}}, "editVersion": 0, "customWidgetInfo": "", "useCustomWidgetInfo": False, "variableList": variables, "formList": [], "expList": [], "localList": [{"id": "draft", "name": "draft", "type": "string", "private": False, "editorVarType": "localList", "description": "仅在客户端暂存的草稿"}], "hsfList": [], "lwpList": [], "extension": {"extendType": "NORMAL", "fileTypeList": []}}
    editor["mockData"]["cardData"].update(status="待处理", phase="red", kind=kind, is_open="yes", card_title="部署模板预览", note_count="0", operation_key="preview-only", h5_url="https://example.invalid")
    output = {"editorData": json.dumps(editor, ensure_ascii=False), "widgetInfo": "", "type": "onebox" if kind == "TOPBOX" else "im", "mode": "card"}
    (Path(__file__).parent / f"{kind.lower()}.json").write_text(json.dumps(output, ensure_ascii=False, indent=2) + "\n")


if __name__ == "__main__":
    for card in ["LOG", "METRIC", "NOTICE", "TOPBOX"]:
        build(card)

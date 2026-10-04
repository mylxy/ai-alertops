"""生成只含备注与监控恢复的指标卡设计预览，不接入业务回调。"""
import copy
import json
import sys
from pathlib import Path

if len(sys.argv) != 2:
    raise SystemExit('用法：python3 build_preview.py <官方多 Tab 页.json 路径>')
source = json.loads(Path(sys.argv[1]).read_text())
editor = json.loads(source['editorData'])
prototypes = {}


def collect(node):
    prototypes.setdefault(node['componentName'], node)
    for child in node.get('children', []):
        collect(child)


collect(editor['schema']['componentsTree'][0])
sequence = 0


def dynamic(kind, value, variable=None):
    return {'type': kind, 'valueType': 'variable' if variable else 'fixed',
            'value': value, 'variable': variable or '', 'variableType': 'global'}


def string(value):
    return {'type': 'dynamicString', 'content': value, 'i18n': False}


def visible(*conditions):
    return {'type': 'dynamicVisible', 'value': True,
            'valueType': 'condition' if conditions else 'fixed',
            'condition': {'op': 'and', 'conditions': [
                {'variable': variable, 'op': op, 'value': value,
                 'variableType': 'global', 'type': 'variable',
                 'valueType': 'fixed', 'valueVariableType': 'global'}
                for variable, op, value in conditions]}}


def node(component, title, props, children=None):
    global sequence
    sequence += 1
    result = {'componentName': component, 'id': f'node_metric_{sequence}',
              'title': title, 'props': props, 'hidden': False, 'isLocked': False,
              'condition': True, 'conditionGroup': ''}
    if children is not None:
        result['children'] = children
    return result


def text(value, *, size=13, color='common_level1_base_color', color_var=None, bold=False, margin=0):
    props = copy.deepcopy(prototypes['BaseText']['props'])
    props.update(text=string(value), fontSizeType='Custom', styleType='custom',
                 customFontSize=size, customFontLineHeight=size + 7,
                 color=dynamic('dynamicColor', color, color_var), bold=bold,
                 autoWidth=False, gravity='left', maxLine=dynamic('dynamicNumber', 20),
                 visible=visible(), margin=-2, marginLeft=0, marginRight=0,
                 marginTop=margin, marginBottom=0)
    return node('BaseText', value[:30], props)


def grid(children, *, background='common_fg_color', dark_background=None, padding=16, margin=0):
    props = copy.deepcopy(prototypes['Grid']['props'])
    props.update(direction='vertical', isAutoWidth=False, width=100, isFixedWidth=False,
                 isAutoHeight=True, childGravity='leftTop', visible=visible(),
                 hasBackground=True, backgroundType='Standard',
                 standardBackgroundColor=dynamic('dynamicColor', 'common_fg_color'),
                 hasHoverBackground=False, hasGradientBackground=False,
                 enableClickEvent=False, actionType='url', hasBorder=False,
                 cornerRadius=8, cornerRadiusLeftTop=8, cornerRadiusRightTop=8,
                 cornerRadiusRightBottom=8, cornerRadiusLeftBottom=8,
                 margin=0, marginLeft=0, marginRight=0, marginTop=margin, marginBottom=0)
    if dark_background:
        props.update(backgroundType='Custom', backgroundColor=background,
                     darkModeBackgroundColor=dark_background)
    else:
        props['standardBackgroundColor'] = dynamic('dynamicColor', background)
    for side in ('Top', 'Right', 'Bottom', 'Left'):
        props['padding' + side] = dynamic('dynamicNumber', padding)
    return node('Grid', '信息分组', props, children)


def panel(children):
    result = grid(children, background='#F5F7FA', dark_background='#242932', padding=12, margin=12)
    result['props'].update(paddingTop=dynamic('dynamicNumber', 10),
                           paddingBottom=dynamic('dynamicNumber', 10))
    return result


def local_action(variable, value):
    return {'actionType': 'setLocalState', 'enableCustomLocalData': False,
            'localVarAction': {'type': 'variableValue', 'variable': variable,
                               'variableType': 'global', 'varType': 'string'},
            'stringLocalValue': string(value)}


def button(label, variable, value, *, color='gray', status_var=None):
    props = copy.deepcopy(prototypes['SingleButton']['props'])
    props.update(text=string(label), status=dynamic('dynamicSelect', 'normal', status_var),
                 color=dynamic('dynamicSelect', color), enableIcon=False, visible=visible(),
                 params=[], events=[], transformToEventChain=False,
                 dynamicEventVar={'type': 'variableValue', 'variableType': 'global', 'variable': ''},
                 autoWidth=False, fixWidth=False, fixedWidth=0, margin=-2,
                 marginLeft=0, marginRight=0, marginTop=8, marginBottom=0)
    props.update(local_action(variable, value))
    return node('SingleButton', label, props)


def note_input():
    props = copy.deepcopy(prototypes['Input']['props'])
    props.update(id=string('metric_note'), title=string('备注'),
                 placeholder=string('记录服务器或采集规则的处理情况'),
                 currentValue=string('${note_draft}'), message=string('请输入备注'),
                 status=dynamic('dynamicSelect', 'normal'), visible=visible(),
                 actionType='setLocalState', params=[],
                 localVarAction={'type': 'variableValue', 'variable': 'note_draft',
                                 'variableType': 'global', 'varType': 'string'},
                 keyOfDynamicObject=string(''), inlineMode=True, textArea=True,
                 minRows=dynamic('dynamicNumber', 2), maxRows=dynamic('dynamicNumber', 4),
                 margin=-2, marginLeft=0, marginRight=0, marginTop=10, marginBottom=0)
    return node('Input', '备注输入', props)


# 监控状态与备注独立：保存备注不会把 firing 改为 resolved。
expressions = {
    'status_label': 'monitor_state == "resolved" ? "已恢复" : (saved_note ? "已备注 · 等待恢复" : "告警中")',
    'status_phase': 'monitor_state == "resolved" ? "green" : (saved_note ? "orange" : "red")',
    'metric_color': 'monitor_state == "resolved" ? "common_green1_color" : "common_red1_color"',
    'metric_value': 'monitor_state == "resolved" ? "43.2%" : "92.6%"',
    'time_summary': 'monitor_state == "resolved" ? "首次 10:02 · 恢复 10:32\\n持续 30 分钟" : "首次 10:02 · 最近接收 10:20\\n持续 18 分钟"',
    'monitor_hint': 'monitor_state == "resolved" ? "监控已确认恢复，本轮告警自动结束。" : "指标仍超阈值，等待监控恢复。"',
    'save_status': 'note_draft ? "normal" : "disabled"',
}

note_details = panel([
    text('备注', size=13, bold=True),
    text('${saved_note}', size=13, margin=3),
    text('林值班（模拟） · 10:21', size=11, color='common_level2_base_color', margin=3),
])
note_details['props']['visible'] = visible(('saved_note', 'notEqual', ''))

add_note = button('添加备注', 'note_open', 'editing', color='blue')
add_note['props']['visible'] = visible(('saved_note', 'equal', ''),
                                      ('note_open', 'notEqual', 'editing'))
note_editor = [
    note_input(),
    button('提交备注', 'saved_note', '${note_draft}', color='blue', status_var='save_status'),
]
for component in note_editor:
    component['props']['visible'] = visible(('saved_note', 'equal', ''),
                                           ('note_open', 'equal', 'editing'))

# 与日志告警卡共用视觉规格：阶段色标题栏、白色正文、灰色信息块。
headers = []
for phase, light, dark in (
    ('red', '#C53D43', '#A83139'),
    ('orange', '#B56913', '#8D5117'),
    ('green', '#21865C', '#1A6D4B'),
):
    heading = [
        text('指标告警 · ${status_label}', size=12, bold=True),
        text('CPU 使用率过高', size=20, bold=True, margin=6),
    ]
    for component in heading:
        component['props'].update(fontColorType='Custom',
                                   customLightColor=dynamic('dynamicColor', '#FFFFFF'),
                                   customDarkColor=dynamic('dynamicColor', '#FFFFFF'))
    header = grid(heading, background=light, dark_background=dark)
    header['props'].update(visible=visible(('status_phase', 'equal', phase)),
                           paddingTop=dynamic('dynamicNumber', 14),
                           cornerRadiusLeftBottom=0, cornerRadiusRightBottom=0)
    headers.append(header)

preview_controls = panel([
    text('设计预览 · 模拟数据', size=11, color='common_level2_base_color'),
    button('模拟监控恢复', 'monitor_state', 'resolved'),
])

card_surface = grid([
    text('prod-demo · app-node-07', size=12, color='common_level2_base_color'),
    panel([
        text('${metric_value}', size=28, color_var='metric_color', bold=True),
        text('阈值 ≥ 85% · 连续 5 分钟', size=12, color='common_level2_base_color', margin=3),
        text('${time_summary}', size=12, color='common_level2_base_color', margin=6),
    ]),
    text('cpu_usage_percent · cpu=all', size=12, color='common_level2_base_color', margin=10),
    text('${monitor_hint}', size=13, margin=10),
    note_details,
    add_note,
    *note_editor,
    preview_controls,
])
card_surface['props'].update(paddingTop=dynamic('dynamicNumber', 12),
                             cornerRadiusLeftTop=0, cornerRadiusRightTop=0)

root = copy.deepcopy(prototypes['Card'])
root.update(id='node_metric_card', title='指标告警 · 备注与自动恢复', children=[*headers, card_surface])
root['props'].update(paddingLeft=0, paddingRight=0, paddingTop=0, paddingBottom=0,
                     enableClickEvent=False, enableOnAppear=False,
                     autoFoldConfig={'needFold': False, 'heightLimit': 480,
                                     'foldStatusLocalDataKey': '_cardFoldStatusLocalDataKey'})
editor['schema']['componentsTree'] = [root]
used = {'Card', 'Grid', 'BaseText', 'Input', 'SingleButton'}
editor['schema']['componentsMap'] = [entry for entry in editor['schema']['componentsMap'] if entry['componentName'] in used]
editor.update(variableList=[], formList=[], customContextList=[],
              expList=[{'private': False, 'type': 'string', 'id': key, 'name': key,
                        'expContent': value, 'description': '模拟场景展示', 'editorVarType': 'expList'}
                       for key, value in expressions.items()],
              localList=[{'name': key, 'id': key, 'private': False, 'type': 'string',
                          'description': '仅用于设计预览', 'editorVarType': 'localList'}
                         for key in ('monitor_state', 'note_open', 'note_draft', 'saved_note')],
              mockData={'cardData': {}, 'cardPrivateData': {},
                        'localData': {'monitor_state': 'firing', 'note_open': '',
                                      'note_draft': '', 'saved_note': ''}},
              extension={'extendType': 'NORMAL', 'fileTypeList': []},
              customWidgetInfo='', useCustomWidgetInfo=False, hsfList=[], lwpList=[], pageData={})


def validate(current):
    props = current['props']
    if current['componentName'] in ('SingleButton', 'Input'):
        assert props['actionType'] == 'setLocalState'
        assert not props.get('events')
    for child in current.get('children', []):
        validate(child)


validate(root)
assert sequence + 1 <= 40
serialized = json.dumps(editor, ensure_ascii=False)
assert not any(term in serialized for term in ('认领', '人工关闭', 'close_confirm', '静默'))
output = {'editorData': serialized, 'widgetInfo': source['widgetInfo'], 'type': 'im', 'mode': 'card'}
destination = Path(__file__).with_name('metric-alert-card.json')
destination.write_text(json.dumps(output, ensure_ascii=False, indent=2))
print(f'已生成 {destination.name}，{sequence + 1} 个原生组件；仅备注操作和监控自动恢复。')

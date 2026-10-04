"""生成只含备注与监控恢复的指标卡设计预览，不接入业务回调。"""
import copy
import json
import re
import sys
from pathlib import Path

if len(sys.argv) > 2:
    raise SystemExit('用法：python3 build_preview.py [官方组件样例.json 路径]')
reference = Path(sys.argv[1]) if len(sys.argv) == 2 else Path(__file__).parents[2] / 'docs/design/dingtalk-log-alert/交互组件的使用与本地更新-reference.json'
source = json.loads(reference.read_text())
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


# 监控状态与人工进展分别维护；标题统一为待处理、处理中、已恢复。
expressions = {
    'status_label': 'monitor_state == "resolved" ? "已恢复" : (getLength(note_bodies) > 0 ? "处理中" : "待处理")',
    'status_phase': 'monitor_state == "resolved" ? "green" : (getLength(note_bodies) > 0 ? "orange" : "red")',
    'metric_color': 'monitor_state == "resolved" ? "common_green1_color" : "common_red1_color"',
    'metric_value': 'monitor_state == "resolved" ? "43.2%" : "92.6%"',
    'time_summary': 'monitor_state == "resolved" ? "首次 10:02 · 恢复 10:32\\n持续 30 分钟" : "首次 10:02 · 最近接收 10:20\\n持续 18 分钟"',
    'monitor_hint': 'monitor_state == "resolved" ? "监控已确认恢复，本轮告警自动结束。" : "指标仍超阈值，等待监控恢复。"',
}
nonblank = 'toStr(note_draft)'
for whitespace in [' ', '\t', '\r', '\n', '\u3000', '\u00a0']:
    nonblank = 'textReplace(' + nonblank + ',' + json.dumps(whitespace) + ',"","1")'
expressions.update({
    'can_submit': 'monitor_state != "resolved" && note_open == "editing" && ' + nonblank + ' != ""',
    'save_status': 'can_submit ? "normal" : "disabled"',
    'note_count': 'toStr(getLength(note_bodies))',
    'has_notes': 'getLength(note_bodies) > 0 ? "yes" : "no"',
    'next_notes': 'can_submit ? arrayAppend(note_bodies,note_draft) : note_bodies',
    'next_metas': 'can_submit ? arrayAppend(note_metas,"演示用户 · 10-05 10:21（模拟）") : note_metas',
    'next_editor': 'can_submit ? "" : note_open',
    'next_draft': 'can_submit ? "" : note_draft',
})
note_children = [text('备注 · ${note_count} 条', size=13, bold=True)]
for index in range(3):
    prefix = f'note_{index}'
    expressions[prefix + '_visible'] = f'getLength(note_bodies) > {index} ? "yes" : "no"'
    expressions[prefix + '_body'] = f'getLength(note_bodies) > {index} ? substr(arrayGet(note_bodies,getLength(note_bodies)-1-{index}),0,100) : ""'
    expressions[prefix + '_meta'] = f'getLength(note_metas) > {index} ? arrayGet(note_metas,getLength(note_metas)-1-{index}) : ""'
    for component in [text('${' + prefix + '_body}', size=13, margin=8),
                      text('${' + prefix + '_meta}', size=11, color='common_level2_base_color', margin=3)]:
        component['props']['visible'] = visible((prefix + '_visible', 'equal', 'yes'))
        note_children.append(component)
history_link = grid([text('查看完整处理记录 ↗', size=12, color='common_link_color')], padding=0, margin=12)
history_link['props'].update(enableClickEvent=True, actionType='toast',
                            toastContent=string('设计预览：此处查看本轮全部备注和恢复记录，正式接入处理记录页面。'), toastType=1)
note_children.append(history_link)
note_details = panel(note_children)
note_details['props']['visible'] = visible(('has_notes', 'equal', 'yes'))

add_note = button('添加备注', 'note_open', 'editing', color='blue')
add_note['props']['visible'] = visible(('monitor_state', 'notEqual', 'resolved'),
                                      ('note_open', 'notEqual', 'editing'))
save_note = button('提交备注', 'note_open', '', color='blue', status_var='save_status')
save_note['props'].update(enableCustomLocalData=True, customLocalData=json.dumps({
    'note_bodies': '${next_notes}', 'note_metas': '${next_metas}',
    'note_open': '${next_editor}', 'note_draft': '${next_draft}',
}, ensure_ascii=False))
cancel_note = button('取消', 'note_open', '')
cancel_note['props'].update(enableCustomLocalData=True,
                           customLocalData=json.dumps({'note_open': '', 'note_draft': ''}))
note_editor = [
    note_input(),
    save_note, cancel_note,
]
for component in note_editor:
    component['props']['visible'] = visible(('monitor_state', 'notEqual', 'resolved'),
                                           ('note_open', 'equal', 'editing'))

# 与日志告警卡共用视觉规格：阶段色标题栏、白色正文、灰色信息块。
headers = []
for phase, light, dark in (
    ('red', '#C73E47', '#A83139'),
    ('orange', '#AC6817', '#8D5117'),
    ('green', '#21865C', '#1A6D4B'),
):
    heading = [
        text('指标告警 · ${status_label}', size=12, bold=True),
        text('CPU 使用率过高', size=19, bold=True, margin=5),
    ]
    for component in heading:
        component['props'].update(fontColorType='Custom',
                                   customLightColor=dynamic('dynamicColor', '#FFFFFF'),
                                   customDarkColor=dynamic('dynamicColor', '#FFFFFF'))
    header = grid(heading, background=light, dark_background=dark)
    header['props'].update(visible=visible(('status_phase', 'equal', phase)),
                           paddingTop=dynamic('dynamicNumber', 13),
                           paddingBottom=dynamic('dynamicNumber', 14),
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
# 原生表达式依赖在生成阶段展开，与日志卡使用相同处理方式。
expanded = {}
def expand_expression(name):
    if name not in expanded:
        parts = re.split(r'("(?:\\.|[^"\\])*"|\'(?:\\.|[^\'\\])*\')', expressions[name])
        for index in range(0, len(parts), 2):
            parts[index] = re.sub(r'\b[A-Za-z_][A-Za-z_0-9]*\b', lambda m: '(' + expand_expression(m[0]) + ')' if m[0] in expressions else m[0], parts[index])
        expanded[name] = ''.join(parts)
    return expanded[name]
expression_types = {'can_submit': 'boolean', 'next_notes': 'stringArray', 'next_metas': 'stringArray'}
editor.update(variableList=[], formList=[], customContextList=[],
              expList=[{'private': False, 'type': expression_types.get(key, 'string'), 'id': key, 'name': key,
                        'expContent': expand_expression(key), 'description': '模拟场景展示', 'editorVarType': 'expList'}
                       for key, value in expressions.items()],
              localList=[{'name': key, 'id': key, 'private': False, 'type': 'stringArray' if key in ('note_bodies', 'note_metas') else 'string',
                          'description': '仅用于设计预览', 'editorVarType': 'localList'}
                         for key in ('monitor_state', 'note_open', 'note_draft', 'note_bodies', 'note_metas')],
              mockData={'cardData': {}, 'cardPrivateData': {},
                        'localData': {'monitor_state': 'firing', 'note_open': '',
                                      'note_draft': '', 'note_bodies': [], 'note_metas': []}},
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

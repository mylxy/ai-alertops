"""保留已确认设计的原生组件，仅替换预览数据和操作绑定。"""
import copy
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DESIGN = ROOT / 'docs/design/dingtalk-log-alert'


def load(path):
    return json.loads(json.loads(path.read_text())['editorData'])


def walk(node):
    yield node
    for child in node.get('children', []):
        yield from walk(child)


def string(value):
    return {'type': 'dynamicString', 'content': value, 'i18n': False}


def fixed(kind, value):
    return {'type': 'dynamic' + kind, 'valueType': 'fixed', 'value': value,
            'variable': '', 'variableType': 'global'}


def visible(*conditions):
    return {'type': 'dynamicVisible', 'value': True, 'valueType': 'condition' if conditions else 'fixed',
            'condition': {'op': 'and', 'conditions': [{'variable': key, 'variableType': 'global',
            'type': 'variable', 'op': 'equal', 'value': value, 'valueType': 'fixed',
            'valueVariableType': 'global'} for key, value in conditions]}}


def clear_preview_feedback(props):
    for key in ('successCondition', 'successToast', 'failureToast', 'localVarAction'):
        props.pop(key, None)


def local(props, mode):
    clear_preview_feedback(props)
    props.update(actionType='setLocalState', enableCustomLocalData=True,
                 customLocalData=json.dumps({'editor_mode': mode, 'draft': '', 'edit_key': '${operation_key}'}, ensure_ascii=False))


def request(props, action):
    clear_preview_feedback(props)
    props.pop('customLocalData', None)
    props.update(actionType='request', enableCustomLocalData=False, events=[], transformToEventChain=False,
                 params=[{'id': name, 'name': name, 'type': kind, **values, 'variableType': 'global'}
                         for name, kind, values in [('action', 'fixed', {'value': action}),
                         ('content', 'variable', {'variable': 'draft'}),
                         ('operation_key', 'variable', {'variable': 'operation_key'})]])


def link(props):
    props.update(actionType='url', enableClickEvent=True, urlType='all',
                 url={'type': 'dynamicLink', 'valueType': 'variable', 'variable': 'h5_url', 'variableType': 'global', 'value': ''})


def rename(value, mapping):
    if isinstance(value, dict):
        return {key: rename(item, mapping) for key, item in value.items()}
    if isinstance(value, list):
        return [rename(item, mapping) for item in value]
    if isinstance(value, str):
        return re.sub(r'\b[A-Za-z_][A-Za-z_0-9]*\b', lambda m: mapping.get(m[0], m[0]), value)
    return value


def log_template():
    editor = load(DESIGN / 'log-alert-card-design.json')
    root = editor['schema']['componentsTree'][0]
    root['children'] = [n for n in root['children'] if n['title'] != '仅设计预览']
    mapping = {'view_label': 'status', 'title': 'card_title', 'view_phase': 'phase', 'view_open': 'is_open',
               'view_count': 'count', 'view_duration': 'duration', 'view_first_seen': 'first_seen',
               'view_latest_compact': 'latest_seen', 'view_trace_id': 'trace_id', 'view_has_trace': 'has_trace',
               'view_heading': 'progress_heading', 'view_body': 'progress_body', 'view_has_mrs': 'has_mrs',
               'view_mr_progress': 'mr_progress', 'view_note_count': 'note_count', 'view_notes_empty': 'notes_empty',
               'view_history_link': 'history_label', 'view_alert_id': 'alert_no', 'handling_note': 'draft'}
    for index in range(1, 4):
        for field in ('body', 'meta', 'visible'):
            mapping[f'view_note_{index}_{field}'] = f'note_{index}_{field}'
    root = rename(root, mapping)
    for n in walk(root):
        p = n['props']
        if n['componentName'] == 'ButtonList':
            for b in p['buttons']['list']:
                label = b['text']['content']
                if label in ('提交备注', '提交并完成'):
                    request(b, 'note' if label == '提交备注' else 'handle')
                else:
                    local(b, {'添加备注': 'note', '标记已处理': 'complete'}.get(label, ''))
        if n['title'] == '查看完整处理记录':
            link(p)
        if n['title'] == '按仓库查看合并请求':
            prototype = n['children'][1]
            n['children'] = n['children'][:1]
            for index in range(1, 9):
                row = rename(copy.deepcopy(prototype), {'view_mr_1_' + f: f'mr_{index}_{f}' for f in ('label', 'status', 'url', 'visible', 'message')})
                for child in walk(row):
                    child['id'] += '_mr_' + str(index)
                row['props']['actionType'] = 'url'
                n['children'].append(row)
    editor['schema']['componentsTree'] = [root]
    return editor


def metric_template():
    editor = load(ROOT / 'design/metric-alert-card/metric-alert-card.json')
    root = editor['schema']['componentsTree'][0]
    surface = root['children'][-1]
    surface['children'].pop()  # 原设计的模拟恢复按钮不进入运行模板。
    mapping = {'status_label': 'status', 'status_phase': 'phase', 'note_draft': 'draft'}
    for index in range(3):
        for field in ('body', 'meta', 'visible'):
            mapping[f'note_{index}_{field}'] = f'note_{index+1}_{field}'
    root = rename(root, mapping)
    for n in walk(root):
        p = n['props']
        if n['componentName'] == 'BaseText':
            text = p['text']['content']
            p['text'] = string({'CPU 使用率过高': '${card_title}', 'prod-demo · app-node-07': '${context}',
                              '阈值 ≥ 85% · 连续 5 分钟': '${metric_rule}', 'cpu_usage_percent · cpu=all': '${metric_key}'}.get(text, text))
        if p.get('actionType') == 'toast':
            link(p)
        if n['componentName'] in ('SingleButton', 'Input'):
            p['visible'] = visible(('is_open', 'yes'), ('view_editor', '' if n['title'] == '添加备注' else 'note'))
            if n['title'] == '提交备注':
                request(p, 'note')
                p['status'] = fixed('Select', 'normal')
            elif n['componentName'] == 'SingleButton':
                local(p, 'note' if n['title'] == '添加备注' else '')
    editor['schema']['componentsTree'] = [root]
    return editor


def notice_template():
    editor = log_template()
    root = editor['schema']['componentsTree'][0]
    head, context, panel = copy.deepcopy(root['children'][0]), copy.deepcopy(root['children'][3]), copy.deepcopy(root['children'][4])
    head['props'].update(visible=visible(), backgroundColor='#6F7C35', darkModeBackgroundColor='#56622B')
    head['children'][0]['props']['text'] = string('NOTICE')
    head['children'][1]['props'].update(text=string('业务提示'), customFontSize=24, customFontLineHeight=30)
    for side in ('Top', 'Bottom', 'Left', 'Right'):
        head['props']['padding'+side] = fixed('Number', 16)
    context['props']['text'] = string('${project}  /  ${service}')
    body = copy.deepcopy(panel['children'][0])
    body['props'].update(text=string('${message}'), customFontSize=14, customFontLineHeight=22, maxLine=fixed('Number', 100))
    panel['children'] = [body]
    panel['props'].update(backgroundType='Standard', standardBackgroundColor=fixed('Color', 'common_bg_color'), marginBottom=10)
    root['children'] = [head, context, panel]
    return editor


def topbox_template():
    editor = load(ROOT / 'design/alert-ceiling-card/h5-entry-onebox.json')
    root = rename(editor['schema']['componentsTree'][0], {'workbench_url': 'h5_url', 'summary': 'lastMessage', 'log_count': 'runtime_log_count', 'metric_count': 'runtime_metric_count', 'total_count': 'runtime_total_count'})
    root['children'][0]['props']['visible'] = visible()  # 实际显隐由服务端开启/关闭原生容器。
    # 适配原生吊顶高度，保留双统计块并压缩入口留白，避免文字被裁切。
    entry = root['children'][0]['children'][-1]
    entry['props']['paddingTop'] = fixed('Number', 2)
    entry['props']['paddingBottom'] = fixed('Number', 2)
    entry['children'][0]['props']['customFontLineHeight'] = 16
    editor['schema']['componentsTree'] = [root]
    return editor


def save(kind, editor):
    root = editor['schema']['componentsTree'][0]
    # 设计的布局全部保留，只有编辑器状态在本地；业务状态仅由服务端返回。
    expressions = [{'id': 'view_editor', 'name': 'view_editor', 'type': 'string', 'private': False,
                    'editorVarType': 'expList', 'description': '成功响应轮换私有键后收起编辑器',
                    'expContent': 'edit_key == operation_key ? editor_mode : ""'}] if kind in ('log', 'metric') else []
    local_names = ['draft', 'editor_mode', 'edit_key'] if expressions else []
    serialized = json.dumps(root, ensure_ascii=False)
    fields = set(re.findall(r'\$\{([A-Za-z_][A-Za-z_0-9]*)\}', serialized))
    def variables(value):
        if isinstance(value, dict):
            if isinstance(value.get('variable'), str) and value['variable']:
                fields.add(value['variable'])
            for item in value.values(): variables(item)
        elif isinstance(value, list):
            for item in value: variables(item)
    variables(root)
    fields.update(['lastMessage', 'operation_key'])
    fields.difference_update(local_names + ['view_editor'])
    editor.update(expList=expressions, variableList=[{'id': f, 'name': f, 'private': f == 'operation_key', 'type': 'string',
                  'editorVarType': 'variables', 'description': '告警平台业务数据'} for f in sorted(fields)],
                  localList=[{'id': f, 'name': f, 'private': False, 'type': 'string', 'editorVarType': 'localList'} for f in local_names],
                  mockData={'cardData': {f: '' for f in fields}, 'cardPrivateData': {}, 'localData': {f: '' for f in local_names}},
                  formList=[], hsfList=[], lwpList=[], customWidgetInfo='', useCustomWidgetInfo=False)
    editor['mockData']['cardData'].update(status='待处理', phase='red', is_open='yes', card_title='【设计验收】完整运行卡片',
       note_count='0', has_trace='no', history_label='查看完整处理记录  ↗', project='告警测试项目', service='test-api', context='告警测试项目 / test-api', count='1', duration='0 秒',
       progress_heading='等待 AI 开始处理', progress_body='模拟测试场景，不涉及真实业务故障。', notes_empty='yes',
       metric_value='95%', metric_rule='阈值 ≥ 90% · 连续 5 分钟', metric_key='cpu_usage', log_pending='1', log_processing='1',
       metric_pending='1', metric_processing='0', log_count='2', metric_count='1', total_count='3', runtime_log_count='2', runtime_metric_count='1', runtime_total_count='3',
       message='【设计验收】业务提示内容，无需在卡片内处理。')
    output = {'editorData': json.dumps(editor, ensure_ascii=False), 'widgetInfo': '', 'type': 'onebox' if kind == 'topbox' else 'im', 'mode': 'card'}
    (ROOT / 'cards' / f'{kind}.json').write_text(json.dumps(output, ensure_ascii=False, indent=2)+'\n')


if __name__ == '__main__':
    for kind, build in [('log', log_template), ('metric', metric_template), ('notice', notice_template), ('topbox', topbox_template)]:
        save(kind, build())

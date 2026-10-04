"""生成最新版告警吊顶设计稿：未完成统计与 H5 入口；不调用业务服务。"""
import copy
import json
from pathlib import Path
from urllib.parse import parse_qs, quote, urlsplit

OUT = Path(__file__).parent
seq = 0

def string(value):
    return {'type': 'dynamicString', 'content': value, 'i18n': False}

def dynamic(kind, value):
    return {'type': 'dynamic' + kind, 'valueType': 'fixed', 'value': value, 'variable': '', 'variableType': 'global'}

def variable(name, kind='variableValue', scope='global'):
    return {'type': kind, 'variableType': scope, 'variable': name}

def visible(name=None, value=1, op='equal', scope='global'):
    conditions = [] if name is None else [{'variable': name, 'op': op, 'value': value, 'variableType': scope, 'type': 'variable', 'valueType': 'fixed', 'valueVariableType': 'global'}]
    return {'type': 'dynamicVisible', 'value': True, 'valueType': 'fixed' if name is None else 'condition', 'condition': {'op': 'and', 'conditions': conditions}}

def node(component, props, children=None, title=''):
    global seq
    seq += 1
    result = {'componentName': component, 'id': 'node_ceiling_' + str(seq), 'props': props, 'title': title or component, 'hidden': False, 'isLocked': False, 'condition': True, 'conditionGroup': ''}
    if children is not None:
        result['children'] = children
    return result

def text(content, size=14, color='#24344D', bold=False, lines=1, **extra):
    props = {'text': string(content), 'fontColorType': 'Custom', 'customLightColor': dynamic('Color', color), 'customDarkColor': dynamic('Color', '#E5EAF2'), 'fontSizeType': 'Custom', 'customFontSize': size, 'customFontLineHeight': size + 7, 'bold': bold, 'italic': False, 'strikeThrough': False, 'lineHeight': 'normal', 'styleType': 'custom', 'gravity': 'left', 'autoWidth': False, 'autoMaxWidth': False, 'fixedWidth': dynamic('Number', 0), 'maxWidth': dynamic('Number', 0), 'maxLine': dynamic('Number', lines), 'enableIcon': False, 'margin': 0, 'marginLeft': 0, 'marginRight': 0, 'marginTop': 0, 'marginBottom': 0, 'visible': visible()}
    props.update(extra)
    return node('BaseText', props)

def grid(children, background=None, padding=0, direction='vertical', **extra):
    props = {'direction': direction, 'childGravity': 'leftTop', 'isAutoHeight': True, 'height': 100, 'isAutoWidth': False, 'isFixedWidth': False, 'width': 100, 'hasBackground': bool(background), 'backgroundType': 'Custom', 'backgroundColor': background or '#FFFFFF', 'darkModeBackgroundColor': '#242B36', 'hasBorder': False, 'hasGradientBackground': False, 'cornerRadius': 8, 'cornerRadiusLeftTop': 8, 'cornerRadiusRightTop': 8, 'cornerRadiusLeftBottom': 8, 'cornerRadiusRightBottom': 8, 'marginLeft': 0, 'marginRight': 0, 'marginTop': 0, 'marginBottom': 0, 'enableClickEvent': False, 'actionType': 'none', 'events': [], 'visible': visible(), 'paddingLeft': dynamic('Number',padding), 'paddingRight': dynamic('Number',padding), 'paddingTop': dynamic('Number',padding), 'paddingBottom': dynamic('Number',padding)}
    props.update(extra)
    return node('Grid', props, children)

def exp(name, expression, kind='string'):
    return {'name':name,'id':name,'type':kind,'private':False,'editorVarType':'expList','expContent':expression,'description':'设计稿展示表达式'}

def decl(name,kind='string',description='',schema=None,editor='variables'):
    d={'name':name,'id':name,'type':kind,'private':False,'description':description,'editorVarType':editor}
    if schema is not None:d['schema']=schema
    return d

# 预览链接仅供当前 Mac 使用；正式接入传入已鉴权 H5 的钉钉容器链接。
PAGE = 'http://127.0.0.1:8847/alert-workbench.prototype.html?variant=workspace&embed=1'
pc_link = 'dingtalk://dingtalkclient/page/link?pc_slide=true&url=' + quote(PAGE, safe='')
mobile_link = ('dingtalk://dingtalkclient/action/im_open_hybrid_panel?'
               'panelHeight=percent83&hybridType=online&pageUrl=' + quote(PAGE, safe=''))
entry_link = ('dingtalk://dingtalkclient/action/open_platform_link?pcLink='
              + quote(pc_link, safe='') + '&mobileLink=' + quote(mobile_link, safe=''))
COUNT_FIELDS = ('log_pending', 'log_processing', 'metric_pending', 'metric_processing')
TOTAL = ' + '.join(COUNT_FIELDS)

def stat_panel(prefix, label, background, accent, right=0):
    return grid([
        text(label, 10, '#60768D', customFontLineHeight=13),
        text('${'+prefix+'_count}', 20, accent, True, customFontLineHeight=22),
        text('待处理 ${'+prefix+'_pending} · 处理中 ${'+prefix+'_processing}',
             9, '#687B8D', customFontLineHeight=12,
             customDarkColor=dynamic('Color', '#A8B8CA')),
    ], background=background, padding=4, paddingLeft=dynamic('Number', 8),
       paddingRight=dynamic('Number', 8), isFixedWidth=True, width=124, marginRight=right)

panels = grid([
    stat_panel('log', '日志告警', '#F3F5F8', '#30445B', 8),
    stat_panel('metric', '指标告警', '#EEF5FD', '#286EAD'),
], direction='horizontal', marginTop=4)
entry = grid([
    text('查看未完成（${total_count}）', 12, '#286EAD', True,
         customFontLineHeight=18, gravity='center',
         customDarkColor=dynamic('Color', '#8DC0FF')),
], background='#EAF2FF', padding=5, darkModeBackgroundColor='#263D5B',
   marginTop=6, enableClickEvent=True, actionType='url', urlType='all',
   url={'type':'dynamicLink', 'valueType':'variable', 'value':'',
        'variable':'workbench_url', 'variableType':'global'})
entry['title'] = '查看未完成：在钉钉内打开 H5'
# 设计器按条件隐藏内容；真正撤下群顶区域由服务端关闭吊顶，不能仅靠空白卡。
content = grid([
    text('本群未完成告警', 12, '#344A63', True, customFontLineHeight=16),
    panels,
    entry,
], marginLeft=12, marginRight=12, marginTop=4, marginBottom=4,
   visible=visible('has_unfinished', 1))
content['title'] = '仅待处理或处理中存在时展示'
root = node('Card', {'enableClickEvent':False, 'showCloseButton':False,
    'autoFoldConfig':{'needFold':False,'heightLimit':480,
                      'foldStatusLocalDataKey':'_cardFoldStatusLocalDataKey'},
    'summaryContent':variable('summary'), 'paddingTop':0, 'paddingBottom':0,
    'paddingLeft':0, 'paddingRight':0}, [content], '本群未完成告警统计')
variables = [decl(name, 'number', '当前群对应类型和状态的告警轮次数，非消息数')
             for name in COUNT_FIELDS]
variables.append(decl('workbench_url', description='钉钉容器链接：PC 侧栏、手机半浮层'))
expressions = [
    exp('log_count', 'log_pending + log_processing', 'number'),
    exp('metric_count', 'metric_pending + metric_processing', 'number'),
    exp('total_count', TOTAL, 'number'),
    exp('has_unfinished', '('+TOTAL+') > 0 ? 1 : 0', 'number'),
    exp('summary', 'concat("本群未完成告警 · ",toStr('+TOTAL+'))'),
]
components = ['Card', 'Grid', 'BaseText']
editor = {'schemaVersion':'3.0.0','schema':{'version':'1.0.0',
    'componentsMap':[{'package':'@ali/dxComponent','version':'1.0.0','exportName':k,
        'main':'./src/index.tsx','destructuring':False,'subName':'','componentName':k}
        for k in components], 'componentsTree':[root], 'i18n':{}},
    'mockData':{'cardData':{},'cardPrivateData':{},'localData':{},'richTextData':{}},
    'editVersion':0,'customWidgetInfo':'','useCustomWidgetInfo':False,
    'variableList':variables,'formList':[],'expList':expressions,'localList':[],
    'hsfList':[],'lwpList':[],'pageData':{},
    'extension':{'extendType':'NORMAL','fileTypeList':[]}}
scenarios = {
    'mixed': (3, 1, 1, 1),
    'processing-only': (0, 2, 0, 1),
    'metric-only': (0, 0, 1, 1),
    'empty': (0, 0, 0, 0),
    'overflow': (120, 35, 8, 12),
}

def prepare_data(counts):
    assert len(counts) == 4 and all(type(n) is int and n >= 0 for n in counts)
    return dict(zip(COUNT_FIELDS, counts), workbench_url=entry_link)

# 核对显隐边界、分组求和及多层 URL 编码，防止处理中的事项被遗漏。
assert sum(scenarios['mixed']) == 6
assert sum(scenarios['processing-only']) == 3
assert sum(scenarios['empty']) == 0
links = parse_qs(urlsplit(entry_link).query)
assert links['pcLink'] == [pc_link] and links['mobileLink'] == [mobile_link]
assert parse_qs(urlsplit(pc_link).query) == {'pc_slide':['true'], 'url':[PAGE]}
assert parse_qs(urlsplit(mobile_link).query)['pageUrl'] == [PAGE]
for name, counts in scenarios.items():
    data = prepare_data(counts)
    assert data['log_pending']+data['log_processing']+data['metric_pending']+data['metric_processing'] == sum(counts)
    e = copy.deepcopy(editor)
    e['mockData']['cardData'] = data
    payload = {'editorData':json.dumps(e,ensure_ascii=False,separators=(',',':')),
               'widgetInfo':'','type':'onebox','mode':'card'}
    (OUT/(name+'.json')).write_text(json.dumps(payload,ensure_ascii=False,indent=2)+'\n')
    if name == 'mixed':
        (OUT/'h5-entry-onebox.json').write_text(json.dumps(payload,ensure_ascii=False,indent=2)+'\n')
        payload['type'] = 'im'
        (OUT/'h5-entry-test-card.json').write_text(json.dumps(payload,ensure_ascii=False,indent=2)+'\n')
print('生成 5 个新版场景：混合、仅处理中、仅指标、零项隐藏、大数量；入口编码与计数自检通过。')

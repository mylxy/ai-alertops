"""仅生成钉钉吊顶卡片设计稿和模拟场景；不连接告警服务。"""
import copy
import json
from pathlib import Path

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

logs = [
 {'id':'demo-log-01','title':'订单写入失败','status':'待处理','original_card_url':''},
 {'id':'demo-log-02','title':'设备报文解析失败','status':'处理中','original_card_url':''}
]
metrics = [
 {'id':'demo-metric-01','title':'CPU 使用率持续过高','status':'待处理','original_card_url':''},
 {'id':'demo-metric-02','title':'数据库连接使用率过高','status':'处理中','original_card_url':''}
]

def card_links(name):
    def entry(bound):
        action = {'actionType':'url','urlType':'all','url':{
            'type':'dynamicLink','valueType':'variable','value':'',
            'variable':name+'[0].original_card_url','variableType':'loop'
        }} if bound else {'actionType':'toast','toastType':1,
            'toastContent':string('此示例尚未绑定原卡消息链接')}
        return grid([
            text('${loop.number} ↗',10,'#426DAB',customFontLineHeight=14,gravity='center',
                 hoverText=string('${loop.status} · ${loop.title}'))
        ],background='#FFFFFF',padding=1,isFixedWidth=True,width=32,marginRight=4,
           cornerRadius=4,cornerRadiusLeftTop=4,cornerRadiusRightTop=4,
           cornerRadiusLeftBottom=4,cornerRadiusRightBottom=4,
           visible=visible(name+'[0].has_link',1 if bound else 0,scope='loop'),
           enableClickEvent=True,**action)
    row = grid([entry(True),entry(False)],isAutoWidth=True)
    loop = node('Loop', {'listData':variable(name),'direction':'horizontal',
        'isAutoWidth':True,'isFixedWidth':False,'childWidth':'wrap_content',
        'flowLayout':False,'scrollable':False,'childGap':False,'paging':False,
        'visible':visible()},[row],'直达原卡的编号入口')
    return node('ScrollerLayout', {
        'direction':'horizontal','childGravity':'leftTop','scrollAreaHeight':dynamic('Number',18),
        'autoScrollOnUpdate':False,'marginTop':3,'visible':visible()
    },[loop],'横向原卡入口')

def stat_panel(name, prefix, title, background, accent, right=0):
    return grid([
        text(title,10,'#60768D',customFontLineHeight=13),
        text('${'+prefix+'_count}',20,accent,True,customFontLineHeight=22),
        text('待处理 ${'+prefix+'_pending} · 处理中 ${'+prefix+'_processing}',9,'#778A9E',
             customFontLineHeight=11,customDarkColor=dynamic('Color','#9AA8BA')),
        card_links(name)
    ],background=background,padding=4,paddingLeft=dynamic('Number',7),paddingRight=dynamic('Number',7),
       isFixedWidth=True,width=124,marginRight=right,cornerRadius=8)

panels = grid([
    stat_panel('log_alerts','log','日志告警','#F3F5F8','#30445B',8),
    stat_panel('metric_alerts','metric','指标告警','#EEF5FD','#286EAD')
],direction='horizontal',marginLeft=12,marginRight=12,marginTop=3)
root=node('Card',{'enableClickEvent':False,'showCloseButton':False,
    'autoFoldConfig':{'needFold':False,'heightLimit':480,'foldStatusLocalDataKey':'_cardFoldStatusLocalDataKey'},
    'summaryContent':variable('summary'),'paddingTop':4,'paddingBottom':4,'paddingLeft':0,'paddingRight':0},[
    text('未结束告警 · ${total_count}',12,'#344A63',True,customFontLineHeight=16,marginLeft=12,marginRight=12),
    panels,
    text('${footer}',9,'#8998AA',customFontLineHeight=11,marginLeft=12,marginRight=12,marginTop=2)
])
fields=list(logs[0])+['number','has_link']
variables=[decl(name,'loopArray',description,[dict(decl(k,'number' if k=='has_link' else 'string'),id=name+'[0].'+k) for k in fields]) for name,description in [('log_alerts','本群未结束日志告警及原卡链接'),('metric_alerts','本群未结束指标告警及原卡链接')]]
variables += [decl(prefix+'_'+status,'number','按告警状态统计的数量') for prefix in ['log','metric'] for status in ['pending','processing']]
expressions=[exp('total_count','log_alerts.length + metric_alerts.length','number'),
    exp('log_count','log_alerts.length','number'),exp('metric_count','metric_alerts.length','number'),
    exp('footer','log_alerts.length + metric_alerts.length > 0 ? "点编号定位原卡" : "当前没有未结束告警"'),
    exp('summary','"日志告警 " + log_alerts.length + " · 指标告警 " + metric_alerts.length')]
components=['Card','Grid','BaseText','Loop','ScrollerLayout']
editor={'schemaVersion':'3.0.0','schema':{'version':'1.0.0','componentsMap':[{'package':'@ali/dxComponent','version':'1.0.0','exportName':k,'main':'./src/index.tsx','destructuring':False,'subName':'','componentName':k} for k in components],'componentsTree':[root],'i18n':{}},'mockData':{'cardData':{'log_alerts':logs,'metric_alerts':metrics},'cardPrivateData':{},'localData':{},'richTextData':{}},'editVersion':0,'customWidgetInfo':'','useCustomWidgetInfo':False,'variableList':variables,'formList':[],'expList':expressions,'localList':[],'hsfList':[],'lwpList':[],'pageData':{},'extension':{'extendType':'NORMAL','fileTypeList':[]}}

scenarios={
 'mixed':copy.deepcopy(editor['mockData']['cardData']),
 'claimed':{'log_alerts':[dict(logs[0],status='处理中'),copy.deepcopy(logs[1])],'metric_alerts':copy.deepcopy(metrics)},
 'finished':{'log_alerts':[copy.deepcopy(logs[1])],'metric_alerts':[]},
 'empty':{'log_alerts':[],'metric_alerts':[]}
}
scenarios['overflow'] = copy.deepcopy(scenarios['mixed'])
for number in range(5, 11):
    scenarios['overflow']['log_alerts'].append(dict(
        logs[0], id=f'demo-overflow-{number}', title=f'示例告警 {number}'))
scenarios['three-alerts'] = {
    'log_alerts': logs + [dict(logs[0], id='demo-log-03', title='设备心跳异常')],
    'metric_alerts': []
}
scenarios['middle-resolved'] = {
    'log_alerts': [scenarios['three-alerts']['log_alerts'][i] for i in (0, 2)],
    'metric_alerts': []
}

def prepare_data(source):
    data = copy.deepcopy(source)
    for prefix in ['log','metric']:
        alerts = data[prefix+'_alerts']
        data[prefix+'_pending'] = sum(a['status']=='待处理' for a in alerts)
        data[prefix+'_processing'] = sum(a['status']=='处理中' for a in alerts)
        # 展示序号跟随本次列表重排；告警身份和原卡链接始终保留在同一项内。
        for index, alert in enumerate(alerts, start=1):
            alert['number'] = f'{index:02d}'
            alert['has_link'] = int(bool(alert['original_card_url']))
    return data

def check_renumbering():
    # 使用不同的占位地址，检查移除中间项后不会把跳转目标按旧位置错配。
    # example.invalid 只用于内存自检，不写入任何可导入模板。
    source = copy.deepcopy(scenarios['three-alerts'])
    for alert in source['log_alerts']:
        alert['original_card_url'] = 'https://example.invalid/' + alert['id']
    before = prepare_data(source)
    source['log_alerts'].pop(1)
    after = prepare_data(source)
    assert [(a['number'], a['id']) for a in after['log_alerts']] == [
        ('01', 'demo-log-01'), ('02', 'demo-log-03')]
    assert after['log_alerts'][1]['original_card_url'] == before['log_alerts'][2]['original_card_url']
    assert (before['log_pending'], before['log_processing']) == (2, 1)
    assert (after['log_pending'], after['log_processing']) == (2, 0)
    assert len(before['log_alerts']) == 3 and len(after['log_alerts']) == 2
    assert all('number' not in a for a in source['log_alerts'])
    assert prepare_data({'log_alerts': [], 'metric_alerts': []}) == {
        'log_alerts': [], 'metric_alerts': [], 'log_pending': 0, 'log_processing': 0,
        'metric_pending': 0, 'metric_processing': 0}

check_renumbering()
for name,source in scenarios.items():
    data = prepare_data(source)
    e=copy.deepcopy(editor);e['mockData']['cardData']=data
    payload={'editorData':json.dumps(e,ensure_ascii=False,separators=(',',':')),'widgetInfo':'','type':'onebox','mode':'card'}
    (OUT/(name+'.json')).write_text(json.dumps(payload,ensure_ascii=False,indent=2))
print('自检通过：移除中间告警后连续编号、保留原卡关联，并同步统计。')
print('生成 7 个模拟场景：混合列表、处理中、部分结束后、空列表、多条告警、三条告警、移除中间告警。')

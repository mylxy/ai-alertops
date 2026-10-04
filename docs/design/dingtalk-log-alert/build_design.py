"""从官方组件样例生成未发布的钉钉卡片设计预览，不用于生产发送。"""
import copy,json,pathlib
P=pathlib.Path(__file__).parent
refs=[json.loads(json.loads((P/f).read_text())['editorData']) for f in ['审批模板-reference.json','交互组件的使用与本地更新-reference.json','official-reference.json']]
nodes={};maps={}
def collect(n):
 nodes.setdefault(n['componentName'],n)
 for c in n.get('children',[]):collect(c)
for e in refs:
 for n in e['schema']['componentsTree']:collect(n)
 for c in e['schema']['componentsMap']:maps[c['componentName']]=c
seq=0
def dynamic_string(v):return {'type':'dynamicString','content':v,'i18n':False}
def fixed_value(t,v):return {'type':t,'valueType':'fixed','value':v,'variable':'','variableType':'global'}
def visible(var=None,val=None):
 x={'type':'dynamicVisible','value':True,'valueType':'fixed','condition':{'op':'and','conditions':[]}}
 if var:x.update(valueType='condition',condition={'op':'and','conditions':[{'value':str(val).lower() if isinstance(val,bool) else val,'op':'equal','variable':var,'variableType':'global','type':'variable','valueType':'fixed','valueVariableType':'global'}]})
 return x
def note_visible(opened):
 x=visible('view_open','yes')
 x['condition']['conditions']+=visible('note_editor_open',opened)['condition']['conditions']
 return x
def node(t,title,props=None,children=None):
 global seq
 seq+=1;n=copy.deepcopy(nodes[t]);n['id']=f'node_log_design_{seq}';n['title']=title;n.pop('children',None)
 n['props'].update(props or {});n.update(hidden=False,isLocked=False,condition=True,conditionGroup='')
 if children is not None:n['children']=children
 return n
def text(title,content,size=14,color='black',bold=False,mt=4,mb=0,inside=False,maxline=5):
 return node('BaseText',title,{'text':dynamic_string(content),'fontSizeType':'Custom','customFontSize':size,'customFontLineHeight':size+6,'bold':bold,'color':color,'marginLeft':0 if inside else 16,'marginRight':0 if inside else 16,'marginTop':mt,'marginBottom':mb,'autoWidth':False,'fixedWidth':0,'maxWidth':0,'width':100,'maxLine':fixed_value('dynamicNumber',maxline),'visible':visible(),'enableIcon':False})
def grid(title,children,bg=False,direction='vertical',color='#F5F7FA',vis=None):
 return node('Grid',title,{'direction':direction,'childGravity':'leftCenter','hasBackground':bg,'hasBorder':False,'backgroundType':'Custom','backgroundColor':color,'darkModeBackgroundColor':'#242932','hasGradientBackground':False,'marginLeft':16,'marginRight':16,'marginTop':12,'marginBottom':0,'paddingLeft':fixed_value('dynamicNumber',12 if bg else 0),'paddingRight':fixed_value('dynamicNumber',12 if bg else 0),'paddingTop':fixed_value('dynamicNumber',10 if bg else 0),'paddingBottom':fixed_value('dynamicNumber',10 if bg else 0),'cornerRadius':8,'cornerRadiusLeftTop':8,'cornerRadiusRightTop':8,'cornerRadiusLeftBottom':8,'cornerRadiusRightBottom':8,'visible':vis or visible(),'actionType':'none','isFixedWidth':False,'isAutoWidth':False,'isAutoHeight':True},children)
def btn(label,color='blue'):
 b=copy.deepcopy(nodes['ButtonList']['props']['buttons']['list'][0]);b.update(id='log_'+str(len(label))+label,text=dynamic_string(label),color=color,status='normal',actionType='toast',toastContent=dynamic_string('设计预览：此操作尚未接入业务服务'),toastType=1,visible=visible(),params=[],iconType='image',icon=fixed_value('dynamicLink',''),darkModeIcon=fixed_value('dynamicLink',''))
 return b
def buttons(title,labels,vis=None):
 return node('ButtonList',title,{'direction':'horizontal','buttons':{'type':'button','list':[btn(v,'blue' if i==0 else 'gray') for i,v in enumerate(labels)]},'visible':vis or visible(),'marginLeft':16,'marginRight':16,'marginTop':12,'marginBottom':8})

flow=json.loads((P/'card-flow.json').read_text())
states=flow['states']; scenarios=flow['scenarios']; by_state={x['code']:x for x in states}
values={'preview_mode':True,'status':'new','alert_id':'LOG-20261004-001','trace_id':'7b3c91d4a2e84f609d5f4c8a1b260ef2','title':'设备数据入库异常','project':'智享云 V3','service':'biz-iot','environment':'生产','first_seen':'10-04 10:00','log_excerpt':'NullPointerException: device property is null','lastMessage':'日志异常｜设备数据入库异常'}
# 一个场景可对应同一主状态的不同进展；平台预览与说明共用此数据。
entries=list(scenarios)
for state in states:
 sample=next(x for x in scenarios if x['state']==state['code'])
 entries.append({**sample,'id':state['code']})
scenario_expr='preview_mode && preview_selection && preview_selection.value ? preview_selection.value : status'
def expression(name,content,kind='string'):
 return {'id':name,'name':name,'private':False,'type':kind,'expContent':content,'description':'卡片设计预览','editorVarType':'expList'}
def scenario_expression(name,get):
 body=' : '.join('('+scenario_expr+') == '+json.dumps(x['id'])+' ? '+json.dumps(get(x),ensure_ascii=False) for x in entries)+' : ""'
 return expression(name,body)
expr=[expression('preview_index','preview_selection && preview_selection.index != null ? preview_selection.index : 0','number')]
for name,get in [
 ('view_first_seen',lambda x:x.get('first_seen',values['first_seen'])),
 ('view_alert_id',lambda x:x.get('alert_id',values['alert_id'])),
 ('view_status',lambda x:x['state']),('view_label',lambda x:by_state[x['state']]['label']),
 ('view_phase',lambda x:by_state[x['state']]['phase']),('view_heading',lambda x:x['heading']),
 ('view_body',lambda x:x['body']),('view_count',lambda x:x['count']),('view_latest',lambda x:x['latest']),
 ('view_trigger',lambda x:x['trigger']),
 ('view_close_hint',lambda x:('AI 仍在运行；关闭卡片不会中断 AI，后续结果仍保留在处理记录中。' if x.get('ai_active') else '关闭后不再启动尚未开始的 AI。' if x['state']=='new' else '')+'人工确认处理完成并填写关闭说明后即可关闭，不要求存在 MR 或全部合并。设计预览未连接业务服务。'),
 ('view_duration',lambda x:str(x['duration_minutes'])+' 分钟'),
 ('view_latest_compact',lambda x:x['latest'].split(' ',1)[1] if x['latest'].split(' ',1)[0]==x.get('first_seen',values['first_seen']).split(' ',1)[0] else x['latest']),
 ('view_trace_id',lambda x:x.get('trace_id',values['trace_id'])),
 ('view_trace_text',lambda x:x.get('trace_id',values['trace_id']) or '本条告警未提供 Trace ID'),
 ('view_has_trace',lambda x:'yes' if x.get('trace_id',values['trace_id']) else 'no'),
 ('view_open',lambda x:'yes' if x['state']!='closed' else 'no'),
 ('view_note_count',lambda x:str(len(x['notes']))),
 ('view_notes_empty',lambda x:'yes' if not x['notes'] else 'no'),
 ('view_history_link',lambda x:'查看全部 '+str(len(x['notes']))+' 条备注与处理记录  ↗' if len(x['notes'])>3 else '查看完整处理记录  ↗'),
 ('view_mr_progress',lambda x:'交付未完成' if x['id']=='incomplete' else str(sum(m['status']=='已合并' for m in x['merge_requests']))+'/'+str(len(x['merge_requests']))+' 已合并')]:
 expr.append(scenario_expression(name,get))
headers=[]
phase_colors={'red':('#C73E47','#A83139'),'orange':('#AC6817','#8D5117'),'green':('#21865C','#1A6D4B')}
for phase,(light,dark) in phase_colors.items():
 overline=text(phase+'状态标题','日志告警  ·  ${view_label}',12,bold=True,mt=0,inside=True,maxline=1)
 title=text(phase+'问题标题','${title}',19,bold=True,mt=5,inside=True,maxline=2)
 for item in [overline,title]:
  item['props'].update(fontColorType='Custom',customLightColor=fixed_value('dynamicColor','#FFFFFF'),customDarkColor=fixed_value('dynamicColor','#FFFFFF'))
 head=grid(phase+'阶段色头部',[overline,title],bg=True,color=light,vis=visible('view_phase',phase))
 head['props'].update(marginLeft=0,marginRight=0,marginTop=0,paddingLeft=fixed_value('dynamicNumber',16),paddingRight=fixed_value('dynamicNumber',16),paddingTop=fixed_value('dynamicNumber',13),paddingBottom=fixed_value('dynamicNumber',14),darkModeBackgroundColor=dark,cornerRadiusLeftBottom=0,cornerRadiusRightBottom=0)
 headers.append(head)
trace_line=text('点击复制完整 Trace ID','${view_trace_id}',11,'gray',mt=0,inside=True,maxline=2)
trace_line['props'].update(fontColorType='Custom',customLightColor=fixed_value('dynamicColor','#5B7FA6'),customDarkColor=fixed_value('dynamicColor','#8FB0D2'))
trace_panel=grid('点击 Trace ID 复制',[trace_line],vis=visible('view_has_trace','yes'))
trace_panel['props'].update(marginLeft=0,marginRight=0,marginTop=10,enableClickEvent=True,actionType='copy',copyType='common',copyValue=dynamic_string('${view_trace_id}'))
missing_trace=text('上游未提供追踪 ID','本条告警未提供 Trace ID',11,'gray',mt=10,inside=True,maxline=2)
missing_trace['props']['visible']=visible('view_has_trace','no')
overview=grid('告警信息',[
 text('累计次数与持续时间','累计 ${view_count} 次  ·  持续 ${view_duration}',12,mt=0,inside=True,maxline=1),
 text('首次与最近告警时间','首次 ${view_first_seen} · 最近 ${view_latest_compact}',11,'gray',mt=8,inside=True,maxline=1),
 trace_panel,missing_trace,
 text('原始日志摘要','${log_excerpt}',11,'gray',mt=5,inside=True,maxline=3)
],bg=True,color='#F3F6FA')
for item in overview['children']:
 if item['componentName']=='BaseText' and item['props']['color']=='gray':
  item['props'].update(fontColorType='Custom',customLightColor=fixed_value('dynamicColor','#778291'),customDarkColor=fixed_value('dynamicColor','#A8B2C0'))
children=headers+[
 text('业务上下文','${project}  /  ${service}',12,'gray',mt=12,maxline=2),
 overview,
 grid('当前处理进展',[text('进展标题','${view_heading}',15,bold=True,mt=0,inside=True,maxline=2),text('进展详情','${view_body}',13,mt=6,inside=True,maxline=12)],bg=False)
]
# 每条已存在的 MR 独立展示。预览编号不冒充真实 Codeup 地址。
expr.append(scenario_expression('view_has_mrs',lambda x:'yes' if x.get('merge_requests') else 'no'))
mr_children=[text('合并请求列表标题','Codeup  ·  ${view_mr_progress}',12,bold=True,mt=0,inside=True,maxline=1)]
for index in range(max(len(x.get('merge_requests',[])) for x in entries)):
 prefix='view_mr_'+str(index+1)
 for suffix in ['label','status','url','visible','message']:
  def mr_value(x,index=index,suffix=suffix):
   items=x.get('merge_requests',[])
   if index>=len(items):return 'no' if suffix=='visible' else ''
   item=items[index];label=item['repository']+' '+item['number']
   return {'label':label+'  ↗','status':item['status'],'url':item['url'],'visible':'yes','message':'设计预览：打开 '+label+'。正式使用此 MR 自己的 Codeup 地址。'}[suffix]
  expr.append(scenario_expression(prefix+'_'+suffix,mr_value))
 row=grid('独立 MR 入口 '+str(index+1),[
  text('MR 仓库与编号','${'+prefix+'_label}',13,'blue',mt=0,inside=True,maxline=2),
  text('MR 独立状态','${'+prefix+'_status}',12,'gray',mt=2,inside=True,maxline=1)
 ],vis=visible(prefix+'_visible','yes'))
 row['props'].update(marginLeft=0,marginRight=0,marginTop=8,enableClickEvent=True,actionType='toast',toastContent=dynamic_string('${'+prefix+'_message}'),toastType=1,urlType='all',url={'type':'dynamicLink','valueType':'variable','variable':prefix+'_url','variableType':'global','value':''})
 mr_children.append(row)
children.append(grid('按仓库查看合并请求',mr_children,bg=True,vis=visible('view_has_mrs','yes')))
# 备注为追加记录，独立于 AI 结论及 MR 数据，避免自动更新覆盖人工内容。
note_children=[text('备注标题','人工备注  ·  ${view_note_count} 条',13,bold=True,mt=0,inside=True,maxline=1)]
empty=text('暂无人工备注','暂无备注，可按需补充处理进展。',12,'gray',mt=8,inside=True,maxline=2)
empty['props']['visible']=visible('view_notes_empty','yes');note_children.append(empty)
for index in range(3):
 prefix='view_note_'+str(index+1)
 for suffix in ['meta','body','visible']:
  def note_value(x,index=index,suffix=suffix):
   notes=list(reversed(x['notes'][-3:]))
   if index>=len(notes):return 'no' if suffix=='visible' else ''
   note=notes[index]
   return {'meta':('最新 · ' if index==0 else '')+note['author']+' · '+note['created_at'][5:],'body':note['body'] if len(note['body'])<=100 else note['body'][:100]+'…','visible':'yes'}[suffix]
  expr.append(scenario_expression(prefix+'_'+suffix,note_value))
 meta=text('备注填写人与时间','${'+prefix+'_meta}',11,'gray',mt=12,inside=True,maxline=2)
 body=text('备注内容','${'+prefix+'_body}',13,bold=index==0,mt=3,inside=True,maxline=4)
 for item in [meta,body]:item['props']['visible']=visible(prefix+'_visible','yes')
 note_children.extend([meta,body])
history=grid('查看完整处理记录',[text('处理记录入口','${view_history_link}',12,'blue',mt=0,inside=True,maxline=2)])
history['props'].update(marginLeft=0,marginRight=0,marginTop=12,enableClickEvent=True,actionType='toast',toastContent=dynamic_string('设计预览：此处查看全部备注原文及 AI、MR、关闭记录。每条备注保留填写人和完整日期时间。'),toastType=1)
note_children.append(history)
children.append(grid('人工备注时间线',note_children,bg=False))
children.append(node('Input','追加人工备注',{'id':'handling_note','title':dynamic_string('增加备注'),'placeholder':dynamic_string('填写原因、进展或上线计划'),'currentValue':dynamic_string('${handling_note}'),'status':fixed_value('dynamicSelect','normal'),'actionType':'setLocalState','localVarAction':{'type':'variableValue','variable':'handling_note','variableType':'global','varType':'string'},'visible':note_visible(True),'params':[]}))
messages={
 '提交备注':'设计预览：备注尚未保存。正式接入后，提交成功会追加备注、记录填写人与时间，并收起输入框；AI 继续。',
 '确认关闭':'${view_close_hint}',
 '查看处理记录':'设计预览：此处查看全部备注原文及 AI、MR、关闭记录，包含每条记录的填写人和时间。'}
# 使用平台原生本地变量展开与收起，备注持久化仍由后续业务服务负责。
def toggle_note_editor(button,opened):
 button.update(actionType='setLocalState',localVarAction={'type':'variableValue','variable':'note_editor_open','variableType':'global','varType':'boolean'},booleanLocalValue=fixed_value('dynamicBoolean',opened))
row=buttons('处理操作',['增加备注','确认关闭'],note_visible(False))
for b in row['props']['buttons']['list']:
 label=b['text']['content']
 if label=='增加备注':toggle_note_editor(b,True)
 else:
  b['toastContent']=dynamic_string(messages[label])
children.append(row)
editor_actions=buttons('备注提交与取消',['提交备注','取消'],note_visible(True))
editor_actions['props']['buttons']['list'][0]['toastContent']=dynamic_string(messages['提交备注'])
toggle_note_editor(editor_actions['props']['buttons']['list'][1],False)
children.append(editor_actions)
children.append(text('告警聚合编号','告警编号  ${view_alert_id}',11,'gray',mt=2,mb=8,maxline=1))
sel=node('SelectBlock','按流程切换预览场景',{'id':'preview_state_select','placeholder':dynamic_string('选择流程场景'),'options':[{'label':dynamic_string(x['label']),'value':dynamic_string(x['id'])} for x in scenarios],'currentIndex':{'type':'dynamicNumber','valueType':'variable','variable':'preview_index','variableType':'global','value':0},'actionType':'setLocalState','localVarAction':{'type':'variableValue','variable':'preview_selection','variableType':'global','varType':'object'},'status':fixed_value('dynamicSelect','normal'),'visible':visible('preview_mode',True),'params':[]})
children.append(grid('仅设计预览',[
 text('预览说明','设计演示 · 模拟数据与操作',11,'gray',mt=0,inside=True,maxline=1),sel,
 text('场景进入条件','进入条件：${view_trigger}',12,'gray',mt=8,inside=True,maxline=6),
 text('演示范围','业务操作尚未连接服务。',11,'gray',mt=6,inside=True,maxline=2)
],bg=True,vis=visible('preview_mode',True)))
root=node('Card','日志告警状态卡',{'actionType':'none','isLinkToCard':False,'showCloseButton':False,'summaryContent':{'type':'variableValue','variable':'lastMessage','variableType':'global'},'autoFoldConfig':{'needFold':False,'heightLimit':900,'foldStatusLocalDataKey':'_cardFoldStatusLocalDataKey'}},children)
e=copy.deepcopy(refs[0]);e['schema']['componentsTree']=[root];e['schema']['componentsMap']=list(maps.values());e['mockData']={'cardData':values,'cardPrivateData':{},'localData':{'preview_selection':{'value':'first','index':0},'handling_note':'','note_editor_open':False},'richTextData':{'cardData':{}}}
e['variableList']=[{'id':k,'name':k,'type':'boolean' if isinstance(v,bool) else 'string','private':False,'description':'设计预览开关' if k=='preview_mode' else k,'editorVarType':'variables'} for k,v in values.items()]
e['expList']=expr;e['localList']=[{'id':k,'name':k,'type':t,'private':False,'description':'仅设计预览','editorVarType':'localList','schema':[]} for k,t in [('preview_selection','object'),('handling_note','string'),('note_editor_open','boolean')]]
e['formList']=[];e['hsfList']=[];e['lwpList']=[];e['extension']={'extendType':'NORMAL','fileTypeList':[]};e['customWidgetInfo']='';e['useCustomWidgetInfo']=False
out={'editorData':json.dumps(e,ensure_ascii=False),'widgetInfo':'','type':'im','mode':'card'}
(P/'log-alert-card-design.json').write_text(json.dumps(out,ensure_ascii=False,indent=2))
(P/'design-preview-data.json').write_text(json.dumps({**flow,'data':values,'templateId':'03a9f40b-e81b-4efc-a12d-6a59a55a9a32.schema','sources':['https://github.com/open-dingtalk/dingtalk-card-examples'],'notes':['未发布的设计草稿；场景切换、点击 Trace ID 复制、备注展开收起和文本输入为本地交互；备注提交与其他业务操作仅显示说明，未连接服务。',f'{len(states)} 个主状态，{len(scenarios)} 个预览场景。','关闭后新发生的同类告警创建新轮次和新卡片，允许新轮次执行一次 AI；迟到事件归原轮次。']},ensure_ascii=False,indent=2))
# 从相同源数据输出对照说明，避免手工维护多份状态名称。
lines=['# 日志告警卡片流程对照说明','','本说明与钉钉卡片设计稿使用相同的状态和场景数据。场景切换、点击 Trace ID 复制、增加备注展开输入框、取消收起及文本输入可在预览中操作；备注提交与其他业务操作仅展示说明，未接入告警、AI、Codeup 或发布服务。','','## 主状态','','| 主状态 | 标题颜色 | 默认操作入口 |','| --- | --- | --- |']
for x in states:lines.append('| '+x['label']+' | '+{'red':'红色','orange':'琥珀橙','green':'绿色'}[x['phase']]+' | '+'、'.join(x['actions'])+' |')
lines+=['','待处理和处理中默认提供确认关闭，不依赖 AI 结果、AI 是否结束、MR 是否存在或是否全部合并。人工核实处理完成后填写关闭说明；日常备注可选。备注编辑期间先完成或取消编辑，再回到默认操作行。已关闭后隐藏确认关闭。AI 若仍在运行则继续，后续结果仅保留为记录，不重新打开卡片。','','取消认领、接管与手动更新进展入口。人工备注支持零条、一条或多条，自动记录填写人和提交时间，不影响 AI 执行。卡片展示最近三条，完整内容通过处理记录访问。','','## 按场景对照','','前八个场景组成首次告警、AI 排查、备注追加、MR 合并、发布验证和关闭的主流程。其余场景演示 AI 不同结论与异常交付。']
lines+=['','## 状态流转图','','```mermaid','flowchart TD']
for state in states:lines.append('  '+state['code']+'["'+state['label']+'"]')
for transition in flow['transitions']:
 lines.append('  '+transition['from']+' -->|"'+transition['condition']+'"| '+transition['to'])
lines+=['  closed -.->|"关闭后确认新发生的同类告警；新轮次"| next_round["新轮次 待处理；旧轮次保持关闭"]','  classDef red fill:#FDECEC,stroke:#C53D43,color:#8A2026;','  classDef orange fill:#FFF3DF,stroke:#B56913,color:#70410C;','  classDef green fill:#E7F5EE,stroke:#21865C,color:#15583B;']
for state in states:lines.append('  class '+state['code']+' '+state['phase']+';')
lines+=['```','','## 逐步预览']
for x in scenarios:
 lines+=['','### '+x['label'],'','进入条件：'+x['trigger'],'','卡片标题：日志告警 · '+by_state[x['state']]['label'],'','正文标题：'+x['heading'],'','正文内容：','']+['- '+v for v in x['body'].split('\n')]
 if x.get('merge_requests'):
  lines+=['','独立 MR 入口：','']+['- '+m['repository']+' '+m['number']+' ↗ · '+m['status'] for m in x['merge_requests']]
 lines+=['','人工备注（共 '+str(len(x['notes']))+' 条）：','']+(['- '+n['author']+' · '+n['created_at']+'：'+n['body'] for n in reversed(x['notes'])] or ['暂无人工备注。'])
lines+=['','## 告警聚合和新轮次','']+['- '+v for v in flow['round_rules']]
lines+=['','## 合并请求入口','']+['- '+v for v in flow['mr_link_rules']]
lines+=['','## 流转规则','']+['- '+v for v in flow['rules']]
lines+=['','## 备注交互','']+['- '+v for v in flow['note_rules']]
lines+=['','## 卡片排版','']+['- '+v for v in flow['presentation_rules']]
lines+=['','## 缺陷 ID 和复制','']+['- '+v for v in flow['trace_rules']]
(P/'卡片流程对照.md').write_text('\n'.join(lines)+'\n')
print('Generated',seq,'native components;',len(states),'states;',len(scenarios),'scenarios')

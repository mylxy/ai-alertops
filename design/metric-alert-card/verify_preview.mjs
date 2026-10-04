// 检查生成模板的三状态和备注动作；真实群内渲染仍需客户端验证。
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const editor = JSON.parse(JSON.parse(readFileSync(new URL('metric-alert-card.json', import.meta.url), 'utf8')).editorData);
const expressions = Object.fromEntries(editor.expList.map(x => [x.name, x.expContent]));
const nodes = [];
const walk = n => { nodes.push(n); (n.children || []).forEach(walk); };
editor.schema.componentsTree.forEach(walk);
const native = {
  getLength: x => x.length, toStr: String,
  arrayGet: (x, i) => x[i], arrayAppend: (x, item) => [...x, item],
  substr: (x, start, length) => x.substring(start, start + length),
  textReplace: (s, from, to) => s.split(from).join(to),
};
let local = structuredClone(editor.mockData.localData);
const value = name => {
  const scope = {...native, ...local};
  return Function(...Object.keys(scope), `return (${expressions[name]})`)(...Object.values(scope));
};
const click = label => {
  const props = nodes.find(n => n.componentName === 'SingleButton' && n.props.text.content === label).props;
  if (!props.enableCustomLocalData) {
    local[props.localVarAction.variable] = props.stringLocalValue.content;
    return;
  }
  const updates = Object.fromEntries(Object.entries(JSON.parse(props.customLocalData)).map(([key, v]) =>
    [key, /^\$\{\w+\}$/.test(v) ? value(v.slice(2, -1)) : v]));
  Object.assign(local, updates);
};
assert.equal(value('status_label'), '待处理');
click('添加备注'); local.note_draft = '取消的草稿'; click('取消');
assert.equal(value('note_count'), '0');
assert.equal(local.note_draft, '');
click('添加备注'); local.note_draft = ' \t\r\n\u3000\u00a0'; click('提交备注');
assert.equal(value('status_label'), '待处理');
assert.equal(value('save_status'), 'disabled');
for (const note of ['开始排查', '调整规则', '等待恢复', '继续观察']) {
  click('添加备注'); local.note_draft = note; click('提交备注');
}
assert.equal(value('status_label'), '处理中');
assert.equal(local.monitor_state, 'firing');
assert.equal(value('note_count'), '4');
assert.equal(value('note_0_body'), '继续观察');
assert.equal(value('note_2_body'), '调整规则');
click('提交备注');
assert.equal(value('note_count'), '4', '重复提交不追加');
click('添加备注'); local.note_draft = '恢复期间提交'; click('模拟监控恢复'); click('提交备注');
assert.equal(value('status_label'), '已恢复');
assert.equal(value('note_count'), '4');
assert.equal(value('save_status'), 'disabled');
local = structuredClone(editor.mockData.localData);
click('模拟监控恢复');
assert.equal(value('status_label'), '已恢复');
assert.equal(value('note_count'), '0');
assert(!editor.expList.some(x => /告警中|已备注/.test(x.expContent)));
console.log('通过：三个状态、取消与空白校验、连续备注、最近三条、重复提交及恢复后禁写。');

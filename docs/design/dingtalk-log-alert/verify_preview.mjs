// 执行生成后的表达式与按钮数据，检查本地预览；不替代钉钉客户端验收。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const read = name => JSON.parse(readFileSync(new URL(name, import.meta.url), 'utf8'));
const editor = JSON.parse(read('log-alert-card-design.json').editorData);
const flow = read('card-flow.json');
const expressions = Object.fromEntries(editor.expList.map(e => [e.name, e.expContent]));
const native = {
  concat: (...args) => args.join(''), toStr: String,
  substr: (s, start, length) => s.substring(start, start + length),
  getLength: a => a.length, objectGet: (o, key) => o[key],
  arrayGet: (a, i) => a[i], arrayAppend: (a, ...items) => [...a, ...items],
  textReplace: (s, match, replacement) => s.split(match).join(replacement),
};
let local;
const reset = id => {
  local = structuredClone(editor.mockData.localData);
  local.preview_selection = {value: id, index: flow.scenarios.findIndex(s => s.id === id)};
};
const value = name => {
  const scope = {...native, ...editor.mockData.cardData, ...local};
  return Function(...Object.keys(scope), `return (${expressions[name]})`)(...Object.values(scope));
};
const nodes = [];
const walk = n => { nodes.push(n); (n.children || []).forEach(walk); };
editor.schema.componentsTree.forEach(walk);
const row = title => nodes.find(n => n.title === title).props;
const click = (title, label) => {
  const button = row(title).buttons.list.find(b => b.text.content === label);
  assert.equal(button.actionType, 'setLocalState');
  const update = Object.fromEntries(Object.entries(JSON.parse(button.customLocalData)).map(([k,v]) =>
    [k, typeof v === 'string' && /^\$\{\w+\}$/.test(v) ? value(v.slice(2,-1)) : v]));
  Object.assign(local, update);
};
const submitNote = text => {
  click('处理操作', '添加备注'); local.handling_note = text;
  click('添加备注提交与取消', '提交备注');
};
for (const scene of flow.scenarios) {
  reset(scene.id);
  assert.equal(value('view_status'), scene.state, scene.id);
  assert.equal(value('view_label'), {new:'待处理',processing:'处理中',closed:'已处理'}[scene.state]);
  assert.equal(value('view_open'), scene.state === 'closed' ? 'no' : 'yes');
  assert.equal(value('view_note_count'), String(scene.notes.length));
  if (scene.state === 'closed') {
    // 即使收到旧编辑器的提交，也不向终态追加备注。
    local.working_scene = scene.id; local.editor_mode = 'note';
    local.preview_notes = scene.notes.map(n => n.body);
    local.preview_metas = scene.notes.map(n => n.author);
    local.handling_note = '迟到备注';
    assert.equal(value('view_can_submit'), false);
    assert.equal(value('view_next_notes').length, scene.notes.length);
  }
}
reset('first');
// 测试投放不带设计器本地场景，仍须显示默认告警。
local.preview_selection = {};
assert.equal(value('view_scene'), 'new');
assert.equal(value('view_count'), '1');
assert.equal(value('view_first_seen'), '10-04 10:00');
assert.equal(value('view_status'), 'new');
reset('first');
click('处理操作', '添加备注');
assert.equal(value('view_status'), 'new');
local.handling_note = '取消的内容';
click('添加备注提交与取消', '取消');
assert.equal(value('view_note_count'), '0');
assert.equal(local.handling_note, '');
submitNote(' \t\n\r\u3000\u00a0');
assert.equal(value('view_status'), 'new');
assert.equal(value('view_note_count'), '0');
for (const note of ['检查原因', '修复 "连接池"\n保留换行', '交付测试', '验证完成']) submitNote(note);
assert.equal(value('view_status'), 'processing');
assert.equal(value('view_note_count'), '4');
assert.equal(value('view_note_1_body'), '验证完成');
assert.equal(value('view_note_3_body'), '修复 "连接池"\n保留换行');
assert.equal(local.handling_note, '');
assert.equal(value('view_editor'), '');
click('处理操作', '标记已处理');
click('处理结果提交与取消', '提交并完成');
assert.equal(value('view_status'), 'processing');
local.handling_note = '已核实完成';
click('处理结果提交与取消', '提交并完成');
assert.equal(value('view_status'), 'closed');
assert.equal(value('view_open'), 'no');
assert.equal(value('view_note_count'), '4');
assert.equal(local.completion_result, '已核实完成');
local.preview_selection = {value:'recurrence',index:17};
assert.equal(value('view_status'), 'new');
assert.equal(value('view_note_count'), '0');
assert.equal(value('view_editor'), '');
for (const id of ['first', 'ai', 'unlocated', 'start_failed']) {
  reset(id); click('处理操作', '标记已处理'); local.handling_note = '核实无需修复';
  click('处理结果提交与取消', '提交并完成');
  assert.equal(value('view_status'), 'closed', id);
}
console.log('通过：18 场景表达式、备注追加与取消、空内容、终态禁写、无 MR 完成和新轮次隔离。');

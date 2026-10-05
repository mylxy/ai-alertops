<script setup lang="ts">
import { computed, ref } from "vue";
import { api, labels, type Row } from "./api";
const props = defineProps<{ detail: Row; canOperate: boolean }>();
const emit = defineEmits<{ history: []; saved: []; failure: [unknown] }>();
const p = computed(() => props.detail.presentation || {});
const round = computed(() => props.detail.round);
const mode = ref(""), draft = ref(""), key = ref(""), submitted = ref(""), busy = ref(false), feedback = ref("");
function edit(action: string) {
  mode.value = action;
  draft.value = "";
  key.value = crypto.randomUUID();
  submitted.value = "";
  feedback.value = "";
}
async function submit() {
  if (!draft.value.trim() || busy.value) return;
  // 网络重试复用幂等键；用户改写内容后才生成新键。
  if (submitted.value && submitted.value !== draft.value) key.value = crypto.randomUUID();
  submitted.value = draft.value;
  busy.value = true;
  try {
    await api(`/api/h5/rounds/${round.value.id}/${mode.value}`, "POST", { content: draft.value, operation_key: key.value });
    mode.value = "";
    draft.value = "";
    emit("saved");
  } catch (e) {
    emit("failure", e);
  } finally {
    busy.value = false;
  }
}
async function copyTrace() {
  try {
    await navigator.clipboard.writeText(p.value.trace_id);
    feedback.value = "Trace ID 已复制";
  } catch {
    feedback.value = "复制未成功，可选中完整 Trace ID 复制";
  }
}
</script>

<template>
  <article :class="['complete-card', p.phase, round.alert_type.toLowerCase()]" :aria-label="round.alert_no">
    <header class="card-head">
      <div>{{ labels[round.alert_type] }} · {{ labels[round.round_status] }}</div>
      <h3>{{ p.card_title }}</h3>
    </header>
    <div class="card-body">
      <p class="card-context">{{ p.project }} / {{ p.service }}</p>
      <section class="evidence">
        <template v-if="round.alert_type === 'METRIC'">
          <strong class="metric-value">{{ p.metric_value }}</strong>
          <p class="metric-rule">{{ p.metric_rule }}</p>
        </template>
        <p class="occurrence">累计 {{ p.count }} 次 · {{ labels[round.count_quality] }} · 持续 {{ p.duration }}</p>
        <p class="event-times">首次 {{ p.first_seen }}<br />最近 {{ p.latest_seen }}</p>
        <template v-if="round.alert_type === 'LOG'">
          <button v-if="p.has_trace === 'yes'" class="trace-copy" @click="copyTrace">Trace ID：{{ p.trace_id }} · 复制</button>
          <p v-else class="event-times">上游未提供 Trace ID</p>
          <p class="log-excerpt">{{ p.log_excerpt }}</p>
        </template>
      </section>
      <template v-if="round.alert_type === 'METRIC'">
        <p class="metric-dimensions">{{ p.metric_key }}</p>
        <p class="monitor-hint">{{ p.monitor_hint }}</p>
      </template>
      <template v-else>
        <section class="handling-progress">
          <h4>{{ p.progress_heading }}</h4>
          <p>{{ p.progress_body || '等待 AI 任务调度，可先记录人工排查进展。' }}</p>
        </section>
        <section v-if="detail.merge_requests.length" class="merges">
          <h4>合并请求 <small>{{ p.mr_progress }}</small></h4>
          <a v-for="mr in detail.merge_requests" :key="mr.id" :href="mr.url" target="_blank" rel="noopener noreferrer"><span>{{ mr.repository_id }} #{{ mr.display_number }} ↗</span><span>{{ labels[mr.merge_status] }}</span></a>
          <p v-if="detail.all_merged">必要修复分支全部已合并，等待人工确认。</p>
        </section>
      </template>
      <section v-if="round.alert_type === 'LOG' || p.has_notes === 'yes'" class="notes">
        <h4>人工备注 · {{ p.note_count }} 条</h4>
        <p v-if="p.notes_empty === 'yes'" class="no-notes">暂无备注，可按需补充处理进展。</p>
        <template v-for="n in 3" :key="n">
          <div v-if="p[`note_${n}_visible`] === 'yes'" class="note">
            <small>{{ p[`note_${n}_meta`] }}</small>
            <p>{{ p[`note_${n}_body`] }}</p>
          </div>
        </template>
      </section>
      <button class="history-link" @click="emit('history')">查看完整处理记录 ↗</button>
      <p v-if="feedback" role="status" class="feedback">{{ feedback }}</p>
      <template v-if="p.is_open === 'yes' && canOperate">
        <form v-if="mode" class="editor" @submit.prevent="submit">
          <label :for="'draft-' + round.id">{{ mode === 'handle' ? '处理结果（必填）' : '添加备注（必填）' }}</label>
          <textarea :id="'draft-' + round.id" v-model="draft" rows="3" maxlength="8000" required :disabled="busy" :placeholder="mode === 'handle' ? '说明修复生效与验证结果，或无需修复的依据' : '记录排查进展，不会中断 AI 执行'" />
          <div class="card-actions">
            <button type="button" :disabled="busy" @click="mode = ''">取消</button>
            <button type="submit" :disabled="busy || !draft.trim()">{{ busy ? '正在保存…' : mode === 'handle' ? '确认已处理' : '提交备注' }}</button>
          </div>
        </form>
        <div v-else class="card-actions">
          <button @click="edit('notes')">添加备注</button>
          <button v-if="round.alert_type === 'LOG'" @click="edit('handle')">标记已处理</button>
        </div>
      </template>
      <p v-else-if="p.is_open === 'yes'" class="feedback">仅当前群成员可以处理告警</p>
      <p class="alert-number">告警编号 {{ round.alert_no }}</p>
    </div>
  </article>
</template>

<style scoped>
.complete-card {
  width:100%;
  max-width:460px;
  margin:0 auto;
  align-self:start;
  background:#fff;
  border:1px solid #e1e6ec;
  border-radius:8px;
  overflow:hidden;
  color:#202d3d;
  font-size:13px}

.card-head {
  padding:13px 16px 14px;
  background:#c73e47;
  color:#fff}
.orange .card-head {
  background:#ac6817}
.green .card-head {
  background:#21865c}
.card-head div {
  font-size:12px;
  line-height:18px;
  font-weight:600}
.card-head h3 {
  font-size:19px;
  line-height:25px;
  margin:5px 0 0;
  color:#fff;
  overflow-wrap:anywhere}
.card-body {
  padding:12px 16px 16px}
.card-body p {
  margin:0;
  white-space:pre-wrap;
  overflow-wrap:anywhere}
.card-context {
  font-size:12px;
  line-height:19px;
  color:#778291}
.evidence {
  margin-top:12px;
  padding:10px 12px;
  border-radius:8px;
  background:#f3f6fa;
  color:#778291;
  font-size:11px;
  line-height:17px}
.occurrence {
  font-size:12px;
  line-height:18px;
  color:#202d3d}
.evidence .event-times {
  font-size:11px;
  line-height:17px;
  margin-top:8px}
.trace-copy {
  display:block;
  border:0;
  background:none;
  padding:0;
  margin-top:10px;
  color:#5b7fa6;
  font-size:11px;
  line-height:17px;
  text-align:left;
  overflow-wrap:anywhere;
  word-break:break-all;
  user-select:text}
.evidence .log-excerpt {
  margin-top:5px}
.metric-value {
  display:block;
  font-size:28px;
  line-height:35px;
  font-weight:700;
  color:#f2510c;
  overflow-wrap:anywhere}
.green .metric-value {
  color:#21865c}
.evidence .metric-rule {
  font-size:12px;
  line-height:19px;
  margin:3px 0 6px}
.card-body .metric-dimensions {
  font-size:12px;
  color:#778291;
  margin-top:10px}
.card-body .monitor-hint {
  font-size:13px;
  margin-top:10px}
.handling-progress,.merges,.notes {
  margin-top:12px}
.card-body h4 {
  font-size:15px;
  line-height:21px;
  margin:0;
  font-weight:700}
.handling-progress p {
  font-size:13px;
  line-height:19px;
  margin-top:6px}
.notes h4 {
  font-size:13px;
  line-height:19px}
.note {
  margin-top:12px;
  font-size:13px;
  line-height:19px}
.note small {
  display:block;
  font-size:11px;
  line-height:17px;
  color:#778291;
  margin-bottom:3px}
.card-body .no-notes {
  font-size:12px;
  color:#778291;
  margin-top:8px}
.history-link {
  border:0;
  background:none;
  padding:0;
  margin-top:12px;
  font-size:12px;
  line-height:18px;
  color:#1677ff}
.card-actions {
  display:flex;
  gap:8px;
  margin-top:12px}
.card-actions button {
  flex:1;
  background:#f3f4f6;
  color:#202d3d;
  border:0;
  border-radius:20px;
  padding:8px 12px;
  font-size:14px;
  line-height:20px;
  min-height:36px}
.card-actions button:first-child,.editor button[type=submit] {
  color:#1677ff}
.card-actions button:disabled {
  opacity:.5;
  cursor:default}
.editor {
  margin-top:12px}
.editor label {
  display:block;
  margin-bottom:6px}
.editor textarea {
  width:100%;
  resize:vertical;
  min-height:80px}
.card-body .alert-number {
  font-size:11px;
  line-height:17px;
  color:#778291;
  margin-top:12px}
.merges a {
  display:flex;
  justify-content:space-between;
  gap:10px;
  padding-top:8px;
  color:#1677ff;
  overflow-wrap:anywhere}
.merges a span:last-child {
  flex-shrink:0;
  color:#778291}
.merges small {
  color:#778291;
  font-weight:400;
  font-size:11px}
.card-body .feedback {
  color:#778291;
  font-size:12px;
  margin-top:8px}
.metric .notes {
  padding:10px 12px;
  border-radius:8px;
  background:#f5f7fa}
@media(max-width:540px) {
  .card-actions button {
  min-height:40px}
}
</style>

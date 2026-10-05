<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { api, APIError, duration, labels, time, type Row } from "./api";

const user = ref<Row | null>(null),
  options = ref<Row>({}),
  loading = ref(false),
  error = ref(""),
  notice = ref("");
const groups = ref<Row[]>([]),
  rounds = ref<Row[]>([]),
  stats = ref<Row[]>([]),
  tasks = ref<Row[]>([]),
  entries = ref<Row[]>([]),
  messages = ref<Row>({ cards: [], topboxes: [] });
const aiStats = ref<Row[]>([]),
  mrStats = ref<Row[]>([]);
const config = ref<Row>({}),
  entity = ref("alert_project"),
  editing = ref<Row | null>(null),
  selected = ref<Row | null>(null);
const isH5 = location.pathname.startsWith("/h5/groups/"),
  groupID = location.pathname.split("/")[3] || "";
const page = ref(isH5 ? "h5" : "alerts"),
  nextCursor = ref(""),
  filterType = ref(""),
  filterStatus = ref(""),
  filterGroup = ref(""),
  filterProject = ref(""),
  filterResource = ref(""),
  search = ref(""),
  fromDate = ref(""),
  toDate = ref("");
const loadedMore = ref(false);
const showHistory = ref(false),
  action = ref(""),
  content = ref(""),
  operationKey = ref(""),
  saving = ref(false),
  lastSubmitted = ref(""),
  member = ref(false);
const tabs = [
  { id: "alerts", name: "告警查询", icon: "◈" },
  { id: "statistics", name: "统计概览", icon: "▥" },
  { id: "tasks", name: "AI 修复任务", icon: "✦" },
  { id: "notices", name: "业务提示", icon: "◇" },
  { id: "groups", name: "告警群", icon: "▦" },
  { id: "events", name: "接入待核对", icon: "◎" },
  { id: "jobs", name: "同步任务", icon: "↻" },
];
const configNames: Record<string, string> = {
  alert_project: "业务项目",
  alert_project_workspace: "修复工作区",
  alert_project_repo: "项目仓库",
  alert_source: "Hook 来源",
  alert_resource: "告警资源",
  alert_group: "告警群",
  alert_route: "固定路由",
  sys_user: "平台账号",
};
const currentTitle = computed(() =>
  page.value === "config"
    ? "配置管理"
    : page.value === "audit"
      ? "操作审计"
      : page.value === "messages"
        ? "模拟群消息"
        : tabs.find((t) => t.id === page.value)?.name || "告警处理工作台",
);
const unfinished = computed(() =>
  groups.value.reduce((sum, g) => sum + Number(g.unfinished || 0), 0),
);
const currentGroup = computed(() => groups.value.find((g) => g.id === groupID));
const round = computed(() => selected.value?.round || {});
const ai = computed(() => selected.value?.ai_tasks?.[0]);
const evidence = computed(() => selected.value?.evidence || {});
const isOpen = computed(() =>
  ["PENDING", "PROCESSING"].includes(round.value.round_status),
);
const syncFailed = computed(() =>
  selected.value?.cards?.some((c: Row) =>
    ["FAILED", "UNKNOWN"].includes(c.delivery_status),
  ),
);
const fields = computed(() => config.value.fields?.[entity.value] || []);
const configRows = computed<Row[]>(() => config.value[entity.value] || []);
const format = (value: unknown) =>
  typeof value === "object" ? JSON.stringify(value) : String(value ?? "—");
const label = (value: string) => labels[value] || value || "—";
let refreshTimer: ReturnType<typeof setInterval> | undefined;

/** 表单输入在轮询中保留；登录失效时停止展示受保护数据。 */
async function safely(fn: () => Promise<void>) {
  error.value = "";
  try {
    await fn();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "操作失败";
    if (
      e instanceof APIError &&
      (e.status === 401 || (e.status === 403 && e.message === "账号已停用"))
    ) {
      user.value = null;
      selected.value = null;
      rounds.value = [];
    }
  }
}
async function login(identity?: string) {
  loading.value = true;
  await safely(async () => {
    if (identity) {
      user.value = await api("/api/auth/demo", "POST", { identity });
      await refresh();
    } else {
      const result = await api("/api/auth/start");
      sessionStorage.setItem(
        "alertops_return",
        location.pathname + location.search,
      );
      location.assign(result.url);
    }
  });
  loading.value = false;
}
async function logout() {
  await safely(async () => {
    await api("/api/auth/logout", "POST", {});
    user.value = null;
    selected.value = null;
    rounds.value = [];
  });
}
async function queryRounds(append = false) {
  const query = new URLSearchParams();
  if (isH5) {
    query.set("group_id", groupID);
    if (!showHistory.value) query.set("unfinished", "1");
  } else if (filterGroup.value) query.set("group_id", filterGroup.value);
  for (const [k, v] of Object.entries({
    type: filterType.value,
    status: filterStatus.value,
    alert_no: search.value,
    project_id: filterProject.value,
    resource_id: filterResource.value,
  })) {
    if (v) query.set(k, v);
  }
  if (fromDate.value)
    query.set("from", String(new Date(fromDate.value + ":00+08:00").getTime()));
  if (toDate.value)
    query.set("to", String(new Date(toDate.value + ":00+08:00").getTime()));
  if (append && nextCursor.value) query.set("cursor", nextCursor.value);
  const result = await api("/api/rounds?" + query);
  loadedMore.value = append;
  rounds.value = append ? [...rounds.value, ...result.items] : result.items;
  nextCursor.value = result.next_cursor;
}
async function refresh() {
  if (!user.value) return;
  const result = await api("/api/groups");
  groups.value = result.items;
  if ((page.value === "alerts" || isH5) && !loadedMore.value)
    await queryRounds();
  else if (page.value === "statistics") {
    const result = await api("/api/stats");
    stats.value = result.items;
    aiStats.value = result.ai;
    mrStats.value = result.merge_requests;
  } else if (page.value === "tasks")
    tasks.value = (await api("/api/tasks")).items;
  else if (["notices", "events", "jobs"].includes(page.value))
    entries.value = (await api("/api/" + page.value)).items;
  else if (page.value === "config")
    config.value = await api("/api/admin/config");
  else if (page.value === "audit")
    entries.value = (await api("/api/admin/audit")).items;
  else if (page.value === "messages")
    messages.value = await api("/api/demo/messages");
  if (selected.value)
    selected.value = await api("/api/rounds/" + round.value.id);
  if (isH5) {
    try {
      await api("/api/h5/groups/" + groupID + "/membership");
      member.value = true;
    } catch {
      member.value = false;
    }
  }
}
async function navigate(id: string) {
  page.value = id;
  loadedMore.value = false;
  selected.value = null;
  action.value = "";
  loading.value = true;
  await safely(refresh);
  loading.value = false;
}
async function select(item: Row) {
  await safely(async () => {
    const detail = await api("/api/rounds/" + item.id);
    if (isH5 && detail.round.alert_group_id !== groupID)
      throw new Error("此告警不属于当前群");
    selected.value = detail;
    if (isH5) {
      const query = new URLSearchParams(location.search);
      query.set("round", String(detail.round.id));
      history.replaceState(null, "", location.pathname + "?" + query);
    }
    action.value = "";
    content.value = "";
    lastSubmitted.value = "";
    notice.value = "";
  });
}
function beginAction(kind: string) {
  action.value = kind;
  content.value = "";
  operationKey.value = crypto.randomUUID();
  lastSubmitted.value = "";
  notice.value = "";
}
async function submitAction() {
  if (!content.value.trim() || saving.value) return;
  if (lastSubmitted.value && lastSubmitted.value !== content.value)
    operationKey.value = crypto.randomUUID();
  lastSubmitted.value = content.value;
  saving.value = true;
  await safely(async () => {
    await api(`/api/h5/rounds/${round.value.id}/${action.value}`, "POST", {
      content: content.value,
      operation_key: operationKey.value,
    });
    notice.value = "处理记录已保存，群卡片正在同步。";
    action.value = "";
    content.value = "";
    await refresh();
  });
  saving.value = false;
}
function edit(row?: Row) {
  editing.value = {};
  for (const f of fields.value)
    editing.value![f.name] = row?.[f.name] ?? f.options?.[0] ?? "";
  if (row) editing.value!.id = row.id;
}
async function saveConfig() {
  saving.value = true;
  await safely(async () => {
    await api("/api/admin/config/" + entity.value, "PUT", editing.value);
    editing.value = null;
    notice.value = "配置已保存";
    await refresh();
  });
  saving.value = false;
}
async function checkWorkspace(row: Row) {
  await safely(async () => {
    const result = await api(
      "/api/admin/workspaces/" + row.id + "/check",
      "POST",
      {},
    );
    notice.value =
      result.workspace_status === "READY"
        ? "工作区检查通过"
        : result.blocked_reason;
    await refresh();
  });
}
async function simulate(kind: string) {
  await safely(async () => {
    await api("/api/demo/scenarios", "POST", { kind });
    notice.value = "模拟来源事件已通过 Hook 接入，正在处理";
    await refresh();
  });
}

onMounted(async () => {
  loading.value = true;
  await safely(async () => {
    options.value = await api("/api/auth/options");
    if (location.pathname === "/auth/callback") {
      const q = new URLSearchParams(location.search);
      user.value = await api("/api/auth/exchange", "POST", {
        code: q.get("authCode") || q.get("code"),
        state: q.get("state"),
      });
      const target = sessionStorage.getItem("alertops_return") || "/";
      location.replace(
        target.startsWith("/") && !target.startsWith("//") ? target : "/",
      );
      return;
    }
    try {
      user.value = await api("/api/me");
    } catch (e) {
      if (!(e instanceof APIError && e.status === 401)) throw e;
    }
    if (user.value) {
      await refresh();
      const rid = new URLSearchParams(location.search).get("round");
      if (rid) await select({ id: rid });
    }
  });
  loading.value = false;
  refreshTimer = setInterval(() => {
    if (user.value && !editing.value && !loading.value) safely(refresh);
  }, 5000);
});
onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer);
});
</script>

<template>
  <main v-if="!user" class="login-shell">
    <div class="login-art">
      <div class="brand-mark">A<span>✦</span></div>
      <p class="eyebrow">AI ALERTOPS</p>
      <h1>让每一条告警<br />都有清晰的处理结果。</h1>
      <p>从异常发现、AI 排查到人工确认，<br />所有进展回到同一条告警。</p>
      <div class="login-flow">
        <span>Hook 接入</span><i>→</i><span>协作处理</span><i>→</i
        ><span>结果可追溯</span>
      </div>
    </div>
    <section class="login-panel">
      <p class="eyebrow">统一身份 · 企业钉钉</p>
      <h2>登录告警平台</h2>
      <p class="muted">使用企业钉钉账号登录，后台与群工作台共用身份。</p>
      <button class="primary wide" :disabled="loading" @click="login()">
        使用钉钉快捷登录 →
      </button>
      <div v-if="options.demo" class="demo-login">
        <p>本地验证环境 · 外部服务为模拟服务</p>
        <button @click="login('admin')">以演示管理员登录</button
        ><button @click="login('member')">以群内普通用户登录</button
        ><button @click="login('outsider')">以非群成员用户登录</button>
      </div>
      <p v-if="error" role="alert" class="error">{{ error }}</p>
      <p class="fine">
        首次登录自动成为普通用户。账号停用后将立即失去访问权限。
      </p>
    </section>
  </main>
  <div v-else :class="['app-shell', { h5: isH5 }]">
    <aside v-if="!isH5" class="sidebar">
      <a href="/" class="brand"
        ><span class="logo-small">A</span><strong>AlertOps</strong
        ><span class="version">V1</span></a
      >
      <div class="nav-caption">告警协作</div>
      <nav>
        <button
          v-for="tab in tabs"
          :key="tab.id"
          :class="{ active: page === tab.id }"
          @click="navigate(tab.id)"
        >
          <span>{{ tab.icon }}</span
          >{{ tab.name }}<b v-if="tab.id === 'alerts'">{{ unfinished }}</b>
        </button>
      </nav>
      <template v-if="user.role === 'ADMIN'"
        ><div class="nav-caption">平台管理</div>
        <nav>
          <button
            :class="{ active: page === 'config' }"
            @click="navigate('config')"
          >
            <span>⚙</span>配置管理</button
          ><button
            :class="{ active: page === 'audit' }"
            @click="navigate('audit')"
          >
            <span>≡</span>操作审计
          </button>
        </nav></template
      >
      <nav v-if="options.demo">
        <div class="nav-caption">隔离验证</div>
        <button
          :class="{ active: page === 'messages' }"
          @click="navigate('messages')"
        >
          <span>▤</span>模拟群消息
        </button>
      </nav>
      <div class="sidebar-bottom">
        <span class="online-dot"></span>钉钉协作 · AI 自动排查
      </div>
    </aside>
    <div class="main-column">
      <header class="app-header">
        <div>
          <span class="muted small">{{
            isH5 ? currentGroup?.name : "工作空间 / 告警平台"
          }}</span>
          <h1>{{ currentTitle }}</h1>
        </div>
        <div class="identity">
          <span class="avatar">{{ user.display_name?.slice(0, 1) }}</span>
          <div>
            <strong>{{ user.display_name }}</strong
            ><small>{{ label(user.role) }}</small>
          </div>
          <button class="text-button" @click="logout">退出</button>
        </div>
      </header>
      <div v-if="options.demo" class="demo-banner">
        <strong>本地验证环境</strong
        ><span>数据保存在独立 MySQL；钉钉和 Codeup 使用模拟服务。</span>
        <div v-if="user.role === 'ADMIN'">
          <button @click="simulate('log')">生成日志告警</button
          ><button @click="simulate('metric')">生成指标告警</button
          ><button @click="simulate('notice')">发送业务提示</button
          ><button @click="simulate('recover')">模拟指标恢复</button>
        </div>
      </div>
      <div class="content">
        <p v-if="error" role="alert" class="error">{{ error }}</p>
        <p v-if="notice" role="status" class="success">{{ notice }}</p>
        <template v-if="page === 'alerts' || isH5">
          <section v-if="isH5" class="group-summary">
            <div>
              <span class="online-dot"></span
              ><strong>{{ currentGroup?.unfinished || 0 }} 项未完成</strong
              ><span class="muted">待处理与处理中均计入</span>
            </div>
            <label class="check"
              ><input
                type="checkbox"
                v-model="showHistory"
                @change="safely(() => queryRounds())"
              />查看已结束</label
            >
          </section>
          <section v-else class="intro">
            <div>
              <h2>统一查看每一轮告警</h2>
              <p>告警处理在所属群的卡片与工作台中完成。</p>
            </div>
            <span class="pill neutral"
              >当前未完成 <b>{{ unfinished }}</b></span
            >
          </section>
          <form class="filters" @submit.prevent="safely(() => queryRounds())">
            <input
              v-model="search"
              placeholder="搜索告警编号"
              aria-label="告警编号"
            /><select v-model="filterType" aria-label="告警类型">
              <option value="">全部类型</option>
              <option value="LOG">日志告警</option>
              <option value="METRIC">指标告警</option></select
            ><select v-model="filterStatus" aria-label="处理状态">
              <option value="">全部状态</option>
              <option
                v-for="s in ['PENDING', 'PROCESSING', 'HANDLED', 'RECOVERED']"
                :value="s"
              >
                {{ label(s) }}
              </option></select
            ><select v-if="!isH5" v-model="filterGroup" aria-label="告警群">
              <option value="">全部告警群</option>
              <option v-for="g in groups" :value="g.id">
                {{ g.name }}
              </option></select
            ><button type="submit" class="primary">查询</button>
            <details v-if="!isH5" class="more-filter">
              <summary>更多筛选</summary>
              <div>
                <input v-model="filterProject" placeholder="项目 ID" /><input
                  v-model="filterResource"
                  placeholder="资源 ID"
                /><label
                  >起始<input type="datetime-local" v-model="fromDate" /></label
                ><label
                  >截止（不含）<input type="datetime-local" v-model="toDate"
                /></label>
              </div>
            </details>
          </form>
          <div v-if="loading" class="empty">正在加载…</div>
          <div v-else-if="rounds.length === 0" class="empty">
            <span>✓</span>
            <h3>
              {{
                isH5 && !showHistory
                  ? "当前群没有未完成告警"
                  : "没有符合条件的告警"
              }}
            </h3>
            <p>已结束的事项保留在历史记录中。</p>
          </div>
          <div v-else-if="isH5" class="h5-list">
            <article
              v-for="item in rounds"
              :key="item.id"
              class="alert-tile"
              @click="select(item)"
              tabindex="0"
              @keydown.enter="select(item)"
            >
              <div class="tile-top">
                <span :class="['type-mark', item.alert_type.toLowerCase()]">{{
                  item.alert_type === "LOG" ? "!" : "↗"
                }}</span
                ><strong>{{ label(item.alert_type) }}</strong
                ><span :class="['pill', item.round_status]">{{
                  label(item.round_status)
                }}</span>
              </div>
              <h3>{{ item.title }}</h3>
              <dl>
                <dt>告警编号</dt>
                <dd>{{ item.alert_no }}</dd>
                <dt>业务项目</dt>
                <dd>{{ item.project_name }}</dd>
                <dt>首次发生</dt>
                <dd>{{ time(item.first_event_time) }}</dd>
                <dt>发生次数</dt>
                <dd>
                  {{ item.event_count }} · {{ label(item.count_quality) }}
                </dd>
              </dl>
              <footer>查看详情与处理记录 <span>→</span></footer>
            </article>
          </div>
          <div v-else class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>告警事项</th>
                  <th>类型 / 状态</th>
                  <th>所属项目与群</th>
                  <th>发生次数</th>
                  <th>首次 / 最近发生</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="item in rounds" :key="item.id">
                  <td>
                    <button class="row-title" @click="select(item)">
                      {{ item.title }}</button
                    ><code>{{ item.alert_no }}</code>
                  </td>
                  <td>
                    <div>{{ label(item.alert_type) }}</div>
                    <span :class="['pill', item.round_status]">{{
                      label(item.round_status)
                    }}</span>
                  </td>
                  <td>
                    {{ item.project_name }}<small>{{ item.group_name }}</small>
                  </td>
                  <td>
                    <strong>{{ item.event_count }}</strong
                    ><small>{{ label(item.count_quality) }}</small>
                  </td>
                  <td>
                    {{ time(item.first_event_time)
                    }}<small>{{ time(item.last_event_time) }}</small>
                  </td>
                  <td>
                    <button class="text-button" @click="select(item)">
                      详情 →
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <button
            v-if="nextCursor"
            class="load-more"
            @click="safely(() => queryRounds(true))"
          >
            加载更多
          </button>
        </template>
        <template v-else-if="page === 'statistics'"
          ><section class="intro">
            <div>
              <h2>近 7 天的告警变化</h2>
              <p>
                当前未完成按现态统计；新增与结束按各自发生时间统计，上海时区。
              </p>
            </div>
          </section>
          <div class="stat-grid">
            <article v-for="item in stats" class="panel">
              <h3>{{ label(item.alert_type) }}</h3>
              <div class="stat-values">
                <div>
                  <b>{{ item.unfinished }}</b
                  ><span>当前未完成</span>
                </div>
                <div>
                  <b>{{ item.created }}</b
                  ><span>期间新增</span>
                </div>
                <div>
                  <b>{{ item.completed }}</b
                  ><span>{{
                    item.alert_type === "LOG" ? "期间已处理" : "期间已恢复"
                  }}</span>
                </div>
              </div>
              <p class="muted">
                已结束轮次平均处理时长：{{
                  item.average_processing_ms == null
                    ? "—"
                    : duration(Number(item.average_processing_ms))
                }}
              </p>
            </article>
          </div>
          <div class="stat-grid">
            <article class="panel">
              <h3>AI 任务（期间入队）</h3>
              <p v-for="item in aiStats">
                {{ label(item.execution_status) }}：{{ item.total }}
                <small class="muted"
                  >平均执行
                  {{
                    item.average_execution_ms == null
                      ? "—"
                      : duration(Number(item.average_execution_ms))
                  }}</small
                >
              </p>
              <p v-if="!aiStats.length" class="muted">期间暂无 AI 任务</p>
            </article>
            <article class="panel">
              <h3>MR（期间关联）</h3>
              <p v-for="item in mrStats">
                {{ label(item.merge_status) }}：{{ item.total }}
              </p>
              <p v-if="!mrStats.length" class="muted">期间暂无合并请求</p>
            </article>
          </div>
          <p class="fine">
            日志事件持续时间、人工处理耗时、AI
            执行耗时分别保留，不合并为一个耗时。
          </p></template
        >
        <template v-else-if="page === 'tasks'"
          ><section class="intro">
            <div>
              <h2>AI 修复任务</h2>
              <p>
                每个日志轮次至多一次。每个项目最多 3
                个任务同时执行，其余按到达顺序排队。
              </p>
            </div>
          </section>
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>告警 / 项目</th>
                  <th>执行状态</th>
                  <th>阶段</th>
                  <th>修复分支</th>
                  <th>排队 / 开始</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="t in tasks">
                  <td>
                    <button
                      class="row-title"
                      @click="select({ id: t.alert_round_id })"
                    >
                      {{ t.alert_no }}</button
                    ><small>{{ t.project_name }}</small>
                  </td>
                  <td>
                    <span :class="['pill', t.execution_status]">{{
                      label(t.execution_status)
                    }}</span
                    ><small class="danger-text">{{ t.error_message }}</small>
                  </td>
                  <td>{{ label(t.phase) }}</td>
                  <td class="break">{{ t.branch_name || "定位后分配" }}</td>
                  <td>
                    {{ time(t.queued_time)
                    }}<small>{{ time(t.started_time) }}</small>
                  </td>
                </tr>
              </tbody>
            </table>
            <div v-if="!tasks.length" class="empty">尚无 AI 修复任务</div>
          </div></template
        >
        <template v-else-if="page === 'groups'"
          ><div class="group-grid">
            <article v-for="g in groups" class="panel">
              <div class="tile-top">
                <span class="type-mark">▦</span>
                <h3>{{ g.name }}</h3>
              </div>
              <p class="muted">{{ g.conversation_id }}</p>
              <p>
                <b class="big-number">{{ g.unfinished }}</b> 项未完成告警
              </p>
              <a class="button primary" :href="'/h5/groups/' + g.id"
                >打开群工作台 →</a
              >
            </article>
          </div></template
        >
        <template v-else-if="page === 'config'"
          ><div class="config-tabs">
            <button
              v-for="(name, key) in configNames"
              :class="{ active: entity === key }"
              @click="
                entity = key;
                editing = null;
              "
            >
              {{ name }}
            </button>
          </div>
          <div class="intro">
            <div>
              <h2>{{ configNames[entity] }}</h2>
              <p>
                配置变更自动留存审计。项目有活动任务时，工作区与仓库配置暂不可变更。
              </p>
            </div>
            <button
              v-if="entity !== 'sys_user'"
              class="primary"
              @click="edit()"
            >
              + 新增配置
            </button>
          </div>
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>ID</th>
                  <th v-for="f in fields">{{ f.label }}</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in configRows">
                  <td>
                    <code>{{ row.id }}</code
                    ><small v-if="row.workspace_status"
                      >{{ label(row.workspace_status) }}
                      {{ row.blocked_reason }}</small
                    ><small v-if="entity === 'sys_user'">{{
                      row.display_name
                    }}</small>
                  </td>
                  <td v-for="f in fields" class="break">
                    {{ label(String(row[f.name] ?? "")) }}
                  </td>
                  <td>
                    <button @click="edit(row)">编辑</button
                    ><button
                      v-if="entity === 'alert_project_workspace'"
                      @click="checkWorkspace(row)"
                    >
                      检查目录
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div></template
        >
        <template v-else-if="page === 'messages'"
          ><section class="intro">
            <div>
              <h2>外部消息服务收到的内容</h2>
              <p>
                显示同步任务实际调用模拟服务的结果；按稳定投递 ID 保留消息实例。
              </p>
            </div>
            <span class="pill neutral">调用 {{ messages.calls }} 次</span>
          </section>
          <article v-for="top in messages.topboxes" class="group-summary">
            <b
              >群吊顶：{{
                top.desired_status === "OPEN" ? "已打开" : "已关闭"
              }}</b
            ><span>{{ top.total }} 项未完成</span
            ><a :href="top.h5_url">打开工作台</a>
          </article>
          <div class="h5-list">
            <article v-for="card in messages.cards" class="alert-tile">
              <div class="tile-top">
                <strong>{{ label(card.card_type) }}</strong
                ><span :class="['pill', card.round_status]">{{
                  label(card.round_status)
                }}</span>
              </div>
              <h3>{{ card.title || card.notice?.class_name }}</h3>
              <p>{{ card.alert_no || card.notice?.service_key }}</p>
              <p class="muted">
                {{ card.result_text || card.notice?.message }}
              </p>
              <small class="break"
                >投递 ID：{{ card.out_track_id }} · 版本
                {{ card.version }}</small
              ><a v-if="card.h5_url" class="button" :href="card.h5_url"
                >查看详情 / 处理</a
              >
            </article>
          </div></template
        >
        <template v-else-if="page === 'notices'"
          ><article v-for="item in entries" class="panel notice-card">
            <span class="pill neutral">业务提示</span>
            <h3>{{ item.class_name }}</h3>
            <p class="muted">
              {{ item.service_key }} · {{ time(item.received_time) }}
            </p>
            <pre>{{ item.message }}</pre>
          </article>
          <div v-if="!entries.length" class="empty">暂无业务提示</div></template
        >
        <template v-else
          ><div v-if="!entries.length" class="empty">
            <span>✓</span>
            <h3>
              {{ page === "audit" ? "暂无审计记录" : "没有待处理的异常任务" }}
            </h3>
          </div>
          <article v-for="item in entries" class="panel issue-row">
            <span
              :class="[
                'pill',
                item.process_status || item.job_status || item.result_status,
              ]"
              >{{
                label(
                  item.process_status || item.job_status || item.result_status,
                )
              }}</span
            >
            <h3>
              {{ item.job_type || item.action_type || label(item.alert_type) }}
            </h3>
            <code>{{ item.id }}</code>
            <p>
              {{ item.process_error || item.last_error || item.target_type }}
            </p>
            <small>{{
              time(
                item.received_time || item.occurred_time || item.next_run_time,
              )
            }}</small>
            <details v-if="page === 'audit'">
              <summary>变更详情</summary>
              <pre>{{
                JSON.stringify(
                  { before: item.before_data, after: item.after_data },
                  null,
                  2,
                )
              }}</pre>
            </details>
          </article></template
        >
      </div>
      <footer class="app-footer">
        AI AlertOps · 所有业务记录可追溯 <span>Asia/Shanghai</span>
      </footer>
    </div>
    <div v-if="selected" class="overlay" @click.self="selected = null">
      <section
        :class="['detail-drawer', { sheet: isH5 }]"
        role="dialog"
        aria-modal="true"
        aria-label="告警详情"
      >
        <header class="drawer-header">
          <div>
            <span class="eyebrow"
              >{{ label(round.alert_type) }} / {{ round.alert_no }}</span
            >
            <h2>{{ round.title }}</h2>
          </div>
          <button aria-label="关闭详情" @click="selected = null">✕</button>
        </header>
        <div class="drawer-content">
          <div class="detail-status">
            <span :class="['pill', round.round_status]">{{
              label(round.round_status)
            }}</span
            ><span class="muted"
              >第 {{ round.round_no }} 轮 ·
              {{ label(round.count_quality) }}</span
            >
          </div>
          <dl class="facts">
            <dt>业务项目</dt>
            <dd>{{ round.project_name }}</dd>
            <dt>告警资源</dt>
            <dd>{{ round.resource_name }}</dd>
            <dt>归属群</dt>
            <dd>{{ round.group_name }}</dd>
            <dt>告警编号</dt>
            <dd>
              <code>{{ round.alert_no }}</code>
            </dd>
            <template v-if="round.alert_type === 'LOG'"
              ><dt>缺陷 ID</dt>
              <dd>{{ evidence.trace_id || "上游未提供 Trace ID" }}</dd>
              <dt>异常类</dt>
              <dd>{{ evidence.class_name }}</dd></template
            ><template v-else
              ><dt>指标 / 规则</dt>
              <dd>{{ evidence.metric_key }} · {{ evidence.metric_rule }}</dd>
              <dt>当前事件值</dt>
              <dd>
                {{
                  evidence.value_status === "MISSING"
                    ? "上游未提供恢复值"
                    : evidence.metric_value_raw
                }}
              </dd>
              <dt>原始周期</dt>
              <dd>{{ evidence.starts_at_raw }}</dd></template
            >
            <dt>首次发生</dt>
            <dd>{{ time(round.first_event_time) }}</dd>
            <dt>最近发生</dt>
            <dd>{{ time(round.last_event_time) }}</dd>
            <dt>发生次数</dt>
            <dd>{{ round.event_count }}</dd>
            <dt>
              {{ round.alert_type === "LOG" ? "日志持续时间" : "周期持续时间" }}
            </dt>
            <dd>
              {{ duration(round.last_event_time - round.first_event_time) }}
            </dd>
            <template v-if="!isOpen"
              ><dt>结束时间</dt>
              <dd>{{ time(round.end_time) }}</dd>
              <dt>处理耗时</dt>
              <dd>
                {{ duration(round.end_time - round.first_event_time) }}
              </dd></template
            >
          </dl>
          <div v-if="round.alert_type === 'LOG'" class="log-box">
            <strong>异常日志</strong>
            <pre>{{ evidence.message }}</pre>
          </div>
          <section v-if="round.result_text" class="result-box">
            <h3>人工处理结果</h3>
            <p>{{ round.result_text }}</p>
            <small>{{ time(round.end_record_time) }}</small>
          </section>
          <section v-if="ai" class="detail-section">
            <h3><span class="ai-spark">✦</span> AI 排查与修复</h3>
            <p>
              <span :class="['pill', ai.execution_status]">{{
                label(ai.execution_status)
              }}</span>
              {{ label(ai.phase) }} · {{ label(ai.business_result) }}
            </p>
            <p v-if="ai.error_message" class="danger-text">
              {{ ai.error_message }}
            </p>
            <dl>
              <dt>修复分支</dt>
              <dd class="break">
                {{ ai.branch_name || "定位后从 master 建立" }}
              </dd>
              <dt>交付情况</dt>
              <dd>
                {{
                  ai.delivery_status === "PENDING"
                    ? "待交付"
                    : label(ai.delivery_status)
                }}
                ·
                {{
                  ai.delivery_set_status === "OPEN"
                    ? "交付集合未封口"
                    : "交付集合已封口"
                }}
              </dd>
              <dt>执行耗时</dt>
              <dd>
                {{
                  ai.started_time
                    ? duration(
                        (ai.finished_time || Date.now()) - ai.started_time,
                      )
                    : "尚未开始"
                }}
              </dd>
            </dl>
            <p v-if="selected.all_merged" class="success">
              必要修复分支全部已合并。告警仍需人工确认处理结果。
            </p>
            <div v-for="repo in selected.task_repos" class="repo-row">
              <strong>{{ repo.repo_code }}</strong
              ><span
                >{{ label(repo.verification_status) }} ·
                {{ label(repo.delivery_status) }} · 必要 MR
                {{ repo.required_mr_count }}</span
              ><small>{{
                repo.verification_summary || repo.error_message
              }}</small>
            </div>
          </section>
          <section v-if="selected.merge_requests.length" class="detail-section">
            <h3>
              合并请求
              <span class="count">{{ selected.merge_requests.length }}</span>
            </h3>
            <a
              v-for="mr in selected.merge_requests"
              class="mr-link"
              :href="mr.url"
              target="_blank"
              rel="noopener noreferrer"
              ><div>
                <strong>#{{ mr.display_number }} {{ mr.title }}</strong
                ><small
                  >{{ mr.source_branch }} → {{ mr.target_branch }} · 评审：{{
                    label(mr.review_status)
                  }}</small
                >
              </div>
              <span :class="['pill', mr.merge_status]">{{
                label(mr.merge_status)
              }}</span
              ><span>↗</span></a
            >
          </section>
          <section class="detail-section">
            <h3>
              处理记录 <span class="count">{{ selected.records.length }}</span>
            </h3>
            <div v-if="!selected.records.length" class="muted">
              暂无处理记录，可按需添加备注。
            </div>
            <ol class="timeline">
              <li v-for="record in [...selected.records].reverse()">
                <div>
                  <strong>{{ record.actor_name }}</strong
                  ><span>{{ label(record.record_type) }}</span
                  ><time>{{ time(record.occurred_time) }}</time>
                </div>
                <p>{{ record.content }}</p>
              </li>
            </ol>
          </section>
          <section class="detail-section">
            <h3>卡片同步</h3>
            <p v-if="syncFailed" class="warning">
              业务记录已经保存；群卡片存在待核对或失败的同步任务，平台会继续补偿。
            </p>
            <div v-for="c in selected.cards" class="sync-row">
              <span>{{ label(c.delivery_status) }}</span
              ><small>版本 {{ c.sent_version }} / {{ c.desired_version }}</small
              ><span class="danger-text">{{ c.last_error }}</span>
            </div>
          </section>
          <details class="detail-section">
            <summary>来源事件（最近 100 条）</summary>
            <article v-for="e in selected.events" class="event-evidence">
              <code>{{ e.id }}</code
              ><span
                >{{ e.event_type }} · {{ label(e.dedup_quality) }} ·
                {{ time(e.received_time) }}</span
              >
              <pre>{{ JSON.stringify(e.payload, null, 2) }}</pre>
            </article>
          </details>
        </div>
        <footer class="drawer-actions">
          <p v-if="notice" class="success" role="status">{{ notice }}</p>
          <p v-if="error" class="error" role="alert">{{ error }}</p>
          <template v-if="isH5 && isOpen && member"
            ><form v-if="action" @submit.prevent="submitAction">
              <label :for="'action-content'">{{
                action === "handle" ? "处理结果（必填）" : "追加备注（必填）"
              }}</label
              ><textarea
                id="action-content"
                v-model="content"
                maxlength="8000"
                rows="3"
                :placeholder="
                  action === 'handle'
                    ? '说明修复生效与验证结果，或无需修复的依据'
                    : '记录排查进展，不会中断 AI 执行'
                "
              ></textarea>
              <div>
                <button type="button" :disabled="saving" @click="action = ''">
                  取消</button
                ><button class="primary" :disabled="saving || !content.trim()">
                  {{
                    saving
                      ? "正在保存…"
                      : action === "handle"
                        ? "确认已处理"
                        : "保存备注"
                  }}
                </button>
              </div>
            </form>
            <div v-else class="action-buttons">
              <button @click="beginAction('notes')">追加备注</button
              ><button
                v-if="round.alert_type === 'LOG'"
                class="primary"
                @click="beginAction('handle')"
              >
                标记已处理</button
              ><span v-else class="muted">指标由监控确认恢复</span>
            </div></template
          ><template v-else-if="!isH5"
            ><span class="muted">管理后台提供查询与统计</span
            ><a
              class="button primary"
              :href="
                '/h5/groups/' + round.alert_group_id + '?round=' + round.id
              "
              >打开群工作台 →</a
            ></template
          >
          <p v-else class="muted">
            {{
              !isOpen
                ? "本轮已结束，保留完整处理历史。"
                : "当前账号未通过本群成员验证，只能查看。"
            }}
          </p>
        </footer>
      </section>
    </div>
    <div v-if="editing" class="overlay" @click.self="editing = null">
      <form class="config-dialog" @submit.prevent="saveConfig">
        <header>
          <h2>{{ editing.id ? "编辑" : "新增" }}{{ configNames[entity] }}</h2>
          <button type="button" aria-label="关闭配置" @click="editing = null">
            ✕
          </button>
        </header>
        <label v-for="f in fields"
          >{{ f.label }}<span v-if="f.optional" class="muted">（可选）</span
          ><select v-if="f.options" v-model="editing[f.name]">
            <option v-for="option in f.options" :value="option">
              {{ label(option) }}
            </option></select
          ><input
            v-else
            v-model="editing[f.name]"
            :required="!f.optional"
            :placeholder="f.kind === 'id' ? '复制对应配置的 ID' : ''"
        /></label>
        <p v-if="error" class="error">{{ error }}</p>
        <footer>
          <button type="button" @click="editing = null">取消</button
          ><button class="primary" :disabled="saving">
            {{ saving ? "正在保存…" : "保存配置" }}
          </button>
        </footer>
      </form>
    </div>
  </div>
</template>

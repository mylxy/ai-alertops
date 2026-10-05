# V1 运行与接口说明

本说明对应 [Issue #1](https://github.com/mylxy/ai-alertops/issues/1) 的首版实现，业务定义见[整体设计](../design/platform/README.md)。首次启动、依赖和环境变量见[根 README](../../README.md)。当前真实外部验收状态见[实施记录](issue-1.md)。

## 部署组成

一个 Go HTTP 进程提供 API、静态 Vue 页面和持久任务处理。MySQL 保存事实、约束、任务与外部身份；没有额外消息中间件。启用 `-runner` 时，同一个宿主进程管理固定服务器 Docker；宿主账号需要操作该服务器 Docker 的权限。容器只挂载分配到的工作区和本任务产物目录，不挂载 Docker socket。

生产先用有 DDL 权限的账号执行 `-migrate`。正常启动使用独立的读写账号，不执行建表。初始脚本 `001_initial.sql` 用于空库；以后修改结构须追加迁移，不能依赖 `CREATE TABLE IF NOT EXISTS` 更新已存在的表。本次任务的数据库均为独占测试库或演示库，不迁移旧平台。

服务只监听本机时，通过反向代理提供 HTTPS。`ALERTOPS_BASE_URL` 是浏览器可访问的统一域名；`ALERTOPS_RUNNER_CALLBACK_URL` 是容器可到达的平台地址，生产建议使用 HTTPS。多进程部署必须使用不同 Snowflake 节点号；宿主调度进程必须共用同一产物根目录与服务器 Docker，不能扩成多服务器调度。

## 身份和权限

- 生产入口为企业钉钉 OAuth，要求授权范围 `openid corpid`，平台核实企业与官方 unionId → userid 映射。首次登录为 ACTIVE/USER，首位管理员由明确配置的 unionId 引导，不能由任意首位访问者抢占。
- 登录 Cookie 有效期 8 小时，HttpOnly、SameSite=Lax，HTTPS 使用 Secure。每次请求重新检查数据库角色和停用状态。
- USER 可查询全平台；ADMIN 额外配置和账号管理。后台没有告警处理接口入口。H5 写操作和 Stream 卡片操作每次实时检查当前群成员，无法确认时拒绝。
- H5 的静态外壳可以加载，数据、详情和处理操作必须通过登录。Cookie 写接口需要 `X-AlertOps-Request: 1`，并检查 Origin；表单不能提供作者和处理时间。
- 酷应用快捷入口配置为平台根地址。钉钉附加的 `openConversationId` 在登录后映射为平台已配置群，直接显示该群 H5；未知群显示未配置提示，不退回全平台查询。带参数的原入口在 OAuth 往返后保留。
- Stream 回调使用官方 SDK 连接验证，按外部卡片实例确定原群、原轮次。首次未开通用户不能仅靠卡片自动开通；停用用户不能借回调恢复账号。

## API 概览

JSON 请求；错误返回 `{"error":"原因"}`。内部 BIGINT ID 对浏览器输出字符串，避免 JS 精度损失。时间为 Unix 毫秒，页面按 Asia/Shanghai 显示。所有普通查询都做敏感字段和文本脱敏。

| 入口 | 身份 | 行为 |
| --- | --- | --- |
| `GET /healthz` | 无 | 进程存活检查 |
| `GET /api/auth/start`、`POST /api/auth/exchange` | OAuth state | 获取授权地址、交换可信身份 |
| `GET /api/me`、`POST /api/auth/logout` | 平台会话 | 当前账号、退出 |
| `POST /hook/sls` | 来源身份 | 日志及 NOTICE 接收 |
| `POST /hook/alert` | 来源身份 | 指标 firing/resolved 批次接收 |
| `GET /api/rounds`、`GET /api/rounds/{id}` | USER/ADMIN | 筛选轮次、记录、AI、MR、证据和卡片同步状态 |
| `GET /api/groups`、`/api/notices`、`/api/events`、`/api/tasks`、`/api/jobs` | USER/ADMIN | 群、提示、待核实事件、执行和同步任务 |
| `GET /api/stats` | USER/ADMIN | 未完成、期间新增/完成、平均处理耗时、AI/MR 分布 |
| `GET /api/h5/groups/{id}/membership` | 平台会话 | 当前是否可以处理该群 |
| `POST /api/h5/rounds/{id}/notes` | 当前群成员 | 追加非空备注 |
| `POST /api/h5/rounds/{id}/handle` | 当前群成员 | 非空结果结束 LOG；METRIC 拒绝人工结束 |
| `GET /api/admin/config`、`PUT /api/admin/config/{entity}` | ADMIN | 配置元数据、校验后新增或修改 |
| `GET /api/admin/audit` | ADMIN | 配置及角色变更审计 |
| `POST /api/admin/workspaces/{id}/check` | ADMIN | 无任务占用时检查工作区 |
| `POST /hook/ai/{id}` | 当前任务 token | 顺序业务进度，不能操作其他任务 |
| `GET /api/ai/tasks/{id}`、`POST /api/ai/tasks/{id}/merge-requests` | 当前任务 token | 执行器状态与稳定交付项登记 |
| `POST /hook/codeup` | `X-Codeup-Token` | 持久化提示，再向权威服务核实实际 MR |

演示身份和场景入口只在 `-demo` 注册。演示模式限制回环监听；不可作为生产账号系统。

`/api/rounds` 支持 `alert_no`、`type`、`status`、`project_id`、`resource_id`、`group_id`、`unfinished=1`、`from`、`to`、`cursor`。每页 50 条，按 `first_event_time DESC,id DESC` 排序；`next_cursor` 非空才继续翻页。时间范围左闭右开。期间新增依据首次发生时间；期间完成依据结束时间；当前未完成不受期间范围影响。AI 分布依据入队时间，MR 分布依据平台关联时间，不将它们伪装成发生趋势图。

人工写入字段为 `content`、`operation_key`。一次用户提交生成一个 UUID，传输重试复用；另一次真实提交必须生成新键，即使文字一样。同键绑定操作、入口、操作者和内容。卡片成功回调返回该用户私有的下一操作键，模板必须以私有变量读取。

## Hook 接入样例

先配置来源、资源、固定群路由。来源记录只保存 `secret_ref` 环境变量名，不保存明文；请求携带 `X-Alert-Source` 和 `Authorization: Bearer <来源密钥>`。如上游提供可靠投递 ID，可传 `Idempotency-Key`，不得把永久固定的规则 ID 当成每次投递 ID。

日志保留原字段：

```json
{"namespace_name":"demo","container_name":"api","class":"OrderService","level":"ERROR","message":"订单查询异常","traceId":"trace-example","@timestamp":"2026-10-05T01:00:00.123Z","_index":"logs-20261005","_id":"event-001"}
```

`level=NOTICE`（兼容历史 `notify`）只创建业务提示卡。没有投递 ID、也没有 `_index + _id` 的日志接收按独立观测计数，并明确 BEST_EFFORT 能力；相同正文不足以证明重复投递。缺少可信发生时间的终态后日志不会猜测归属。

指标保留旧 `alerts` 批次和旧 `annotations.summary` 解析，也支持结构化标签：

```json
{"alerts":[{"status":"firing","startsAt":"2026-10-05T01:00:00.000000001Z","labels":{"instanceId":"demo-server","instanceName":"示例服务器","metricKey":"cpu","metricValue":"95","metricRuleId":"cpu-high"},"annotations":{}}]}
```

恢复使用同一 `startsAt`、`status=resolved`、有效 `endsAt`。恢复值没有提供时省略 `metricValue`，页面明确显示缺失。未知周期恢复或同问题重叠周期进入待核实；原始纳秒周期不会先降为毫秒再匹配。有效批次先原子保存事件和详情，成功返回后由持久任务聚合。

## Docker、工作区与修复交付

每项目配置三个固定目录，slot_no=1、2、3。每个目录下，各仓库 `relative_path` 对应独立普通 clone；项目自己的 Skill 和 AGENTS.md 预先准备。目录不得重叠、使用符号链接别名或 Git worktree。当前宿主探测仅允许常规 clone 配置，拒绝 Git include、执行过滤器、fsmonitor、共享元数据及子模块；子模块代码按独立仓库登记。此限制避免容器修改 Git 配置后在宿主执行命令。

任务原子领取项目、槽位和分配版本；STARTING/RUNNING/FINALIZING 合计最多三个，其余 FIFO。创建容器和排队都不把告警变为处理中。CLI 已启动且通过平台门槛后才交付任务提示；人工提前关闭时退出，已开始的任务继续记录到原轮次。

执行器按定位 → 登记必要仓库 → fetch 最新 origin/master → 普通 hotfix 分支 → 修改/测试/提交 → 推送对应分支 → 稳定 MR 交付项的流程运行。分支为 `hotfix-YYYYMMDD-HHmmssSSS-AL编号`，上海时区、毫秒。宿主不执行修复仓库的构建或测试，实际命令留在容器里。真实 Git/MR 的权限取决于项目注入的凭据；本次平台开发没有替用户推送当前 GitHub 仓库。

必要仓库集合必须完整封存，失败仓库也保留。MR 数量动态，H5 展示全部，卡片最多展示前八条并提供完整入口。合并必须经 Codeup 权威查询确认组织、仓库、分支、源版本和合并提交；评审通过不是合并，合并不关闭告警。外部创建超时先按稳定标记查询；已发出请求但暂时查询不到时持续核对，不能再次创建。

宿主确认 Docker 退出、保存产物并检查目录后才释放。心跳中断和重启只核对同一个容器；不会重跑 CLI。脏目录仅阻塞自己的槽位。没有人工重跑按钮，失败由人工接续处理。并行分支最终合并冲突仍由评审人员处理，不自动 rebase 或 merge。

## 失败与恢复

| 现象 | 平台行为 | 运维处理 |
| --- | --- | --- |
| Hook 401/400 | 未接收或整批拒绝 | 核对来源认证和字段，不换 ID 掩盖冲突 |
| 来源有事件但缺路由/周期冲突 | 持久保留 NEEDS_REVIEW | 查询原因后修正后续配置；本版不提供任意改绑历史轮次 |
| 明确投递失败 | 保留业务，原外部 ID 退避重试 | 修正模板、权限、群安装等原因 |
| 卡片或吊顶 SENDING/UNKNOWN | 暂停该外部目标后续写入，其他正常目标继续 | 核对提供方原请求；当前真实钉钉适配器无可靠完成查询协议，不能自动解除未知状态 |
| MR 创建响应丢失 | 只按稳定交付标记查原请求 | 保留现场，待权威服务可见；禁止换键重建 |
| Docker 不可达/运行状态未知 | 保留原槽位占用 | 恢复 Docker 后核对原容器，不能直接改数据库释放 |
| CLI 非零/缺失/非法最终结果 | FAILED，保存原因及产物 | 人工继续，不自动再次运行 |
| 正常结束但工作区脏/配置异常 | 任务结束，工作区 BLOCKED | 无占用时人工检查并修复现场，再通过后台检查 |

未知投递的保守挂起保障不被迟到旧写覆盖，但**不能保证该目标自动恢复到最新展示**。上线前必须针对真实钉钉确定可验证的协调方法；不得通过盲改 `sent_version`、换 outTrackId 或重复人工提交来伪造同步成功。

产物位于 `<artifact_root>/<task_id>`：上下文及配置/初始基线、Skill 内容哈希、原始 Codex JSONL、结构化业务事件、最终结果、CLI 版本/退出码、容器日志。宿主拒绝链接/特殊读取文件，日志经独立临时文件原子替换。短期 token 和 Git 凭据文件收尾时删除，数据库任务 token 随终态撤销。原始产物只供受控宿主运维查看，不能通过普通查询下载；其中可能含业务日志，须按组织规则备份和保留。建议终态任务产物保留 30 天后归档；当前未实现自动删除，不能误清运行任务。

## 真实接入验收顺序

1. 配置测试企业应用，验证真实 OAuth、停用、退群，导入并发布四类模板，验证 Stream 回调和私有操作键。
2. 在专用测试群验证首次卡、连续备注、多卡同步、真实吊顶归零关闭；桌面钉钉侧栏和真手机半浮层分别验收。
3. 测试 Codeup 仓库完成普通分支、真实推送、三个 MR、评审与实际合并、重复/提前 Hook、合并提交核对。
4. 固定测试服务器配置三个 clone、项目依赖和模型 API 凭据，运行真实 Codex 修复；记录成本、超时、退出与失败现场。当前默认镜像只提供通用 Git/Python/Node/Codex，Java/Go 等业务依赖应按项目派生镜像固定版本。
5. 注入网络未知响应并证明不会重复建卡/MR或覆盖新状态，完成提供方协调验收后再标记 V1 生产交付通过。

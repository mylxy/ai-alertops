# 测试入口与边界

本套件采用用户确认的 API/Hook + 真实 MySQL 边界。测试文件位于 `backend/internal/platform`，集成场景带 `//go:build integration`。测试默认串行使用一个本任务独占库，禁止 `t.Parallel` 或多个进程同时重置同一库。

## 运行

```bash
# 专用库必须预先创建；测试会清空其中的本平台数据。
export ALERTOPS_TEST_DSN='root:LOCAL_PASSWORD@tcp(127.0.0.1:13306)/alertops_test'
./scripts/check.sh

# 构建固定 Codex 镜像，开启真实 Docker 协议验证。
make runner
ALERTOPS_TEST_DOCKER=1 ./scripts/check.sh

# 单场景示例
cd backend
go test -race -tags=integration ./internal/platform -run TestMRReconciliation -count=1
```

不提供 DSN 时集成测试失败，不会自动连接共享数据库。库名须包含 `test` 或 `demo`，这个限制不能替代运维确认库为独占。`-demo -seed` 同样是隔离环境重置入口。

`check.sh` 依次运行 go vet、含竞态检测的完整集成套件、Vue/TypeScript 检查与 Vite 构建、Python 语法检查。Docker 开关默认关闭，开启后使用真实容器和 Git clone，但将容器内 Codex 命令替换为确定性协议程序，避免日常测试消耗模型调用或修改外部仓库。

## 场景索引

| 文件 | 主要验证 |
| --- | --- |
| `lifecycle_integration_test.go` | Hook 持久接收、强去重、备注/关闭、历史不变 |
| `policy_integration_test.go` | 指标纳秒周期、重叠周期、恢复缺值、普通用户全局读、群写权限、NOTICE |
| `identity_integration_test.go` | 可信 Stream 身份、相同文字独立提交、请求重试、私有下一键、退群、停用旧会话 |
| `races_integration_test.go` | 多人备注/关闭竞争、真实重复与强键冲突、复发新编号、启动门槛与人工关闭 |
| `delivery_integration_test.go` | H5 新通知、所有历史卡最新状态、吊顶归零真正关闭的外部协议 |
| `recovery_integration_test.go` | 迟到 firing 不覆盖恢复证据、未知结果暂停、同轮正常卡独立更新、长字段、明确失败重试 |
| `ai_integration_test.go` | 五任务并发领取最多三槽、唯一分配、实际开始与人工状态、顺序回调、收尾状态 |
| `mr_integration_test.go` | 三仓库三 MR、创建响应未知、早到 Hook、先封存后补偿、评审与真实合并、完整必要集合 |
| `runtime_integration_test.go` | 真实 Docker、独立 Git clone、同容器重启核对、成功/非零/缺失/非法/无法定位/产物链接六场景、凭据清理与槽位释放 |
| `query_integration_test.go` | 同毫秒 51 项分页、期间边界、未送达及多卡不误计数；实际 MySQL 23 表、字符集、主键、无外键、自增/非空和第四槽位拒绝 |
| `adapters_test.go` | 真实适配器的受控 HTTP 契约：OAuth、成员分页、卡片/吊顶、业务失败、动态 MR 脱敏、Codeup patch 证据 |
| `artifacts_test.go` | 不跟随链接、不阻塞 FIFO、有界产物读取；宿主拒绝执行仓库 fsmonitor 配置 |

测试通过平台行为断言副作用，不模拟 SQL 层。无法由公开结果证明的数据库约束、占用和文件安全边界直接检查真实数据库/临时目录。

## 本次验证环境

2026-10-05，Go 1.26.3、Node 26、Docker 29.4.0/OrbStack、MySQL 8.4。独占容器 `ai-alertops-mysql-test` 映射本机 13316，`alertops_test` 用于回归、`alertops_demo` 用于浏览器演示。Docker 测试动态创建并清理自身任务容器及临时 clone，不操作真实项目仓库。

浏览器验收已操作管理员登录、后台详情、H5 连续处理、指标恢复和历史；桌面 1440×960、移动 390×844。移动视口验证无横向溢出。该移动尺寸测试不等同于真实手机钉钉容器测试。

真实钉钉已可通过桌面自动化读取现有测试群，但本平台尚未配置其企业应用凭据、发布模板和 Codeup 测试仓库；未在现有用户草稿上发消息。真实企业授权、模板编译/发布、原生吊顶、真手机、实际 MR 和模型修复不能据此标记通过。详见[实施与验收记录](../docs/implementation/issue-1.md)。

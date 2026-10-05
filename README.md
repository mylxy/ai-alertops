# AI AlertOps

日志与指标 Hook 驱动的告警协作平台：Go 后端、MySQL 8.4、Vue 管理后台与群 H5、钉钉卡片/Stream 适配器，以及固定服务器 Docker + Codex CLI 修复执行器。

本次以 [Issue #1](https://github.com/mylxy/ai-alertops/issues/1) 为完整规格，在一个开发任务内分阶段实现，没有创建子 Issue。当前可运行完整本地演示和真实 MySQL/Docker 回归；真实企业钉钉、Codeup 和模型修复验收的状态详见[实施与验收记录](docs/implementation/issue-1.md)，不能把本地替身结果视为生产接入完成。

## 本地运行

需要 Go 1.26.3、Node.js 24 及以上、Docker。首次运行：

```bash
make demo-db
make build
cd backend
ALERTOPS_MYSQL_DSN='root:alertops-local-demo@tcp(127.0.0.1:13306)/alertops_demo' \
  ./bin/alertops -demo -seed -frontend ../frontend/dist
```

打开 [本地告警平台](http://127.0.0.1:18080)。页面提供演示管理员、群内普通用户、非群成员三个身份，以及日志、指标、NOTICE、恢复事件入口。普通用户可查询全平台；群外身份只能查看；处理入口位于群 H5。演示不启动 AI 容器，也不发送真实钉钉消息或提交真实 MR。

`-seed` **会清空所选隔离演示库的本平台数据**，仅首次初始化使用。重启时去掉该参数以保留业务。程序限制演示模式只能监听回环地址；演示账号绝不用于生产。模拟外部消息服务保存在内存，重启后的历史事实以数据库卡片同步记录为准。

## 验证

[集成测试索引](integration/INTEGRATION_TESTS.md) 说明独占数据库、测试入口与外部边界。设置专用测试库后运行：

```bash
ALERTOPS_TEST_DSN='root:LOCAL_PASSWORD@tcp(127.0.0.1:13306)/alertops_test' ./scripts/check.sh
make runner
cd backend
ALERTOPS_TEST_DOCKER=1 ALERTOPS_TEST_DSN='root:LOCAL_PASSWORD@tcp(127.0.0.1:13306)/alertops_test' \
  go test -tags=integration ./internal/platform -run TestDockerLifecycle -count=1 -timeout=90s
```

测试入口会拒绝未显式提供 DSN；库名必须带 `test` 或 `demo`，且必须是本次任务独占的库。套件重建其中的业务数据，不可指向共享环境。

## 真实部署

1. 在独立 MySQL 8.4 数据库中使用 DDL 账号运行 `backend/bin/alertops -migrate`；配置见 [.env.example](.env.example)。正常服务连接只需本库查询/写入权限，不执行 DDL。
2. 配置专用企业应用、OAuth 回调 `https://域名/auth/callback`、Stream 卡片能力、可查询成员的场景群/酷应用和四个发布模板，见[卡片说明](cards/README.md)。明确指定首次管理员 unionId。
3. 配置来源密钥引用、项目/资源/固定群路由、Codeup 组织与数字仓库 ID。Codeup Webhook 指向 `/hook/codeup` 并设置 `X-Codeup-Token`；宿主只创建/查询 MR，没有自动合并接口。
4. 每项目预置三个互不重叠的固定目录，每目录包含对应仓库的独立 Git clone 与项目 Skill。只支持普通 clone，不能配置 Git worktree 或共享 `.git`。通过管理员“检查目录”后投入使用。
5. 构建并固定执行器镜像版本或 digest；为项目配置 Codex API 凭据引用、仓库 Git 凭据引用。执行器默认 2 CPU、4 GiB、512 PID、3600 秒任务时限，可按固定镜像派生修改。容器不挂载 Docker socket。
6. 采用[宿主 systemd 样例](deploy/alertops.service)运行，反向代理提供 HTTPS；`ALERTOPS_RUNNER_CALLBACK_URL` 必须可从容器访问。生产监听、TLS、DNS、企业权限和群安装条件由实际部署环境提供。

不要在项目有 STARTING/RUNNING/FINALIZING 任务时修改工作区与仓库。目录脏或执行状态不明时保留现场；只检查/修复对应槽位，不能通过删除数据库占用记录制造第二个写入者。

## 文档与代码入口

- [已确认设计](docs/design/platform/README.md)、[23 表数据库模型](docs/design/platform/database.md)、[DDL](backend/internal/platform/schema/001_initial.sql)
- [AI 执行流程](docs/design/platform/ai-runtime.md)、[执行器](runner/run.py)、[受限 Git/MR 业务命令](runner/alertops_action.py)
- [API 与运行说明](docs/implementation/operations.md)、[验收记录和实现假设](docs/implementation/issue-1.md)
- [领域术语](CONTEXT.md)、[原卡片设计](docs/design/dingtalk-log-alert/README.md)

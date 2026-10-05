# 钉钉真实接入验收记录

日期：2026-10-05。目标为现有企业测试应用和「告警平台卡片交互测试」群；只使用模拟告警数据，不操作生产告警群。以下分别记录平台配置完成与真实业务链路通过，不能互相替代。

## 已完成的官方平台配置

- 测试应用：告警卡片本地联调；AgentId `5036643162`。应用版本 `1.0.2` 已上线，官方页面发布时间为 `2026-10-05 14:06:55`。
- 应用可见范围保持「部分员工：刘强」。通讯录接口权限也已明确保存为部分员工，只选择刘强，没有选择部门或全体员工。
- 用户明确批准开通 `Contact.User.Read`、`qyapi_get_member`、`qyapi_chat_manage`。`Card.Instance.Write` 原先已开通。
- OAuth 回调配置为 `http://127.0.0.1:18081/auth/callback`。这是当前 Mac 本机联调地址，不能作为其他电脑或手机的访问地址。
- 酷应用「告警平台联调助手」已发布，编码 `COOLAPP-1-103BD9D4A58C0B0E40430002`。发布到企业自建酷应用列表获得单独授权；实际安装仍只允许指定测试群。群快捷入口暂指向本机 `http://127.0.0.1:18081/`。

## 已发布运行模板

原有设计稿保留。下面四个新模板都关联专用测试应用，经过官方搭建器文件导入、编译预览与发布，页面均返回「模板发布成功」。

| 类型 | 模板 ID | 导入类型 |
| --- | --- | --- |
| LOG | `7b9d69e9-66a5-4209-ac50-b6c00aa58cd2.schema` | `im` |
| METRIC | `8e4e37ed-2087-4745-a3ed-8383d085be31.schema` | `im` |
| NOTICE | `880fadbb-8578-4687-8f2b-0ec9cfae3102.schema` | `im` |
| TOPBOX | `d08d5e27-6a7c-43b4-a4f3-9b8f71ffa79f.schema` | `onebox` |

模板发布仅证明线上模板可编译使用；尚未证明平台 Hook → 群投递 → Stream 操作 → 数据库与原卡更新的完整链路。

## 联调发现并修复

1. 群成员适配器改为官方 SDK `BatchQueryGroupMember` 使用的 `/v1.0/im/sceneGroups/members/batchQuery`，请求分页字段为 `maxResults`、`nextToken`，响应续页字段为 `nextToken`。保留 `success` 和 `hasMore` 的核对。旧 `/members/query` 不是批量成员 ID 分页接口。
2. 吊顶模板导出类型改为 `onebox`。首次使用 `im` 导入真正的吊顶模板被搭建器拒绝，修正后导入、预览和发布成功。

验证命令：

```bash
cd backend
go test ./internal/platform -run 'TestDingTalk' -count=1
GOCACHE=/private/tmp/alertops-go-cache \
  ALERTOPS_TEST_DSN='<当前任务独占 MySQL 测试库>' \
  go test -tags=integration ./internal/platform \
  -run '^TestCardIdentityAndIndependentNotes$' -count=1 -timeout=2m
```

上述两组测试通过；后者使用真实 MySQL 和本地 HTTP 替身验证连续备注、幂等、退群与停用，不代表访问了真实企业成员接口。

## 尚待完成

- 测试应用现有 Client Secret 注入本机服务：官方页面显示复制成功，但当前浏览器自动化剪贴板没有取得内容，尚未保存密钥。没有重置或重新生成应用密钥。
- 在指定群安装酷应用、取得真实 `openConversationId`、验证群成员查询。
- 真实 OAuth 登录、平台自动创建 USER、管理员引导和 H5 登录保护。
- 通过真实 Hook 发送 LOG、METRIC、NOTICE，验证 Stream 备注、日志人工完成、指标自动恢复和吊顶归零关闭。
- PC 钉钉侧栏与真手机半浮层分别验收。手机需要可达的测试服务域名。

当前 macOS 原生窗口点击返回 `noWindowsAvailable` / ScreenCaptureKit `-3811`，导致钉钉安装与点击验收未执行；网页开发平台仍可操作。需要恢复可捕获的已解锁桌面后继续，不通过脚本替代或伪造原生客户端验收。

本机接入参数位于 Git 忽略的 `.local/`，文件权限为 `0600`。密钥、访问 token、OAuth 授权码不记录在此文档中。

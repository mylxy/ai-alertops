# 日志告警卡片设计

本目录保存已确认的日志告警卡片流程、钉钉设计器备份和审核依据。交互设计已覆盖首次告警、持续聚合、AI 修复、多仓库 MR、人工处理、人工关闭及关闭后复发；当前没有生产业务实现。

先阅读[日志告警卡片设计说明](日志告警卡片设计说明.md)。需要逐项对照钉钉预览时，阅读自动生成的[卡片流程对照](卡片流程对照.md)。

## 钉钉设计稿

- 名称：AI AlertOps · 日志告警状态卡（设计稿）
- 模板 ID：`03a9f40b-e81b-4efc-a12d-6a59a55a9a32.schema`
- [打开官方设计器](https://editor.dingtalk.com/editor?templateId=03a9f40b-e81b-4efc-a12d-6a59a55a9a32.schema&scene=open_platform&bizId=1791101120958&customBuilder=false&corpId=ding6d039ea9a87c3a7235c2f4657eb6378f&appId=5036643162)
- 模板未发布，未向群发送测试消息。

打开后选择“预览模式”。底部下拉框包含 19 个模拟场景；编辑模式会同时显示条件隐藏的节点，不能用它判断实际按钮显隐。

预览可真实操作场景切换、点击 ID 复制、备注输入区展开、取消收起和本地输入。备注提交、确认关闭、处理记录和 MR 入口目前展示设计说明；真实提交、关闭表单、记录页和 Codeup 跳转尚未接入。关闭后的页面使用场景切换演示，不代表预览已执行真实关闭。

## 文件与复现

| 文件 | 用途 |
| --- | --- |
| `日志告警卡片设计说明.md` | 已确认的业务规则、流程图、接入约束和验收条件 |
| `card-flow.json` | 状态、19 个场景、备注及聚合规则的源数据 |
| `build_design.py` | 使用 Python 标准库生成设计器导入包和场景说明 |
| `log-alert-card-design.json` | 官方设计器导入备份，不能直接当作生产发送参数 |
| `design-preview-data.json` | 模拟变量、场景和模板标识 |
| `卡片流程对照.md` | 与场景源数据同步生成的预览对照说明 |
| `verify_design.py` | 核对设计文件、状态约束和生成结果一致性 |
| `preview-verification.json` | 实际浏览器验证范围和未验证边界 |
| 三个 `reference.json` 文件 | 生成器使用的官方组件样例备份 |

在项目根目录执行：

```bash
python3 docs/design/dingtalk-log-alert/build_design.py
python3 docs/design/dingtalk-log-alert/verify_design.py
```

只修改源数据和生成器，再重新生成；不要单独修改生成的 JSON 或场景说明。组件结构参考[钉钉官方卡片样例](https://github.com/open-dingtalk/dingtalk-card-examples)，本地保留原始引用以便复现，不需要生成时下载依赖。

**这是一份设计器模拟模板。** `preview_mode=false` 仅隐藏演示入口，并不会把所有场景表达式自动转换成真实业务绑定。正式接入必须替换模拟内容、时间、身份、MR URL 和 `view_*` 表达式，并重新验证实际钉钉客户端。

## 审核结论

本轮设计审核确认三个主状态和两条收尾路径一致：AI 交付后人工上线验证关闭，以及 AI 未完成交付后人工处理关闭。两条路径都不以 MR 合并作为人工关闭门槛。

审核修正了首次告警误标 AI 运行的提示，明确了 AI 启动失败转人工、人工先关闭后收到 AI 结果、旧事件与新轮次隔离，以及关闭后新发生告警创建新卡并执行一次 AI。最终浏览器验证结果及限制见 `preview-verification.json`。

指标卡的一次备注、自动恢复规则不适用于本日志卡，详见[指标告警卡片设计](../../../design/metric-alert-card/README.md)。

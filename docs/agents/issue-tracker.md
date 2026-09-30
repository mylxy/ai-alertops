# 事项跟踪：GitHub

本仓库的需求、规格和任务存放于 GitHub Issues，使用 `gh` CLI 操作。
当前仓库为 `mylxy/ai-alertops`。

在仓库目录内运行命令，由 `gh` 根据 Git remote 确定目标仓库；
在其他目录运行时，显式传入 `--repo mylxy/ai-alertops`。

## 常用操作

- 创建事项：`gh issue create --title "标题" --body-file <正文文件>`
- 读取事项及评论：`gh issue view <编号> --comments`
- 获取结构化信息：
  `gh issue view <编号> --json number,title,body,labels,comments`
- 列出待处理事项：
  `gh issue list --state open --json number,title,body,labels`
- 更新正文：`gh issue edit <编号> --body-file <正文文件>`
- 添加评论：`gh issue comment <编号> --body-file <评论文件>`
- 添加标签：`gh issue edit <编号> --add-label "<标签>"`
- 移除标签：`gh issue edit <编号> --remove-label "<标签>"`
- 关闭事项：`gh issue close <编号>`

多行正文和评论先写入临时文件，再通过 `--body-file` 传入。
根据任务需要使用 `--label`、`--state` 等筛选参数。
分诊标签名称以 `docs/agents/triage-labels.md` 为准。

技能要求“发布到事项跟踪系统”时，创建 GitHub Issue；
要求“获取相关事项”时，读取对应 Issue 的正文、标签和评论。

## PR 作为需求入口

**PRs as a request surface: no.**

若后续将此标记改为 `yes`，外部 PR 使用与 Issue 相同的分诊角色。
读取时使用 `gh pr view <编号> --comments` 和 `gh pr diff <编号>`；
评论、标签和关闭操作使用对应的 `gh pr` 命令。
外部 PR 范围为作者关联类型 `CONTRIBUTOR`、
`FIRST_TIME_CONTRIBUTOR` 或 `NONE`。

GitHub 的 Issue 与 PR 共用编号空间。
编号类型不明确时，先用 `gh pr view <编号>` 判断；
确认不是 PR 后再按 Issue 读取。

## Wayfinder 工作约定

- 总览使用带 `wayfinder:map` 标签的 Issue，记录笔记、已有决策和待澄清问题。
- 子任务优先通过 GitHub sub-issues 关联到总览。
  不支持时，在总览正文维护任务清单，并在子任务正文顶部标注 `Part of #<总览编号>`。
- 子任务类型标签为 `wayfinder:research`、`wayfinder:prototype`、
  `wayfinder:grilling` 或 `wayfinder:task`。
- 阻塞关系优先使用 GitHub 原生 Issue dependencies。
  不支持时，在子任务正文顶部维护 `Blocked by: #<编号>, #<编号>`。
  所有阻塞事项关闭后，子任务才可开始。
- 选择下一项工作时，按总览顺序选取未关闭、无未关闭阻塞事项、
  且尚未分配负责人的第一个子任务。
- 认领任务：`gh issue edit <编号> --add-assignee @me`。
- 完成后，将结论写入评论并关闭子任务，
  再向总览中的已有决策追加摘要及链接。

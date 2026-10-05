# 钉钉部署模板

`log.json`、`metric.json`、`notice.json`、`topbox.json` 是卡片搭建器导入格式。使用 `python3 cards/build_templates.py` 从仓库保存的官方组件样例重新生成。原 `design/` 与 `docs/design/dingtalk-log-alert/` 的交互原型仍用于对照，不作为生产模板直接发送。

生成物沿用红/橙/绿状态、上下文与时间、日志/指标证据、最近三条人工备注、结果和详情入口。LOG 增加 AI 进度与动态 Markdown MR 列表；超过八条时卡片提示进入 H5 查看完整列表，数据库与 H5 不限制为两条。NOTICE 没有处理入口；TOPBOX 只显示两类未完成统计。

卡片参数由 `backend/internal/platform/card_view.go` 与 `CardParams` 生成。原卡的 Input 仅保存本地草稿，明确提交才发送 `action=note|handle`、`content`、`operation_key`。Stream 回调验证企业、应用身份映射、原群和当前成员；成功响应给该用户返回新的私有操作键，同一次传输重试返回同一个后继键。因此连续提交不依赖群卡异步更新完成。后台业务状态决定终态按钮不可用，服务端再次复核。

H5 链接使用 `open_platform_link`，PC `pc_slide=true`，移动端 `im_open_hybrid_panel / percent83`。原始 HTTPS H5 也可在浏览器登录访问。

部署时在专用企业应用下分别导入、预览、保存并发布四个模板，将发布 ID 填入 `DINGTALK_TEMPLATE_*`。卡片搭建器需要编译导入后的 `editorData`；生成文件中的 `widgetInfo` 留空，不能当成已在线编译的 XML。真实搭建器编译、客户端回调及真吊顶安装条件仍须在目标应用验收，生成 JSON 与本地替身测试不能证明这些步骤通过。

未知写入结果采用保守协调：普通明确失败重试原 ID；网络超时、异常响应或发出后进程崩溃，单个目标暂停后续写入并显示待核对。当前钉钉 API 适配器未获得可验证的查询/条件版本协议，不会假称已协调，也不会换 ID 重发未知实例。须在生产接入验收中确定提供方完成证据；其他目标和已保存业务继续运行。

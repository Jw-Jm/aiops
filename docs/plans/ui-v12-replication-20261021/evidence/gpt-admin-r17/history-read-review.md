# P2-04 历史归属读取：r17静态复核

核对当前4b207ae及保留的r14～r16工作区，不继承历史“映射已完成＝历史读取已完成”的结论。本文件只记录调用链发现，不是新实施方案或运行通过证据。

- assistant.loadConversation以owner_subject精确匹配；ListPage和QueryResult各自还有主体过滤。仅改其中一个WHERE不能贯通会话、消息、引用、分页及流事件。
- assistant.authorizeConversation要求当前grant摘要与冻结GrantSnapshot完全一致。显式映射不改旧主体、不复制旧角色；原生与legacy授权摘要不同，即便资源范围相同也不会自动通过。
- investigation.AssistantReadJobTx、currentAssistant及Repository.current继续检查原Job主体、冻结输入/上下文/权限摘要和当前策略。消息、结果及来源引用复用它们。运行中的Worker、恢复与取消也使用相关校验，不能将只读兼容直接塞入共享执行校验。
- 原生审计Job已有显式authority source/user/version；assistant.Submit目前只在audit域写入这些字段，operational域仍是P2-05缺口。普通原生operational Job在无浏览器上下文的Worker中仍会走旧角色查询，通常拒绝；这是源码推断，未在本轮调用模型/Worker验证。
- action.Get/输出/后置报告复用effectiveRead，以当前operator集群和资源范围授权。它属于资源域历史读取，不能未经合同核对改成“只允许同subject”。本轮未宣称该链运行通过。

保留现有拒绝边界。本轮没有放开旧主体匹配、覆盖冻结摘要、把映射当作角色或执行证明，也没有编写无人调用的兼容层。助手/调查完整历史读取仍未实现，需与Job来源及签名链联动验证；最后管理员并发和审计故障可独立补验，见本轮边界测试。

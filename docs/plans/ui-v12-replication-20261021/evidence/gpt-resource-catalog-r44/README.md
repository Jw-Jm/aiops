# r44 资源搜索、共享健康投影与目录速览

GPT直接执行，无CodeBuddy/子代理、提交或推送。30471 API/Worker/Web前滚r44；schema仍71，无迁移。旧r39c二进制与r43静态容器保留；调查器、冻结模型Profile及独立30472未改。

## 完成的链

C002新增可选q：对已授权资源的名称、Kind或稳定UID做不区分大小写的子串搜索，最多256个Unicode字符。保留原name精确匹配；q与领域/健康/类型/命名空间/标签/canonicalIds取交集，匹配汇总、排序和分页使用同一集合，QueryDigest把q绑定游标。公开OpenAPI、签名内部GraphQuery/schema、Go生成物与正式TS客户端已同步；不另建搜索服务，不提供语义/全局搜索，不先截本页再搜索。

发现并修复共享缺陷：旧后端entityHealth用于健康筛选/统计，页面读取attributes.health却通常缺字段，真实正常Pod显示未知。现在projectEntity克隆顶层属性后统一补health/resourceDomain（复用原分类函数），保留硬件来源降级未知；不改原不可变图属性、canonical domain、数据库历史或已提交结果。目录、详情与邻居图沿同一投影读取。

资源目录补原型要求的UID、类型/领域、命名空间、来源/最近观测、速览/详情列，默认10条并支持5/10/20/50，搜索、筛选与条数保存在URL。速览显式重新GET C003，按完整规范ID取资源，拒绝时隐藏旧内容和链接；详情/拓扑导航携带clusterUid+canonicalId。集群/筛选变化卸载旧抽屉，迟到读不能回填。原来更多精确筛选仍保留，未为做视觉而删功能。

## 验证（分开判定）

- graph/httpapi/contract包通过（graph原整组266s，之后未重复整组）；新增搜索回归验证中文/大小写、Kind/UID、授权过滤与汇总、游标摘要、256/257字符边界、列表/详情健康一致。最终resourceDomain增量执行合同/域/硬件定向回归通过。
- 28项定向Vitest（5文件）通过；最终分类显示后再跑4项目录用例通过；TypeScript/Vite正式构建通过。覆盖q与原筛选交集、游标清除、匹配总数与本页数分离、每页条数、速览复读与403隐藏/迟到范围。首轮测试在URL切换的Skeleton期间找分页选择器失败，改为等真实控件恢复，保留失败日志。
- 真实远程Chrome/operator A：大写真实Pod UID搜索精确1行、meta.summary.total=1；health=normal、resourceDomain=kubernetes；正式UI搜索回显、本页/总数、速览C003、规范ID与集群链接、条数20及刷新URL恢复通过。未授权真实B namespace搜索结果与summary.total均0；257中文字符返回400。拓扑列表同Pod当前显示正常且API投影一致。0业务写入、0前端异常；模型任务仍[data_query]，无模型调用。
- 独立Kubernetes/PG：实际Pod UID/Running/Ready=True与API健康一致；READ ONLY资源行未删除、canonical/namespace/kind/name一致；schema71；原r41/r42日志摘要仍14f0d1e…，未重发查询。当前API/Worker的PodUID、挂载二进制路径与本地/远程SHA完全一致；独立30472仍运行。源码与制品在source.json和两份runtime receipt中绑定。
- 没有截图/Trace、完整像素比较、自然TTL等待、长时间高负载或全栈冷重启；真实两集群/连续来源与其他Kind健康验收仍不通过。本轮首次恢复读200不能宣称消除启动503/来源连续性缺口。

## 运行、失败与接续

当前：ops-v12-native-r44-web-30471 / web-r44，index SHA256 2f0fdde5a1c90583d6f463fce8597fbe21406cc2f85eb06a2c2cfeac90e54957；API398bea8254fe…、Worker9773cd1bd280…，完整哈希见backend-runtime.json。先Worker后API，保证旧请求不携带q时仍兼容；q为空时omitempty不改变原请求摘要。模型Profile、策略、来源/资格、Secret及数据库版本均不改。只允许按本任务材料前滚；不能恢复旧身份/整库或盲目把带q的新API配到旧Worker。

静态部署首次错误把previous容器写成新r44名，在inspect阶段失败、未停止r43；只修正Web部署脚本并接续，没有重复后端替换。另两次浏览器拓扑健康检查误用getByText(exact正常)：实际单元格是“●正常”，原API与行文本均正常，修为同时核对可见行与真实API字段；失败JSON保留，未放松业务断言。生成器必须在platform/api目录执行，避免相对output写错目录；一次误生成的临时文件已清理并正确重生成。

P3-01保持in_progress，SL008仅追加实际链与证据，六维不提升。完整目录/详情/总览视觉、选择/对比/导出及责任归属、预置与持续日志来源、两真实集群、完整重启、r42真实AI创建执行及P4～P8继续待办。原日志快照资格截至2026-10-29 10:45(+08)，未延长；虚拟化/完整Trace/生产发行继续按边界延期。

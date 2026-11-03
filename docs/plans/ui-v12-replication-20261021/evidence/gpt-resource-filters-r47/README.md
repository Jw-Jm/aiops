# r47：资源目录候选与重置

## 实现与合同

SL008/P3-01，沿用Ponytail full、既有原生授权和图读取路径。本轮补原型的类型/命名空间候选与重置，不创建管理API、依赖、迁移或业务写入；不恢复虚拟化、Trace或生产发行。

C002 GET resources新增可选`includeFilterOptions`：Worker对同一不可变图快照先经ontologyReader.GetNode/Scope.Allows完成当前租户、集群、namespace、集群级及精确资源范围过滤，再生成去重排序的`meta.summary.filterOptions{kinds,namespaces,clusterScoped}`。候选来自整个已授权投影，在业务条件/分页前生成；当前匹配total/byKind/byHealth、排序与分页语义不变。未授权namespace、集群资源和精确范围外资源均不会成为候选。来源有部分覆盖时仍保留原覆盖警告，不把当前投影的候选当全量真实世界库存。

flag默认关闭且JSON omitempty，因此旧签名请求/QueryDigest不增加false字段；开启后flag进入签名/游标查询摘要，防止不匹配游标复用。内部OpenAPI/JSON Schema、外部OpenAPI/resource-graph schema、Go和Web生成物同步。保留原namespace精确匹配，增加保留值`__cluster__`（不是合法Kubernetes namespace），仅匹配已授权空namespace，不能扩大ClusterScoped权限。

ResourcesPage复用同一个列表读取，不额外遍历API分页或从当前页猜候选。支持类型/namespace下拉搜索，选择保留其他URL条件并清游标；无匹配结果仍保留完整候选，当前URL指定的无匹配条件不自动回退。读取失败禁用候选，并通过key重新创建Select清除Ant Design缓存的旧popup。重置清q/domain/kind/namespace/name/health/label/cursor/sort/order，保留clusterUid和limit；组件重建仍清理旧选择、抽屉、对比及导出。现有即时筛选方式保留，整页原型布局及显式筛选提交方式未因此验收通过。

## 验证与失败保留

- 三文件25项定向Vitest、TypeScript/Vite构建通过（已有大chunk提示仍在）。在增加namespace选择保留条件/清游标和精确资源授权候选断言后，分别再次运行目录11项与图专项，均通过；不是把重跑合计为36个不同用例。
- 图包排除既有`TestUpstreamConformanceAnd20KPlatformGeneration`后通过，HTTP和全部合同测试通过。首次广跑在20k构图持续执行，终止保留堆栈；有界90秒整包复跑仍在同一容量构图超时。没有宣称整包或容量通过，没有删除该测试、修改容量实现或添加豁免。最终专项包含新schema验证、flag摘要差异、旧响应省略、空筛选完整候选、namespace/ClusterScoped正反例和精确资源范围收窄。
- 两次UI定位失败来自Ant Design中文双字空格与loading图标；定位修正后，撤权用例真实发现缓存候选仍在DOM，产品代码已修复，最终用例要求候选确实消失。失败日志保留。

真实Chrome/operator A、1440×960、zh-CN/Asia-Shanghai：完整授权库存38条，6种类型（ConfigMap/EndpointSlice/Event/Pod/Service/StatefulSet）、1个namespace。limit1+不存在q+未授权namespace组合为实际0结果，候选仍与完整库存一致，没有其他namespace或集群级候选；legacy不返回新字段、非法flag400、无集群级授权的保留值读取为空。页面真实下拉/重置/刷新恢复limit20/选Pod后服务端实查通过；0业务写入、0页面异常。

首轮浏览器用通用dropdown类定位，离场动画期间混入前一个下拉，导致断言失败；按对应Select的list控件限定定位后通过。`browser-locator-failed.json`保留原始失败，未修改产品或放宽预期集合。

独立READ ONLY PG核对全部38条canonical/name/kind/namespace及未删除状态；Kubernetes当前Pod/Service/StatefulSet的UID/name/namespace逐项吻合。实际二进制hash/PodUID、schema71、旧日志不可变摘要核对通过，模型仍仅data_query、SP05配置未开启，30472仍运行。PG不能替代被门禁阻止的事件API，没有发新日志查询或模型请求。

## 运行与恢复

本地只编辑/单元/合同/构建，运行仅`ssh mssc-direct`访问192.168.0.63任务环境。30471 Worker先于API前滚r47，随后静态Web r47，schema71无迁移；原r44二进制和r46d静态Web保留，来源和模型配置、调查器、30472未改。制品/源码在source.json，实际运行在backend-runtime.json/web-runtime.json，浏览器和独立oracle分别落盘。此前保存的r46/r44证据是历史状态，不覆盖其实际版本；当前版本以r47为准。

没有恢复旧身份、整库或旧凭据；失败时只针对本任务组件前滚或使用原生兼容制品，不把旧库回滚当静态Web恢复。部署脚本为本轮一次性执行记录，不应直接重跑创建同名目录/容器。

未测试第二个实际授权namespace/两个真实集群、集群级正例实机、全部资源Kind、完整视觉、持续日志来源、全栈重启、模型资源排查、SP05非空事件与长期容量；相关正例只在明确fixture范围验证。未截新图/重采参考，六维及P3状态不提升，V1.2未完成。没有提交/推送。

# r39 历史分页、完整请求与过期合同：局部通过，P3未整体关闭

GPT直接执行；延续main脏工作区，HEAD 082f87d；不提交/推送、不重置其他修改、不使用CodeBuddy。根00～10/虚拟化/Trace/共享存储配置未改，无新依赖或模型调用，无长期高负载验证。

## 运行与查看

[30471日志查询](https://192.168.0.63:30471/observe/logs)，沿用此前交付的v12.fullview或任务operator。当前Web `ops-v12-native-r39b-web-30471`，index SHA256 `b1d6e8872514304dfb19a14f36fa6cff6cd7565b85da098f0cd89336b03d6d24`；API/Worker为r39c，SHA与PodUID见backend-runtime-r39c.json。任务库ui_v12_r24_native仍PG17.11/schema71，本轮无迁移。30472未改。

部署仅替换任务两个Pod的二进制hostPath和30471 Web，保留配置、身份、数据库、来源和旧artifact/容器。先Worker后API，旧API向新版Worker的未分页list模式仍兼容。新API必须配新版Worker，不盲目降级Worker（旧版还可能忽略UID资格）；优先修正并前滚。Web可回到同后端兼容的旧构建，但旧Web不会提供新分页交互。所有日志/镜像/失败文件均保留。

任务日志来源仍为r37单Pod固定UID/固定30分钟窗口真实快照，资格/证书期限不因本轮更新延长；不是持续日志管道或全覆盖。Kubernetes原自动投射凭据不由该快照期限替代。

## 实现与合同

- C025历史响应保留`data: queryView[]`及requestId，新增`nextCursor: uuid|null`；cursor是当前tenant/owner/cluster内的不可变查询行UUID，limit默认20、范围1～50。按created_at/query_id倒序keyset翻页，不用offset。每次扫描limit+1条；权限/来源无效条目省略且游标按实际扫描继续，可能出现空页但还有nextCursor。前端逐页追加并禁用结束后的“加载更多”，打开历史不重复调用refetch。
- 游标不是授权，不能用其他owner/cluster的查询ID继续读取。页中每条都走原生会话/独立operator授权/来源登记事务复检、资格核对及最终复检；不继承旧角色、不放宽来源门禁。
- C026 queryView新增可选request，由数据库不可变request列派生；旧view JSON未含request也补齐。新创建及幂等重放均返回原保存请求。检查页和复制使用保存的模板、资源/Namespace UID、来源/凭据/资格绑定、绝对半开窗口、filters与budget，兼容旧服务的元数据子集回显，不拿当前capabilities预算伪造历史。
- 历史详情按级别再查只替换filters，其余保存请求保留，包括原始微秒时间字符串与预算；不会通过秒级表单回填悄悄收窄窗口或把7改成20。
- 过期读在当前授权/来源登记事务复检后投影为expired，清除响应的result/resultRef及取消终止元数据，给出RESULT_EXPIRED；只读投影不改写原不可变结果。C168结果和C027取消返回410 RESULT_EXPIRED，不返回结果内容。前端显示明确过期态，关闭旧结果/抽屉且不读取过期结果。
- Worker领取过期任务只转expired，不调来源；执行停止检查包含expires_at；Finish保留租约与事务授权复检，过期不得发布结果。三处共用过期清理逻辑，HTTP中止仍不宣称来源端已停止。

## 分层验证

1. ui-passed.log：9项Vitest（此前迟到、取消版本/409、样例总体分离、详情与排序、拒绝后缓存隐藏；新增历史追加分页、保存预算/微秒窗口再查、过期缓存隐藏）。web-build-passed.log：正式TypeScript/Vite通过，保留既有大chunk提示。
2. go-unit.log、worker-unit-passed.log、contract-passed.log、generate.log：定向Go/原生准入/HTTP与Schema/OpenAPI合同、生成通过。公开分页参数遵循仓库limit约定，未放宽校验以接受pageSize。
3. lifecycle-r2.log：远程任务Postgres新建独立数据库，迁移1→71，验证旧View完整请求、游标连续性/过期排除/跨集群与未持有游标拒绝、授权先于过期披露、expired不发元数据I/O；原幂等、不可变结果、取消/迟到、来源停用、中断、撤权、0 AI Jobs继续通过。
4. lifecycle-expiry-worker.log：再次独立库验证新增过期accepted不调来源、过期running停止检查、Finish拒绝结果发布。数据库因audit.records按已有保护规则保留，未强删。
5. browser-history.json、browser-history-level.json：远程真实Chrome1440×960/原生cookie/真实API及来源。当前capability最多20，实际提交budget7，测得总体186、返回7。游标两页无重复，随机未持有cursor及limit0/51为400；正式历史弹窗“加载更多”、恢复、完整请求复制通过；详情级别再查保持微秒窗口/来源/预算7。persistence.json确认仅level不同，保存摘要与界面一致，未以样例数推总体。
6. expiry-fixture.json + browser-expiry*.json：**任务库中单独插入的元数据过期fixture，没有业务结果，没有来源执行；未修改现有记录的期限或结果。** 验证真实HTTP200 expired/410 results/410 cancel。它不是等待真实查询30天自然到期的live证据；有内容的过期结果隐藏由独立PG受控集成验证。
7. browser-final-read.json：最终r39c上历史列表200、原真实186/7结果200、元数据过期200/410继续成立。未再次运行大批来源查询。

## 失败与限制

- lifecycle.log：首个macOS tar含AppleDouble `._*.sql`，Goose拒绝解析；原目录保留。新source-r2只用真实.sql打包后通过，未改迁移、未删失败文件。
- contract-limit-name-failed.log：pageSize违反已有OpenAPI约定，改为limit后通过。
- web-fixture-type-failed.log：新分页fixture缺少时间/字面量类型，修正fixture后构建通过；未放宽业务类型。
- 远程6443首次拒绝连接，后续只读检查200，未操作系统服务；此间独立Docker PostgreSQL正常。未归因或承诺不再发生。
- browser-expiry-r39c-startup-failed.json：r39c启动后首轮读取未获预期状态；API日志同时记录503（request IDs 01a18256-18c5-746c-bd6a-62a98a22d279、01a18256-18bd-7291-9d04-2a908f22838b），Worker有lease_cas/route_mirror_write慢阶段。随后browser-expiry.json与最终读回通过。失败保留，**未证明永久消除间歇性503或完成冷启动全状态验收**，不自动重试写请求。
- 未重采736张参考，当前完整视觉仍未通过（此前严格截图探针拒绝未绕过）。自然30天业务到期、两真实集群、时间桶/字段侧面、AI精确范围与预置监控、资源聚合/拓扑及完整发行仍未完成；P3-02/03/04保持in_progress、六维不提升。

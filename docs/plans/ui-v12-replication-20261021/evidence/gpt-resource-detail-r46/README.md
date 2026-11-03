# r46 资源详情、当前集群身份核对与来源追溯

GPT直接执行，无CodeBuddy/子代理、提交或推送。30471最终Web为ops-v12-native-r46d-web-30471/web-r46d，index SHA256 `3ffed369a43a00562c57e4a6e8af42c527ac9c29398ee9ae9afbf1d6d98ab1b2`。r46/r46b/r46c及旧r45静态容器停止保留；API/Worker仍r44，PG15454/schema71，无迁移；调查器/模型Profile、30472及来源登记/资格未改。

## 完成的路径

旧资源详情只有原始字段与关系图，查询键未含当前集群，入口只凭canonical调用自带作用域的C003。现先用C002当前clusterUid+完整canonicalIds精确核对，再读取C003并查找同一canonical节点；不取nodes[0]、不按名称或首个资源回退。缓存/组件绑定scopeKey+canonical，权限失败/缺失/刷新等待/切换集群的迟到结果不回显旧身份或AI链接。总览及旧GraphView的资源详情链接补当前/节点所属集群，AI入口使用服务端scope字段，删除详情里的手工字符串分割。

详情按固定SL028/192/193/194提供四个页签：

- 资源概况：身份/UID/namespace/集群/观测、实际健康/标签及安全状态投影。C003 observationSources直接展示来源登记版本与覆盖信息，不调用管理员来源接口，不把登记版本等同资格或健康。来源版本与PG active revision独立核对。未知版本明确未提供。
- 关系与影响：复用r43的TopologyResult，移到features供两个页面共用；C004限定深度1/2、200节点/400边，图列表切换不发新读取，邻居抽屉重新C003核对。旧tab=graph仍可进入原高级关系查询，tab=detail回到概况；旧高级功能未删除。该主视图为原型邻居关联，不宣称因果，也没有把C004冒充C005影响计算。
- 指标与证据：按现有NAV开放状态呈现入口；当前仅Pod日志入口可用，携带clusterUid+canonicalId。LogsPage读取该参数并在同集群参数变化时重置工作区，不自动选其他对象或提交查询。无资格来源/能力错误/范围不匹配时禁止新查询；失败复查会撤掉“资格已核对”旧徽标。历史结果与新查询资格分开。
- 关联事件：C029/C031按精确canonical+cluster分别读取，独立UUID/opaque cursor参数、每页20，失败/未启用与空结果分开；读取未成功时不显示“本页0条”。两类记录不混算，也不通过资源关系补别的对象记录。

只增加前端接入，没有新增API/依赖/Agent/模型或业务写入。C003/C004/C031清单参数按当前OpenAPI/生成合同纠正（原基准保留）；实际链见implementation-bindings.json。

## 分层验证

62项定向Vitest（7文件）及最终TypeScript/Vite构建通过。新增用例涵盖当前集群前置核对、非首节点定位、403/缺失无回退、旧身份隐藏、关系懒加载与新读抽屉、精确日志深链无POST、事件独立过滤/分页、接口失败和SP05禁用不算0、跨集群迟到丢弃及旧tab兼容。日志已有迟到回执用例保留原“不读旧任务/不显示旧总体”断言；资源改变清空来源/窗口后，按钮新语义为禁用，需重新选来源才可查询。新断言不是放宽旧业务结果。

真实Chrome/operator A（1440×960、zh-CN/Asia-Shanghai）：

- C002/C003均200，同Pod精确UID、来源登记版本3、复制标识、AI/详情/拓扑范围链接正确；概况不自动读取事件或来源查询。
- C004关系图实际渲染，邻居抽屉C003新读、图/列表局部切换不重新查询；来源与图元数据仍来自真实服务。
- Pod日志深链实际GET capabilities200/sources=[]，显示“当前资源没有合格日志来源”、查询禁用；0新日志查询。原10:45资格没有续期，不以旧快照作为当前来源。
- 跨集群指定根真实403且没有后续C003；同集群缺失UID返回空根，没有C003或名称回退；无旧AI链接。
- 0业务写入、0页面异常。关联事件的实际结果为下面的阻塞，**不是事件真实空态/非空态验收通过**。

READ ONLY PG/Kubernetes/真实Pod配置：资源canonical/name/kind/namespace/UID一致；来源登记active/revision3与C003一致；schema71；原r41/r42日志摘要不变；API/Worker仍同一r44 PodUID。当前两个SP04配置实际指向r28-runtime:/etc/r28/sp04.json，均无SP05块/Enabled=false。DB中该资源finding/incident行数0只是独立库存观察，不能替代被门禁阻止的公开API读取。证据runtime-oracle.json绑定最终Web index SHA。

## 已定位的真实阻塞

GET findings与incidents均为HTTP409 `CAPABILITY_DISABLED: SP05 runtime is disabled`。界面保留“此功能未启用”和未取得读取结果，不输出伪造的0列表；本轮未开启SP05、未直查DB填前端、未造Finding/Incident。P4需要在真实运行配置及来源条件具备后继续，不把这个门禁扩散为资源/日志深链的阻断。SP06资源排查Profile限制继续保留，未发模型请求或扩大taskKinds。

## 视觉与安全

使用既有r3 SL028参考，没有重采736张。按原CSS取双栏20px间距、卡片头60px、内边距20px、标签键列120px；截图发现实际长Kubernetes标签键挤入值列，修正overflow-wrap，真实7个键均scrollWidth=clientWidth=120。规范标识等宽，scope_limited/来源连续性提示复用共享中文状态映射。

两张最终产品图在artifacts/test-reports/ui-v12-product-r46/detail-r46d-summary{,-full}.png。独立的资源详情捕获协议仅允许/resources/detail，只排除从本次授权C003 metadata确认的完整UUID；未知代码/因子URI/OTP/Base32及前后探针保留，7项协议自检通过。没有使用日志专用helper越界，也没有拍登录/MFA/Trace或遮罩整个区域。

visual-review.json明确保持visualPassed=false：真实身份/namespace/标签长度、原生权限壳、必需覆盖提示及来源追溯卡片导致整页与演示参考不同；没有以组件尺寸/截图存在替代逐状态像素验收。来源版本卡片、刷新和明确无资格状态是登记的真实接入扩展；没有编造OOM/重启状态摘要。

## 失败、恢复与未测

首轮TS联合Promise类型、ByRole冗余exact参数、AntD离场loading图标定位、mock未设返回、日志旧按钮语义、一次格式替换未命中的错误页脚断言均保留。首次浏览器等待两个事件接口时未处理pending Promise，未写整组回执；修复Promise.all/先落盘后关闭后，才定位真实SP05 409。browser-events-blocked-r46.json保留该初版实证，后续b/c/d前滚分别处理错误计数、来源失败旧徽标和实际长标签布局，没有改API或将错误改成功。

旧OIDC时代的browser transport fixture只补当前根解析/显式graphDepth=2，未运行该旧登录脚本，也未把静态编译当真实E2E。没有测试全部资源Kind、两个真实集群、SP05非空业务、完整视觉、自然保存结果TTL/全栈重启、模型回答质量或长期负载。SL028～042及相关状态只追加证据，不提升六维；P3-01继续in_progress，V1.2未完成。恢复只替换本任务静态Web；不恢复旧身份/整库，不自动启用SP05/模型、虚拟化/Trace或生产发行。

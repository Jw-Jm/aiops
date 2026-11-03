# r38 日志样例检查与详情：已部署，P3未整体验收

GPT直接执行；延续main脏工作区，未提交/推送、未切分支，既有修改保留。未使用CodeBuddy、新依赖、模型调用、长期负载测试或虚拟化。

## 当前运行与查看

仅30471 Web升级到 `ops-v12-native-r38-web-30471`，index SHA256 `571b3828447c9a009a014327947dab8c7bc601bc06b8639c5e32d923c0dcf13a`；旧r37j容器停止保留，失败可恢复同后端兼容的旧Web。API r37、Worker r37g、PG17.11/schema71与30472均未改。运行目录 `/data/ops-v12-native-visual-r24-20261026/r38`。源码/构建/运行身份见source.json、web-runtime.json。

[日志工作台](https://192.168.0.63:30471/observe/logs)沿用先前交付的v12.fullview或任务operator账号。选择ops-seaweedfs-0和r37-native-pod-log-snapshot来源。它仍是单Pod固定UID/固定窗口真实快照，不是长期日志管道；资格期限不因Web更新延长。

## 实现与作用范围

- 对照冻结SL-287/417/418与SL-292/419/420/421/456/457的SPEC/原型处理器，复用V12Drawer/Tabs/DataTable/Typography复制能力。
- 查询检查：概况、查询条件、结果快照三页签；资源/Namespace UID、来源revision、资格digest、半开窗口和query/result标识均来自服务端读取。复制查询条件是持久化View已公开的元数据子集，明确没有完整执行预算；不把当前表单或capabilities预算伪装成历史请求。服务端完整请求仍需后续公开合同补齐。
- 日志详情：字段、原始内容（脱敏快照，非来源未经脱敏原始日志）；上一条/下一条仅在当前可见样例中导航，保持当前页签、固定UID/来源/窗口/query。复制与当前条目一致，边界按钮禁用。
- 时间正/倒序、自动换行不重查来源，保持样例身份与来源总体；关键词Enter不重发。字段级别“按此筛选”则发起实际新查询；新请求与原持久化请求只差level过滤。
- 读取复检被拒绝时，隐藏缓存结果及已打开详情/检查抽屉；不继续展示旧授权数据。该分支本轮为单元机制验证，未重新操作现有账号撤权做live演练。

## 验证与保留失败

unit-passed.log：6项Vitest通过，包括原迟到响应、取消版本/409、总体与样例分离，以及新增排序/换行/详情相邻导航/检查快照和403后缓存隐藏。build-passed.log：TypeScript/Vite通过，保留既有大chunk提示。没有改变公共Layer组件。

browser-details.json：远程真实Chrome1440×960，以原生cookie登录，正常来源返回186总体/20样例。已验证排序换行不重查、字段/raw切换/下一条、两种实际剪贴板复制、查询检查三页签、持久化元数据与快照，以及字段ERROR过滤发起真实请求。persistence.json：数据库确认查询9da1a116…摘要与检查页一致；新查询a5699a01…与原请求resource/source/window/budget完全一致，仅多level=error，真实总体180/返回20，未用样例数推断总体。

失败保留：unit-*-failed.log为AntD测试环境固定ID导致命名定位冲突，以及Tabs旧class/hidden属性假设；测试改为实际title范围和aria-hidden，不改产品以迁就fixture。browser-details-time-format-failed.json为Z与.000Z字符串比较误报，改为比较实际时间点后通过，不改变请求。所有记录保留而非改写成功。

视觉：未重采736张参考。当前截图探针仍命中recovery-code，严格拒绝保存截图；browser-details.json记录JSON断言，不能当成视觉证据。现有表格式样例与原型列表/侧边字段面、时间窗输入与单选择器等差异尚未消除，完整视觉不通过。

## 未完成与接续

P3-01资源授权聚合/拓扑、日志时间桶/侧边字段面、AI精确范围衔接、预置视图、个人历史分页/明确过期、完整执行请求读取合同、两真实集群和Worker实际重启中断验证尚未完成。P3-02/03/04保持in_progress，六维不提升。继续补真实合同与切片，不用样例直方图伪造总体趋势，不以本轮局部UI完成宣称P3或V1.2通过。

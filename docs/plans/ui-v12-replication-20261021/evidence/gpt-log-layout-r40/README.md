# r40 日志时间桶、字段侧栏与列表：局部完成，P3未整体通过

GPT直接执行；main脏工作区，HEAD 082f87d。未提交/推送、未覆盖其他任务修改。沿用Ponytail和用户要求：复用已保存快照与现有控件，没有为本地分布新增后端合同；没有新增重复判空/try-catch或通用查询包装。结果分支已经排除busy/active，因此移除新侧栏上两处重复禁用条件，仅保留合同不支持UNKNOWN查询的禁用。

## 当前运行

仅30471 Web更新为 `ops-v12-native-r40b-web-30471`，index SHA256 `c477bb4b370039b250962a4104391dd4e09ca2d2db5671ea034e45e0b073875f`，见web-runtime-r40b.json/source.json。旧r39b/r40容器和包保留。API/Worker仍r39c，任务库仍PG17.11/schema71；30472、账号、角色、来源及存储配置未改。

[日志查询](https://192.168.0.63:30471/observe/logs)沿用此前交付的v12.fullview/任务operator。来源仍是r37 `ops-seaweedfs-0` 固定UID/固定窗口快照；本轮资格有效至2026-10-29 10:45:00 UTC+08（browser-layout.json），未自动延长，不是持续日志管道。资格过期后应拒绝新查询，后续真实持续采集另做接入和材料核实；Kubernetes原自动投射采集不受该快照期限替代。

## 实现

- 从冻结prototype.html的logHistogram/logsWorkspace与shared/platform.css复制局部结构/尺寸/色值。恢复时间柱、190px字段侧栏、整行可点击的日志列表和64px行高。
- 每5分钟统计**本次返回样例**，末尾区间截到真实window.to；点击时间桶/清除筛选只改可见样例，柱高和级别数不随关键词变化，也不冒充总体/错误率/完整覆盖。计算采用浏览器Date毫秒时间；保存的请求微秒字符串不改写。
- 级别侧栏显示返回样例分布，资源字段绑定当前快照的namespace/UID/来源。INFO等按钮使用现有submit和保存的完整request，仅替换filters；UNKNOWN由现有合同明确不支持查询。
- 排序、换行和相邻详情仍使用queryId+原始样例序号稳定标识；本地筛选清理已选详情，不新建来源查询。无匹配时显示“没有匹配的返回样例”，不改为来源no_data。

## 验证

- ui-final.log：10项Vitest，包括此前9项、跨午夜/5分钟半开边界/末尾不足5分钟、本地时间与关键词组合、清除筛选和分布口径。build-final.log：TypeScript/Vite正式构建通过，既有大chunk提示保留。
- browser-layout.json：远程真实Chrome1440×960、原生cookie、真实来源。总体186/返回20，6柱合计20，空桶不冒充来源无日志；本地筛选/排序/换行无额外create，详情身份不变。侧栏INFO真正新查，总体6/样例6；persistence.json确认原请求与INFO请求除level外完全一致，两个结果摘要/总体与浏览器一致。没有模型调用。
- capture-states-r40b.json：最新Web恢复既有查询6a17ed38…，0次新查询；捕获默认倒序/不换行、换行、关键词空态，对应SL050/291/056控制状态；default-full保留20条真实长页。最早logs-results图片是正序+换行复合状态，仅作交互证据，不混称SL050默认图。
- 实测facet=190px、日志行=64px、histogram=151.4375px，来自browser-layout.json及源CSS尺寸；不能据此宣称整图一致。

## 截图安全与比较

此前r36～r39的严格恢复码正则会匹配UUID里的四位-四位片段，因而拒绝截图；历史拒绝记录不改。新safe-log-capture.mjs**只在/observe/logs**从恢复码探针中排除已由授权观察元数据确认的完整UUID（tenant/cluster/resource/namespace/source/query/result）；不会把任意样例内容、Cookie/CSRF或因子值加入排除表。未知UUID、孤立恢复码、otpauth URI、OTP/Base32输入仍阻止，稳定后前后均检查，迟到秘密会删除本次图片。r30/r36原协议不改。

capture-tests-passed.log：5项纯机制用例验证已知UUID误判、未知UUID仍拒绝、旁边真实恢复码不豁免、因子URI/输入与因子页面仍拒绝、捕获后迟到秘密删除。capture-import-failed.log保留首次本地导入远程路径错误；修为本地自包含settle后通过，实际捕获前已修复，没有含秘密图片。

正式产品图片位于 `artifacts/test-reports/ui-v12-product-r40/`，最新看r40b-logs-default.png/default-full/wrap/keyword-empty；文件摘要及比较结论见visual-review.json。沿用原736张参考，没有重采或更改黄金图，没有新掩码。

**visual=differences_remaining**：已人工打开对照SL050原参考和正式产品；顶部AI范围询问/预置视图/导出入口未齐，双日期输入与单窗口选择器不同，结果区域整体位置/间距尚未对齐。角色导航及真实资源/来源/总体取代原型演示字段，另列适配，不以差异截图提升视觉通过。组件尺寸/截图安全、交互、API、数据库和完整视觉分别记录。

## 剩余

继续资源授权聚合/拓扑、AI精确范围衔接、预置监控/导出、窗口控件及整页视觉；持续合格日志来源和两个真实集群样板仍未完成。完整重启/自然到期、启动503及正式发行仍沿用r39未关闭项。P3-02/03/04保持in_progress、六维不提升；不做长期高负载、KubeVirt/CDI、完整Trace或生产发布。

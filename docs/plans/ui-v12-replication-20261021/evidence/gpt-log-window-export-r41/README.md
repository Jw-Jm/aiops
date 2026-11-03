# r41 精确窗口选择与可见样例 CSV：局部完成，P3未整体验收

GPT直接执行，main脏工作区/HEAD082f87d，未提交/推送、未重置其他修改。复用已有Select、结果读取与审计页Blob下载方式；没有新导出接口/依赖/后台循环。业务请求只在原入口校验，移除了只为手填日期服务的客户端Date有效性/顺序检查，不新增重复判空或泛化容错层。

## 当前运行

[30471日志查询](https://192.168.0.63:30471/observe/logs)，沿用此前交付账号。Web `ops-v12-native-r41b-web-30471`，index SHA256 `4bffe3daf39bbe1c665edb7a2355e34dd09754afcae28c885b9d1fe6df477b5f`；API/Worker仍r39c，任务PG17.11/schema71；30472、账号与授权、来源、存储配置未改。旧r40b/r41包和容器保留。

来源仍为r37真实单Pod固定窗口快照，资格到2026-10-29 10:45 UTC+08，未自动延长，不代表持续日志管道；原Kubernetes自动轮换采集未被替换。

## 实现与实际动作链

- 双datetime-local输入改成切片的单窗口Select。候选来自合格来源的coverage/maxWindow及已获准的历史窗口；窗口值保留原始RFC3339字符串，历史自定义窗口不会被当前来源默认窗口覆盖。超过单次窗口上限时生成明确的受限窗口；不提供任意表达式。
- 提交和历史回填不再通过Date.toISOString截断微秒；日期计算仅用于标签或受限窗口候选，最终请求使用选定窗口原值。已有服务端schema/权限/来源/预算校验不变。
- 查询检查与“导出数据”移至结果头部；纠正旧summary后代span选择器误把按钮文字染灰，样式只限定查询编号文本。
- CSV只导出点击时当前可见的脱敏样例（关键词/时间桶/排序），不把样例当总体；每行包含原始时间、资源/命名空间UID、来源版本、半开窗口、queryId/resultDigest和总体/返回/导出三个数量。
- 下载前使用同一结果Query的refetch重新GET C168，当前原生会话/权限/来源/过期由现有服务端链核对。读取失败不生成Blob、不导出旧缓存；资源/集群切换后generation拒绝迟到下载；空可见集合禁用导出。刷新核对也重读失败结果，清理已显示的导出错误。
- 复用Blob/anchor下载，不新增通用导出框架。CSV引号、逗号、换行按引用规则处理，可能被表格软件解释为公式的来源单元格按文本前缀输出；这是数据输出边界处理，不是额外业务防御层。

P0原query-export stage=local描述原型的CSV动作，不能冒充当前运行链。**当前仅/observe/logs补充prefetch C168 → local CSV**，完整处理器/服务/存储/回显见implementation-bindings.json与实际requestId。其他指标/网络/硬件导出不因本轮而声称接入；未为维持旧阶段计数而伪造无网络导出，也没有新增导出服务。

## 验证

- ui-final15.log：15项Vitest。原10项继续通过；新增 fresh-result/可见样例/CSV公式与多行引号转义、403不下载且隐藏旧数据、资源切换后的迟到导出、历史微秒窗口原样提交、超过来源单次上限时的窗口收敛且不截断结束时间。
- build-final15.log：TypeScript/Vite正式构建通过；既有大chunk提示保留。r41b仅修正头部文字颜色选择器，功能代码与真实导出验证版本一致。
- browser-export.json：远程真实Chrome1440×960、原生cookie、真实来源。单选择器请求from/to与资格窗口字符串逐字一致；总体186/返回20，筛选后CSV导出1条。导出恰好多读一次C168，createCount保持1，没有新建查询。空筛选不导出，也不冒充no_data。
- export-persistence.json：Python标准csv解析实际Chrome下载，核对字段与任务Postgres不可变请求/结果：UID/namespace/source/window/queryId/digest一致，原始样例纳秒时间也未丢失，总体186/返回20/导出1。CSV字节摘要已记录。
- capture-states-r41b.json：最新Web恢复同一既有查询，0次create；默认、换行、关键词空态/长页通过r40日志捕获前后探针。图片与实际CSV在 `artifacts/test-reports/ui-v12-product-r41/`。未重复原736张参考图或修改黄金图。
- UI拒绝/迟到下载为明确单元机制证据，本轮没有改动既有用户授权再做live撤权；实际下载前读取200的链路已有当前服务端复检，不把该正例替代全部负例验收。

## 失败与视觉

ui-cancel-wait-failed.log保留异步提交期间取消按钮尚disabled的fixture点击问题；用例等待按钮enabled后点击，不放宽生产逻辑。ui-history-*-failed.log保留未限定弹层的“查看”匹配到背景“查看来源状态”的定位错误，改为历史弹层内定位。build-test-type-failed.log为mock.instances的unknown类型，明确DOM类型后构建通过，不修改业务类型或错误处理。

已人工查看正式截图：单窗口和头部导出恢复，尚缺“按此范围询问AI”/预置监控入口，顶部空间、公共壳/角色导航与全部状态仍未对齐。真实数据替代演示值单独记录；没有新掩码或整图视觉通过结论。完整P3、持续来源、两真实集群、自然到期/完整重启及启动503继续未关闭；不做长期高负载、虚拟化或生产发布。

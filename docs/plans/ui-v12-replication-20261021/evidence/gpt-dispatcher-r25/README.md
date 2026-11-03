# r25：运行中Dispatcher生命周期与MFA状态真实性（2026-10-27）

GPT直接实施；main/082f87d66c5b，保留r23/r24全部未提交修改。没有CodeBuddy、生产发布、虚拟化或Trace。

## 改动与实际验证

新增 `test/integration/native_dispatcher_lifecycle_test.go`，复用正式Dispatcher/Repository与原生账号fixture。在ssh mssc-direct、任务独立PostgreSQL17.11/15454的全新随机数据库中迁移至70：

- 真实RunOne发出两个已登记上下文后，用户取消或撤权，正式10秒Heartbeat取消远程请求上下文。调查器故意返回迟到成功，不能覆盖取消态/平台部分结果或写入撤权结果；旧上下文拒绝、撤权Get拒绝。
- Worker进程上下文停止，运行任务保持可接管；测试提前推进已存租约到期后，再次RunOne领取generation2，generation1签名上下文被拒，新上下文验证后发布成功。

回归通过26.38秒。使用测试ES256签名器与远程调查器函数，不能冒充生产Transit/mTLS/模型/工具/独立Worker进程重启。数据库含真实审计而保留，未删除历史库、镜像或数据。首轮两项错误测试假设及日志保留，未修改产品生命周期实现迎合测试。

`web/src/pages/AccountSecurity.tsx`此前在状态读取未完成/失败时显示“未启用、恢复码0”并可开始绑定。现复用React Query和QueryState，查询key含主体及会话，接入abort信号；加载时不显示推测状态，错误时提供重试。保持真实绑定、恢复和再次验证写入链路。新增2项回归，原管理账号选择测试等待服务端列表启用后操作。

180 Vitest+6 SSE、TypeScript/Vite构建通过。已将独立30471升级Web-r5，远程真实浏览器等待/网络中断/重试通过；重试GET200请求ID见mfa-status-browser.json。3张截图只证明相应状态存在，未宣称完整视觉通过。错误截图在用户列表仍加载时采集，不作为整页静态像素验收。

## 版本与恢复

地址 https://192.168.0.63:30471/login；当前容器ops-v12-native-r25-web-r5，目录 `/data/ops-v12-native-visual-r24-20261026/web-r5`。r24旧Web-r4停止保留；API/Worker final、Bao/Archive和原生业务数据库schema70未改。原生账号/密码/MFA私密材料未改且仅远程private。

`source.json`绑定r24完整快照与本轮3文件增量、集成二进制、Web制品哈希；最终增量source-delta-2仅包含明确3文件，首个带AppleDouble元数据的包保留。`web-runtime.json`记录镜像与实际Web。回归执行目录 `/data/ops-v12-native-visual-r24-20261026/r25-dispatcher-20261027/attempt-2`，失败attempt1保留；重复运行需新目录，不覆盖回执/数据库。不重跑prepare/init/bootstrap。

## 剩余

SP06业务独立Worker真实签名Context/mTLS/工具结果/来源资格/接管取消异常恢复、正式不可变Bundle与切换仍待验证；P1/P2完整27状态与补充状态视觉、P3 SL-049及P4～P8仍未完成。本轮产品六维提升0，不能把回归fixture与r24审计Worker合并成业务Worker通过。

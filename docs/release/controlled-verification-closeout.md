# 受控 TCP/HTTP 与专用 PVC 文件写读验证

日期：2026-10-19。本轮完成安排中的第一阶段功能开发；统一安装补齐、远程部署与运行验证仍是后续阶段。没有远程访问、部署、性能调优或生产发行。

## 执行与确认

复用原有 CommandExecution、本人风险确认、单次 Runner claim、mTLS 回调、审计及输出归档。请求增加可选 `verification`，类型为 tcp、http、pvc_read_write；必须关联同租户、同故障、同目标 UID 的已完成执行。普通命令不变。专用分支不解释 Bash，实际意图固定为 `ops-verify <kind>`；不能混入人工命令。

API 绑定目标真实名称、配置的 Runner namespace、探测参数、HTTP 断言和可能的回退配置目标，使用 `execution-request/v4`。修改参数必须重新评估并本人确认。只有已签名/准入、工具声明包含 `ops-verify` 的 namespace Profile 可用，派发与 claim 阶段再次检查；没有新增角色或扩大已有 RBAC。前端从关联执行进入，显示探测参数与风险；不会自动发起验证。

## TCP 与 HTTP

Runner 通过该 Profile 的短期 Kubernetes 凭据读取原目标 Service，核对 namespace、名称、UID 和 core/v1 类型。网络地址只取当前原生 ClusterIP，端口必须是 Service 声明的 TCP 端口。不接受调用者提供主机、URL、请求头或凭据，不使用代理，不访问 loopback/link-local/unspecified 地址；Headless、ExternalName 或无法得到有效地址者不能证明连接恢复。

TCP 建立实际连接；连接被拒绝为 not_resolved，超时、无权限、地址不可用等为 inconclusive。HTTP 只发送 GET 到明确、经操作者核对为无副作用的健康检查路径，不接受 query、外部 URL 或重定向跳转；验证预期状态码及可选响应正文 SHA-256。响应上限 64 KiB，正文不存档，只记录摘要与状态。HTTPS 使用系统信任根并校验 Service 名称（`name.namespace.svc`），不跳过证书验证，不注入业务认证。单次网络探测 5 秒有界；原执行和 Job 仍受 Profile 超时约束。

网络位置是独立 Runner Pod 所在 namespace，明确进入确认摘要和回执；不是任意业务客户端的位置，也不验证 DNS。探测结束复读同一目标 UID/resourceVersion；变化时保持 inconclusive。真实应用客户端的 NetworkPolicy、DNS、认证、长连接及其他流量路径不在此证明范围。

## 专用 PVC 文件写读

只允许 Runner 同 namespace 的专用 PVC。派发前及 Runner 写入前均核对 UID、Bound 和 `ops.platform/verification-only: "true"` 注解；不自动给业务 PVC 加标记。固定挂载 `.ops-verification` 子目录到 `/run/ops-probe`，无用户路径参数。管理员须预备专用卷/目录及 UID 65532 所需权限；未就绪不能作为通过。

探测 Pod 删除原 `fsGroup` 设置，不通过递归 chown 改变整个卷内文件权限；不创建特权容器或 hostPath。使用 `os.OpenRoot`、`O_EXCL` 与 0600 权限，仅创建 `probe-<executionId>` 这一个文件，写入 4 KiB 随机数据、fsync、读取校验，最后关闭并清理。不会覆盖已有文件，不做通配删除，也不递归清理目录；同名残留直接拒绝。清理失败为 inconclusive，可根据执行 ID 在专用目录定位残留。中断或未知副作用不自动重试。

该结果证明的是 Runner 对专用 PVC 的一次文件系统写读，不证明业务应用代码、数据库事务、生产文件、全部容量、存储介质长期耐久性或灾备能力。不得把业务卷改标为专用卷来绕过边界。

## 回执与后检查

`command-verification/v1` 回执记录执行/目标 UID、请求摘要、Runner 位置、开始/结束时间、断言结果和清理状态。认证回调校验这些字段、完成状态与输出序列，要求输出字节恰好等于规范回执；缺失、替换或跨执行的回执被拒绝。普通命令 stdout、退出码 0 或模型文本不能充当探测证据。

回执是已授权 Runner 的执行证明，通过既有加密输出归档保存，不冒充 Kubernetes 或 DeepFlow 的原生 Evidence。归档未验证时 Worker 等待原有归档恢复，不重新运行探测。读取时复核当前目标权限、Profile 资格、原生 Evidence/来源/Recipe 权限，后检查才引用相同回执。回执时间还须晚于关联原执行完成，且不晚于本次执行完成；时钟不一致不能判为通过。

可信原生健康事实与实际探测须同时通过；原生来源缺失、探测超时、无回执、未验证归档或新增 Warning 均不能报告恢复。人工回退仍要求已声明的配置摘要匹配；探测执行本身不执行回退命令，也不证明恢复由某次操作造成。原执行的冻结报告不改写，新回执位于关联执行的报告中。

## 启用顺序与验证边界

1. 从同一源码准备 API、Worker、Runner、Web 与 Schema；普通 v1/v2/v3 请求保持原行为，新探测仅 v4。旧 Runner 不被视为支持新协议，混合版本未验证。
2. 经既有签名发布/运行准入流程准备包含 `ops-verify` 的 namespace Profile、对应 Runner 镜像与精确 NetworkPolicy，并进入 SP07 `AdmittedProfiles`。不以在网页选择探测代替发布准入。
3. 网络验证要求 Runner 具有明确的 Service 端口出口；PVC 另需 Runner 同 namespace 的专用标记卷和固定子目录。缺条件不扩大权限。
4. 后续统一安装补齐后，再在独立开发机冻结同一版本进行数据库、页面和专用 TCP/HTTP/PVC 场景验证；远程性能相关失败只归档，不反复压测或修改门禁。

本地 `make check-local` 通过：Go 单元/合同、安全、vet、Python 及 Web；Web 82 个组件用例、5 个 SSE 协议测试及 TypeScript 通过。`make check-delivery` 的生成物、既有源码材料和交付合同通过。新增网络/文件、回执与 Runner 分支定向测试通过；Go 全仓构建与受影响包/Integration vet 通过，Runner Linux/amd64 交叉编译通过。

数据库集成新增参数绑定/变更拒绝、回执缺失拒绝、精确输出、幂等回放与未归档拒绝用例，仅完成编译，未在 PostgreSQL 执行。上述网络用例使用本地测试服务，文件用例使用临时目录；不是远程 Service、PVC 或应用验收。历史失败、未运行、人工模型质量 pending 不改写；KubeVirt/CDI、Trace 和 PyRCA 边界不变。

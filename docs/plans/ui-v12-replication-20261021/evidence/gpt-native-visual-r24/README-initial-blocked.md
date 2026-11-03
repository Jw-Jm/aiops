# r24：原生全栈与页面收尾（未整体验收）

2026-10-26，main / 082f87d66c5b，包含尚未提交的 r23 和 r24 变更。GPT 直接实施，未连接 CodeBuddy。

## 代码结果

- 补上 SL-003「我的账号」和 SL-004「修改本人密码」。登录、恢复会话、改密回执投影服务端 loginName/displayName；不解析 subject 猜用户名，不调用管理员接口。沿用真实改密及会话轮换，保留密码错误处理、重复提交保护、等待时禁止关闭、双角色独立显示和现有退出撤销。
- 用户管理复用公共表格/状态控件；改成切片的卡片、表头、字号、间距和详情键值布局，抽屉操作移到页脚，编辑保留只读用户名。增加明确标为“本页”的状态/角色过滤，未伪装成服务端全量过滤；授权版本、再次验证、CSRF、请求中断与不确定提交提示仍保留。
- 公共弹窗 660px 居中、抽屉 540px；依据固定 HTML 的 padding、遮罩、页脚、关闭按钮位置调整。未重新采集参考图。注册的安全扩展（密码 12～256、当前凭据、原因、精确目标证明等）仍保留；未实现的“移除用户”没有假按钮。
- 正式 OpenBao 基础 API workload policy 缺少 MFA Transit 能力。新增显式 `openbao configure --native-mfa-key <existing-key>`：只核实已有的独立、不可导出 aes256-gcm96 key，只授予 API 对该 key 的 encrypt/decrypt，不创建或轮换 key，不授予 Worker。仅允许扩展精确旧 API role，其他 namespace/audience/policy 漂移拒绝。`status` 支持同一参数，纯读取检查 key、ACL 和 role，发现漂移不会修写。

## 验证与范围

`result.json`、测试日志和 `state-review.json` 记录范围。前端全量 172 Vitest + 6 SSE 通过；后续账号/用户页增量再验。Go OpenBao/httpapi/opsctl race、app、完整 contract、生成代码一致性及 runtime/migration 来源检查通过；正式 Web 构建通过，保留既有大 chunk 提示。新增会话投影、权限漂移和本页过滤测试。

27 个账号/用户/授权主状态与 MFA 等补充状态仍需真实浏览器逐状态核对。静态尺寸、单元测试和旧版通过证据不替代新截图及整图差异；本轮没有提升任何切片六维状态。

失败保留：首次 Modal 测试未等待挂载；Ant Design 测试环境固定 `test-id`，新 Select 与 modal 标题 ID 冲突，改为等待唯一 dialog，生产可访问名称仍待浏览器确认。曾误调用不存在的 `test:sse` 脚本；实际全量 `npm test` 已运行六项 SSE，不新增重复脚本。

## 远程实际断点

最后成功核对 `ssh mssc-direct` 为 mssc，/ 根盘约 77%，/data 约 12%。新目录 `/data/ops-v12-native-visual-r24-20261026`，新 namespace `ops-native-v12-r24-20261026`，不修改旧 namespace、镜像、存储类或旧预览。

已确认新的 OpenBao 2.7.0 实例 Ready、初始化、解封、基础 configure/status，以及对新任务已有 MFA key 的 native configure 成功。初次基础 configure 在刚解封时 mount500，原请求重试后成功，未重初始化。reviewer 使用 Kubernetes 投射 token（600s）；**尚未观察轮换前后实际请求**，不宣称自动轮换通过。

首次尝试在既有任务 PG 的新空库 `ui_v12_r24_native` 从 1 开始迁移，被有限 owner 的 ALTER ROLE 权限拒绝；未提高原角色权限。改为准备完全隔离的 `ops-v12-native-r24-pg`、loopback15454、任务目录独立数据盘。发送创建/接续命令时 SSH 再次超时，无法回收结果；新 PG 是否启动、后续阶段是否执行均未知。旧 PG15453 的新失败空库保留，其他旧库未前滚或回滚。

`prepare-bao.py`/`prepare-runtime.py` 是一次性准备脚本，`archive-bucket.go` 调用正式 S3 CreateBucket 核验版本与对象锁。运行配置从原已解析开发 Profile 分离，Keycloak 显式 disabled；宿主机二进制只读挂入已有精确镜像作源码 canary。不是签名 Bundle 安装、PVC/HA 或 OCI 新镜像准入验收。API/Worker 仅挂自己的 binary、公开 Profile/CA/Registry 信任和所需 Secret；没有挂载包含 recovery/root 凭据的整个任务目录。

**30471 是计划端口，尚未确认可用，不作为已交付入口。** r23 的 30470 与旧轮次保留；旧临时 JWT 到期，不假定当前 MFA 仍可用。

## 恢复时直接接续

1. 先核对主机/新目录所有权、`prepare-runtime-attempt-3.log`、PG 容器/端口、新库 schema、namespace 内 Pods/Secrets 与 first-tenant 回执。任务私密材料只读本机 root700/private，不输出、不复制到 Git。已执行的对象和初始账号不得重建或覆盖；这些一次性脚本不能盲目重跑。
2. 按实际断点补新 PG 1→70、原生 first-tenant CLI、Archive IAM/独立 CA/真实 bucket；启动独立 API/Worker/Web。新 API18092、metrics19092，Worker19093，Web30471。上传最终 CLI（本轮末尾修过只读 status），记录最终源码/制品；远程 source 目前仍是 r23 快照，不能当作 r24 完整源码。
3. 观察 reviewer/API/Worker 的投射 token 指纹和到期（不保存 token），超过一轮轮换及 OpenBao 续取后执行实际 MFA/Worker 归档。再验证进程重启、Job 租约接管、撤权、取消和前滚恢复；独立进程 Ready 不等于签名 Context/工具结果业务链通过。
4. 构建部署最新 Web，在同一明确版本上核对 `state-review.json` 的主状态与补充状态。登录/首次改密/账号/MFA/用户授权分别记录视觉、交互、API、持久化、授权、live；秘密仅做最小遮罩，失败截图及请求 ID 保留。

虚拟化、Trace、生产发布、P3～P8 全量验收和用户未要求的功能未恢复。

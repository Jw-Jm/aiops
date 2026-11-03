# r24 接续：原生启动、账号/MFA/权限（2026-10-27）

main / 082f87d66c5b，含尚未提交的r23/r24修改，GPT直接实施，未连接CodeBuddy。**1/2尚未整体关闭**：原生核心运行链已经真实验证，SP06业务Worker与完整逐状态视觉仍未通过。早期阻塞记录保留在README-initial-blocked.md，不代表当前状态。

## 已交付代码

- SL-003我的账号、SL-004本人改密，使用C-007/009服务端姓名/登录名/独立授权投影与C-011真实会话轮换。等待禁止重复提交和关闭，错误当前密码保留会话，退出调用C-010服务端撤销。
- 原生角色拒绝页提供“查看当前身份”。零角色可访问自身安全设置，业务仍分别检查operator/platform_admin，实际管理API403，没有默认授权。
- 用户表格、只读用户名、详情抽屉/页脚、编辑/重置弹窗及标为“本页”的筛选。保留CSRF、精确目标证明、版本、请求中断和提交不确定提示。
- 管理员MFA重置选择真实账号，版本来自服务端投影，移除可编辑UID/revision/凭据版本；保留确认登录名、原因、密码+TOTP、事务内复检与审计。
- 公共弹窗660、抽屉540，按固定HTML修正头部76、内边距、行高、信息条图标/颜色及危险按钮。注册的安全扩展保留。
- `opsctl openbao configure --native-mfa-key <existing-key>`只核验已有独立、不可导出aes256-gcm96 key，仅授予API该key的encrypt/decrypt，Worker无此权限。不创建/轮换key，不覆盖foreign role/policy。同参数`status`纯读取核验，不自动修写。

## 实际运行

[独立开发预览](https://192.168.0.63:30471/login)。最新Web为`ops-v12-native-r24-web-r4`/`web-r4`；API18092/metrics19092，独立审计Worker19093。目录`/data/ops-v12-native-visual-r24-20261026`，namespace `ops-native-v12-r24-20261026`。独立PG容器`ops-v12-native-r24-pg`、loopback15454、数据库`ui_v12_r24_native`、PG17.11/schema70。

新库迁移和正式原生首租户CLI实际运行；API/Worker/Web明确native，Profile中Keycloak disabled。OpenBao2.7.0、Archive独立IAM/CA/bucket/版本及对象锁实际配置。旧预览、旧库、镜像、存储类保留。

## 运行证据

- `runtime/projected-samples.json`：初始同Pod的API、Worker、reviewer指纹/iat自然变化，轮换后真实登录200；API对MFA key200、Worker403。后续重建Pod的采样单独解释，不当作同Pod轮换；不保存JWT/token。
- `account-browser.json`、`member-account-browser.json`：真实等待/401、首次改密取消撤销/重登、错误当前密码401、改密201、刷新恢复及本人改密取消/错误。
- `mfa-browser.json`、`member-mfa-browser.json`：真实Transit设置、仅一次恢复码、刷新不返秘密、恢复撤销、强制重绑，管理员恢复200或零角色仍403。
- `users-browser.json`、`users-followup.json`：创建201且无默认角色，密码+TOTP目标绑定，独立管理员授权/撤销。状态修改先真实提交，暂缓回包再关闭，显示不确定并刷新确认持久化；恢复账号不复活撤销角色。
- `zero-role-browser.json`：自身安全可用，两个业务角色页拒绝，真实管理API403，当前身份及本人改密不授予角色。
- `mfa-admin-reset-validation.json`：重置200撤销两个会话，原有效Cookie200→401；重新登录强制重绑，旧恢复码剩余0，无授权扩大。原脚本完成全部业务断言后缺chmodSync导入导致导出失败，失败原件保留；导入修复及0600独立核验，没有重复执行重置掩盖失败。
- `reenroll-after-admin-reset.json`：重绑解除限制且仍零授权；C-174 pending/confirmed owner GET200不返URI/种子/恢复码，另一个管理员GET404。
- `restart-browser-before/after.json`、`restart-20261027-before/after.json`：只重建本任务API/Worker，新鲜Cookie/sessionRef保持，独立管理员集群200、MFA仍confirmed；退出200后旧Cookie重放401；旧签名段逐行未变。
- `audit-archive-verification.json`：正式验证代码实际从S3读回、解密、核验摘要/签名/前驱链，序号1～49，7个签名段。仅证明审计链，不能替代SP06业务Job。

上列文件位于runtime/。原始请求ID、审计ID和写尝试ID保留。含动态码的早期原图仅在remote private/withheld-shots，不进入公开证据。

## 验证及视觉边界

178 Vitest+6 SSE、Go OpenBao/httpapi/opsctl race、app/完整contract、生成一致性及runtime/migration来源检查、Web构建通过。证据Go工具带build-ignore，显式编译；`go list ./...`通过。保留既有chunk/jsdom提示，没有扩大治理。

原736张参考复用，无重采。`visual-foreground-final.json`、`runtime/account-stable-capture.json`实测弹窗660宽、头部76、DOM高423.8125、行高19.8；参考和产品前景栅格均660×424。整页仍有底图/滚动条、实际账号/授权、焦点和正式文案差异，**没有判整页视觉通过**。早期动画截图及无效数值诊断保留并标无效；最终等待rc-motion安装并结束，再作元素与页面截图。

`state-review.json`保留27主状态和补充状态，不因单元、机制或前景尺寸提升六维。`contract-audit-before.json`保留历史判断，当前C-012/013、C-172～176按源码与运行范围修正，原始新增分类及original-plan/不改。C-001是参考背景overview；账号用C-007/009，准确字段链见form-implementation-bindings.json。

## 版本、启动与恢复

最新源码/二进制摘要见`source-final-2.json`，远程`source-final-2/`保存运行及测试源码。早期source/为r23快照，只作历史。API/Worker只读挂各自final二进制，Web挂web-r4。这里是源码canary，未宣称新OCI/签名Bundle、空缓存离线冷装、PVC/HA或生产发布通过。

管理员`v12.r24.admin`与旧预览不共用账号；当前密码只在remote private/browser-account.json。MFA和Cookie材料仅root700/private、文件0600，不复制进Git。该管理员没有operator默认授权，资源/日志来源尚未配置。

恢复先通过ssh mssc-direct核对主机、任务标签、Pod/容器/端口/库版本及回执。不要重跑init、first-tenant或一次性prepare脚本，不覆盖私密材料；数据和撤销状态只前滚，不恢复旧库快照或旧身份凭据。可用api-pod-final.json、worker-pod-final.json恢复本任务Pod。

主机10-26重启后无线接口wlp4s0变成wlp3s0，原连接绑定已修；10-27再次断连，重新激活同一已保存连接后6443恢复。未改地址/凭据或存储，未新增监控循环。先核对，不假定无线或旧临时JWT仍有效。

## 仍未关闭

- SP06业务Worker签名Context/真实工具结果、业务Job租约接管、取消及异常恢复；当前独立Worker只启用审计/Archive。
- P1/P2全部27状态与补充错误/过期/权限/长页的完整视觉和全部交互，子路径不能推导整组通过。
- 正式Bundle/OCI/离线安装闭包及既有来源材料问题，未绕过准入。历史映射待验、P3 SL-049真实资源日志、P4～P8继续按依赖推进。
- 不恢复KubeVirt/CDI、Trace及生产；本轮未提交/推送。

## 跟踪清单收尾

2026-10-27：当前177合同/458状态结构校验通过。账号前景新增会话/登录/退出绑定由`../contract-adaptations.json`显式登记，冻结slice-map原候选保持不变。`check-contract-adaptations.py`验证基准删除和未登记增量都会失败；初始校验失败保留。`go list ./...`、审计核验工具显式文件Linux构建、diff whitespace检查通过。30份导出runtime JSON通过秘密字段/JWT/私钥/otpauth保守扫描；未导出private材料。以上均不提升产品验收。

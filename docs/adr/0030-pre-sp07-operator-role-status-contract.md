# ADR-0030：提供正式 Operator RoleBinding 撤权与恢复入口

Status: Accepted（本轮 R1/R5/R6 授权范围内的实施决定；真实新交付验收未完成）

## 问题及证据

R9 正式初始化通过真实 Keycloak LoA2 身份创建 Operator RoleBinding。现有 `tenant.Service.UpdateRoleBinding` 有 revision、RLS 和 Audit，但 HTTP 路由只有查询/新增。当前正式部署对状态更新返回 404，角色未改变；见 [真实失败](../evidence/pre-sp07-20261007/current-core-r7-role-status-red-r1.json)。不能靠直写表或专项构造器完成本轮正式撤权验收。

## 决定及兼容性

新增 `PATCH /api/v1/admin/role-bindings/{bindingId}/status`，operationId `setOperatorRoleBindingStatus`，只接受 `expectedRevision` 与 `status: active|disabled`。仅支持既有 Operator binding；不通过该入口修改其他角色。身份、tenant、role、cluster/namespace scopes 从当前租户的被锁定 binding 加载并原样交给既有审计更新，不接受调用方提供这些字段。

当前有效平台管理员与真实 step-up 必须在幂等重放之前验证。复用既有租户幂等事务、Admin 当前授权、revision 比较、既有 Scope 检查和 Audit。新请求拒绝未知字段、额外 JSON、非法 UUID/revision/status 和超长输入；旧 revision 409、跨租户不可获取目标。恢复明确推进 revision，使旧授权 Context 不被恢复为有效。

OpenAPI minor 从 1.3.0 增至 1.4.0，REST major 仍 `/api/v1`；这是新增 operation 和新请求 DTO，没有给既有 DTO 加必填字段、改窄旧枚举或改变旧响应语义。重生成全部已知 Go/TypeScript 消费者。根目录正式文档只读不修改。

该操作是 SP-03 已有角色底座的撤权入口。没有新增 CommandExecution、RiskAcknowledgement 执行业务、Runner、SSH/Ansible 执行、SQL/URL 工具或 ActionPlan 执行句柄；SP-07 仍未开始。

## 验收及交付限制

保留原生 404 与 Contract 失败证据，再实施修复。数据库回归须证明撤权后 Graph 授权拒绝、旧 revision 不能恢复、恢复后的身份/范围未扩大、非 Operator 不受该入口修改及审计记录保留。正式 HTTP 的幂等、step-up、跨租户、读权限撤权/恢复和旧 Context 仍须在新签名源码绑定交付上验收。

R10 已在新的 R8 namespace 完成正式空库迁移、身份与 Registry 初始化和业务安装。真实 Keycloak step-up 请求暴露了中间件顺序缺陷：`requireTenantAdminStepUp` 在幂等事务创建前读取事务，返回 500，角色未改变；见 [原生失败](../evidence/pre-sp07-20261007/current-core-r8-operator-status-native-r1.json)及 [PRE-R1-ROLE-002](../evidence/pre-sp07-20261007/operator-role-status-native-transaction-defect-r92.json)。

补充真实 PostgreSQL 与完整 Router 的失败回归后，将该入口的 step-up 验证放到既有 `IdempotencyMiddleware.AuthorizeTx` 扩展点。它在租户事务内、幂等 `Begin` 和成功响应重放之前验证并刷新当前会话；不得只调整顺序使缓存重放绕过当前 step-up。回归同时证明无效字段拒绝、精确状态修改、幂等重放不增加 revision、恢复及撤销 step-up 后缓存请求拒绝；见 [失败](../evidence/pre-sp07-20261007/operator-role-router-red-r96-command.json)和 [源码回归](../evidence/pre-sp07-20261007/operator-role-router-green-r98-command.json)。最初测试输入缺少必需 scope 的准备失败单独保留，不能当作缺陷复现。

修复后的源码尚未进入新签名 Bundle 或原生部署；R10 的真实接口验收仍失败。新源码门禁、签名交付、原生 HTTP/旧 Context 验收和最终完整独立审核待完成，不能沿用 R9/R10 签名或旧源码 PASS。

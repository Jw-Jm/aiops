# r17验证范围

沿用现有守卫TestNativeMFAEnrollmentStepUpAndReset，对获准mssc-direct任务PG15453/schema57创建新租户/账号。真实数据库、原生Cookie/CSRF、HTTP处理器、密码+真实TOTP时间窗、操作证明和幂等链；HTTP由httptest调用正式Handler，不是30469浏览器验收。MFA密钥使用既有文件sealer测试fixture，不冒充生产OpenBao验证。

新增审计故障：仅匹配新建tenant/target/identity.admin.update事件的自建临时trigger。DDL以SET LOCAL ROLE schema_owner在独立事务安装/清理；不改存量角色或用户。失败后检查账号revision和业务审计没有提交，恢复后同证明、同幂等键重试和重放，只提交一次业务变更及审计。

新增并发：两名独立授予的原生管理员，准备两个精确password/TOTP证明。测试控制事务先持有既有tenant advisory lock；确认两个真实HTTP事务均已在该锁等待才放行。验证一项200、一项401/403/409，恰好一名有效管理员；幸存管理员通过实际登录/必要首次改密及用户列表读取证明仍可用。覆盖一次真实重叠，不宣称所有调度排列。

1～57迁移冻结且未修改；不修改当前30467/30468/30469预览或用户r14双角色账号；不接入模型、Kubernetes、Worker或生产。原始日志仅远程private，回收证据做秘密行清理。默认跳过和仅编译不计运行通过。

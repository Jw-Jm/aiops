# r11本地代码复审与修正

本轮核对r8～r10原生账号/授权、主体清单/映射、执行配置读取和平台chart，沿HTTP调用方、事务、SQL、生成客户端与界面分页读取。是开发过程中的定向复审，不是全仓库安全审计或全部V1.2验收。按人工配置管理交互及当前chart单API/双Worker配置审查；没有真实大目录或并发负载证据。

## 必须修复：已改源码，验证范围分开记录

1. **P1：更新一个角色可能复活另一个失效角色。**
   位置：migrations/00055_native_user_administration.sql:158。
   具体情况：账号authorization_version=4，旧platform_admin行仍active但version=3，因版本不匹配已失效；管理员只替换operator时，原SQL将所有active行写为5，会让旧admin重新有效。修改为只推进与目标账号变更前版本相同的授权行；所选角色仍按显式请求撤销/创建，其他失效角色保持失效。
   test/integration/native_user_administration_test.go新增受控stale-admin fixture并检查更新operator后仍非管理员。55未执行，本轮只静态核对及交叉编译；不能写该SQL回归已运行通过。原本合法的双角色保留断言继续存在。

2. **P1：非SP06的动态PKI API缺少OpenBao地址/CA。**
   位置：deploy/charts/ops-platform/templates/deployments.yaml:105；调用方internal/app/workload_identity.go:23。
   具体情况：workloadIdentity.mode=openbao-kubernetes，SP04开启、SP06关闭、默认OIDC模式，runtimeWorkloadIdentity要求的OPENBAO_ADDR/OPENBAO_CA_FILE没有被渲染，启动会在取证书前拒绝。已将该PKI模式加入API环境接线条件，复用既有CA和投射JWT，不授予OIDC API原生MFA key配置。
   新Helm回归用例在修复前确实失败（缺少这两项），修复后完整合同及最终定向复验通过。失败日志gpt-review-r11-pki-regression-before.log保留。只证明渲染，不证明真实发证/冷启动。

3. **P2：执行配置列表忽略分页合同。**
   位置：internal/httpapi/action_handlers.go:190；实现internal/httpapi/execution_profile_page.go；调用方AdminPage和CommandComposer。
   具体情况：界面发送limit=50和cursor，原处理器仍返回整个授权结果且没有meta.nextCursor，导致分页无效。现在先完成服务端当前权限过滤，再以已可见profile ID+精确版本定位下一页；同ID的不同版本不会跳过，失效/隐藏/未知游标拒绝，要求回首页刷新。
   默认100、最大200，cursor最多128字符，统一400错误；返回meta.nextCursor，OpenAPI与生成代码同步，GET明确支持原生Cookie。分页单元覆盖跨版本、末页/空数组、隐藏锚点、非法游标/limit，HTTP非法limit在触库前拒绝；13项相关前端定向测试通过。
   当前仍复用既有“读取tenant配置目录→授权过滤”方式，没有新增DB keyset查询；配置目录扩大时需下推分页，代码已标shortcut。没有把大目录性能或真实DB/浏览器写通过。

## 边界修正

4. **P2：已核对清单可带数据库不支持的NUL文本。**
   位置：internal/identity/subject_inventory.go:46/51。
   具体情况：reviewedBy或legacySubject中的转义NUL能通过Go结构解码，之后才由PostgreSQL jsonb/text拒绝。现在在清单解析阶段拒绝，CLI的预连接校验因此可直接失败，保持完整旧主体精确匹配，不修改合法字符串或按名称推断。新增两组解析回归，identity及CLI定向测试通过。

## 验证与剩余问题

- identity/httpapi/action/app完整包、完整test/contract通过；最终OpenAPI/UIV12/原生及OIDC Helm定向复验通过；CLI相关定向通过。
- 相关前端3文件13项测试及TypeScript/Vite构建通过；既有canvas模拟提示和大chunk警告保留，不安装新依赖来消除提示。
- Linux API/opsctl/守卫集成二进制仅构建，未运行远程业务。冻结1～54保持；55/56未执行、不冻结。
- 正式Bundle安装器/business-values/Profile仍有OIDC依赖，Job/Worker显式原生authority/user/version与签名跨进程复检尚未完成，P2-05不能通过。历史业务记录归属读取、并发撤权/最后管理员和审计回滚仍需实现或远程核验。
- 现有opsctl全包来源材料锁失败独立保留，未因定向测试通过而关闭。

结论：本轮已修复上述源码问题，P2-04/P2-05继续in_progress；不是部署或V1.2整体放行。未检查真实SQL、运行发证、业务浏览器/HTML像素及完整负载行为；恢复获准远程后再验证。

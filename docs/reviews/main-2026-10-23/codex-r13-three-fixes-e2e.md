# r13：三项修复与端到端验收（2026-10-25）

结论：**独立复审中的3项问题已修复；MR-001、MR-017、MR-018、MR-019 的指定端到端缺口已补齐，并在 mssc-direct 执行通过。** 本轮没有调用 CodeBuddy，没有提交、推送、合并或发布。

本轮叠加在既有 r11/r12 未提交工作树上。此结论针对下列修复和测试链路，不代表全部 MR-001～020 已完成最终安装态验收。原始 [r11独立复审](independent-r11-review.md) 和各轮失败证据保留。

## 3项问题如何修复

1. **pending 符号链接越界写私钥**：`internal/integrations/openbao/bootstrap.go` 对派生 pending 路径独立检查受保护目录；先解析并记录父目录身份，再以 `os.OpenRoot` 固定目录句柄，并比较打开后的目录身份。pending 读取拒绝符号链接/非普通文件，核对已打开文件身份；写入改为0600排他创建，已有条目不会被截断或跟随。新 CA 与 pending 的创建/清理沿用固定目录句柄。已有公共 CA 指向另一个外部目录的合法别名仍可读取。
2. **已有有效证书搭配错误私钥仍被接纳**：每次接受已有 Secret 都解码私钥并调用 `tls.X509KeyPair`，与 CA、服务名和已知 pending 身份校验共同生效。测试保留真实有效证书，只替换为另一把有效私钥、缺失私钥或非法编码，不再用无效证书造成测试假阳性。
3. **MR-005 数据库夹具缺初始化**：关系测试复用 `sp05Database` 的真实初始化和 worker pool；共用初始化先以管理员执行版本1，再授予 schema_owner 对本次新库的 CREATE 权限，然后以受限 migration_role 前滚。端到端复用的 native Job 夹具同步修正相同的角色引导顺序。

原缺陷的触发输入现在被拒：悬空/已存在 pending 链接不能创建或截断受保护文件，排他写不能覆盖已有文件或链接；保留有效原证书并改变私钥时返回 `OPENBAO_TLS_SECRET_INVALID`。这些反例在修复前已复现，在修复后通过本地 race 与远程 OpenBao 全包测试。

合法流程保持：首次创建、确认后的幂等重试、合法外部 CA 别名、未知结果保留 CA/pending、同一身份恢复及0600权限均有回归覆盖。

## 新增和补齐的端到端链路

### MR-001：预算必须先于来源请求持久化

`test/integration/main_review_budget_rpc_e2e_test.go` 的 `TestMainReviewCurrentResourceMCPReservationBeforeSource` 使用正式 MCP SDK、实际 Gateway、签名上下文与 mTLS、真实 PostgreSQL admission/预算账本、AssistantTools、签名 Graph HTTP 和实际图查询。

在来源 HTTP 请求即将发出时，用另一条数据库连接确认 `budget_reserved.rawQueries=1` 且 admission 已是 reserved；查询不携带时间窗，实际返回1个 Pod并提交查询结果，结算后 RawQueries 消耗1、预留归零。下一次不同查询耗尽预算，被拒且来源调用次数保持1。

### MR-017：原生身份贯穿到 Worker 成功响应

- `TestMainReviewNativeInteractiveAndTaskGraphRPC200`：从真实原生账户/会话、已激活的 policy、正式 Submit/Claim 得到持久任务；原生会话和持久任务分别经签名+mTLS进入真实 Graph Worker，均返回合同有效且含预期 Pod 的200。撤销 operator grant 后，同一任务不能再读图。
- `internal/app/main_review_native_rpc_e2e_test.go` 的 `TestMainReviewNativeEvidenceAnalysisAuditRPC200`：worker_runtime_role 对原生账户表无直接读权，仍能通过窄解析函数重证原生会话。正式 Evidence、Analysis、Audit Worker HTTP 入口分别返回200；Analysis 通过正式固定模板适配器读取6次重启。legacy 上下文不能借用原生授权，撤销会话后不再到达来源或审计 verifier。
- 修正旧 `native_internal_rpc_test.go` 的测试证书：补齐与 SPIFFE 身份对应的精确 DNS SAN；没有放宽生产工作负载证书验证。

### MR-018：指标判断引用真正的 Step，且可经 API 读回

`TestMainReviewMetricJudgementSourceStepHTTP` 从正式 Victoria 固定模板执行受控指标响应，提交真实 QueryResult/Step，调用正式 evaluator，确认判断的 supports 使用 **StepID**，事实引用保留 QueryResultID；提交 evaluator Step和最终答案，再经正式 `/assistant/turns/{turn}/source-steps/{step}` HTTP 路由读回200。

把 QueryResultID 当 StepID、随机 Step、其他 owner 都被拒；其他 owner 返回隐藏资源存在性的404。既有 `TestNativeFaultChainSourceStepReadBack` 同时实际执行，覆盖跨作业和内容摘要篡改的拒绝。补齐了该旧夹具 admission 的 JTI字段，使它满足原有数据库约束并能执行到业务断言。

### MR-019：Node Unknown 真正结清到 Incident

`TestMainReviewNodeUnknownIncidentRecovery` 使用正式 SyncSnapshot、InspectSP05Snapshot、SubmitSP05Candidate、归档协议、Finding ingest/relay 和 Incident RecoveryPass。Unknown 观测创建 firing Finding与 open Incident；同一 UID 变为 True 后 Finding resolved；等待期未满不能结清，图不可用也不能结清；持久化等待时钟超过301秒且图健康后 Incident resolved，重复健康样本不重新打开。

### 真实 Kubernetes bootstrap 与数据库迁移

`TestOpenBaoBootstrapTLSLive` 在实际 Kubernetes API 的独立命名空间执行：pending 链接被拒且不产生 Secret；真实 Secret create 已提交但客户端回执和后续查询被定点故障注入，确认 CA/pending 保留；恢复真实 kubectl 后，同一身份收敛且 CA不变。随后从集群 Secret建立真实 TLS连接验证 CA/SAN/密钥可用，修改实际 Secret 的私钥后 bootstrap 拒绝，恢复私钥后幂等成功。

`TestObservedRelationsUseTheExactIdentity` 已在真实空库完整执行，覆盖关系身份正反例；`TestMainReviewMigration63To69` 另外验证现有63版本库由受限 migration_role 前滚到69。未修改已应用1～63的迁移文件。

## 实际验收结果与复跑入口

结构化回执：[evidence/r13/verification.json](evidence/r13/verification.json)。最终日志：

- [integration-final.log](evidence/r13/integration-final.log)：8项顶层集成测试通过，无失败或跳过。
- [native-rpc-final.log](evidence/r13/native-rpc-final.log)：1项顶层、3个 RPC 子项通过，无失败或跳过。
- [openbao-final.log](evidence/r13/openbao-final.log)：OpenBao 全包25项顶层测试通过，无失败或跳过。
- [local-race.log](evidence/r13/local-race.log)：OpenBao、app、resourcestore、resource 四包race通过。本机数据库专项不执行，由上述远程命令验收。
- [local-opsctl.log](evidence/r13/local-opsctl.log)：CLI包通过；[local-vet.log](evidence/r13/local-vet.log) 无诊断；diff检查通过。

测试以本机 Go1.27.1 交叉编译为 Linux/amd64 测试二进制，再在 mssc-direct 实际运行。没有用远程旧Go工具链冒充当前构建。远程源码/二进制/日志在 `/opt/ops-main-review-r13-20261025`，使用专属 `ops-main-review-r13-pg` PostgreSQL17.11容器。连接口令保存在远程0600私有文件，未放入仓库或日志。

具备合适Go工具链及隔离环境时，可从仓库根重跑：

```sh
# 从外部安全提供 SP03_TEST_DATABASE_URL 和 OPS_OPENBAO_TLS_TEST_CONTEXT；不使用共享生产库。
go test ./test/integration -run '^(TestMainReview.*|TestObservedRelationsUseTheExactIdentity|TestOpenBaoBootstrapTLSLive|TestNativeFaultChainSourceStepReadBack)$' -count=1 -v
go test ./internal/app -run '^TestMainReviewNativeEvidenceAnalysisAuditRPC200$' -count=1 -v
go test -race ./internal/integrations/openbao ./internal/app ./internal/resourcestore ./internal/resource -count=1
```

开发中失败日志保存在同目录的 first/second/third/fourth 文件，分别记录夹具的速率配置、DNS SAN、先冻结授权后新增来源、HTTP拒绝状态及未填账户版本字段等问题；这些是历史失败，不替代最终日志。

## 验证边界与停止状态

业务测试中的 Pod/Node输入、Lease API、Victoria 响应、Transit/S3 和 Audit verifier 有明确的受控夹具；PostgreSQL迁移/RLS/原生权威、正式业务入口、签名HTTP和mTLS实际运行。Audit verifier计数只证明原生授权到验证器的调用边界，不是实际归档签名验真的证明；真实 Kubernetes bootstrap 使用实际 API，仅故障回执由测试注入。

未重跑安装态全部真实来源、完整 Bundle安装或浏览器端到端；未将上述结果提升为20项总体完成。测试命名空间已发起删除；含 Audit/Evidence的测试数据库按既有保留规则留存，专用 PostgreSQL容器保留。没有修改共享部署、正式00～10文档或恢复虚拟化阶段。

用户授权的本轮修复和指定端到端补齐已经完成，到此停止。

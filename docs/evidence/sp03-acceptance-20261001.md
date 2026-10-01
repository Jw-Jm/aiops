# SP-03 非虚拟化最终验收补充报告

## 结论

**SP-03 非虚拟化范围通过。** 用户于本轮明确批准临时 TokenReview 集群授权后，补齐原报告中 Task 3.7、3.8 的真实验收缺口。修复后的全部 SP-03 底座再次执行代码、生成物、安全及真实集成门禁，退出码均为 0，显式集成与安全入口零跳过。已发现的范围内阻断问题清零。

**KubeVirt/CDI 延期、未验证。** 未启用 virtualization/full Profile，未运行虚拟化 live PoC，未修改根目录 00～10 正式文档，未开展 SP-04～SP-09 业务实现。

本报告与[上一轮完整审查报告](sp03-review-20260930.md)共同构成验收记录。上一轮“证据不足”及全部失败日志保留，不能把当时的跳过项改写为通过。

## 实际基线、提交与审核范围

- 原 SP-02 定位点：`5c1ee721155bedce87bf540a56b3ec8736b71639`。
- 原 SP-03 交付：`5c1ee721155bedce87bf540a56b3ec8736b71639..ebe143c033ceb4615cd7c8689082a3c0da599ada`，Task 3.1～3.9 的 11 个原提交见上一轮报告。
- 实际初审 main 基线：`a645a4208c0ce745232d16c8244c29f2b38485df`；本轮续验 main 基线：`d056d22b207fd2f952fc2a15fbb6ba73f49f82f5`。
- 最终受测代码 HEAD：`312f37b6492d4a1ec305b77067ea289840739e83`；代码修复总 range 为 `a645a4208c0ce745232d16c8244c29f2b38485df..312f37b6492d4a1ec305b77067ea289840739e83`。
- 本轮新增代码/测试 range：`d056d22b207fd2f952fc2a15fbb6ba73f49f82f5..312f37b6492d4a1ec305b77067ea289840739e83`，4 个提交：`30e93ec` 配置 namespace 绑定修复；`d8fbbf2` 实际 Worker 进程 SIGKILL 验证；`c4888f8` 有界故障分类；`312f37b` 安全进程诊断。完整 SHA 见 [repair-commits.json](sp03-acceptance-20261001/repair-commits.json)。
- 报告提交只归档证据；最终交付 HEAD 为本报告所属 Git commit。按用户此前“SP01、SP02、SP03 全部合入主分支”的授权，在原 main 仍干净且为 d056d22 时快进合入。
- 工作路径为隔离 checkout `/Users/mssc/Documents/Code/ops/.worktrees/sp03-review-fixes`；原 `/Users/mssc/Documents/Code/ops/platform` 未 reset、stash、clean 或回滚。`ac8f4ff` 用户保留材料保持原样，未纳入 SP-03 审核批准范围。宽泛的 `5c1ee72..HEAD` 还包含其他历史，不能把该范围所有文件都称为 SP-03 交付。
- 已结合原实际 diff、实现调用路径、14 个 forward-only migration、依赖锁、Schema/OpenAPI/生成物、测试与原始证据重新审查全部修复后的 SP-03 路径；本轮额外核对 Pod 登录→PKI→mTLS 和命令入口→Worker-only LOGIN→RLS→Transit→S3→VerifyRange。go.mod/go.sum 与初审基线无差异。本轮未改变公共字段或放宽安全门禁。

## 本轮发现、最小修复与复验

### R14：非默认安装 namespace 不能登录 OpenBao（Task 3.8）

- 位置：`internal/integrations/openbao/bootstrap.go:795`；回归：`internal/integrations/openbao/audience_test.go:32`。
- 复现：ServiceDomain 为 `ops-sp03-review-20261001.svc.cluster.local`，原 `parts[1] != "svc"` 判断错误回退到 ops-system，OpenBao 身份角色绑定了错误 namespace。实际 Pod token 登录 HTTP 403，提示 namespace 未授权。
- 修复：判断改为 `parts[1] == "svc"`，默认 ops-system 行为保留；SA、namespace、audience 与 SPIFFE SAN 仍精确绑定。
- `namespace-red.log` 退出 1；`namespace-green.log` 目标及相邻回归退出 0。实际 `workload-first.log` 退出 1；仅重建本轮独立 inmem OpenBao 后，`workload-second.log` 退出 0；最终完整集成再次通过。
- 上述短名均有 `sp03-review-authorized-` 前缀，位于[本轮证据目录](sp03-acceptance-20261001/)。

### R15：Worker 归档失败缺少可用的安全故障分类（Task 3.9）

- 位置：`internal/app/audit_runtime.go:50,135,146`；回归：`internal/app/runtime_test.go:35`。
- 条件：实际 S3 故障后仅有通用“signature pass failed”日志，无法定位归档、Transit、签名或数据库类别。
- 修复：保留内部错误原因，日志只输出固定枚举 `archive|transit|signature|database`，不输出原始错误或凭据；未修改权限、重试或保留策略。
- `fault-code-red.log` 退出 1；`fault-code-green.log` 目标/相邻回归退出 0；实际失败进程日志输出 `failure_code=archive`，最终代码门禁退出 0。

### E1：独立 S3 测试实例耗尽 volume 槽位

- 强化实际 OS 进程测试后，单独一次通过，但相邻组合及多次重跑归档失败；`process-adjacent.log`、`process-adjacent-second.log`、`worker-process-third.log`、`worker-process-fourth.log`、`worker-proxy.log` 均退出 1，保留原样。d8fbbf2 提交时组合验证尚未通过，不能把该提交时刻称为完成验收。
- 临时诊断代理发现真实 PUT 500；锁定 SeaweedFS 服务原始日志 `seaweed-diagnostic.log` 明确为 `No writable volumes and no free volumes left`。默认 volume.max=8，在重复随机 bucket/保留对象后耗尽。JWT 未过期且登录 200，见安全的 `expiry-diagnostic.json`。
- 使用相同锁定镜像新建另一套本轮隔离临时 S3，显式 `-volume.max=256`；创建命令/退出 0 在 `seaweed-create.log`。旧实例及其保留对象完全保留，没有清理共享 S3、删除证据或降低保留断言。
- 最终测试移除诊断代理，直连 `http://127.0.0.1:32777`；`capacity-green.log` 退出 0，随后最终完整集成退出 0。这个变化仅处理隔离测试容量，不构成产品容量/HA 开发。

## Task 3.1～3.9 最终状态

- **3.1 通过：** 真实 PostgreSQL 空库、旧数据升级、迁移失败回滚/恢复、NOBYPASSRLS/角色权限、tenant 复合 FK、SET LOCAL 单连接池复用、固定 search_path/EXECUTE、运行时不能直接插改删审计及 LOGIN 权限限制再次通过；已执行跨租户复用用例成功越权数为零。
- **3.2 通过：** 规范化摘要、tenant/subject/operation/key 唯一范围、并发/冲突/重放、同事务账本、lease/崩溃/过期和执行请求不重复 dispatch 通过；OpenAPI 写操作覆盖通过。未来执行消费者仅为最小测试桩。
- **3.3 通过：** issuer/audience/签名/rollover、显式租户与 scope、两角色不继承、真实 Keycloak PKCE/OTP step-up 和管理 API、subject/tenant/sid/acr/auth_time、1 小时 absolute/idle 原子触碰及撤销重放通过。时间边界为确定性测试，未宣称实际等待 1 小时。
- **3.4 通过：** Source/Cluster scope、稳定 clusterUid、重复注册、auth_ref、禁用/轮换、revision、RLS、同事务审计与历史回滚通过。
- **3.5 通过：** Policy/Recipe/Tool 各自 Schema、draft/published/retired、签名发布、不可变历史、激活/回滚、scope 冲突与乐观锁通过。
- **3.6 通过：** 本地 OPA 先验签再编译、digest cache、完整确定性决策、tenant/scope/Agent write/step-up/command digest、失败保留有效版本及 fail closed 通过。锁定 OPA v1.21.0 的 3/3 Rego 原始成功证据在上一轮目录，策略源码本轮未变。
- **3.7 通过：** 真实 S3 tenant/digest/Compliance retention、真实 Transit 加解密/轮换、全局 audit_seq/RFC 8785/同事务追加、租户 Merkle 签名分段、v2 predecessor 与归档 proof、删改/插入/重排/缺口/伪签名检测再次通过。实际 Worker 命令进程 SIGKILL 后重启恢复，两个租户 VerifyRange，pending=0；最大证明延迟 **364.365 秒 <600 秒**。测试先写入距当前 6 分钟的记录制造积压，并注入真实 S3 连接拒绝；不宣称故障实际持续了 6 分钟。
- **3.8 通过：** 真实 Pod-bound projected JWT 经集群 TokenReview 和独立 OpenBao 登录；三个授权身份签发成功，未授权 SA/namespace/audience 拒绝；Investigator 与最小测试 Runner 完成实际 TLS 握手/请求，无 JWT 转发，Bearer-only 请求不能替代 mTLS。真实 PKI 撤销/CRL通过；错误 SAN/CA、过期、2/3 TTL 轮换及重叠期确定性测试通过。未宣称进行了小时级实时时钟轮换演练。
- **3.9 通过：** 结构化字段/关联链/嵌套敏感值脱敏、bounded labels、Trace 传播、真实 API stdout→VictoriaLogs 及指标→VictoriaMetrics、实际观测出口拒绝时业务继续并退化通过；CRD 条件渲染属于 Contract 证据。Trace endpoint 可禁用，未宣称存在真实 OTLP collector 的成功采集证据。

历史 v1 审计 manifest 没有签名 predecessor，仍按上一轮 R12 与 runtime Runbook 对其链验证 fail closed，未改写历史签名；本轮真实验收针对 forward migration 后 v2 路径。现有 core 数据/旧 v1 proof 未迁移、未验收；若以后要求旧格式的完整链证明，需要另行审批设计。该限制不被描述为旧数据验证成功。

## 最终命令、退出码与源码绑定

命令在隔离 checkout 执行；工具链 Go 1.27.1、Node 24.21.0、pnpm 12.7.0、Python 3.12.14、uv 0.12.17 均通过锁定检查。

```sh
make check-toolchain             # exit 0
make check-generated             # exit 0
make check                       # exit 0
make test-security               # exit 0
python3 /tmp/sp03-review-authorized-prepare.py # exit 0; only public count printed
source /tmp/sp03-review-20261001-66yogqy9/credentials.env
source /tmp/sp03-review-20261001-66yogqy9/workload.env
source /tmp/sp03-review-20261001-66yogqy9/s3-capacity.env
make test-integration            # exit 0
```

- `make check` 只代表代码/生成物/Contract 回归，其中未提供 live 变量而跳过的用例不计入真实验收。显式 integration **24 个 pass 事件（含 2 个子测试），零 fail、零 skip**；security **11 个 pass 事件（含 4 个子测试），零 fail、零 skip**。逐个事件/退出码见 [final-gates.json](sp03-acceptance-20261001/final-gates.json)，原输出见同目录 `final-*.log`。
- 实际 API 使用 `mise exec -- go build -trimpath -o /tmp/sp03-review-20261001-66yogqy9/platform-api-accepted ./cmd/platform-api`，退出 0；`go version -m` 为受测 HEAD 312f37b、`vcs.modified=false`，SHA256 `f9f70838dfe60a572bbc5507b2aeeb68eeff776601df44cd4fd147e72f77f086`。完整集成的 Victoria scrape 使用该进程。
- 实际 Worker 在测试内通过锁定 Go 构建 `cmd/platform-worker`；白名单子进程环境只含 Worker LOGIN、projected-token 路径、公共 CA、运行时 S3 配置，不继承 bootstrap/root 测试 token。最终第一次 PID 98861 被 SIGKILL，第二次 PID 98863 SIGTERM 后退出 0；DB LOGIN、两个 tenant UUID、bucket 身份见 `final-integration.log`。

## 环境身份、授权撤销与数据保护

- Kubernetes context `orbstack`；仅应用仓库 `test/fixtures/sp03-review/workload-tokenreview.yaml` 与本轮 TLS Secret。临时 namespace、SA/Pod UID、锁定 image/imageID、TokenReview-only 角色规则与绑定见 `identities-final.json`；实际 JWT 的公开 issuer/audience/subject/Pod UID/expiry 见 `token-identities.json`，没有 token 值。
- OpenBao 初次错误 namespace 验证失败后只删除重建本轮 `review-openbao` Pod；最终 UID `4bff6d89-c4ec-4f90-b128-8904e1636abb`。独立 inmem 存储，无 PVC；未初始化或解封现有 OpenBao。
- 七个自建 Docker 容器的 ID、锁定镜像、端口与存储身份见 `final-docker-identities.json`。原六实例不变，新增容量实例 `sp03-review-seaweed-capacity-20261001` ID `61e25d8d13dcbf86c0272f958b83458822c0fbc15da971bd548f28cbd6330a3e`、loopback port 32777、临时 /data。原 S3 port 32773 及保留对象未删除。
- 测试完成后按原 UID 校验再删除 ClusterRoleBinding/ClusterRole 及两个 namespace；第一个 namespace 等待 45 秒超时退出 1，随后确认已消失，保留 `cleanup.log`。第二个 namespace 删除及等待退出 0，见 `cleanup-second.log`。最终四次 `kubectl get ... --ignore-not-found -o name` 均退出 0 且输出为空，见 [cleanup-final.json](sp03-acceptance-20261001/sp03-review-authorized-cleanup-final.json)。TokenReview 授权已撤销，测试 Pod/Secret/ConfigMap 已随 namespace 删除，本轮端口转发和独立 API 进程已停止。
- 现有 core、Victoria、OpenBao、受保护 PVC 和其他 release 未执行写入或清理；未取得新增共享服务权限。独立 Docker 环境仍保留用于复现。
- credential、恢复材料、长期私钥和实际 JWT 仅在外部 0700/0600 临时位置。归档对实际 credential/recovery/S3 值、实际 JWT 与私钥标识扫描，零命中；归档文件字节身份见 [raw-log-index.json](sp03-acceptance-20261001/raw-log-index.json)。原始工具 stdout 的尾随空格属于保留证据，不据此删除或改写失败输出。

SP-03 非虚拟化范围无未完成门禁。KubeVirt/CDI、后续业务闭环、生产 HA 与旧 v1 完整 proof 迁移不在此次通过判断内。

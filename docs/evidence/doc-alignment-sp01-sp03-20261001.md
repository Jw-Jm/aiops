# SP-01～SP-03 文档修订同步与代码复审记录

2026-10-01。本轮对应修改审核完成，未发现剩余范围内阻断问题。

用户明确授权同步 SP-01、SP-02、SP-03，并要求发现问题后自行修复、继续
复审。起点为 `c6dccf7e0c7bf5a009319e13ab0259a4914e5367`，本次修改按用户随后授权提交到本地 `main`，提交编号见 Git 记录。
没有发布、部署、改写已执行迁移或复用历史报告作为新证据。
实施边界和兼容决定见 [ADR-0017](../adr/0017-align-sp01-sp03-with-reviewed-specification.md)。
本轮未再次修改根目录 00～10 正式文档。ADR-0008 的虚拟化延期保持有效。

## 修订映射与实现

- **SP-01 Task 1.3/1.4**：OpenAPI 增加来源范围声明和实际返回模型，API Contract
  修订为 1.1.0，Go/TypeScript 类型及 SQLC 模型重新生成。请求新增字段保持
  可选，旧请求仍可创建无查询授权的注册。闭集错误枚举保留旧兼容成员，运行时
  统一输出 IDEMPOTENCY_CONFLICT。生成物检查在隔离副本中从空输出目录生成，
  与当前工作文件比较，拒绝旧、多余和缺失文件；不依赖是否已暂存，也不移走
  用户文件。覆盖失败恢复和并行读取的回归用例。
- **SP-02 core Profile**：开发模板中 KubeVirt/CDI 明确 disabled、unverified，
  原因 development_deferred。保留候选准入、矩阵、源码、历史 Fixture 和延期
  Profile；未安装、升级、启动虚拟化资源，也未设置虚拟化 PoC 开关。
- **SP-03 Task 3.1/3.2、3.3/3.4**：新增 forward-only migration 00017，以独立列
  保存 backendLogicalId/dataScopeMapping。验证六类精确 scope、标签、长度、
  空值和重复值；拒绝未知维度、通配/正则形式及控制字符。注册、重复注册比较、
  修订、回滚、列表和同事务审计均保留映射。凭据轮换保留稳定来源与后端身份。
  非空后端身份不可重新绑定，数据库 trigger 同样执行约束。旧库升级不补造权限；
  回滚到旧历史清空范围但保留已补齐的后端身份。
- **SP-03 Task 3.2**：幂等冲突错误码对齐；处理中响应添加 Retry-After。
  原有重放前当前鉴权、撤权阻断、业务事务和不重复执行约束保留并复验。
- **SP-03 Task 3.7**：Archive Put 在返回可提交引用前回读指定版本，核验归属、
  正文摘要、长度、类型和声明的保留期。回读失败保留 pending 状态；恢复复用
  已上传的不可变对象。删除同时尊重后端和引用保留期。Worker 新审计归档默认
  保留 365 天，旧对象与已签名历史不自动改写。
- **SP-03 Task 3.5/3.6、3.8/3.9**：身份、Policy、配置版本、Workload、日志和
  数据库权限共用代码复查后未发现因本轮文档修订而必须扩展的业务实现。
  当前 API 不依赖尚未实现的 Graph 来启动；核心 Keycloak/OpenBao 的依赖边界保留。

注册中的 dataScopeMapping 是声明，`queryCapability` 始终 disabled/unverified。
不能通过提交 verified、verification 或状态字段取得查询能力。真正的 Adapter
后端隔离验证及不可绕过的过滤属于 SP-04。本轮不把旧 Finding/Incident/RCA/
InvocationContext/Command 的 v1 Schema 静默替换为新必填语义；新增 Graph/Job/SSE、
reducer、预算、执行摘要、RCA CAS、Evidence 依赖和 Legal Hold 的业务合同与迁移
在其消费阶段实施，不因本次基础代码同步启动 SP-04～SP-09。

## 审核、修复与复审

第一轮相关包和 Contract 检查通过。全量检查识别到新迁移包含 Down 段，违反
仓库仅允许 Up 的规则；已移除，下轮全部通过。新增审计断言曾误用 payload
列，已按实际 record 列修正并在 PostgreSQL 上验证。

复审进一步修正三个问题：旧历史回滚不能擦除稳定后端 ID；后端较短的保留期
不能绕过引用保留期；生成检查不应临时移走输出、干扰并行构建。隔离生成最初
使用 node_modules 目录符号链接，被锁定的 pnpm 拒绝；改为复制已安装依赖并
保留内部链接，生成检查通过，无依赖版本或锁文件变更。

第三轮检查旧请求兼容、未知/空/伪造验证输入、重复注册、并发 revision、SQL
身份保护、回滚历史、同事务审计、重放撤权、处理中重试、归档损坏和回读故障
恢复。最终无未关闭的本轮发现。最后执行并行 make check 验证检查入口之间
没有生成文件消失或写入相互干扰。

## 实际验证与证据

- [最终 make -j2 check](doc-alignment-sp01-sp03-20261001/make-check-parallel-final.log)：
  通过。包括生成一致性、runtime-source、go vet、全仓库 Go 测试、Contract 与
  Web 3 项测试。Go 默认入口中跳过的外部 live 用例不据此算作通过。
- [隔离生成最终日志](doc-alignment-sp01-sp03-20261001/generated-isolated-final.log)
  与 [合同专项回归](doc-alignment-sp01-sp03-20261001/contract-alignment-final.log)：通过。
- [独立 PostgreSQL 与真实 SeaweedFS TLS/IAM 回归](doc-alignment-sp01-sp03-20261001/integration-final.jsonl)：
  18 个顶层测试通过；包含子测试共 20 项，零失败、零跳过。空库到 17、旧库
  16→17、迁移失败恢复、来源身份/修订/HTTP/旧请求、租户隔离、角色权限、
  幂等、配置、Audit pending/回读恢复和真实对象存储隔离均覆盖。
  [明确的选测列表](doc-alignment-sp01-sp03-20261001/integration-selection.txt) 未冒充全 live 验收。
- [安全回归](doc-alignment-sp01-sp03-20261001/security.jsonl)：7 个顶层测试，
  包含子测试共 11 项，通过且零跳过。
- [race 检查](doc-alignment-sp01-sp03-20261001/race-final.jsonl)：覆盖 Source、
  HTTP、Archive、Audit 和独立数据库并发修订/幂等/分段恢复。9 个顶层测试、
  包含子测试共 22 项，通过且零跳过、无数据竞争报告。
- 保留 [首轮迁移门禁失败](doc-alignment-sp01-sp03-20261001/make-check.log)、
  [隔离依赖链接失败](doc-alignment-sp01-sp03-20261001/generated-final.log) 及
  [包含跳过项的集成原始运行](doc-alignment-sp01-sp03-20261001/integration-round2.jsonl)。
  没有将失败或跳过改写为通过。

后三组最终 JSON 报告均通过仓库 check-test-report.py 的零跳过门禁。
测试数据库与对象存储均为本轮拥有的独立 Fixture，未改动现有云平台数据。

## 结论边界与运行前要求

本轮源码修改和相关回归审核完成，不重新宣称完整 SP-02 或完整 1.0 运行验收。
7 项依赖独立 Keycloak/OpenBao/Projected Token/Victoria 环境凭据的 live 测试
本轮未执行通过，详见原始集成日志；旧阶段 live 报告保持历史含义。未重新打包、
安装、干净重装或验证生产容量/HA。

部署更新后的 API/Worker 前先执行 migration 00017，并为新增审计归档的
365 天保留配置容量与密钥版本保留。旧对象需要延长时须执行受控存储保留期
扩展，不能只修改引用日期。KubeVirt/CDI 及其专属能力继续延期、未验证。

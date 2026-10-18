# 统一版本交付准备

日期：2026-10-18。状态：**准备基线已整理，尚未发行准入**。机器可读记录为 [delivery-baseline.json](delivery-baseline.json)，本地核对入口为 `make check-delivery`，复用生成物、运行/迁移工具源码材料检查并核对本基线。本文件不授权部署、升级、签名发布或恢复延期能力，不替代 Bundle 清单和真实运行回执。

## 版本集合与兼容边界

现有平台 appVersion 为 `1.0.0`，Chart 包为 `0.1.0`；公开 OpenAPI 为 `1.4.0`，内部 Graph API 为 `1.0.0`。调查 wrapper Python 包为 `0.1.0`，复用 HolmesGPT `0.42.0`；Web package 未声明独立 version。它们属于不同版本空间，不能靠统一字符串证明兼容。API/Worker 当前也没有独立可用于配对的应用包版本；最终以同一冻结提交、生成物和实际镜像 digest 组成交付集合。

- API/Worker：同一源码提交、公开 OpenAPI/内部 Graph 合同与前向迁移同批准备。新总览请求使用 `includeDomains`，新 Worker 才声明该可选字段；旧消费者保持原请求可使用旧响应形状，但**新 API 与旧 Worker 的混合运行未获证明**。不把可选字段扩展等同于任意滚动升级兼容。
- Investigator：旧调查使用 `investigation-job/v2` 和 `investigation-result/v1`；助手使用 `investigation-job/v3`、`dispatcher-payload/v2`、`assistant-tools/v2`、`assistant/v1` 与 `assistant-answer/v1`。Python 仍接受声明范围内的 v1/v2 助手目录，当前 Go 提交使用 v2；这不是对旧任意镜像的承诺。Python 输出 Schema、API 验证器、模型 Profile 引用/预算与实际 wrapper 必须同批匹配。
- Web：生成客户端来自同一公开 OpenAPI，组件/SSE/TypeScript 测试与生成物一致性分别检查。`sp08.release` 是显示配置，现有前端不以它执行独立发行验签；信任仍来自外部独立公钥验证的 Bundle、镜像和文件清单，不能填写 `signature` 就视为已验签。
- Runner：复用既有准入 Runner，不新增常驻服务；执行 Profile 的 `toolImageDigest` 必须等于 Bundle 对应镜像。旧确认、幂等 Ledger、执行状态、归档和 Post-check 的版本绑定必须保留。
- 共用处置关联：普通执行维持 `execution-request/v1`，带继续处置/回退关系的执行使用 v2；结构化后检查报告为 `command-post-check/v1`。关系纳入本人确认摘要，报告复用持久事件而无新增迁移。新字段的实际跨版本运行仍未验证，按同批 API/Worker/Web 交付；详见 [本轮功能记录](disposition-verification-closeout.md)。
- Schema/迁移：当前迁移为连续 `00001`～`00043`，最新为资源观测迁移。Schema 的各自 `$id` 见基线；生成物与 Python 输出合同继续由已有检查核对。历史记录与未完成任务不能通过变更版本字符串、删除旧列或重写迁移强行兼容。

基线检查只证明当前登记值与源码一致，不证明新旧进程、旧数据库、旧浏览器缓存或两种架构的实际兼容。当前工作区包含未提交改动，尚无最终冻结提交、新整套镜像 digest 或签名 Bundle；这些字段保持 null。

## 安装配置交付

复用 [标准 Kubernetes/containerd 安装](../runbooks/generic-kubernetes-offline-install.md) 和 [正式初始化](../runbooks/pre-sp07-current-bootstrap.md)，保持 `dependencies → bootstrap-api → business` 三阶段，不新增安装器或手工业务表 seed。交付材料分为：

- 每架构一份完整 BuildSpec：`schemaVersion`、`bundleId`、`platformVersion`、`architecture`、逐文件路径/大小/摘要、Materials 及对应镜像/Chart/源码/许可证/SBOM。由现有 `opsctl bundle build` 生成并验证 `bundle.lock.json`、分离签名和 `payload.tar.zst`；只用本地已准备、已准入物料，不替换目录中的旧摘要。
- 一份目标环境输入：namespace、context、真实 kube-system/节点 UID、Kubernetes/containerd 架构、StorageClass、来源端点和身份，经过 `profile detect/resolve` 固定。仓库模板不是可直接安装的 resolved Profile，也不是实际环境证明。
- 一份 `installation-business-values/v1`：SP04 的 Graph/来源/Lease/归档范围；SP05 的绑定与分析器；SP06 的租户、策略、预算、身份与明确模型状态；需要处置时再提供完整 SP07 Profile/主机接入/凭据/网络目标。实际值由其 Schema 和 `ReadBusinessValues` 校验；Secret 内容、DSN、私钥、Token 和恢复材料留在仓库及 Bundle 外。
- 原独立 CA、工作负载 PKI、OpenBao 恢复材料、历史 Transit key、Archive IAM/COMPLIANCE、OIDC/LoA2 与已注册租户/来源/策略。升级不重新 init OpenBao、创建首租户或覆盖旧归档；来源/资源 UID 变化须重新核对，不继承旧凭据和窗口证明。

**DEL-001：统一安装覆盖尚有缺口。** 当前业务值 Schema 根层没有 `sp08`，SP06 两个闭合分支也没有 `assistant`；installer 仍将 `components.web.enabled` 固定为 false。Chart 虽已有 Web、助手和 `faultChainEnabled` 配置，不能因此宣称正式三阶段安装已经包含这些能力。完整统一交付前须另行开发其业务输入校验、Bundle 物料配对和阶段回执绑定；本轮仅登记，不手工绕过 installer 部署。

**DEL-002：完整多架构交付未证明。** Web Dockerfile 复制固定 `ld-musl-x86_64.so.1`；不能直接用于 ARM64。API/Worker 的 TARGETARCH 或双架构编译成功不能替代 Web、Python/native 依赖和 Runner 的逐架构闭包、镜像与实际验证。现有 arm64/amd64 Catalog 继续独立使用，拒绝混合架构安装。

## 能力开关

Chart 默认 `workloadsEnabled=false`，SP04/05/06/07/08 与助手均未启用；故障链字段缺省为 false。默认值代表需要显式配置，不代表没有实现。统一交付必须记录本次启用集合以及各能力的当前来源/对象/授权/模型资格，不能以 Pod Ready 或测试 Fixture 代替能力可用性。

SP05 依赖 SP04；SP06 启用还需当前只读策略、标准 MCP、签名上下文、受限模型出口与预算。助手需 admitted 且引用匹配的模型 Profile；故障链另需显式开关和合格来源。`modelState=pending` 会停用 Investigator/模型出口并清空继承模型值，后续激活不能复用旧配置回执冒充完成。SP07 需要正式 Host Onboarding/Profile、授权及操作者本人风险确认；Post-check 自动判据仍仅覆盖合格 Node/PVC，其余 inconclusive。

KubeVirt/CDI 保持 disabled/candidate/unverified，VM/full Profile 开发延期；PyRCA excluded、网络 Trace 补全禁用。DeepFlow 实时能力按实际登记/资格报告，历史独立开发验收不自动使其进入当前 core Bundle。生产 HA/灾备、模型人工质量和容量达标分别待其阶段安排。

## 升级与回退准备

当前 installer 的阶段回执绑定原 Bundle、配置摘要、namespace/installation UID 与来源资源；不能修改回执以迁就新镜像，也不能把 SP07 首次初始化选项当作通用升级接口。现有 release 清理/保留数据重装入口可复用，但它不是不间断在线升级或自动二进制降级工具。

后续升级阶段应先固定以下可审查输入，随后另行安排实跑：

1. 记录旧签名包/工具包、配置与当前数据库版本；确认当前 Job/Step、SSE 游标、命令执行/未知副作用、审计和归档均有处置方案。停止新提交并协调排空消费者，不让未证明兼容的新旧 Worker 竞争同一队列。
2. 保存 PostgreSQL 备份/PITR 检查点和恢复验证计划，以及对象版本、Legal Hold、旧密钥/CA 和持久卷身份。恢复计划覆盖外部副作用；不能只靠数据库回退推定命令未执行。
3. 验签新包，按当前节点 UID 逐节点导入精确 digest；只由受控 migration LOGIN 执行前向增量，API/Worker 凭据不获得迁移权限。旧迁移保持原字节，历史 00038 的 Down 文本不授权执行。
4. 按已有受控 release 清理/保留数据重装机制准备阶段回执更新，再用同一新包和冻结配置完成正式三阶段激活；若要求连续滚动升级，需先补足并验证该路径，不能临时换成直接 `helm upgrade` 绕过回执。
5. 复测身份/撤权、来源/Graph、Evidence 归档、旧新任务 Ledger/SSE、助手及人工命令闭环，记录实际版本集合和失败；未知与未执行保留原状态。

回退优先使用经过验证的向前修复。只有明确证明旧程序能读写升级后 Schema、配置、已生成 Job/输出和能力集合，才可安排应用回退；目前没有此类整套兼容证明。数据库不执行 Goose Down，也不删除 `goose_db_version`。确需恢复备份时应停写并隔离消费者，协调数据库、归档/对象版本、密钥及命令副作用，另行审批和演练；单次 Helm rollback 不足以恢复整个平台。

## 当前已知限制与后续阶段输入

DEL-003：最终提交和整套制品尚未冻结；本轮只整理源代码合同与配置，未生成或签名新的发行 Bundle，未改现有准入为 qualified。DEL-004：历史联合验收 FAIL、页面/新同轮长上下文 NOT_RUN、Node/PVC 真实助手授权/对象不足、人工质量 pending 继续保留。远程超时根因未证明时不统一归因于性能；按用户要求，远程性能问题仅作记录。

当前本地检查和历史结果只覆盖各自源码/范围。下阶段输入为：先处理 DEL-001/002 的安装与架构缺口，再冻结提交、准备逐架构材料和外部签名密钥、复用现有 Bundle 验签/安装机制；实际部署、升级回退演练、发行准入仍另作阶段安排。根目录 00～10 正式文档未修改。

本轮 `make check-delivery`、本地 `make test-contract`、最终基线专项和 `git diff --check` 通过；详见 [限定范围检查记录](delivery-verification.json)。没有运行镜像构建、签名、新 Bundle 制作、实际部署/升级/回退或远程验收。结果为 `PREPARED_NOT_RELEASE_QUALIFIED`。

# 完整静态审查覆盖与验证边界

本轮已完成固定提交上当前启用的第一方功能实现的静态审查。上一版关于 HTTP、SQL、Graph/RCA、集成适配器、CLI/Bundle 以及前端 Admin/图谱组件“仍需补读”的记录已由本轮逐文件阅读补齐；不再把旧阶段状态作为当前结论。

这项完成声明针对代码审查，不是发行、真实环境验收或“保证没有其它缺陷”。接口声明、机械材料与第三方代码按下列不同方式处理。

## 文件盘点与实际台账

原始 [inventory.csv](inventory.csv) 登记主分支 19,787 个跟踪文件：第一方实现/配置 817、测试/夹具 691、生成物 532、上游实现/准入 5,506、文档/历史证据 12,193、其他材料 48。该机械分类及 inventoried_only 状态保留，不将盘点伪装成阅读。

补审将“其他材料”中的全部 48 项纳入 [file-review-ledger.csv](file-review-ledger.csv)，包括实际参与运行的配方、Bundle 目录入口、Go/Web 构建配置，避免按扩展名漏掉功能。台账共 865 项：

- **static_reviewed：689。** 已阅读实现/有效配置并结合消费者、权限、错误与生命周期推理；普通源码记录实际全文件范围。DeepFlow 大清单按完整解析后的有效 YAML/XML 阅读，省略 XML 注释，台账明确说明。
- **contract_checked：139。** 版本化 JSON Schema、公开 OpenAPI 和 Python provider Schema。按结构/引用、字段语义、消费者及生成一致性检查；136 份嵌入根合同及 definitions 实际全量离线编译。不是声称把所有重复序列化展开逐行人工读完。
- **material_checked：23。** 两架构 Catalog、许可证 registry、Chart 归档、依赖锁、历史本地 replacements、标准 LICENSE 和脚本使用说明等。做实际材料核验，未逐条重新提供法律解释或审计上游实现。
- **excluded：14。** 两份 KubeVirt/CDI 解析清单遵守 ADR-0008；十二份未启用 PyRCA 的上游/冻结夹具保留。PyRCA 第一方 adapter 已阅读，其离线 PoC 不是启用的业务链。
- **pending：0；partial：0。** 只说明台账范围内没有遗漏待处理状态；不把 865 当作全仓人工审查分母。

具体路径、范围与核验说明均可按 CSV 检查。状态是审查方式，不是测试通过或缺陷已修复。

## 已覆盖的全部第一方功能域

**身份与数据库权限。** 原生/OIDC 装配、Cookie/Origin/CSRF、登录/密码/限流、账户/角色/历史主体映射、MFA 登记/挑战/恢复码/重置、持久原生授权与旧授权分支；全部 63 个迁移文件及最终生效的权限函数、search_path、RLS、EXECUTE 归属与锁序；审计幂等和版本撤销调用。MR-014、017、020 为补审确认；没有真实 PostgreSQL 事务证明。

**人工执行。** ActionPlan、命令 AST/风险分类、Profile/策略/目标绑定、MFA/Ack/Prepare、持久 authority、Dispatch/Claim、Runner transport/SSH 证书/受控 Kubernetes Job、输出与事件、未知结果、归档、取消、后检查与执行关联。所有 action、Runner 和 HTTP 调用方已读；未执行实际命令。

**AI 问数与调查。** Catalog/QueryIntent/成本/编译/控制面 SQL/来源聚合/比较/趋势，MCP 准入/预留/结算和工具集合；v2/v3 Job、持久预算/lease/心跳/接管/取消/结果验证，Assistant 会话与绑定/指代/历史映射，故障链及资源关联 evaluator；Python worker、Holmes adapter、上下文/输出合同、脱敏、TLS 和运行配置全部第一方实现。MR-018 为补审确认；模型质量和实际来源时延未验收。

**资源与图谱。** Canonical ID、投影、Kubernetes/硬件/存储/外部网络关系、identity reconcile/snapshot/历史关系及变化、Graph 查询/分页/预算/排序、owner Lease、快照重建和 API→Worker 签名复核。选中的上游集成边界有检查，未扩大为 Ariadne/整个 Kubernetes 客户端源码审计。

**观测、Finding、Incident、Evidence、RCA。** 采集与来源配置、Kubernetes informer/官方规则、硬件/NPD/Coroot/PodMetrics、DeepFlow/Victoria/Redfish/K8sGPT、Finding polling/outbox、Incident 关联/人工变更/合并拆分/恢复、Evidence/Legal Hold/保留/清理、Archive/S3、审计链/Merkle/签名段、全部 RCA 与后检查分支和八份配方。MR-019 为补审确认；硬件校准和崩溃窗口待实际验证。

**配置、启动与交付。** API/Worker 的全部第一方装配，Profile/来源/租户/配置注册与签名发布，OpenBao/Keycloak 全实现、PKI/CRL 生命周期、Bootstrap、opsctl 全命令、Bundle 每个 stage/driver/清理保护、供应链准入和源码准备/构建/检查脚本、Helm 模板/值 Schema、镜像构建和 nginx 代理。MR-015/016 为补审确认；未执行任何安装/清理/Bootstrap/远程探针。

**前端。** web/src 全部第一方 TS/TSX：共享客户端/SSE/认证、原生安全与 MFA 页面、集群范围、Shell/路由、资源/告警/Incident、助手/故障链/趋势、命令编辑/确认/输出/后检查、全部 Admin 页面、RCA/Evidence 与图谱组件；两份样式及构建配置也已读。检查范围切换、请求归属、撤权、分页、取消、迟到回调和数据呈现，不把 jsdom 与构建当作真实浏览器视觉或可访问性验收。

## 合同与材料的实际检查

[all-contracts.log](evidence/all-contracts.log) 记录 136 份嵌入合同及其 definitions 的全量编译，采用 [Go overlay 源码](probes/contracts.go.txt)，不修改生产目录。公开 OpenAPI 有 97 个路径、109 个操作；521 个本地引用可解析。路由参数/认证/幂等/SSE 与实现已结合审查；编译通过不能证明所有实例语义或认证文档完全一致。

[material-checks.json](evidence/material-checks.json) 记录 8 份 registry 共 4,095 个固定 scope 的唯一性、摘要形状、保留 notice/ADR 的实际摘要，两架构 Catalog 的结构和本地材料，3 个上游 Chart 的目录/版本/归档身份，以及 Python/Runner/pnpm 锁的结构。Catalog 中相对上游源码归档的 notice 路径没有冒充本地已读，逐组件记录 sourceArchiveNoticePathsNotRead。未从公网取新材料，也未重新签发资格。已实际发现的当前运行时/迁移闭包漂移仍列 MR-006/007，不能被这个材料结构检查覆盖为 PASS。

## 历史问题复核

- PWR-001 的日志/比较原始查询展开预算已经进入 `SourceRequestCost`，没有照抄旧缺陷。本轮 MR-001 是统一成本校验对 Graph 当前统计造成的另一个合同冲突。
- PWR-002 的标签范围变化被误判删除已有 native/visible 分离；本轮没有把它继续列为未修复。
- PWR-006 的进程重启丢失删除知识已有 `ReconcileNativeSnapshot`；本轮未进行真实集群重启验收，不将现有实现否定为仍未开发。
- PWR-004 曾完成旧版本材料同步；本轮 MR-006/007 基于当前新增密码实现与 44–63 迁移重新生成并实测，不能借历史“已修复”判定当前通过。
- 历史报告中仍需真实硬件/BMC、离线/浏览器/模型等验收的内容，不自动转换成这轮新增代码缺陷，也不借历史证据宣称本轮已验证。

## 排除或收窄的候选

1. **“历史容器投影直接保存 env、command 等秘密”未成立。** HistoryProjection 复制容器字段前，生产路径经过 `kubernetes.Project/projectNested`，只保留 name/image；现有敏感字段 canary 测试通过。不能只看第二层投影就报告泄漏。
2. **“多集群历史关系会连到另一集群”未成立。** 后置 Scope 检查拒绝错误目标；实际问题是唯一性判定过早导致缺失，见 MR-005。
3. **“原生身份下全部 AI Job 均失败”过宽。** v3 已有持久 native source/user/version 与专用 currentAssistant；MR-002 仅针对仍走旧身份复核的 v2 Incident Job。
4. **“所有原生命令都无法执行”过宽。** namespace Profile 与没有挑战 header 的普通分支不会进入同一强验证条件；MR-003 限定高权限，以及需 step-up 的链路。
5. **“前端测试通过证明原生 SSE 已连通”不成立。** 原有测试正把 Bearer-only 拒绝作为成功断言；本轮以实际 Cookie 选项补了复现。
6. **“生成模型含密码字段即为公开泄漏”证据不足。** 类型定义本身不代表 handler 在输出该对象；MR-008 仅记录真实生成漂移。
7. **“迁移有 Down 标记即会删除生产数据”不成立。** 补审确认 00055–57 会主动抛错、00058 含 DROP FUNCTION，并非均为空段；迁移 CLI 仅前向执行，尚未观察到执行 Down；MR-007 记录合同/材料不一致，不声称发生回滚。
8. **“源码检查失败就是验证程序需要放宽”不成立。** 运行闭包确实多了实际编译文件；应更新资格与材料，不关闭锁校验。
9. **构建体积警告未升级成缺陷。** 构建有大于 500 kB 的 chunk 提示，现有页面已做部分懒加载；没有首屏/慢网测量支持真实不可用结论，本轮不为消除警告引入拆包工程。
10. **KubeVirt/CDI、Trace 和尚未授权的运行验收不列成缺失功能。** 遵守 AGENTS/ADR 的范围控制。


## 没有完成、也没有记为通过的验证

- 真实 PostgreSQL：63 个迁移从空库/已有库前滚、RLS/SECURITY DEFINER 权限、锁顺序、并发撤权、跨会话失败预算、幂等与进程恢复。
- 真实 API/Worker/浏览器：原生和 legacy 身份全链、MFA/PKCE/SSE、刷新接管、全部页面交互/键盘/视觉/可访问性。
- 真实来源与执行：Kubernetes/BMC/Redfish、模型、SSH/Ansible、DeepFlow/Victoria、OpenBao/Archive 故障与证书轮换。
- 真实交付与负载：当前 Bundle 双架构空缓存断网安装、升级/干净重装/恢复、OOM/RSS/并发/P95/持续容量。MR-009 是代码与配额推导，不是实测 OOM。
- 所有测试/夹具、生成代码展开、第三方完整实现、历史 evidence 和许可证原文的逐行审计；公网 CVE/漏洞数据库扫描和新的法律准入。

这些项目是后续验证工作，不能用“尚未搭建真实环境”否定本轮已经完成的静态代码审查，也不能用静态覆盖清零将它们算作运行验收通过。KubeVirt/CDI 仍延期，不能作为补齐审查的理由自动恢复开发。

## 候选与结束条件

尚有一个未升级为缺陷的 DeepFlow 外层时限候选，见 [candidates.md](candidates.md)：内部 allowance 与外层 deadline 不同，源码注释提示负载风险，但没有本轮可归因的实际失败测量。它保留为待验证，不报告成已证明的性能故障。

本轮静态审查的结束条件已满足：当前第一方功能文件及必要配置有记录，全部确认问题与反证/边界已落盘，修改方案覆盖全部 20 项；台账未留待补读。已确认缺陷的修复和上述真实运行验收，是下一阶段工作，不在本轮审查完成声明内。

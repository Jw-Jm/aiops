# V1.2 实施进度

## GPT远程恢复续作（2026-10-23，r13；本段优先于下方历史记录）

连接已恢复：mssc-direct仍为获准主机mssc；根盘74%/58G可用、/data 12%/1.6T可用。远程原仓库3339876e保持未改，本地feat/ui-v12-replication/9bd78d84及继承工作区保留。任务PostgreSQL17.11/15453已从54前滚至56，55/56按实际执行SQL摘要冻结；1～54未改，旧入口/镜像/凭据不覆盖。现有OpenBao TLS/Raft、Transit key与精确策略已核对，仅刷新任务JWT，真实TokenReview成功/错误SA403；无重新初始化或创建key。

r7～r11合并真实DB/HTTP集成已通过（seventh轮MFA用例467.85秒）；最终首次改密错误码修正另经真实HTTP和浏览器复验。新预览https://192.168.0.63:30467/login已启动，用户创建/首次改密/权限拒绝/退出有当前机制证据，尚无HTML像素通过。历史失败全部保留在evidence/gpt-resume-r13/。已修正：测试重放必须复用实际已消费TOTP；测试从真实会话读取userID而不解析主体前缀；首次改密按真实201合同；登录/改密审计用事务内持久主体；原生命名空间授权不要求整集群授权；管理员授权请求使用合同规定的空范围数组；密码重置审计只记录非秘密密码版本、不触碰通用秘密拦截。详细版本边界及最后浏览器脚本退出定位中断、接续完成记录见evidence/gpt-resume-r13/result.json；不把机制通过升级为整个切片通过。

r12独立安装器源码已补原生business-values/Profile/CA、Keycloak禁用与完整OCI校验后跳过缓存、原生环境引导所需信任与Secret、CLI来源核验用私有会话Cookie；定向单元/Helm/CLI通过。全bundle包保留OPA来源摘要不一致失败；首次租户CLI仍有OIDC依赖，完整冷启动未验，详见evidence/gpt-installer-r12/result.json。安装器未部署，不视为P2-05完成。

P2-02/03/04/05继续in_progress；历史业务归属读取、Job/Worker显式原生authority/user/version、并发最后管理员与管理审计故障注入、逐状态HTML像素比对、真实来源SL-049及后续模块仍未完成。原额度耗尽中断和后续断连的待办都保留于continuation-handoff.md。

## 本轮实施：SL-001收尾与共享授权层（2026-10-22，preview-r2）

**状态：P2-02 的 SL-001 六项已按实测记录（视觉通过，含登记文案差异）；P2-04 授权边界完成共享层落地并在真实浏览器验证；P2-03/P2-04 用户生命周期/P2-05/P3 未实现。**

- SL-001右侧改按`shared/platform.css`实测值重排（表单gap 18px、标签19.8px+6px、输入38px、标题27px/650、按钮43px/圆角6px），7个antd复位选择器加深到`v12.css`可赢的层级。同一远程Chrome内把冻结原型以`file://`打开逐元素测量，除刻意缩短的tag外全部元素y/x/w/h差值为0.00；截图差异21/1363300（0.00154%），只遮罩两处登记文案。证据：`evidence/preview-r2/{geometry.json,visual-diff-filled.json,product-login-filled.png}`。
- 登录状态实测：空字段校验不发请求、未知账号401常量文案、真实请求期间`aria-busy`加载态、登录后reload恢复、退出200+旧Cookie 401。新增“卸载即中止登录、迟到成功不采纳”Vitest用例。证据：`evidence/preview-r2/{login-state-*.png,auth-browser-result.json}`。
- 403根因修复落在共享层：新增`internal/authz`，原生会话只用`identity.authorize_native_role`（迁移47，48修正FOUND语义）按会话授权版本`FOR SHARE`复检，历史会话只用`platform.role_bindings`，禁止跨来源回退；迁移source/tenant/configregistry/action/audit/assistant七个调用点。迁移47误删role_grants策略的`find_session`分支，导致会话`grants: []`仍403，由迁移49前滚修复。真实浏览器`GET /api/v1/admin/clusters` 200且`platform.role_bindings`仍0行；纯管理员operator接口仍403。证据：`evidence/preview-r2/result.json`。
- 新增preview-r2（30463，独立API单元与容器，复用同一任务库/schema49），r1保留不改。运行说明见[preview-runtime.md](preview-runtime.md)。

未完成：P2-03 MFA/再次验证/恢复码；P2-04用户创建/改状态/重置/会话撤销/角色授予/历史主体映射API（因此无独立测试身份，首次改密现场复验、operator范围、MFA现场流程、入口429均待其解锁）；P2-05全栈切换；P3-01～04与SL-049。下面各段为历史轮次记录，环境与版本只对各自轮次有效。

## GPT直接续作：独立原生预览（2026-10-22）

已在mssc-direct启动正式API/Web独立入口 https://192.168.0.63:30462/login 。修复Foundation未接native路由导致的真实登录404；集成测试改走正式装配并通过真实PG。Chrome验证首次改密201、刷新恢复、退出200/旧Cookie401、新密码再登录及API进程重启后会话持久化。SL-001修为原型双栏并与既有1440×960参考比较，visual仍failed；没有重采参考图或继承全量通过。来源服务仍依赖旧授权表事务复检，原生管理员集群列表403，待P2-04/05处理，未插入旧授权绕过。P2-02继续in_progress。入口、密码私有读取、服务恢复及限制见[运行说明](preview-runtime.md)，证据见[evidence/preview-r1/result.json](evidence/preview-r1/result.json)。下面“未部署页面/浏览器未验”等旧段落只代表各自历史轮次。

更新：2026-10-22（Asia/Shanghai）。**项目未完成；G0/P2-01已验，P2-02真实DB及HTTP处理链验证已通过，正式浏览器/视觉/live未验；P1/P2-02仍in_progress，产品验收未通过。** 本文件记录真实实施，不继承原型或方案验证的通过结论。

## 历史交付记录（环境与版本仅适用于各自证据轮次）

- 分支 `feat/ui-v12-replication`，开始 HEAD `9bd78d84e212217626cdf0cfe54af84a155a9a3d`，初始工作区干净，origin/main 与本地一致。本次代码尚未提交/推送，没有创建 PR。基准见 [baseline-code.json](evidence/baseline-code.json)。保留原始五个清单/基准文件在 original-plan/，方案正文可从此 HEAD 恢复。
- P0-01 完成代码、设计与实际运行版本的分离记录。远程仓库在 `3339876e`，运行实例使用独立镜像 digest；现存统一开发数据库为 PostgreSQL 17.11、当前迁移 43。只读依据见 [运行记录](evidence/remote-environment.txt)、[当前数据库元数据](evidence/shared-db-readonly-current.txt)。没有把现存实例更新为本分支。
- P0-03 的四个版本化 Schema 与 r4 合同测试已完成：55 正例/87 反例通过，C-017 密码重置已定义；9 项新增操作同步为 C-168～C-176。
- P1-01 已落地紫色设计令牌、238px 侧栏/70px 顶栏、页面头/卡片/状态/表格/表单字段/抽屉/确认与嵌套浮层层；portal 使用独立 `v12-layer-root` 皮肤，context API 稳定，焦点恢复/顶层关闭/长内容底部可达有定向测试；已接入 AppShell、PageHeading、Status、Findings/Resources。
- P1-02 已按固定源 NAV 恢复 7 个可折叠模块（知识管理含 cases/runbooks），manifest 22 路由通过 `/knowledge`→cases 映射，不硬凑链接数；导航保留运行/配置 clusterUid，URL/history 与授权撤销走统一 generation/cancel/草稿确认，切换清空 canonicalId/conversation/incident/cursor 等旧对象；配置集群从管理员 `getClusters` 独立读取，来源按注册集群全量分页过滤，策略/执行/部署明确租户级；纯 admin 不显示 operator 菜单，operator 保留 `/capabilities` 路由。
- P0-04 的任务专属 Fixture 已在远程 r2 自测 46/46 通过，历史读取失败修复；证据为 [r2 JSON](evidence/ui-v12-fixture-run-r2.json) 与 [r2 日志](evidence/fixture-run-r2.log)，r1 失败证据保留。
- P0-02 初审后已按“开窗/预取/提交/确认”重做 458 状态的绑定：157 个动作按阶段绑定，`confirm-yes` 改为 10 条逐片 mutation，54 个可见表单（含 8 个只在 snapshot DOM 出现的表单）都有提交绑定，并补 544 个 form 外控件（cluster-picker、graph-root/depth、rca-version、session-search、riskAcknowledged、stepup 等），见 [act-bindings.csv](evidence/act-bindings.csv)、[form-bindings.csv](evidence/form-bindings.csv)、[confirm-bindings.csv](evidence/confirm-bindings.csv)、[form-inventory.csv](evidence/form-inventory.csv)、[state-bindings.csv](evidence/state-bindings.csv)。P0-02 已在 G0 范围关闭（R7 独立审核签核）。
- 原方案 138 项方法/路径基准保留在 original-plan/。逐动作核实补录 28 项 OpenAPI 已存在但原清单遗漏的操作（C-140～C-167），并保留 C-139 为执行配置版本退役，不替换成通用 registry。当前清单 177 项：91 项已有、86 项拟新增。[合同审查](evidence/contract-audit.csv)、[表单摘要](evidence/form-contracts.csv)。
- 正式请求层已修复身份/授权切换后的迟到成功响应：退出、换账号、撤权时中断旧请求及 SSE，丢弃缓冲的旧事件和旧成功结果。同一凭据刷新保留有效请求及 tenant，调用方取消继续有效。复用既有 client/SSE，没有新增依赖或更改后端合同。改动触及所有 requestOptions/checked 调用方和三个流式入口，已做相应前端回归。
- 远程 Linux Chrome 156 在 1440×960、DPR=1、zh-CN、Asia/Shanghai 下，从固定原型生成 **458 张 viewport 参考图、736 张总截图**。额外保存长页和独立内部滚动底部，没有遮罩。Noto Sans/Noto Sans CJK SC 字体回退已记录。长页补图可能不含滚动条沟槽，主参考强制校验 1440×960；没有缩放缩略图。[采集结果](evidence/reference-capture.json)、[三轮差异及修复](evidence/reference-rounds.json)。抽查 SL-049 加载态及 SL-106 嵌套弹窗；这不是正式产品像素比较。

## P0-02 核实结论（初审后重新打开）

核实方式：逐条读取 458 份 `spec.json`/`SPEC.md`/`recipe.js`/`replay.html`，并对每份 `index.html` 的实际 DOM 提取当前可见表单、字段和 `form=` 提交按钮（排除隐藏 login 底板）；动作阶段从原型分派实际调用的函数读取，再沿平台实际路由、处理器、服务、SQL/Worker 与 Web 调用方核对。所有结论来自源码位置，不以“路径在 OpenAPI 中存在”或“文件存在”签核，也不把 recipe 候选复用当作完整逐动作签核。

- 458 个状态共 3888 个绑定面：2909 个动作实例 + 435 次表单提交实例 + 544 个 form 外控件实例（54 种可见表单、371 条逐片表单记录、8 类非表单控件；recipe 只覆盖 46 种表单）。
- 阶段裁决：`open=533`、`prefetch=639`、`submit=571`、`confirm=46`、`local=2099`。
- 动作面裁决：`local=2616`、`direct=255`、`adapt=693`、`missing=324`。
- 157 个按阶段绑定的动作：`open=59`、`prefetch=32`、`submit=11`、`confirm=12`、`local=43`；`confirm-yes` 不设通用行，改由 10 条逐片确认绑定（4 条 direct、6 条 missing）。
- 54 个表单：`local=1`、`direct=3`、`adapt=19`、`missing=31`；其中 8 种只在 snapshot DOM 出现，不在 recipe 提交步骤中。
- 458 个状态的主读取：`direct=355`、`adapt=26`、`missing=77`；带对话框表单的切片按上下文读取覆盖 route 推断：SL-106 主读取父 Finding C-030、关联候选 Incident C-031，关联写入 C-039；SL-001/002 为未认证 C-009，SL-009 为图读取 C-004。
- 跨模块/叠层复合状态 62 个：31 个由实际 active dialog/drawer 叠层判定（SL-106 modal+drawer 等），31 个由跨模块入口判定（资源抽屉、告警抽屉、AI 入口、服务列表等）。逐片记录在 `state-bindings.csv` 的 `composite`、`active_overlays` 与 `surfaces`，不按 `spec.depth` 臆断。

阶段口径（初审修正）：

- `open`：只打开对话框/抽屉/确认框，不产生平台请求；提交绑定在对应 `form:` 或 `confirm-bindings.csv`。
- `prefetch`：打开或切换时读取列表/详情/候选数据；列出具体 GET，不以 route 代替。
- `submit`：真正发出 POST/PATCH/PUT/DELETE 的提交动作；命令创建只在 `confirm-yes`，不在 `command-review`。
- `confirm`：只打开确认框，复用已有 assessment/字段/revision；具体 mutation 逐片绑定。
- `local`：客户端状态、导航、剪贴板、对已授权已加载数据的导出；不因此放宽权限。

裁决口径：

- `direct`：现有请求的方法、字段、状态和错误语义已覆盖，只差正式前端接线。
- `adapt`：现有请求存在，但字段、状态、分页、投影或权限语义需要映射或扩展。
- `missing`：平台没有对应公开合同或处理器，需按主任务新增并持久化，不允许用前端演示数据或定时器替代。

初审修正的具体点：

1. `ai-resource`、`ai-incident`、`observe-ai`、`session-create` 只调用 `newSessionDialog` 打开 `session-form`，不在这里 POST 创建会话；创建在 `session-form` 提交，首条消息在 `composer` 提交。
2. `binding-create` 只打开 `binding-form`；`cluster-create` 只打开 `cluster-wizard-form`；`command-create` 只打开 `command-form`；`incident-state` 只打开 `transition-form`。POST 均移到对应表单提交行。
3. `command-review` 只打开确认框，复用已有 assessment/ack/step-up，不再执行 risk-assess→ack→create 三连；命令创建在 SL-071 的 `confirm-yes`，绑定 C-057、request digest、profile 版本与目标。
4. `confirm-yes` 不再笼统记 local：SL-071/297/306/430 分别绑定执行创建、会话归档、执行取消、调查停止；SL-165/166/176/348/371/426 绑定知识发布/归档、模型停用、规则停用、会话撤销、巡检取消，均为 missing 并写清主任务。
5. DOM 表单补充：除 recipe 的 46 种外，补 `v11-user-filter`、`model-filter`、`evidence-filter`、`session-rename-form`、`binding-filter`、`v11-monitor-form`、`knowledge-form`、`source-filter` 8 种。SL-106 的 `v11-alert-link-form` 由 `v11-alert-link` 从 `alertDrawer` 打开，字段 incident/reason/review，写 C-039（missing），候选读取 C-031；其 surfaces 现含 `form:v11-alert-link-form`，gap 明示 C-039 missing。
6. 第二轮阶段复核：`session-rename` 只开 `session-rename-form`；`session-archive` 只开确认框，归档在 SL-297 的 `confirm-yes`；`native-change-password` 只开 `native-password-form`；`session-scope` 只开只读范围/模型抽屉，不提交 rebind；`observe-log-bucket` 只过滤已加载样例，`observe-device` 只改选择并回 idle；`query-source` 只展示已有查询检查，不重新 POST evidence 查询。
7. `policy-validate` 不再映射发布：本地结构预检保留，服务端 validation-only 合同为 C-177（拟新增，`POST /api/v1/admin/policy-bundles:validate`），不签名、不写版本、不激活；真实发布仍由 `policy-publish-form` 的 C-156 执行。
8. R5 登录修正：SL-001/002 主读取改为 `POST /api/v1/auth/login`（C-009 missing），只含可见 `login-form`（username/password→loginName/password），不读 overview；SL-005 首次改密主读取 C-011。
9. R5 拓扑/非表单修正：SL-009 主读取改为 C-004 neighbors，新增 `graph-root`/`graph-depth` change 绑定；全部 458 片补 544 个 form 外控件（cluster-picker/config-cluster/session-search/riskAcknowledged/stepup 等），按可见 DOM 与 change/input 语义绑定，cluster-picker 仅本地切范围后重读。
10. R5 复合读取：SL-106 保留父 Finding C-030、候选 Incident C-031 与写 C-039，不压成单条 primary。
11. R6 复用改密表单：native-password-form 七片按 self/first/reset 绑定；self/first→C-011 passwordChangeRequest，SL-005 first 缺 currentPassword 登记补充状态 P2-02；SL-094/370 reset→C-017 adminPasswordResetRequest（userId 路径、initialPassword、双 revision），confirmPassword/mustChange 不进 body。
12. R6 表单请求：model-form 区分 create replace/update keep；v11-user-form create C-015 与 update C-016 分列，update 不发送密码；binding-form 改为 C-020 的 userId/scope/expectedAuthorizationVersion/reason，旧 subject handler 仅作待适配复用。


## 重要发现

1. **证据查询不是日志工作台合同。** `POST /api/v1/evidence:query` 是同步、单次、有界查询：接收 `resourceCanonicalId`、`type`、`queryTemplate`、绝对 `timeRange{from,to}`、`budget`、`sourceRegistrationId`、`namespace`、`limit`；没有查询 ID、异步任务、取消、个人历史、服务端时间分桶、关键词或级别过滤。结果只给 evidence 列表与 `partial/freshness/degradedSources/warnings/queryHash`，不回显生效来源 revision、资格、覆盖和窗口。因此 SL-049 的提交、历史、取消、分桶、级别筛选分别为 `adapt/missing`，不能声称日志工作台合同已具备。
2. **告警状态时间线没有投影。** `GET /findings/{id}` 返回 finding 记录（含 `sourceRevision` 与发生/恢复时间），但 `finding.timeline` 未暴露；告警详情“状态时间线”仍是 `adapt`（P4-03）。
3. **执行链路缺全局列表和 operator 配置投影。** `/command-executions` 只有 POST 与单条 GET；全局列表仍为 `missing`（P6-01）。执行配置列表只允许 `platform_admin`，普通 operator 无法选择 `executionProfileId/version`，命令创建对话框必须补受控能力投影（P6-01）。
4. **调查创建字段远少于原型。** 现有 POST 只接收 `triggerRevision` 与 `triggerKind=manual`，预算/工具目录由服务端策略决定；原型的步骤、超时、调查重点和模型选择不能提交（P4-05）。
5. **AI 会话没有改名，范围重绑不含模型。** 归档是独立 POST，重命名仍为 `missing`（P5-02）；`binding` 只接收 namespace/资源/窗口，模型由平台准入 Profile 固定（P5-03）。停止、断流与固定引用已有合同。
6. **策略只能“发布即校验”。** 没有只校验不发布的合同；`policy-validate` 为 `adapt`，发布路径会真实编译和签名（P7-06）。执行配置表单缺少服务端必填的 `schemaVersion/version/clusterUid/tools/principal`，`label` 与 `name` 语义不同（P7-06）。
7. **接入与组件能力仍为空。** 来源登记/更新/轮换存在，但没有连接测试；组件只读检查、缺口方案、受控部署、部署历史均无合同（P7-01/P7-02）。核对已有组件不自动授予凭据或数据资格。
8. **知识、模型、通知、巡检与用户管理没有公开合同。** 分别归 P7-04/P7-05、P5-03、P7-03、P6-03/P6-04、P2-04；这些状态的读取和写入均不能按现有 API 接线。
9. **身份关联的实际边界。** 资源规范标识已嵌入来源稳定 UID；`namespace UID` 未建模，不能作为查询键。`sourceRevision` 是每条 evidence 的字段，不是查询入参或响应回显。查询窗口是绝对半开 `from/to`，没有时区回显。资源/证据读取使用 tenant 校验、operator 角色绑定、namespace/资源授权、scope digest 与 graph owner epoch/revision 复核；平台管理员不自动获得运维数据权限。
10. **合同清单原先少记 28 项现有操作。** 例如会话详情、消息分页、turn、来源步骤、故障链上下文/调查、资源解释/解析、行动计划、法律保留、策略发布、来源回滚、RCA 当前/历史、事件合并/拆分等。它们已补为 C-140～C-167，避免把已有能力误标为拟新增。

## 验证结果与适用范围

- P1 R3 修复后：`pnpm test`：6 项 Node SSE 合同 + 28 个 Vitest 文件、113 项通过（含真实 AppShell+Assistant 路由阻断、成功包络正对照、create body 和 SSE abort），[日志](evidence/web-tests-p1-r4.log)。jsdom 输出已有 canvas getContext 未实现提示，不能据此声称真实图形渲染已验收。
- P1 R3 修复后：`pnpm build`：TypeScript 与 Vite 构建通过，[日志](evidence/web-build-p1-r4.log)。已有大 chunk 提示未在本任务做无关拆包。
- 修复前独立审核保留：88 项 Vitest 通过记录见 [web-tests.log](evidence/web-tests.log)、[web-build.log](evidence/web-build.log)，复审意见见 [codex-p1-review-r1.json](evidence/codex-p1-review-r1.json)。
- 参考图采集：r1 的 DOM/浮层顺序断言错误已修复；r2 全部采集成功，但全页图尺寸不应替代固定 viewport；r3 全部通过 viewport 像素尺寸自检。三轮原始输出都保留，不将错误抹成首次成功。
- `python3 docs/plans/ui-v12-replication-20261021/validate-progress.py`：核对 458 状态、157 个阶段动作、10 条逐片确认、54 种表单（含 8 种 DOM-only）、62 个复合状态、177 合同引用与六项未验证状态，[结果](evidence/progress-validation.json)。`g0_passed=true`（G0 准备阶段独立签核）；产品六项仍 not_verified，属于后续切片验收。
- `go test ./test/contract -run 'TestUIV12' -count=1`：4 个 Schema、55 正例/87 反例通过；`go test ./test/integration -run '^TestUIV12FixtureDSNGuard$'`：通过。
- `GOOS=linux GOARCH=amd64 go test -c ./test/integration`：生成 Linux 二进制；远程 r2 自测 46/46 通过（见上）。
- `validate-plan.py` 与 `validation.json` 已恢复为开始时 HEAD 内容，本轮不运行、不改造；它们只用于实施前基准，旧证据保持 `checks_passed=4904`。当前扩展校验全部由 `validate-progress.py` 及其日志承担。
- `git -c core.whitespace=cr-at-eol diff --check` 通过；CSV 保留原 UTF-8 BOM/CRLF；validate-progress.py 还会重新解析 458 份 index.html，断言 form-inventory.csv 未漂移。源码版本及工作树文件摘要见 [tested-source.json](evidence/tested-source.json)。

没有执行业务数据库迁移、原生账号切换、真实模型/查询/执行/巡检/通知联调或产品视觉比较。上述单元、构建、原型参考采集与静态绑定均不能证明这些业务通过。

## 当前未完成与阻断范围

完整 39 项状态见 [task-progress.csv](task-progress.csv)，具体裁决见 [p0-decisions.md](p0-decisions.md)。P0-01～P0-04 均已按 [G0 独立批准](evidence/g0-independent-approval.json) 完成准备阶段；P1-01/P1-02 已开始实施，P1-03 仍受 P2 原生身份依赖，P1-04 保留参考图与远程视觉待办；P2 未启动。

1. **P0-02 已关闭（G0 范围）**：458 状态、阶段/confirm/54 种可见表单/544 个 form 外控件/62 复合状态已完成，R7 独立审核通过；仍不等于接口运行、权限或持久化验收。
2. **P0-03 已关闭（G0 范围）**：四个版本化 Schema 与 r4 测试实测 **55 正例/87 反例**通过；R6 已按实际 Schema 修正 C-051/052、C-170、C-173、C-175 和登录/模型/用户/改密/角色表单投影，R7 独立审核通过。C-017 密码重置已定义，9 项新增操作同步为 C-168～C-176。Schema 未进入 OpenAPI/客户端/实现。
3. **P0-04**：任务专属数据库 Fixture 已在远程 r2 自测通过 46/46 检查，历史读取失败已修复；2 集群/3 来源/6 实体/3 绑定/2 会话/9 审计、第二次 seed 不重复。证据见 [p0-environment.md](p0-environment.md)、[r2 JSON](evidence/ui-v12-fixture-run-r2.json)、[r2 日志](evidence/fixture-run-r2.log)。原生用户/登录/主体映射仍属 P2；G0 不因此关闭。
4. **G0 准备阶段已通过，产品验收未通过**：[G0 独立批准](evidence/g0-independent-approval.json) 绑定 2026-10-21 工作树，确认映射/合同/环境准备；它只允许进入 P1/P2，不表示产品六项通过。458 状态六项仍全部 `not_verified`。
5. **P1-01/P1-02 实施中**：已落地紫色设计令牌/公共控件、固定源折叠导航、规范路由与旧链接映射、运行/配置集群分离与切换清理；正式页面视觉对比、P1-04 状态定位及 P2 原生身份仍未完成。
6. 远程当前事实（2026-10-21 r2）：根分区 74%、约 59G 可用；`/data` 约 1.6T、11% 使用。任务环境为 `/data/ops-ui-v12-20261021-r2`（独立 PG 17.11、迁移 43、回环 15449）；`/data/ops-ui-v12-20261021-r1` 仅保留历史参考图/浏览器材料。不删除旧目录、镜像或变更 Docker/Kubernetes 共享配置。

下一步继续 P1-01/P1-02 的页面接入与远程视觉核对，按依赖推进 P1-03/P2；SL-049 日志链路依赖 P3 新查询合同。未恢复虚拟化、完整 Trace 或生产发布，也未改根目录 00～10 正式文档。

## 运行与制品位置

本地只编辑/编译/单元/合同验证；业务浏览器、数据库、集群和模型运行仍只在获准开发机。`ssh mssc-direct` 已验证指向同时具有 10.10.10.2 和 192.168.0.63 的同一主机。

参考 PNG 保存在本地忽略目录 `platform/artifacts/test-reports/ui-v12-reference-r3/`，可直接查看；其完整清单在 evidence/reference-capture.json。远程固定参考包在 `/data/ops-ui-v12-20261021-r1/ops-platform-v1.2-20261020/`。本任务的 HTTP/Chrome 进程和 SSH 转发已停止，[清理记录](evidence/reference-cleanup.txt)，共享服务未改变。

重采参考图需在远程重启专属回环 HTTP/Chrome、转发 CDP，并使用新的输出目录（不覆盖旧轮次）。浏览器 profile 和日志仍放在 /data；HTTP 仅绑定 127.0.0.1:18775，CDP 仅绑定 127.0.0.1:18776。驱动命令在本地 web/ 执行：

```sh
node tests/v12/capture-reference.mjs \
  --cdp http://127.0.0.1:18776 \
  --base http://127.0.0.1:18775/ \
  --design ../../ui-handoff/ops-platform-v1.2-20261020 \
  --output ../artifacts/test-reports/ui-v12-reference-r4
```

驱动强制核对固定源 SHA、远程 Linux、DPR、viewport 和每张主图的 PNG 像素尺寸。它不运行产品、不宣告六项验收通过。仅查看已有截图无需重启服务。该批尚无数据库变更或原生账号写入，不涉及身份回滚；以后产生原生写入后须遵循方案的兼容版本/前滚边界。

## Codex 回调独立复核 r5

合同包与 Fixture guard 独立测试通过；同源码独立交叉编译的 Linux 二进制在 mssc-direct 任务专属库复验 46/46 通过，保留 r1 失败及 r2 自测。P0-04 数据准备结论可保留。P0-02 的登录、拓扑/非 form 控件、SL-106 复合读取已按 R5 修正并由 R7 签核；P0-03 的合同 CSV/表单字段已按已测 Schema 同步。详见 evidence/codex-callback-review-r5.json 与 r7；G0 准备阶段已批准，产品六项未验证属于后续产品验收。上文旧环境与完成描述已按 2026-10-21 r2 事实同步；历史 r1 目录仅作参考材料保留。

## Codex 回调复核 r6

登录/拓扑/form外控件和指定Schema字段增量修复已核实，合同测试及结构检查通过。复用native-password-form的self/first/reset仍共用C-011，管理员reset应为C-017；模型/用户submit_request与原生授权表单仍混用旧字段，P0-02/03保持打开。精确清单见evidence/codex-callback-review-r6.json。Fixture未变，不重复远程运行；G0未签核，产品六项未验证。

## 当前结论：Codex r7 G0 独立签核

2026-10-21：P0-02/03 本轮增量返修已独立复核，P0-01/04 既有准备证据保留；G0 按实施方案的映射、依赖、已有/拟新增分类、版本合同、账号回退与补充安全状态条件通过。证据为 evidence/codex-callback-review-r7.json、evidence/g0-independent-approval.json，签核关联未提交工作树的具体清单摘要。上文逐轮“未通过/重开”是保留的历史结论，以本节和 task-progress.csv 为当前状态。

继续按 tasks.csv 进入 P1-01 公共设计令牌/控件，再依赖推进 P1-02 导航与集群上下文；P2 原生认证及 SL-049 全栈样板仍未实现。本签核不代表产品通过：458 状态六项仍为 not_verified；原生身份/CSRF/MFA/历史映射、实际来源资格、模型运行与产品视觉比较均留后续真实验证。参考图与独立 Fixture r3 46/46 不重做。

## Codex P1 独立审核 r1

实际公共控件与shell已接入，独立pnpm test通过6合同+92 Vitest，TypeScript/Vite构建通过。仍有运行集群导航/URL/助手流失效、配置集群真实范围、导航层级/角色、portal样式和产品不可用文案问题；详见evidence/codex-p1-review-r1.json。P1-01/P1-02保持in_progress，产品六项未验证；G0准备签核保留。

P1 R1 返修结果（2026-10-21）：运行/配置集群切换已走统一 generation/cancel/草稿确认并清空对象参数，导航保留 clusterUid，助手 scopeKey/SSE 与异步 mutation 按作用域中止；配置集群改为管理员 getClusters 全分页候选，来源按注册集群全分页过滤，策略/执行/部署明确租户级；导航恢复固定源折叠层级与角色隔离，portal 皮肤/焦点/长内容与不可用产品文案已修。完整 `pnpm test` 25 文件/100 项通过，构建通过；P1-01/P1-02 仍 in_progress，视觉/live 未验证。

P1 R2 返修结果（2026-10-21）：助手首次/同 scope incident/canonicalId 深链保持排障模式，不再被 mount 重置；URL/history 与手动 selector 分离，带新对象的合法跳转保留完整位置，dirty 取消恢复原提交位置与草稿，确认才失效旧范围；Assistant create/send/resolve 在异步结果后先校验 scope/signal/unmount 再本地提交，卸载也会中止 controller。初始 unknown UID、撤销 A 且仍授权 B 时最终无静默回退。真实 AssistantPage/URL 边界新增 8 项回归，完整 `pnpm test` 26 文件/108 项通过，构建通过。

P1 R3 返修结果（2026-10-21）：Assistant 集成测试改用生成合同成功包络并增加正对照；跨 scope/unmount 迟到 create 不提交；AppShell 用 React Router `useBlocker` 在提交前阻断 dirty 跨集群导航，真实 AssistantPage 保持 pathname/search/hash/对象/草稿，确认才切换并保留新 hash；未授权 URL 在子请求前门控；同 scope 新 incident/canonicalId 深链按实际 create body 验证 mode/domain/cluster/object；真实 hook 受控 stream 验证旧 SSE abort。完整 `pnpm test` 28 文件/113 项通过，构建通过。

## Codex P1 独立审核 r2

独立全web复验6合同+100 Vitest与构建通过；导航层级、角色路由、配置候选/来源分页及portal修正认可。助手初始化mode清理、URL绑定对象/取消回滚与异步局部提交仍需修正，详见evidence/codex-p1-review-r2.json。P1仍in_progress，远程视觉/live未验证。不核验CodeBuddy模型设置。

## Codex P1 独立审核 r3

20项定向用例与构建通过；真实成功包络/SSE未覆盖，AppShell切换会卸载草稿页、完整位置回滚未包括pathname/hash，同scope新排障深链仍需核对。详见evidence/codex-p1-review-r3.json。保留失败与既有通过记录，P1仍in_progress，不将测试通过扩大为功能验收。

## Codex P1 独立审核 r4：增量功能修正接收

21项真实Shell/Assistant/Layer定向检查与正式构建通过；useBlocker阻断、有效成功包络与正对照、旧SSE信号中止和同scope排障深链修正已核实。P1-01/02保持in_progress，正式视觉/live验收未完成，P1-04仍待状态定位/比较。

## Codex P2-01 独立审核 r1

身份与CLI定向单元通过；静态审核发现外键顺序、rolsuper拼写、NOINHERIT职责角色/USAGE、登录前FORCE RLS上下文、跨tenant关联FK及DB测试防误连问题，见`evidence/codex-p2-review-r1.json`。OPA source-lock失败归因保持unresolved，未更改无关锁。

## Codex P2-01 独立审核 r2

初始化顺序/职责校验修正认可；指出复合FK仍为单列引用、preauth查找别名错误，见`evidence/codex-p2-review-r2.json`。随后只允许新建任务专属PG实例核验。

## P2-01 代码交付（2026-10-21）

新增前滚迁移44、`internal/identity`、`opsctl bootstrap native-administrator`与默认跳过的数据库边界测试；早期交付未运行DB，未切OIDC，未碰共享库。

## P2-01 静态审核返修（2026-10-21）

按r1/r2修正：tenant advisory lock后按accounts → credentials → platform_admin grant → initialization marker → audit顺序写入；CLI/事务使用真实NOINHERIT migration LOGIN并`SET LOCAL ROLE migration_role`核验；pre-auth函数用重置后的精确GUC保留FORCE RLS；六个account子表使用复合`(tenant_id,user_id)` FK；DB测试增加任务专属guard、NOINHERIT LOGIN、跨租户FK/RLS、GUC spoof、API/Worker拒绝和持久审计检查。OPA source-lock失败归因保持unresolved。

## P2-01 实际隔离 DB 验证（2026-10-21）

## Codex P2-01 独立验收 r3（2026-10-22）

Codex在全新15451/PG17.11实例从空库迁移1..44，真实NativeIdentity测试通过，独立CLI首运行成功/重复拒绝；证据见`evidence/codex-p2-review-r3.json`与`evidence/codex-native-r2/`。验收范围仅P2-01数据/初始化，不代表P2-02或产品验收。


## P2-02 代码交付（2026-10-22）

新增前滚迁移45（未执行）、native session repository/auth middleware/HTTP handlers、C-007/C-009/C-010/C-011 OpenAPI与生成客户端，以及正式Web登录/首改密/退出接入。native模式要求固定部署租户、server session cookie、CSRF/Origin、DB版本/状态复核和known/unknown登录围栏；first_password_only没有业务grants且只能读session/改密/退出。OIDC兼容模式保留，workload mTLS链不变。本轮只完成Go单元/合同、Web TestingLibrary、TypeScript/build与Linux交叉编译；未运行迁移45、HTTP server或真实登录。详见[p2-native-auth.md](p2-native-auth.md)。P2-02保持in_progress，产品六项仍not_verified。

## P2-02 集中返修 r1（2026-10-22）

## P2-02 集中返修 r2（2026-10-22）

按 `codex-p2-http-review-r2.json` 处理 B1-B9：安装登录入口默认值并连接真实改密输入；cookie 派生 CSRF 写入 middleware；主动登录清理不再触发 expired/generation 自失效；cluster resolver 走 tenant RLS 事务且 operator/admin 范围分离，Web 使用服务端 registered internal ID；SQL known-failure 限定列并用 account FOR UPDATE 串行化，改密增加入口准入；session 创建/改密的事务内完成投影与 administrator_state，`find_active_session` 校验 issued session version；兼容既有 SP08 OIDC runtime-config，`/login` 门控并支持安全深链/角色 home；administrator_state 返回真实计数并补 REVOKE PUBLIC。新增真实 AuthProvider/Login/generated fetch 测试、B4 迟到测试、operator/admin scope 与投影回滚集成测试代码。本地 Go/合同、31 个 Web 文件/129 项、构建、Linux 编译通过；迁移45/HTTP/真实登录仍未执行。P2-02 继续 in_progress。


按 `codex-p2-http-review-r1.json` 修正：cookie 改为先写 Set-Cookie 再写 body；GET session 稳定派生并恢复 CSRF，改密/退出统一请求头；迁移45改用单行入口围栏、精确 RLS 操作、revoke/change `RETURN NEXT`、`issued_session_version`、绝对到期边界封顶、旧 session 事务内复检、SQL 内 known 失败阈值和原子审计；operator 集群 UID 经 DB resolver 转真实 ID，先聚合独立角色 scope，再处理 namespace（单 cluster 多 namespace 全保留，跨 cluster 歧义 fail-closed）；AuthProvider 配置门控、native expired/forbidden、logout 前置 generation、迟到 refresh 拒绝、同 session refresh 不误清缓存、first_password_only 空 grants；request context 保留取消与值。新增真实 AuthProvider/生成请求测试和真实 HTTP/DB 链集成测试代码。本地 Go/合同、30 个 Web 文件/123 项、构建、Linux 交叉编译通过；未执行迁移45/HTTP/真实登录。P2-02 继续 in_progress。

## Codex P2-02 独立审核 r1（2026-10-22）

Go四包、Web三文件18项与正式构建独立通过；实际代码审核发现Cookie写入顺序、恢复CSRF/改密请求头、迁移45的RLS/结果行/版本/TTL、事务审计/旧session复检、并发限流、授权投影和身份生命周期问题，见evidence/codex-p2-http-review-r1.json。迁移45尚未执行，不得切换运行或判定P2-02通过；按新任务264ba468-737c-4111-b2ff-93b686aff578集中返修。P2-01已验数据/初始化保持原范围；产品六项仍未验证。原Stop通知未触发，已使用bridge人工恢复一次并成功notified；不核验CodeBuddy模型。

## Codex P2-02 独立审核 r2（2026-10-22）

Go五包（含合同）、6项SSE合同+30文件123项Web及构建独立通过，迁移44未变。Cookie顺序/SQL结果行/部分RLS、事务审计、context/Origin和first-only修正认可；仍有入口默认值、改密参数、恢复CSRF、登录generation自失效、注册集群RLS/角色范围、SQL限流并发、提交后投影、旧OIDC配置及管理员事实问题。见evidence/codex-p2-http-review-r2.json，集中返修任务766b08fa-17fd-45ca-af7a-564aa3f83fd9。生成客户端仍被mock且未覆盖真实nativeLogin，默认skip的DB测试不代表运行通过。P2-02保持in_progress；未运行45/HTTP/浏览器，产品六项仍未验。本轮自动回调正常。

## GPT直接修正与真实数据库验证（2026-10-22）

按用户最新要求，本轮由本对话GPT直接修改，未发送新CodeBuddy任务。修正766b08fa回报中仍漏接的返回列/sessionVersion/限流边界，并通过真实复跑发现及修复窗口过期锁、清理失败计数RLS与吞错；45首次执行后冻结，新增前滚46。新实例`/data/ops-ui-v12-http-20261022-r1`、回环15452、PG17.11从空库迁移1..45后前滚46。最终NativeHTTPSessionFlow（含角色/租户范围、审计失败回滚、绝对到期子测试）及NativeIdentityRepositoryAndRoles通过。Web退出立即取消旧请求，晚到退出不清除新身份；登录未知/锁定同401，同源/CSRF/请求ID边界已复验。131项Vitest+6SSE、Go单元/合同和构建通过。失败和版本绑定见[evidence/codex-p2-direct-r1.json](evidence/codex-p2-direct-r1.json)。

验收范围仅真实DB+HTTP处理链与本地Web机制；未启动常驻native API/Web、未进行正式浏览器/HTML像素/live验收，未部署新的可登录V1.2页面。P2-02保持in_progress，全部切片六项状态保持未验证。旧30451/15449/15450/15451及共享环境未改；44与既有验收源码一致。

## Codex 集中返修 r3：R1～R4 关闭与 P2-03 MFA/再次验证（2026-10-22，preview-r3 独立实例）

**状态：R1（会话绑定授权）、R2（审计授权来源分派与摘要）、R3（退出撤销未确认可见且可受控重试）、R4（浏览器关键结果断言与分母纠正）已实现并在真实 PostgreSQL/真实浏览器验证；P2-03 的 TOTP 设置/确认/恢复码/管理员重置与真实两因素 step-up 已交付；P2-04 用户生命周期 API、P2-05 全栈切换、P3 仍未交付。**

- R1：新增前滚迁移 50 `identity.authorize_native_session_role(...)`（绑定确切 session、租户、用户、会话签发的凭据/授权/会话版本、账户 active、非首次改密态、会话未撤销且在空闲与绝对期限内、租户有效、授权版本一致的角色授予；锁序 account→session→grant `FOR SHARE`；旧的未绑定 `identity.authorize_native_role` 被删除）。真实实例验证：撤销会话、凭据版本轮换、账户停用、会话过期、首次改密态全部被拒且写事务无残留；并发场景下先取共享锁的授权线性化在前、并发撤销随后生效，之后授权必败。证据：`evidence/mfa-r3/session-binding-integration.log`。
- R2：审计域授权改为可信来源分派（native 只走 `identity.authorize_native_session_role` 并记录来源/用户/版本，历史绑定只走旧围栏且不得跨来源回退）；`AuthorizationRevision` 改为绑定租户 revision+subject+真实 native 授权版本+生效授予集合，撤权再授予后摘要必然变化；Worker/Job 复检沿用记录来源，不降级 legacy。单元测试 `internal/investigation/assistant_audit_authority_test.go`。
- R3：退出时服务器撤销失败不再静默吞错——本机身份立即清除并取消旧请求，同时在登录页保留“上次退出的服务器会话尚未确认撤销”提示与“重试撤销”按钮（重试绑定原 sessionRef/会话版本/CSRF，浏览器换账号后拒绝跨账号撤销，不清新身份）。真实浏览器注入 500 后复验提示、重试、旧 Cookie 401。
- R4：浏览器验收脚本改为逐条断言（admin/clusters 200、operator 无授权范围 403 且 code=FORBIDDEN、legacy role-bindings 0 行、会话 LoA1→LoA2、注册 URI 只出现一次、恢复码 10 条、入口限流 429 且 code=RATE_LIMITED、首次登录 first-password-only 且无业务授权）。分母纠正：像素复算为 21/1363300（两处登记文案遮罩），旧报告的 1365851 已在 p2-native-auth.md/progress.md/acceptance.csv/task-progress.csv/preview-r2/result.json 更正；本轮构建按同一采集流程复算同为 21/1363300（`evidence/mfa-r3/visual-diff-current.json`）。
- P2-03：新增迁移 51（`identity.mfa_enrollments`、`identity.mfa_recovery_codes`、`identity.step_up_challenges`、`identity.step_up_sessions` 与 16 个 SECURITY DEFINER 函数，FORCE RLS、API/Worker 无表授权、固定 search_path）。TOTP 仅用标准库实现（RFC 6238，6 位/SHA-1/30 秒），密钥只以密文入库（AES-256-GCM 任务域密钥后端，另有 OpenBao/机器身份链适配器；无密钥后端时接口 503 而不是明文降级），恢复码高熵且只存 SHA-256 摘要。挑战绑定 sessionRef/operation/目标/风险确认/版本/绝对与空闲期限，一次性消费、并发重放拒绝；只有真实密码+TOTP 才 loa2，恢复码只恢复 loa1 并撤销旧会话、强制重设；管理员重置在目标绑定的 step-up 下同事务递增目标/版本、撤销会话、删除因子与恢复码并写审计。设置 secret 只在创建响应出现一次，后续 GET 不回显。
- 新增正式路由与客户端：`/api/v1/auth/mfa/status`、`/api/v1/auth/mfa/enrollments`(+`{id}`/`{id}:verify`)、`/api/v1/auth/step-up-challenges`(+`{id}:verify`)、`/api/v1/auth/mfa/recovery-codes:verify`、`/api/v1/admin/users/{userId}/mfa:reset`，OpenAPI/生成客户端/配置同步；Web 新增正式路由 `/account/security`（状态、绑定、验证、恢复码、管理员重置）。
- 验证环境：全新独立实例 `ops-ui-v12-mfa-pg-20261022`（127.0.0.1:15453，PG17.11，从空库前滚 1..51）、API `ops-ui-v12-mfa-api`（127.0.0.1:18084）、Web 容器 `ops-ui-v12-mfa-web`（192.168.0.63:30464）；15452 与 30462/30463 共享入口、15449～15451 及旧存储未改动，v12.admin 密码与 MFA 状态未改。证据：`evidence/mfa-r3/`（集成日志、浏览器结果与截图、像素复算、Go/Web 测试日志）。

## Codex独立审核preview-r2（2026-10-22）

仅接受SL-001布局改进、原生管理员列表接通等已验证子集；整体changes_required。定向Go/19Web/合同通过，独立像素复算21/1363300（两处登记文案遮罩）。只读真实DB探针发现撤销前上下文仍能通过新授权函数，原生审计分支仍被旧表围栏提前拒绝；退出失败静默与浏览器关键结果缺断言亦需修复。见[evidence/codex-preview-r2-review-r1.json](evidence/codex-preview-r2-review-r1.json)。P2-03、P2-04剩余生命周期、P2-05、P3尚未交付，ready不代表整体通过。47～49已执行冻结，修复继续新迁移。

## Codex 独立审核 MFA r3（2026-10-22）

结论 `changes_required`，取代上一节“R1～R4关闭”的整体声明。独立7包Go、25项Web、合同、构建及15453/schema51两项集成测试通过；认可迁移50会话绑定修正，原生审计来源分派/摘要结构已修正，完整审计与Worker运行仍需验证。MFA事务复检、恢复码失败预算、重绑流程、错误后revision、退出失败刷新、恢复后强制设置及真实业务再次验证仍未闭环；详见[evidence/codex-mfa-r3-review-r1/result.json](evidence/codex-mfa-r3-review-r1/result.json)。两个负向SQL探针均回滚；撤销探针仅在事务内延长证明期限以隔离撤销判断，失败浏览器探针保留且不计通过。50/51已执行冻结，后续修复只用52+。

P2-02/P2-03保持in_progress；SL-001既有静态visual结论保留，生命周期缺陷使interaction/authorization/live不能按全部状态通过。OpenBao实际启动接线、制品版本绑定和运行说明尚未完整。Hook记录执行模型minimax-m3.1-flash，与指定Deepseek-V4.1-Flash不符；保留成果并记录流程偏差，不将此视为模型质量测试。

## GPT直接继续：MFA生命周期 r4/r5（2026-10-22）

用户取消CodeBuddy执行，交接c767b57e已cancelled，后续由本对话GPT直接实现。新增前滚52并在隔离15453执行；修复MFA当前会话复检与锁、持久恢复失败预算、保留旧因子的原子重绑、服务端强制重新设置、页面revision及嵌套guard、退出待撤销刷新。独立真实表单与500/模拟超时/模拟断网后GET200→刷新→重试401、恢复后重新登录限制及重新设置恢复业务均通过，详见evidence/gpt-mfa-r4/result.json。隔离新入口30465/API18085，旧30462/30463/30464及用户凭据未变。

继续新增53精确challenge证明解析/重置锁序及原生源配置请求绑定。真实集成已验证无proof拒绝、篡改body拒绝、正确PATCH持久revision2；命令及其余业务接线仍进行中，P2-03不标完成。52/53已执行冻结。OpenBao机器身份启动接线已写，实际Transit验收尚未完成；未把文件密钥后端当成OpenBao通过。

## GPT直接继续：r6真实OpenBao与r7管理操作接线（2026-10-22）

隔离15453已前滚54并冻结。真实OpenBao TLS/Raft + Kubernetes TokenReview + ops-api projected JWT + 专属Transit key完成MFA设置、重绑、恢复、退出失败恢复及后端停机503/重启恢复验证，见evidence/gpt-mfa-r6/runtime-openbao/。原生命令证明与实际assessment/UID/资源revision/命令摘要/执行配置版本绑定的真实集成通过，证明测试不代表真实命令派发、Worker或策略准入。独立来源浏览器已实测PATCH200并持久status=disabled/revision2，请求ID 01a16359-b24e-7406-8400-2a7c46710b53；完整结果仍在远程gpt-r6-preview/logs/source-browser-result.json，尚待回收截图。

r7补齐策略草稿、发布、激活/回滚、版本退役及专用执行配置发布/退役的请求级证明，移除RetireProfile/OnboardHost旧角色表围栏；明确拒绝native修改legacy授权/创建legacy初始授权租户。新增administrative_write仅绑定实际HTTP方法/路径/规范化内容，允许新对象尚无ID/revision；修订号/固定版本仍由业务服务检查。Go三包及合同、27项前端定向、3项证明/冲突恢复检查与构建通过。r7尚未部署及真实数据库运行，不能标P2-03完成。

当前远程局部阻塞：ssh mssc-direct的本机10.10.10.1绑定地址消失，192.168.0.63:22严格已知主机入口也超时；未改SSH/网络配置，未启动OrbStack。继续本地原生用户/授权开发，所有新迁移和业务浏览器验收保留未验证。OpenBao测试JWT是限时TokenRequest样本，重新联调前需重新签发任务专属JWT，生产部署仍需Kubernetes自动轮换projection。

用户特别要求保留“额度耗尽前”的未完成项；完整接续断点、已接回结果、r7未运行变更、后续依赖及环境保护见[continuation-handoff.md](continuation-handoff.md)。远程断连单独记录，不取代原中断任务。

## GPT直接继续：P2-04原生用户生命周期与授权r8（2026-10-22）

已源码接通C-014～018及C-019～021原生适配：真实账号列表/创建、资料与启停、独立密码重置、会话撤销、双角色分别授予与撤销；角色范围使用登记cluster UID，namespace不得隐式扩大为集群。创建强制改密且无默认角色，密码/状态/授权变化撤销旧会话；新SQL在目标有序锁和tenant advisory lock下复查当前原生管理员/版本，保留最后有效管理员。所有业务变更与幂等结果、审计在同一事务；API没有identity表直授权。原生请求不回退legacy角色表，旧OIDC路径继续保留原合同。

新增55尚未执行、不冻结；delivery-baseline的55是源码迁移范围，不是运行库版本。当前最后验证运行库仍15453/schema54。新用户页面复用v12公共页头、Surface、Modal/Drawer；安全验证和原生密码12～256字符要求作为补充交互，不能直接继承HTML像素通过。原生C-021只能撤销；恢复授权须重新使用C-020明确角色/范围，保留新binding和审计。

密码请求先严格规范化，再用当前会话CSRF秘密对密码做HMAC参与证明/幂等摘要；不将低成本密码验证摘要、明文密码或Argon2 hash写入证明、幂等响应或审计。Go/Web独立向量一致，修改密码/目标/版本使旧证明失效。

本地144 Vitest+6 SSE、Go auth/identity/httpapi/tenant/action/app与合同、TypeScript/Vite正式构建通过；最终HTTP/密码摘要/用户边界定向复验通过；Linux API和集成二进制已构建未运行。失败记录保留：首次合同检查因55源码范围未同步delivery-baseline失败，修正后通过；合同CSV写入列名错误已从本任务最新保存快照完整恢复177条并核对，后续原子写入，见evidence/gpt-admin-r7/contracts-recovery-r8.json。

真实数据库集成用例已准备，使用真实TOTP时间窗等待，不重置防重放计数；需在恢复连接后用15分钟测试超时运行。实际DB/HTTP结果、最后管理员并发、审计回滚、真实浏览器/视觉尚未验证，C-170/C-171历史主体显式映射及历史归属读取未实现，P2-04继续in_progress；P2-05/P3及其余依赖仍未完成。版本、制品及限制见[evidence/gpt-users-r8/result.json](evidence/gpt-users-r8/result.json)。

最终整理出现Go摘要函数重复err声明，编译失败日志已保留gpt-users-r8-boundaries-compile-failed.log；修正后相关Go包/合同复验及HTTP最终复验、Linux API/集成二进制重建通过，见gpt-users-r8-go-revalidated.log和gpt-users-r8-http-revalidated.log。密码HashPassword延后到真实操作证明及幂等授权通过之后，重放直接复用结果，避免未验证请求进行昂贵哈希。源码制品清单已绑定最终版本。

## GPT本地继续：历史主体受控清单与显式映射r9（2026-10-22）

按用户要求推进不依赖远程的部分，未探测/修改远程、未启动本机数据库或业务浏览器。补齐C-170/C-171源码：受控migration-duty CLI导入已人工核对清单，严格JSON/重复键/大小/目标ID边界；原生管理员用逐请求再次验证提交映射，服务端按同tenant清单摘要、完整旧主体及真实user ID匹配。旧主体必须在该tenant已核实历史来源中存在，拒绝现有原生主体与冲突归属。映射与账号版本更新、旧会话撤销、幂等结果及审计在同一事务；原生subject和双角色范围不变，不复制旧角色。列表、分页、创建弹窗已接真实生成客户端；修改字段后旧证明失效，关闭进行中请求提示刷新确认。

新增56与此前55均未执行、不冻结；源码delivery-baseline已为56，最后独立验证运行库仍15453/schema54。复核修正55的管理员计数错误：各管理员应匹配其自身授权版本，不能同时等于当前查看账号版本。已准备不同版本账号的断言，尚未运行SQL。原有1～54未改。历史映射存在但不代表历史读取已接通；旧NULL inventory_digest记录保留，不自动升级为本轮核对材料。

本地Go identity/httpapi/contract通过，CLI新命令及边界定向通过；145 Vitest+6 SSE、TypeScript/Vite构建通过；Linux API/CLI/集成二进制交叉编译通过未运行。opsctl首次全包测试保留失败：TestBundleBuildCLIAndIndependentVerify因opa-sdk-source与既有reviewed对应源码锁不一致，未修改来源材料/锁来绕过；新CLI定向测试通过。进度校验177合同/458状态通过，不提升任何产品六维验收。迁移55/56执行、真实HTTP/持久化/并发/审计回滚、历史归属读取复检和视觉均待验证。

操作、恢复边界与续接步骤见[subject-mapping-runbook.md](subject-mapping-runbook.md)，证据见[evidence/gpt-mapping-r9/result.json](evidence/gpt-mapping-r9/result.json)。P2-04继续in_progress，P2-05与P3未宣称完成。

## GPT本地继续：原生部署配置及执行配置读取r10（2026-10-22）

独立推进P2-05可离线实施部分：platform Helm runtime.humanAuthMode统一Web/API/Worker，native发布auth.mode=native、去掉OIDC issuer/代理/CA依赖，/oidc/404；API接固定tenant/origin和现有MFA Transit key，复用独立CA与既有OpenBao audience限时JWT投射；不向Worker添加MFA key配置，不改OpenBao策略。缺配置、未知模式、非法origin/端口、API/Web origin不一致均拒绝渲染。Web实际渲染配置参与checksum，保留TLS/CSRF同源边界、禁缓存/SSE和旧OIDC兼容配置。

C-134 action.ListProfiles仍直查legacy角色表的遗漏已修，改共享authz.RequireAdmin及独立native operator当前会话/版本复检。单元覆盖原生管理员、撤权、operator独立、主体不匹配、数据库失败；守卫集成补读取与撤权断言，仅交叉编译未运行。发现原OpenAPI有cursor/limit但处理器/服务未实现分页与meta，合同审计显式保留缺口；C-139专用退役未改。

Go action/authz/app和完整contract通过，最终Helm定向复验通过；Linux API及集成二进制构建通过。首次SP06离线fixture遗漏SP05导致失败，补齐原依赖后通过，失败日志保留；不降低原门禁。没有本机数据库/业务浏览器、没有远程调用/部署、没有CodeBuddy。未改Web源码，不重复无关Vitest/视觉参考采集，未提升产品验收。

P2-05仅配置子项in_progress，P2-03/04前置验收仍未完成，不宣称进入后续业务验收。正式Bundle安装器/business-values/Profile仍依赖OIDC，Job/Worker授权来源与版本持久化、历史读取复检、无OIDC真实冷启动和切换回退尚未完成。55/56仍未执行，最后独立验证schema54。操作及限制见[native-deployment-runbook.md](native-deployment-runbook.md)，证据见[evidence/gpt-native-deploy-r10/result.json](evidence/gpt-native-deploy-r10/result.json)。

## GPT本地继续：定向代码复审与修正r11（2026-10-22）

复核r8～r10原生身份/授权、主体映射清单、C-134及部署调用链，修正两项P1源码问题：55修改一个角色时曾将其他active但版本已失效的角色一并更新为当前版本，会意外恢复旧角色；现只推进变更前版本本来有效的独立授权，准备stale-admin→更新operator仍不成为admin的真实集成断言。55未执行，SQL回归只编译未运行，不写通过。动态PKI在OIDC+SP04、无SP06时API缺OpenBao地址/CA的遗漏，已用Helm用例复现后补齐，失败证据保留，完整合同及最终定向复验通过。

C-134补HTTP分页/meta，先按当前权限过滤，再按profile ID+版本定位游标；同ID多版本不丢失，未知/失权/退役游标拒绝，limit默认100最大200、cursor最大128。OpenAPI与生成代码同步，原生Cookie认证声明接入；目录读取仍沿既有实现，不把数据库大目录分页/性能写通过。清单解析新增reviewedBy/legacySubject的NUL拒绝，避免非法文本触库后才失败。

identity/httpapi/action/app、完整contract、本轮最终OpenAPI/UIV12及Helm定向、CLI相关定向通过；13项前端定向与正式构建通过，Linux API/opsctl/守卫集成二进制构建未运行。未连接远程、未运行本机DB/业务浏览器、未派发CodeBuddy。1～54未改，55/56仍未执行，未升级六维验收；P2-04/P2-05及V1.2未完成。完整发现、具体输入、修正及验证界限见[evidence/gpt-review-r11/review.md](evidence/gpt-review-r11/review.md)；最新证据[evidence/gpt-review-r11/result.json](evidence/gpt-review-r11/result.json)。正式安装器、Job/Worker显式原生授权、历史归属读取、并发与审计回滚仍待继续。

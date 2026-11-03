# r33：GPT 直接接续集中返修（2026-10-27）

用户撤销 CodeBuddy 执行，交接 1d91d497 已取消。本轮由 GPT 修改、构建、远程验证，保留 platform/main/082f87d66c5b 上 r23～r32 全部未提交修改，无提交、推送、迁移或模型调用。来源与实际二进制见 source.json、runtime-final.json；不代表 P2/G2 或 V1.2 完成。

## 修改与证据边界

- Lease：缺少 Client/Graph/Mirror 时清除快照并返回 ErrNotReady，Graph=nil 不再崩溃；不改变 epoch，失效通过 deadline 围栏。三种依赖回归通过。
- Helm：本地采集按所有明确声明 Namespaces 的去重并集分别生成 Role/RoleBinding，全部绑定发布命名空间的 ops-worker；lease Role 仍仅发布命名空间，集群级读取仍为既有六类，无 Secret/写权限。单/双命名空间渲染断言覆盖数量、位置、主体、资源和 lease 名。未部署第二命名空间，不能把渲染测试称为实际多命名空间授权验收；localCollector=false 不生成此本地授权，远程集群仍须专属凭据。
- 归档冲突根因：LIST 原用 encoding/json 解码 map 得到 float64，WATCH 的 Unstructured 解码保留 int64；projectNested 对 restartCount/exitCode/signal 只保留 int64，同一 UID/RV 因入口不同产生不同投影。LIST 改用 Kubernetes 已安装的整数保留解码器，继续兼容列表子项缺 Kind/APIVersion 的原生响应。新回归比较相同 LIST/WATCH 对象的完整投影并断言重启次数未丢失。
- 归档 v2：ArchiveService 的首观察并发归一原只认 v1，现同时支持固定 v1/v2，内容/来源/revision/范围等不可变检查不放宽。真实 PG 下两版各 16 路首观察幂等、后续时间复用、改变内容拒绝通过。未回写任何旧归档/摘要，不通过换投影版本逃避旧冲突。
- 降级：队列记住具体 UID/RV 的不可变冲突；其他任务完成不会清掉它。Graph 查询实际产生 partial/warnings/degradedSources（单元回归），现有 EnvelopeMeta 消费这组字段。未在业务浏览器注入真实归档冲突，不能称降级界面已运行验收。归档冲突仍跳过该条并继续采集，未归档新内容不产生成功引用；旧归档不可覆盖的真实 PG 反例仍通过。
- 来源合同：修正新测试库迁移44创建 identity schema 所需的独立库 CREATE 授权；PG 实测登记错误范围拒绝、合法登记/轮换、错误 mapping 更新拒绝、存量不兼容呈现、停用仍可执行、轮换拒绝激活、显式修复、回滚错误旧 mapping 拒绝、合法回滚追加 revision。status-only 更新保持原有可用性，不宣称其与修改 scope 同样校验。同步生成 Go/TS 的 incompatible_data_scope_mapping enum。
- 历史 HTTP：新增真实 PG + 正式 Cookie 登录/中间件/AssistantHandlers 回归。修复前会话详情200但 GET messages404（historical-before.log）；只把消息分页的只读预检改为既有 GetReadable，写/重绑路径保持严格所有者检查。修复后会话、消息、轮次均200；同名未映射账号拒绝，撤权拒绝，追加任务拒绝且 Job 数仍1，冻结归属/input/scope/binding 字段不变（historical-fixed.log）。这是明确标记的受控迁移 fixture 机制验收，非真实旧库迁移；inventory/mapping 完整 API/UI、执行与审计引用仍待验。

## 真实采集与重连

只在 ssh mssc-direct 的获准任务环境执行。PG 17.11/15454、schema70；根目录77%、/data12%（开工复核），使用独立 r33/ 目录；v12.r24.admin/member 密码、MFA、角色不变。

- 新建无秘密 ConfigMap r33-rotation-proof，UID 85711746-8bbe-4411-9620-f683d5acf665，初始 RV1863416（10:28:02Z）。
- kubelet 凭据自动轮换：digestPrefix 3ed7e8cd（10:19:55～10:29:55Z）→ca24cf6d（10:28:20～10:38:20Z），没有手工续签或加长期令牌。轮换后修改标签为 two，服务端复读 RV1863556；DB 同时保留两个 RV，API200且标签 two。kubectl label -o json 的客户端旧 RV 被断言拒绝，随后明确 GET 服务端版本，未用旧输出冒充提交版本。
- Worker 部署本轮二进制后重新建立 list/watch，将同一资源标签更新 three，服务端 RV1864069；DB 新增该 RV，API200回显 three。资源及失败证据保留，不删除历史。
- 本轮 API/Worker 均更新到 r33；Web 保持 r14、调查器 r28。该栈是任务开发源码 canary，未做正式 OCI/Bundle 或新 namespace 整栈冷启动。最终浏览器重登读取200，连续12次资源读全部200、outsideScope=0，见 resource-final-stack.json、resource-12-reads.json；这只支持记录窗口内结论，不承诺所有负载永无503。

## 验证与保留失败

本地 local-focused.log（app/graph/kubernetes/contract 定向），local-race.log（队列/Lease/采集相关 race），source-http.log、historical-local.log、historical-race.log 均通过；go generate ./api 成功，TypeScript tsc -b 通过，diff --check 通过。未重复无关全 Web、SSE 或模型质量测试，未采新参考图。

远程 source-archive-recovery.log 两组真实 PG 测试通过（包含 v1/v2 16路并发、源撤销、损坏、法律保留、未知提交结果、恢复）；historical-fixed.log 通过。每个测试新建随机库，含审计者按已有守卫保留，列表见 artifact-index.json。归档回归旧 fixture 恰180日的纳秒边界在 PG 微秒往返后失败，改用生产采集器相同181日余量，未降低产品保留规则。原失败日志和 fixtures 修复说明见 failures.json。

## 未关闭项与接续

1. C：受控历史数据的 inventory/import/mapping 完整管理 API/UI，历史执行/审计/证据引用及双角色组合，仍需在独立环境验收；本轮只补历史 HTTP 读取缺口。
2. D：独立 namespace/数据库的 Web/API/Worker/调查器/CLI 无OIDC整栈冷启动和浏览器、重启持久化仍未执行，不称外部阻塞；正式 Bundle/离线冷装另外受合格物料门禁限制。
3. 真实无效/丢失采集凭据导致拒绝及恢复、第二命名空间实际授权、归档冲突 UI 注入未验。现有定向故障测试不替代这些 live 项。
4. P1/P2 完整视觉仍未通过，P3 SL-049 同组资源日志样板及 P4～P8 按依赖继续。acceptance 六维不提升：visual0、interaction5、api5、persistence5、authorization3、live5。

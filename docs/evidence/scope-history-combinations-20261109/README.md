# 命名空间调查与历史来源组合验收补正

2026-11-09，`mssc-direct / ops-current`，入口 `https://192.168.0.63:30471`，schema 74、namespace UID 均未变化。当前接受候选为 `fbdfef5e6c80caf6fa5af6816ac9896e5ea5593d`；API 来自该提交，调查器/Web 来自 `3e597ea36eae708ed47e3e24955d6a974e392f9a`，Worker 继续复用已记录的 c2fe195 镜像。精确镜像及来源见 [运行记录](runtime-final.json)。部署前备份为 `/mnt/ops-sdb/ops-current/backups/20261109T011204Z`。

## 补正的两个缺口

此前“完整收口”的声明早于下面两项组合通过，应当更正：命名空间 operator 的 403 只证明拒绝，不能当作调查正例；任意旧归档能读与另一个旧故障能复盘，也不能合并成同一旧故障读取旧来源证据的证明。旧回执保留原样，本目录补充实际正例和失败原因。

1. **命名空间 operator 实际调查。** 通过原生管理 API、管理员密码/TOTP 创建专用 operator，唯一绑定 `233b35d6-0ef4-46d1-a1e8-1ba3185656c0`，授权版本始终为 2，仅有 `ops-current`，`clusterScoped=false`，没有管理员角色。页面统一故障入口调查真实 RolloutStuck 故障 `01a1bdac-eaef-7730-a73a-90738956cc96`，轮次 `01a1bde0-56a6-7998-9069-73aad67d6ee0` 实际读取 Finding、归档和固定 RCA 82，引用 200；最终候选回读仍为 200。回答如实保留根因未确认和缺失观测，业务 `partial` 不冒充确定结论。
2. **同一旧故障、来源升级、旧归档及 AI 复盘。** 最终候选上，上述同一命名空间账号从旧故障 `01a1ae2d-eff0-7664-a10e-d02a701023f4` 创建会话 `01a1be1b-2a19-73ea-b2c4-60b099f655b1`，轮次 `01a1be1b-4ad6-70a3-be11-ee53cbbb8bb4` 实际执行 `get_assistant_context → model → get_formal_references → model`。正式读取返回历史 RCA 1，固定摘要 `sha256:d131fc8917122a7c9fd0ed2c661d8d47d28a0cf667ab8edb74b163f4f82f1a35`。模型收到并解释两条来源版本 1 的真实归档事实：`3dbe6b3d-67b1-560e-9712-a5093ce69b3b`、`9f58d2ab-8723-5df5-931c-85b89676206f`，均为 `archived_verified` 的 FailedMount Event，resourceVersion 分别为 2528117、2528128。两条证据和 RCA 1 都实际引用并回读 200，页面固定版本抽屉与重载恢复通过。

当前来源注册仍为版本 2，旧故障 revision 11、当前 RCA 22、RCA 1 的输入/内容均与补验前相同。没有刷新 RCA、回调注册版本、重查指标日志或执行命令。旧 RCA 本身为 unresolved/partial，最终回答因此保留 `partial`；本项通过的是合法历史事实读取、解释和固定引用，不证明已找到历史根因。

最终候选的范围外故障与显式请求集群级资源均返回 403；匿名读取旧证据、固定引用均为 401；另一真实账号读取该私有会话引用为 404。账号授权没有通过扩大范围取得正例。

## 产品修复及旧接口

- 页面不再默认为命名空间账号请求集群级资源；调查准入按实际命名空间逐一检查策略并保持同一固定策略版本。
- 历史正式引用在逐项重验当前账号范围和归档来源后，允许已经升级的来源读取历史快照。活跃 RCA 提交仍使用严格的同版本来源检查；没有放宽新查询或提交门禁。
- 正式引用工具通过原有签名 Worker 归档读取路径获取实际事实，并校验固定证据身份、版本、摘要；最多 20 条/24KiB 事实，超限明确列为遗漏和 partial。模型的“禁止新查询”约束保留，同时允许显式指定的正式历史版本读取。
- 本轮额外发现旧 v2 接口返回 202 后任务立即失败：当前托管模型 Profile 明确禁止 legacy model，旧接口却仍准入。两条实际失败 Job 保留，不能称为调查成功。最终 API 在创建任务前返回 503 / `CAPABILITY_DISABLED`，指向支持的统一 v3 会话；实测没有新增 v2 Job。没有开启被禁止的旧模型回退。命名空间正例由当前统一页面的真实 v3 调查和上述组合完成。

部署中曾因重建 Web 容器漏带原 `/tmp` tmpfs 而启动失败，已恢复原 tmpfs、只读根文件系统和运行用户，最终入口恢复。该操作失误不计为产品通过证据。

## 已有真实操作证据直接复用

[原真实故障、修改、恢复及回退记录](../incident-remediation-20261109/README.md) 保留；最终候选仅回读，没有再次制造故障或执行修改。9 条 execution 身份完全相同、总 attempt 仍为 7；未知结果 `01a1bdb7-d0b2-76b3-ad19-12e88272c0dc` 保持未知。最终回退 `01a1bdbe-f44e-7796-9d5b-e7ab8a625eab` 的原生恢复报告和四条归档仍返回 200，配置目标摘要仍来自原验收。原操作发生在 c951824，不写成本候选重新执行。

## 核对材料和总账

运行 `python3 docs/evidence/scope-history-combinations-20261109/verify-combinations.py` 可核对保留回执，输出见 [verification.json](verification.json)。它不执行远程操作。实际调用见 `final-history-steps.jsonl`，原始失败见 `namespace-page-before.json`、`namespace-v2-result.json`、`namespace-v2-retry.json`、`history-before-turns.json`；最终读取和拒绝见各 `final-*.json`。

受影响 Go 包通过；调查器 108 项、页面相关 23 项和 Web 构建通过。最终旧接口准入补丁另通过 httpapi/app 回归。截图证明页面行为，不是 HTML 1:1 像素验收。

[总账覆盖映射](ledger-coverage.json) 把已有运行证据登记到 P4-05/P5-01/P5-02/P6-01；四项由 not_started 纠正为 in_progress。458 切片的完整动作及视觉未逐项通过，不能凭其中某条机制升级整片六维；对应行登记覆盖子动作及待验范围。22 菜单、HTML 1:1、完整发行分别继续按原完整标准；KubeVirt/CDI、Trace、SP-08 的明确阶段边界不变。

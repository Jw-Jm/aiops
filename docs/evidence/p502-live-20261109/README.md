# P5-02 会话机制现场验收及原生保留修复

2026-11-09，候选 `7c336fe7f96bb88b54984976e883f3130b89f915` 已部署 API/Web，schema 74。接受指针及旧 acceptance.json 仍为 fbdfef5；没有把待验候选写成已接受版本。部署前备份 /mnt/ops-sdb/ops-current/backups/20261109T071733Z。精确运行状态见 runtime-pending.json。

Codex 独立审核 MiniMax 回调1d628d4a（128个请求阶段均为MiniMax-M3.1-Flash）。8项新增带库测试全部通过、无跳过；另3项既有范围/保留/取消带库用例通过；4个相关Web文件16项通过，Go包、integration编译、构建和生成物复现通过。生成检查需传Go可执行文件，MiniMax先前把它当包管理器脚本的解释有误。

真实命名空间operator唯一授权保持ops-current。页面创建及改名201/200，重载标题持久；归档200，已归档筛选及字面100%_反斜杠搜索可用。新轮次实际调用get_formal_references，读取同一旧故障RCA1及两条旧来源证据，当前注册仍为2。归档前后回答与固定引用内容相同，RCA和两条证据均回读200。模型如实partial，不声称已确定根因。

分页续页与规范化过滤200，改变过滤携旧游标403。改名同键同载荷返回相同结果，异载荷及过期revision409；无CSRF403；越范围重绑403；另一真实所有者读取404、改名403；匿名引用401；标题token脱敏。页面范围调整200。真实调查轮次在浏览器离线时由只读SQL确认running，重连并重载后仍同turn/job，终态partial且只提交一次。另一次真实页面停止返回200和cancelled终态。首次尝试停止时轮次已结束，原始partial保留，不冒充取消成功。保留/清理的过期时间推进仅在隔离测试库执行，未改业务历史时间。

**现场未通过：原生platform_admin设置legal hold返回403。** 已核对当前原生会话及platform_admin授权版本3，使用正确csrf.token后仍为业务拒绝；SQL assistant_set_hold仍只检查旧role_bindings。相关材料为native-admin-hold-failure.json及live-qa-findings.json。最初验收helper误用csrfToken得到CSRF拒绝，修正后才采集上述业务拒绝，不混淆两者。该问题需要产品修复和原生账号带库测试，不能靠添加旧角色绑定规避。因此本目录不是P5-02完成回执，更不是四项完成声明。

P4-05/P5-01/P6-01仍在原授权范围内，后续接续；HTML 1:1尚未验。既有真实命令修改、回退及未知结果证据继续复用，本轮未新执行资源修改命令。无变更KubeVirt/CDI、Trace及SP-08阶段边界。

## 最终补验（2026-11-09）

原生legal hold缺陷已由MiniMax修复，Codex审核并独立复跑15项带库用例（14功能+迁移角色）全部通过、无跳过。105个模型请求阶段均为MiniMax-M3.1-Flash。新增迁移75保留旧函数，以真实原生会话、账号本人subject、有效版本和platform_admin授权围住原生写入口，API角色不能直接改hold表。冻结迁移1..74未改。

最终候选179ca6afc9b0ba60e95e5f8e2f674fcd2deaf11d已部署，正式工具将schema74推进75；备份为/mnt/ops-sdb/ops-current/backups/20261109T074941Z。第一次迁移helper用urlencode默认加号编码空格导致pgx把+role当参数，尚未连上数据库；改为%20后正式迁移成功。两份日志保留，产品迁移没有失败提交。release-manifest.json及runtime-final.json为最终运行版本；runtime-pending.json为此前7c未接受的历史状态，不再代表当前环境。

真实原生管理员在页面设置与解除保留均200，库内标记一致；设置原因中的合成token已脱敏，两个审计记录同样脱敏。普通命名空间operator仍403。迁移后旧回答与RCA1固定引用200且内容不变。9条原执行身份、7次attempt不变，未知记录及回退报告可读，不重复执行。

本次通过的是会话生命周期、历史/固定引用、续传停止及保留机制。P5-02的14条主切片还包含数量下钻、趋势、日志、复制导出等与P5-01共享动作，后续必须一并收口，不能把机制通过升级为整片/整项完成。四项仍in_progress，HTML视觉未验；原始失败、原始版本和Synthetic/live证据类型均保留。acceptance.json仅接受本候选的明确机制范围。

### 总账静态检查

validate-progress.py通过。C-075真实PATCH已实现，当前contracts.csv与contract-audit.csv已从拟新增更正为已有，原始original-plan未改。validate-plan.py仍把初始119已有/60拟新增硬编码为当前必须值，因此现实现120/59时该两条计数检查失败；其余5079检查通过。这是当前账本工具待修项，不冒充通过。后续P4-05阶段须同时修正当前实现进度与冻结设计基线的计数校验，保留初始基线和真实API存在性检查。

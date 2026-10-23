# V1.2 独立开发预览（2026-10-22）

## 当前入口（preview-r2，本轮最新构建）

入口：[V1.2 登录页](https://192.168.0.63:30463/login)。同样只支持本 origin，自签名证书与 r1 相同主机名/IP（浏览器首次访问需接受告警）。用户名仍是 `v12.admin`，密码未改变、未开启 MFA，读取方式见下一节。

- 目录：`/data/ops-ui-v12-http-20261022-r1/preview-r2`，复用本任务 15452/schema49 的隔离 PostgreSQL。
- API：正式 `cmd/platform-api`，本轮由当前工作树重新编译为 `preview-r2/platform-api`；systemd transient 单元 `ops-ui-v12-preview-r2-api`，127.0.0.1:18063，指标 19063。
- Web：本轮 `web/dist` 重新构建，容器 `ops-ui-v12-preview-r2-web`，host network，只监听 192.168.0.63:30463，非 root 101:101、16MiB tmpfs。
- 与 r1 的差异：`NATIVE_AUTH_PUBLIC_ORIGIN` 为 30463；新增共享授权层与迁移 47/48/49；SL-001 右侧按原型实测值重排。
- r1（30462）保留为上一轮制品，不重启、不复用其构建。

重启与停止：

```sh
ssh mssc-direct 'systemctl restart ops-ui-v12-preview-r2-api'
ssh mssc-direct 'docker restart ops-ui-v12-preview-r2-web'
ssh mssc-direct 'systemctl stop ops-ui-v12-preview-r2-api; docker stop ops-ui-v12-preview-r2-web'
```

主机重启后重建 API（凭据由私有文件装入环境，不作为命令参数）：

```sh
ssh mssc-direct 'systemd-run --unit=ops-ui-v12-preview-r2-api --property=WorkingDirectory=/data/ops-ui-v12-http-20261022-r1/preview-r2 --property=Restart=on-failure --property=RestartSec=5 /usr/bin/python3 /data/ops-ui-v12-http-20261022-r1/preview-r2/run-api.py'
```

本轮 r2 验证结论：[evidence/preview-r2/result.json](evidence/preview-r2/result.json)、[浏览器](evidence/preview-r2/auth-browser-result.json)、[几何](evidence/preview-r2/geometry.json)、[像素](evidence/preview-r2/visual-diff-filled.json)。`GET /api/v1/admin/clusters` 由 403 变为 200，`platform.role_bindings` 仍为 0 行；operator 接口对纯管理员仍 403。

## preview-r1（2026-10-22，历史轮次，保留）

入口：[V1.2 登录页](https://192.168.0.63:30462/login)。需能访问开发机局域网；只支持此 origin。用户名 `v12.admin`。当前密码已在真实浏览器首次改密后轮换，授权开发机上读取：

```sh
ssh mssc-direct 'cat /data/ops-ui-v12-http-20261022-r1/preview-r1/private/administrator-current-password'
```

不要使用 `administrator-password` 里的旧初始密码，不把密码复制到证据、仓库、URL 或工单。此账号是独立验收组织的原生管理员，没有运维操作员权限。当前可查看登录与管理员框架；来源服务仍按旧 `platform.role_bindings` 事务复检，原生管理员的集群列表读取为403。尚不能查看真实业务资源；P2-04/P2-05完成前不通过添加旧授权记录掩盖此缺口。

HTTPS 使用本任务自签名证书，到期2026-11-21；浏览器首次访问会提示证书不受信任。证书只对应开发预览，指纹：

```text
SHA256 30:74:95:2F:15:34:A8:C1:52:4A:44:E7:6E:66:8B:6D:B0:DF:12:93:9B:6F:16:FD:4B:65:DA:76:94:16:A2:D9
```

## 实际运行

- 主机：`mssc-direct`，hostname `mssc`，LAN `192.168.0.63`。
- 目录：`/data/ops-ui-v12-http-20261022-r1/preview-r1`，复用本任务15452的隔离PostgreSQL17.11/schema46；独立组织、API LOGIN和bootstrap LOGIN。
- API：正式 `cmd/platform-api`，二进制 `platform-api-preview-r2`；systemd任务单元 `ops-ui-v12-preview-r1-api`，127.0.0.1:18062，指标127.0.0.1:19062。
- Web：正式 `web/dist`，任务容器 `ops-ui-v12-preview-r1-web`，只监听192.168.0.63:30462。复用缓存的非root Nginx镜像，挂载本轮构建及独立TLS材料；不构建另一套前端。镜像101:101，TLS目录同UID，临时目录使用16MiB tmpfs。旧失败容器保留为 `ops-ui-v12-preview-r1-web-attempt1`。
- 正式API仍校验既有r10已准入resolved Profile的只读副本；未改installable、ADR-0008或准入字段。未配置SP04/06/07 runtime、Worker、模型或来源，不启用其工作负载链路，不宣称已经验证本进程公网出口隔离或全栈业务能力。
- `__Host-ops-session` Secure/HttpOnly/SameSite=Strict；TLS代理保持原Host，显式origin用于CSRF复检。访问日志关闭，证据不保存会话、CSRF或密码。
- 旧30451、其他数据库实例、共享配置、存储、镜像和参考包未替换。未做生产发布。

当前API使用systemd transient单元，主机重启后不会自动重建。当前运行中重启/停止：

```sh
ssh mssc-direct 'systemctl restart ops-ui-v12-preview-r1-api'
ssh mssc-direct 'docker restart ops-ui-v12-preview-r1-web'
ssh mssc-direct 'systemctl stop ops-ui-v12-preview-r1-api; docker stop ops-ui-v12-preview-r1-web'
```

主机重启后恢复API（凭据由私有文件装入环境，不作为命令参数）：

```sh
ssh mssc-direct 'systemd-run --unit=ops-ui-v12-preview-r1-api --property=WorkingDirectory=/data/ops-ui-v12-http-20261022-r1/preview-r1 --property=Restart=on-failure --property=RestartSec=5 /usr/bin/python3 /data/ops-ui-v12-http-20261022-r1/preview-r1/run-api.py'
```

恢复使用已修复的native版本或前滚版本，不能切回旧OIDC制品/旧凭据或用旧快照覆盖新密码及撤销记录。迁移44/45/46均未改动。

## 验证与限制

证据：[本轮结果](evidence/preview-r1/result.json)、[浏览器](evidence/preview-r1/browser-result.json)、[进程重启](evidence/preview-r1/restart-result.json)、[持久审计](evidence/preview-r1/persistence.json)、[视觉比较](evidence/preview-r1/visual-comparison.json)。所有浏览器运行位于获准开发机，Chrome156.0.8078.4、1440×960、100%、zh-CN/Asia-Shanghai，与已有参考环境一致；没有重新采集原型图。

真实首次登录、改密201及first-only阻断已执行；该轮随后因“退出”按钮图标改变可访问名称而在自动化定位失败，原记录保留。之后使用已轮换密码完成刷新、角色隔离、跨Origin拒绝、退出200、Cookie删除、旧Cookie401、新密码再次登录。另验证同一会话跨API进程重启和浏览器刷新，之后真实退出。失败不重写为通过。

正式装配发现并修复：`NewFoundationHandlerWithNativeAuth`原来没有把native处理器接入dispatch，真实登录404；集成测试原先自行装配路由而漏检。现测试直接调用正式Foundation装配，真实PG回归通过。

SL-001已由旧居中布局修为原型双栏；仍存在右侧表单细小排版及文案差异，visual保持failed，其他切片不继承本次机制通过。原型的公开演示账号/密码、模拟说明和“交互原型”标签改为真实账号说明；没有把演示账号放进正式构建。首次改密补充状态和管理员业务页尚未做视觉验收。P2-02整体仍in_progress；MFA/恢复/再次验证、用户授权与历史主体映射、API/Worker/CLI全栈切换仍待P2-03/04/05。

## GPT r8源码候选（未部署，不能作为可登录页面）

r8新增原生用户页面及迁移55。仅本地构建/单元/合同通过，远程连接当前不可用；旧入口的运行状态须恢复连接后重新核对。API二进制与集成测试二进制位于本地artifacts/test-reports/ui-v12-session-r2/，其摘要及源码见evidence/gpt-users-r8/source.json。不要只替换旧30462/30463或旧数据库，勿更改用户v12.admin凭据。

恢复后先按continuation-handoff.md核对任务实例15453/schema54和目录所有权，使用新的任务轮次目录保留原制品。只在任务专属数据库前滚55，运行guarded TestNativeMFAEnrollmentStepUpAndReset（含新用户/授权真实HTTP用例，真实TOTP等待，测试超时15分钟），修复失败并保留证据。API/Web都要使用匹配的新版本与已迁移数据库；不能把编译成功或原r6浏览器记录当作r8运行通过。OpenBao测试JWT需重签任务TokenRequest并保留正式机器身份链，不重新初始化Bao，不使用文件后端替代Transit验收。

## r9本地构建提示

r9仅本地源码/单元/合同/构建，无远程部署；现有可访问地址若恢复仍可能是旧r4/r6版本，不包含r7/r8/r9。源码最新迁移56不等于运行库版本（最后验证54）；55/56未执行。新清单导入与主体映射运行条件见subject-mapping-runbook.md，不在旧预览中宣称可用。

## r10离线配置提示

r10新增平台chart原生配置及执行配置读取授权修复，只本地测试/构建，未部署，不产生新的登录地址。API与Web预览仍需远程恢复后核对实际版本；P2-05安装器/Worker授权切换未完成，不将Helm渲染当作无OIDC冷启动通过。详见native-deployment-runbook.md。

## 当前GPT r13入口（2026-10-23，覆盖旧“未部署/断连”状态）

- 登录：https://192.168.0.63:30467/login；用户管理：/settings/permissions。使用任务r6测试管理员，账号材料仅在远程/data/ops-ui-v12-mfa-20261022-r1/gpt-resume-r13/preview/private/browser-identity.json，由获准机器操作者私下读取loginName/rotatedPassword；不复制到报告。受保护旧v12.admin及30462/30463凭据未改。
- API：loopback18087，metrics19087，systemd单元ops-ui-v12-gpt-r13-api.service。最终包装器preview/run-api.py执行platform-api-password；旧platform-api与run-api-before-password.py保留。仅允许任务单元前滚恢复，不切回旧OIDC身份或覆盖新密码/撤销事实。
- 最终Web容器ops-ui-v12-gpt-r13-web-r3，UID101，仅挂载preview/web-password、tls、nginx.conf；API私有DSN/凭据不挂给Web。失败的首容器与停止的r2保留；现有其他预览均未改。
- PostgreSQL17.11/15453/schema56；55/56已执行冻结，后续57+。Bao沿用r6 TLS/Raft/Transit，不重新初始化；任务JWT只续期1小时，正式自动轮换/安装冷启动未验证。
- 新用户创建经真实操作证明与Transit验证，持久化且无默认角色；同一账号首次改密错误/成功、旧Cookie拒绝、无权限及退出已核验。首次改密脚本尾部定位中断由独立退出脚本接完，未重置账号或重建数据。详细版本/截图/请求ID和失败见evidence/gpt-resume-r13/result.json。
- 这不是V1.2整体验收：SL-005当前是独立首次改密页，与固定HTML对话框尚未像素比对；历史业务归属、异步Worker、安装冷启动、SL-049真实来源及其他模块仍未完成。安装器r12只本地实现与定向测试，不是运行于本预览的正式安装结果。

# 统一 amd64 物料与后续安装验证

运行源码候选为 `1a7c7da4af7d74d80df0df0aec72868cbe2785cc`，物料位于开发机 `/opt/ops-verification-20261019-1a7c7da/distribution`。本目录保留构建与原生物料测试，完整大文件留在该独立目录。当前镜像结果不表示签名 Bundle、真实安装或发行通过。

五个 OCI 均已构建；`native-build-receipts.json` 保留首轮失败及最后成功结果。同名组件取最后一个含 imageDigest 的条目。调查服务 42 个 unittest、Runner 六项原生协议测试和 Web 回环 HTTPS/静态页/运行配置/API 不可用映射通过；见对应日志与 JSON。原始失败包括：准备脚本误用 ARM64 分析器摘要、旧缓存复制超时、测试相对目录挂载错误、Docker bridge 缺失、固定回环端口冲突。最后 Web 使用动态空闲回环端口，未修改网络或占用已有端口；临时测试证书不作为正式 TLS 身份。

Web 源码原文与 notice 摘要核对记录于 `web-source-verification.json`；nginx 发布 GPG 签名未验证的原始状态保留。调查服务/Runner 的当前 amd64 有限许可身份仅重新绑定精确镜像/对应源码，所有 1,773 个原 notice 与 ADR 摘要均已复核，依赖身份和条款不变，见 `native-admission-renewal.json`。ARM64 不变。源归档、构建配方和原文共同随后续签名物料交付，不把组件升级或 Fixture 当成延期能力恢复。

下一阶段仍须使用既有签名 Bundle 和独立验签入口，再验证独立环境三阶段安装、真实登录/助手/故障链及实际 Service/PVC 执行链。远程性能问题仅记录，不关闭身份、来源、时间、保留和签名门禁；根目录正式文档与既有用户修改未动。

正式包准备发现并修复浏览器初始化模式缺口：新 `clientMode=browser-pkce` 与前端 `/auth/callback` 一致、无 client secret，并登记同源 silent callback；原 confidential 模式保持兼容。Web 对应源码通过明确的 `platform-web-source` 物料登记，避免添加未声明的 payload 文件；旧原生测试失败与首轮制包拒绝仍保留。

## 签名包及独立验证结果

`unified-amd64-installer-20261019-r2` 与 `unified-amd64-20261019-r2` 已在独立构建目录生成并签名。新安装器源码为 `9ade735`，运行组件源码为 `1a7c7da`；两次修复仅改变初始化/交付入口与准入数据，运行镜像不被事后替换。签名包文件不进入 Git，位置为上述 distribution 目录的 `installer-bundle-r2` 和 `core-bundle-r2`。

工具包经旧受信 `opsctl-signed-r12` 完整验证，再核对新安装器 SHA256 为 `c7d2dcaee95d1f9caddd50c2eceb664c9aa5a601bd2a4c5ed60a6c497efe28c0`。OpenSSL 对两个包的规范清单与 Base64 解码后的 Ed25519 分离签名均返回 0。首次直接传 Base64 文本给 OpenSSL 的格式错误已改为独立解码，不把它记录为签名成功或更改签名内容。Core 构建器自检通过，但之后独立进程的完整 payload 复核在 600 秒内没有取得完成回执；停止等待该连接，远程最终进程状态未知。**完整独立复核未通过完成门禁，不能标为 PASS。**

## 安装尚未执行

新 SSH 会话在 banner exchange 超时，且先前只读检查发现目标只有 no-provisioner 存储类、Available PV 为 0。未创建新 namespace/PV/PVC、未替换既有服务，未启动 dependencies/bootstrap-api/business，也未执行真实 Web 登录、助手/故障链、Service TCP/HTTP、专用 PVC 写读及归档后检查。未增加超时、扩容、重启机器或调整性能；根因未确认，不笼统归因于机器性能。

环境恢复后先读取原复核进程/回执的实际状态，不自动重复重操作；若需重验，仍执行同一完整门禁。通过后再准备专属存储与独立身份/来源配置，执行三阶段安装及真实闭环；不得用原生物料测试、旧数据库测试或已签名清单代替这些未运行项目。现状见 `material-status.json`，发行/人工模型质量未获准入。
## 2026-10-19 恢复后重跑：r4 签名包与三阶段安装

主机重启后 WiFi 网卡由 `wlp3s0` 变为 `wlp4s0`，netplan/NetworkManager 仍绑定旧名，节点地址 `192.168.0.63` 缺失导致 etcd/apiserver 崩溃循环。修正 netplan 片段与 NM 连接（原文件备份到 `/root/*.bak-20261019`）后节点恢复 Ready；既有 core/final/助手环境的 Pod 状态保持原样，未改造。

**高负载根因**：安装与 `bundle import` 都会把 8.9 GB payload 解到 `$TMPDIR`，而该主机 `/tmp` 是 7.5 GiB 内存型 tmpfs，首次导入即耗尽内存并使主机失联。此后所有 bundle 操作显式使用磁盘型 `TMPDIR`，未放宽任何超时或门禁。另发现 kubelet `node.status.images` 默认只上报 50 个镜像（按大小排序），5.1 MiB 的 `platform-web` 被截断导致离线准入误报缺失；将 `nodeStatusMaxImages` 提到 200，摘要校验逻辑不变。仅删除 7 个无任何工作负载引用的历史候选镜像，`mysql`/`clickhouse`/`sp08 dev` 等保留环境镜像未动。

**r2 安装失败与两个真实缺陷**（均在本轮修复并重签物料）：

1. `internal/app/sp06_runtime.go` 在 assistant 块存在但未启用时解引用空 Profile（`sp06_runtime.go:347`），安装器自身渲染 `assistant:{enabled:false,profile:null}` 后 `ops-api` 立即 panic，`bootstrap-api` 阶段永远无法收敛（`api-crash.log`、`sp06-runtime-config.json`）。修复提交 `04cae43`，新增 `internal/app/sp06_assistant_wiring_test.go`。
2. SP08 把 OIDC CA 以文件 subPath 挂进只读 TLS Secret 卷目录，runc 报 `not a directory`，`ops-web` CrashLoopBackOff（已用独立 Pod 最小复现）。修复提交 `ae82c80`：CA 改挂独立目录并同步 nginx 配置。

**r4 物料与验签**：`unified-amd64-installer-20261019-r4` 与 `unified-amd64-20261019-r4` 由同一签名密钥生成；工具包经旧受信安装器验证，运行包由新安装器完成全量 payload 复核，两包 OpenSSL 分离签名均返回 0（`core-r4-independent-verification.json`、`installer-r4-independent-verification.json`）。相对 r2 仅 3 个物料变化（`platform-api`、`opsctl-source`、`ops-platform-chart`），其余 33 个摘要一致。

**独立环境**：新建 `ops-amd64-unified-r4-20261019` 与专属 targets 命名空间、专属 StorageClass/PV（PV `claimRef` 预绑定，避免上一轮按容量随机配对），未接管任何既有卷/数据。

**三阶段安装**：`dependencies` → `bootstrap-api` → `business` 全部通过，postgresql/keycloak/seaweedfs/openbao/victoria-*/vmalert/api/worker×2/investigator/web 均 Running。期间完成 OpenBao init/unseal/configure、租户级归档 IAM 与版本化对象锁 Bucket、空库 43 迁移与 3 个受限登录（幂等复跑）、真实 OTP 注册与 LoA-2 登录、首租户、Graph 租约、真实 API 集群/3 个来源注册与 13 项签名发布。

**真实验证结论**：

- Web 登录 **PASS**：`https://192.168.0.63:30444` 静态页与 `runtime-config.json` 200，经 nginx `/oidc` 代理的 discovery 200，真实 PKCE + TOTP 取得 `acr=urn:ops:loa:2` 令牌，经代理调用 API 201、未认证请求 401（`web-verification.json`）。
- 助手/故障链 **NOT_RUN**：不存在 `admissionState=admitted` 的冻结模型 Profile，唯一评测报告为 `FAIL/NOT_ADMITTED` 且人工评审未填；按要求不代填、不伪造。
- SP07 执行链与 TCP/HTTP/PVC 写读/回执归档/后检查 **NOT_RUN**：缺少事件（finding 摄入绑定为空、分析器未产生事件），且图谱 46 个资源中不含本轮专用 Service/PVC——已注册来源的命名空间范围只有 targets 命名空间，而 PVC 验证要求运行器命名空间；`commands:risk-assess` 返回 403（`sp07-probe.json`）。执行 Profile 本身已通过签名 API 成功发布（`sp07-profile-publication.json`）。

下一轮若要闭环执行链，需要把安装命名空间纳入 Kubernetes 来源范围，并先取得真实事件；不得用测试替身、Fixture 或手工种表代替。

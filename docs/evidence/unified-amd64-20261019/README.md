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

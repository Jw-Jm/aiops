# 统一 amd64 物料与后续安装验证

运行源码候选为 `1a7c7da4af7d74d80df0df0aec72868cbe2785cc`，物料位于开发机 `/opt/ops-verification-20261019-1a7c7da/distribution`。本目录保留构建与原生物料测试，完整大文件留在该独立目录。当前镜像结果不表示签名 Bundle、真实安装或发行通过。

五个 OCI 均已构建；`native-build-receipts.json` 保留首轮失败及最后成功结果。同名组件取最后一个含 imageDigest 的条目。调查服务 42 个 unittest、Runner 六项原生协议测试和 Web 回环 HTTPS/静态页/运行配置/API 不可用映射通过；见对应日志与 JSON。原始失败包括：准备脚本误用 ARM64 分析器摘要、旧缓存复制超时、测试相对目录挂载错误、Docker bridge 缺失、固定回环端口冲突。最后 Web 使用动态空闲回环端口，未修改网络或占用已有端口；临时测试证书不作为正式 TLS 身份。

Web 源码原文与 notice 摘要核对记录于 `web-source-verification.json`；nginx 发布 GPG 签名未验证的原始状态保留。调查服务/Runner 的当前 amd64 有限许可身份仅重新绑定精确镜像/对应源码，所有 1,773 个原 notice 与 ADR 摘要均已复核，依赖身份和条款不变，见 `native-admission-renewal.json`。ARM64 不变。源归档、构建配方和原文共同随后续签名物料交付，不把组件升级或 Fixture 当成延期能力恢复。

下一阶段仍须使用既有签名 Bundle 和独立验签入口，再验证独立环境三阶段安装、真实登录/助手/故障链及实际 Service/PVC 执行链。远程性能问题仅记录，不关闭身份、来源、时间、保留和签名门禁；根目录正式文档与既有用户修改未动。

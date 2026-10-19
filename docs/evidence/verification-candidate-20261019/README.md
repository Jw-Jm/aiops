# 2026-10-19 开发候选与远程数据库验证

功能源码候选：`1a7c7da4af7d74d80df0df0aec72868cbe2785cc`。这是开发快照，不是最终发行提交；机器记录见 [preparation-status.json](preparation-status.json)。未纳入用户原有 `fix-status.md` 的独立改动。

`make check-local` 与 Web production build 通过。由 Git 源码归档在干净目录离线编译 API、Worker、Runner、opsctl、db-migrate 五个 Linux/amd64 二进制，打包两个既有 Chart，并收集当前 Web 静态构建；41 个文件的摘要/大小已逐项复核，见 [candidate-manifest.json](candidate-manifest.json)。本地完整候选保存在 `/Users/mssc/Documents/Code/ops/.artifacts/release-candidates/20261019-1a7c7da`，不把二进制和完整源码复制进 Git。

源归档与测试二进制传至独立远程目录后重新核对 SHA256，在原专用测试 PostgreSQL 端点上，由现有 fixture 创建随机新数据库执行 `TestSP07CommandTransactionAndClaim`。冻结报告/人工关联和受控探测的两个子用例均通过，未跳过。数据库真实运行，Policy、Protector、传输仍使用测试替身；不是实际 Kubernetes、Transit、模型、TCP/HTTP Service 或 PVC 写读验收。详见 [数据库结果](database-verification.json) 和 [测试日志](database-verification.log)。测试数据库含审计记录，按已有保留逻辑保留，未删库或清除 Evidence/Audit。

首次远程镜像清单筛选因目标没有 `rg` 未完成，已中止该读取会话；随后用 Python 筛选的 `ctr -n k8s.io images list -q` 有界读取 15 秒超时，之后未继续查询、重启、扩容或性能调优；根因尚未建立，不直接归因为机器性能。旧签名包不含 Web 且版本为历史源码，不能配上新二进制直接用于当前安装。当前候选尚缺新 OCI、源码/许可证/SBOM 配对与完整签名 Bundle，所以没有直接 Helm 更新或替换既有服务。

后续仍按原 Bundle 机制完成新 OCI 与发行材料、独立验签，再在开发环境验证三阶段安装和实际受控 TCP/HTTP/专用 PVC 场景；不得扩大为业务生产卷或集群级授权。历史失败/未覆盖及人工质量 pending 保留，虚拟化/Trace 延期不变。

候选物料亦已复制到 `/opt/ops-verification-20261019-1a7c7da/candidate`，远程重新核对压缩包 SHA256 和全部 41 个文件摘要/大小，结果 MATCH；见[复制回执](candidate-copy-receipt.json)。复制不激活运行进程，也不赋予签名 Bundle 资格。

# K8sGPT 锁定二进制恢复

2026-11-05，在 `mssc-direct`（192.168.0.63）恢复两个既有准入哈希的原始文件，未修改源码或哈希锁。

- amd64：`/data/ops-k8sgpt-material-20261105/amd64/k8sgpt`，SHA-256 `e6ce7073f35889bc678443034169d4a64e94106442a5775fe3f59ca4ae6b66a4`。从本地保留的 Worker 镜像 `sha256:969af1ca0c53b096b62ca5cf8fd0abebf319b6a9971cc802a017a0f3044df2a8` 中提取 `/opt/ops/bin/k8sgpt`；原镜像 `/usr/share/ops/legal` 同步保存在 `amd64/legal`。
- arm64：`/data/ops-k8sgpt-material-20261105/arm64/k8sgpt`，SHA-256 `6f9152ff31d2692a14e880ae73c2fe35dbc2938560d2c55227f4b0c67409af53`。从本地项目 `build/qualified/k8sgpt` 传输，传输后重新核验。

两份文件都已通过 `docker build --network=none --pull=false --platform=linux/<arch>` 的 scratch 镜像打包检查。amd64 另通过禁网、只读根文件系统、非 root、清空 capabilities 的 `version` 命令检查，配置写入临时 `/tmp`。详细回执见 [recovery.json](recovery.json)。这仅证明文件已恢复、可离线打包；不是从源码重建、真实分析链路或完整 Worker 构建验收。检查镜像不作为发行材料，正式分发仍须附上既有对应源码、闭包锁和许可证材料。

首次打包检查受远程 legacy Docker builder 不支持 `COPY --chmod` 阻塞；对已设为 0555 的文件改用普通 COPY 后通过。首次只读版本检查因 CLI 默认写入 `/.config` 失败；将 HOME/XDG_CONFIG_HOME 指向临时目录后通过。原始失败日志和最终日志保留在远程材料目录。

此前 [r55 门禁记录](../live-gates-r55-20261105/README.md) 中的二进制缺失现已解除，但该历史记录不回写。旧 Analyzer 测试仍硬编码 `GOARCH=arm64`，不能直接在当前 amd64 节点完成原生运行。未改动 `/tmp/ops-sp05-k8sgpt-current-dir`、现有构建输入或集群部署，也未将这些门禁标为通过。使用上述路径时须显式选择目标架构，不能把 arm64 文件用于 amd64 Worker。

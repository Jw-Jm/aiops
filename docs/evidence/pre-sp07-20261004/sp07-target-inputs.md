# SP-07 目标与物料输入准备

日期：2026-10-04。范围仅为输入准备，不实施 Task 7.1～7.6。

用户授权创建本机专属隔离 Linux 目标。目标容器、镜像、内部网络、发现地址、独立 host key 指纹、TrustedUserCAKeys、公钥 principal 与身份验证原始结果见 [SSH 准备证据](ssh-target-preparation.json)及[构建与边界](ssh-target-build-and-boundary.json)。秘密、CA 私钥、登录私钥和账户密码位于仓库外 0700 私有目录。

普通 `opsordinary` 无 sudo 组；`opssudo` 加入 sudo 组，保留 Debian 默认需密码授权；root 使用单独 principal。普通证书登录 root/sudo 身份被拒绝。认证检查使用 `ssh -N`，无远程命令。该专属准备 CA 尚未接入未来平台 OpenBao SSH CA 的正式 Profile，Task 7.5 未验收。

四类未来 ExecutionProfile 的输入状态：

- `k8s_namespace`：已创建自有 namespace、隔离策略、Ready Pod 与 PVC，身份/UID/StorageClass 在[初始事实](kubernetes-target-current-facts.json)。未来命令凭据、版本化 Profile 和具体权限门禁留在 SP-07，当前没有 pods/exec 执行授权。
- `k8s_cluster`：实际 cluster/Node UID 与 kubelet 地址已记录，独立 cluster CA 的 kubelet SAN/链验证退出 0，见 [TLS 控制](kubelet-tls-control.json)。未来 cluster 执行权限没有建立。
- `ssh_user`：专属普通/sudo 账户及各自 CA principal 接入条件已准备。主机唯一身份、正式平台 CA 信任和凭据引用须在下一阶段注册，不按 IP/名称自动绑定。
- `ssh_root`：单独 root principal 的身份认证已验证，普通证书跨身份拒绝。root 命令、风险确认、step-up 与执行授权尚未实施。

调度场景的预期先于操作记录：刻意设置不存在的 selector，Pod Pending/Unschedulable；移除故障条件并重建后新 Pod Running，UID 改变，见[恢复事实](kubernetes-target-recovery-facts.json)。旧 UID 的历史身份保留，不宣称正式 Source/Graph 已自动绑定新对象。事实源不可用必须表示 unavailable/partial，不能解释为恢复。本轮正式 Inspection→Evidence/RCA 最新事实链仍待验收。

下一阶段准入计划：

- Kubernetes Runner：按照正式 Task 7.4 选择最小 Job 镜像、精确工具/版本和目标 API 范围；先取得 manifest/平台 digest、实际 SBOM、对应源码、许可证/notice 与工具闭包，签名离线分发。当前未创建 Runner Job。
- Ansible Runner：按照 Task 7.5 锁定 Python 3.12 兼容的 ansible-runner、ansible-core、SSH 客户端及依赖；实际版本在下一阶段准入时裁决，不能用未锁定版本或本准备目标镜像替代。只通过受控 OpenBao CA/principal 获取短期身份。
- Bash AST：正式方案指定 `mvdan.cc/sh/v3/syntax`；下一阶段锁定精确模块版本、source/checksum 与许可证，覆盖原始 UTF-8 文本/摘要、不改写命令的规则。当前未增加 parser 依赖。
- 每类镜像独立完成来源、架构、原始许可证、SBOM/源码闭包、离线导入及签名验证。目标准备镜像不进入当前运行 Bundle，不记作 Runner/Ansible 分发准入。

性能项目为用户豁免；物理 BMC/DIMM/NIC 无现场目标和证明，继续 unverified。虚拟化 disabled/unverified，PyRCA disabled/excluded。ActionPlan 仅建议，无执行句柄。

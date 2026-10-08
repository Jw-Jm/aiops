# 主分支与开发环境更新授权（2026-10-08）

用户明确要求将修复代码合并 main、清理工作区并推送 GitHub，同时部署刚完成的功能修复、执行必要业务冒烟；不重复全冷装和性能验收。本次按该授权提交此前保留的前置计划、安装/bootstrap/身份/保留/Metrics 实现、生成物、物料锁和完整历史证据，包括失败、未完成项与用户豁免。提交源码不代表完整 R0～R6 验收通过。

实际起点：仓库 `/Users/mssc/Documents/Code/ops/platform`，分支 `pre-sp07/nonvirtual-readiness`，HEAD 与 fetch 后的 `origin/main` 均为 `cd577dfc4e9d1d53f93b5c031d9b0a303ef32dc8`。其他 worktree 保留，不 reset/clean。当前开发安装 namespace 为 `ops-pre-sp07-core-r9-20261008`，UID `8ef6f18e-0621-45d0-877e-4e575cb34603`。本次更新复用该安装的数据、信任及依赖，不运行首次安装或重新初始化，不启用已停用的旧环境。

功能源码及必要回归已由独立审核者明确 PASS，见 [功能修复总结](functional-fixes-20261008.md)；本次额外清理已独立 PASS，见 [清理记录](orbstack-cleanup-followup-r294.md)。新镜像必须重新绑定受测源码并以独立信任验签；旧 R12 不作为新增修复的部署证明。滚动更新与业务冒烟结果随后独立记录。

完整冷装、DeepFlow 准入/运行、原历史 Transit 缺失恢复等未完成项保持原状态。性能与全部 Ollama 实模型验收按用户豁免，不记 PASS、不重跑。KubeVirt/CDI 延期 disabled/unverified，PyRCA disabled/excluded，ActionPlan 仅建议；本次不开始 SP-07。

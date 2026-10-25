# MR-006 本轮自行修复与验收（2026-10-25）

用户授权 Codex 自行完成本轮修复后停止。本轮解决平台二进制离线重建缺口，没有调用或派发 CodeBuddy，没有开展后续业务 fixture 或远程部署。

## 根因校正

r11 报告及此前答复以“99 个运行时模块，第一方需要 148 个，缺 49 个”解释无法重建，该数量比较不足以确定源码缺口。Codex 在独立目录、`GOPROXY=off` 下实际复现：Go 先因缺失依赖图的 `go.mod` 元数据失败，其中包含旧版本元数据；补入经 `go.sum` 验证的元数据后，又发现第一方 `bundle`、`api`、`gen` 等实际编译/嵌入输入未被先前试验完整复制。

本轮两架构实际编译的第三方源码仍为原准入的 **99 个模块**，没有引入新运行依赖。构建补充 **325 份模块图元数据**，每份按 Go 的 go.mod h1 算法与仓库 `go.sum` 核对；第一方输入 **5486 个文件**，来自目标架构 `go list -deps -json` 的 Go/汇编/嵌入文件及原始 `go.mod`、`go.sum`。不需要将生成工具和未编译模块源码一并扩充为运行时准入。

## 实际修改

- `scripts/rebuild-platform-runtime.py`：校验现有 runtime 源归档与架构 Catalog 摘要、准入锁及实际编译模块集合；组合第一方源码和已认证元数据，产出确定性的 `platform-source.tar` 与清单；从产出的归档重新装配任务专属模块缓存，以空编译缓存构建 API、Worker、opsctl，并与仓库相同参数构建结果比较。
- `scripts/test_rebuild_platform_runtime.py`：覆盖元数据被修改时拒绝、归档路径越界拒绝与 Go 模块路径转义。
- `docs/runbooks/platform-runtime-offline-rebuild.md`：可重复执行的双架构准备与重建命令、输入和验收边界。
- `fix-progress.md`：更新 MR-006 的实际本地验收状态。本报告纠正 r11 的原因判断；原失败日志与历史记录保留。

未修改应用 Go 代码、迁移、go.mod/go.sum、既有许可证决策或资格门禁。

## 实测结果

两架构均使用 Go 1.27.1、CGO_ENABLED=0，设置 GOPROXY/GOSUMDB=off、GOVCS=*:off、GOTOOLCHAIN=local，从最初为空的任务模块缓存与编译缓存开始。每个二进制使用 `-mod=readonly -trimpath -buildvcs=false -ldflags="-s -w" -p=2`，与当前仓库构建的 SHA-256 相同。

- arm64：3/3 二进制字节一致，证据 `evidence/r12/offline-rebuild-arm64.json`。
- amd64：3/3 二进制字节一致，证据 `evidence/r12/offline-rebuild-amd64.json`。
- Python 信任边界回归：2/2 通过。
- `go test ./internal/bundle ./cmd/opsctl -count=1`：全部通过。
- `go test ./test/contract -run '^TestDeliveryPreparation' -count=1`：通过。

归档、清单、完整日志和二进制落在仓库忽略目录 `artifacts/sp07-runtime-source/main-review-20261025/r12/{arm64,amd64}/`；准确绝对路径见 `evidence/r12/artifacts.json`。组合源码归档摘要分别为 arm64 `sha256:83afa197b9e3cd18f46b332f3e67dbb43ed282571900f69e1546a7cfba1c7b6c`、amd64 `sha256:4f2dd917290af76ba3c36192d6bb378195452e26c9e5ec0347aa974748b3732c`。这些新产物尚未部署或发布。

## 本轮结束

MR-006 的平台二进制离线重建缺口已修复。原生执行、真实数据库/业务链路、完整 Bundle 安装与公网出口阻断、远程 mssc-direct 验收均未执行，不从本轮交叉编译结果推定通过。按用户要求到此停止。

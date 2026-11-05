# 完整 Worker 离线构建与原生 Analyzer 链路

2026-11-05，在远程 `mssc-direct`（192.168.0.63，linux/amd64）验证。生产 Worker 源码为 `60a9824c3c41d103ed347c8a1da70b69c7f63f8f`；隔离目录 `/data/ops-worker-build-20261105/source` 中另覆盖了本轮两个集成测试文件，生产 Go 源码未改动。既有部署未替换。

## 结果

- 完整 `cmd/platform-worker` 编译成功：Go 1.27.1、CGO 关闭、`-mod=readonly -trimpath -buildvcs=false -ldflags="-s -w"`。使用 `unshare --net` 隔离网络，`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`，复用远程已保留的模块缓存。
- 完整 Worker 镜像打包成功：`ops.local/worker-build-check:20261105-amd64`，摘要 `sha256:08e902b27b34b39f8a792e86d2398898145bd6b00ae1c00c0f7d4c3abfa86483`，scratch 基础、`--network=none --pull=false`、用户 `65532:65532`。包含生产 Worker、锁定 amd64 K8sGPT 和许可证材料。
- 从最终镜像重新提取 Worker、CLI 和分发声明，与构建输入逐项核验。缺配置启动按预期退出 1，证明入口可加载并保留配置校验，不作为服务就绪证明。
- `TestSP05LockedAnalyzerRealOrbStackSandboxAndNativeFaults` 通过（117.20s）：真实 Pod/Node/PVC 固定过滤器、原生 UID/owner、有限 cgroup、只读根文件系统，无 LLM。
- `TestSP05ActualLinuxWorkerAuthorizedAnalyzerDurableChain` 通过（89.55s）：实际 `StartSP04Worker`、授权 broker、原始 CLI、真实故障 Pod、统一摄取、PG Finding/Incident、OpenBao 与角色隔离 TLS/IAM S3，`archivedEvidence=1`。
- K8sGPT adapter 包全量测试和新增未准入文件拒绝测试通过。
- 直接执行修改后的 Worker Dockerfile 哈希校验命令：amd64/arm64 正确文件均接受，跨架构错配两项、空架构、未知架构四项均拒绝，见 `dockerfile-hash-tests.json`。

## 架构与哈希的处理

通用入口不应固定 arm64。Worker Dockerfile 现在要求目标 `TARGETARCH`（BuildKit 自动提供，legacy builder 须显式传入），仅允许 amd64/arm64，并按目标架构核对既有准入哈希。调用者不能自选任意摘要。两个原生活环境测试使用 `runtime.GOARCH`，通过 `OPS_TEST_K8SGPT_BINARY` 指定文件位置，再用生产 `LockedBinaryForArchitecture` 校验；保留旧指针文件作为兼容回退。

固定的已准入摘要属于供应链边界，应继续写入受版本控制的准入规则。历史 `scripts/build-sp05-worker-material.py` 是既有 arm64 源码与工具链材料的重现配方，本轮没有把该配方或其源码闭包声明改成未经验证的通用跨架构构建器。

远程 Docker 与 Kubernetes containerd 镜像库分离。测试增加显式可选的 `OPS_TEST_CONTAINERD_ADDRESS`：将本轮唯一命名的测试镜像从 Docker 保存后导入本地 containerd，仍保持 `imagePullPolicy: Never`，结束时删除该测试镜像引用。不设置时保持原来共享镜像库环境的行为。移除了 legacy Docker 不支持的 COPY chmod 选项，CLI 文件复制仍设 0555，Go 生成的入口为可执行文件。

持久化测试的旧 collector fixture 未声明 namespace，触发既有 `validateCollectorScope` 拒绝。仅在该测试补充与来源注册相同的 `collector.Namespaces = []string{ns}`；生产范围校验未放宽。首次失败回执保留在 `runs/native-worker-live.log`。

## 对应源码与声明

从旧 amd64 Worker 镜像恢复的 notices 原样保留在 `/data/ops-k8sgpt-material-20261105/amd64/legal`；其分发声明实际填写 arm64 二进制/源码摘要，不能直接作为新 amd64 镜像声明。本轮使用既有 amd64 准入锁核验并保留对应源码包：

`/opt/ops-amd64-validation-20261009/bundle-inputs/1e79bee49bab0acf5ba40b0ad732bccb7d9a310105c57aa571d414e001956948-k8sgpt-runtime-source-amd64.tar`

完整包 SHA-256 为 `1e79bee49bab0acf5ba40b0ad732bccb7d9a310105c57aa571d414e001956948`。新镜像从该已认证源码包提取 notices，附 `sp05-k8sgpt-runtime-source-amd64.lock.json` 并生成与实际 amd64 CLI、源码包一致的声明；源码包仍在镜像之外保留，正式发行时必须随附。旧镜像、原始声明、准入哈希和源码归档均未改写。

## 复现和限制

构建打包脚本见 [package.py](package.py)，编译和镜像信息见 [build.json](build.json)，最终内容核验见 [image-verification.json](image-verification.json)。远程保留完整构建目录及日志；本目录不收录凭证、二进制或 492 MB 源码包。

```sh
cd /data/ops-worker-build-20261105/source
unshare --net env GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  /root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.1.linux-amd64/bin/go build \
  -mod=readonly -trimpath -buildvcs=false '-ldflags=-s -w' \
  -o /data/ops-worker-build-20261105/ops-worker ./cmd/platform-worker
python3 /data/ops-worker-build-20261105/package.py
```

活环境测试沿用 r55 的隔离 PG/OpenBao fixture，凭证仅通过原有私有 env 文件载入。新增参数为 `OPS_TEST_K8SGPT_BINARY=/data/ops-k8sgpt-material-20261105/amd64/k8sgpt` 和 `OPS_TEST_CONTAINERD_ADDRESS=/run/containerd/containerd.sock`，并令 PATH 使用上述实际 Go 1.27.1 工具链。最初沙箱测试的镜像经手工导入，持久化测试已覆盖新的自动导入/清理分支。

这证明当前 amd64 完整 Worker 可利用保留依赖离线编译、打包并运行实际业务启动链路；不代表空缓存构建、完整发行 Bundle 或 arm64 原生运行验收。常规多阶段 `build/images/worker/Dockerfile` 因远程未缓存其固定 Go/distroless 基础镜像，未完整执行；本轮验证的是已存在的离线材料路线（主机 Go 编译加 scratch 打包），其哈希门禁另单独执行。未部署新镜像，未同步本轮修改到远程主仓库或 GitHub。

工具链排障也保留在远程工作记录：`/opt/go/bin/go` 底层是 Go 1.26.0，在模块目录自动选择 1.27.1；因此严格离线时直接调用已缓存 1.27.1 工具链。测试网络隔离需要启用 namespace 的 loopback，adapter 测试最终仅启用 lo 后通过；一次误含活环境测试的离线测试筛选失败未当作产品失败或通过证据。

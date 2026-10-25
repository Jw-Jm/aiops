# 平台运行时的源码归档与离线重建

在仓库根目录使用锁定的 Go 1.27.1 和 Python 3，分别准备 Linux arm64/amd64。构建产物放在仓库外：

```sh
task_output=$(mktemp -d /private/tmp/ops-runtime-rebuild.XXXXXX)
for architecture in arm64 amd64; do
  python3 scripts/prepare-runtime-source.py \
    --architecture "$architecture" --out "$task_output/$architecture/upstream"
  python3 scripts/rebuild-platform-runtime.py \
    --architecture "$architecture" \
    --source-archive "$task_output/$architecture/upstream/runtime-go-source.tar" \
    --out "$task_output/$architecture/platform"
done
```

`rebuild-platform-runtime.py` 校验上游源码归档与对应架构的 Catalog 摘要、准入锁及实际编译模块集合一致。它补入实际选中的第一方 Go/汇编/嵌入文件，保留原始 `go.mod`、`go.sum`，并仅加入依赖图内、可由 `go.sum` 验证的 `go.mod` 元数据。工具链和测试依赖的源码不进入运行时编译闭包；不会将未选中的模块声明当作新的已准入运行代码。

输出的 `platform-source.tar` 和 `platform-source.lock.json` 绑定所有第一方输入与元数据。脚本重新读取该归档，在任务专属的空模块缓存、空编译缓存中安装归档内的模块源码和元数据，设置 `GOPROXY=off`、`GOSUMDB=off`、`GOVCS=*:off`、`GOTOOLCHAIN=local`，以 `-mod=readonly -trimpath -buildvcs=false -ldflags="-s -w"` 构建 API、Worker、opsctl。随后与当前仓库使用相同参数构建的二进制逐一比较 SHA-256；不一致即失败。

每个架构的 `offline-rebuild.json` 记录上游归档、组合源码归档、构建参数与三个二进制的实测摘要。`ops-api`、`ops-worker`、`opsctl` 位于同一输出目录。重建需要本机已经安装的 Go 1.27.1 编译器；归档包含对应标准库源码，不包含编译器可执行程序。

这些证据覆盖离线编译与字节一致性。原生执行、数据库前滚、镜像/Bundle 安装和公网出口阻断验收分别记录，不从交叉编译结果推定通过。

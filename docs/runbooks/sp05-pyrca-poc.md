# SP-05 PyRCA PoC（disabled）

只在 `platform/poc/pyrca`、锁定 Python 3.12.14 / uv 0.12.17 下执行：

```sh
uv sync --locked
uv run --locked pytest -q
PYTHONPATH=upstream uv run --locked python adapter.py
```

必须在该目录运行 pytest；不要从仓库根目录递归采集第三方示例测试。
不要在 API/Worker 中安装这个环境。`upstream` 是未修改、逐文件验真的
EpsilonDiagnosis 选入源码，不是一个自研排序内核。

`fixtures/manifest.json` 锁定训练与 holdout 文件 SHA-256、输入窗口和
冻结来源。修改输入需另建版本并先定义预期，不能覆盖旧 holdout 或根据
运行输出反向生成标签。保留原始失败、成功和实际增益结果。

结果为 9 个合成案例、基线 Top-3 100%、PyRCA 66.7%、增益 −33.3 个百分点，
不推广到生产/真实硬件；详见 ADR-0020。所有运行准入条件未满足，因此
`runtimeEnabled=false`。本轮没有追加持续负载、P95、容量或资源基准。

# DeepFlow 独立开发环境配置

仅用于 `192.168.0.63` 的 `ops-assistant-deepflow-20261013` namespace，
release 标签 `assistant-network-20261013`。不修改 core/final 发布配置。
镜像锁、数据库密码、渲染后的 Secret 和原始采集资源保存在远程私有目录，禁止提交。

`agent-defaults.json` 是 Agent 资源上限的唯一默认来源：

- `global.limits.max_memory: 1024`，单位 MiB，即 1 GiB。
- `global.limits.max_millicpus: 1000`，即 1 CPU。
- `render.go` 从同一文件生成 DaemonSet limits；requests 为 CPU 100m、内存 256Mi。

Server 使用 5 秒 × 120 次（最长 10 分钟）的启动探针，保护开发环境冷启动的
数据库迁移及资源 ID 初始化。存活探针使用 5 秒 timeout、10 秒周期、12 次失败
窗口，避免开发主机磁盘暂时阻塞导致重复冷启动；readiness 与 live 来源证明
仍须实际健康响应，不把延长等待计为验收通过。2026-10-14 的慢 I/O
冷启动超过原 5 分钟窗口，保留该失败后调整，不能把启动等待计为健康通过。

MySQL 开发容器上限为 1 GiB、request 256Mi。2026-10-14 原 512Mi 上限下
实测使用 536289280 / 536870912 字节，memory.max 事件 146 次、full pressure
avg10 8.43%，伴随大量读取与启动阻塞，调整该开发配置；不是修改宿主机全局配置。

DeepFlow Server 会按 Agent Group 配置更新 DaemonSet 的上限；只修改
DaemonSet 不能持久解决内存不足。修改资源上限时必须同时渲染部署配置并同步
Agent Group。已有通信等配置保留；Controller 和 Ingester 地址从本 namespace
的 Service 读取，避免把错误的 `127.0.0.1` 下发给 Pod。

在项目目录构建 renderer，然后复制到远程执行；运行操作只在远程进行：

```sh
GOOS=linux GOARCH=amd64 go build -o /tmp/deepflow-dev-render ./scripts/deepflow-dev/render.go
```

远程以当前 checkout 为工作目录（私有 material 目录需预先准备锁定镜像和密码）：

```sh
umask 077
material=/opt/ops-assistant-dev-20261012/deepflow-material
"$material/render" --images "$material/images.json" --password-file "$material/mysql-password" \
  --agent-defaults scripts/deepflow-dev/agent-defaults.json > "$material/install.jsonl"
kubectl apply -f "$material/install.jsonl"
kubectl -n ops-assistant-deepflow-20261013 port-forward service/ops-assistant-deepflow-server 21417:20417 21416:20416
```

在另一个远程终端同步 Agent Group；该脚本核对资源所属 release，合并并回读配置，
重复执行不改变已一致的配置。只通过本机环回 port-forward 访问独立 Controller：

```sh
python3 scripts/deepflow-dev/configure-agent.py --controller http://127.0.0.1:21417
kubectl -n ops-assistant-deepflow-20261013 rollout status daemonset/deepflow-agent --timeout=180s
kubectl -n ops-assistant-deepflow-20261013 get pods -l component=deepflow-agent
```

2026-10-13 已在实际环境回读验证 1024 MiB / 1000 millicpus。验证范围是此开发
实例，不代表全协议、生产兼容性或 Trace 验收。Trace 保持禁用。


真实 HTTP 采集使用 checked-in `network-canaries.json`：两个专属 Deployment
与 Service，锁定离线 command-runner 镜像，只访问本 namespace 的 health/
fail 接口。client 持续循环直至显式停止，不用短循环结束后的容器自动重启
制造身份漂移；旧 canary 的多次重启记录保留，不能当成 Agent 重启。

```sh
kubectl apply -f scripts/deepflow-dev/network-canaries.json
kubectl -n ops-assistant-deepflow-20261013 rollout status deployment/network-client --timeout=180s
```

采集结束只在核对 release 标签与记录 UID 后，缩容这两个 canary Deployment
为 0；不删除证据、受保护存储、发行 namespace 或其他业务资源。


用户于 2026-10-14 明确允许因远程机器配置绕过 DeepFlow 阈值。此独立开发
实例的 `agent-defaults.json` 增加
`global.circuit_breakers.relative_sys_load.trigger_threshold: 0`，只禁用系统
负载熔断；Agent 的 1024 MiB / 1000 millicpus 上限保持不变。
这采用 [锁定源码的官方配置语义](https://raw.githubusercontent.com/deepflowio/deepflow/e567b167453ffa99f08f26def20379b4f831e073/server/agent_config/template.yaml)：阈值 0 支持热更新禁用，
用于避免机械盘 I/O 累积的 load15 与五分钟恢复等待阻塞本机开发采集。
不把此设置传播到 core/final 发行配置；生产阈值另行评估。
同步脚本递归核对指定叶值，保留现有 metric、recovery_threshold 等配置。
绕过只影响 Agent 系统负载保护，不绕过平台原生 UID、范围、权限、来源新鲜度
或非空 L7 证明；仍须以实际数据复验。

## 独立对账探针

`fault-chain-probe.py` 与 `fault-chain-window-probe.py` 是来源对账的独立
oracle：真实记录来自 owned client Pod 内部，探针只读取 DeepFlow 的 D1 元数据
（`tap_side`、`l7_protocol`、`type`、`response_code`、`status`、计数与耗时），
不读取 URL、header 或正文。

- 判据仍是“某单一观测点的完成 HTTP 会话数恰好等于独立记录”，不放宽为近似或
  区间；`--count` 下界 200，`--wait-seconds` 两个脚本统一为 30～600，默认 240。
- 三窗口刺激对每个 HTTP/refused 连接绑定本轮未用的源端口，并记录
  `uniqueClientPorts=1000+refused`。否则短连接复用四元组时，HTTP 请求数不能
  直接作为 L4 记录数的独立 oracle；2026-10-18 实测 1000 响应/962 端口/962 L4。
  保留每请求 5ms 间隔降低峰值，所有精确总体与稳定性复读条件不变。
- 入库是渐进的：首轮为空只记为一次轮询，探针在有界窗口内重试；命中后 10 秒复读
  一次，只有复读完全一致才写 PASS，`ingestLagSeconds` 与 `confirmSeconds` 分开记录。
- 失败必须可归因，`failureClass` 区分
  `collector_blackout_downstream_of_agent_or_ingest`（同 namespace 其它 Pod 也没有
  L7，属于采集/入库空洞）、`namespace_without_pair`（同 namespace 有其它采集，
  pair 没有）、`count_unstable_late_rows`（复读与首读不一致，窗口仍在增长）、
  `count_mismatch`、`group_budget_exceeded` 等；报告同时保留 `pairObservations`、
  `namespaceOtherObservations` 与 `agentRestarts` 前后值。
- 一次源错误不会让三窗口探针丢失报告：`sourceErrors` 记录失败轮次，`stable`
  明确是否经过复读确认，`previousMeasurements` 保留上一轮测量。
- Agent 重新附加 eBPF 期间没有任何 L7 记录，两个探针都不会把它读成“0 个 5xx”；
  采集空洞只能等下一次独立窗口，不能用历史或放宽判据补。

```sh
python3 scripts/deepflow-dev/fault-chain-probe.py --report reports/http-proof.json --count 1000
python3 scripts/deepflow-dev/fault-chain-window-probe.py --report reports/window-proof.json
python3 -m unittest discover -s scripts/deepflow-dev -p 'test_*.py'
```

# 开发测试入口

本地功能回归与真实环境验收独立运行。工具链版本沿用 `.tool-versions`/`make check-toolchain`，前端依赖先用 `pnpm --dir web install --frozen-lockfile` 安装，Python 依赖先用 `cd services/investigator && uv sync --locked --group dev` 准备。依赖安装可能需要已准入镜像源或缓存；测试不启动远程服务，不自动采集真实来源。

## 日常本地开发

- `make test-unit`：仅 Go `cmd/internal/gen` 包。排除已登记的容量测试、外部业务输入校验和受隔离限制的原生 CLI 探测；显式空 build tags，避免继承 live tags。输出 `artifacts/test-reports/unit.txt`，保留 Go 测试失败退出码。
- `make test-contract`：本地 Contract；排除上游真实回放和 20K 容量用例，即使环境留有回放启用变量也不会执行。输出 `contract.txt`。
- `make test-web`：TypeScript、组件和 SSE 协议测试；不运行 Playwright。
- `make test-python`：Investigator 的完整 pytest，以及 DeepFlow、助手评估和采集/长上下文验证脚本的 unittest。`uv run --offline --locked --group dev` 不在测试时在线更新依赖，也不访问真实模型。
- `make test-local`：上述四项加已有本地安全测试。`make check-local` 再加全仓 `go vet`；vet 只静态检查 Integration，不执行它。
- `make check`：在 `check-local` 外保留生成物与锁定运行源码检查。源码材料过期时仍须按原流程更新，不能把入口拆分当作完整交付通过。

本地套件可能使用回环地址上的进程内 HTTP/TLS Fixture 或已安装 Helm 工具，不要求数据库、集群、模型、浏览器和真实来源。被排除的专项没有执行，不能从本地成功报告推导为验收 PASS。新增真实环境/容量用例时，须更新相应分组选取和入口合同测试。

## 独立专项

- `make test-integration`：数据库、归档及运行机制集成。需要各用例既有环境；独立来源专项、OIDC 浏览器运行和容量用例排除，两个子进程辅助测试仍由父用例调用。报告 `integration.jsonl`。
- `make test-live-sources`：当前助手 Kubernetes 原生观测、DeepFlow L4/L7 和无模型故障链专项。使用各用例的原有来源、对象、采集与授权前置；不自动创建对象、扩权或采集。报告 `live-sources.jsonl`。
- `make test-native`：真实业务值与原生 K8sGPT CLI 的既有专项，要求专属输入和隔离运行限制。报告 `native.jsonl`。已有 `pre_sp07_live`/`pre_sp07_metrics_live`/`sp05_diagnostic` 历史测试仍需原有显式 tags 与专属流程，不被本地入口启用。
- `make test-browser-runtime`：助手实际 OIDC/API/Worker/model/MCP 与浏览器交互场景，需原有 `OPS_ASSISTANT_RUNTIME_ACCEPTANCE`、来源回执及私有配置。报告 `browser-runtime.jsonl`。
- `make test-web-e2e`：Playwright，必须提供 `OPS_WEB_QA_INPUT` 和各场景配置。JSON 报告默认 `artifacts/test-reports/web-e2e.json`，可用 `OPS_WEB_QA_REPORT` 指定本轮专属绝对路径。运行前清除该输出文件，拒绝沿用旧报告；跳过、空套件、失败、flaky 或全局错误均失败。
- `make test-capacity`：既有本地 20K 图构建/上游黄金输入、200 节点查询 P95 与大批重建期间查询测试。报告 `capacity.jsonl`。这是专项测量，不进入日常功能回归，也不等同生产容量。
- `make test-capacity-live`：原有两 Pod 真实来源功能加 4×25 查询的组合用例，以及已安装平台十调查并发用例（启用原有 `pre_sp07_live` tag）。前者没有拆改断言；因其同时测容量，整体归入此入口。要求采集回执、专属 VM/VL/数据库和已安装平台前置；报告 `capacity-live.jsonl`。两类容量入口均拒绝 `OPS_PERFORMANCE_EXEMPTION`，不能用历史性能豁免生成新容量 PASS。
- `make test-replay`：真实锁定上游回放，保留原有启用变量和材料门禁；报告 `replay.jsonl`。
- `make test-e2e`：非虚拟化安装/健康等原有场景；报告 `e2e.jsonl`。继续排除延期 KubeVirt 用例，不恢复 VM 开发或验收。

Go 专项继续通过 `check-test-report.py` 拒绝 skip/空报告，测试进程失败即失败。报告来自当前命令；相同入口会覆盖自己的默认报告，独立轮次须另行保存证据，不并发使用同一报告路径。环境未具备应记录 NOT_RUN/受限，不降低门禁。本轮整理不连接远程、不执行上述真实环境或容量专项，也不处理远程性能。

## 本轮验证（2026-10-18）

`make check-local` 通过：Go 单元、Contract、全仓 vet、77 项前端组件测试、5 项 SSE 测试、93 项 Investigator pytest、25 项脚本 unittest、13 项安全测试。入口合同用隔离目录中的假 Go/pnpm 执行器验证跳过、空套件、失败、完整报告和过期浏览器报告的处理，不访问真实环境。容量豁免拦截另有入口回归。

未执行 `make check` 的生成物/运行源码材料门禁，未运行数据库集成、真实来源、浏览器、回放或容量测试；本地通过不替代这些验收。

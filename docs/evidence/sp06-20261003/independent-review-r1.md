# SP-06 首轮完整独立审核：FAIL

审核者 `/root/sp06_independent_review`，未参与实现，全程只读；没有写文件、运行数据库/集群/模型副作用测试或读取外部私有凭据。对象为原 r8 全部授权交付（基线 `1c748b5b1f0bf33fb54774c2068a735abe2be599`、分支 `sp06/nonvirtual-investigation`、Bundle `sp06-nonvirtual-20261004-r8`），不是仅最后 diff。下述行号均对应修复前内容。范围覆盖 Task 6.1～6.6、§14.6、正式 Contract、真实链路、部署、复用、分发、保留闭包及证据。

- **IR-01 / P1 / 真实缺陷**：`internal/investigation/mcp/server.go:129`、`repository.go:480`、`recovery.go:33`。已注册 D0 Context 在原 D0+D1 Job 上仍可通过 MCP、Steps、Step 恢复及缓存模型取得 D1 Evidence。违反 Context 数据等级绑定、续签不得扩权及 Task 6.2/6.3/6.5。复现应使用真实归档 D1 Evidence、正式签名 D0 Context，覆盖四条路径并保留合法 D0 正例。
- **IR-02 / P1 / 真实缺陷**：`internal/httpapi/investigation_handlers.go:261`。一次读取 64 个事件后，token 到期及当前 Role/Source 检查仅发生在整批后；每条低于三秒的慢客户端可在中途失权后继续取得后续事件。违反根 07 §32 和 Task 6.6。复现应在同一批内到期/撤 Role/撤 Source，断言停止后续业务事件，保留正常续传和慢客户端截止。
- **IR-03 / P2 / 真实缺陷**：`internal/investigation/recovery.go:33`、`allocation.go:96`、`repository.go:237`。Step 恢复、缓存模型和内部 Steps 不增加 ResultBytes，重复读取可超过 Job/Profile/Policy 输出限制。违反根 09 §51.3。下调预算、提交成功结果、重复或并发恢复，验证 Ledger/Audit 原子记账、耗尽后 partial、Tool Calls 不增加、成功工具不重执行。
- **IR-04 / P2 / 真实缺陷**：`internal/httpapi/investigation_handlers.go:57,158`、`internal/investigation/repository.go:101`。预算错误返回 409 而非根 07 §33 的 429；创建同 key 不同请求返回 REPLAY_REJECTED 而非根 07 §31 的 IDEMPOTENCY_CONFLICT；取消 key 未持久绑定请求，可取消两个 Job。验证预算不足、创建冲突、跨 Job 取消键、同请求重放与撤权后重放；真正 MCP nonce 重放仍保留 REPLAY_REJECTED。
- **IR-05 / P2 / 必要物料一致性缺口**：`docs/poc/holmes-investigator-reuse-lock.yaml:8`、`docs/runbooks/sp06-investigation-runtime.md:107`。锁仍记录旧 `8c7e…` 镜像和 `cda77…` 源码，而受测 Catalog/admission 为 `419164…` / `2c302…`。保留历史证据，同步当前精确锁并检查与 Catalog/admission/Bundle 一致，相关检查应拒绝不同摘要。

审核者独立接受的原 r8 证据：24 个选中门禁原始 SHA256 和退出码全部匹配；SP06 integration 14 pass/0 fail/0 skip，SP04 2/0/0，ListWatch 1/0/0；开始审核时 4526 个 runtime 绑定和 5271 个完整快照文件全部匹配；197 个已安装 Holmes Python 文件与官方 publisher sdist 逐字节一致；1069 个原始 notice SHA 全部匹配；完整源码归档与 admission 一致。原生日志含真实模型/标准 SDK MCP、10 个并发调查与主链路保护、CNI 撤回及离线重装后的新调查。上述成功证据不能覆盖 IR-01～05。

性能用户豁免，不计通过；虚拟化继续延期 disabled/unverified；PyRCA disabled/excluded。外部 DeepFlow/硬件现场、正向原生 Metrics-server及 RCA 准确率未验证的表述诚实，不构成新增缺陷或扩大范围建议。没有另一个已确认但未告知的范围内问题。

首轮结论 **FAIL**，五项全部开放；修复、必要回归及新内容冻结后须最终完整独立复审。主代理后续修复不改变本历史审核结论。

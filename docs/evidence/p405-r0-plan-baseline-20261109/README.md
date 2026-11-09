# R0 修复：静态进度账目与冻结设计基线解耦

## 问题

`docs/plans/ui-v12-replication-20261021/validate-plan.py` 第 91/92 行把 **当前已实现分类计数**
与 **冻结设计基线计数** 强制相等：

```
require(sum(c["status"].startswith("已有") ...) == base["existing_contract_operations"] == 119, ...)
require(sum(c["status"] == "拟新增" ...) == base["proposed_contract_operations"] == 60, ...)
```

`baseline.json` 的 119/60 是 **2026-10-21 设计冻结时** 的分类快照（该快照在 git `378112c`
处仍为 179/119/60，可复核）。C-075 的 PATCH 在 `7c336fe` 落地后由「拟新增」转为「已有-适配」，
当前真实分类为 **179 / 120 已有 / 59 拟新增**。

因此该检查把「实现进度推进」误判为「账目损坏」，属于 **静态进度与冻结基线耦合**，
不是 API 缺失，也不是分类错误。

## 修复原则（最小且不削弱门禁）

1. **179 唯一 contract** 与 **method/path 唯一** 校验保留不动。
2. **冻结基线身份** 固定为 `FROZEN_EXISTING = 119` / `FROZEN_PROPOSED = 60`（写死在脚本里的常量），
   `baseline.json` 若被改成 118/61 之类的自洽但漂移值，`baseline_errors()` 直接报错。
   另加基线自洽：`existing + proposed == contract_operations`。
3. 引入 `contract_errors(rows, frozen, operations)` 统一判定：
   - 逐行分类必须与 **实际 OpenAPI 存在性** 一致（沿用原 `exists` 判定，未削弱）；
   - 当前清单必须 **全量已分类**（`existing + proposed == 行数`）；
   - 已有项数 **不得低于** 冻结基线（实现进度只增不减）；
   - 拟新增项数 **不得高于** 冻结基线（不得凭实现反向扩大未实现面）。
4. 新增 **自反检查**（三项各自独立断言）：
   - `baseline_errors({118, 61})` **必须** 报错 —— 真正改动冻结基线数值会被拒绝
     （上轮只翻转首行分类，证明的是分类错误，不是基线漂移）；
   - 把首行分类对调后 `contract_errors()` **必须** 报错 —— 错误分类仍被拒绝；
   - 当前清单 `contract_errors()` **必须** 不报错 —— 正常实现进度可通过。

   分类计数由冻结基线（119/60）推进到当前实现（120/59）是被允许的；被拒绝的是
   **冻结基线数值本身被改动**、**分类与实际 OpenAPI 不符**、**行未全量分类** 三类漂移。

## 证据

| 命令 | 结果 |
| --- | --- |
| `python3 validate-plan.py`（修复后） | `"status": "passed"`，`checks_passed: 5084`，`contracts: 179`，`tasks: 39`，`slices: 458`，`errors: []` |
| `python3 validate-progress.py` | 通过（`g0_passed: true`，`g0_blockers: []`） |
| 将 C-075 分类由「已有-适配」改回「拟新增」后 `python3 validate-plan.py` | `"status": "failed"`，`C-075 existing/proposed classification` —— 错误分类仍被拒绝 |
| 还原后 `python3 validate-plan.py` | `"status": "passed"`，`checks_passed: 5084` |

上轮 README 写的 `4725` 与实测 `5083` 不符，是把更早一次运行的数字抄进了表格；
本表按当前脚本实际输出重写。

冻结基线数值 119/60 不再是可被“自洽地”改写的量：脚本内的常量与 `baseline.json`
必须一致，否则校验直接失败。

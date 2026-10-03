# ADR-0020：SP-05 PyRCA 增益裁决与运行排除

Status: Accepted（disabled 裁决；不构成 SP-05 整体验收）
Date: 2026-10-02

依据 10 Task 5.8、正式准入条件和 ADR-0019，本轮只评估候选排序增益。
用户豁免性能专项，并未豁免 PyRCA 的任何启用条件。

固定源码 Salesforce/PyRCA `411310d589fac5cb8e7bdce67d33eadb091a1083`
（sfr-pyrca/1.0.1，BSD-3-Clause），不改动 EpsilonDiagnosis 算法。
仅选取该算法的源码导入闭包，在独立 Python 3.12 PoC 中执行；没有服务、
业务数据库或事实源客户端，也没有 Investigator、LLM 或命令执行接口。
全部原始选入文件及 SHA-256 在第三方 admission 中逐文件记录。

训练窗口与评估窗口严格分离。输入为手工定义且在适配器之前冻结的版本化
Metric Evidence holdout 与确定性 Candidate，固定种子、alpha、bootstrap
次数和 Top-3，不根据 holdout 标签调参。只能返回既有候选的排序，保持原始
状态，不能生成 confirmed。这里的版本化合成 holdout 并非生产 Evidence/v2
归档证明，不能冒充真实硬件或生产指标运行验收。

九个合成案例的确定性 Top-3 为 9/9，PyRCA 为 6/9，绝对增益为
−33.333 个百分点，未达到至少 +5 个百分点；新增错误 confirmed 为 0。
DIMM 为 2/3、PVC/CSI 为 3/3、Node 为 1/3。基线在该小样本上存在天花板，
此结果仅支持本轮不启用，不支持一般效果或生产安全性结论。

完整 PyRCA 分发依赖（包括 scikit-learn<1.2）未取得锁定 Python 3.12
运行准入。PoC 的 13 个精确 Python distribution、锁文件、原始许可证/
notice 和原始选入代码单独保存；不将 PoC 闭包当作完整产品闭包。
资源、延迟和专门性能项未测，按用户指令豁免本轮执行，但保持准入未验证。

裁决：**PyRCA disabled**；API、Worker、Investigator 运行镜像和 Bundle
选入物料均排除 Python/PyRCA 及其 PoC 依赖。未来启用需另有正增益、完整
依赖/分发裁决、资源与延迟、Profile 及真实运行证据；本轮裁决不自动恢复。

原始结果：[pyrca-gain-decision.json](../evidence/sp05-20261002/pyrca-gain-decision.json)。
可复现命令与边界：[Runbook](../runbooks/sp05-pyrca-poc.md)。

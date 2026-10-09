# 首轮独立只读审核：NOT PASS

审核子代理 sp07_final_readonly_review 未参与实现，严格只读复核完整 Task7.1～7.6。确认两个阻断：

1. P1：OpenBao 成功响应固定1MiB，正式10MiB输出的JSON/Transit响应超过此边界，导致归档失败；归档循环首次错误立即return阻塞后续。native-persistence-audit-final.json原25条执行中9条未核验归档，证据真实保留。
2. P2：Task7.5错误known_hosts的实际负例缺失，只有正确hostkey真实正向和证书边界单测。

主代理先补review-transit-large-response-red.log（exit1），再将encrypt/decrypt读取边界按既有20MiB输入的base64扩张和受限envelope确定；其他OpenBaoAPI仍1MiB，命令10MiB输出/900秒等正式阈值不变。归档同批遇错继续其他记录，保留错误并执行受限清理。green exit0，受影响回归exit0。修复经r9签名build/verify/import和真实ARM64滚动部署rev12；现网28条终态全部核验归档，10MiB输出SSE重算归档digest相符，积压自动恢复，只重试可恢复归档，未重试不确定命令。

主代理在自有隔离Linux目标临时替换HostKey（不改变CA/principal/known_hosts/Profile），通过正式API执行新确认的标记命令。StrictHostKeyChecking探针255明确HOST IDENTIFICATION HAS CHANGED；原生Ansible连接拒绝，execution_unknown/exitCode null，标记不存在。随后原私有/public hostkey逐字节恢复，独立keyscan一致，恢复后正向业务冒烟另记。首轮客户端误断言unknown有255退出码、第二轮遇滚动更新连接中断均保留失败；修正验收断言并在部署完成后串行负例成功，无放宽hostkey验证或自动重发旧执行。

除上述两项，首轮未发现其他确定范围内产品缺陷；阶段授权、虚拟化排除、模型功能/准确率区分、性能豁免和历史检查失败分类正确。修复后交回同一独立审核者复核。

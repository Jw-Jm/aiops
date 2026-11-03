# r26：MiniMax真实连接验证（2026-10-27）

用户提供MiniMax-M3.1-Flash及精确Anthropic Messages地址，授权验证与接续真实业务Worker。凭据已通过关闭回显的SSH标准输入写到任务private文件0600，不进入源码、命令行参数、日志或报告。

在ssh mssc-direct执行一次最小真实请求，使用系统TLS校验、禁重定向、禁代理、不发送业务数据。结果HTTP402 / insufficient_balance_error，请求ID见connectivity.json。未得到模型回答，不重复尝试，不宣称Key认证/模型准入/业务Worker通过。

现有模型合同存在独立适配缺口：Go Profile只允许内部/v1；Python ModelContract只允许内部OpenAI协议llama3.1:8b-16k；Profile Schema固定zero-charge及自托管权重/许可证/评测/准入摘要。MiniMax外部Anthropic计费服务不能套用这些材料。官方文档确认该模型支持Anthropic格式和工具消息，文档不代表项目准入，不能伪造权重或零费用证明。

没有改动运行模型Profile或放宽门禁，30471仍为r25 Web-r5，API/Worker final和PG15454/schema70不改。第一项真实业务Worker未完成。用户需先核对Key所属账户在该模型/接口的余额、套餐额度或调用权限。额度恢复后用新证据文件续验，随后按实际协议/费用/来源证据补充版本合同与适配；不重跑init/bootstrap或覆盖本次失败。

原始Key仅远程 `/data/ops-v12-native-visual-r24-20261026/private/minimax-m31-api-key`；下一轮直接从该文件读取，不再向用户索要或输出。完整合同定位及边界见result.json。

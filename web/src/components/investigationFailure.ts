const failures: Record<string, string> = {
  MODEL_UNAVAILABLE: "模型暂不可用；仍可查看故障、证据和规则诊断结果。",
  MODEL_TIMEOUT: "模型请求超时；请核对已保存的步骤和结果。",
  MODEL_AUTHENTICATION_FAILED:
    "模型服务认证失败，请联系平台管理员核对连接配置。",
  MODEL_CONTEXT_LIMIT: "证据超过模型上下文限制；请缩小调查范围。",
  MODEL_RATE_LIMITED: "模型服务限制了请求频率，请稍后重试。",
  MODEL_REQUEST_REJECTED: "模型服务拒绝此次请求，请联系平台管理员核对配置。",
  MODEL_FAILURE: "模型调用失败；请核对已保存的步骤和结果。",
  INVALID_MODEL_JSON: "模型返回的内容不符合结果格式，未形成可用的调查结论。",
  BUDGET_EXHAUSTED: "最近一次调查已用尽额度；请核对已有结果与调查预算",
  TIME_BUDGET_EXHAUSTED: "最近一次调查已超过时间限制；请核对已完成的步骤",
  AUTHORIZATION_REVOKED: "最近一次调查的访问授权已撤销，请核对当前权限",
  UNKNOWN_OUTCOME: "最近一次调查步骤的结果不确定，请核对持久记录",
};
export function investigationFailure(code: string) {
  return failures[code];
}

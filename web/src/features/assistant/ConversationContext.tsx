import { Alert, Space, Tag, Typography } from "antd";

function object(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

// Display only known context fields; arbitrary graph attributes and model text
// cannot become target identities here. The API reauthorizes this binding.
export function ConversationContext({
  binding,
  domain,
  cluster,
}: {
  binding: Record<string, unknown>;
  domain: string;
  cluster?: string;
}) {
  const scope = object(binding.executionScope);
  const context = object(binding.resourceContext);
  const window = object(context.timeRange || binding.timeRange);
  const namespaces = Array.isArray(scope.namespaces)
    ? scope.namespaces.filter(
        (value): value is string => typeof value === "string",
      )
    : [];
  const targets = Array.isArray(context.resources)
    ? context.resources.map(object)
    : [];
  return (
    <Alert
      type="info"
      title="本会话调查范围"
      description={
        <Space orientation="vertical">
          <Space wrap>
            <Tag>{domain === "audit" ? "管理员审计域" : "运维授权域"}</Tag>
            {cluster && <Typography.Text>集群：{cluster}</Typography.Text>}
            {domain !== "audit" && (
              <Typography.Text>
                命名空间：{namespaces.join("、") || "无命名空间资源"}
                {scope.clusterScoped === true ? "；含授权集群级资源" : ""}
              </Typography.Text>
            )}
            {typeof binding.timezone === "string" && (
              <Typography.Text>时区：{binding.timezone}</Typography.Text>
            )}
          </Space>
          {targets.map(
            (target, index) =>
              typeof target.canonicalId === "string" && (
                <Typography.Text key={index}>
                  资源：{String(target.kind || "")}{" "}
                  {String(target.namespace || "集群级")}/
                  {String(target.name || "")} · {target.canonicalId}
                </Typography.Text>
              ),
          )}
          {typeof window.from === "string" && typeof window.to === "string" && (
            <Typography.Text>
              调查时间：{window.from} ～ {window.to}（绝对时间）
            </Typography.Text>
          )}
          <Typography.Text type="secondary">
            本轮使用已确认的范围和资源身份。可明确调整后续轮次的目标、范围或时间；查询中的“现在”需要重新读取事实。
          </Typography.Text>
        </Space>
      }
    />
  );
}

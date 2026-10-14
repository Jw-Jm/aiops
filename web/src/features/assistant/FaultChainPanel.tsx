import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  List,
  Space,
  Tag,
  Timeline,
  Typography,
} from "antd";
import { Link } from "react-router-dom";
import { checked, requestOptions } from "../../api/client";
import { getAssistantFaultChainContext } from "../../api/generated/platform";
import type { AssistantAnswerV1 } from "../../api/generated/models";
import { useAuth } from "../../auth/AuthProvider";
import { QueryState } from "../../components/QueryState";
import { AssistantEvidence } from "./AssistantEvidence";
import { QueryResultView } from "./QueryResultView";

export function FaultChainPanel({
  answer,
  turnId,
}: {
  answer: AssistantAnswerV1;
  turnId: string;
}) {
  const { session } = useAuth();
  const scopeKey = JSON.stringify([
    session?.tenantId,
    session?.subject,
    session?.roles,
    session?.clusterScopes,
    session?.namespaceScopes,
  ]);
  const [selected, setSelected] = useState(0);
  const query = useQuery({
    queryKey: ["assistant", scopeKey, "fault-chain", turnId],
    enabled: !!session,
    queryFn: ({ signal }) =>
      checked(getAssistantFaultChainContext(turnId, requestOptions(signal))),
  });
  const context = query.data?.data;
  const edge = context?.edges[selected] || context?.edges[0];
  return (
    <QueryState
      loading={query.isPending}
      error={query.error}
      retry={() => void query.refetch()}
    >
      {context && (
        <Card title="故障链证据调查">
          <Space orientation="vertical" style={{ width: "100%" }}>
            <Alert
              type="info"
              title="依赖和异常是调查线索；故障传播仍待因果验证。"
            />
            <Typography.Text>调查观测时间：{context.asOf}</Typography.Text>
            <Timeline
              items={[
                ...(context.request.baseline
                  ? [
                      {
                        content: `基线：${context.request.baseline.from} 至 ${context.request.baseline.to}`,
                      },
                    ]
                  : [{ content: "基线未指定，变化量不可用", color: "gray" }]),
                {
                  content: `故障：${context.request.fault.from} 至 ${context.request.fault.to}`,
                  color: "orange",
                },
                ...(context.request.recovery
                  ? [
                      {
                        content: `恢复：${context.request.recovery.from} 至 ${context.request.recovery.to}`,
                      },
                    ]
                  : [{ content: "恢复未验证", color: "gray" }]),
                {
                  content: `调查完成：${context.asOf}（窗口顺序不代表传播顺序）`,
                  color: "gray",
                },
              ]}
            />
            <Space wrap>
              {context.edges.map((item, i) => (
                <Button
                  key={item.relationDigest}
                  type={i === selected ? "primary" : "default"}
                  aria-pressed={i === selected}
                  onClick={() => setSelected(i)}
                >
                  链路 {i + 1} · {item.relation.kind}
                </Button>
              ))}
            </Space>
            {!edge && (
              <Alert
                type="warning"
                title="未取得可引用的范围内关系，不能构造传播链。"
              />
            )}
            {edge && (
              <>
                <Space wrap>
                  <Link
                    to={`/resources/detail?canonicalId=${encodeURIComponent(edge.relation.from)}`}
                  >
                    {edge.relation.from}
                  </Link>
                  <Typography.Text>→</Typography.Text>
                  <Link
                    to={`/resources/detail?canonicalId=${encodeURIComponent(edge.relation.to)}`}
                  >
                    {edge.relation.to}
                  </Link>
                </Space>
                <Typography.Text>
                  调用方向：
                  {edge.callDirection === "unknown"
                    ? "未验证"
                    : edge.callDirection}
                </Typography.Text>
                <Tag color="orange">
                  时间先后：
                  {edge.temporalAssessment === "unreliable"
                    ? "未验证"
                    : edge.temporalAssessment}
                </Tag>
                <Typography.Text>
                  窗口比较：
                  {edge.comparisonStatus === "comparable"
                    ? "口径一致"
                    : edge.comparisonStatus === "coverage_insufficient"
                      ? "覆盖不足"
                      : "缺少基线"}
                </Typography.Text>
                <List
                  dataSource={edge.observations}
                  renderItem={(observation) => (
                    <List.Item>
                      <Space orientation="vertical" style={{ width: "100%" }}>
                        <Typography.Text>{observation.reason}</Typography.Text>
                        <Typography.Text type="secondary">
                          规则：{observation.ruleId}
                        </Typography.Text>
                        {observation.supports.length > 0 && (
                          <>
                            <Typography.Text>支持证据</Typography.Text>
                            <AssistantEvidence refs={observation.supports} />
                          </>
                        )}
                        {observation.conflicts.length > 0 && (
                          <>
                            <Typography.Text>冲突证据</Typography.Text>
                            <AssistantEvidence refs={observation.conflicts} />
                          </>
                        )}
                      </Space>
                    </List.Item>
                  )}
                />
                {edge.queryResultRefs.map((id) => (
                  <QueryResultView key={id} id={id} answer={answer} />
                ))}
                <Typography.Text type="secondary">
                  关系观测时间：{edge.relation.observedAt}
                  ；历史窗口内是否成立仍待核验。
                </Typography.Text>
                <Typography.Text strong>待补证据</Typography.Text>
                <List
                  size="small"
                  dataSource={edge.missingEvidence}
                  renderItem={(item) => <List.Item>{item}</List.Item>}
                />
                <Typography.Text strong>下一步只读检查</Typography.Text>
                <List
                  size="small"
                  dataSource={edge.nextChecks}
                  renderItem={(item) => <List.Item>{item}</List.Item>}
                />
              </>
            )}
            {context.missingEvidence.length > 0 && (
              <Alert
                type="warning"
                title={context.missingEvidence.join("；")}
              />
            )}
          </Space>
        </Card>
      )}
    </QueryState>
  );
}

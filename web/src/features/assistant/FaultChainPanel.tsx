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

const missingLabels: Record<string, string> = {
  causal_mechanism_unverified: "故障因果机制尚未验证",
  clock_and_event_time_unverified: "来源时钟与事件时间尚未验证",
  historical_relation_unverified: "历史窗口内的依赖关系尚未验证",
  change_source_not_checked: "尚未取得合格变更证据",
  change_causal_mechanism_unverified: "变更与异常的因果机制尚未验证",
  shared_resource_cause_not_checked: "尚未核验共享资源异常",
  shared_resource_causal_mechanism_unverified:
    "共同资源与症状的因果机制尚未验证",
  recovery_baseline_and_health_unverified:
    "完整恢复尚未核验：需要同口径基线、合格覆盖和相关健康证据",
  network_window_statistics: "缺少相应窗口的网络统计",
  committed_scoped_resource_relations: "缺少已提交的范围内资源关系",
};

export function FaultChainPanel({
  answer,
  turnId,
  onReadOnlyCheck,
}: {
  answer: AssistantAnswerV1;
  turnId: string;
  onReadOnlyCheck?: (message: string) => void;
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
        <Card title="故障链证据调查" style={{ overflowWrap: "anywhere" }}>
          <Space orientation="vertical" style={{ width: "100%" }}>
            <Alert
              type="info"
              title="依赖和异常是调查线索；故障传播仍待因果验证。"
            />
            <Tag color="green">证据已提交；模型解释使用独立轮次</Tag>
            {onReadOnlyCheck && (
              <Button
                onClick={() =>
                  onReadOnlyCheck(
                    "请通过 get_assistant_context 读取已授权的历史故障链上下文和统计引用，解释已提交的支持、冲突与缺口。保留原窗口、数字和规则结论，不推断时间先后或确认根因。",
                  )
                }
              >
                准备解释这些证据
              </Button>
            )}
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
                ...(edge?.relation.observedAt
                  ? [
                      {
                        content: `所选关系观测：${edge.relation.observedAt}（历史窗口内是否成立仍待核验）`,
                        color: "gray",
                      },
                    ]
                  : []),
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
                        {observation.resourceHealth && (
                          <>
                            <Link
                              to={`/resources/detail?canonicalId=${encodeURIComponent(observation.resourceHealth.canonicalId)}`}
                            >
                              健康观测资源：
                              {observation.resourceHealth.canonicalId}
                            </Link>
                            <Typography.Text>
                              已提交状态：Ready=
                              {observation.resourceHealth.conditionStatus}
                              ；观测时间：
                              {observation.resourceHealth.observedAt}
                            </Typography.Text>
                            <Typography.Text type="secondary">
                              状态步骤：{observation.resourceHealth.stepId}
                              ；来源版本：
                              {observation.resourceHealth.sources
                                .map(
                                  (source) =>
                                    `${source.sourceRegistrationId}@${source.revision}`,
                                )
                                .join("、")}
                            </Typography.Text>
                          </>
                        )}
                        {observation.relatedRelations?.map((path) => (
                          <Typography.Text
                            key={`${path.from}:${path.kind}:${path.to}`}
                          >
                            共享关系：{path.from} → {path.to}（{path.kind}
                            ；观测：{path.observedAt}）
                          </Typography.Text>
                        ))}
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
                <a href="#assistant-binding-editor">
                  通过现有编辑器调整调查范围
                </a>
                <List
                  size="small"
                  dataSource={edge.missingEvidence}
                  renderItem={(item) => (
                    <List.Item>
                      <Space wrap>
                        <span>{missingLabels[item] || item}</span>
                        {onReadOnlyCheck &&
                          [
                            "shared_resource_cause_not_checked",
                            "recovery_baseline_and_health_unverified",
                          ].includes(item) && (
                            <Button
                              size="small"
                              onClick={() =>
                                onReadOnlyCheck(
                                  `请使用已发布的 get_resource_health 只读检查本会话授权范围内的目标及依赖自身状态，核对历史故障链缺口 ${item}。当前快照不能补造历史窗口健康证明。`,
                                )
                              }
                            >
                              准备只读健康检查
                            </Button>
                          )}
                        {item.includes("scope") && (
                          <a href="#assistant-binding-editor">
                            编辑现有调查范围
                          </a>
                        )}
                      </Space>
                    </List.Item>
                  )}
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
                title={context.missingEvidence
                  .map((item) => missingLabels[item] || item)
                  .join("；")}
              />
            )}
          </Space>
        </Card>
      )}
    </QueryState>
  );
}

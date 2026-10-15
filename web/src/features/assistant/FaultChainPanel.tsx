import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  Drawer,
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
import {
  FaultChainGapCheck,
  type FaultChainCheckControls,
} from "./FaultChainGapCheck";
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
  checkControls,
  anchorPrefix,
}: {
  answer: AssistantAnswerV1;
  turnId: string;
  onReadOnlyCheck?: (message: string) => void;
  checkControls?: FaultChainCheckControls;
  anchorPrefix?: string;
}) {
  const { session } = useAuth();
  const scopeKey = JSON.stringify([
    session?.tenantId,
    session?.subject,
    session?.roles,
    session?.clusterScopes,
    session?.namespaceScopes,
  ]);
  const viewId = anchorPrefix || turnId;
  const [originOpen, setOriginOpen] = useState(false);
  const [selected, setSelected] = useState(0);
  const [focusedFact, setFocusedFact] = useState<{
    queryResultId: string;
    factId: string;
  }>();
  function locateQuery(id: string) {
    const element = document.getElementById(`fault-chain-${viewId}-${id}`);
    element?.focus();
    element?.scrollIntoView?.({ block: "nearest" });
  }
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
        <Card
          id={`fault-chain-${viewId}`}
          title="故障链证据调查"
          style={{ overflowWrap: "anywhere" }}
        >
          <Space orientation="vertical" style={{ width: "100%" }}>
            <Alert
              type="info"
              title="依赖和异常是调查线索；故障传播仍待因果验证。"
            />
            {context.request.readOnlyCheck && (
              <Alert
                type="info"
                title={`只读检查：${context.request.readOnlyCheck.tool}`}
                description={
                  <>
                    <Button size="small" onClick={() => setOriginOpen(true)}>
                      查看原调查轮次与缺口
                    </Button>
                    {context.request.readOnlyCheck.relationDigest && (
                      <div>
                        原关系：{context.request.readOnlyCheck.relationDigest}
                      </div>
                    )}
                    <div>
                      原缺口：
                      {missingLabels[context.request.readOnlyCheck.gapCode] ||
                        context.request.readOnlyCheck.gapCode}
                    </div>
                    <div>
                      检查目标：{context.request.readOnlyCheck.canonicalId}
                      ；原缺口不会自动关闭，当前快照不能替代历史证明。
                    </div>
                  </>
                }
              />
            )}
            {context.request.readOnlyCheck && (
              <Drawer
                title="原调查已提交证据与缺口"
                open={originOpen}
                onClose={() => setOriginOpen(false)}
                width="min(960px, 100vw)"
              >
                {originOpen && (
                  <FaultChainPanel
                    answer={answer}
                    turnId={context.request.readOnlyCheck.originTurnId}
                    anchorPrefix={`${viewId}-origin`}
                  />
                )}
              </Drawer>
            )}
            {context.request.readOnlyCheck?.tool === "get_resource_health" &&
              !context.resourceHealthChecks?.length && (
                <Alert
                  type="warning"
                  title="未取得可引用的合格健康快照；保留原缺口。"
                />
              )}
            {context.resourceHealthChecks?.map((health) => (
              <Card
                key={`${health.stepId}:${health.canonicalId}`}
                size="small"
                title="只读健康检查结果"
              >
                <Link
                  to={`/resources/detail?canonicalId=${encodeURIComponent(health.canonicalId)}`}
                >
                  {health.canonicalId}
                </Link>
                <div>
                  Ready={health.conditionStatus}；观测：{health.observedAt}
                </div>
                <div>
                  已提交步骤：{health.stepId}；来源：
                  {health.sources
                    .map(
                      (source) =>
                        `${source.sourceRegistrationId}@${source.revision}`,
                    )
                    .join("、")}
                </div>
              </Card>
            ))}
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
                        content: (
                          <Button
                            type="link"
                            style={{
                              whiteSpace: "normal",
                              height: "auto",
                              textAlign: "left",
                            }}
                            onClick={() => {
                              document
                                .getElementById(
                                  `fault-chain-${viewId}-relation`,
                                )
                                ?.focus();
                              if (edge.queryResultRefs[0])
                                locateQuery(edge.queryResultRefs[0]);
                            }}
                          >
                            所选关系观测：{edge.relation.observedAt}
                            （历史窗口内是否成立仍待核验）
                          </Button>
                        ),
                        color: "gray",
                      },
                    ]
                  : []),
                ...(edge?.observations.flatMap((observation, index) =>
                  observation.resourceHealth
                    ? [
                        {
                          content: (
                            <Button
                              type="link"
                              style={{
                                whiteSpace: "normal",
                                height: "auto",
                                textAlign: "left",
                              }}
                              onClick={() => {
                                const element = document.getElementById(
                                  `fault-chain-${viewId}-observation-${index}`,
                                );
                                element?.focus();
                                element?.scrollIntoView?.({ block: "nearest" });
                              }}
                            >
                              健康观测：{observation.resourceHealth.observedAt}{" "}
                              · {observation.resourceHealth.canonicalId} Ready=
                              {observation.resourceHealth.conditionStatus}
                              （定位已提交状态与来源）
                            </Button>
                          ),
                          color: "gray",
                        },
                      ]
                    : [],
                ) || []),
                {
                  content: `调查完成：${context.asOf}（窗口顺序不代表传播顺序）`,
                  color: "gray",
                },
              ]}
            />
            <Space wrap aria-label="调查节点">
              {[
                ...new Set(
                  context.edges.flatMap((item) => [
                    item.relation.from,
                    item.relation.to,
                  ]),
                ),
              ].map((node) => (
                <Button
                  key={node}
                  style={{
                    whiteSpace: "normal",
                    height: "auto",
                    maxWidth: "100%",
                  }}
                  onClick={() => {
                    setSelected(
                      context.edges.findIndex(
                        (item) =>
                          item.relation.from === node ||
                          item.relation.to === node,
                      ),
                    );
                    setFocusedFact(undefined);
                  }}
                >
                  定位节点 {node}
                </Button>
              ))}
            </Space>
            <Space wrap>
              {context.edges.map((item, i) => (
                <Button
                  key={item.relationDigest}
                  style={{ whiteSpace: "normal", height: "auto" }}
                  type={i === selected ? "primary" : "default"}
                  aria-pressed={i === selected}
                  onClick={() => {
                    setSelected(i);
                    setFocusedFact(undefined);
                  }}
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
                <Space id={`fault-chain-${viewId}-relation`} tabIndex={-1} wrap>
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
                <Typography.Text type="secondary">
                  关系摘要：{edge.relationDigest}；来源：
                  {edge.relation.provenance?.sourceRegistrationId || "未披露"}
                  ；规则版本：
                  {edge.relation.provenance?.ruleVersion || "未披露"}
                </Typography.Text>
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
                  renderItem={(observation, index) => (
                    <List.Item>
                      <Space orientation="vertical" style={{ width: "100%" }}>
                        <Typography.Text
                          id={`fault-chain-${viewId}-observation-${index}`}
                          tabIndex={-1}
                        >
                          {observation.reason}
                        </Typography.Text>
                        <Typography.Text type="secondary">
                          规则：{observation.ruleId}
                        </Typography.Text>
                        {observation.factRefs.map((ref) => (
                          <Button
                            key={`${ref.queryResultId}:${ref.factId}`}
                            size="small"
                            style={{ whiteSpace: "normal", height: "auto" }}
                            onClick={() => {
                              setFocusedFact(ref);
                              locateQuery(ref.queryResultId);
                            }}
                          >
                            定位统计 Fact {ref.factId}
                          </Button>
                        ))}
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
                          <Space
                            wrap
                            key={`${path.from}:${path.kind}:${path.to}`}
                          >
                            <span>共享关系：</span>
                            <Link
                              to={`/resources/detail?canonicalId=${encodeURIComponent(path.from)}`}
                            >
                              {path.from}
                            </Link>
                            <span>→</span>
                            <Link
                              to={`/resources/detail?canonicalId=${encodeURIComponent(path.to)}`}
                            >
                              {path.to}
                            </Link>
                            <span>
                              （{path.kind}；观测：{path.observedAt}）
                            </span>
                          </Space>
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
                        {checkControls && (
                          <FaultChainGapCheck
                            key={`${edge.relationDigest}:${item}`}
                            context={context}
                            edge={edge}
                            gap={item}
                            turnId={turnId}
                            controls={checkControls}
                          />
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
            {(edge?.queryResultRefs || []).map((id) => (
              <QueryResultView
                key={id}
                id={id}
                answer={answer}
                anchorId={`fault-chain-${viewId}-${id}`}
                focusFactId={
                  focusedFact?.queryResultId === id
                    ? focusedFact.factId
                    : undefined
                }
              />
            ))}
            {context.queryResultRefs
              .filter(
                (id) =>
                  !context.edges.some((item) =>
                    item.queryResultRefs.includes(id),
                  ),
              )
              .map((id) => (
                <div key={id}>
                  <Alert
                    type="info"
                    title="该已提交统计未匹配本轮关系；按原查询目标、对端及窗口独立展示，不能据此构造链路。"
                  />
                  <QueryResultView
                    id={id}
                    answer={answer}
                    anchorId={`fault-chain-${viewId}-${id}`}
                  />
                </div>
              ))}
            {checkControls &&
              context.missingEvidence.includes(
                "committed_scoped_resource_relations",
              ) && (
                <FaultChainGapCheck
                  context={context}
                  gap="committed_scoped_resource_relations"
                  turnId={turnId}
                  controls={checkControls}
                />
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

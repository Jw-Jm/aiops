import { useState } from "react";
import { Alert, Button, Select, Space } from "antd";
import { checked, requestOptions } from "../../api/client";
import { collectAssistantFaultChain } from "../../api/generated/platform";
import type {
  AssistantConversationV1,
  AssistantTurnV1,
  AssistantFaultChainContextV1,
  AssistantFaultChainContextV1EdgesItem,
  AssistantFaultChainRequestV1ReadOnlyCheckTool,
} from "../../api/generated/models";

export type FaultChainCheckControls = {
  conversation: AssistantConversationV1;
  disabled: boolean;
  onSubmitted: (turn: AssistantTurnV1) => void;
};
const tools: Record<string, AssistantFaultChainRequestV1ReadOnlyCheckTool> = {
  shared_resource_cause_not_checked: "get_resource_health",
  recovery_baseline_and_health_unverified: "get_resource_health",
  committed_scoped_resource_relations: "get_dependencies",
  historical_relation_unverified: "get_dependencies",
  network_window_statistics: "execute_analysis_query",
  complete_collector_coverage: "execute_analysis_query",
};
const labels = {
  get_resource_health: "执行只读健康检查",
  get_dependencies: "执行只读依赖检查",
  execute_analysis_query: "执行冻结窗口统计检查",
};
export function FaultChainGapCheck({
  context,
  edge,
  gap,
  turnId,
  controls,
}: {
  context: AssistantFaultChainContextV1;
  edge?: AssistantFaultChainContextV1EdgesItem;
  gap: string;
  turnId: string;
  controls: FaultChainCheckControls;
}) {
  const [target, setTarget] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [error, setError] = useState<Error>();
  const tool = tools[gap];
  if (
    !tool ||
    context.request.readOnlyCheck ||
    (tool === "execute_analysis_query" &&
      edge?.relation.kind !== "communicates_with")
  )
    return null;
  const root = context.request.targetCanonicalId;
  const peer = edge
    ? edge.relation.from === root
      ? edge.relation.to
      : edge.relation.from
    : root;
  const healthTargets = [
    ...new Set([
      root,
      ...(edge ? [edge.relation.from, edge.relation.to] : []),
      ...(edge?.observations || []).flatMap((o) => [
        ...(o.resourceHealth ? [o.resourceHealth.canonicalId] : []),
        ...(o.relatedRelations || []).flatMap((r) => [r.from, r.to]),
      ]),
    ]),
  ];
  async function submit() {
    setBusy(true);
    setError(undefined);
    try {
      const result = await checked(
        collectAssistantFaultChain(
          controls.conversation.conversationId,
          {
            message: `只读检查故障链缺口：${gap}`,
            expectedConversationRevision: controls.conversation.revision,
            parentTurnId: turnId,
            faultChainRequest: {
              ...context.request,
              readOnlyCheck: {
                originTurnId: turnId,
                ...(edge ? { relationDigest: edge.relationDigest } : {}),
                gapCode: gap,
                tool,
                canonicalId:
                  tool === "execute_analysis_query"
                    ? peer
                    : tool === "get_dependencies"
                      ? root
                      : target || root,
                ...(tool === "execute_analysis_query"
                  ? {
                      direction:
                        edge?.callDirection === "incoming"
                          ? ("incoming" as const)
                          : edge?.callDirection === "outgoing"
                            ? ("outgoing" as const)
                            : ("either" as const),
                    }
                  : {}),
              },
            },
          },
          requestOptions(undefined, true),
        ),
      );
      setSubmitted(true);
      controls.onSubmitted(result.data);
    } catch (cause) {
      setError(cause instanceof Error ? cause : new Error("只读检查提交失败"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Space orientation="vertical">
      <Space wrap>
        {tool === "get_resource_health" && (
          <Select
            aria-label={`健康检查目标 ${gap}`}
            value={target || root}
            disabled={controls.disabled || busy || submitted}
            options={healthTargets.map((value) => ({ value, label: value }))}
            onChange={setTarget}
            style={{ minWidth: 160, maxWidth: "100%" }}
          />
        )}
        <Button
          size="small"
          disabled={controls.disabled || busy || submitted}
          loading={busy}
          onClick={() => void submit()}
        >
          {labels[tool]}
        </Button>
      </Space>
      {submitted && (
        <Alert type="info" title="检查已提交到独立轮次；原缺口保留。" />
      )}
      {error && (
        <Alert
          type="error"
          title={error.message}
          description={<a href="#assistant-binding-editor">检查现有调查范围</a>}
        />
      )}
    </Space>
  );
}

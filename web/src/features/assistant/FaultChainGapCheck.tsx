import { useEffect, useRef, useState } from "react";
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
  // epoch identifies the page scope this control was mounted for. The page
  // advances it on every scope, conversation, binding or turn change, so an
  // answer of an older epoch is never accepted, even when the same
  // conversation id is shown again.
  epoch: number;
  onSubmitted: (turn: AssistantTurnV1, epoch: number) => void;
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
  // The request belongs to this mounted check: a conversation switch or an
  // unmount cancels it, so a late answer cannot be attributed to another
  // conversation. The controller of the running request is captured before the
  // call; the effect replaces the ref with a fresh controller for the next
  // conversation and aborts the captured one, so an old answer can never pass
  // the check of a new controller.
  const pending = useRef(new AbortController());
  // The live epoch of the mounted scope. A running request compares its own
  // epoch with this one, so an answer, an error or a busy release of an older
  // scope never touches the current state.
  const epochRef = useRef(controls.epoch);
  epochRef.current = controls.epoch;
  useEffect(() => {
    const controller = new AbortController();
    pending.current = controller;
    setBusy(false);
    setSubmitted(false);
    setError(undefined);
    return () => controller.abort();
    // The state belongs to the mounted scope: a new conversation or a new page
    // epoch starts from a clean state.
  }, [controls.conversation.conversationId, controls.epoch]);
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
    // The ownership of this request: the controller, the epoch and the
    // conversation it was started for.
    const controller = pending.current;
    const epoch = controls.epoch;
    const owns = () =>
      !controller.signal.aborted &&
      pending.current === controller &&
      epoch === epochRef.current;
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
          requestOptions(controller.signal, true),
        ),
      );
      if (!owns()) return;
      setSubmitted(true);
      controls.onSubmitted(result.data, epoch);
    } catch (cause) {
      // A failed or cancelled request of an older epoch must not show an error
      // on the current state.
      if (!owns()) return;
      setError(cause instanceof Error ? cause : new Error("只读检查提交失败"));
    } finally {
      // Only the request that still owns the state may release the busy flag.
      if (owns()) setBusy(false);
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

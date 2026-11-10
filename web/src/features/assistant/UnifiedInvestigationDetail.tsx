import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Descriptions, Drawer, Space, Tag } from "antd";
import {
  getAssistantTurn,
  getInvestigation,
  getInvestigationSteps,
} from "../../api/generated/platform";
import type {
  AssistantAnswerV1FormalRefsItem,
  InvestigationJobV2,
} from "../../api/generated/models";
import { checked, requestOptions } from "../../api/client";
import { QueryState } from "../../components/QueryState";
import { StepLedger } from "../investigation/StepLedger";
import { BudgetSummary } from "../investigation/BudgetSummary";
import { AssistantEvidence } from "./AssistantEvidence";
import { FormalReferenceView } from "./FormalReferenceView";
import { list, record } from "../../components/data";

// The unified entry shows the same public investigation contract the incident
// workspace shows: the tool names and safe step states, the committed
// summaries, the budget actually granted and consumed, and the real job
// state. It never renders prompts, raw arguments, model claims or tokens.
const stateLabels: Record<string, string> = {
  queued: "已排队",
  running: "执行中",
  partial: "部分完成",
  succeeded: "已完成",
  failed: "已失败",
  cancelled: "已取消",
  expired: "已过期",
};

function stateTag(state: string) {
  if (state === "succeeded") return <Tag color="green">{stateLabels[state]}</Tag>;
  if (state === "failed" || state === "expired")
    return <Tag color="red">{stateLabels[state] || state}</Tag>;
  if (state === "partial") return <Tag color="gold">{stateLabels[state]}</Tag>;
  if (state === "cancelled") return <Tag>{stateLabels[state]}</Tag>;
  return <Tag color="blue">{stateLabels[state] || state}</Tag>;
}

// A job that is still running is refreshed with a bounded poll until it
// reaches a terminal state. The poll only reads; it never starts a job.
const LIVE_STATES = ["queued", "running", "cancel_requested"];
const LIVE_POLL_MS = 2000;

export function UnifiedInvestigationDetail({
  jobId,
  turnId,
  scopeKey,
  onClose,
  onStop,
}: {
  jobId?: string;
  turnId?: string;
  // The trusted scope of the page that opened the detail. The detail never
  // derives its own scope: the selected cluster and the authorization
  // generation of the page are part of the cache key, so a scope or account
  // change drops this detail together with the rest of the page.
  scopeKey: string;
  onClose: () => void;
  onStop?: (target: {
    jobId: string;
    turnId: string;
    conversationId: string;
  }) => void;
}) {
  const [copied, setCopied] = useState(false);
  // A history entry may only carry the turn. The turn is read through the
  // same authorization as every other conversation read, and it resolves the
  // job the turn actually produced. The turn is only read when the job is
  // unknown: a known job id must never trigger a turn request with an
  // undefined turn id.
  const turnEnabled = !jobId && !!turnId;
  const turn = useQuery({
    queryKey: ["assistant", scopeKey, "investigation-turn", turnId],
    enabled: turnEnabled,
    queryFn: ({ signal }) =>
      checked(getAssistantTurn(turnId!, requestOptions(signal))),
  });
  const turnData = turn.data?.data;
  const resolvedJobId = jobId || turnData?.jobId;
  const resolvedTurnId = turnId || turnData?.turnId;
  const job = useQuery({
    queryKey: ["assistant", scopeKey, "investigation", resolvedJobId],
    enabled: !!resolvedJobId,
    queryFn: ({ signal }) =>
      checked(getInvestigation(resolvedJobId!, requestOptions(signal))),
    refetchInterval: (query) => {
      const state = (
        query.state.data as { data?: InvestigationJobV2 } | undefined
      )?.data?.state;
      return state && LIVE_STATES.includes(state) ? LIVE_POLL_MS : false;
    },
  });
  // The steps of a running job are refreshed by the state of the job itself.
  // The step list carries no job state, so the poll cannot be derived from the
  // step payload; it follows the job and stops with it.
  const jobState = (job.data?.data as InvestigationJobV2 | undefined)?.state;
  const jobLive = !!jobState && LIVE_STATES.includes(jobState);
  const jobTerminal = !!jobState && !jobLive;
  const steps = useQuery({
    queryKey: ["assistant", scopeKey, "investigation-steps", resolvedJobId],
    enabled: !!resolvedJobId,
    queryFn: ({ signal }) =>
      checked(getInvestigationSteps(resolvedJobId!, requestOptions(signal))),
    refetchInterval: jobLive ? LIVE_POLL_MS : false,
  });
  // The last poll of a running job may still miss the step that closed it.
  // A terminal job reads its final steps and its final consumed budget once
  // more and then stops.
  const stepsRefetchRef = useRef(steps.refetch);
  stepsRefetchRef.current = steps.refetch;
  const finalStepsRead = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (!jobTerminal || !resolvedJobId) return;
    if (finalStepsRead.current === resolvedJobId) return;
    finalStepsRead.current = resolvedJobId;
    void stepsRefetchRef.current();
  }, [jobTerminal, resolvedJobId]);
  const data = job.data?.data as InvestigationJobV2 | undefined;
  const stepList = steps.data?.data || [];
  const result = record(data?.result);
  const evidenceRefs = list(result.evidenceRefs).filter(
    (id): id is string => typeof id === "string" && id.length > 0,
  );
  const formalRefs = list(result.formalRefs).filter(
    (item): item is AssistantAnswerV1FormalRefsItem =>
      !!item &&
      typeof item === "object" &&
      typeof (item as AssistantAnswerV1FormalRefsItem).kind === "string" &&
      typeof (item as AssistantAnswerV1FormalRefsItem).revision === "number",
  );
  const running =
    !!data && LIVE_STATES.includes(data.state);
  const effectiveTurnId = data?.turnId || resolvedTurnId;
  // The conversation that produced the job. The stop confirmation is bound to
  // it, so a detail of another conversation can never be stopped through the
  // conversation that is shown now.
  const effectiveConversationId =
    data?.conversationId || turnData?.conversationId;
  return (
    <Drawer
      title="调查过程"
      open
      onClose={onClose}
      width="min(900px, 100vw)"
      extra={
        <Space>
          {data && stateTag(data.state)}
          {data?.jobId && (
            <Button
              size="small"
              onClick={() => {
                void navigator.clipboard
                  ?.writeText(data.jobId)
                  .then(() => setCopied(true))
                  .catch(() => setCopied(false));
              }}
            >
              {copied ? "已复制作业号" : "复制作业号"}
            </Button>
          )}
          {running && onStop && resolvedJobId && (
            <Button
              danger
              onClick={() =>
                onStop({
                  jobId: resolvedJobId,
                  turnId: effectiveTurnId || resolvedTurnId || "",
                  conversationId: effectiveConversationId || "",
                })
              }
            >
              停止调查
            </Button>
          )}
          <Button onClick={onClose}>关闭</Button>
        </Space>
      }
    >
      <QueryState
        loading={turn.isLoading || job.isLoading}
        error={turn.error || job.error}
        retry={() => {
          // Only the queries that were actually asked for are retried. A
          // known job id never reads a turn, so a retry must not send a turn
          // request with an undefined turn id.
          if (turnEnabled) void turn.refetch();
          if (resolvedJobId) void job.refetch();
        }}
      >
        {data && (
          <Space orientation="vertical" size="middle" style={{ width: "100%" }}>
            <Descriptions
              column={1}
              size="small"
              items={[
                {
                  key: "state",
                  label: "真实状态",
                  children: (
                    <Space wrap>
                      {stateTag(data.state)}
                      {data.errorCode && (
                        <span>错误码：{data.errorCode}</span>
                      )}
                    </Space>
                  ),
                },
                {
                  key: "focus",
                  label: "调查重点",
                  children: data.focus || "未指定",
                },
                {
                  key: "jobId",
                  label: "调查作业",
                  children: data.jobId,
                },
                {
                  key: "turn",
                  label: "来源轮次",
                  children: effectiveTurnId || "未提供",
                },
                {
                  key: "conversation",
                  label: "来源会话",
                  children: data.conversationId || "未提供",
                },
              ]}
            />
            <BudgetSummary job={data} />
            <section aria-label="公开步骤">
              <h3>公开步骤</h3>
              <QueryState
                loading={steps.isLoading}
                error={steps.error}
                retry={() => void steps.refetch()}
                empty={stepList.length === 0}
              >
                <StepLedger steps={stepList} />
              </QueryState>
            </section>
            {evidenceRefs.length > 0 && (
              <section aria-label="已提交证据">
                <h3>已提交证据</h3>
                <AssistantEvidence refs={evidenceRefs} />
              </section>
            )}
            {formalRefs.length > 0 && effectiveTurnId && (
              <section aria-label="固定结论版本">
                <h3>固定结论版本</h3>
                <Space orientation="vertical" size="small">
                  {formalRefs.map((ref, index) => (
                    <FormalReferenceView
                      key={`${ref.kind}-${ref.revision}-${index}`}
                      refValue={ref}
                      index={index}
                      turnId={effectiveTurnId}
                    />
                  ))}
                </Space>
              </section>
            )}
            {data.state === "partial" && (
              <Alert
                type="warning"
                title="本轮只提交了已核实的事实，未确认根因。"
              />
            )}
            {data.state === "failed" && (
              <Alert
                type="error"
                title="本轮调查失败，已提交的事实保留，未确认根因。"
              />
            )}
            {data.state === "cancelled" && (
              <Alert
                type="info"
                title="本轮调查已取消，已提交的事实保留，未确认根因。"
              />
            )}
            {data.state === "expired" && (
              <Alert
                type="info"
                title="本轮调查已过期，已提交的事实保留，未确认根因。"
              />
            )}
          </Space>
        )}
      </QueryState>
    </Drawer>
  );
}

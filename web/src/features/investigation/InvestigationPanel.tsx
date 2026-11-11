import { investigationFailure } from "../../components/investigationFailure";
import { stateLabel } from "../../components/presentation";
import { useState, useRef, useEffect } from "react";
import { useQuery, useQueryClient, useMutation } from "@tanstack/react-query";
import { Alert, Button, Input, Space, Table } from "antd";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  createInvestigation,
  getInvestigation,
  getInvestigationSteps,
  cancelInvestigation,
  getIncidentInvestigations,
} from "../../api/generated/platform";
import type {
  IncidentV2,
  InvestigationSuccessV2,
  InvestigationStepsSuccessV2,
} from "../../api/generated/models";
import { checked, requestOptions } from "../../api/client";
import { record, list, text, displayTime } from "../../components/data";
import { QueryState } from "../../components/QueryState";
import { Details } from "../../components/Details";
import { Status } from "../../components/Status";
import { BudgetSummary } from "./BudgetSummary";
import { StepLedger } from "./StepLedger";
import { useInvestigationStream } from "./useInvestigationStream";
import { useAuth } from "../../auth/AuthProvider";
import { ConfirmDialog } from "../../components/v12/Layer";
export function InvestigationPanel({
  incident,
  onEvidence,
  assistantHref,
  creationBlocked = false,
}: {
  incident: IncidentV2;
  onEvidence?: (id: string) => void;
  assistantHref?: string;
  creationBlocked?: boolean;
}) {
  const navigate = useNavigate();
  const { session, nativeSession } = useAuth();
  const [stopTarget, setStopTarget] = useState<{
    jobId: string;
    identity: string;
    key: string;
  }>();
  const [url, setUrl] = useSearchParams();
  const client = useQueryClient();
  const [input, setInput] = useState("");
  const [focus, setFocus] = useState("");
  const [tightenDuration, setTightenDuration] = useState("");
  const [tightenCalls, setTightenCalls] = useState("");
  const [inputError, setInputError] = useState("");
  const [existingNotice, setExistingNotice] = useState(false);
  const lastRefresh = useRef(0);
  const historyCursor = url.get("historyCursor") || undefined;
  const timeline = useQuery({
    queryKey: ["investigation-history", incident.incidentId, historyCursor],
    queryFn: ({ signal }) =>
      checked(
        getIncidentInvestigations(
          incident.incidentId,
          { limit: 20, cursor: historyCursor },
          requestOptions(signal),
        ),
      ),
  });
  const history = list(record(timeline.data).data).map(record);
  const jobId = url.get("jobId") || undefined;
  const stopIdentity = `${incident.incidentId}:${jobId}:${session?.subject}:${nativeSession?.sessionRef ?? ""}`;
  useEffect(() => setStopTarget(undefined), [stopIdentity]);
  const job = useQuery({
    queryKey: ["investigation", jobId],
    enabled: !!jobId,
    queryFn: ({ signal }) =>
      checked(getInvestigation(jobId!, requestOptions(signal))),
    refetchInterval: (q) =>
      ["queued", "running", "cancel_requested"].includes(
        (q.state.data as InvestigationSuccessV2 | undefined)?.data.state || "",
      )
        ? 5000
        : false,
  });
  const data = (job.data as InvestigationSuccessV2 | undefined)?.data;
  const historicalReadOnly = !!data && data.subject !== session?.subject;
  const steps = useQuery({
    queryKey: ["investigation-steps", jobId],
    enabled: !!jobId,
    queryFn: ({ signal }) =>
      checked(getInvestigationSteps(jobId!, requestOptions(signal))),
    refetchInterval:
      data && ["queued", "running"].includes(data.state) ? 5000 : false,
  });
  const choose = (id: string) => {
    const n = new URLSearchParams(url);
    n.set("jobId", id);
    setUrl(n);
  };
  // A unified entry job (investigation-job/v3) carries the conversation and
  // the turn that produced it. The history opens the read-only unified detail
  // of exactly that conversation, turn and job; it never starts another job
  // and never re-runs a model.
  //
  // The link identifiers are absent for a job without an assistant binding, so
  // their presence is checked on the raw value. The display helper answers
  // with a placeholder for a missing value and would mark every row as
  // unified.
  const linkValue = (value: unknown): string | undefined =>
    typeof value === "string" && value.length > 0 ? value : undefined;
  const openUnified = (row: Record<string, unknown>) => {
    if (!assistantHref) return;
    const conversationId = linkValue(row.conversationId);
    const turnId = linkValue(row.turnId);
    const jobId = linkValue(row.jobId);
    if (!conversationId || !turnId || !jobId) return;
    const next = new URLSearchParams(assistantHref.split("?")[1] || "");
    next.set("conversation", conversationId);
    next.set("investigationJob", jobId);
    next.set("investigationTurn", turnId);
    navigate(`/assistant?${next.toString()}`);
  };
  const tighteningInputError = (): string | undefined => {
    const duration = tightenDuration.trim();
    const calls = tightenCalls.trim();
    if (duration) {
      const v = Number(duration);
      if (!Number.isInteger(v) || v < 1 || v > 600)
        return "时长收紧必须是 1–600 的整数秒";
    }
    if (calls) {
      const v = Number(calls);
      if (!Number.isInteger(v) || v < 1 || v > 40)
        return "工具调用收紧必须是 1–40 的整数";
    }
    return undefined;
  };
  const create = useMutation({
    mutationFn: () => {
      // 只收紧：输入值只能低于服务端额度，高于额度的值由服务端按额度截回。
      const duration = tightenDuration.trim();
      const calls = tightenCalls.trim();
      const tightening: { durationSeconds?: number; toolCalls?: number } = {};
      if (duration) tightening.durationSeconds = Number(duration);
      if (calls) tightening.toolCalls = Number(calls);
      return checked(
        createInvestigation(
          incident.incidentId,
          {
            triggerRevision: incident.revision,
            triggerKind: "manual",
            ...(focus.trim() ? { focus: focus.trim() } : {}),
            ...(duration || calls ? { tightening } : {}),
          },
          requestOptions(undefined, true),
        ),
      );
    },
    onSuccess: (res) => {
      setInputError("");
      const d = record(record(res).data);
      setExistingNotice(
        !["queued", "running", "cancel_requested"].includes(text(d.state)),
      );
      if (typeof d.jobId === "string") choose(d.jobId);
      void client.invalidateQueries({
        queryKey: ["investigation-history", incident.incidentId],
      });
    },
  });
  const cancel = useMutation({
    mutationFn: (target: { jobId: string; identity: string; key: string }) =>
      checked(
        cancelInvestigation(
          target.jobId,
          {},
          requestOptions(undefined, true, target.key),
        ),
      ),
    onSuccess: (_, target) => {
      setStopTarget((current) =>
        current?.key === target.key ? undefined : current,
      );
      void client.invalidateQueries({
        queryKey: ["investigation", target.jobId],
      });
    },
  });
  const stream = useInvestigationStream(
    jobId,
    !!data && ["queued", "running", "cancel_requested"].includes(data.state),
    () => {
      if (Date.now() - lastRefresh.current > 1000) {
        lastRefresh.current = Date.now();
        void client.invalidateQueries({ queryKey: ["investigation", jobId] });
        void client.invalidateQueries({
          queryKey: ["investigation-steps", jobId],
        });
      }
    },
  );
  return (
    <>
      <Alert
        type="info"
        showIcon
        title="智能调查会读取证据，不会执行命令"
        description="调查用于整理证据和提出可能原因。处置命令需要你在人工处置中单独输入并确认。页面关闭后，可从历史记录继续查看进度。"
      />
      {assistantHref && (
        <p className="status-note">
          新调查和追问会绑定当前故障，并保存在助手会话中。下方保留历史调查记录。
        </p>
      )}
      {!assistantHref && (
        <details className="inline-details status-note">
          <summary>本次调查重点与收紧（可选）</summary>
          <Space direction="vertical" style={{ maxWidth: 480 }}>
            <Input.TextArea
              aria-label="调查重点"
              value={focus}
              onChange={(e) => setFocus(e.target.value)}
              maxLength={500}
              showCount
              placeholder="例如：优先核对内存通道与节点运行环境的关系"
              autoSize={{ minRows: 2, maxRows: 4 }}
            />
            <Space wrap>
              <Input
                aria-label="收紧时长秒"
                value={tightenDuration}
                onChange={(e) => setTightenDuration(e.target.value)}
                placeholder="收紧时长（秒，1–600）"
                style={{ width: 180 }}
                inputMode="numeric"
              />
              <Input
                aria-label="收紧工具调用次数"
                value={tightenCalls}
                onChange={(e) => setTightenCalls(e.target.value)}
                placeholder="收紧工具调用（1–40）"
                style={{ width: 180 }}
                inputMode="numeric"
              />
            </Space>
            <span className="muted">
              只能收紧不能放宽：填写的值高于服务端额度时按服务端额度执行。同一故障版本下不同收紧或重点会生成新的调查任务。
            </span>
          </Space>
        </details>
      )}
      {inputError && (
        <Alert
          showIcon
          type="error"
          title="调查请求未提交"
          description={inputError}
          className="status-note"
        />
      )}
      <Space wrap className="status-note">
        <Button
          type="primary"
          disabled={creationBlocked}
          loading={create.isPending}
          onClick={() => {
            if (assistantHref) {
              navigate(assistantHref);
              return;
            }
            const e = tighteningInputError();
            setInputError(e ?? "");
            if (!e) create.mutate();
          }}
        >
          开始智能调查
        </Button>
        <details className="inline-details">
          <summary>通过编号查找调查</summary>
          <Space>
            <Input
              aria-label="持久调查 Job ID"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder="输入完整调查编号"
              style={{ width: 240 }}
            />
            <Button disabled={!input} onClick={() => choose(input)}>
              查看调查记录
            </Button>
          </Space>
        </details>
        {jobId && (
          <Button
            danger
            loading={cancel.isPending}
            disabled={
              !data ||
              historicalReadOnly ||
              !["queued", "running"].includes(data.state)
            }
            onClick={() => {
              cancel.reset();
              setStopTarget({
                jobId: jobId!,
                identity: stopIdentity,
                key: crypto.randomUUID(),
              });
            }}
          >
            取消调查
          </Button>
        )}
      </Space>
      <ConfirmDialog
        open={!!stopTarget && stopTarget.identity === stopIdentity}
        title="停止调查？"
        description={
          <>
            <p>调查作业 {stopTarget?.jobId}</p>
            <p>已提交的事实和步骤会保留，未确认的根因仍需核对。</p>
            {cancel.error && (
              <QueryState loading={false} error={cancel.error} />
            )}
          </>
        }
        confirmLabel="确认停止"
        cancelLabel="继续运行"
        danger
        loading={cancel.isPending}
        onCancel={() => setStopTarget(undefined)}
        onConfirm={() => {
          if (
            stopTarget?.identity === stopIdentity &&
            data?.jobId === stopTarget.jobId &&
            !historicalReadOnly &&
            ["queued", "running"].includes(data.state) &&
            !cancel.isPending
          )
            cancel.mutate(stopTarget);
        }}
      />
      <h3>同一故障的历史调查</h3>
      <QueryState
        loading={timeline.isPending}
        error={timeline.error}
        retry={() => void timeline.refetch()}
      >
        {history.length === 0 ? (
          <p className="muted">
            {assistantHref
              ? "当前没有可见的历史调查。助手会话中的调查保存在会话记录里，此处只显示与本故障关联的调查。"
              : "当前没有可见的历史调查。开始调查后，进度和结果会保存在此处。"}
          </p>
        ) : (
          <Space wrap>
            {history.map((p) => {
              const rowJobId = text(p.jobId);
              const unified =
                !!linkValue(p.conversationId) &&
                !!linkValue(p.turnId) &&
                !!assistantHref;
              return (
                <Space key={rowJobId} size={4}>
                  {/* A unified entry job (investigation-job/v3) is read through
                      the unified detail of exactly that conversation, turn and
                      job. The primary entry opens it directly, so a reader never
                      lands in the legacy result view and never starts another
                      job or another model run. A v2 job keeps its own result
                      view. */}
                  <Button
                    title={rowJobId}
                    onClick={() =>
                      unified ? openUnified(p) : choose(rowJobId)
                    }
                  >
                    {stateLabel(text(p.state))} · 故障版本{" "}
                    {text(p.triggerRevision)}
                  </Button>
                </Space>
              );
            })}
          </Space>
        )}
        {history.length > 0 && (
          <Space className="status-note">
            <Button
              disabled={!historyCursor}
              onClick={() => {
                const n = new URLSearchParams(url);
                n.delete("historyCursor");
                setUrl(n);
              }}
            >
              最新历史
            </Button>
            <Button
              disabled={!record(record(timeline.data).meta).nextCursor}
              onClick={() => {
                const n = new URLSearchParams(url);
                n.set(
                  "historyCursor",
                  text(record(record(timeline.data).meta).nextCursor),
                );
                setUrl(n);
              }}
            >
              更早调查
            </Button>
          </Space>
        )}
      </QueryState>
      {existingNotice && (
        <Alert
          showIcon
          type="info"
          title="已打开此故障版本的已有调查"
          description="后端复用了同一主体、故障版本、策略且收紧与重点相同的持久结果，未重新调用模型。请核对调查版本；故障有新证据后可调查新版本，更换收紧或重点也会生成新任务。"
        />
      )}
      {create.error && <QueryState loading={false} error={create.error} />}{" "}
      {cancel.error && cancel.variables?.identity === stopIdentity && (
        <QueryState loading={false} error={cancel.error} />
      )}{" "}
      {!jobId && (
        <p className="status-note">
          点击“开始智能调查”分析当前故障，或从历史调查中查看已有结果。
        </p>
      )}
      {jobId && (
        <QueryState
          loading={job.isPending}
          error={job.error}
          retry={() => void job.refetch()}
        >
          {data && (
            <>
              {historicalReadOnly && (
                <Alert
                  type="info"
                  title="历史调查只读"
                  description="此调查保留原主体归属。可查看已授权的结果和证据，不能取消原主体任务。"
                />
              )}
              <Space wrap>
                <Status value={data.state} />
                <span>
                  {["queued", "running", "cancel_requested"].includes(
                    data.state,
                  )
                    ? "实时更新：" + stateLabel(stream.status)
                    : "调查已结束，结果来自持久记录"}
                </span>
                {["queued", "running", "cancel_requested"].includes(
                  data.state,
                ) && <Button onClick={stream.reconnect}>重新连接</Button>}
              </Space>
              {stream.error && (
                <Alert
                  showIcon
                  type="warning"
                  title="实时连接中断，持久记录仍可刷新"
                  description={stream.error.message}
                />
              )}
              {data.triggerRevision !== incident.revision && (
                <Alert
                  showIcon
                  type="warning"
                  title="这是故障旧版本的调查结果"
                  description={
                    "此调查基于故障版本 " +
                    data.triggerRevision +
                    "；当前故障版本为 " +
                    incident.revision +
                    "。结果可能已不适用于当前状态，可开始新的调查核对。"
                  }
                />
              )}
              {data.errorCode && (
                <Alert
                  showIcon
                  type="warning"
                  title="调查未完整完成"
                  description={
                    <>
                      <p>
                        {investigationFailure(data.errorCode) ??
                          "调查遇到错误，请核对持久步骤和结果，必要时联系平台管理员。"}
                      </p>
                      <details>
                        <summary>查看原始错误代码</summary>
                        {data.errorCode}
                      </details>
                    </>
                  }
                />
              )}
              <Details
                value={{
                  jobId: data.jobId,
                  ...(data.errorCode ? { errorCode: data.errorCode } : {}),
                }}
              />
              {data.focus ? (
                <p className="status-note">本次调查重点：{data.focus}</p>
              ) : null}
              <BudgetSummary job={data} />
              <h3>调查过程</h3>
              <QueryState
                loading={steps.isPending}
                error={steps.error}
                retry={() => void steps.refetch()}
              >
                <StepLedger
                  onEvidence={onEvidence}
                  steps={
                    (steps.data as InvestigationStepsSuccessV2 | undefined)
                      ?.data || []
                  }
                />
              </QueryState>
              <h3>最终结果</h3>
              <Space wrap>
                {list(record(data.result).evidenceRefs)
                  .filter(
                    (id): id is string =>
                      typeof id === "string" && id.length > 0,
                  )
                  .slice(0, 128)
                  .map((id) => (
                    <Button
                      key={id}
                      title={id}
                      disabled={!onEvidence}
                      onClick={() => onEvidence?.(id)}
                    >
                      查看结果证据 {id.slice(-8)}
                    </Button>
                  ))}
              </Space>
              {list(record(data.result).actionPlans).map((v) => {
                const plan = record(v);
                return typeof plan.actionPlanId === "string" ? (
                  <Button
                    key={plan.actionPlanId}
                    title={plan.actionPlanId}
                    onClick={() => {
                      const n = new URLSearchParams(url);
                      n.set("tab", "action");
                      n.set("actionPlanId", plan.actionPlanId as string);
                      setUrl(n);
                    }}
                  >
                    查看处置建议（需另行输入和确认）
                  </Button>
                ) : null;
              })}
              {data.result ? (
                <Details value={data.result} />
              ) : (
                <p>尚无持久化最终结果。</p>
              )}
              <details>
                <summary>最近实时事件（最多 100 条）</summary>
                <Table
                  rowKey={(r) => r.eventId}
                  dataSource={stream.events}
                  pagination={{ pageSize: 10, showSizeChanger: false }}
                  columns={[
                    {
                      title: "事件",
                      dataIndex: "eventType",
                      render: (v: string) => (
                        <span title={v}>
                          {(
                            {
                              "job.state": "调查状态更新",
                              "step.started": "步骤开始",
                              "step.completed": "步骤结束",
                              "evidence.added": "新增证据",
                              "rca.updated": "根因结果更新",
                              "job.completed": "调查结束",
                            } as Record<string, string>
                          )[v] ?? "调查事件（展开查看详情）"}
                        </span>
                      ),
                    },
                    {
                      title: "时间",
                      dataIndex: "occurredAt",
                      render: (v: string) => (
                        <time dateTime={v} title={v}>
                          {displayTime(v)}
                        </time>
                      ),
                    },
                    {
                      title: "正式摘要",
                      render: (_, r) => <Details value={r.payload} />,
                    },
                  ]}
                />
              </details>
            </>
          )}
        </QueryState>
      )}
    </>
  );
}

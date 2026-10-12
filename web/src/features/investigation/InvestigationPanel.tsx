import { investigationFailure } from "../../components/investigationFailure";
import { stateLabel } from "../../components/presentation";
import { useState, useRef } from "react";
import { useQuery, useQueryClient, useMutation } from "@tanstack/react-query";
import { Alert, Button, Input, Space, Table } from "antd";
import { useSearchParams } from "react-router-dom";
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
export function InvestigationPanel({
  incident,
  onEvidence,
}: {
  incident: IncidentV2;
  onEvidence?: (id: string) => void;
}) {
  const [url, setUrl] = useSearchParams();
  const client = useQueryClient();
  const [input, setInput] = useState("");
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
  const create = useMutation({
    mutationFn: () =>
      checked(
        createInvestigation(
          incident.incidentId,
          { triggerRevision: incident.revision, triggerKind: "manual" },
          requestOptions(undefined, true),
        ),
      ),
    onSuccess: (res) => {
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
    mutationFn: () =>
      checked(cancelInvestigation(jobId!, {}, requestOptions(undefined, true))),
    onSuccess: () =>
      void client.invalidateQueries({ queryKey: ["investigation", jobId] }),
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
      <Space wrap className="status-note">
        <Button
          type="primary"
          loading={create.isPending}
          onClick={() => create.mutate()}
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
            disabled={!data || !["queued", "running"].includes(data.state)}
            onClick={() => cancel.mutate()}
          >
            取消调查
          </Button>
        )}
      </Space>
      <h3>历史调查</h3>
      <QueryState
        loading={timeline.isPending}
        error={timeline.error}
        retry={() => void timeline.refetch()}
      >
        {history.length === 0 ? (
          <p className="muted">
            当前没有可见的历史调查。开始调查后，进度和结果会保存在此处。
          </p>
        ) : (
          <Space wrap>
            {history.map((p) => (
              <Button
                key={text(p.jobId)}
                title={text(p.jobId)}
                onClick={() => choose(text(p.jobId))}
              >
                {stateLabel(text(p.state))} · 故障版本 {text(p.triggerRevision)}
              </Button>
            ))}
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
          description="后端复用了同一主体、故障版本和策略的持久结果，未重新调用模型。请核对调查版本；故障有新证据后可调查新版本。"
        />
      )}
      {create.error && <QueryState loading={false} error={create.error} />}{" "}
      {cancel.error && <QueryState loading={false} error={cancel.error} />}{" "}
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

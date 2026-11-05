import { stateLabel } from "../components/presentation";
import { resourceIdentifier } from "../components/FaultSummary";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Select } from "antd";
import { DataTable as Table } from "../components/v12/primitives";
import { Link, useSearchParams } from "react-router-dom";
import {
  cancelCommandExecution,
  getCommandExecution,
  listCommandExecutions,
} from "../api/generated/platform";
import type { ListCommandExecutionsState } from "../api/generated/models";
import { checked, requestOptions } from "../api/client";
import { PageHeading } from "../components/PageHeading";
import { QueryState } from "../components/QueryState";
import { Status } from "../components/Status";
import { Details } from "../components/Details";
import { EnvelopeMeta } from "../components/EnvelopeMeta";
import { displayTime, list, record, text } from "../components/data";
import { ExecutionOutput } from "../features/action/ExecutionOutput";
import { archivedPreview } from "../features/action/output";

const STATES: ListCommandExecutionsState[] = [
  "prepared",
  "dispatching",
  "running",
  "succeeded",
  "failed",
  "cancelled",
  "execution_unknown",
];
const ACTIVE = ["prepared", "dispatching", "running"];

function ExecutionDetail({ executionId }: { executionId: string }) {
  const client = useQueryClient();
  const detail = useQuery({
    queryKey: ["command-execution", executionId],
    queryFn: ({ signal }) =>
      checked(getCommandExecution(executionId, requestOptions(signal))),
    refetchInterval: (q) =>
      ACTIVE.includes(text(record(record(q.state.data).data).state))
        ? 3000
        : false,
  });
  const executed = record(record(detail.data).data);
  const cancel = useMutation({
    mutationFn: () =>
      checked(
        cancelCommandExecution(executionId, requestOptions(undefined, true)),
      ),
    onSuccess: async () => {
      await Promise.all([
        detail.refetch(),
        client.invalidateQueries({ queryKey: ["command-executions"] }),
      ]);
    },
  });
  return (
    <QueryState
      loading={detail.isPending}
      error={detail.error}
      retry={() => void detail.refetch()}
    >
      <div className="execution-detail">
        <Status value={executed.state} />
        {text(executed.state) === "execution_unknown" && (
          <Alert
            showIcon
            type="error"
            title="执行结果不确定：请核对目标状态，暂勿重复提交"
            description="先核对持久输出与目标事实；不能通过重试猜测命令是否已执行。"
          />
        )}
        {cancel.error && <QueryState loading={false} error={cancel.error} />}
        <Button
          danger
          disabled={!ACTIVE.includes(text(executed.state))}
          loading={cancel.isPending}
          onClick={() => cancel.mutate()}
        >
          取消执行
        </Button>
        <ExecutionOutput
          output={
            record(executed.extensions).outputPreviews
              ? archivedPreview(record(executed.extensions).outputPreviews)
              : ""
          }
          truncated={executed.outputTruncated === true}
        />
        <Details value={executed} />
      </div>
    </QueryState>
  );
}

export function CommandExecutionsPage() {
  const [url, setUrl] = useSearchParams();
  const state = (url.get("state") || "") as ListCommandExecutionsState | "";
  const cursor = url.get("cursor") || undefined;
  const update = (key: string, value: string) => {
    const n = new URLSearchParams(url);
    n.delete("cursor");
    if (value) n.set(key, value);
    else n.delete(key);
    setUrl(n);
  };
  const q = useQuery({
    queryKey: ["command-executions", state, cursor],
    queryFn: ({ signal }) =>
      checked(
        listCommandExecutions(
          { limit: 50, cursor, ...(state ? { state } : {}) },
          requestOptions(signal),
        ),
      ),
  });
  const rows = list(record(q.data).data).map(record);
  const meta = record(q.data).meta;
  return (
    <>
      <PageHeading
        title="命令执行"
        description="查看本租户全部命令执行与回执；新命令在故障工作台内经风险评估后创建。"
      />
      <section className="section">
        <div className="filters">
          <div className="filter">
            <label htmlFor="execution-state">状态</label>
            <Select
              id="execution-state"
              allowClear
              placeholder="全部"
              value={state || undefined}
              options={STATES.map((value) => ({
                value,
                label: stateLabel(value),
              }))}
              onChange={(v) => update("state", v ?? "")}
            />
          </div>
        </div>
        <QueryState
          loading={q.isPending}
          error={q.error}
          empty={rows.length === 0}
          retry={() => void q.refetch()}
        >
          <EnvelopeMeta value={meta} />
          <Table
            rowKey={(r) => text(r.executionId)}
            dataSource={rows}
            pagination={false}
            scroll={{ x: 900 }}
            expandable={{
              expandedRowRender: (r) => (
                <ExecutionDetail executionId={text(r.executionId)} />
              ),
            }}
            columns={[
              {
                title: "目标",
                width: 280,
                render: (_, r) => {
                  const target = text(
                    record(record(r.extensions).binding).targetCanonicalId,
                  );
                  return (
                    <Link
                      to={
                        "/resources/detail?canonicalId=" +
                        encodeURIComponent(target)
                      }
                    >
                      <span title={target}>{resourceIdentifier(target)}</span>
                    </Link>
                  );
                },
              },
              {
                title: "状态",
                width: 150,
                render: (_, r) => <Status value={r.state} />,
              },
              {
                title: "退出码",
                width: 90,
                render: (_, r) =>
                  r.exitCode === null || r.exitCode === undefined
                    ? "—"
                    : text(r.exitCode),
              },
              {
                title: "提交者",
                width: 180,
                render: (_, r) => text(r.subject),
              },
              {
                title: "创建时间",
                width: 200,
                render: (_, r) =>
                  displayTime(record(r.extensions).createdAt),
              },
              {
                title: "完成时间",
                width: 200,
                render: (_, r) => displayTime(r.completedAt),
              },
            ]}
          />
          <div className="pagination">
            <span>本页 {rows.length} 条 · 每页最多 50 条</span>
            <div>
              <Button
                disabled={!url.has("cursor")}
                onClick={() => update("cursor", "")}
              >
                回到第一页
              </Button>{" "}
              <Button
                disabled={!record(meta).nextCursor}
                onClick={() => {
                  const n = new URLSearchParams(url);
                  n.set("cursor", text(record(meta).nextCursor));
                  setUrl(n);
                }}
              >
                下一页
              </Button>
            </div>
          </div>
        </QueryState>
      </section>
    </>
  );
}

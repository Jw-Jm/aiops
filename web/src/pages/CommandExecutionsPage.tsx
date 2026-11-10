import { stateLabel } from "../components/presentation";
import { resourceIdentifier } from "../components/FaultSummary";
import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Input, Modal, Select } from "antd";
import { DataTable as Table } from "../components/v12/primitives";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import {
  cancelCommandExecution,
  getCommandExecution,
  getIncident,
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
import { PostCheckPanel } from "../features/action/PostCheckPanel";
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
  const [confirmCancel, setConfirmCancel] = useState(false);
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
  const binding = record(record(executed.extensions).binding);
  const incidentId =
    typeof executed.incidentId === "string"
      ? executed.incidentId
      : typeof binding.incidentId === "string"
        ? binding.incidentId
        : "";
  const incidentHref = incidentId
    ? "/events/incidents/" +
      encodeURIComponent(incidentId) +
      "?" +
      new URLSearchParams({
        clusterUid: String(binding.clusterUid || ""),
        tab: "action",
      })
    : undefined;
  const cancel = useMutation({
    mutationFn: () =>
      checked(
        cancelCommandExecution(executionId, requestOptions(undefined, true)),
      ),
    onSuccess: async () => {
      setConfirmCancel(false);
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
          onClick={() => setConfirmCancel(true)}
        >
          取消执行
        </Button>
        <Modal
          title="取消执行"
          open={confirmCancel}
          onCancel={() => setConfirmCancel(false)}
          onOk={() => cancel.mutate()}
          okText="确认取消执行"
          cancelText="继续等待"
          confirmLoading={cancel.isPending}
          okButtonProps={{
            danger: true,
            disabled: !ACTIVE.includes(text(executed.state)),
          }}
        >
          <p>
            取消执行 {executionId}
            ？已发生的外部效果不会因此回退；取消与运行可能并发，最终状态以回执为准。
          </p>
        </Modal>
        {incidentHref && (
          <p>
            <Link to={incidentHref}>打开关联故障与人工处置</Link>
          </p>
        )}
        <ExecutionOutput
          output={
            record(executed.extensions).outputPreviews
              ? archivedPreview(record(executed.extensions).outputPreviews)
              : ""
          }
          truncated={executed.outputTruncated === true}
        />
        <Details value={executed} />
        {incidentHref && (
          <PostCheckPanel
            executionId={executionId}
            incidentHref={incidentHref}
          />
        )}
      </div>
    </QueryState>
  );
}

function NewExecutionDialog({ close }: { close: () => void }) {
  const navigate = useNavigate();
  const [incidentId, setIncidentId] = useState("");
  const [verifiedId, setVerifiedId] = useState("");
  const valid =
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
      incidentId,
    );
  const incident = useQuery({
    queryKey: ["command-new-incident", verifiedId],
    enabled: !!verifiedId && verifiedId === incidentId,
    queryFn: ({ signal }) =>
      checked(getIncident(verifiedId, requestOptions(signal))),
  });
  const data = incident.data?.data;
  return (
    <Modal title="新建人工执行" open onCancel={close} footer={null}>
      <p>
        选择有权访问的故障，核对目标资源与执行环境，再独立输入实际命令。风险评估、本人确认及必要的二次验证在故障工作台完成。
      </p>
      <label htmlFor="command-incident">关联故障 ID</label>
      <Input
        id="command-incident"
        value={incidentId}
        onChange={(e) => {
          setIncidentId(e.target.value.trim());
          setVerifiedId("");
        }}
        maxLength={36}
      />
      <Button
        disabled={!valid}
        loading={incident.isFetching}
        onClick={() =>
          verifiedId === incidentId
            ? void incident.refetch()
            : setVerifiedId(incidentId)
        }
      >
        核对故障
      </Button>
      {!!incidentId && !valid && (
        <Alert type="error" title="请输入完整的故障 UUID" />
      )}
      {verifiedId === incidentId && incident.error && (
        <QueryState loading={false} error={incident.error} />
      )}
      {verifiedId === incidentId && data?.incidentId === verifiedId && data && (
        <>
          <p>
            {data.incidentId} · {data.resourceCanonicalId}
          </p>
          <Button
            type="primary"
            onClick={() =>
              navigate(
                "/events/incidents/" +
                  encodeURIComponent(data.incidentId) +
                  "?" +
                  new URLSearchParams({
                    clusterUid: data.clusterUid,
                    tab: "action",
                  }),
              )
            }
          >
            进入风险评估与人工执行
          </Button>
        </>
      )}
      <p>
        <Link to="/events/incidents">从故障列表选择</Link>
      </p>
    </Modal>
  );
}

export function CommandExecutionsPage() {
  const [newExecution, setNewExecution] = useState(false);
  const [url, setUrl] = useSearchParams();
  const state = (url.get("state") || "") as ListCommandExecutionsState | "";
  const cursor = url.get("cursor") || undefined;
  const search = url.get("q") || "";
  const [draft, setDraft] = useState(search);
  useEffect(() => setDraft(search), [search]);
  const update = (key: string, value: string) => {
    const n = new URLSearchParams(url);
    if (key !== "executionId") n.delete("cursor");
    if (value) n.set(key, value);
    else n.delete(key);
    setUrl(n);
  };
  const q = useQuery({
    queryKey: ["command-executions", state, search, cursor],
    queryFn: ({ signal }) =>
      checked(
        listCommandExecutions(
          {
            limit: 50,
            cursor,
            ...(search ? { q: search } : {}),
            ...(state ? { state } : {}),
          },
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
        description="查看当前授权范围内的命令执行与回执；命令完成与故障恢复分别核对。"
      />
      {newExecution && (
        <NewExecutionDialog close={() => setNewExecution(false)} />
      )}
      <section className="section">
        <Button type="primary" onClick={() => setNewExecution(true)}>
          新建人工执行
        </Button>
        <Button
          onClick={() =>
            Modal.info({
              title: "受控作业模板尚未启用",
              content:
                "当前仅支持独立输入命令，经风险评估、本人确认和二次验证执行。模板库尚未开放，不能用未发布模板代替实际命令。",
            })
          }
        >
          受控作业模板
        </Button>
        <div className="filters">
          <div className="filter">
            <label htmlFor="execution-search">搜索执行</label>
            <Input.Search
              id="execution-search"
              placeholder="执行 ID、故障 ID、提交者或目标资源"
              value={draft}
              maxLength={256}
              onChange={(e) => setDraft(e.target.value)}
              onSearch={(v) => update("q", v.trim())}
            />
          </div>
          <Button
            onClick={() => {
              setDraft("");
              const n = new URLSearchParams(url);
              ["q", "state", "cursor"].forEach((k) => n.delete(k));
              setUrl(n);
            }}
          >
            清空筛选
          </Button>
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
          {url.get("executionId") &&
            !rows.some((r) => r.executionId === url.get("executionId")) && (
              <ExecutionDetail
                key={url.get("executionId")}
                executionId={url.get("executionId")!}
              />
            )}
          <Table
            rowKey={(r) => text(r.executionId)}
            dataSource={rows}
            pagination={false}
            scroll={{ x: 900 }}
            expandable={{
              expandedRowKeys: url.get("executionId")
                ? [url.get("executionId")!]
                : undefined,
              onExpand: (expanded, r) =>
                update("executionId", expanded ? text(r.executionId) : ""),
              expandedRowRender: (r) => (
                <ExecutionDetail executionId={text(r.executionId)} />
              ),
            }}
            columns={[
              {
                title: "执行 ID",
                width: 260,
                render: (_, r) => (
                  <Button
                    type="link"
                    onClick={() => update("executionId", text(r.executionId))}
                  >
                    {text(r.executionId)}
                  </Button>
                ),
              },
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
                        "/resources/detail?" +
                        new URLSearchParams({
                          canonicalId: target,
                          clusterUid: String(
                            record(record(r.extensions).binding).clusterUid ||
                              "",
                          ),
                        })
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
                render: (_, r) => displayTime(record(r.extensions).createdAt),
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

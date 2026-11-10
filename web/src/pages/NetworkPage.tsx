import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Alert, Button, Select } from "antd";
import { getResources, queryEvidence } from "../api/generated/platform";
import type { ResourcePageV2 } from "../api/generated/models";
import { checked, requestOptions } from "../api/client";
import { useOperationalCluster } from "../app/ClusterContext";
import {
  PageHeader,
  Surface,
  Field,
  DataTable,
} from "../components/v12/primitives";
import { QueryState } from "../components/QueryState";
import { Details } from "../components/Details";
import { record, list, text, displayTime } from "../components/data";
import { AskAssistantEntry } from "../features/assistant/AskAssistantEntry";

// Same DeepFlow templates the investigation assistant is admitted to use.
const OPERATIONS: { value: string; label: string }[] = [
  { value: "GetResourceNetworkHealth", label: "网络健康" },
  { value: "GetNetworkDependencies", label: "网络依赖" },
  { value: "FindTCPRetransmission", label: "TCP 重传" },
  { value: "FindPacketLoss", label: "丢包" },
  { value: "FindConnectionFailure", label: "连接失败" },
  { value: "GetNetworkPath", label: "网络路径" },
  { value: "GetL7Context", label: "L7 上下文" },
];

function NetworkWorkspace({ cluster }: { cluster: string }) {
  const [canonicalId, setCanonicalId] = useState("");
  const [operation, setOperation] = useState(OPERATIONS[0].value);
  const resources = useQuery({
    queryKey: ["network-resources", cluster],
    queryFn: ({ signal }) =>
      checked(
        getResources(
          { clusterUid: cluster, limit: 50 },
          requestOptions(signal),
        ),
      ),
  });
  const page = resources.data as ResourcePageV2 | undefined;
  const run = useMutation({
    mutationFn: (frozen: {
      from: string;
      to: string;
      canonicalId: string;
      operation: string;
    }) =>
      checked(
        queryEvidence(
          {
            resourceCanonicalId: frozen.canonicalId,
            type: "deepflow",
            queryTemplate: frozen.operation,
            timeRange: { from: frozen.from, to: frozen.to },
            budget: { timeoutMs: 9000, maxBytes: 32 << 10 },
            limit: 100,
          },
          requestOptions(undefined, true),
        ),
      ),
  });
  const sameSelection =
    run.variables?.canonicalId === canonicalId &&
    run.variables?.operation === operation;
  const window = sameSelection ? run.variables : undefined;
  const result =
    sameSelection && record(run.data).data
      ? record(record(run.data).data)
      : null;
  const rows = result ? list(result.evidence).map(record) : [];
  return (
    <Surface title="网络观测查询">
      <QueryState
        loading={resources.isPending}
        error={resources.error}
        retry={() => void resources.refetch()}
      >
        <Field label="目标资源" htmlFor="network-resource">
          <Select
            id="network-resource"
            showSearch
            optionFilterProp="label"
            style={{ minWidth: 360 }}
            placeholder="选择资源"
            value={canonicalId || undefined}
            onChange={setCanonicalId}
            options={(page?.data ?? []).map((r) => ({
              value: r.canonicalId,
              label: `${r.kind}/${r.name}（${r.namespace ?? "cluster"}）`,
            }))}
          />
        </Field>
        <Field label="查询操作" htmlFor="network-operation">
          <Select
            id="network-operation"
            style={{ minWidth: 240 }}
            value={operation}
            onChange={setOperation}
            options={OPERATIONS}
          />
        </Field>
        <Button
          type="primary"
          disabled={!canonicalId}
          loading={run.isPending}
          onClick={() => {
            const to = new Date();
            const from = new Date(to.getTime() - 3600_000);
            const frozen = {
              from: from.toISOString(),
              to: to.toISOString(),
              canonicalId,
              operation,
            };
            run.mutate(frozen);
          }}
        >
          查询最近一小时
        </Button>
        <AskAssistantEntry
          entry={{
            clusterUid: cluster,
            canonicalId: canonicalId || undefined,
            mode: "troubleshoot",
            ...(window ? { from: window.from, to: window.to } : {}),
          }}
          disabled={!canonicalId}
          reason="请先选择要询问的资源。"
          label="按此范围询问 AI"
        />
      </QueryState>
      {sameSelection && run.error && (
        <QueryState loading={false} error={run.error} />
      )}
      {result && (
        <>
          {text(result.freshness) !== "fresh" && (
            <Alert
              type="warning"
              showIcon
              title={`结果新鲜度：${text(result.freshness) || "未知"}`}
            />
          )}
          {result.partial === true && (
            <Alert
              type="warning"
              showIcon
              title="结果为部分数据，来源可能降级。"
              description={list(result.degradedSources).map(text).join("、")}
            />
          )}
          <DataTable
            rowKey={(r) => text(r.evidenceId)}
            dataSource={rows}
            pagination={false}
            expandable={{
              expandedRowRender: (r) => <Details value={r.factSlice} />,
            }}
            columns={[
              { title: "来源", render: (_, r) => text(r.sourceSystem) },
              {
                title: "观测起",
                render: (_, r) => displayTime(r.observedFrom),
              },
              { title: "观测止", render: (_, r) => displayTime(r.observedTo) },
              { title: "回放状态", render: (_, r) => text(r.replayState) },
            ]}
          />
        </>
      )}
    </Surface>
  );
}

export function NetworkPage() {
  const { clusterUid, invalid } = useOperationalCluster();
  if (!clusterUid || invalid)
    return <Alert type="warning" title="请先选择有运维授权的运行集群。" />;
  return (
    <>
      <PageHeader
        title="网络观测"
        description="按授权资源读取 DeepFlow 网络证据；查询窗口为最近一小时，结果可展开核对。"
      />
      <NetworkWorkspace key={clusterUid} cluster={clusterUid} />
    </>
  );
}

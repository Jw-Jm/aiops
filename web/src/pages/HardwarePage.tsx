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

function HardwareWorkspace({ cluster }: { cluster: string }) {
  const [canonicalId, setCanonicalId] = useState("");
  const resources = useQuery({
    queryKey: ["hardware-resources", cluster],
    queryFn: ({ signal }) =>
      checked(
        getResources(
          { clusterUid: cluster, domain: "hardware", limit: 50 },
          requestOptions(signal),
        ),
      ),
  });
  const page = resources.data as ResourcePageV2 | undefined;
  const run = useMutation({
    mutationFn: () => {
      const to = new Date();
      const from = new Date(to.getTime() - 300_000);
      return checked(
        queryEvidence(
          {
            resourceCanonicalId: canonicalId,
            type: "hardware",
            queryTemplate: "hardware-health/v1",
            timeRange: { from: from.toISOString(), to: to.toISOString() },
            budget: { timeoutMs: 2500, maxBytes: 32 << 10 },
            limit: 100,
          },
          requestOptions(undefined, true),
        ),
      );
    },
  });
  const result = record(run.data).data ? record(record(run.data).data) : null;
  const rows = result ? list(result.evidence).map(record) : [];
  return (
    <Surface title="硬件健康查询">
      <QueryState
        loading={resources.isPending}
        error={resources.error}
        retry={() => void resources.refetch()}
      >
        <Field label="硬件资源" htmlFor="hardware-resource">
          <Select
            id="hardware-resource"
            showSearch
            optionFilterProp="label"
            style={{ minWidth: 360 }}
            placeholder="选择硬件资源"
            value={canonicalId || undefined}
            onChange={setCanonicalId}
            options={(page?.data ?? []).map((r) => ({
              value: r.canonicalId,
              label: `${r.kind}/${r.name}`,
            }))}
          />
        </Field>
        <Button
          type="primary"
          disabled={!canonicalId}
          loading={run.isPending}
          onClick={() => run.mutate()}
        >
          查询当前健康
        </Button>
        <AskAssistantEntry
          entry={{
            clusterUid: cluster,
            canonicalId: canonicalId || undefined,
            mode: "troubleshoot",
          }}
          disabled={!canonicalId}
          reason="请先选择要询问的硬件资源。"
          label="按此范围询问 AI"
        />
      </QueryState>
      {run.error && <QueryState loading={false} error={run.error} />}
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

export function HardwarePage() {
  const { clusterUid, invalid } = useOperationalCluster();
  if (!clusterUid || invalid)
    return <Alert type="warning" title="请先选择有运维授权的运行集群。" />;
  return (
    <>
      <PageHeader
        title="硬件健康"
        description="按授权硬件资源读取 Redfish 健康证据；查询窗口为最近五分钟。"
      />
      <HardwareWorkspace key={clusterUid} cluster={clusterUid} />
    </>
  );
}

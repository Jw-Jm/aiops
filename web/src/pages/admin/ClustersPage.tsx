import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Input } from "antd";
import { getClusters, createCluster } from "../../api/generated/platform";
import type { AuthorizedClusterV1 } from "../../api/generated/models";
import { checked, requestOptions } from "../../api/client";
import {
  administrativeWrite,
  useNativeWriteProof,
} from "../../auth/useNativeWriteProof";
import { PageHeader, Surface, Field, DataTable } from "../../components/v12/primitives";
import { QueryState } from "../../components/QueryState";
import { EnvelopeMeta } from "../../components/EnvelopeMeta";
import { record, list, text } from "../../components/data";

const LIMIT = 50;

function RegisterCluster({ done }: { done: () => void }) {
  const [clusterUid, setClusterUid] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [distribution, setDistribution] = useState("");
  const [apiEndpointRef, setApiEndpointRef] = useState("");
  const [kubernetes, setKubernetes] = useState("");
  const [capabilityKeys, setCapabilityKeys] = useState("kubernetes");
  const valid =
    clusterUid.trim().length > 0 &&
    displayName.trim().length > 0 &&
    distribution.trim().length > 0 &&
    /^openbao:\/\/[A-Za-z0-9_./-]+$/.test(apiEndpointRef.trim()) &&
    kubernetes.trim().length > 0 &&
    capabilityKeys.split(",").some((k) => k.trim().length > 0);
  const body = {
    clusterUid: clusterUid.trim(),
    displayName: displayName.trim(),
    distribution: distribution.trim(),
    apiEndpointRef: apiEndpointRef.trim(),
    actualVersions: { kubernetes: kubernetes.trim() },
    capabilities: Object.fromEntries(
      capabilityKeys
        .split(",")
        .map((k) => k.trim())
        .filter(Boolean)
        .map((k) => [k, true]),
    ),
  };
  const proof = useNativeWriteProof(
    valid
      ? administrativeWrite(
          "cluster_administration",
          "POST",
          "/api/v1/admin/clusters",
          body,
        )
      : undefined,
    false,
  );
  const submit = useMutation({
    mutationFn: () =>
      checked(createCluster(body, proof.options())),
    onSuccess: done,
  });
  return (
    <Surface title="登记新集群">
      <Field label="集群 UID" htmlFor="cluster-uid">
        <Input
          id="cluster-uid"
          value={clusterUid}
          onChange={(e) => setClusterUid(e.target.value)}
        />
      </Field>
      <Field label="显示名称" htmlFor="cluster-name">
        <Input
          id="cluster-name"
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
        />
      </Field>
      <Field label="发行版" htmlFor="cluster-distribution">
        <Input
          id="cluster-distribution"
          placeholder="如 kubeadm / openshift"
          value={distribution}
          onChange={(e) => setDistribution(e.target.value)}
        />
      </Field>
      <Field label="API 凭据引用" htmlFor="cluster-ref">
        <Input
          id="cluster-ref"
          placeholder="openbao://clusters/<name>/api"
          value={apiEndpointRef}
          onChange={(e) => setApiEndpointRef(e.target.value)}
        />
      </Field>
      <Field label="Kubernetes 版本" htmlFor="cluster-k8s">
        <Input
          id="cluster-k8s"
          placeholder="如 v1.31.2"
          value={kubernetes}
          onChange={(e) => setKubernetes(e.target.value)}
        />
      </Field>
      <Field label="能力键（逗号分隔）" htmlFor="cluster-caps">
        <Input
          id="cluster-caps"
          value={capabilityKeys}
          onChange={(e) => setCapabilityKeys(e.target.value)}
        />
      </Field>
      {submit.error && <QueryState loading={false} error={submit.error} />}
      {proof.verification}
      <Button
        type="primary"
        disabled={!valid || !proof.ready}
        loading={submit.isPending}
        onClick={() => submit.mutate()}
      >
        登记集群
      </Button>
      {submit.isSuccess && (
        <Alert type="success" title="集群已登记。" showIcon />
      )}
    </Surface>
  );
}

export function ClustersPage() {
  const client = useQueryClient();
  const [cursor, setCursor] = useState<string | undefined>();
  const q = useQuery({
    queryKey: ["admin-clusters", cursor],
    queryFn: ({ signal }) =>
      checked(getClusters({ limit: LIMIT, cursor }, requestOptions(signal))),
  });
  const rows = list(record(q.data).data).map(
    (r) => record(r) as unknown as AuthorizedClusterV1,
  );
  const meta = record(q.data).meta;
  return (
    <>
      <PageHeader
        title="集群接入"
        description="登记与核对运行集群；登记需要管理验证，凭据只保存在引用后端。"
      />
      <QueryState loading={q.isPending} error={q.error} retry={() => void q.refetch()}>
        <Surface title="已登记集群">
          <EnvelopeMeta value={meta} />
          <DataTable
            rowKey={(r) => text(r.clusterId)}
            dataSource={rows}
            pagination={false}
            columns={[
              { title: "显示名称", render: (_, r) => text(r.displayName) },
              { title: "集群 UID", render: (_, r) => text(r.clusterUid) },
              { title: "集群 ID", render: (_, r) => text(r.clusterId) },
            ]}
          />
          <div className="pagination">
            <Button disabled={!cursor} onClick={() => setCursor(undefined)}>
              回到第一页
            </Button>{" "}
            <Button
              disabled={!record(meta).nextCursor}
              onClick={() => setCursor(text(record(meta).nextCursor))}
            >
              下一页
            </Button>
          </div>
        </Surface>
        <RegisterCluster
          done={() =>
            void client.invalidateQueries({ queryKey: ["admin-clusters"] })
          }
        />
      </QueryState>
    </>
  );
}

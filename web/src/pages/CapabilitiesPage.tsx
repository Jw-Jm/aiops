import { ScopeFilter } from "../components/ScopeFilter";
import { useOperationalCluster } from "../app/ClusterContext";
import { useQuery } from "@tanstack/react-query";
import { getCapabilities } from "../api/generated/platform";
import { checked, requestOptions } from "../api/client";
import { PageHeading } from "../components/PageHeading";
import { QueryState } from "../components/QueryState";
import { CapabilityStatus } from "../components/CapabilityStatus";
import type { CapabilitiesSuccessV1 } from "../api/generated/models";
export function CapabilitiesPage() {
  const { clusterUid: cluster, invalid } = useOperationalCluster();
  const q = useQuery({
    queryKey: ["capabilities", cluster],
    enabled: !!cluster,
    queryFn: ({ signal }) =>
      checked(getCapabilities({ clusterUid: cluster }, requestOptions(signal))),
  });
  const data = q.data as CapabilitiesSuccessV1 | undefined;
  return (
    <>
      <PageHeading
        title="能力与来源"
        description="查看当前环境可用的功能及数据来源。“未启用”和“待验收”均不能视为已通过真实运行验收。"
      />
      <div className="filters">
        <ScopeFilter value={cluster} />
      </div>
      <QueryState
        noScope={!cluster || invalid}
        loading={q.isPending && !!cluster}
        error={q.error}
        retry={() => void q.refetch()}
      >
        {data && <CapabilityStatus value={data.data} />}
      </QueryState>
    </>
  );
}

import { useSearchParams } from "react-router-dom";
import { ScopeFilter, useClusters } from "../components/ScopeFilter";
import { useQuery } from "@tanstack/react-query";
import { getCapabilities } from "../api/generated/platform";
import { checked, requestOptions } from "../api/client";
import { PageHeading } from "../components/PageHeading";
import { QueryState } from "../components/QueryState";
import { CapabilityStatus } from "../components/CapabilityStatus";
import type { CapabilitiesSuccessV1 } from "../api/generated/models";
export function CapabilitiesPage() {
  const [url, setUrl] = useSearchParams();
  const clusters = useClusters();
  const cluster = url.get("clusterUid") || clusters[0] || "";
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
        <ScopeFilter
          value={cluster}
          onChange={(v) => setUrl({ clusterUid: v })}
        />
      </div>
      <QueryState
        noScope={!cluster}
        loading={q.isPending && !!cluster}
        error={q.error}
        retry={() => void q.refetch()}
      >
        {data && <CapabilityStatus value={data.data} />}
      </QueryState>
    </>
  );
}

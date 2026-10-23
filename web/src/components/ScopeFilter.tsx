import { Select, Alert } from "antd";
import { useOperationalCluster } from "../app/ClusterContext";
export function useClusters() {
  return useOperationalCluster().candidates.map((cluster) => cluster.clusterUid);
}
export function ScopeFilter({
  value,
  onChange,
}: {
  value?: string;
  onChange?: (value: string) => void;
}) {
  const scope = useOperationalCluster();
  return (
    <div className="filter">
      <label htmlFor="cluster-filter">运行集群</label>
      <Select
        id="cluster-filter"
        aria-label="运行集群"
        value={value || scope.clusterUid || undefined}
        placeholder="选择已授权集群"
        showSearch
        optionFilterProp="label"
        status={scope.invalid ? "error" : undefined}
        options={scope.candidates.map((cluster) => ({
          value: cluster.clusterUid,
          label: `${cluster.displayName} · ${cluster.clusterUid}`,
        }))}
        onChange={(next) => {
          scope.requestCluster(next);
          onChange?.(next);
        }}
      />
      {scope.invalid ? (
        <Alert
          type="error"
          showIcon
          message="该集群不在授权范围，未回退到其他范围"
        />
      ) : null}
    </div>
  );
}

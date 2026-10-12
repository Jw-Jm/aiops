import { Select } from "antd";
import { useAuth } from "../auth/AuthProvider";
export function useClusters() {
  const { session } = useAuth();
  return session?.authorizedClusters.map((c) => c.clusterUid) || [];
}
export function ScopeFilter({
  value,
  onChange,
}: {
  value?: string;
  onChange: (value: string) => void;
}) {
  const { session } = useAuth();
  const clusters = useClusters();
  return (
    <div className="filter">
      <label htmlFor="cluster-filter">集群范围</label>
      <Select
        id="cluster-filter"
        aria-label="集群范围"
        value={value || undefined}
        placeholder="选择已授权集群"
        showSearch
        options={clusters.map((c) => ({
          value: c,
          label:
            session?.authorizedClusters.find((x) => x.clusterUid === c)
              ?.displayName || c,
        }))}
        onChange={onChange}
      />
    </div>
  );
}

import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Select, Spin } from "antd";
import { PageHeading } from "../components/PageHeading";
import { Surface, DataTable } from "../components/v12/primitives";
import { useOperationalCluster } from "../app/ClusterContext";
import { observation, type ObservationCapabilities } from "../api/observation";
import {
  metrics,
  type MetricCapability,
  type MetricResult,
} from "../api/metrics";

const datasets = [
  { id: "metrics.pod-cpu", name: "CPU 使用量", metric: "cpu_cores" },
  { id: "metrics.pod-memory", name: "内存工作集", metric: "memory_bytes" },
  {
    id: "metrics.pod-restarts",
    name: "容器重启增量",
    metric: "restart_increase",
  },
  { id: "metrics.pod-phase", name: "Pod 状态数量", metric: "phase_count" },
];
export function MetricsPage() {
  const { clusterUid, invalid } = useOperationalCluster();
  const [url] = useSearchParams();
  if (!clusterUid || invalid)
    return <Alert type="warning" title="请先选择有运维授权的运行集群。" />;
  return (
    <MetricWorkspace
      key={clusterUid + ":" + (url.get("canonicalId") || "")}
      cluster={clusterUid}
      initialResource={url.get("canonicalId") || ""}
    />
  );
}
function MetricWorkspace({
  cluster,
  initialResource,
}: {
  cluster: string;
  initialResource: string;
}) {
  const [canonical, setCanonical] = useState(initialResource),
    [dataset, setDataset] = useState(datasets[0].id),
    [result, setResult] = useState<MetricResult | null>(null),
    [error, setError] = useState<string | null>(null),
    [busy, setBusy] = useState(false);
  const generation = useRef(0),
    controller = useRef(new AbortController());
  useEffect(() => () => controller.current.abort(), []);
  function reset() {
    generation.current++;
    controller.current.abort();
    controller.current = new AbortController();
    setResult(null);
    setError(null);
    setBusy(false);
  }
  const resources = useQuery({
    queryKey: ["metric-resources", cluster],
    queryFn: ({ signal }) =>
      observation<ObservationCapabilities>("capabilities", cluster, signal),
  });
  const caps = useQuery({
    queryKey: ["metric-capabilities", cluster, canonical],
    enabled: !!canonical,
    refetchInterval: 10000,
    queryFn: ({ signal }) =>
      metrics<{ sourceCapabilities: MetricCapability[] }>(
        "capabilities",
        cluster,
        canonical,
        signal,
      ),
  });
  const available =
    caps.data?.sourceCapabilities.filter(
      (c) => c.datasetId === dataset && c.operation === "trend",
    ) ?? [];
  const selected = available.length === 1 ? available[0] : undefined;
  // shortcut: use whole-second windows; add subsecond template alignment before exposing finer ranges.
  const end = selected
    ? Math.floor(Date.parse(selected.coverageTo) / 1000) * 1000
    : 0;
  const start = end - 120000;
  const warm =
    !!selected &&
    !caps.error &&
    Date.parse(selected.verifiedUntil) > Date.now() &&
    start - 300000 >= Date.parse(selected.coverageFrom);
  async function query() {
    if (!warm || !selected || !canonical) return;
    reset();
    setBusy(true);
    const version = generation.current;
    const definition = datasets.find((d) => d.id === dataset)!;
    try {
      const value = await metrics<MetricResult>(
        "query",
        cluster,
        "",
        controller.current.signal,
        {
          canonicalId: canonical,
          intent: {
            schemaVersion: "query-intent/v1",
            datasetId: dataset,
            operation: "trend",
            metrics: [definition.metric],
            dimensions: ["time"],
            filters: [],
            sort: [],
            limit: 20,
            stepSeconds: 60,
            timeRange: {
              from: new Date(start).toISOString(),
              to: new Date(end).toISOString(),
              timezone: "Asia/Shanghai",
            },
          },
        },
      );
      if (version !== generation.current) return;
      setResult(value);
    } catch (e) {
      if (version === generation.current)
        setError(e instanceof Error ? e.message : "指标查询失败");
    } finally {
      if (version === generation.current) setBusy(false);
    }
  }
  const rows = result?.rows ?? [];
  const max = Math.max(...rows.map((r) => r.value), 1e-9),
    min = Math.min(...rows.map((r) => r.value), 0);
  const x = (timestamp: string) =>
    40 +
    ((Date.parse(timestamp) - Date.parse(result!.timeRange.from)) * 720) /
      Math.max(
        Date.parse(result!.timeRange.to) - Date.parse(result!.timeRange.from),
        1,
      );
  const points = rows
    .map(
      (r, i) =>
        `${i === 0 || Date.parse(r.dimension!) - Date.parse(rows[i - 1].dimension!) !== (result?.stepSeconds ?? 60) * 1000 ? "M" : "L"}${x(r.dimension!)},${190 - ((r.value - min) / (max - min)) * 150}`,
    )
    .join(" ");
  return (
    <div className="v12-logs-page">
      <PageHeading
        title="指标查询"
        description="按已授权 Pod 的固定身份读取真实持续采集指标；来源过期或不可用时停止新查询。"
      />
      <Surface title="查询条件">
        <div
          className="v12-resource-pad"
          style={{
            display: "flex",
            gap: 16,
            flexWrap: "wrap",
            alignItems: "end",
          }}
        >
          <label>
            资源 Pod
            <Select
              aria-label="指标资源"
              style={{ width: 300, display: "block" }}
              loading={resources.isFetching}
              value={canonical || undefined}
              options={resources.data?.resources.map((r) => ({
                value: r.canonicalId,
                label: r.displayName || r.uid,
              }))}
              onChange={(v) => {
                reset();
                setCanonical(v);
              }}
            />
          </label>
          <label>
            指标
            <Select
              aria-label="指标类型"
              style={{ width: 200, display: "block" }}
              value={dataset}
              options={datasets.map((d) => ({ value: d.id, label: d.name }))}
              onChange={(v) => {
                reset();
                setDataset(v);
              }}
            />
          </label>
          <Button
            onClick={() => {
              reset();
              void resources.refetch();
              void caps.refetch();
            }}
          >
            刷新来源
          </Button>
          <Button
            type="primary"
            disabled={!warm || caps.isFetching || busy}
            onClick={() => void query()}
          >
            查询指标
          </Button>
        </div>
      </Surface>
      {(resources.error || caps.error || error) && (
        <Alert
          type="error"
          showIcon
          title={error || resources.error?.message || caps.error?.message}
          action={
            <Button
              onClick={() => {
                reset();
                void resources.refetch();
                void caps.refetch();
              }}
            >
              重新核对
            </Button>
          }
        />
      )}
      {canonical && !caps.isFetching && !caps.error && !selected && (
        <Alert
          type="warning"
          title="当前没有合格的指标来源"
          description="采集尚未就绪、数据已过期或来源不可用；不会使用旧样例或将缺失数据显示为零。"
        />
      )}
      {selected && (
        <Alert
          type="info"
          title={warm ? "真实指标来源已就绪" : "正在积累采样窗口"}
          description={
            <span>
              采集器 {selected.collectorVersion}；水位 {selected.coverageTo}
              。固定 5 分钟回看和最近 2 分钟查询；覆盖完整性尚未证实。
              <br />
              来源 {selected.sourceId}
            </span>
          }
        />
      )}
      {busy && <Spin />}
      {result && (
        <Surface title="指标结果">
          <div className="v12-resource-pad">
            <p role="status">
              {result.status === "no_data"
                ? "所选范围没有指标样本，不能推断为零。"
                : `返回 ${rows.length} 个指标计算点 · ${result.unit} · ${result.exact ? "固定窗口计算" : "采集覆盖未知"}`}
            </p>
            <p>
              窗口 {result.timeRange.from} 至 {result.timeRange.to}；资源 UID{" "}
              {canonical.split("/").at(-1)}
            </p>
            {rows.length > 0 && (
              <svg
                viewBox="0 0 800 220"
                role="img"
                aria-label="指标时间趋势"
                style={{ width: "100%", maxHeight: 260 }}
              >
                <line
                  x1="40"
                  y1="190"
                  x2="760"
                  y2="190"
                  stroke="currentColor"
                />
                <path d={points} fill="none" stroke="#1677ff" strokeWidth="3" />
                {rows.map((r, i) => (
                  <circle
                    key={r.dimension || i}
                    cx={x(r.dimension!)}
                    cy={190 - ((r.value - min) / (max - min)) * 150}
                    r="4"
                    fill="#1677ff"
                  >
                    <title>
                      {r.dimension}: {r.value} {result.unit}
                    </title>
                  </circle>
                ))}
              </svg>
            )}
            <DataTable
              rowKey="dimension"
              pagination={false}
              dataSource={rows}
              columns={[
                { title: "时间", dataIndex: "dimension" },
                { title: `数值（${result.unit}）`, dataIndex: "value" },
              ]}
            />
            <p>
              来源 {result.sourceManifest.sourceId} · revision{" "}
              {result.sourceManifest.revision}；读取 {result.snapshotReadAt}
            </p>
          </div>
        </Surface>
      )}
    </div>
  );
}

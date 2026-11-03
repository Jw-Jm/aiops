import { useSearchParams } from "react-router-dom";
import { useEffect, useRef, useState } from "react";
import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Alert, Button, Input, Select, Spin, Tabs, Typography } from "antd";
import { ClockCircleOutlined, DownloadOutlined } from "@ant-design/icons";
import { ApiError } from "../api/client";
import { PageHeading } from "../components/PageHeading";
import { useOperationalCluster } from "../app/ClusterContext";
import { V12Drawer, V12Modal } from "../components/v12/Layer";
import { DataTable, StatusPill } from "../components/v12/primitives";
import {
  observation,
  observationHistory,
  type ObservationCapabilities,
  type ObservationView,
  type ObservationResult,
} from "../api/observation";

const active = (view?: ObservationView) =>
  !!view && ["accepted", "running", "cancel_requested"].includes(view.state);
export function LogsPage() {
  const { clusterUid, invalid } = useOperationalCluster();
  const [url] = useSearchParams();
  const initialResource = url.get("canonicalId") || "";
  if (!clusterUid || invalid)
    return <Alert type="warning" title="请先选择有运维授权的运行集群。" />;
  return (
    <LogWorkspace
      key={clusterUid + ":" + initialResource}
      cluster={clusterUid}
      initialResource={initialResource}
    />
  );
}
function LogWorkspace({
  cluster,
  initialResource,
}: {
  cluster: string;
  initialResource: string;
}) {
  const cache = useQueryClient();
  const [resource, setResource] = useState(initialResource);
  const [source, setSource] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [level, setLevel] = useState("");
  const [keyword, setKeyword] = useState("");
  const [queryId, setQueryId] = useState("");
  const [busy, setBusy] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [sourceOpen, setSourceOpen] = useState(false);
  const [sampleId, setSample] = useState<string | null>(null);
  const [detailTab, setDetailTab] = useState("fields");
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const [inspectorTab, setInspectorTab] = useState("summary");
  const [ascending, setAscending] = useState(false);
  const [wrap, setWrap] = useState(false);
  const [bucket, setBucket] = useState<number | null>(null);
  const generation = useRef(0),
    controller = useRef(new AbortController());
  useEffect(
    () => () => {
      generation.current++;
      controller.current.abort();
    },
    [],
  );
  const capabilities = useQuery({
    queryKey: ["observation-capabilities", cluster, resource],
    queryFn: ({ signal }) =>
      observation<ObservationCapabilities>(
        "capabilities",
        cluster,
        signal,
        undefined,
        resource,
      ),
  });
  const sources = capabilities.isError
    ? []
    : (capabilities.data?.sources ?? []);
  const selectedSource = sources.find(
    (s) => s.source.registrationId === source,
  );
  const selectedResource = capabilities.data?.resources.find(
    (r) => r.canonicalId === resource,
  );
  const sourceWindows = sources.map((s) => {
    const latestStart =
      Date.parse(s.coverageTo) - Math.min(1800, s.maxWindowSeconds) * 1000;
    return {
      sourceId: s.source.registrationId,
      from:
        latestStart > Date.parse(s.coverageFrom)
          ? new Date(Math.ceil(latestStart / 1000) * 1000).toISOString()
          : s.coverageFrom,
      to: s.coverageTo,
    };
  });
  const qualifiedWindow = sourceWindows.find((w) => w.sourceId === source);
  const windows = qualifiedWindow ? [qualifiedWindow] : [];
  if (from && to && !windows.some((w) => w.from === from && w.to === to))
    windows.push({ sourceId: source, from, to });
  const windowFormat = new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
  const status = useQuery({
    queryKey: ["observation-query", cluster, queryId],
    enabled: !!queryId,
    queryFn: ({ signal }) =>
      observation<ObservationView>(`queries/${queryId}`, cluster, signal),
    refetchInterval: (query) => (active(query.state.data) ? 750 : false),
  });
  const view = status.data;
  const result = useQuery({
    queryKey: ["observation-result", cluster, queryId],
    enabled: view?.state === "completed",
    queryFn: ({ signal }) =>
      observation<ObservationResult>(
        `queries/${queryId}/results`,
        cluster,
        signal,
      ),
  });
  const history = useInfiniteQuery({
    queryKey: ["observation-history", cluster],
    enabled: historyOpen,
    initialPageParam: "",
    queryFn: ({ signal, pageParam }) =>
      observationHistory(cluster, pageParam, signal),
    getNextPageParam: (page) => page.nextCursor ?? undefined,
  });
  function invalidate() {
    generation.current++;
    controller.current.abort();
    controller.current = new AbortController();
    setQueryId("");
    setError(null);
    setBusy(false);
    setExporting(false);
    setSample(null);
    setInspectorOpen(false);
    setBucket(null);
  }
  async function submit(
    selectedLevel = level,
    savedRequest?: ObservationView["request"],
  ) {
    const target = savedRequest?.resource ?? selectedResource;
    const binding = savedRequest?.source ?? selectedSource?.source;
    if (!target || !binding || (!savedRequest && (!from || !to))) {
      setError(new Error("请选择资源、已验证来源和完整的绝对时间窗。"));
      return;
    }
    invalidate();
    const current = generation.current;
    setBusy(true);
    try {
      const accepted = await observation<ObservationView>(
        "queries",
        cluster,
        controller.current.signal,
        {
          ...(savedRequest ?? {
            schemaVersion: "ui-v12-observability-query/v1",
            queryKind: "logs",
            templateId: "logs.records",
            templateVersion: "logs.records/v1",
            resource: {
              canonicalId: target.canonicalId,
              uid: target.uid,
              clusterUid: target.clusterUid,
              namespaceScope: target.namespaceScope,
            },
            source: binding,
            window: {
              from,
              to,
              timezone: "Asia/Shanghai",
              bounds: "[from,to)",
            },
            budget: capabilities.data!.budget,
          }),
          filters: selectedLevel
            ? [{ field: "level", operator: "eq", value: selectedLevel }]
            : [],
        },
      );
      if (current === generation.current) {
        cache.setQueryData(
          ["observation-query", cluster, accepted.queryId],
          accepted,
        );
        setQueryId(accepted.queryId);
      }
    } catch (cause) {
      if (current === generation.current)
        setError(
          cause instanceof Error
            ? cause
            : new Error("提交未确认，请核对查询历史。"),
        );
    } finally {
      if (current === generation.current) setBusy(false);
    }
  }
  async function cancel() {
    if (!view || busy) return;
    const current = generation.current;
    setBusy(true);
    setError(null);
    try {
      const latest = await observation<ObservationView>(
        `queries/${view.queryId}`,
        cluster,
        controller.current.signal,
      );
      if (current !== generation.current) return;
      cache.setQueryData(
        ["observation-query", cluster, latest.queryId],
        latest,
      );
      if (!active(latest) || latest.state === "cancel_requested") return;
      await observation(
        `queries/${view.queryId}:cancel`,
        cluster,
        controller.current.signal,
        { expectedRevision: latest.revision, reason: "用户取消本次查询" },
      );
      if (current === generation.current) await status.refetch();
    } catch (cause) {
      if (current === generation.current) {
        if (cause instanceof ApiError && cause.status === 409) {
          await status.refetch();
          if (current === generation.current)
            setError(new Error("查询状态已更新，请核对回执后再次取消。"));
        } else
          setError(
            cause instanceof Error
              ? cause
              : new Error("取消未确认，请刷新查询状态。"),
          );
      }
    } finally {
      if (current === generation.current) setBusy(false);
    }
  }
  const expired =
    !status.error &&
    (view?.state === "expired" ||
      (result.error instanceof ApiError && result.error.status === 410));
  async function exportSamples() {
    const current = generation.current;
    setExporting(true);
    setError(null);
    try {
      const fresh = await result.refetch({ throwOnError: true });
      if (current !== generation.current) return;
      const data = fresh.data!;
      const rows = data.samples.items
        .filter(
          (item) =>
            (!keyword || item.message.includes(keyword)) &&
            (bucket === null ||
              (Date.parse(item.timestamp) >= buckets[bucket].from &&
                Date.parse(item.timestamp) < buckets[bucket].to)),
        )
        .sort(
          (a, b) =>
            (ascending ? 1 : -1) *
            (Date.parse(a.timestamp) - Date.parse(b.timestamp)),
        );
      const csv = [
        [
          "观测时间（RFC3339）",
          "级别",
          "脱敏日志（返回样例）",
          "集群",
          "规范资源标识",
          "资源 UID",
          "命名空间 UID",
          "窗口开始",
          "窗口结束",
          "窗口时区",
          "来源",
          "来源版本",
          "查询编号",
          "结果摘要",
          "来源总体",
          "返回样例数",
          "导出样例数",
        ],
        ...rows.map((item) => [
          item.timestamp,
          item.level,
          item.message,
          data.resource.clusterUid,
          data.resource.canonicalId,
          data.resource.uid,
          data.resource.namespaceScope.namespaceUid,
          data.window.from,
          data.window.to,
          data.window.timezone,
          data.source.registrationId,
          data.source.registrationRevision,
          data.queryId,
          data.resultDigest,
          data.population.count ?? "未测量",
          data.samples.returned,
          rows.length,
        ]),
      ]
        .map((row) =>
          row
            .map((value) => {
              const text = String(value);
              // Source text must remain text when opened by a spreadsheet.
              const plain = /^\s*[=+\-@]|^[\t\r\n]/.test(text)
                ? "'" + text
                : text;
              return '"' + plain.replaceAll('"', '""') + '"';
            })
            .join(","),
        )
        .join("\r\n");
      const address = URL.createObjectURL(
        new Blob(["\ufeff" + csv], { type: "text/csv;charset=utf-8" }),
      );
      const link = document.createElement("a");
      link.href = address;
      link.download = `logs-${data.queryId}-samples.csv`;
      link.click();
      URL.revokeObjectURL(address);
    } catch (cause) {
      if (current === generation.current)
        setError(
          cause instanceof Error
            ? cause
            : new Error("结果导出未成功，请重新核对权限与来源。"),
        );
    } finally {
      if (current === generation.current) setExporting(false);
    }
  }
  const displayedError =
    error ??
    capabilities.error ??
    status.error ??
    (expired ? null : result.error);
  const snapshot =
    view?.state !== "completed" || status.error || result.error
      ? undefined
      : result.data;
  const queryView = status.error ? undefined : view;
  const samples = (snapshot?.samples.items ?? []).map((item, index) => ({
    ...item,
    rowId: `${queryId}:${index}`,
  }));
  const windowStart = snapshot ? Date.parse(snapshot.window.from) : 0;
  const windowEnd = snapshot ? Date.parse(snapshot.window.to) : 0;
  const buckets = Array.from(
    { length: Math.ceil((windowEnd - windowStart) / 300_000) },
    (_, index) => {
      const from = windowStart + index * 300_000,
        to = Math.min(from + 300_000, windowEnd);
      return {
        from,
        to,
        count: samples.filter(
          (item) =>
            Date.parse(item.timestamp) >= from &&
            Date.parse(item.timestamp) < to,
        ).length,
      };
    },
  );
  const bucketPeak = Math.max(1, ...buckets.map((item) => item.count));
  const timeFormat = new Intl.DateTimeFormat("zh-CN", {
    timeZone: snapshot?.window.timezone ?? "Asia/Shanghai",
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
  const items = samples
    .filter(
      (item) =>
        (!keyword || item.message.includes(keyword)) &&
        (bucket === null ||
          (Date.parse(item.timestamp) >= buckets[bucket].from &&
            Date.parse(item.timestamp) < buckets[bucket].to)),
    )
    .sort(
      (a, b) =>
        (ascending ? 1 : -1) *
        (Date.parse(a.timestamp) - Date.parse(b.timestamp)),
    );
  const sampleIndex = items.findIndex((item) => item.rowId === sampleId);
  const sample = items[sampleIndex];
  const fields =
    sample && snapshot
      ? {
          timestamp: sample.timestamp,
          level: sample.level,
          message: sample.message,
          resourceCanonicalId: snapshot.resource.canonicalId,
          resourceUid: snapshot.resource.uid,
          namespace: snapshot.resource.namespaceScope.namespace,
          namespaceUid: snapshot.resource.namespaceScope.namespaceUid,
          source: snapshot.source.registrationId,
          sourceRevision: snapshot.source.registrationRevision,
          window: snapshot.window,
          queryId: snapshot.queryId,
        }
      : undefined;
  const savedConditions =
    queryView?.request ??
    (queryView
      ? {
          resource: queryView.resource,
          source: queryView.source,
          window: queryView.window,
          filters: queryView.filters,
        }
      : undefined);
  return (
    <div className="v12-logs-page">
      <PageHeading
        title="日志查询"
        description="在固定资源与时间范围内检索日志，沿着字段和样例继续调查。"
        extra={
          <Button
            icon={<ClockCircleOutlined />}
            onClick={() => {
              setHistoryOpen(true);
            }}
          >
            查询历史
          </Button>
        }
      />
      <section className="v12-log-workspace">
        <form
          className="v12-log-query"
          onSubmit={(event) => {
            event.preventDefault();
            if (!busy && !active(view)) void submit();
          }}
        >
          <div className="v12-log-scope">
            <label>
              资源对象
              <Select
                aria-label="资源对象"
                value={resource || undefined}
                placeholder="请选择已授权 Pod"
                loading={capabilities.isPending}
                options={(capabilities.data?.resources ?? []).map((r) => ({
                  value: r.canonicalId,
                  label: `Pod / ${r.namespaceScope.namespace} / ${r.displayName ?? r.uid}`,
                }))}
                onChange={(value) => {
                  invalidate();
                  setResource(value);
                  setSource("");
                  setFrom("");
                  setTo("");
                }}
              />
            </label>
            <label>
              数据来源
              <Select
                aria-label="数据来源"
                disabled={capabilities.isFetching}
                value={source || undefined}
                placeholder="请选择已验证来源"
                options={sources.map((s) => ({
                  value: s.source.registrationId,
                  label:
                    s.source.collectorVersion === "kubernetes-pod-logs/v1"
                      ? s.name
                      : `VictoriaLogs · ${s.name}`,
                }))}
                onChange={(value) => {
                  invalidate();
                  setSource(value);
                  const window = sourceWindows.find(
                    (w) => w.sourceId === value,
                  )!;
                  setFrom(window.from);
                  setTo(window.to);
                }}
              />
            </label>
            <label>
              观测窗口 · UTC+08:00
              <Select
                aria-label="观测窗口"
                placeholder="请选择合格来源窗口"
                value={from && to ? JSON.stringify([from, to]) : undefined}
                options={windows.map((w) => ({
                  value: JSON.stringify([w.from, w.to]),
                  label: windowFormat
                    .formatRange(new Date(w.from), new Date(w.to))
                    .replaceAll("/", "-"),
                  title: `${w.from} – ${w.to}`,
                }))}
                onChange={(value) => {
                  invalidate();
                  const [nextFrom, nextTo] = JSON.parse(value);
                  setFrom(nextFrom);
                  setTo(nextTo);
                }}
              />
            </label>
            <div className="v12-log-run">
              {active(view) ? (
                <Button
                  danger
                  aria-label={
                    view?.state === "cancel_requested"
                      ? "取消待确认"
                      : "取消查询"
                  }
                  disabled={busy || view?.state === "cancel_requested"}
                  onClick={() => void cancel()}
                >
                  {view?.state === "cancel_requested"
                    ? "取消待确认"
                    : "取消查询"}
                </Button>
              ) : (
                <Button
                  type="primary"
                  autoInsertSpace={false}
                  aria-label="查询"
                  htmlType="submit"
                  loading={busy}
                  disabled={
                    capabilities.isFetching ||
                    !selectedResource ||
                    !selectedSource ||
                    !from ||
                    !to
                  }
                >
                  查询
                </Button>
              )}
              <Button
                disabled={!snapshot}
                href={
                  snapshot
                    ? `/assistant?${new URLSearchParams({ clusterUid: cluster, logQuery: snapshot.queryId })}`
                    : undefined
                }
              >
                按此资源与时间询问 AI
              </Button>
              <small>固定资源与绝对时间窗</small>
            </div>
          </div>
          <div className="v12-log-search">
            <label>
              关键词（脱敏样例内检索）
              <Input
                aria-label="关键词（脱敏样例内检索）"
                value={keyword}
                onChange={(e) => {
                  setSample(null);
                  setKeyword(e.target.value);
                }}
                onPressEnter={(event) => event.preventDefault()}
              />
            </label>
            <label>
              日志级别
              <Select
                aria-label="日志级别"
                value={level}
                options={[
                  { value: "", label: "全部级别" },
                  ...["error", "warn", "info", "debug"].map((value) => ({
                    value,
                    label: value.toUpperCase(),
                  })),
                ]}
                onChange={(value) => {
                  invalidate();
                  setLevel(value);
                }}
              />
            </label>
            <span>字面量检索 · 固定资源 · 最多 20 条脱敏样例</span>
          </div>
        </form>
        <div className="v12-log-context">
          <StatusPill
            value={selectedSource ? "active" : "pending"}
            label={selectedSource ? "资格已核对" : "待选择合格来源"}
          />
          <span>{selectedSource?.source.collectorVersion}</span>
          <Button
            disabled={!resource || busy || active(view)}
            loading={capabilities.isFetching}
            onClick={() => {
              invalidate();
              setSource("");
              setFrom("");
              setTo("");
              void capabilities.refetch();
            }}
          >
            刷新来源
          </Button>
          <Button type="text" onClick={() => setSourceOpen(true)}>
            查看来源状态
          </Button>
        </div>
      </section>
      {resource && capabilities.isSuccess && !selectedResource && (
        <Alert
          type="warning"
          showIcon
          title="指定资源不在当前授权日志范围内，请返回资源目录重新选择。"
        />
      )}
      {selectedSource?.source.collectorVersion === "kubernetes-pod-logs/v1" && (
        <Alert
          type="info"
          showIcon
          title="直接读取当前容器的 Kubernetes 原生日志；不含轮转前或前次容器日志。日志总体未测量，空样例不代表该时段没有日志。需要较新日志时刷新来源并重新选择窗口。"
        />
      )}
      {selectedResource && capabilities.isSuccess && sources.length === 0 && (
        <Alert
          type="info"
          showIcon
          title="当前资源没有合格日志来源。"
          description="历史结果仍按原授权与保留规则读取；新查询需要当前合格来源。"
        />
      )}
      {displayedError ? (
        <Alert
          type="error"
          showIcon
          title={displayedError.message}
          action={
            <Button
              onClick={() => {
                setError(null);
                void capabilities.refetch();
                if (queryId) void status.refetch();
                if (view?.state === "completed") void result.refetch();
              }}
            >
              刷新核对
            </Button>
          }
        />
      ) : null}
      <section className="v12-log-results" aria-live="polite">
        {busy ||
        active(view) ||
        (view?.state === "completed" && result.isPending) ? (
          <div className="v12-log-state">
            <Spin />
            <h3>
              {view?.state === "cancel_requested"
                ? "取消请求已提交，等待终止回执"
                : "正在读取选定范围"}
            </h3>
            <p>核对资源身份、来源版本和查询预算；可以取消本次查询。</p>
          </div>
        ) : expired ? (
          <div className="v12-log-state">
            <h3>查询结果已过期</h3>
            <p>历史结果内容已停止提供，请重新查询当前获准范围。</p>
            <Button onClick={() => void submit()}>重新查询</Button>
          </div>
        ) : view?.state === "failed" || view?.state === "denied" ? (
          <div className="v12-log-state">
            <h3>查询未成功</h3>
            <p>{view.failure?.message}</p>
            <Button onClick={() => void submit()}>重新查询</Button>
          </div>
        ) : view?.state === "terminated" ? (
          <div className="v12-log-state">
            <h3>本次查询已终止</h3>
            <p>
              {view.termination?.sourceStopped
                ? "查询未发往来源。"
                : "平台已停止接收结果；来源端停止尚未确认。"}
            </p>
            <Button onClick={() => void submit()}>重新查询</Button>
          </div>
        ) : snapshot ? (
          <>
            <div className="v12-log-summary">
              {snapshot.status === "no_data"
                ? "所选范围没有日志"
                : snapshot.population.basis === "not_measured"
                  ? `日志总体未测量 · 本次脱敏样例 ${snapshot.samples.returned} 条`
                  : `来源总体 ${snapshot.population.count ?? "未测量"} 条 · 本次脱敏样例 ${snapshot.samples.returned} 条`}
              <div className="v12-log-result-actions">
                <span>查询 {snapshot.queryId}</span>
                <Button
                  size="small"
                  onClick={() => {
                    setInspectorTab("summary");
                    setInspectorOpen(true);
                  }}
                >
                  查询检查
                </Button>
                <Button
                  size="small"
                  icon={<DownloadOutlined />}
                  aria-label="导出数据"
                  loading={exporting}
                  disabled={items.length === 0}
                  onClick={() => void exportSamples()}
                >
                  导出数据
                </Button>
              </div>
            </div>
            <div className="v12-log-histogram">
              <div className="v12-log-hist-heading">
                <h3>返回样例的时间分布</h3>
                <span>柱高仅表示本次样例数，不代表日志总体 · 5 分钟分桶</span>
              </div>
              <div
                className="v12-log-hist-bars"
                style={{
                  gridTemplateColumns: `repeat(${buckets.length},minmax(0,1fr))`,
                }}
              >
                {buckets.map((item, index) => (
                  <button
                    type="button"
                    key={item.from}
                    aria-pressed={bucket === index}
                    className={bucket === index ? "active" : ""}
                    aria-label={`筛选 ${timeFormat.format(item.from)} 至 ${timeFormat.format(item.to)} 的返回样例，共 ${item.count} 条`}
                    onClick={() => {
                      setSample(null);
                      setBucket(index);
                    }}
                  >
                    <span>{item.count}</span>
                    <i
                      style={{
                        height: Math.max(2, (item.count / bucketPeak) * 52),
                      }}
                      aria-hidden="true"
                    />
                    <small>{timeFormat.format(item.from)}</small>
                  </button>
                ))}
              </div>
            </div>
            <div className="v12-log-layout">
              <aside className="v12-log-facets" aria-label="返回样例字段">
                <h3>级别分布</h3>
                <p>本次返回 {snapshot.samples.returned} 条样例</p>
                <button
                  type="button"
                  onClick={() => {
                    setLevel("");
                    void submit("", queryView?.request);
                  }}
                >
                  全部级别
                </button>
                {["error", "warn", "info", "debug", "unknown"].map((value) => (
                  <button
                    type="button"
                    key={value}
                    aria-label={`${value.toUpperCase()} · 按级别查询`}
                    disabled={value === "unknown"}
                    title={
                      value === "unknown"
                        ? "来源暂不支持按未知级别查询"
                        : undefined
                    }
                    onClick={() => {
                      setLevel(value);
                      void submit(value, queryView?.request);
                    }}
                  >
                    <span>
                      <i className={`v12-log-level-dot ${value}`} />
                      {value.toUpperCase()}
                    </span>
                    <b>
                      {samples.filter((item) => item.level === value).length}
                    </b>
                  </button>
                ))}
                <hr />
                <h3>资源字段</h3>
                <dl className="v12-log-fields">
                  <dt>namespace</dt>
                  <dd title={snapshot.resource.namespaceScope.namespaceUid}>
                    {snapshot.resource.namespaceScope.namespace}
                  </dd>
                  <dt>resource</dt>
                  <dd title={snapshot.resource.canonicalId}>
                    {selectedResource?.displayName ?? snapshot.resource.uid}
                  </dd>
                  <dt>source</dt>
                  <dd>
                    VictoriaLogs<small>{snapshot.source.registrationId}</small>
                  </dd>
                </dl>
              </aside>
              <section className="v12-log-main">
                <div className="v12-log-tools">
                  <span>{items.length} 条当前可见样例</span>
                  {bucket !== null ? (
                    <Button
                      onClick={() => {
                        setSample(null);
                        setBucket(null);
                      }}
                    >
                      清除时间筛选 ×
                    </Button>
                  ) : null}
                  <Button
                    onClick={() => setAscending(!ascending)}
                    aria-label={ascending ? "时间：正序" : "时间：倒序"}
                  >
                    {ascending ? "时间：正序 ↑" : "时间：倒序 ↓"}
                  </Button>
                  <Button aria-pressed={wrap} onClick={() => setWrap(!wrap)}>
                    {wrap ? "关闭换行" : "自动换行"}
                  </Button>
                </div>
                <div
                  className={
                    wrap ? "v12-log-list v12-log-wrap" : "v12-log-list"
                  }
                  aria-label="返回日志样例"
                >
                  {items.map((item) => (
                    <button
                      type="button"
                      key={item.rowId}
                      className="v12-log-entry"
                      aria-label={`日志详情 ${item.timestamp} ${item.level.toUpperCase()}`}
                      onClick={() => {
                        setSample(item.rowId);
                        setDetailTab("fields");
                      }}
                    >
                      <time dateTime={item.timestamp} title={item.timestamp}>
                        {timeFormat.format(new Date(item.timestamp))}
                      </time>
                      <span className={`v12-log-level ${item.level}`}>
                        {item.level.toUpperCase()}
                      </span>
                      <code className="v12-log-message">{item.message}</code>
                      <span className="v12-log-arrow" aria-hidden="true">
                        ›
                      </span>
                    </button>
                  ))}
                  {items.length === 0 ? (
                    <div className="v12-log-state">
                      <h3>没有匹配的返回样例</h3>
                      <p>
                        调整级别、关键词或样例时间筛选；这不代表完整日志总体没有异常。
                      </p>
                    </div>
                  ) : null}
                </div>
                <p className="v12-log-footnote">
                  脱敏样例 · 点击日志查看字段和原始内容 ·
                  不由样例数推断错误发生率。采集完整覆盖未证实。
                </p>
              </section>
            </div>
          </>
        ) : (
          <div className="v12-log-state">
            <h3>请选择资源与来源后查询</h3>
            <p>仅展示当前授权范围与已验证来源；查询历史保留服务端回执。</p>
          </div>
        )}
      </section>
      <V12Modal
        open={historyOpen}
        title="查询历史"
        onClose={() => setHistoryOpen(false)}
        width={840}
        footer={
          <>
            <Button
              disabled={!history.hasNextPage || history.isFetching}
              loading={history.isFetchingNextPage}
              onClick={() => void history.fetchNextPage()}
            >
              加载更多
            </Button>
            <Button onClick={() => setHistoryOpen(false)}>关闭</Button>
          </>
        }
      >
        <p>仅显示当前仍获授权、来源资格有效的个人查询。</p>
        {history.error ? (
          <Alert type="error" title={history.error.message} />
        ) : (
          <DataTable
            rowKey="queryId"
            loading={history.isPending}
            dataSource={history.data?.pages.flatMap((page) => page.data) ?? []}
            pagination={false}
            columns={[
              { title: "创建时间", dataIndex: "createdAt" },
              { title: "资源 UID", render: (_, row) => row.resource.uid },
              { title: "状态", dataIndex: "state" },
              {
                title: "操作",
                render: (_, row) => (
                  <Button
                    onClick={() => {
                      invalidate();
                      setResource(row.resource.canonicalId);
                      setSource(row.source.registrationId);
                      setFrom(row.window.from);
                      setTo(row.window.to);
                      setLevel(
                        row.filters?.find((f) => f.field === "level")?.value ??
                          "",
                      );
                      setKeyword("");
                      cache.setQueryData(
                        ["observation-query", cluster, row.queryId],
                        row,
                      );
                      setQueryId(row.queryId);
                      setHistoryOpen(false);
                    }}
                  >
                    查看
                  </Button>
                ),
              },
            ]}
          />
        )}
      </V12Modal>
      <V12Modal
        open={sourceOpen}
        title="来源状态"
        onClose={() => setSourceOpen(false)}
        width={760}
        footer={<Button onClick={() => setSourceOpen(false)}>关闭</Button>}
      >
        <p>
          来源注册、查询资格与数据覆盖分别核对。未配置或过期资格的来源不会成为可选项。
        </p>
        {sources.map((s) => (
          <div key={s.source.registrationId}>
            <strong>{s.source.registrationId}</strong>
            <p>
              登记版本 {s.source.registrationRevision} · 凭据版本{" "}
              {s.source.credentialRevision} · {s.source.collectorVersion}
            </p>
            <p>
              可查询窗口 {s.coverageFrom} 至 {s.coverageTo}
            </p>
            <p>
              {s.source.collectorVersion === "kubernetes-pod-logs/v1"
                ? "本次已核对当前容器与 pods/log 读取权限；提交和读取结果时重新核对，轮转覆盖未知。"
                : `资格有效至 ${s.verifiedUntil}；采集完整覆盖未证实`}
            </p>
          </div>
        ))}
      </V12Modal>
      <V12Drawer
        open={inspectorOpen && !!queryView && !!snapshot}
        title="查询检查"
        onClose={() => setInspectorOpen(false)}
        footer={
          <Typography.Text
            copyable={{
              text: JSON.stringify(savedConditions, null, 2),
              tooltips: ["复制查询条件", "已复制"],
            }}
          >
            复制查询条件
          </Typography.Text>
        }
      >
        <Tabs
          activeKey={inspectorTab}
          onChange={setInspectorTab}
          items={[
            {
              key: "summary",
              label: "概况",
              children: (
                <DataTable
                  rowKey="field"
                  pagination={false}
                  columns={[
                    { title: "字段", dataIndex: "field" },
                    { title: "值", dataIndex: "value" },
                  ]}
                  dataSource={
                    queryView && snapshot
                      ? [
                          { field: "查询编号", value: queryView.queryId },
                          { field: "结果状态", value: snapshot.status },
                          { field: "资源 UID", value: queryView.resource.uid },
                          {
                            field: "所属集群 / 命名空间",
                            value: `${queryView.resource.clusterUid} / ${queryView.resource.namespaceScope.namespace}`,
                          },
                          {
                            field: "观测窗口",
                            value: `${queryView.window.from} 至 ${queryView.window.to} · ${queryView.window.timezone} · ${queryView.window.bounds}`,
                          },
                          {
                            field: "来源实例 / 固定版本",
                            value: `${queryView.source.registrationId} / r${queryView.source.registrationRevision}`,
                          },
                          { field: "结果摘要", value: snapshot.resultDigest },
                          {
                            field: "样本资格",
                            value: "脱敏返回样例，不代表完整日志总体",
                          },
                        ]
                      : []
                  }
                />
              ),
            },
            {
              key: "request",
              label: "查询条件",
              children: (
                <>
                  <pre className="v12-log-json">
                    {JSON.stringify(savedConditions, null, 2)}
                  </pre>
                  <p>
                    {queryView?.request
                      ? "以上为服务端已保存的完整查询请求，包含固定模板和执行预算。"
                      : "旧版服务只返回已保存的查询范围，未返回完整执行预算。"}
                    当前关键词仅筛返回样例，不改变已提交请求。
                  </p>
                </>
              ),
            },
            {
              key: "data",
              label: "结果快照",
              children: (
                <pre className="v12-log-json">
                  {JSON.stringify(snapshot, null, 2)}
                </pre>
              ),
            },
          ]}
        />
      </V12Drawer>
      <V12Drawer
        open={!!sample}
        title="日志详情"
        onClose={() => setSample(null)}
        footer={
          <div className="v12-log-detail-actions">
            <Button
              disabled={sampleIndex <= 0}
              onClick={() => setSample(items[sampleIndex - 1].rowId)}
            >
              上一条
            </Button>
            <Button
              disabled={sampleIndex < 0 || sampleIndex >= items.length - 1}
              onClick={() => setSample(items[sampleIndex + 1].rowId)}
            >
              下一条
            </Button>
            <Typography.Text
              copyable={{
                text: JSON.stringify(fields, null, 2),
                tooltips: ["复制原始内容", "已复制"],
              }}
            >
              复制原始内容
            </Typography.Text>
          </div>
        }
      >
        {sample && fields ? (
          <>
            <StatusPill
              value={sample.level}
              label={sample.level.toUpperCase()}
            />
            <p>
              {sample.timestamp} · {snapshot?.queryId}
            </p>
            <Tabs
              activeKey={detailTab}
              onChange={setDetailTab}
              items={[
                {
                  key: "fields",
                  label: "字段",
                  children: (
                    <DataTable
                      rowKey="field"
                      pagination={false}
                      columns={[
                        { title: "字段", dataIndex: "field" },
                        {
                          title: "值",
                          dataIndex: "value",
                          render: (value, row) =>
                            row.field === "level" ? (
                              <Button
                                onClick={() => {
                                  setLevel(sample.level);
                                  void submit(sample.level, queryView?.request);
                                }}
                              >
                                {sample.level.toUpperCase()} · 按此筛选
                              </Button>
                            ) : (
                              value
                            ),
                        },
                      ]}
                      dataSource={Object.entries(fields).map(
                        ([field, value]) => ({
                          field,
                          value:
                            typeof value === "object"
                              ? JSON.stringify(value)
                              : String(value),
                        }),
                      )}
                    />
                  ),
                },
                {
                  key: "raw",
                  label: "原始内容",
                  children: (
                    <pre className="v12-log-json">
                      {JSON.stringify(fields, null, 2)}
                    </pre>
                  ),
                },
              ]}
            />
            <p>
              内容已脱敏；字段与原始内容使用同一份返回快照，仅在当前可见样例中切换。
            </p>
          </>
        ) : null}
      </V12Drawer>
    </div>
  );
}

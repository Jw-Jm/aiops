import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Input, Select, Button, Space } from "antd";
import { DataTable as Table } from "../components/v12/primitives";
import { Link, useSearchParams } from "react-router-dom";
import {
  getResources,
  getResourceByCanonicalID,
} from "../api/generated/platform";
import type {
  GetResourcesParams,
  ResourcePageV2,
  ResourceEntityV2,
} from "../api/generated/models";
import { checked, requestOptions } from "../api/client";
import { PageHeading } from "../components/PageHeading";
import { ScopeFilter } from "../components/ScopeFilter";
import { useOperationalCluster } from "../app/ClusterContext";
import { QueryState } from "../components/QueryState";
import { EnvelopeMeta } from "../components/EnvelopeMeta";
import { FullText, Details } from "../components/Details";
import { Status } from "../components/Status";
import { V12Drawer, V12Modal } from "../components/v12/Layer";
import { record, text, displayTime } from "../components/data";
export function ResourcesPage() {
  const { scopeKey } = useOperationalCluster();
  const [url] = useSearchParams();
  return <ResourceCatalog key={scopeKey + ":" + url.toString()} />;
}
function ResourceCatalog() {
  const [selected, setSelected] = useState<ResourceEntityV2>();
  const [selection, setSelection] = useState<string[]>([]);
  const [comparisonIds, setComparisonIds] = useState<string[]>([]);
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<Error>();
  const operation = useRef<AbortController | undefined>(undefined);
  useEffect(() => () => operation.current?.abort(), []);
  const [url, setUrl] = useSearchParams();
  const { clusterUid: cluster, invalid } = useOperationalCluster();
  const params: GetResourcesParams = {
    limit: [5, 10, 20, 50].includes(Number(url.get("limit")))
      ? Number(url.get("limit"))
      : 10,
    clusterUid: cluster,
    includeFilterOptions: true,
    order: url.get("order") === "desc" ? "desc" : "asc",
    ...Object.fromEntries(
      [
        "q",
        "domain",
        "kind",
        "namespace",
        "name",
        "health",
        "label",
        "cursor",
        "sort",
      ]
        .filter((k) => url.has(k))
        .map((k) => [k, url.get(k)!]),
    ),
  };
  const update = (key: string, value: string) => {
    const next = new URLSearchParams(url);
    next.delete("cursor");
    if (value) next.set(key, value);
    else next.delete(key);
    setUrl(next);
  };
  const query = useQuery({
    queryKey: ["resources", params],
    enabled: !!params.clusterUid && !invalid,
    queryFn: async ({ signal }) =>
      checked(getResources(params, requestOptions(signal))),
  });
  const page = query.data as ResourcePageV2 | undefined;
  const options = query.isError
    ? {}
    : record(record(page?.meta.summary).filterOptions);
  const readCurrent = async (ids: string[], signal: AbortSignal) => {
    const result = await checked(
      getResources(
        { ...params, canonicalIds: ids, cursor: undefined, limit: 50 },
        requestOptions(signal),
      ),
    );
    if (
      result.data.length !== ids.length ||
      result.data.some((node) => !ids.includes(node.canonicalId))
    )
      throw new Error("资源已变化或授权范围已收窄，请刷新目录后重新选择。");
    return result;
  };
  const comparison = useQuery({
    queryKey: ["resource-comparison", params, comparisonIds],
    enabled:
      comparisonIds.length >= 2 &&
      comparisonIds.length <= 4 &&
      !query.isError &&
      !invalid,
    queryFn: ({ signal }) => readCurrent(comparisonIds, signal),
  });
  const comparisonRows = comparisonIds.map((id) =>
    comparison.data?.data.find((node) => node.canonicalId === id),
  );
  const exportPage = async () => {
    if (!page?.data.length || query.isError || exporting) return;
    const ids = page.data.map((node) => node.canonicalId);
    const matched = record(page.meta.summary).total;
    const controller = new AbortController();
    operation.current = controller;
    setExporting(true);
    setExportError(undefined);
    try {
      const current = await readCurrent(ids, controller.signal);
      if (controller.signal.aborted) return;
      const revision = record(current.meta.graphRevision);
      const csv = [
        [
          "名称",
          "类型",
          "资源领域",
          "健康状态",
          "命名空间",
          "来源",
          "规范资源标识",
          "资源UID",
          "集群UID",
          "最近观测",
          "读取时间",
          "OwnerEpoch",
          "GraphGeneration",
          "请求ID",
          "投影时效",
          "部分结果",
          "覆盖说明",
          "本页导出数",
          "点击时匹配总数",
        ],
        ...current.data.map((node) => [
          node.name,
          node.kind,
          node.attributes.resourceDomain,
          String(node.attributes.health ?? "unknown"),
          node.namespace || "",
          node.sourceRefs.join(";"),
          node.canonicalId,
          node.stableId,
          node.scope,
          node.lastObservedAt,
          current.meta.collectedAt,
          revision.ownerEpoch,
          revision.graphGeneration,
          current.requestId,
          current.meta.freshness,
          current.meta.partial,
          current.meta.warnings,
          current.data.length,
          matched,
        ]),
      ]
        .map((row) =>
          row
            .map((value) => {
              const raw = value === undefined ? "未提供" : String(value);
              const plain = /^\s*[=+\-@]|^[\t\r\n]/.test(raw) ? "'" + raw : raw;
              return '"' + plain.replaceAll('"', '""') + '"';
            })
            .join(","),
        )
        .join("\r\n");
      const address = URL.createObjectURL(
        new Blob(["\ufeff" + csv], { type: "text/csv;charset=utf-8" }),
      );
      const anchor = document.createElement("a");
      anchor.href = address;
      anchor.download = `resources-${cluster}-page.csv`;
      anchor.click();
      URL.revokeObjectURL(address);
    } catch (cause) {
      if (!controller.signal.aborted)
        setExportError(
          cause instanceof Error
            ? cause
            : new Error("导出未完成，请重新核对当前资源。"),
        );
    } finally {
      if (!controller.signal.aborted) setExporting(false);
    }
  };
  const detail = useQuery({
    queryKey: ["resource-quick-view", cluster, selected?.canonicalId],
    enabled: !!selected && !invalid && !query.isError,
    queryFn: ({ signal }) =>
      checked(
        getResourceByCanonicalID(
          { canonicalId: selected!.canonicalId },
          requestOptions(signal),
        ),
      ),
  });
  const shownDetail = detail.data?.data.nodes.find(
    (node) => node.canonicalId === selected?.canonicalId,
  );
  return (
    <>
      <PageHeading
        title="资源目录"
        description="以资源身份为中心查看状态、关系、告警与观测。"
        extra={
          <Space>
            <Button
              loading={exporting}
              disabled={!page?.data.length || query.isError || invalid}
              onClick={() => void exportPage()}
            >
              导出当前结果
            </Button>
            <Button
              loading={query.isFetching}
              onClick={() => {
                setSelected(undefined);
                setSelection([]);
                setComparisonIds([]);
                void query.refetch();
              }}
            >
              刷新
            </Button>
            <Link
              to={
                "/resources/topology?" +
                new URLSearchParams({ clusterUid: cluster })
              }
            >
              <Button>关系拓扑</Button>
            </Link>
          </Space>
        }
      />
      <section className="section">
        <h2>授权资源</h2>
        <p className="muted">
          导出仅包含本页资源的重新授权投影，不代表全部匹配结果；选择 2 至 4
          个本页资源可对比静态字段。
        </p>
        {exportError && <QueryState loading={false} error={exportError} />}
        {selection.length > 0 && (
          <Space className="status-note">
            <span>已选择 {selection.length} 项</span>
            <Button
              disabled={
                selection.length < 2 || selection.length > 4 || query.isError
              }
              onClick={() => setComparisonIds([...selection])}
            >
              对比资源
            </Button>
            <Button onClick={() => setSelection([])}>取消选择</Button>
          </Space>
        )}
        <div className="filters">
          <div className="filter">
            <label htmlFor="resource-q">搜索资源</label>
            <Input
              id="resource-q"
              type="search"
              maxLength={256}
              placeholder="资源名称、类型或 UID"
              defaultValue={params.q || ""}
              onPressEnter={(event) => update("q", event.currentTarget.value)}
              onBlur={(event) => {
                if (event.target.value !== (params.q || ""))
                  update("q", event.target.value);
              }}
            />
          </div>
          <ScopeFilter value={params.clusterUid} />
          <div className="filter">
            <label htmlFor="resource-domain">资源领域</label>
            <Select
              id="resource-domain"
              allowClear
              value={params.domain}
              placeholder="全部领域"
              options={[
                { value: "hardware", label: "硬件" },
                { value: "kubernetes", label: "Kubernetes" },
                { value: "network", label: "网络" },
                { value: "storage", label: "存储" },
              ]}
              onChange={(v) => update("domain", v || "")}
            />
          </div>
          {(["kind", "namespace"] as const).map((k) => (
            <div className="filter" key={k}>
              <label htmlFor={"resource-" + k}>
                {k === "kind" ? "资源类型" : "命名空间"}
              </label>
              <Select
                key={k + ":" + query.isError}
                id={"resource-" + k}
                allowClear
                showSearch
                optionFilterProp="label"
                loading={query.isFetching}
                disabled={invalid || query.isPending || query.isError}
                placeholder={k === "kind" ? "全部类型" : "全部授权命名空间"}
                value={params[k] || undefined}
                options={[
                  ...(Array.isArray(
                    options[k === "kind" ? "kinds" : "namespaces"],
                  )
                    ? (
                        options[
                          k === "kind" ? "kinds" : "namespaces"
                        ] as string[]
                      ).map((value) => ({ value, label: value }))
                    : []),
                  ...(k === "namespace" && options.clusterScoped === true
                    ? [{ value: "__cluster__", label: "集群级资源" }]
                    : []),
                ]}
                onChange={(value) => update(k, value || "")}
              />
            </div>
          ))}
          {["name", "label"].map((k) => (
            <div className="filter" key={k}>
              <label htmlFor={"resource-" + k}>
                {
                  {
                    name: "名称",
                    kind: "资源类型",
                    namespace: "命名空间",
                    health: "健康状态",
                    label: "标签（key=value）",
                  }[k]
                }
              </label>
              <Input
                id={"resource-" + k}
                defaultValue={url.get(k) || ""}
                key={k + location.search}
                allowClear
                onPressEnter={(e) => update(k, e.currentTarget.value)}
                onBlur={(e) => {
                  if (e.target.value !== (url.get(k) || ""))
                    update(k, e.target.value);
                }}
              />
            </div>
          ))}
          <div className="filter">
            <label htmlFor="resource-health">健康状态</label>
            <Select
              id="resource-health"
              allowClear
              placeholder="全部状态"
              value={params.health || undefined}
              options={[
                { value: "normal", label: "正常" },
                { value: "abnormal", label: "异常" },
                { value: "unknown", label: "未知" },
                { value: "degraded", label: "已降级" },
              ]}
              onChange={(v) => update("health", v || "")}
            />
          </div>
          <div className="filter">
            <label htmlFor="resource-sort">排序</label>
            <Select
              id="resource-sort"
              value={params.sort || "name"}
              options={["name", "kind", "updatedAt"].map((v) => ({
                label: (
                  {
                    name: "名称",
                    kind: "资源类型",
                    updatedAt: "更新时间",
                  } as Record<string, string>
                )[v],
                value: v,
              }))}
              onChange={(v) => update("sort", v)}
            />
          </div>
          <Button
            onClick={() =>
              update("order", params.order === "asc" ? "desc" : "asc")
            }
          >
            {params.order === "asc" ? "升序" : "降序"}
          </Button>
          <Button
            onClick={() => {
              const next = new URLSearchParams(url);
              for (const key of [
                "q",
                "domain",
                "kind",
                "namespace",
                "name",
                "health",
                "label",
                "cursor",
                "sort",
                "order",
              ])
                next.delete(key);
              setUrl(next);
            }}
          >
            重置
          </Button>
        </div>
        <p className="muted">
          类型和命名空间候选来自当前授权范围的完整资源投影，不受本页或筛选条件限制；不代表来源完整覆盖。
        </p>
        <QueryState
          loading={query.isPending && !!params.clusterUid}
          error={query.error}
          retry={() => void query.refetch()}
          noScope={!params.clusterUid || invalid}
          empty={page?.data.length === 0}
        >
          <EnvelopeMeta value={page?.meta} />
          <Table<ResourceEntityV2>
            rowKey="canonicalId"
            rowSelection={{
              selectedRowKeys: selection,
              onChange: (keys) => setSelection(keys.map(String)),
              getTitleCheckboxProps: () => ({ "aria-label": "选择本页资源" }),
              getCheckboxProps: (node) => ({
                "aria-label":
                  "选择 " +
                  node.name +
                  " · " +
                  node.kind +
                  " · " +
                  (node.namespace || "集群级资源"),
              }),
            }}
            dataSource={page?.data}
            pagination={false}
            expandable={{ expandedRowRender: (row) => <Details value={row} /> }}
            scroll={{ x: 940 }}
            columns={[
              {
                title: (
                  <button
                    className="link"
                    onClick={() => {
                      const next = new URLSearchParams(url);
                      next.delete("cursor");
                      next.set("sort", "name");
                      next.set(
                        "order",
                        params.sort === "name" && params.order === "asc"
                          ? "desc"
                          : "asc",
                      );
                      setUrl(next);
                    }}
                  >
                    资源名称 ↕
                  </button>
                ),
                dataIndex: "name",
                width: 230,
                render: (v, row) => (
                  <>
                    <Link
                      to={
                        "/resources/detail?" +
                        new URLSearchParams({
                          clusterUid: cluster,
                          canonicalId: row.canonicalId,
                        })
                      }
                    >
                      {v}
                    </Link>
                    <div className="muted">{row.stableId}</div>
                  </>
                ),
              },
              {
                title: "类型 / 领域",
                render: (_, row) => (
                  <>
                    {row.kind}
                    <div className="muted">
                      {(
                        {
                          kubernetes: "Kubernetes",
                          network: "网络",
                          storage: "存储",
                          hardware: "物理硬件",
                        } as Record<string, string>
                      )[String(row.attributes.resourceDomain)] || row.domain}
                    </div>
                  </>
                ),
              },
              {
                title: "健康状态",
                width: 160,
                render: (_, row) => (
                  <Status value={record(row.attributes).health ?? "unknown"} />
                ),
              },
              {
                title: "命名空间",
                render: (_, row) => row.namespace || "集群级资源",
              },
              {
                title: "来源 / 最近观测",
                width: 200,
                render: (_, row) => (
                  <>
                    <FullText value={row.sourceRefs.join(", ")} />
                    <div className="muted">
                      {displayTime(row.lastObservedAt)}
                    </div>
                  </>
                ),
              },
              {
                title: "操作",
                render: (_, row) => (
                  <Space>
                    <button className="link" onClick={() => setSelected(row)}>
                      速览
                    </button>
                    <Link
                      to={
                        "/resources/detail?" +
                        new URLSearchParams({
                          clusterUid: cluster,
                          canonicalId: row.canonicalId,
                        })
                      }
                    >
                      详情
                    </Link>
                  </Space>
                ),
              },
            ]}
          />
          <div className="pagination">
            <span className="muted">
              本页 {page?.data.length ?? 0} 条 · 当前授权匹配{" "}
              {text(record(page?.meta.summary).total)} 条
            </span>
            <Space>
              <label>
                每页{" "}
                <select
                  aria-label="资源每页条数"
                  value={params.limit}
                  onChange={(event) => update("limit", event.target.value)}
                >
                  {[5, 10, 20, 50].map((limit) => (
                    <option key={limit} value={limit}>
                      {limit} 条
                    </option>
                  ))}
                </select>
              </label>
              <Button
                disabled={!url.has("cursor")}
                onClick={() => update("cursor", "")}
              >
                回到第一页
              </Button>
              <Button
                disabled={
                  typeof page?.meta.nextCursor !== "string" ||
                  !page.meta.nextCursor
                }
                onClick={() => {
                  const next = new URLSearchParams(url);
                  next.set("cursor", String(page?.meta.nextCursor));
                  setUrl(next);
                }}
              >
                下一页
              </Button>
            </Space>
          </div>
        </QueryState>
      </section>
      <V12Modal
        open={comparisonIds.length > 0 && !query.isError && !invalid}
        title="资源对比"
        width={920}
        onClose={() => setComparisonIds([])}
      >
        <QueryState
          loading={comparison.isPending || comparison.isFetching}
          error={comparison.error}
          retry={() => void comparison.refetch()}
        >
          {comparison.data && !comparison.isError && (
            <>
              <EnvelopeMeta value={comparison.data.meta} />
              <Table
                rowKey="field"
                pagination={false}
                dataSource={[
                  {
                    field: "类型",
                    values: comparisonRows.map((node) => node?.kind),
                  },
                  {
                    field: "状态",
                    values: comparisonRows.map(
                      (node) => node?.attributes.health ?? "unknown",
                    ),
                  },
                  {
                    field: "命名空间",
                    values: comparisonRows.map(
                      (node) => node?.namespace || "集群级资源",
                    ),
                  },
                  {
                    field: "资源 UID",
                    values: comparisonRows.map((node) => node?.stableId),
                  },
                  {
                    field: "来源",
                    values: comparisonRows.map((node) =>
                      node?.sourceRefs.join(", "),
                    ),
                  },
                  {
                    field: "最近观测",
                    values: comparisonRows.map((node) =>
                      displayTime(node?.lastObservedAt),
                    ),
                  },
                  {
                    field: "规范资源标识",
                    values: comparisonRows.map((node) => node?.canonicalId),
                  },
                ]}
                columns={[
                  { title: "字段", dataIndex: "field" },
                  ...comparisonRows.map((node, index) => ({
                    title: node?.name,
                    key: comparisonIds[index],
                    render: (
                      _: unknown,
                      row: { field: string; values: unknown[] },
                    ) =>
                      row.field === "状态" ? (
                        <Status value={row.values[index]} />
                      ) : (
                        <FullText value={row.values[index]} />
                      ),
                  })),
                ]}
              />
              <p className="muted">
                只对比同次授权读取的静态投影字段，不将不同资源的指标或证据混合计算。
              </p>
            </>
          )}
        </QueryState>
      </V12Modal>
      <V12Drawer
        open={!!selected && !invalid && !query.isError}
        title={selected?.name || "资源速览"}
        onClose={() => setSelected(undefined)}
        footer={
          shownDetail && !detail.isError && !detail.isFetching ? (
            <Space>
              <Link
                to={
                  "/resources/detail?" +
                  new URLSearchParams({
                    clusterUid: cluster,
                    canonicalId: shownDetail.canonicalId,
                  })
                }
              >
                进入资源详情
              </Link>
              <Link
                to={
                  "/resources/topology?" +
                  new URLSearchParams({
                    clusterUid: cluster,
                    canonicalId: shownDetail.canonicalId,
                  })
                }
              >
                查看关系
              </Link>
            </Space>
          ) : null
        }
      >
        <QueryState
          loading={detail.isPending || detail.isFetching}
          error={detail.error}
          empty={detail.isSuccess && !shownDetail}
          retry={() => void detail.refetch()}
        >
          {shownDetail && (
            <>
              <Status value={shownDetail.attributes.health ?? "unknown"} />
              <p>
                {shownDetail.kind} · {shownDetail.namespace || "集群级资源"}
              </p>
              <Details
                value={{
                  clusterUid: shownDetail.scope,
                  namespace: shownDetail.namespace,
                  uid: shownDetail.stableId,
                  canonicalId: shownDetail.canonicalId,
                  sourceRefs: shownDetail.sourceRefs,
                  lastObservedAt: shownDetail.lastObservedAt,
                  attributes: shownDetail.attributes,
                  labels: shownDetail.labels,
                }}
              />
            </>
          )}
        </QueryState>
      </V12Drawer>
    </>
  );
}

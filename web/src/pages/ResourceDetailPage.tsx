import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Tabs, Space, Typography } from "antd";
import { Link, useSearchParams } from "react-router-dom";
import {
  getResources,
  getResourceByCanonicalID,
  getResourceNeighbors,
  getFindings,
  getIncidents,
} from "../api/generated/platform";
import type {
  ResourceEntityV2,
  FindingPageV2,
  IncidentPageV2,
} from "../api/generated/models";
import { checked, requestOptions } from "../api/client";
import { useOperationalCluster } from "../app/ClusterContext";
import { navItem } from "../app/navigation";
import { PageHeading } from "../components/PageHeading";
import { Details, FullText } from "../components/Details";
import { QueryState } from "../components/QueryState";
import { EnvelopeMeta } from "../components/EnvelopeMeta";
import { Status } from "../components/Status";
import { list, record, text, displayTime } from "../components/data";
import { symptomLabel } from "../components/FaultSummary";
import { Surface, DataTable } from "../components/v12/primitives";
import { V12Drawer } from "../components/v12/Layer";
import { ResourceGraphExplorer } from "../features/graph/ResourceGraphExplorer";
import { TopologyResult } from "../features/graph/TopologyResult";

export function ResourceDetailPage() {
  const { clusterUid, invalid, scopeKey } = useOperationalCluster();
  const [url] = useSearchParams();
  const id = url.get("canonicalId") || "";
  return (
    <QueryState loading={false} error={null} noScope={!clusterUid || invalid}>
      <ResourceDetail key={scopeKey + ":" + id} cluster={clusterUid} id={id} />
    </QueryState>
  );
}

function ResourceDetail({ cluster, id }: { cluster: string; id: string }) {
  const [url, setUrl] = useSearchParams();
  const { candidates } = useOperationalCluster();
  const requestedTab = url.get("tab") || "summary";
  const tab =
    requestedTab === "graph"
      ? "relations"
      : ["relations", "observations", "events"].includes(requestedTab)
        ? requestedTab
        : "summary";
  const resolution = useQuery({
    queryKey: ["resource-detail-root", cluster, id],
    enabled: !!id,
    queryFn: ({ signal }) =>
      checked(
        getResources(
          { clusterUid: cluster, canonicalIds: [id], limit: 1 },
          requestOptions(signal),
        ),
      ),
  });
  const resolved = resolution.data?.data.find(
    (node) => node.canonicalId === id,
  );
  const resource = useQuery({
    queryKey: ["resource-detail", cluster, id],
    enabled: !!resolved && !resolution.isFetching && !resolution.isError,
    queryFn: ({ signal }) =>
      checked(
        getResourceByCanonicalID({ canonicalId: id }, requestOptions(signal)),
      ),
  });
  const graph = resource.data?.data;
  const entity = graph?.nodes.find((node) => node.canonicalId === id);
  const current =
    resolved &&
    !resolution.isFetching &&
    !resolution.isError &&
    !resource.isFetching &&
    !resource.isError
      ? entity
      : undefined;
  const clusterName =
    candidates.find((candidate) => candidate.clusterUid === cluster)
      ?.displayName || cluster;
  const href = (path: string, node = current) =>
    path +
    "?" +
    new URLSearchParams({
      clusterUid: cluster,
      ...(node ? { canonicalId: node.canonicalId } : {}),
    });
  return (
    <div className="v12-resource-detail">
      <PageHeading
        title={current?.name || "资源详情"}
        kicker={
          current
            ? current.kind + " / " + (current.namespace || "集群级资源")
            : undefined
        }
        description="资源信息、关联关系和观测结果都绑定同一资源身份。"
        extra={
          <Space>
            <Link
              to={"/resources?" + new URLSearchParams({ clusterUid: cluster })}
            >
              <Button>返回资源目录</Button>
            </Link>
            {current && (
              <Link to={href("/assistant")}>
                <Button type="primary">AI 排查</Button>
              </Link>
            )}
            <Button
              aria-label="刷新资源"
              disabled={!id}
              loading={resolution.isFetching || resource.isFetching}
              onClick={() => void resolution.refetch()}
            >
              刷新
            </Button>
          </Space>
        }
      />
      <QueryState
        loading={!!id && (resolution.isPending || resolution.isFetching)}
        error={resolution.error}
        empty={!id || (resolution.isSuccess && !resolved)}
        retry={() => void resolution.refetch()}
      >
        <QueryState
          loading={resource.isPending || resource.isFetching}
          error={resource.error}
          empty={resource.isSuccess && !entity}
          retry={() => void resource.refetch()}
        >
          {current && graph && (
            <>
              <div className="v12-resource-meta">
                <Status value={current.attributes.health ?? "unknown"} />
                <span>当前资源投影；来源覆盖与资源健康分别记录。</span>
                <span className="v12-resource-uid">{current.stableId}</span>
              </div>
              <EnvelopeMeta value={graph} />
              <Tabs
                activeKey={tab}
                onChange={(value) => {
                  const next = new URLSearchParams(url);
                  next.set("tab", value);
                  setUrl(next);
                }}
                items={[
                  { key: "summary", label: "资源概况" },
                  { key: "relations", label: "关系与影响" },
                  { key: "observations", label: "指标与证据" },
                  { key: "events", label: "关联事件" },
                ]}
              />
              {tab === "summary" && (
                <>
                  <div className="v12-resource-grid">
                    <Surface
                      title="基本信息"
                      extra={
                        <Typography.Text
                          copyable={{ text: current.canonicalId }}
                        >
                          复制标识
                        </Typography.Text>
                      }
                    >
                      <div className="v12-resource-pad">
                        <dl className="v12-resource-kv">
                          <dt>资源名称</dt>
                          <dd>{current.name}</dd>
                          <dt>资源类型</dt>
                          <dd>{current.kind}</dd>
                          <dt>所属集群</dt>
                          <dd>
                            {clusterName} / {cluster}
                          </dd>
                          <dt>命名空间</dt>
                          <dd>{current.namespace || "集群级资源"}</dd>
                          <dt>唯一身份</dt>
                          <dd>
                            <FullText value={current.canonicalId} />
                          </dd>
                          <dt>UID</dt>
                          <dd>{current.stableId}</dd>
                          <dt>数据来源</dt>
                          <dd>
                            {current.sourceRefs.join(", ") || "未提供来源标识"}
                          </dd>
                          <dt>最近观测</dt>
                          <dd>{displayTime(current.lastObservedAt)}</dd>
                        </dl>
                      </div>
                    </Surface>
                    <Surface title="状态与标签">
                      <div className="v12-resource-pad">
                        <Status
                          value={current.attributes.health ?? "unknown"}
                        />
                        <dl className="v12-resource-kv v12-resource-labels">
                          {Object.entries(current.labels).map(
                            ([key, value]) => (
                              <div key={key}>
                                <dt>{key}</dt>
                                <dd>{value}</dd>
                              </div>
                            ),
                          )}
                        </dl>
                        {Object.keys(current.labels).length === 0 && (
                          <p className="muted">当前投影未提供标签。</p>
                        )}
                        <p className="muted">
                          仅显示脱敏安全投影；未知状态保留为未知。
                        </p>
                        <details>
                          <summary>查看安全状态投影</summary>
                          <Details value={current.attributes} />
                        </details>
                      </div>
                    </Surface>
                  </div>
                  <Surface title="来源版本与覆盖说明">
                    <div className="v12-resource-pad">
                      <p className="muted">
                        来源登记版本用于追溯本次资源投影，不代表连续覆盖或业务正常。
                      </p>
                      <DataTable
                        rowKey={(source) =>
                          source.sourceRegistrationId + "@" + source.revision
                        }
                        dataSource={graph.observationSources || []}
                        pagination={false}
                        locale={{
                          emptyText: "当前读取未提供来源版本；不推断为已验证。",
                        }}
                        expandable={{
                          expandedRowRender: (source) => (
                            <Details value={source} />
                          ),
                        }}
                        columns={[
                          {
                            title: "来源登记",
                            dataIndex: "sourceRegistrationId",
                            render: (value) => <FullText value={value} />,
                          },
                          { title: "登记版本", dataIndex: "revision" },
                        ]}
                      />
                      <Details
                        value={{
                          graphRevision: graph.graphRevision,
                          collectedAt: graph.collectedAt,
                          freshness: graph.freshness,
                          partial: graph.partial,
                          warnings: graph.warnings,
                          degradedSources: graph.degradedSources,
                        }}
                      />
                    </div>
                  </Surface>
                </>
              )}
              {tab === "relations" &&
                (requestedTab === "graph" ? (
                  <Surface title="高级关系查询">
                    <ResourceGraphExplorer canonicalId={current.canonicalId} />
                  </Surface>
                ) : (
                  <ResourceRelations
                    key={current.canonicalId}
                    resource={current}
                    cluster={cluster}
                  />
                ))}
              {tab === "observations" && (
                <div className="v12-resource-grid">
                  {[
                    ["metrics", "指标查询"],
                    ["logs", "日志查询"],
                    ["network", "网络统计"],
                  ].map(([path, title]) => {
                    const available =
                      navItem("/observe/" + path)?.available &&
                      (path !== "logs" || current.kind === "Pod");
                    return (
                      <Surface key={path} title={title}>
                        <div className="v12-resource-pad">
                          <p className="muted">
                            以 {current.name}{" "}
                            的规范身份进入工作台，查询前重新核对资源与来源资格。
                          </p>
                          {available ? (
                            <Link to={href("/observe/" + path)}>
                              <Button type="primary">进入{title}</Button>
                            </Link>
                          ) : (
                            <>
                              <Button disabled>进入{title}</Button>
                              <p className="muted">
                                {path === "logs"
                                  ? "当前日志模板仅支持已授权 Pod。"
                                  : "当前版本尚未开放此工作台。"}
                              </p>
                            </>
                          )}
                        </div>
                      </Surface>
                    );
                  })}
                </div>
              )}
              {tab === "events" && (
                <>
                  <ResourceEventList cluster={cluster} canonicalId={id} />
                  <ResourceEventList
                    cluster={cluster}
                    canonicalId={id}
                    incidents
                  />
                </>
              )}
            </>
          )}
        </QueryState>
      </QueryState>
    </div>
  );
}

function ResourceRelations({
  resource,
  cluster,
}: {
  resource: ResourceEntityV2;
  cluster: string;
}) {
  const [url, setUrl] = useSearchParams();
  const depth = url.get("graphDepth") === "1" ? 1 : 2;
  const listView = url.get("graphView") === "list";
  const [selected, setSelected] = useState<ResourceEntityV2>();
  const query = useQuery({
    queryKey: [
      "resource-detail-relations",
      cluster,
      resource.canonicalId,
      depth,
    ],
    queryFn: ({ signal }) =>
      checked(
        getResourceNeighbors(
          {
            canonicalId: resource.canonicalId,
            depth,
            maxNodes: 200,
            maxEdges: 400,
          },
          requestOptions(signal),
        ),
      ),
  });
  const detail = useQuery({
    queryKey: ["resource-relation-detail", cluster, selected?.canonicalId],
    enabled: !!selected && !query.isError,
    queryFn: ({ signal }) =>
      checked(
        getResourceByCanonicalID(
          { canonicalId: selected!.canonicalId },
          requestOptions(signal),
        ),
      ),
  });
  const node = detail.data?.data.nodes.find(
    (node) => node.canonicalId === selected?.canonicalId,
  );
  const change = (key: string, value: string) => {
    const next = new URLSearchParams(url);
    next.set(key, value);
    setSelected(undefined);
    setUrl(next);
  };
  return (
    <Surface
      title="资源关系"
      extra={
        <Link
          to={
            "/resources/topology?" +
            new URLSearchParams({
              clusterUid: cluster,
              canonicalId: resource.canonicalId,
              graphDepth: String(depth),
            })
          }
        >
          <Button>全页查看</Button>
        </Link>
      }
    >
      <div className="v12-topology-toolbar">
        <label>
          关系深度{" "}
          <select
            aria-label="关系深度"
            value={depth}
            onChange={(event) => change("graphDepth", event.target.value)}
          >
            <option value="1">1 层</option>
            <option value="2">2 层</option>
          </select>
        </label>
        <div className="v12-topology-modes">
          <button
            className={!listView ? "active" : ""}
            onClick={() => change("graphView", "graph")}
          >
            关系图
          </button>
          <button
            className={listView ? "active" : ""}
            onClick={() => change("graphView", "list")}
          >
            关系列表
          </button>
        </div>
      </div>
      <QueryState
        loading={query.isPending}
        error={query.error}
        retry={() => void query.refetch()}
      >
        {query.data && (
          <TopologyResult
            graph={query.data.data}
            rootId={resource.canonicalId}
            listView={listView}
            select={setSelected}
          />
        )}
      </QueryState>
      <V12Drawer
        open={!!selected && !query.isError}
        title="资源详情"
        onClose={() => setSelected(undefined)}
        footer={
          node && !detail.isError && !detail.isFetching ? (
            <Link
              to={
                "/resources/detail?" +
                new URLSearchParams({
                  clusterUid: cluster,
                  canonicalId: node.canonicalId,
                })
              }
            >
              进入资源详情
            </Link>
          ) : null
        }
      >
        <QueryState
          loading={detail.isPending || detail.isFetching}
          error={detail.error}
          empty={detail.isSuccess && !node}
          retry={() => void detail.refetch()}
        >
          <Details value={node} />
        </QueryState>
      </V12Drawer>
    </Surface>
  );
}

function ResourceEventList({
  cluster,
  canonicalId,
  incidents = false,
}: {
  cluster: string;
  canonicalId: string;
  incidents?: boolean;
}) {
  const [url, setUrl] = useSearchParams();
  const key = incidents ? "incidentCursor" : "findingCursor";
  const cursor = url.get(key) || undefined;
  const params = {
    clusterUid: cluster,
    resourceCanonicalId: canonicalId,
    limit: 20,
    cursor,
  };
  const query = useQuery<FindingPageV2 | IncidentPageV2>({
    queryKey: ["resource-events", incidents, params],
    queryFn: ({ signal }) =>
      incidents
        ? checked(getIncidents(params, requestOptions(signal)))
        : checked(getFindings(params, requestOptions(signal))),
  });
  const rows = list(record(query.data).data).map(record),
    meta = record(record(query.data).meta);
  const title = incidents ? "关联故障" : "关联告警";
  const navigatePage = (cursor: string) => {
    const next = new URLSearchParams(url);
    cursor ? next.set(key, cursor) : next.delete(key);
    setUrl(next);
  };
  return (
    <div role="region" aria-label={title}>
      <Surface title={title}>
        <div className="v12-resource-pad">
          <p className="muted">
            仅查询此规范身份的直接关联记录；未发现记录不代表资源正常。
          </p>
          <QueryState
            loading={query.isPending}
            error={query.error}
            empty={query.isSuccess && rows.length === 0}
            retry={() => void query.refetch()}
          >
            <EnvelopeMeta value={meta} />
            <DataTable
              rowKey={(row) => text(incidents ? row.incidentId : row.findingId)}
              dataSource={rows}
              pagination={false}
              expandable={{
                expandedRowRender: (row) => <Details value={row} />,
              }}
              columns={[
                {
                  title: incidents ? "故障" : "告警现象",
                  render: (_, row) =>
                    incidents ? (
                      <Link
                        to={
                          "/events/incidents/" +
                          encodeURIComponent(text(row.incidentId)) +
                          "?" +
                          new URLSearchParams({ clusterUid: cluster })
                        }
                      >
                        {text(row.incidentId)}
                      </Link>
                    ) : (
                      symptomLabel(row.normalizedSymptom ?? row.ruleId)
                    ),
                },
                {
                  title: "严重度",
                  render: (_, row) => <Status value={row.severity} />,
                },
                {
                  title: "状态",
                  render: (_, row) => (
                    <Status value={row.state ?? row.lifecycleState} />
                  ),
                },
                {
                  title: incidents ? "最近更新" : "最近观测",
                  render: (_, row) =>
                    displayTime(row.updatedAt ?? row.observedAt),
                },
              ]}
            />
          </QueryState>
          <div className="pagination">
            <span className="muted">
              {query.isSuccess
                ? `本页 ${rows.length} 条 · 每页最多20条`
                : "尚无可显示的读取结果"}
            </span>
            <Space>
              <Button disabled={!cursor} onClick={() => navigatePage("")}>
                回到第一页
              </Button>
              <Button
                disabled={
                  query.isError ||
                  typeof meta.nextCursor !== "string" ||
                  !meta.nextCursor
                }
                onClick={() => navigatePage(String(meta.nextCursor))}
              >
                下一页
              </Button>
            </Space>
          </div>
        </div>
      </Surface>
    </div>
  );
}

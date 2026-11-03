import { useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Button } from "antd";
import { Link, useSearchParams } from "react-router-dom";
import {
  getResources,
  getResourceNeighbors,
  getResourceByCanonicalID,
} from "../api/generated/platform";
import type { ResourceEntityV2 } from "../api/generated/models";
import { checked, requestOptions } from "../api/client";
import { useOperationalCluster } from "../app/ClusterContext";
import { PageHeading } from "../components/PageHeading";
import { QueryState } from "../components/QueryState";
import { EnvelopeMeta } from "../components/EnvelopeMeta";
import { Details } from "../components/Details";
import { Surface } from "../components/v12/primitives";
import { V12Drawer } from "../components/v12/Layer";
import { TopologyResult } from "../features/graph/TopologyResult";

export function TopologyPage() {
  const { clusterUid, invalid, scopeKey } = useOperationalCluster();
  return (
    <QueryState loading={false} error={null} noScope={!clusterUid || invalid}>
      <TopologyWorkspace key={scopeKey} cluster={clusterUid} />
    </QueryState>
  );
}

function TopologyWorkspace({ cluster }: { cluster: string }) {
  const [url, setUrl] = useSearchParams();
  const requested = url.get("canonicalId") || "";
  const depth = url.get("graphDepth") === "1" ? 1 : 2;
  const listView = url.get("graphView") === "list";
  const [selected, setSelected] = useState<ResourceEntityV2>();
  const candidates = useInfiniteQuery({
    queryKey: ["topology-roots", cluster],
    initialPageParam: "",
    queryFn: ({ signal, pageParam }) =>
      checked(
        getResources(
          { clusterUid: cluster, limit: 50, cursor: pageParam || undefined },
          requestOptions(signal),
        ),
      ),
    getNextPageParam: (page) =>
      typeof page.meta.nextCursor === "string" && page.meta.nextCursor
        ? page.meta.nextCursor
        : undefined,
  });
  const roots = candidates.data?.pages.flatMap((page) => page.data) ?? [];
  const rootId = requested || roots[0]?.canonicalId || "";
  // Resolve explicit links through the current cluster's authorized list, including roots outside the first page.
  const root = useQuery({
    queryKey: ["topology-root", cluster, rootId],
    enabled: !!rootId,
    queryFn: ({ signal }) =>
      checked(
        getResources(
          { clusterUid: cluster, canonicalIds: [rootId], limit: 1 },
          requestOptions(signal),
        ),
      ),
  });
  const entity = root.data?.data[0];
  const neighbors = useQuery({
    queryKey: ["topology-neighbors", cluster, entity?.canonicalId, depth],
    enabled: !!entity && !root.isError,
    queryFn: ({ signal }) =>
      checked(
        getResourceNeighbors(
          {
            canonicalId: entity!.canonicalId,
            depth,
            maxNodes: 200,
            maxEdges: 400,
          },
          requestOptions(signal),
        ),
      ),
  });
  const graph = neighbors.data?.data;
  const detail = useQuery({
    queryKey: ["topology-detail", cluster, selected?.canonicalId],
    enabled: !!selected && !neighbors.isError && !root.isError,
    queryFn: ({ signal }) =>
      checked(
        getResourceByCanonicalID(
          { canonicalId: selected!.canonicalId },
          requestOptions(signal),
        ),
      ),
  });
  const change = (key: string, value: string) => {
    const next = new URLSearchParams(url);
    next.set("clusterUid", cluster);
    next.set(key, value);
    setSelected(undefined);
    setUrl(next);
  };
  const choices =
    entity && !roots.some((node) => node.canonicalId === entity.canonicalId)
      ? [entity, ...roots]
      : roots;
  const result = !root.isError && !neighbors.isError ? graph : undefined;
  const shownDetail = detail.data?.data.nodes.find(
    (node) => node.canonicalId === selected?.canonicalId,
  );
  return (
    <>
      <PageHeading
        title="关系拓扑"
        description="从一个资源出发查看依赖、归属与影响，不推断未经验证的因果。"
        extra={
          <Button
            disabled={!entity || root.isError || neighbors.isError}
            onClick={() => setSelected(entity)}
          >
            查看根资源
          </Button>
        }
      />
      <div className="v12-topology-page">
        <Surface title="关系范围">
          <div className="v12-topology-toolbar">
            <label>
              根资源{" "}
              <select
                aria-label="根资源"
                value={rootId}
                onChange={(event) => change("canonicalId", event.target.value)}
              >
                {!entity && requested && (
                  <option value={requested}>核对指定资源</option>
                )}
                {choices.map((node) => (
                  <option key={node.canonicalId} value={node.canonicalId}>
                    {node.kind} / {node.name} · {node.namespace || "集群资源"}
                  </option>
                ))}
              </select>
            </label>
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
            {candidates.hasNextPage && (
              <Button
                loading={candidates.isFetchingNextPage}
                onClick={() => void candidates.fetchNextPage()}
              >
                更多根资源
              </Button>
            )}
            <Button
              disabled={!entity}
              loading={root.isFetching || neighbors.isFetching}
              onClick={async () => {
                setSelected(undefined);
                const response = await root.refetch();
                if (!response.isError && response.data?.data.length)
                  void neighbors.refetch();
              }}
            >
              刷新关系
            </Button>
            <div className="v12-topology-modes" aria-label="拓扑展示方式">
              <button
                className={!listView ? "active" : ""}
                aria-pressed={!listView}
                onClick={() => change("graphView", "graph")}
              >
                关系图
              </button>
              <button
                className={listView ? "active" : ""}
                aria-pressed={listView}
                onClick={() => change("graphView", "list")}
              >
                关系列表
              </button>
            </div>
          </div>
          <QueryState
            loading={candidates.isPending || (!!rootId && root.isPending)}
            error={root.error || candidates.error}
            empty={!rootId || (root.isSuccess && !entity)}
            retry={() => {
              void candidates.refetch();
              if (rootId) void root.refetch();
            }}
          >
            {candidates.data?.pages.map((page, index) =>
              page.meta.partial ||
              (Array.isArray(page.meta.degradedSources) &&
                page.meta.degradedSources.length) ? (
                <EnvelopeMeta key={index} value={page.meta} />
              ) : null,
            )}
            <QueryState
              loading={neighbors.isPending}
              error={neighbors.error}
              retry={() => void neighbors.refetch()}
            >
              {result && (
                <TopologyResult
                  key={rootId + ":" + depth}
                  graph={result}
                  rootId={rootId}
                  listView={listView}
                  select={setSelected}
                />
              )}
            </QueryState>
          </QueryState>
        </Surface>
      </div>
      <V12Drawer
        open={!!selected && !root.isError && !neighbors.isError}
        title="资源详情"
        onClose={() => setSelected(undefined)}
        footer={
          shownDetail && !detail.isError ? (
            <Link
              to={
                "/resources/detail?" +
                new URLSearchParams({
                  clusterUid: cluster,
                  canonicalId: shownDetail.canonicalId,
                })
              }
            >
              打开完整资源详情
            </Link>
          ) : null
        }
      >
        <QueryState
          loading={detail.isPending}
          error={detail.error}
          empty={detail.isSuccess && !shownDetail}
          retry={() => void detail.refetch()}
        >
          <Details value={shownDetail} />
        </QueryState>
      </V12Drawer>
    </>
  );
}

import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Input, Select, Space } from "antd";
import { useSearchParams } from "react-router-dom";
import { getResourceNeighbors } from "../../api/generated/platform";
import type {
  ResourceGraphV2,
  GetResourceNeighborsDirection,
} from "../../api/generated/models";
import { checked, requestOptions, authEvents } from "../../api/client";
import { QueryState } from "../../components/QueryState";
import { GraphView } from "./GraphView";
import { edgeExpired } from "./layout";

function distances(
  graph: ResourceGraphV2,
  root: string,
  direction: GetResourceNeighborsDirection,
) {
  const out = new Map<string, number>([[root, 0]]);
  for (let depth = 0; depth < 2; depth++) {
    for (const edge of graph.edges) {
      if (edgeExpired(edge)) continue;
      if (
        direction !== "in" &&
        out.get(edge.from) === depth &&
        !out.has(edge.to)
      )
        out.set(edge.to, depth + 1);
      if (
        direction !== "out" &&
        out.get(edge.to) === depth &&
        !out.has(edge.from)
      )
        out.set(edge.from, depth + 1);
    }
  }
  return out;
}
function edgeKey(edge: ResourceGraphV2["edges"][number]) {
  return JSON.stringify([
    edge.from,
    edge.to,
    edge.kind,
    edge.provenance.sourceRegistrationId,
    edge.provenance.ruleVersion,
  ]);
}
export function ResourceGraphExplorer({
  canonicalId,
}: {
  canonicalId: string;
}) {
  const [url, setUrl] = useSearchParams();
  const initialDepth = url.get("graphDepth") === "2" ? 2 : 1;
  const requestedLimit = Number(url.get("graphLimit"));
  const maxNodes = [25, 50, 100, 200].includes(requestedLimit)
    ? requestedLimit
    : 100;
  const direction: GetResourceNeighborsDirection =
    url.get("graphDirection") === "in"
      ? "in"
      : url.get("graphDirection") === "out"
        ? "out"
        : "both";
  const relation = (url.get("graphRelation") ?? "").slice(0, 128);
  const [relationInput, setRelationInput] = useState(relation);
  const scope = JSON.stringify([
    canonicalId,
    initialDepth,
    maxNodes,
    direction,
    relation,
  ]);
  const liveScope = useRef(scope);
  liveScope.current = scope;
  const epoch = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);
  const [expanded, setExpanded] = useState<{
    scope: string;
    base: ResourceGraphV2;
    value: ResourceGraphV2;
  }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  const [note, setNote] = useState("");
  const query = useQuery({
    queryKey: ["resource-progressive-graph", scope],
    enabled: !!canonicalId,
    queryFn: ({ signal }) =>
      checked(
        getResourceNeighbors(
          {
            canonicalId,
            direction,
            relationKind: relation || undefined,
            depth: initialDepth,
            maxNodes,
            maxEdges: 400,
          },
          requestOptions(signal),
        ),
      ),
  });
  const base = query.data?.data;
  const graph =
    base && expanded?.scope === scope && expanded.base === base
      ? expanded.value
      : base;
  const levels = graph
    ? distances(graph, canonicalId, direction)
    : new Map<string, number>();
  const cancel = () => {
    epoch.current++;
    controller.current?.abort();
    setBusy(false);
    setExpanded(undefined);
    setError(undefined);
    setNote("");
  };
  useEffect(() => {
    cancel();
    setRelationInput(relation);
    const revoked = () => cancel();
    for (const name of ["expired", "forbidden", "authorizationChanged"])
      authEvents.addEventListener(name, revoked);
    return () => {
      epoch.current++;
      controller.current?.abort();
      for (const name of ["expired", "forbidden", "authorizationChanged"])
        authEvents.removeEventListener(name, revoked);
    };
  }, [scope]);
  const change = (key: string, value: string) => {
    const n = new URLSearchParams(url);
    value ? n.set(key, value) : n.delete(key);
    setUrl(n);
  };
  const canExpand = (id: string) =>
    !!graph &&
    !busy &&
    !query.isFetching &&
    (levels.get(id) ?? 2) < 2 &&
    graph.nodes.length < maxNodes &&
    ![
      "VirtualMachine",
      "VirtualMachineInstance",
      "DataVolume",
      "VM",
      "VMI",
    ].includes(graph.nodes.find((n) => n.canonicalId === id)?.kind ?? "");
  const expand = async (id: string) => {
    if (!graph || !base || !canExpand(id)) return;
    const version = epoch.current,
      snapshotScope = scope,
      snapshotBase = base;
    const abort = new AbortController();
    controller.current?.abort();
    controller.current = abort;
    setBusy(true);
    setError(undefined);
    setNote("");
    try {
      const response = await checked(
        getResourceNeighbors(
          {
            canonicalId: id,
            direction,
            relationKind: relation || undefined,
            depth: 1,
            maxNodes,
            maxEdges: 400,
          },
          requestOptions(abort.signal),
        ),
      );
      if (
        abort.signal.aborted ||
        version !== epoch.current ||
        liveScope.current !== snapshotScope
      )
        return;
      const patch = response.data;
      if (
        patch.graphRevision.ownerEpoch !== graph.graphRevision.ownerEpoch ||
        patch.graphRevision.graphGeneration !==
          graph.graphRevision.graphGeneration
      ) {
        setExpanded(undefined);
        setNote("观测版本已变化，未混合新旧关系。请刷新起点后重新展开。");
        return;
      }
      const nodeMap = new Map(graph.nodes.map((n) => [n.canonicalId, n]));
      for (const n of patch.nodes) nodeMap.set(n.canonicalId, n);
      const edgeMap = new Map(graph.edges.map((e) => [edgeKey(e), e]));
      for (const e of patch.edges) edgeMap.set(edgeKey(e), e);
      const combined = {
        ...graph,
        nodes: [...nodeMap.values()],
        edges: [...edgeMap.values()],
      };
      const allowed = distances(combined, canonicalId, direction);
      const existingIds = new Set(graph.nodes.map((n) => n.canonicalId));
      const nodes = combined.nodes
        .filter(
          (n) =>
            existingIds.has(n.canonicalId) ||
            (allowed.get(n.canonicalId) ?? 3) <= 2,
        )
        .slice(0, maxNodes);
      const ids = new Set(nodes.map((n) => n.canonicalId));
      const allEdges = combined.edges.filter(
        (e) => ids.has(e.from) && ids.has(e.to),
      );
      const edges = allEdges.slice(0, 400);
      const cut =
        combined.nodes.length > nodes.length ||
        allEdges.length > edges.length ||
        patch.budgets.truncated;
      const freshness =
        graph.freshness === "unavailable" || patch.freshness === "unavailable"
          ? "unavailable"
          : graph.freshness === "stale" || patch.freshness === "stale"
            ? "stale"
            : "fresh";
      const collectedAt =
        Date.parse(graph.collectedAt) <= Date.parse(patch.collectedAt)
          ? graph.collectedAt
          : patch.collectedAt;
      const conflicts = [
        ...new Map(
          [...(graph.conflicts ?? []), ...(patch.conflicts ?? [])].map((v) => [
            JSON.stringify(v),
            v,
          ]),
        ).values(),
      ];
      const value: ResourceGraphV2 = {
        ...graph,
        summary: undefined,
        rankedEvidence: null,
        conflicts: conflicts.slice(0, 64),
        freshness,
        collectedAt,
        nodes,
        edges,
        partial: graph.partial || patch.partial || cut || conflicts.length > 64,
        warnings: [
          ...new Set([
            "此图为同一观测版本的多次有界查询合并；观测时间取最早值，不沿用单次查询汇总和证据排名。",
            ...graph.warnings,
            ...patch.warnings,
          ]),
        ].slice(0, 64),
        degradedSources: [
          ...new Set([...graph.degradedSources, ...patch.degradedSources]),
        ].slice(0, 64),
        budgets: {
          ...graph.budgets,
          maxDepth: 2,
          maxNodes,
          maxEdges: 400,
          nodeCount: nodes.length,
          edgeCount: edges.length,
          truncated: graph.budgets.truncated || cut,
        },
      };
      setExpanded({ scope: snapshotScope, base: snapshotBase, value });
      setNote(
        cut
          ? "已在深度 2 和节点预算内展开；部分结果被截断，请缩小关系范围或以另一资源为起点。"
          : "已展开该节点的一层关系，总路径深度不超过 2。",
      );
    } catch (e) {
      if (!abort.signal.aborted && version === epoch.current)
        setError(e instanceof Error ? e : new Error("关系查询失败"));
    } finally {
      if (version === epoch.current) setBusy(false);
    }
  };
  return (
    <>
      <Space wrap className="status-note">
        <label>
          初始查询深度{" "}
          <Select
            aria-label="初始查询深度"
            value={initialDepth}
            onChange={(v) => change("graphDepth", String(v))}
            options={[
              { value: 1, label: "1 层（逐步展开）" },
              { value: 2, label: "2 层" },
            ]}
          />
        </label>
        <label>
          节点预算{" "}
          <Select
            aria-label="图谱节点预算"
            value={maxNodes}
            onChange={(v) => change("graphLimit", String(v))}
            options={[25, 50, 100, 200].map((v) => ({
              value: v,
              label: v + " 节点",
            }))}
          />
        </label>
        <Select
          aria-label="关系方向"
          value={direction}
          onChange={(v) => change("graphDirection", v)}
          options={[
            { value: "both", label: "双向关系" },
            { value: "in", label: "入向关系" },
            { value: "out", label: "出向关系" },
          ]}
        />
        <Input
          aria-label="关系类型筛选"
          maxLength={128}
          placeholder="关系类型；留空表示全部"
          value={relationInput}
          onChange={(e) => setRelationInput(e.target.value)}
          onPressEnter={() => change("graphRelation", relationInput)}
          onBlur={() => {
            if (relationInput !== relation)
              change("graphRelation", relationInput);
          }}
        />
        <Button
          loading={query.isFetching}
          disabled={busy}
          onClick={() => {
            cancel();
            void query.refetch();
          }}
        >
          刷新起点
        </Button>
        <Button
          disabled={!expanded || busy}
          onClick={() => {
            setExpanded(undefined);
            setNote("");
          }}
        >
          收起全部扩展
        </Button>
      </Space>
      <p className="status-note">
        点击节点的“展开一层关系”按需查询。累计最多 200 节点、400 关系、深度
        2；到达限制后可缩小范围或打开该资源作为新起点。
      </p>
      {expanded?.scope === scope && expanded.base === base && (
        <p className="status-note">
          此图合并同一观测版本的多次有界查询，时效采用最保守状态，观测时间取最早值；不沿用单次查询的统计或证据排名。
        </p>
      )}
      {note && <Alert showIcon type="info" title={note} />}
      {error && <QueryState loading={false} error={error} />}
      <QueryState
        loading={query.isPending}
        error={query.error}
        retry={() => void query.refetch()}
      >
        <GraphView
          value={graph}
          scopeKey={"progressive:" + scope}
          onExpand={(id) => void expand(id)}
          canExpand={canExpand}
          expanding={busy}
        />
      </QueryState>
    </>
  );
}

import { useEffect, useRef, useState } from "react";
import cytoscape from "cytoscape";
import { Alert, Button, Select, Space, Table } from "antd";
import { Link } from "react-router-dom";
import type {
  DiagnosticGraphSnapshotV2,
  ResourceEntityV2,
  ResourceRelationV2,
} from "../../api/generated/models";
import { record, list, text } from "../../components/data";
import { Details, FullText } from "../../components/Details";
import { EnvelopeMeta } from "../../components/EnvelopeMeta";
import { Status } from "../../components/Status";
import { GraphLegend, relationClass } from "./legend";
import { edgeExpired, graphWithinBudget, graphLayout } from "./layout";
export function GraphView({
  value,
  scopeKey,
  onExpand,
  canExpand,
  expanding = false,
  expandLabel = "展开一层关系",
}: {
  value: unknown;
  scopeKey: string;
  onExpand?: (id: string) => void;
  canExpand?: (id: string) => boolean;
  expanding?: boolean;
  expandLabel?: string;
}) {
  const graph = value as DiagnosticGraphSnapshotV2 | undefined;
  const container = useRef<HTMLDivElement>(null);
  const instance = useRef<cytoscape.Core | null>(null);
  const [selected, setSelected] = useState<unknown>();
  const [pathStart, setPathStart] = useState<string>();
  const [pathEnd, setPathEnd] = useState<string>();
  const [pathNote, setPathNote] = useState("");
  const [mode, setMode] = useState("graph");
  const [now, setNow] = useState(Date.now());
  const previous = useRef<
    { scopeKey: string; epoch: number; generation: number } | undefined
  >(undefined);
  const nodes = graph?.nodes || [];
  const edges = graph?.edges || [];
  const budget = record(graph?.budgets);
  const depth = typeof budget.maxDepth === "number" ? budget.maxDepth : 2;
  const allowed = graphWithinBudget(nodes.length, depth);
  const rev = record(graph?.graphRevision);
  const epoch = Number(rev.ownerEpoch);
  const generation = Number(rev.graphGeneration);
  const stale =
    previous.current &&
    previous.current.scopeKey === scopeKey &&
    (epoch < previous.current.epoch ||
      (epoch === previous.current.epoch &&
        generation < previous.current.generation));
  useEffect(() => {
    if (!stale && Number.isFinite(epoch) && Number.isFinite(generation))
      previous.current = { scopeKey, epoch, generation };
  }, [scopeKey, epoch, generation, stale]);
  useEffect(() => {
    const ids = new Set(nodes.map((n) => n.canonicalId));
    if (pathStart && !ids.has(pathStart)) {
      setPathStart(undefined);
      setPathNote("路径起点已删除，请重新选择。");
    }
    if (pathEnd && !ids.has(pathEnd)) {
      setPathEnd(undefined);
      setPathNote("路径终点已删除，请重新选择。");
    }
    const chosen = record(selected);
    if (typeof chosen.canonicalId === "string" && !ids.has(chosen.canonicalId))
      setSelected(undefined);
  }, [value]);
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 15000);
    return () => clearInterval(id);
  }, []);
  useEffect(() => {
    if (!container.current || mode !== "graph" || !allowed || stale || !graph)
      return;
    const ids = new Set(nodes.map((n) => n.canonicalId));
    const cy = cytoscape({
      container: container.current,
      elements: [
        ...nodes.map((n) => ({
          data: { id: n.canonicalId, label: n.name, kind: n.kind },
        })),
        ...edges
          .filter((e) => ids.has(e.from) && ids.has(e.to))
          .map((e, i) => ({
            data: {
              id: "edge-" + i,
              source: e.from,
              target: e.to,
              label: e.kind,
              expired: edgeExpired(e, now) ? 1 : 0,
              relationClass: relationClass(e),
            },
          })),
      ],
      layout: graphLayout(nodes.length),
      style: [
        {
          selector: "node",
          style: {
            "background-color": "#3d7d7a",
            label: "data(label)",
            color: "#243445",
            "font-size": 11,
            "text-wrap": "ellipsis",
            "text-max-width": "140px",
            "text-valign": "bottom",
            "text-margin-y": 8,
            "border-width": 2,
            "border-color": "#d5e8e3",
            width: 34,
            height: 34,
          },
        },
        {
          selector: "edge",
          style: {
            width: 1.5,
            "line-color": "#9daeba",
            "target-arrow-color": "#9daeba",
            "target-arrow-shape": "triangle",
            "curve-style": "bezier",
            label: "data(label)",
            "font-size": 9,
            "text-background-color": "#fff",
            "text-background-opacity": 0.8,
          },
        },
        {
          selector: 'edge[relationClass = "dynamic"]',
          style: { "line-style": "dashed", width: 2 },
        },
        {
          selector: 'edge[relationClass = "inferred"]',
          style: { "line-style": "dotted", width: 2 },
        },
        { selector: 'edge[relationClass = "explicit"]', style: { width: 2.5 } },
        {
          selector: "edge.critical-path",
          style: {
            "line-color": "#a56518",
            "target-arrow-color": "#a56518",
            width: 4,
          },
        },
        {
          selector: "node.critical-path",
          style: {
            "background-color": "#a56518",
            "border-color": "#a56518",
            "border-width": 4,
          },
        },
        {
          selector: "edge[expired = 1]",
          style: { "line-style": "dashed", opacity: 0.45 },
        },
        {
          selector: ":selected",
          style: {
            "border-width": 4,
            "border-color": "#a56518",
            "line-color": "#a56518",
            "target-arrow-color": "#a56518",
          },
        },
      ],
      minZoom: 0.15,
      maxZoom: 2.5,
    });
    instance.current = cy;
    cy.on("tap", "node", (e) =>
      setSelected(nodes.find((n) => n.canonicalId === e.target.id())),
    );
    cy.on("tap", "edge", (e) => {
      const index = Number(e.target.id().slice(5));
      setSelected(edges.filter((e) => ids.has(e.from) && ids.has(e.to))[index]);
    });
    if (
      pathStart &&
      pathEnd &&
      cy.getElementById(pathStart).length &&
      cy.getElementById(pathEnd).length
    ) {
      const result = cy
        .elements()
        .dijkstra({ root: cy.getElementById(pathStart), directed: false });
      const end = cy.getElementById(pathEnd);
      if (end.length && Number.isFinite(result.distanceTo(end)))
        result.pathTo(end).addClass("critical-path");
    }
    const observer = new ResizeObserver(() => {
      cy.resize();
      cy.fit(undefined, 30);
      if (cy.zoom() > 1.2)
        cy.zoom({
          level: 1.2,
          renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 },
        });
    });
    observer.observe(container.current);
    return () => {
      observer.disconnect();
      cy.destroy();
      instance.current = null;
    };
  }, [value, mode, allowed, stale, pathStart, pathEnd]);
  useEffect(() => {
    const cy = instance.current;
    if (!cy) return;
    const ids = new Set(nodes.map((n) => n.canonicalId));
    const visible = edges.filter((e) => ids.has(e.from) && ids.has(e.to));
    cy.edges().forEach((edge) => {
      const index = Number(edge.id().slice(5));
      const item = visible[index];
      if (item) edge.data("expired", edgeExpired(item, now) ? 1 : 0);
    });
  }, [now, value]);
  function highlight() {
    const cy = instance.current;
    if (!cy || !pathStart || !pathEnd || stale) return;
    const start = cy.getElementById(pathStart);
    const finish = cy.getElementById(pathEnd);
    if (!start.length || !finish.length) {
      setPathNote("所选节点已不存在，请重新选择。");
      return;
    }
    cy.elements().removeClass("critical-path");
    const result = cy
      .elements()
      .dijkstra({ root: cy.getElementById(pathStart), directed: false });
    const end = cy.getElementById(pathEnd);
    if (!Number.isFinite(result.distanceTo(end))) {
      setPathNote("两节点之间没有可达关系；孤立节点或断开图不会虚构路径。");
      return;
    }
    result.pathTo(end).addClass("critical-path");
    setPathNote("已高亮选定节点间的最短关系路径；请结合证据核对诊断意义。");
  }
  if (!graph) return <Alert showIcon type="info" title="没有可用图谱数据" />;
  if (stale)
    return (
      <Alert
        showIcon
        type="warning"
        title="收到过期图谱版本，请重新查询"
        description="此结果早于当前查询已收到的版本，暂不展示节点和关系，避免依据过期数据操作。"
      />
    );
  return (
    <>
      <EnvelopeMeta value={graph} />
      {stale && (
        <Alert
          showIcon
          type="warning"
          title="拒绝展示旧 generation 图谱，请刷新观测"
        />
      )}
      {!allowed && (
        <Alert
          showIcon
          type="warning"
          title="超过 200 节点或深度 2 的展示预算"
          description="请缩小查询范围，或使用当前受控列表；前端不会自动全量扩展。"
        />
      )}
      <Space wrap className="status-note">
        <span>
          {nodes.length} 节点 · {edges.length} 关系 · 深度 ≤ 2
        </span>
        <Select
          aria-label="图谱展示方式"
          value={mode}
          onChange={setMode}
          options={[
            { value: "graph", label: "关系图" },
            { value: "list", label: "列表（键盘可达）" },
          ]}
        />
        <Button
          disabled={mode !== "graph"}
          onClick={() => instance.current?.fit(undefined, 30)}
        >
          适配窗口
        </Button>
        <span>虚线：TTL / 有效期已过期</span>
      </Space>
      {mode === "graph" && allowed && !stale && (
        <div
          ref={container}
          className="graph-canvas"
          role="img"
          aria-label="资源关系图。下方节点列表和关系列表可使用键盘访问。"
        />
      )}
      <GraphLegend />
      <Space wrap className="status-note">
        <Select
          aria-label="路径起点"
          placeholder="选择路径起点"
          style={{ width: 220 }}
          options={nodes
            .slice(0, 200)
            .map((n) => ({ label: n.name, value: n.canonicalId }))}
          value={pathStart}
          onChange={setPathStart}
        />
        <Select
          aria-label="路径终点"
          placeholder="选择路径终点"
          style={{ width: 220 }}
          options={nodes
            .slice(0, 200)
            .map((n) => ({ label: n.name, value: n.canonicalId }))}
          value={pathEnd}
          onChange={setPathEnd}
        />
        <Button
          disabled={
            !pathStart || !pathEnd || mode !== "graph" || !allowed || !!stale
          }
          onClick={highlight}
        >
          高亮关系路径
        </Button>
        <span role="status">{pathNote}</span>
      </Space>
      <h3>节点</h3>
      <Select
        aria-label="键盘选择图节点"
        showSearch
        optionFilterProp="label"
        style={{ width: "100%", marginBottom: 12 }}
        options={nodes.slice(0, 200).map((n) => ({
          label: n.name + " · " + n.kind,
          value: n.canonicalId,
        }))}
        onChange={(id) => {
          setSelected(nodes.find((n) => n.canonicalId === id));
          instance.current?.nodes().unselect();
          instance.current?.getElementById(id).select();
        }}
      />
      <Table<ResourceEntityV2>
        rowKey="canonicalId"
        pagination={{ pageSize: 20, showSizeChanger: false }}
        dataSource={nodes.slice(0, 200)}
        scroll={{ x: 600 }}
        columns={[
          {
            title: "节点",
            render: (_, n) => (
              <Link
                to={
                  "/resources/detail?canonicalId=" +
                  encodeURIComponent(n.canonicalId)
                }
              >
                {n.name}
              </Link>
            ),
          },
          { title: "类型", dataIndex: "kind" },
          ...(onExpand
            ? [
                {
                  title: "查询关系",
                  render: (_: unknown, n: ResourceEntityV2) => (
                    <Button
                      loading={expanding}
                      disabled={
                        expanding ||
                        (canExpand ? !canExpand(n.canonicalId) : false)
                      }
                      onClick={() => onExpand(n.canonicalId)}
                    >
                      {expandLabel}
                    </Button>
                  ),
                },
              ]
            : []),
          {
            title: "完整标识",
            dataIndex: "canonicalId",
            render: (v) => (
              <details>
                <summary>查看标识</summary>
                <FullText value={v} />
              </details>
            ),
          },
        ]}
      />
      <h3>关系与来源</h3>
      <Table<ResourceRelationV2>
        rowKey={(e, i) => e.from + e.to + e.kind + i}
        dataSource={edges.slice(0, 400)}
        pagination={{ pageSize: 20, showSizeChanger: false }}
        scroll={{ x: 750 }}
        expandable={{ expandedRowRender: (e) => <Details value={e} /> }}
        columns={[
          {
            title: "关系",
            render: (_, e) => (
              <>
                {e.kind}
                <br />
                <span className="muted">
                  {
                    {
                      dynamic: "动态关系",
                      inferred: "推断关系",
                      explicit: "显式关系",
                      unknown: "未分类关系",
                    }[relationClass(e)]
                  }
                </span>
              </>
            ),
          },
          {
            title: "起点",
            render: (_, e) => (
              <span title={e.from}>
                {nodes.find((n) => n.canonicalId === e.from)?.name ??
                  "引用节点未在当前图中"}
              </span>
            ),
          },
          {
            title: "终点",
            render: (_, e) => (
              <span title={e.to}>
                {nodes.find((n) => n.canonicalId === e.to)?.name ??
                  "引用节点未在当前图中"}
              </span>
            ),
          },
          {
            title: "来源",
            render: (_, e) => (
              <details>
                <summary>来源登记与规则</summary>
                <FullText value={e.provenance.sourceRegistrationId} />
                <FullText value={e.provenance.ruleVersion} />
              </details>
            ),
          },
          { title: "置信度", render: (_, e) => e.confidence.toFixed(3) },
          {
            title: "观测时效",
            render: (_, e) => (
              <Status value={edgeExpired(e, now) ? "stale" : graph.freshness} />
            ),
          },
          {
            title: "动态有效期",
            render: (_, e) => (e.ttl === 0 ? "非动态关系" : e.ttl + " 秒"),
          },
        ]}
      />
      {selected && (
        <section className="section">
          <h3>已选节点 / 关系</h3>
          <Details value={selected} />
          {onExpand && typeof record(selected).canonicalId === "string" && (
            <Button
              disabled={
                expanding ||
                (canExpand
                  ? !canExpand(String(record(selected).canonicalId))
                  : false)
              }
              onClick={() => onExpand(String(record(selected).canonicalId))}
            >
              {expandLabel}
            </Button>
          )}
        </section>
      )}
    </>
  );
}

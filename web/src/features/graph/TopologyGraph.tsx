import { useEffect, useRef } from "react";
import cytoscape from "cytoscape";
import type {
  ResourceEntityV2,
  ResourceGraphV2,
} from "../../api/generated/models";
import { edgeExpired, graphLayout } from "./layout";

export function TopologyGraph({
  graph,
  rootId,
  select,
}: {
  graph: ResourceGraphV2;
  rootId: string;
  select: (node: ResourceEntityV2) => void;
}) {
  const container = useRef<HTMLDivElement>(null);
  const instance = useRef<cytoscape.Core | null>(null);
  useEffect(() => {
    if (!container.current) return;
    const cy = cytoscape({
      container: container.current,
      elements: [
        ...graph.nodes.map((node) => ({
          data: {
            id: node.canonicalId,
            label: node.name + "\n" + node.kind,
            root: node.canonicalId === rootId ? 1 : 0,
          },
        })),
        ...graph.edges.map((edge, index) => ({
          data: {
            id: "edge-" + index,
            source: edge.from,
            target: edge.to,
            label: edge.kind,
            expired: edgeExpired(edge) ? 1 : 0,
          },
        })),
      ],
      layout: graphLayout(graph.nodes.length),
      style: [
        {
          selector: "node",
          style: {
            shape: "round-rectangle",
            width: 176,
            height: 58,
            "background-color": "#fff",
            "border-color": "#d9dced",
            "border-width": 1.2,
            label: "data(label)",
            color: "#20263a",
            "font-size": 12,
            "text-valign": "center",
            "text-wrap": "ellipsis",
            "text-max-width": "160px",
          },
        },
        {
          selector: "node[root = 1]",
          style: { "border-color": "#5451d6", "border-width": 2 },
        },
        {
          selector: "edge",
          style: {
            width: 1.5,
            "line-color": "#d6d7e8",
            "target-arrow-color": "#d6d7e8",
            "target-arrow-shape": "triangle",
            "curve-style": "bezier",
            label: "data(label)",
            "font-size": 10,
            "text-background-color": "#fafbff",
            "text-background-opacity": 1,
          },
        },
        {
          selector: "edge[expired = 1]",
          style: { "line-style": "dashed", opacity: 0.45 },
        },
      ],
      minZoom: 0.15,
      maxZoom: 2.5,
    });
    instance.current = cy;
    cy.on("tap", "node", (event) => {
      const node = graph.nodes.find(
        (node) => node.canonicalId === event.target.id(),
      );
      if (node) select(node);
    });
    const observer = new ResizeObserver(() => {
      cy.resize();
      cy.fit(undefined, 30);
    });
    observer.observe(container.current);
    return () => {
      observer.disconnect();
      cy.destroy();
      instance.current = null;
    };
  }, [graph, rootId, select]);
  return (
    <>
      <div className="v12-topology-graph">
        <div className="v12-topology-zoom">
          <button
            aria-label="缩小关系图"
            onClick={() => {
              const cy = instance.current;
              if (cy) cy.zoom(cy.zoom() / 1.2);
            }}
          >
            −
          </button>
          <button onClick={() => instance.current?.fit(undefined, 30)}>
            重置
          </button>
          <button
            aria-label="放大关系图"
            onClick={() => {
              const cy = instance.current;
              if (cy) cy.zoom(cy.zoom() * 1.2);
            }}
          >
            +
          </button>
        </div>
        <div
          ref={container}
          className="v12-topology-canvas"
          role="img"
          aria-label="当前集群资源关系图，节点可点击；下方可用键盘选择资源。"
        />
      </div>
      <label className="v12-topology-keyboard">
        查看图中资源{" "}
        <select
          aria-label="查看图中资源"
          value=""
          onChange={(event) => {
            const node = graph.nodes.find(
              (node) => node.canonicalId === event.target.value,
            );
            if (node) select(node);
          }}
        >
          <option value="">选择资源查看详情</option>
          {graph.nodes.map((node) => (
            <option key={node.canonicalId} value={node.canonicalId}>
              {node.kind} / {node.name} · {node.namespace || "集群资源"}
            </option>
          ))}
        </select>
      </label>
    </>
  );
}

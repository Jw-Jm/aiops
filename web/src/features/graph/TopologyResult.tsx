import { Alert } from "antd";
import type {
  ResourceEntityV2,
  ResourceGraphV2,
} from "../../api/generated/models";
import { EnvelopeMeta } from "../../components/EnvelopeMeta";
import { Details } from "../../components/Details";
import { Status } from "../../components/Status";
import { DataTable } from "../../components/v12/primitives";
import { TopologyGraph } from "./TopologyGraph";
import { edgeExpired } from "./layout";
export function TopologyResult({
  graph,
  rootId,
  listView,
  select,
}: {
  graph: ResourceGraphV2;
  rootId: string;
  listView: boolean;
  select: (node: ResourceEntityV2) => void;
}) {
  const name = (id: string) =>
    graph.nodes.find((node) => node.canonicalId === id)?.name || id;
  return (
    <div className="v12-topology-result">
      <EnvelopeMeta value={graph} />
      <p className="muted">
        {graph.nodes.length} 个节点 · {graph.edges.length} 条关系 · 图版本{" "}
        {graph.graphRevision.ownerEpoch}/{graph.graphRevision.graphGeneration}
        {graph.budgets.truncated ? " · 已达查询预算，结果截断" : ""}
      </p>
      {graph.edges.length === 0 && (
        <Alert type="info" title="当前仅有资源投影，没有可用的关系数据。" />
      )}
      {listView ? (
        <>
          <DataTable<ResourceEntityV2>
            rowKey="canonicalId"
            dataSource={graph.nodes}
            pagination={{ pageSize: 20, showSizeChanger: false }}
            columns={[
              {
                title: "资源",
                render: (_, node) => (
                  <button className="link" onClick={() => select(node)}>
                    {node.name}
                  </button>
                ),
              },
              { title: "类型", dataIndex: "kind" },
              {
                title: "命名空间",
                render: (_, node) => node.namespace || "集群资源",
              },
              {
                title: "关系说明",
                render: (_, node) =>
                  node.canonicalId === rootId ? "根资源" : "授权邻居",
              },
              {
                title: "状态",
                render: (_, node) => (
                  <Status value={node.attributes.health ?? "unknown"} />
                ),
              },
            ]}
          />
          <DataTable<ResourceGraphV2["edges"][number]>
            rowKey={(_, index) => String(index)}
            dataSource={graph.edges}
            pagination={{ pageSize: 20, showSizeChanger: false }}
            expandable={{
              expandedRowRender: (edge) => <Details value={edge} />,
            }}
            columns={[
              { title: "起点", render: (_, edge) => name(edge.from) },
              { title: "关系", dataIndex: "kind" },
              { title: "终点", render: (_, edge) => name(edge.to) },
              {
                title: "观测时效",
                render: (_, edge) => (
                  <Status
                    value={edgeExpired(edge) ? "stale" : graph.freshness}
                  />
                ),
              },
              {
                title: "来源 / 规则",
                render: (_, edge) => (
                  <span>
                    {edge.provenance.sourceRegistrationId} /{" "}
                    {edge.provenance.ruleVersion}
                  </span>
                ),
              },
            ]}
          />
        </>
      ) : (
        <TopologyGraph graph={graph} rootId={rootId} select={select} />
      )}
    </div>
  );
}

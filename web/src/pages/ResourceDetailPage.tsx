import { useQuery } from "@tanstack/react-query";
import { Button, Tabs } from "antd";
import { Link, useSearchParams } from "react-router-dom";
import {
  getResourceByCanonicalID,
  getResourceNeighbors,
} from "../api/generated/platform";
import { checked, requestOptions } from "../api/client";
import { PageHeading } from "../components/PageHeading";
import { Details } from "../components/Details";
import { QueryState } from "../components/QueryState";
import { GraphView } from "../features/graph/GraphView";
export function ResourceDetailPage() {
  const [url, setUrl] = useSearchParams();
  const id = url.get("canonicalId") || "";
  const tab = url.get("tab") || "detail";
  const resource = useQuery({
    queryKey: ["resource", id],
    enabled: !!id,
    queryFn: ({ signal }) =>
      checked(
        getResourceByCanonicalID({ canonicalId: id }, requestOptions(signal)),
      ),
  });
  const graph = useQuery({
    queryKey: ["resource-graph", id],
    enabled: !!id && tab === "graph",
    queryFn: ({ signal }) =>
      checked(
        getResourceNeighbors(
          { canonicalId: id, depth: 2, maxNodes: 200, maxEdges: 400 },
          requestOptions(signal),
        ),
      ),
  });
  return (
    <>
      <PageHeading
        title="资源详情"
        description="完整标识、来源和关系均取自当前授权查询。"
        extra={
          <Link to="/resources">
            <Button>返回资源目录</Button>
          </Link>
        }
      />
      <section className="section">
        <Tabs
          activeKey={tab}
          onChange={(v) => {
            const n = new URLSearchParams(url);
            n.set("tab", v);
            setUrl(n);
          }}
          items={[
            {
              key: "detail",
              label: "资源信息",
              children: (
                <QueryState
                  loading={resource.isPending && !!id}
                  error={resource.error}
                  empty={!id}
                  retry={() => void resource.refetch()}
                >
                  <Details
                    value={
                      resource.data && "data" in resource.data
                        ? resource.data.data.nodes[0]
                        : undefined
                    }
                  />
                </QueryState>
              ),
            },
            {
              key: "graph",
              label: "资源关系图",
              children: (
                <QueryState
                  loading={graph.isPending && !!id}
                  error={graph.error}
                  retry={() => void graph.refetch()}
                >
                  <GraphView
                    scopeKey={"resource:" + id}
                    value={
                      graph.data && "data" in graph.data
                        ? graph.data.data
                        : undefined
                    }
                  />
                </QueryState>
              ),
            },
          ]}
        />
      </section>
    </>
  );
}

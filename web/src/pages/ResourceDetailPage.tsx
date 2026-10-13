import { useQuery } from "@tanstack/react-query";
import { Button, Tabs, Space } from "antd";
import { Link, useSearchParams } from "react-router-dom";
import { getResourceByCanonicalID } from "../api/generated/platform";
import { checked, requestOptions } from "../api/client";
import { PageHeading } from "../components/PageHeading";
import { Details } from "../components/Details";
import { QueryState } from "../components/QueryState";
import { ResourceGraphExplorer } from "../features/graph/ResourceGraphExplorer";
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
  return (
    <>
      <PageHeading
        title="资源详情"
        description="完整标识、来源和关系均取自当前授权查询。"
        extra={
          <Space>
            <Link
              to={`/assistant?canonicalId=${encodeURIComponent(id)}&clusterUid=${encodeURIComponent(id.split("/")[3] || "")}`}
            >
              <Button>自然语言排查</Button>
            </Link>
            <Link to="/resources">
              <Button>返回资源目录</Button>
            </Link>
          </Space>
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
              children: <ResourceGraphExplorer key={id} canonicalId={id} />,
            },
          ]}
        />
      </section>
    </>
  );
}

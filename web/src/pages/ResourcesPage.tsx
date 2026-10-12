import { useQuery } from "@tanstack/react-query";
import { Input, Select, Table, Button, Space } from "antd";
import { Link, useSearchParams } from "react-router-dom";
import { getResources } from "../api/generated/platform";
import type {
  GetResourcesParams,
  ResourcePageV2,
  ResourceEntityV2,
} from "../api/generated/models";
import { checked, requestOptions } from "../api/client";
import { PageHeading } from "../components/PageHeading";
import { ScopeFilter, useClusters } from "../components/ScopeFilter";
import { QueryState } from "../components/QueryState";
import { EnvelopeMeta } from "../components/EnvelopeMeta";
import { FullText, Details } from "../components/Details";
import { Status } from "../components/Status";
import { record, text } from "../components/data";
export function ResourcesPage() {
  const [url, setUrl] = useSearchParams();
  const clusters = useClusters();
  const params: GetResourcesParams = {
    limit: 50,
    clusterUid: url.get("clusterUid") || clusters[0],
    order: url.get("order") === "desc" ? "desc" : "asc",
    ...Object.fromEntries(
      [
        "kind",
        "clusterUid",
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
    enabled: !!params.clusterUid,
    queryFn: async ({ signal }) =>
      checked(getResources(params, requestOptions(signal))),
  });
  const page = query.data as ResourcePageV2 | undefined;
  return (
    <>
      <PageHeading
        title="资源目录"
        description="通过名称、命名空间或健康状态查找资源，点击名称查看详情。"
      />
      <section className="section">
        <div className="filters">
          <ScopeFilter
            value={params.clusterUid}
            onChange={(v) => update("clusterUid", v)}
          />
          {["name", "kind", "namespace", "label"].map((k) => (
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
        </div>
        <QueryState
          loading={query.isPending && !!params.clusterUid}
          error={query.error}
          retry={() => void query.refetch()}
          noScope={!params.clusterUid}
          empty={page?.data.length === 0}
        >
          <EnvelopeMeta value={page?.meta} />
          <Table<ResourceEntityV2>
            rowKey="canonicalId"
            dataSource={page?.data}
            pagination={false}
            expandable={{ expandedRowRender: (row) => <Details value={row} /> }}
            scroll={{ x: 940 }}
            columns={[
              {
                title: "资源",
                dataIndex: "name",
                width: 230,
                render: (v, row) => (
                  <>
                    <Link
                      to={
                        "/resources/detail?canonicalId=" +
                        encodeURIComponent(row.canonicalId)
                      }
                    >
                      {v}
                    </Link>
                    <div className="muted">
                      {row.kind} · {text(row.namespace)}
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
                title: "来源",
                width: 200,
                render: (_, row) => (
                  <FullText value={row.sourceRefs.join(", ")} />
                ),
              },
            ]}
          />
          <div className="pagination">
            <span className="muted">
              本页 {page?.data.length ?? 0} 条 · 每页最多 50 条
            </span>
            <Space>
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
    </>
  );
}

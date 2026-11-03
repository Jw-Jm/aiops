import { beforeEach, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ResourceDetailPage } from "../src/pages/ResourceDetailPage";
import { ApiError, setCredentials } from "../src/api/client";
import { resource as fixture } from "./fixtures";
const api = vi.hoisted(() => ({
  roots: vi.fn(),
  entity: vi.fn(),
  neighbors: vi.fn(),
  findings: vi.fn(),
  incidents: vi.fn(),
}));
const scope = vi.hoisted(() => ({
  clusterUid: "cluster-a",
  scopeKey: "a:0",
  invalid: false,
  candidates: [{ clusterUid: "cluster-a", displayName: "集群 A" }],
}));
vi.mock("../src/app/ClusterContext", () => ({
  useOperationalCluster: () => scope,
}));
vi.mock("../src/api/generated/platform", () => ({
  getResources: api.roots,
  getResourceByCanonicalID: api.entity,
  getResourceNeighbors: api.neighbors,
  getFindings: api.findings,
  getIncidents: api.incidents,
}));
vi.mock("../src/features/graph/TopologyGraph", () => ({
  TopologyGraph: ({ graph, select }: any) => (
    <div data-testid="graph">
      {graph.nodes.map((node: any) => (
        <button key={node.canonicalId} onClick={() => select(node)}>
          {node.name}
        </button>
      ))}
    </div>
  ),
}));
vi.mock("../src/features/graph/ResourceGraphExplorer", () => ({
  ResourceGraphExplorer: () => <div>legacy progressive graph</div>,
}));
const node = {
  ...fixture,
  canonicalId: "k8s+v1://tenant/cluster-a/core/Pod/uid-a",
  scope: "cluster-a",
  stableId: "uid-a",
  kind: "Pod",
  name: "real-pod",
  namespace: "apps",
  labels: { app: "actual" },
  attributes: { health: "normal", resourceDomain: "kubernetes" },
  sourceRefs: ["source-a"],
};
const neighbor = {
  ...node,
  canonicalId: "k8s+v1://tenant/cluster-a/core/Service/uid-b",
  stableId: "uid-b",
  kind: "Service",
  name: "real-service",
};
const graph = {
  schemaVersion: "resource-graph/v2",
  nodes: [neighbor, node],
  edges: [],
  graphRevision: { ownerEpoch: 2, graphGeneration: 71 },
  observationSources: [
    {
      sourceRegistrationId: "source-a",
      revision: 7,
      scopeDigest: "sha256:" + "a".repeat(64),
    },
  ],
  freshness: "fresh",
  collectedAt: "2026-10-29T03:00:00Z",
  partial: true,
  warnings: ["scope_limited"],
  degradedSources: [],
  budgets: { truncated: false },
};
const response = (data: unknown) => ({
  status: 200,
  headers: new Headers(),
  data,
});
const path = (tab = "summary", id = node.canonicalId) =>
  "/resources/detail?" +
  new URLSearchParams({ clusterUid: "cluster-a", canonicalId: id, tab });
function Location() {
  return <output data-testid="url">{useLocation().search}</output>;
}
function view(
  url = path(),
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
) {
  return (
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[url]}>
        <ResourceDetailPage />
        <Location />
      </MemoryRouter>
    </QueryClientProvider>
  );
}
beforeEach(() => {
  Object.values(api).forEach((mock) => mock.mockReset());
  setCredentials("test-token");
  scope.clusterUid = "cluster-a";
  scope.scopeKey = "a:0";
  scope.invalid = false;
  api.roots.mockResolvedValue(response({ data: [node], meta: {} }));
  api.entity.mockResolvedValue(response({ data: graph }));
  api.neighbors.mockResolvedValue(response({ data: graph }));
  api.findings.mockResolvedValue(response({ data: [], meta: {} }));
  api.incidents.mockResolvedValue(response({ data: [], meta: {} }));
});
it("resolves the exact resource in current cluster before entity read; selects the requested canonical rather than the first node", async () => {
  render(view());
  await screen.findByRole("heading", { name: node.name });
  expect(api.roots).toHaveBeenCalledWith(
    { clusterUid: "cluster-a", canonicalIds: [node.canonicalId], limit: 1 },
    expect.anything(),
  );
  expect(api.entity).toHaveBeenCalledWith(
    { canonicalId: node.canonicalId },
    expect.anything(),
  );
  expect(screen.queryByRole("heading", { name: neighbor.name })).toBeNull();
  expect(screen.getByRole("link", { name: "AI 排查" })).toHaveAttribute(
    "href",
    "/assistant?" +
      new URLSearchParams({
        clusterUid: "cluster-a",
        canonicalId: node.canonicalId,
      }),
  );
  expect(screen.getByText("登记版本")).toBeVisible();
  expect(screen.getByText("7", { exact: true })).toBeVisible();
  expect(
    screen.getByText(
      "来源登记版本用于追溯本次资源投影，不代表连续覆盖或业务正常。",
    ),
  ).toBeVisible();
  expect(screen.getByText("结果受当前授权范围限制")).toBeVisible();
  expect(api.findings).not.toHaveBeenCalled();
  expect(api.neighbors).not.toHaveBeenCalled();
});
it("does not use a foreign-cluster reference or fall back to another resource", async () => {
  api.roots.mockRejectedValue(
    new ApiError(403, "FORBIDDEN", "cross-cluster denied"),
  );
  render(view(path("summary", "foreign-resource")));
  await screen.findByText("没有此范围的访问权限");
  expect(api.entity).not.toHaveBeenCalled();
  expect(api.neighbors).not.toHaveBeenCalled();
  expect(screen.queryByRole("link", { name: "AI 排查" })).toBeNull();
});
it("shows missing roots without reading entity or showing cached actions", async () => {
  api.roots.mockResolvedValue(response({ data: [], meta: {} }));
  render(view());
  await screen.findByText("当前筛选范围没有正式数据");
  expect(api.entity).not.toHaveBeenCalled();
  expect(screen.queryByRole("link", { name: "AI 排查" })).toBeNull();
});
it("hides cached identity and links when a root refresh is denied", async () => {
  render(view());
  await screen.findByRole("heading", { name: node.name });
  api.roots.mockRejectedValue(new ApiError(403, "FORBIDDEN", "revoked"));
  fireEvent.click(screen.getByRole("button", { name: "刷新资源" }));
  await screen.findByText("没有此范围的访问权限");
  expect(screen.queryByRole("heading", { name: node.name })).toBeNull();
  expect(screen.queryByRole("link", { name: "AI 排查" })).toBeNull();
});
it("loads bounded relations only on the tab and opens a freshly read neighbor", async () => {
  render(view());
  await screen.findByRole("heading", { name: node.name });
  fireEvent.click(screen.getByRole("tab", { name: "关系与影响" }));
  await screen.findByTestId("graph");
  expect(api.neighbors).toHaveBeenCalledWith(
    { canonicalId: node.canonicalId, depth: 2, maxNodes: 200, maxEdges: 400 },
    expect.anything(),
  );
  fireEvent.click(screen.getByRole("button", { name: neighbor.name }));
  expect(
    await screen.findByRole("link", { name: "进入资源详情" }),
  ).toHaveAttribute(
    "href",
    "/resources/detail?" +
      new URLSearchParams({
        clusterUid: "cluster-a",
        canonicalId: neighbor.canonicalId,
      }),
  );
  expect(api.entity).toHaveBeenLastCalledWith(
    { canonicalId: neighbor.canonicalId },
    expect.anything(),
  );
});
it("binds the logs entry to exact cluster and resource and disables unavailable workspaces", async () => {
  render(view(path("observations")));
  await screen.findByRole("link", { name: "进入日志查询" });
  expect(screen.getByRole("link", { name: "进入日志查询" })).toHaveAttribute(
    "href",
    "/observe/logs?" +
      new URLSearchParams({
        clusterUid: "cluster-a",
        canonicalId: node.canonicalId,
      }),
  );
  expect(screen.getByRole("button", { name: "进入指标查询" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "进入网络统计" })).toBeDisabled();
});
it("queries direct findings/incidents separately with exact resource and independently paged cursors", async () => {
  api.findings.mockImplementation(async (params) =>
    response({
      data: params.cursor
        ? []
        : [
            {
              findingId: "f-one",
              resourceCanonicalId: node.canonicalId,
              normalizedSymptom: "fixture-only-symptom",
              lifecycleState: "resolved",
              severity: "warning",
              observedAt: "2026-10-29T03:00:00Z",
            },
          ],
      meta: { nextCursor: params.cursor ? null : "opaque-findings" },
    }),
  );
  api.incidents.mockResolvedValue(
    response({
      data: [
        {
          incidentId: "i-one",
          resourceCanonicalId: node.canonicalId,
          state: "open",
          severity: "warning",
          updatedAt: "2026-10-29T03:00:00Z",
        },
      ],
      meta: {},
    }),
  );
  render(view(path("events")));
  await screen.findByText("fixture-only-symptom");
  const incidents = screen.getByRole("region", { name: "关联故障" });
  expect(
    within(incidents).getByRole("link", { name: "i-one" }),
  ).toHaveAttribute("href", "/events/incidents/i-one?clusterUid=cluster-a");
  for (const mock of [api.findings, api.incidents])
    expect(mock).toHaveBeenCalledWith(
      {
        clusterUid: "cluster-a",
        resourceCanonicalId: node.canonicalId,
        limit: 20,
        cursor: undefined,
      },
      expect.anything(),
    );
  fireEvent.click(
    within(screen.getByRole("region", { name: "关联告警" })).getByRole(
      "button",
      { name: "下一页" },
    ),
  );
  await waitFor(() =>
    expect(api.findings).toHaveBeenLastCalledWith(
      expect.objectContaining({
        resourceCanonicalId: node.canonicalId,
        cursor: "opaque-findings",
      }),
      expect.anything(),
    ),
  );
  expect(screen.getByTestId("url")).toHaveTextContent(
    "findingCursor=opaque-findings",
  );
  expect(api.incidents).toHaveBeenCalledTimes(1);
});
it("keeps a failed event query distinct from an empty list", async () => {
  api.findings.mockRejectedValue(
    new ApiError(503, "SOURCE_DEGRADED", "unavailable"),
  );
  render(view(path("events")));
  await screen.findByText("数据来源暂不可用");
  expect(
    within(screen.getByRole("region", { name: "关联告警" })).queryByText(
      "当前筛选范围没有正式数据",
    ),
  ).toBeNull();
});
it("discards an entity response after the cluster changes", async () => {
  let complete!: (value: unknown) => void;
  api.entity.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        complete = resolve;
      }),
  );
  const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    }),
    rendered = render(view(path(), client));
  await waitFor(() => expect(api.entity).toHaveBeenCalled());
  scope.clusterUid = "cluster-b";
  scope.scopeKey = "b:1";
  api.roots.mockResolvedValue(response({ data: [], meta: {} }));
  rendered.rerender(view(path(), client));
  await act(async () => complete(response({ data: graph })));
  await screen.findByText("当前筛选范围没有正式数据");
  expect(screen.queryByRole("heading", { name: node.name })).toBeNull();
});
it("retains the legacy detail/graph URL mappings and refuses requests without a valid cluster", async () => {
  const rendered = render(view(path("graph")));
  await screen.findByText("legacy progressive graph");
  expect(screen.getByRole("tab", { name: "关系与影响" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  scope.invalid = true;
  rendered.rerender(view(path("graph")));
  await screen.findByText("没有可用的集群范围");
});

it("does not present disabled SP05 as zero associated events", async () => {
  api.findings.mockRejectedValue(
    new ApiError(409, "CAPABILITY_DISABLED", "SP05 runtime is disabled"),
  );
  api.incidents.mockRejectedValue(
    new ApiError(409, "CAPABILITY_DISABLED", "SP05 runtime is disabled"),
  );
  render(view(path("events")));
  await waitFor(() =>
    expect(screen.getAllByText("此功能未启用")).toHaveLength(2),
  );
  for (const title of ["关联告警", "关联故障"]) {
    const region = screen.getByRole("region", { name: title });
    expect(within(region).getByText("尚无可显示的读取结果")).toBeVisible();
    expect(within(region).queryByText(/本页 0 条/)).toBeNull();
  }
});

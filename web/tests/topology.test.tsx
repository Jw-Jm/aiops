import { beforeEach, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TopologyPage } from "../src/pages/TopologyPage";
import { ApiError, setCredentials } from "../src/api/client";
import { resource } from "./fixtures";

const api = vi.hoisted(() => ({
  resources: vi.fn(),
  neighbors: vi.fn(),
  detail: vi.fn(),
}));
const scope = vi.hoisted(() => ({
  clusterUid: "cluster-a",
  invalid: false,
  scopeKey: "a:0",
}));
vi.mock("../src/app/ClusterContext", () => ({
  useOperationalCluster: () => scope,
}));
vi.mock("../src/api/generated/platform", () => ({
  getResources: api.resources,
  getResourceNeighbors: api.neighbors,
  getResourceByCanonicalID: api.detail,
}));
vi.mock("../src/features/graph/TopologyGraph", () => ({
  TopologyGraph: ({ graph, select }: any) => (
    <div data-testid="real-graph">
      {graph.nodes.map((node: any) => (
        <button key={node.canonicalId} onClick={() => select(node)}>
          {node.name}
        </button>
      ))}
    </div>
  ),
}));
const node = {
  ...resource,
  canonicalId: "kubernetes+v1://tenant/cluster-a/core/Pod/uid-a",
  name: "same-name",
  scope: "cluster-a",
  kind: "Pod",
  namespace: "ns-a",
  stableId: "uid-a",
};
const second = {
  ...node,
  canonicalId: "kubernetes+v1://tenant/cluster-a/core/Pod/uid-b",
  namespace: "ns-b",
  stableId: "uid-b",
};
const envelope = (data: unknown) => ({
  status: 200,
  headers: new Headers(),
  data,
});
const page = (nodes = [node], cursor?: string) =>
  envelope({ data: nodes, meta: { nextCursor: cursor } });
const graph = {
  nodes: [node, second],
  edges: [
    {
      from: node.canonicalId,
      to: second.canonicalId,
      kind: "observed-relation",
      provenance: { sourceRegistrationId: "source-a", ruleVersion: "rule-v1" },
      ttl: 0,
    },
  ],
  graphRevision: { ownerEpoch: 1, graphGeneration: 7 },
  budgets: { truncated: true },
  freshness: "fresh",
  partial: true,
  warnings: ["fixture partial"],
  degradedSources: [],
};
function view(path = "/resources/topology?clusterUid=cluster-a") {
  return (
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={[path]}>
        <TopologyPage />
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
  api.resources.mockImplementation(async (params) =>
    page(
      params.canonicalIds
        ? [node, second].filter((value) =>
            params.canonicalIds.includes(value.canonicalId),
          )
        : [node, second],
    ),
  );
  api.neighbors.mockResolvedValue(envelope({ data: graph }));
  api.detail.mockResolvedValue(envelope({ data: { nodes: [node] } }));
});
it("queries an authorized root then bounded neighbors; local view changes do not read sources again", async () => {
  render(view());
  await screen.findByTestId("real-graph");
  expect(api.resources).toHaveBeenCalledWith(
    expect.objectContaining({
      clusterUid: "cluster-a",
      canonicalIds: [node.canonicalId],
      limit: 1,
    }),
    expect.anything(),
  );
  expect(api.neighbors).toHaveBeenCalledWith(
    { canonicalId: node.canonicalId, depth: 2, maxNodes: 200, maxEdges: 400 },
    expect.anything(),
  );
  fireEvent.click(screen.getByRole("button", { name: "关系列表" }));
  expect(await screen.findByText("observed-relation")).toBeVisible();
  expect(screen.getByText("source-a / rule-v1")).toBeVisible();
  expect(screen.getByText(/结果截断/)).toBeVisible();
  expect(screen.getByText("此查询返回部分或来源降级结果")).toBeVisible();
  expect(api.neighbors).toHaveBeenCalledTimes(1);
  fireEvent.change(screen.getByLabelText("关系深度"), {
    target: { value: "1" },
  });
  await waitFor(() =>
    expect(api.neighbors).toHaveBeenLastCalledWith(
      expect.objectContaining({ depth: 1 }),
      expect.anything(),
    ),
  );
});
it("keeps same-name roots distinct by canonical UID and namespace, and rereads detail", async () => {
  render(view());
  await screen.findByTestId("real-graph");
  const options = screen
    .getAllByRole("option")
    .map((option) => option.textContent);
  expect(options).toContain("Pod / same-name · ns-a");
  expect(options).toContain("Pod / same-name · ns-b");
  fireEvent.change(screen.getByLabelText("根资源"), {
    target: { value: second.canonicalId },
  });
  await waitFor(() =>
    expect(api.neighbors).toHaveBeenLastCalledWith(
      expect.objectContaining({ canonicalId: second.canonicalId }),
      expect.anything(),
    ),
  );
  api.detail.mockResolvedValue(envelope({ data: { nodes: [second] } }));
  fireEvent.click(screen.getByRole("button", { name: "查看根资源" }));
  expect(
    await screen.findByRole("link", { name: "打开完整资源详情" }),
  ).toHaveAttribute(
    "href",
    "/resources/detail?" +
      new URLSearchParams({
        clusterUid: "cluster-a",
        canonicalId: second.canonicalId,
      }),
  );
  expect(api.detail).toHaveBeenCalledWith(
    { canonicalId: second.canonicalId },
    expect.anything(),
  );
});
it("rejects an explicit foreign-cluster root without neighbor fallback", async () => {
  api.resources.mockImplementation(async (params) => {
    if (params.canonicalIds)
      throw new ApiError(403, "FORBIDDEN", "foreign scope");
    return page();
  });
  render(
    view("/resources/topology?clusterUid=cluster-a&canonicalId=foreign-root"),
  );
  await screen.findByText("没有此范围的访问权限");
  expect(api.neighbors).not.toHaveBeenCalled();
  expect(screen.queryByTestId("real-graph")).toBeNull();
});
it("does not substitute the first candidate when an explicit root is missing", async () => {
  render(
    view("/resources/topology?clusterUid=cluster-a&canonicalId=deleted-root"),
  );
  await screen.findByText("当前筛选范围没有正式数据");
  expect(api.neighbors).not.toHaveBeenCalled();
});
it("loads additional authorized root pages only on request", async () => {
  api.resources.mockImplementation(async (params) =>
    params.canonicalIds
      ? page()
      : params.cursor
        ? page([second])
        : page([node], "opaque-cursor"),
  );
  render(view());
  await screen.findByTestId("real-graph");
  expect(screen.queryByRole("option", { name: /ns-b/ })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "更多根资源" }));
  await screen.findByRole("option", { name: /ns-b/ });
  expect(api.resources).toHaveBeenCalledWith(
    expect.objectContaining({
      clusterUid: "cluster-a",
      cursor: "opaque-cursor",
      limit: 50,
    }),
    expect.anything(),
  );
});
it("discards a late graph after a cluster switch", async () => {
  let complete!: (value: unknown) => void;
  api.neighbors.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        complete = resolve;
      }),
  );
  const rendered = render(view());
  await waitFor(() => expect(api.neighbors).toHaveBeenCalledTimes(1));
  scope.clusterUid = "cluster-b";
  scope.scopeKey = "b:1";
  api.resources.mockResolvedValue(page([]));
  rendered.rerender(view());
  await screen.findByText("当前筛选范围没有正式数据");
  await act(async () => complete(envelope({ data: graph })));
  expect(screen.queryByTestId("real-graph")).toBeNull();
  expect(screen.queryByText("fixture partial")).toBeNull();
});
it("hides the graph and drawer on a denied neighbor refresh", async () => {
  render(view());
  await screen.findByTestId("real-graph");
  api.neighbors.mockRejectedValue(
    new ApiError(403, "FORBIDDEN", "scope revoked"),
  );
  fireEvent.change(screen.getByLabelText("关系深度"), {
    target: { value: "1" },
  });
  await screen.findByText("没有此范围的访问权限");
  expect(screen.queryByTestId("real-graph")).toBeNull();
  expect(screen.queryByRole("dialog")).toBeNull();
});
it("makes no resource request without an authorized cluster", async () => {
  scope.invalid = true;
  render(view());
  await screen.findByText("没有可用的集群范围");
  expect(api.resources).not.toHaveBeenCalled();
});

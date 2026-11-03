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
import { ResourcesPage } from "../src/pages/ResourcesPage";
import { ApiError, setCredentials } from "../src/api/client";
import { resource } from "./fixtures";
const api = vi.hoisted(() => ({ list: vi.fn(), detail: vi.fn() }));
const scope = vi.hoisted(() => ({
  clusterUid: "cluster-a",
  invalid: false,
  scopeKey: "a:0",
}));
vi.mock("../src/app/ClusterContext", () => ({
  useOperationalCluster: () => scope,
}));
vi.mock("../src/components/ScopeFilter", () => ({ ScopeFilter: () => null }));
vi.mock("../src/api/generated/platform", () => ({
  getResources: api.list,
  getResourceByCanonicalID: api.detail,
}));
const node = {
  ...resource,
  canonicalId: "k8s+v1://tenant/cluster-a/core/Pod/uid-a",
  stableId: "uid-a",
  name: "gateway-api",
  kind: "Pod",
  scope: "cluster-a",
  namespace: "apps",
  attributes: { health: "normal" },
};
const response = (data: unknown) => ({
  status: 200,
  headers: new Headers(),
  data,
});
const page = {
  data: [node],
  meta: {
    summary: { total: 123 },
    nextCursor: "cursor-opaque",
    freshness: "fresh",
  },
};
const view = (url = "/resources?clusterUid=cluster-a") => (
  <QueryClientProvider
    client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
  >
    <MemoryRouter initialEntries={[url]}>
      <ResourcesPage />
    </MemoryRouter>
  </QueryClientProvider>
);
beforeEach(() => {
  api.list.mockReset();
  api.detail.mockReset();
  setCredentials("test-token");
  scope.clusterUid = "cluster-a";
  scope.invalid = false;
  scope.scopeKey = "a:0";
  api.list.mockResolvedValue(response(page));
  api.detail.mockResolvedValue(response({ data: { nodes: [node] } }));
});
it("shows identity, namespace, actual observation and whole authorized filtered count separately from this page", async () => {
  render(view());
  await screen.findByRole("link", { name: node.name });
  expect(screen.getByText("uid-a")).toBeVisible();
  expect(screen.getByText("本页 1 条 · 当前授权匹配 123 条")).toBeVisible();
  expect(
    screen.getByRole("columnheader", { name: "来源 / 最近观测" }),
  ).toBeVisible();
  expect(api.list).toHaveBeenCalledWith(
    expect.objectContaining({ limit: 10, clusterUid: "cluster-a" }),
    expect.anything(),
  );
});
it("searches the whole authorized server set with q and resets the cursor while preserving other filters", async () => {
  render(
    view(
      "/resources?clusterUid=cluster-a&namespace=apps&health=normal&cursor=old-cursor",
    ),
  );
  await screen.findByRole("link", { name: node.name });
  fireEvent.change(screen.getByLabelText("搜索资源"), {
    target: { value: "GATE" },
  });
  fireEvent.keyDown(screen.getByLabelText("搜索资源"), {
    key: "Enter",
    code: "Enter",
  });
  await waitFor(() =>
    expect(api.list).toHaveBeenLastCalledWith(
      expect.objectContaining({
        q: "GATE",
        namespace: "apps",
        health: "normal",
        clusterUid: "cluster-a",
      }),
      expect.anything(),
    ),
  );
  expect(api.list.mock.lastCall?.[0]).not.toHaveProperty("cursor");
  fireEvent.change(await screen.findByLabelText("资源每页条数"), {
    target: { value: "20" },
  });
  await waitFor(() => expect(api.list.mock.lastCall?.[0].limit).toBe(20));
  expect(api.list.mock.lastCall?.[0].q).toBe("GATE");
});
it("rereads canonical details for quick view; a denied read exposes no stale metadata or navigation", async () => {
  render(view());
  await screen.findByRole("link", { name: node.name });
  fireEvent.click(screen.getByRole("button", { name: "速览" }));
  const link = await screen.findByRole("link", { name: "进入资源详情" });
  expect(link).toHaveAttribute(
    "href",
    "/resources/detail?" +
      new URLSearchParams({
        clusterUid: "cluster-a",
        canonicalId: node.canonicalId,
      }),
  );
  expect(api.detail).toHaveBeenCalledWith(
    { canonicalId: node.canonicalId },
    expect.anything(),
  );
  fireEvent.click(
    screen.getByRole("dialog").querySelector(".ant-drawer-close")!,
  );
  api.detail.mockRejectedValue(new ApiError(403, "FORBIDDEN", "scope revoked"));
  fireEvent.click(screen.getByRole("button", { name: "速览" }));
  await screen.findByText("没有此范围的访问权限");
  expect(screen.queryByRole("link", { name: "进入资源详情" })).toBeNull();
  expect(screen.queryByRole("link", { name: "查看关系" })).toBeNull();
});
it("discards late quick view after the operational cluster changes", async () => {
  let finish!: (value: unknown) => void;
  api.detail.mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const rendered = render(view());
  await screen.findByRole("link", { name: node.name });
  fireEvent.click(screen.getByRole("button", { name: "速览" }));
  await waitFor(() => expect(api.detail).toHaveBeenCalled());
  scope.clusterUid = "cluster-b";
  scope.scopeKey = "b:1";
  api.list.mockResolvedValue(response({ data: [], meta: {} }));
  rendered.rerender(view());
  await act(async () => finish(response({ data: { nodes: [node] } })));
  expect(screen.queryByRole("dialog")).toBeNull();
});

function captureDownload() {
  Object.defineProperty(URL, "createObjectURL", {
    configurable: true,
    writable: true,
    value: vi.fn(() => "blob:resource-export"),
  });
  Object.defineProperty(URL, "revokeObjectURL", {
    configurable: true,
    writable: true,
    value: vi.fn(),
  });
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(() => {});
  return { create: vi.mocked(URL.createObjectURL), click };
}
it("exports a fresh exact page and distinguishes matched total, quote escaping and formula text", async () => {
  const download = captureDownload();
  const fresh = { ...node, name: '=SUM(1,2)\n"quoted"' };
  api.list.mockImplementation(async (params) =>
    response(params.canonicalIds ? { ...page, data: [fresh] } : page),
  );
  render(
    view(
      "/resources?clusterUid=cluster-a&namespace=apps&q=UID-A&cursor=old-cursor",
    ),
  );
  await screen.findByRole("link", { name: node.name });
  fireEvent.click(screen.getByRole("button", { name: "导出当前结果" }));
  await waitFor(() => expect(download.create).toHaveBeenCalledTimes(1));
  const csv = await new Promise<string>((resolve) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.readAsText(download.create.mock.calls[0][0] as Blob);
  });
  expect(csv).toContain('"\'=SUM(1,2)\n""quoted"""');
  expect(csv).toContain('"1","123"');
  expect(csv).toContain(node.canonicalId);
  expect(api.list).toHaveBeenLastCalledWith(
    expect.objectContaining({
      clusterUid: "cluster-a",
      canonicalIds: [node.canonicalId],
      namespace: "apps",
      q: "UID-A",
      limit: 50,
      cursor: undefined,
    }),
    expect.anything(),
  );
  expect(download.click).toHaveBeenCalledTimes(1);
  expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:resource-export");
});
it("does not export cached rows when current authorization or page membership changes", async () => {
  const download = captureDownload();
  api.list.mockImplementation(async (params) =>
    response(params.canonicalIds ? { data: [], meta: {} } : page),
  );
  render(view());
  await screen.findByRole("link", { name: node.name });
  fireEvent.click(screen.getByRole("button", { name: "导出当前结果" }));
  await screen.findByText(/资源已变化或授权范围已收窄/);
  expect(download.create).not.toHaveBeenCalled();
});
it("discards an export read that finishes after a cluster switch", async () => {
  const download = captureDownload();
  let complete!: (value: unknown) => void;
  api.list.mockImplementation((params) =>
    params.canonicalIds
      ? new Promise((resolve) => {
          complete = resolve;
        })
      : Promise.resolve(response(page)),
  );
  const rendered = render(view());
  await screen.findByRole("link", { name: node.name });
  fireEvent.click(screen.getByRole("button", { name: "导出当前结果" }));
  await waitFor(() => expect(complete).toBeDefined());
  scope.clusterUid = "cluster-b";
  scope.scopeKey = "b:1";
  api.list.mockResolvedValue(response({ data: [], meta: {} }));
  rendered.rerender(view());
  await act(async () => complete(response(page)));
  expect(download.create).not.toHaveBeenCalled();
});
it("compares 2-4 exact resources in a new server projection without mixing metrics", async () => {
  const second = {
    ...node,
    canonicalId: "k8s+v1://tenant/cluster-a/core/Pod/uid-b",
    stableId: "uid-b",
    name: "same-name",
    namespace: "ns-b",
  };
  api.list.mockResolvedValue(response({ ...page, data: [node, second] }));
  render(view());
  await screen.findByRole("link", { name: node.name });
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: "选择 " + node.name + " · Pod · apps",
    }),
  );
  expect(screen.getByRole("button", { name: "对比资源" })).toBeDisabled();
  fireEvent.click(
    screen.getByRole("checkbox", { name: "选择 same-name · Pod · ns-b" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "对比资源" }));
  await screen.findByRole("dialog", { name: "资源对比" });
  await screen.findByText(
    "只对比同次授权读取的静态投影字段，不将不同资源的指标或证据混合计算。",
  );
  expect(api.list).toHaveBeenLastCalledWith(
    expect.objectContaining({
      canonicalIds: [node.canonicalId, second.canonicalId],
      cursor: undefined,
      limit: 50,
    }),
    expect.anything(),
  );
  expect(screen.getAllByText("uid-b").length).toBeGreaterThan(0);
});
it("refuses comparison when one selected UID is no longer readable", async () => {
  const second = {
    ...node,
    canonicalId: "k8s+v1://tenant/cluster-a/core/Pod/uid-b",
    stableId: "uid-b",
    name: "second",
  };
  api.list.mockImplementation(async (params) =>
    response({ ...page, data: params.canonicalIds ? [node] : [node, second] }),
  );
  render(view());
  await screen.findByRole("link", { name: node.name });
  fireEvent.click(screen.getByRole("checkbox", { name: "选择本页资源" }));
  fireEvent.click(screen.getByRole("button", { name: "对比资源" }));
  await screen.findByText(/资源已变化或授权范围已收窄/);
  expect(
    screen.queryByText(
      "只对比同次授权读取的静态投影字段，不将不同资源的指标或证据混合计算。",
    ),
  ).toBeNull();
});

it("uses complete authorized candidates on an empty filtered page and resets filters without changing cluster or page size", async () => {
  api.list.mockImplementation(async (params) =>
    response({
      ...page,
      data: params.q ? [] : page.data,
      meta: {
        ...page.meta,
        summary: {
          total: params.q ? 0 : 123,
          filterOptions: {
            kinds: ["ConfigMap", "Pod"],
            namespaces: ["apps", "second"],
            clusterScoped: false,
          },
        },
      },
    }),
  );
  render(
    view(
      "/resources?clusterUid=cluster-a&q=absent&namespace=apps&kind=Pod&health=normal&domain=kubernetes&name=old&label=a%3Db&cursor=old&sort=kind&order=desc&limit=20",
    ),
  );
  await screen.findByText("当前筛选范围没有正式数据");
  fireEvent.mouseDown(screen.getByLabelText("资源类型"));
  await screen.findByRole("option", { name: "ConfigMap" });
  expect(screen.queryByRole("option", { name: "Secret" })).toBeNull();
  fireEvent.keyDown(screen.getByLabelText("资源类型"), { key: "Escape" });
  fireEvent.mouseDown(screen.getByLabelText("命名空间"));
  await screen.findByRole("option", { name: "second" });
  expect(screen.queryByRole("option", { name: "集群级资源" })).toBeNull();
  fireEvent.click(screen.getByTitle("second"));
  await waitFor(() =>
    expect(api.list.mock.lastCall?.[0].namespace).toBe("second"),
  );
  expect(api.list.mock.lastCall?.[0]).not.toHaveProperty("cursor");
  expect(api.list.mock.lastCall?.[0].q).toBe("absent");
  fireEvent.click(screen.getByRole("button", { name: /^重\s*置$/ }));
  await screen.findByRole("link", { name: node.name });
  expect(api.list.mock.lastCall?.[0]).toEqual({
    clusterUid: "cluster-a",
    limit: 20,
    order: "asc",
    includeFilterOptions: true,
  });
  expect(screen.getByLabelText("搜索资源")).toHaveValue("");
});
it("drops candidate options when a previously successful catalog refresh is denied", async () => {
  api.list.mockResolvedValue(
    response({
      ...page,
      meta: {
        summary: {
          total: 1,
          filterOptions: {
            kinds: ["PrivateKind"],
            namespaces: ["apps"],
            clusterScoped: true,
          },
        },
      },
    }),
  );
  render(view());
  await screen.findByRole("link", { name: node.name });
  fireEvent.mouseDown(screen.getByLabelText("资源类型"));
  await screen.findByRole("option", { name: "PrivateKind" });
  fireEvent.keyDown(screen.getByLabelText("资源类型"), { key: "Escape" });
  api.list.mockRejectedValue(new ApiError(403, "FORBIDDEN", "revoked"));
  fireEvent.click(screen.getByRole("button", { name: /刷\s*新/ }));
  await screen.findByText("没有此范围的访问权限");
  expect(screen.getByLabelText("资源类型")).toBeDisabled();
  expect(screen.getByLabelText("命名空间")).toBeDisabled();
  expect(screen.queryByRole("option", { name: "PrivateKind" })).toBeNull();
});

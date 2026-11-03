import { MemoryRouter } from "react-router-dom";
// UI mechanism fixture only; not live source or browser visual acceptance.
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { LogsPage } from "../src/pages/LogsPage";
import { ApiError } from "../src/api/client";
import {
  observation,
  observationHistory,
  type ObservationView,
} from "../src/api/observation";
vi.mock("../src/app/ClusterContext", () => ({
  useOperationalCluster: () => ({ clusterUid: "cluster-one", invalid: false }),
}));
vi.mock("../src/api/observation", () => ({
  observation: vi.fn(),
  observationHistory: vi.fn(),
}));
const resource = {
  canonicalId: "resource-one",
  uid: "uid-one",
  displayName: "web-one",
  clusterUid: "cluster-one",
  namespaceScope: {
    kind: "namespaced" as const,
    namespace: "apps",
    namespaceUid: "namespace-one",
  },
};
const other = {
  ...resource,
  canonicalId: "resource-two",
  uid: "uid-two",
  displayName: "web-two",
};
const source = {
  registrationId: "source-one",
  registrationRevision: 1,
  credentialRevision: 1,
  qualificationDigest: "sha256:qualified",
  collectorVersion: "collector/v1",
};
const caps = {
  resources: [resource, other],
  sources: [
    {
      name: "live-source",
      source,
      coverageFrom: "2026-10-28T01:30:00.150326Z",
      coverageTo: "2026-10-28T02:00:00Z",
      verifiedUntil: "2026-10-29T02:00:00Z",
      maxWindowSeconds: 1800,
    },
  ],
  budget: {
    maxRows: 20,
    maxBytes: 131072,
    maxDurationSeconds: 8,
    maxSourceCalls: 2,
  },
};
const view: ObservationView = {
  createdAt: "2026-10-28T02:00:00Z",
  expiresAt: "2026-11-27T02:00:00Z",
  queryId: "query-one",
  revision: 7,
  state: "completed",
  resource,
  source,
  window: {
    from: "2026-10-28T01:30:00Z",
    to: "2026-10-28T02:00:00Z",
    timezone: "Asia/Shanghai",
    bounds: "[from,to)",
  },
  failure: null,
  termination: null,
  filters: [],
};
beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(observation).mockReset();
  vi.mocked(observationHistory).mockReset();
});
function mount(path = "/observe/logs") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const ui = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <LogsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...ui, client };
}
async function select(name: string, text: string) {
  await waitFor(() =>
    expect(screen.getByRole("combobox", { name })).toBeEnabled(),
  );
  fireEvent.mouseDown(screen.getByRole("combobox", { name }));
  fireEvent.click(
    await screen.findByText(text, {
      selector: ".ant-select-item-option-content",
    }),
  );
}
async function prepare() {
  await select("资源对象", "Pod / apps / web-one");
  await select("数据来源", "VictoriaLogs · live-source");
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "查询" })).toBeEnabled(),
  );
}
it("keeps measured population separate from locally filtered examples", async () => {
  vi.mocked(observation).mockImplementation(async (path) => {
    if (path === "capabilities") return caps;
    if (path === "queries") return view;
    if (path.endsWith("/results"))
      return {
        queryId: view.queryId,
        resource,
        source,
        window: view.window,
        population: { basis: "exact", count: 101 },
        samples: {
          returned: 1,
          items: [
            {
              timestamp: "2026-10-28T01:45:00Z",
              level: "info",
              message: "one bounded log",
            },
          ],
        },
        status: "partial",
      };
    return view;
  });
  mount();
  await prepare();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  expect(
    await screen.findByText(/来源总体 101 条 · 本次脱敏样例 1 条/),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("link", { name: "按此资源与时间询问 AI" }),
  ).toHaveAttribute(
    "href",
    "/assistant?clusterUid=cluster-one&logQuery=query-one",
  );
  fireEvent.change(
    screen.getByRole("textbox", { name: "关键词（脱敏样例内检索）" }),
    { target: { value: "absent" } },
  );
  expect(screen.queryByText("one bounded log")).not.toBeInTheDocument();
  expect(screen.getByText(/来源总体 101 条/)).toBeInTheDocument();
  expect(screen.queryByText("所选范围没有日志")).not.toBeInTheDocument();
  expect(
    fireEvent.keyDown(
      screen.getByRole("textbox", { name: "关键词（脱敏样例内检索）" }),
      { key: "Enter", code: "Enter", charCode: 13, keyCode: 13 },
    ),
  ).toBe(false);

  const create = vi
    .mocked(observation)
    .mock.calls.find(([path, , , body]) => path === "queries" && !!body)!;
  expect(create[3]).toMatchObject({
    resource: {
      uid: "uid-one",
      namespaceScope: { namespaceUid: "namespace-one" },
    },
    window: {
      from: "2026-10-28T01:30:00.150326Z",
      to: "2026-10-28T02:00:00Z",
      bounds: "[from,to)",
    },
  });
  expect((create[3] as { resource: object }).resource).not.toHaveProperty(
    "displayName",
  );
});
it("discards a late accepted response after the resource changes", async () => {
  let accept!: (value: unknown) => void;
  vi.mocked(observation).mockImplementation((path) =>
    path === "capabilities"
      ? Promise.resolve(caps)
      : new Promise((resolve) => {
          accept = resolve;
        }),
  );
  mount();
  await prepare();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await waitFor(() => expect(accept).toBeDefined());
  await select("资源对象", "Pod / apps / web-two");
  accept(view);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "查询" })).toBeDisabled(),
  );
  expect(
    vi
      .mocked(observation)
      .mock.calls.some(([path]) => path === "queries/query-one"),
  ).toBe(false);
  expect(screen.queryByText(/来源总体/)).not.toBeInTheDocument();
});
it("binds cancellation to the actual persisted revision and waits for a receipt", async () => {
  let cancelled = false;
  vi.mocked(observation).mockImplementation(async (path) => {
    if (path === "capabilities") return caps;
    if (path.endsWith(":cancel")) {
      cancelled = true;
      return { state: "cancel_requested", termination: null };
    }
    return {
      ...view,
      revision: 8,
      state: cancelled ? "cancel_requested" : "running",
    };
  });
  mount();
  await prepare();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));

  const cancelButton = await screen.findByRole("button", { name: "取消查询" });
  await waitFor(() => expect(cancelButton).toBeEnabled());
  fireEvent.click(cancelButton);
  await screen.findByText("取消请求已提交，等待终止回执");
  expect(
    vi
      .mocked(observation)
      .mock.calls.find(([path]) => path.endsWith(":cancel"))?.[3],
  ).toMatchObject({ expectedRevision: 8 });
  expect(screen.queryByText("本次查询已终止")).not.toBeInTheDocument();
});

it("refreshes a conflicting cancellation without silently repeating it", async () => {
  vi.mocked(observation).mockImplementation(async (path) => {
    if (path === "capabilities") return caps;
    if (path.endsWith(":cancel"))
      throw new ApiError(409, "CONFLICT", "changed");
    return { ...view, state: "running" };
  });
  mount();
  await prepare();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  const cancelButton = await screen.findByRole("button", { name: "取消查询" });
  await waitFor(() => expect(cancelButton).toBeEnabled());
  fireEvent.click(cancelButton);
  await screen.findByText("查询状态已更新，请核对回执后再次取消。");
  expect(
    vi
      .mocked(observation)
      .mock.calls.filter(([path]) => path.endsWith(":cancel")),
  ).toHaveLength(1);
  expect(screen.queryByText("本次查询已终止")).not.toBeInTheDocument();
});

const inspectedResult = {
  queryId: view.queryId,
  resultId: "result-one",
  resultDigest: "sha256:frozen-result",
  resource,
  source,
  window: view.window,
  status: "partial",
  population: { basis: "exact", count: 186 },
  samples: {
    returned: 3,
    items: [
      {
        timestamp: "2026-10-28T01:35:00Z",
        level: "info",
        message: "oldest sample",
      },
      {
        timestamp: "2026-10-28T01:45:00Z",
        level: "warn",
        message: "middle sample",
      },
      {
        timestamp: "2026-10-28T01:55:00Z",
        level: "error",
        message: "newest sample",
      },
    ],
  },
};
it("sorts and wraps only returned samples, preserves identity through adjacent detail and saved conditions", async () => {
  vi.mocked(observation).mockImplementation(async (path) => {
    if (path === "capabilities") return caps;
    if (path.endsWith("/results")) return inspectedResult;
    return view;
  });
  mount();
  await prepare();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await screen.findByText("newest sample");
  expect(
    screen.getAllByRole("button", { name: /^日志详情 / })[0],
  ).toHaveTextContent("newest sample");
  fireEvent.click(screen.getByRole("button", { name: "时间：倒序" }));
  expect(
    screen.getAllByRole("button", { name: /^日志详情 / })[0],
  ).toHaveTextContent("oldest sample");
  fireEvent.click(screen.getByRole("button", { name: "自动换行" }));
  expect(screen.getByRole("button", { name: "关闭换行" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  fireEvent.click(screen.getAllByRole("button", { name: /^日志详情 / })[0]);
  const detail = (await screen.findByText("日志详情")).closest<HTMLElement>(
    '[role="dialog"]',
  )!;
  expect(within(detail).getByRole("button", { name: "上一条" })).toBeDisabled();
  fireEvent.click(within(detail).getByRole("tab", { name: "原始内容" }));
  expect(detail.querySelector("pre")).toHaveTextContent(
    '"message": "oldest sample"',
  );
  expect(detail.querySelector("pre")).toHaveTextContent(
    '"namespaceUid": "namespace-one"',
  );
  fireEvent.click(within(detail).getByRole("button", { name: "下一条" }));
  expect(detail.querySelector("pre")).toHaveTextContent(
    '"message": "middle sample"',
  );
  fireEvent.click(within(detail).getByRole("button", { name: /close/i }));
  fireEvent.click(screen.getByRole("button", { name: "查询检查" }));
  const inspector = (
    await screen.findByText("查询检查", { selector: ".ant-drawer-title" })
  ).closest<HTMLElement>('[role="dialog"]')!;
  fireEvent.click(within(inspector).getByRole("tab", { name: "查询条件" }));
  expect(inspector.querySelector("pre")).toHaveTextContent(view.window.from);
  expect(inspector.querySelector("pre")).toHaveTextContent(
    source.qualificationDigest,
  );
  fireEvent.click(within(inspector).getByRole("tab", { name: "结果快照" }));
  expect(
    inspector.querySelector("[role=tabpanel][aria-hidden=false] pre"),
  ).toHaveTextContent(inspectedResult.resultDigest);
  expect(
    vi
      .mocked(observation)
      .mock.calls.filter(([path, , , body]) => path === "queries" && body),
  ).toHaveLength(1);
});
it("hides cached samples and their open detail when the query read is denied", async () => {
  let denied = false;
  vi.mocked(observation).mockImplementation(async (path) => {
    if (path === "capabilities") return caps;
    if (path.endsWith("/results")) return inspectedResult;
    if (denied) throw new ApiError(403, "FORBIDDEN", "revoked");
    return view;
  });
  const { client } = mount();
  await prepare();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await screen.findByText("newest sample");
  fireEvent.click(screen.getAllByRole("button", { name: /^日志详情 / })[0]);
  await screen.findByText("日志详情");
  denied = true;
  await client.invalidateQueries({ queryKey: ["observation-query"] });
  await screen.findByText(/FORBIDDEN/);
  await waitFor(() =>
    expect(
      screen.queryByRole("dialog", { name: "日志详情" }),
    ).not.toBeInTheDocument(),
  );
  expect(screen.queryByText("newest sample")).not.toBeInTheDocument();
});

it("loads the next owned history cursor without dropping the previous page", async () => {
  vi.mocked(observation).mockResolvedValue(caps);
  vi.mocked(observationHistory).mockImplementation(async (_cluster, cursor) =>
    cursor
      ? {
          data: [
            {
              ...view,
              queryId: "query-two",
              resource: { ...resource, uid: "history-second-uid" },
            },
          ],
          nextCursor: null,
        }
      : {
          data: [
            { ...view, resource: { ...resource, uid: "history-first-uid" } },
          ],
          nextCursor: "cursor-one",
        },
  );
  mount();
  fireEvent.click(screen.getByRole("button", { name: /查询历史/ }));
  await screen.findByText("history-first-uid");
  fireEvent.click(screen.getByRole("button", { name: "加载更多" }));
  await screen.findByText("history-second-uid");
  expect(screen.getByText("history-first-uid")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "加载更多" })).toBeDisabled();
  expect(vi.mocked(observationHistory).mock.calls[1].slice(0, 2)).toEqual([
    "cluster-one",
    "cursor-one",
  ]);
});
it("inspects the saved request budget rather than today's capabilities", async () => {
  const savedRequest = {
    schemaVersion: "ui-v12-observability-query/v1",
    queryKind: "logs",
    templateId: "logs.records",
    templateVersion: "logs.records/v1",
    resource,
    source,
    window: { ...view.window, from: "2026-10-28T01:30:00.150326Z" },
    filters: [],
    budget: { ...caps.budget, maxRows: 7 },
  };
  vi.mocked(observation).mockImplementation(async (path) => {
    if (path === "capabilities") return caps;
    if (path.endsWith("/results")) return inspectedResult;
    return { ...view, request: savedRequest };
  });
  mount();
  await prepare();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await screen.findByText("newest sample");
  fireEvent.click(screen.getByRole("button", { name: "查询检查" }));
  const inspector = (
    await screen.findByText("查询检查", { selector: ".ant-drawer-title" })
  ).closest<HTMLElement>('[role="dialog"]')!;
  fireEvent.click(within(inspector).getByRole("tab", { name: "查询条件" }));
  expect(JSON.parse(inspector.querySelector("pre")!.textContent!)).toEqual(
    savedRequest,
  );
  fireEvent.click(within(inspector).getByRole("button", { name: /close/i }));
  fireEvent.click(screen.getAllByRole("button", { name: /^日志详情 / })[0]);
  fireEvent.click(
    await screen.findByRole("button", { name: "ERROR · 按此筛选" }),
  );
  const writes = vi
    .mocked(observation)
    .mock.calls.filter(([path, , , body]) => path === "queries" && body);
  expect(writes.at(-1)![3]).toEqual({
    ...savedRequest,
    filters: [{ field: "level", operator: "eq", value: "error" }],
  });
});
it("shows expiry without reading or exposing the cached result", async () => {
  vi.mocked(observation).mockImplementation(async (path) =>
    path === "capabilities"
      ? caps
      : {
          ...view,
          state: "expired",
          failure: {
            code: "RESULT_EXPIRED",
            message: "expired",
            retryable: false,
          },
        },
  );
  const { client } = mount();
  client.setQueryData(
    ["observation-result", "cluster-one", "query-one"],
    inspectedResult,
  );
  await prepare();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await screen.findByText("查询结果已过期");
  expect(screen.queryByText("newest sample")).not.toBeInTheDocument();
  expect(
    vi
      .mocked(observation)
      .mock.calls.some(([path]) => path.endsWith("/results")),
  ).toBe(false);
});

it("buckets only returned samples across midnight, clips the last interval and filters locally", async () => {
  const window = {
    ...view.window,
    from: "2026-10-28T15:55:00Z",
    to: "2026-10-28T16:22:00Z",
  };
  const result = {
    ...inspectedResult,
    window,
    samples: {
      returned: 4,
      items: [
        {
          timestamp: "2026-10-28T15:55:00Z",
          level: "info",
          message: "window start",
        },
        {
          timestamp: "2026-10-28T15:59:59.999Z",
          level: "info",
          message: "before boundary",
        },
        {
          timestamp: "2026-10-28T16:00:00Z",
          level: "error",
          message: "at boundary",
        },
        {
          timestamp: "2026-10-28T16:21:59.999Z",
          level: "warn",
          message: "partial final interval",
        },
      ],
    },
  };
  vi.mocked(observation).mockImplementation(async (path) =>
    path === "capabilities"
      ? caps
      : path.endsWith("/results")
        ? result
        : { ...view, window },
  );
  mount();
  await prepare();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await screen.findByText("at boundary");
  expect(
    screen.getByText(/来源总体 186 条 · 本次脱敏样例 4 条/),
  ).toBeInTheDocument();
  expect(
    screen.getAllByRole("button", { name: /^筛选 .*的返回样例/ }),
  ).toHaveLength(6);
  fireEvent.click(
    screen.getByRole("button", {
      name: "筛选 00:00:00 至 00:05:00 的返回样例，共 1 条",
    }),
  );
  expect(screen.getByText("at boundary")).toBeInTheDocument();
  expect(screen.queryByText("before boundary")).not.toBeInTheDocument();
  fireEvent.change(
    screen.getByRole("textbox", { name: "关键词（脱敏样例内检索）" }),
    { target: { value: "absent" } },
  );
  expect(screen.getByText("没有匹配的返回样例")).toBeInTheDocument();
  expect(screen.queryByText("所选范围没有日志")).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", {
      name: "筛选 23:55:00 至 00:00:00 的返回样例，共 2 条",
    }),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "清除时间筛选 ×" }));
  fireEvent.change(
    screen.getByRole("textbox", { name: "关键词（脱敏样例内检索）" }),
    { target: { value: "" } },
  );
  fireEvent.click(
    screen.getByRole("button", {
      name: "筛选 00:20:00 至 00:22:00 的返回样例，共 1 条",
    }),
  );
  expect(screen.getByText("partial final interval")).toBeInTheDocument();
  expect(screen.queryByText("at boundary")).not.toBeInTheDocument();
  const facet = screen.getByRole("complementary", { name: "返回样例字段" });
  expect(
    within(facet).getByRole("button", { name: "INFO · 按级别查询" }),
  ).toHaveTextContent("2");
  expect(
    vi
      .mocked(observation)
      .mock.calls.filter(([path, , , body]) => path === "queries" && body),
  ).toHaveLength(1);
});

const originalObjectURL = URL.createObjectURL,
  originalRevokeURL = URL.revokeObjectURL;
afterEach(() => {
  URL.createObjectURL = originalObjectURL;
  URL.revokeObjectURL = originalRevokeURL;
});
function captureDownload() {
  const create = vi.fn((_blob: Blob) => "blob:unit-export");
  URL.createObjectURL = create;
  URL.revokeObjectURL = vi.fn();
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(() => {});
  return { create, click };
}
it("rechecks the result and exports only visible samples with CSV text escaping and scope", async () => {
  const downloaded = captureDownload();
  let reads = 0;
  const data = {
    ...inspectedResult,
    samples: {
      returned: 2,
      items: [
        {
          timestamp: view.window.from,
          level: "error",
          message: '=SUM(1,2)\n"quoted"',
        },
        {
          timestamp: "2026-10-28T01:59:00Z",
          level: "info",
          message: "not exported",
        },
      ],
    },
  };
  vi.mocked(observation).mockImplementation(async (path) => {
    if (path === "capabilities") return caps;
    if (path.endsWith("/results")) {
      reads++;
      return data;
    }
    return view;
  });
  mount();
  await prepare();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await screen.findByText(/来源总体 186 条/);
  fireEvent.change(
    screen.getByRole("textbox", { name: "关键词（脱敏样例内检索）" }),
    { target: { value: "SUM" } },
  );
  fireEvent.click(screen.getByRole("button", { name: "导出数据" }));
  await waitFor(() => expect(downloaded.create).toHaveBeenCalledTimes(1));
  expect(reads).toBe(2);
  const blob = downloaded.create.mock.calls[0][0];
  const csv = await new Promise<string>((resolve) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.readAsText(blob);
  });
  expect(csv).toContain(`"'=SUM(1,2)\n""quoted"""`);
  expect(csv).not.toContain("not exported");
  expect(csv).toContain('"186","2","1"');
  expect(csv).toContain(resource.canonicalId);
  expect(csv).toContain(inspectedResult.resultDigest);
  expect(
    (downloaded.click.mock.instances[0] as HTMLAnchorElement).download,
  ).toBe("logs-query-one-samples.csv");
  expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:unit-export");
});
it("does not download cached data when export read is denied", async () => {
  const downloaded = captureDownload();
  let denied = false;
  vi.mocked(observation).mockImplementation(async (path) => {
    if (path === "capabilities") return caps;
    if (path.endsWith("/results")) {
      if (denied) throw new ApiError(403, "FORBIDDEN", "revoked");
      return inspectedResult;
    }
    return view;
  });
  mount();
  await prepare();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await screen.findByText("newest sample");
  denied = true;
  fireEvent.click(screen.getByRole("button", { name: "导出数据" }));
  await screen.findByText(/FORBIDDEN/);
  expect(downloaded.create).not.toHaveBeenCalled();
  expect(screen.queryByText("newest sample")).not.toBeInTheDocument();
});
it("discards a late export after resource selection changes", async () => {
  const downloaded = captureDownload();
  let complete!: (value: unknown) => void,
    reads = 0;
  vi.mocked(observation).mockImplementation(async (path) => {
    if (path === "capabilities") return caps;
    if (path.endsWith("/results")) {
      reads++;
      return reads === 1
        ? inspectedResult
        : await new Promise((resolve) => {
            complete = resolve;
          });
    }
    return view;
  });
  mount();
  await prepare();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await screen.findByText("newest sample");
  fireEvent.click(screen.getByRole("button", { name: "导出数据" }));
  await waitFor(() => expect(reads).toBe(2));
  await select("资源对象", "Pod / apps / web-two");
  await act(async () => {
    complete(inspectedResult);
  });
  expect(downloaded.create).not.toHaveBeenCalled();
});
it("keeps a historical selected window exact beside the current source window", async () => {
  const window = {
    ...view.window,
    from: "2026-10-28T01:30:01.123456Z",
    to: "2026-10-28T01:45:00.654321Z",
  };
  vi.mocked(observationHistory).mockResolvedValue({
    data: [{ ...view, window }],
    nextCursor: null,
  });
  vi.mocked(observation).mockImplementation(async (path) =>
    path === "capabilities"
      ? caps
      : path.endsWith("/results")
        ? {
            ...inspectedResult,
            window,
            samples: { returned: 1, items: [inspectedResult.samples.items[0]] },
          }
        : { ...view, window },
  );
  mount();
  fireEvent.click(screen.getByRole("button", { name: /查询历史/ }));
  const history = (
    await screen.findByText("查询历史", { selector: ".ant-modal-title" })
  ).closest<HTMLElement>('[role="dialog"]')!;
  fireEvent.click(
    await within(history).findByRole("button", { name: /^查\s*看$/ }),
  );
  await screen.findByText("oldest sample");
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await waitFor(() =>
    expect(
      vi
        .mocked(observation)
        .mock.calls.some(([path, , , body]) => path === "queries" && !!body),
    ).toBe(true),
  );
  const body = vi
    .mocked(observation)
    .mock.calls.find(
      ([path, , , body]) => path === "queries" && !!body,
    )![3] as { window: typeof window };
  expect(body.window).toEqual(window);
});

it("bounds a long qualified source window without truncating its end timestamp", async () => {
  const coverage = {
    ...caps,
    sources: [
      {
        ...caps.sources[0],
        coverageFrom: "2026-10-28T01:00:00.150326Z",
        coverageTo: "2026-10-28T02:00:00.150326Z",
      },
    ],
  };
  vi.mocked(observation).mockImplementation(async (path) =>
    path === "capabilities" ? coverage : { ...view, state: "accepted" },
  );
  mount();
  await prepare();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await waitFor(() =>
    expect(
      vi
        .mocked(observation)
        .mock.calls.some(([path, , , body]) => path === "queries" && !!body),
    ).toBe(true),
  );
  const body = vi
    .mocked(observation)
    .mock.calls.find(
      ([path, , , body]) => path === "queries" && !!body,
    )![3] as { window: { from: string; to: string } };
  expect(body.window).toMatchObject({
    from: "2026-10-28T01:30:01.000Z",
    to: "2026-10-28T02:00:00.150326Z",
  });
  expect(
    Date.parse(body.window.to) - Date.parse(body.window.from),
  ).toBeLessThanOrEqual(1800_000);
});

it("restores an exact resource deep link without submitting a query", async () => {
  vi.mocked(observation).mockResolvedValue({ ...caps, sources: [] });
  mount("/observe/logs?clusterUid=cluster-one&canonicalId=resource-one");
  await waitFor(() =>
    expect(observation).toHaveBeenCalledWith(
      "capabilities",
      "cluster-one",
      expect.anything(),
      undefined,
      "resource-one",
    ),
  );
  await screen.findByText("Pod / apps / web-one");
  expect(screen.getByRole("button", { name: "查询" })).toBeDisabled();
  expect(
    vi
      .mocked(observation)
      .mock.calls.some(
        (call) => call[0] === "queries" && call[3] !== undefined,
      ),
  ).toBe(false);
});

it("rejects a denied deep link without selecting a different resource or submitting", async () => {
  vi.mocked(observation).mockRejectedValue(
    new ApiError(403, "FORBIDDEN", "scope denied"),
  );
  mount("/observe/logs?clusterUid=cluster-one&canonicalId=foreign-resource");
  await screen.findByText(/FORBIDDEN/);
  expect(screen.queryByText("Pod / apps / web-one")).toBeNull();
  expect(screen.getByRole("button", { name: "查询" })).toBeDisabled();
  expect(
    vi
      .mocked(observation)
      .mock.calls.every(
        (call) => call[0] === "capabilities" && call[4] === "foreign-resource",
      ),
  ).toBe(true);
});

it("does not retain a checked source badge after the capability recheck fails", async () => {
  vi.mocked(observation).mockImplementation(async (path) =>
    path === "capabilities" ? caps : view,
  );
  const { client } = mount();
  await prepare();
  await screen.findByText("资格已核对");
  vi.mocked(observation).mockRejectedValue(
    new ApiError(403, "FORBIDDEN", "qualification recheck denied"),
  );
  await act(async () => {
    await client.invalidateQueries({ queryKey: ["observation-capabilities"] });
  });
  await screen.findByText(/FORBIDDEN/);
  expect(screen.queryByText("资格已核对")).toBeNull();
  expect(screen.getByText("待选择合格来源")).toBeVisible();
  expect(screen.getByRole("button", { name: "查询" })).toBeDisabled();
});

it("labels the real native log source and keeps an empty bounded read distinct from measured zero", async () => {
  const nativeSource = {
    ...source,
    collectorVersion: "kubernetes-pod-logs/v1",
  };
  const nativeView = { ...view, source: nativeSource };
  vi.mocked(observation).mockImplementation(async (path) => {
    if (path === "capabilities")
      return {
        ...caps,
        sources: [
          {
            ...caps.sources[0],
            name: "Kubernetes 原生日志（当前容器）",
            source: nativeSource,
          },
        ],
      };
    if (path.endsWith("/results"))
      return {
        queryId: view.queryId,
        resource,
        source: nativeSource,
        window: view.window,
        population: { basis: "not_measured" },
        samples: { returned: 0, items: [] },
        status: "partial",
        warnings: ["native_current_container_logs_rotation_coverage_unknown"],
      };
    return nativeView;
  });
  mount("/observe/logs?canonicalId=resource-one");
  await select("数据来源", "Kubernetes 原生日志（当前容器）");
  await screen.findByText(/直接读取当前容器的 Kubernetes 原生日志/);
  expect(
    screen.queryByText("VictoriaLogs · Kubernetes 原生日志（当前容器）"),
  ).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "查询" }));
  await screen.findByText("日志总体未测量 · 本次脱敏样例 0 条");
  expect(screen.queryByText("所选范围没有日志")).toBeNull();
  expect(screen.queryByText(/来源总体 0 条/)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /刷新来源/ }));
  await waitFor(() =>
    expect(
      vi
        .mocked(observation)
        .mock.calls.filter(([path]) => path === "capabilities").length,
    ).toBeGreaterThan(1),
  );
  expect(screen.getByRole("button", { name: "查询" })).toBeDisabled();
  expect(
    vi.mocked(observation).mock.calls.filter(([path]) => path === "queries"),
  ).toHaveLength(1);
});

// UI mechanism fixture; no database, browser visual or live authorization claim.
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
afterEach(cleanup);
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { ResourcesPage } from "../src/pages/ResourcesPage";
import { QueryResultView } from "../src/features/assistant/QueryResultView";
import {
  AssistantTurnActions,
  buildTurnCopyPayload,
  turnCopyText,
  turnSourceStatement,
} from "../src/features/assistant/AssistantTurnActions";
import {
  buildResultCopyPayload,
  resultCopyText,
} from "../src/features/assistant/resultActions";
import { countDrillTarget } from "../src/features/assistant/drill";
import { assistantEntryHref } from "../src/features/assistant/scope";
import type {
  AssistantAnswerV1,
  AssistantQueryResultV1,
  AssistantStoredQueryResultV1,
} from "../src/api/generated/models";
import {
  getResources,
  getAssistantQueryResult,
  getAssistantTurn,
} from "../src/api/generated/platform";
import { ApiError, setNativeCredentials } from "../src/api/client";

// The authorization context of the fixture. The account and the scope
// generation are mutable, so a test can switch the context of a mounted
// component.
const fixtureState = vi.hoisted(() => ({
  session: {
    tenantId: "tenant-fixture",
    subject: "operator-fixture",
    roles: ["operator"] as string[],
    clusterScopes: [] as string[],
    namespaceScopes: [] as string[],
  },
  cluster: { scopeKey: "cluster-a:1", generation: 1 },
}));

vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({ session: { ...fixtureState.session } }),
}));
vi.mock("../src/app/ClusterContext", () => ({
  useOperationalCluster: () => ({
    clusterUid: "cluster-a",
    candidates: [],
    scopeKey: fixtureState.cluster.scopeKey,
    generation: fixtureState.cluster.generation,
  }),
  useClusterDraft: vi.fn(),
}));
vi.mock("../src/api/generated/platform", async (original) => ({
  ...(await original<object>()),
  getResources: vi.fn(),
  getAssistantQueryResult: vi.fn(),
  getAssistantTurn: vi.fn(),
}));

const answer: AssistantAnswerV1 = {
  schemaVersion: "assistant-answer/v1",
  answerKind: "diagnosis",
  summary: "保留已提交证据",
  asOf: "2026-11-10T00:00:00Z",
  scopeSummary: "bound",
  partial: false,
  factRefs: [],
  queryResultRefs: [],
  entityRefs: [],
  evidenceRefs: [],
  hypotheses: [
    {
      key: "H1",
      summary: "内存压力导致重启",
      supports: ["EV-6101"],
      conflicts: [],
      missingEvidence: [],
      nextChecks: [],
    },
  ],
  warnings: [],
  degradedSources: [],
  missingEvidence: [],
  nextChecks: [],
};

const result: AssistantQueryResultV1 = {
  schemaVersion: "query-result/v1",
  datasetId: "resources.current",
  planDigest: "sha256:" + "1".repeat(64),
  catalogDigest: "sha256:" + "2".repeat(64),
  scopeDigest: "sha256:" + "3".repeat(64),
  status: "complete",
  exact: true,
  truncated: false,
  dimension: "health",
  unit: "resources",
  rows: [
    { dimension: "normal", value: 12 },
    { dimension: "abnormal", value: 3 },
  ],
  snapshotReadAt: "2026-11-10T00:00:00Z",
  freshness: "fresh",
  warnings: [],
  degradedSources: [],
  graphRevision: { ownerEpoch: 4, graphGeneration: 7 },
};

const stored: AssistantStoredQueryResultV1 = {
  queryResultId: "query-result-1",
  contentDigest: "sha256:" + "4".repeat(64),
  result,
  facts: [
    {
      schemaVersion: "query-fact/v1",
      factId: "sha256:" + "5".repeat(64),
      row: 1,
      column: "value",
      value: 3,
      unit: "resources",
      exact: true,
    },
  ],
};

// The committed turn of the fixture. The copy and the export read it again
// under the current authorization before they publish anything.
function turnResponse(
  turnId: string,
  refs: string[],
  freshAnswer: AssistantAnswerV1 = answer,
) {
  return {
    status: 200,
    headers: new Headers(),
    data: {
      data: {
        turnId,
        conversationId: "conv-1",
        jobId: "job-1",
        state: "completed",
        createdAt: "2026-11-10T00:00:00Z",
        conversationRevision: 1,
        answer: { ...freshAnswer, queryResultRefs: refs },
      },
      meta: {},
      requestId: "unit",
    },
  } as never;
}

function storedResponse() {
  return {
    status: 200,
    headers: new Headers(),
    data: {
      data: stored,
      queryScope: {
        clusterUid: "cluster-a",
        namespaces: ["apps"],
        canonicalIds: [],
        clusterScoped: false,
        filters: [{ field: "kind", operator: "eq", value: "Pod" }],
        filtersKnown: true,
      },
      meta: {},
      requestId: "unit",
    },
  } as never;
}

// The download of the fixture: the object URL and the click of the anchor.
function captureDownload() {
  Object.defineProperty(URL, "createObjectURL", {
    configurable: true,
    writable: true,
    value: vi.fn(() => "blob:assistant-export"),
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

function readBlob(blob: Blob) {
  return new Promise<string>((resolve) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.readAsText(blob);
  });
}

// The messages of the notification bar. The count grows with every new
// message, so a test can tell a new message from a message of an earlier
// test.
function notices() {
  return document.querySelectorAll(".ant-message-notice");
}

// The answer level actions of one turn. The turn can be switched without
// unmounting the component.
function renderTurn(turn: string, turnAnswer: AssistantAnswerV1 = answer) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const tree = (id: string) => (
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <AssistantTurnActions
          answer={turnAnswer}
          identity={{ conversationId: "conv-1", turnId: id }}
        />
      </MemoryRouter>
    </QueryClientProvider>
  );
  return { client, tree, view: render(tree(turn)) };
}

function renderResult() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  (
    getAssistantQueryResult as unknown as ReturnType<typeof vi.fn>
  ).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: {
      data: stored,
      queryScope: {
        clusterUid: "cluster-a",
        namespaces: ["apps"],
        canonicalIds: [],
        clusterScoped: false,
        filters: [{ field: "kind", operator: "eq", value: "Pod" }],
        filtersKnown: true,
      },
      meta: {},
      requestId: "unit",
    },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <QueryResultView id="query-result-1" answer={answer} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  fixtureState.session.subject = "operator-fixture";
  fixtureState.cluster.scopeKey = "cluster-a:1";
  fixtureState.cluster.generation = 1;
  // The request options of the native session are built before the generated
  // client is called, so the fixture needs the credentials of the session.
  setNativeCredentials("tenant-fixture", "fixture-csrf-key-0001");
});

it("drills a count into the real record list and keeps the snapshot identity", async () => {
  renderResult();
  await waitFor(() =>
    expect(screen.getAllByText("查看符合条件的记录 →").length).toBe(2),
  );
  const links = screen.getAllByText("查看符合条件的记录 →");
  const normal = links.find((node) =>
    (node.getAttribute("href") || "").includes("health=normal"),
  );
  expect(normal).toBeTruthy();
  const href = normal?.closest("a")?.getAttribute("href") || "";
  expect(href.startsWith("/resources?")).toBe(true);
  const params = new URLSearchParams(href.split("?")[1]);
  expect(params.get("assistantQueryResultId")).toBe("query-result-1");
  expect(params.get("assistantGroup")).toBe("normal");
  expect(params.get("clusterUid")).toBe("cluster-a");
  expect(params.get("aiFrom")).toBe("assistant");
  expect(params.get("aiDataset")).toBe("resources.current");
  expect(params.get("aiScopeDigest")).toBe(result.scopeDigest);
  expect(params.get("aiSnapshotAt")).toBe(result.snapshotReadAt);
  expect(params.get("aiCount")).toBe("12");
  expect(params.get("aiVersion")).toContain("Graph 7");
  // A dimension without a matching list filter has no drill down.
  const other = links.find((node) =>
    (node.getAttribute("href") || "").includes("health=abnormal"),
  );
  expect(other).toBeTruthy();
});

it("offers no drill down for a dataset without a record list", () => {
  const target = countDrillTarget(
    { ...result, datasetId: "metrics.pod-cpu" },
    "normal",
  );
  expect(target).toBeUndefined();
});

it("keeps the copy payload free of internal binding and raw model input", () => {
  const payload = buildTurnCopyPayload(answer, {
    conversationId: "conversation-a",
    turnId: "turn-a",
  });
  const text = turnCopyText(payload);
  expect(text).toContain("保留已提交证据");
  expect(text).toContain("数据与数字来自平台已提交并持久化的查询结果");
  const serialized = JSON.stringify(payload);
  expect(serialized).not.toContain("grantSnapshot");
  expect(serialized).not.toContain("rawModelInput");
  expect(serialized).not.toContain("binding");
  expect(payload.conversationId).toBe("conversation-a");
  expect(payload.turnId).toBe("turn-a");
});

it("copies the numbers of the persisted result with its fixed identifiers", () => {
  const payload = buildResultCopyPayload("query-result-1", stored)!;
  expect(payload.datasetId).toBe("resources.current");
  expect(payload.scopeDigest).toBe(result.scopeDigest);
  expect(payload.rows.length).toBe(2);
  expect(payload.facts[0].factId).toBe(stored.facts[0].factId);
  const text = resultCopyText(payload);
  expect(text).toContain("abnormal：3 resources");
  expect(text).toContain("数字来自平台已提交并持久化的查询结果");
});

it("switches the trend between the chart and the values", async () => {
  const trendResult: AssistantQueryResultV1 = {
    ...result,
    datasetId: "metrics.pod-cpu",
    dimension: "time",
    unit: "cores",
    timeRange: {
      from: "2026-11-10T00:00:00Z",
      to: "2026-11-10T00:10:00Z",
      timezone: "Asia/Shanghai",
    },
    stepSeconds: 60,
    rows: [
      { dimension: "2026-11-10T00:00:00Z", value: 1 },
      { dimension: "2026-11-10T00:01:00Z", value: 2 },
    ],
  };
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  (
    getAssistantQueryResult as unknown as ReturnType<typeof vi.fn>
  ).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: {
      data: { ...stored, result: trendResult },
      meta: {},
      requestId: "unit",
    },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <QueryResultView id="query-result-1" answer={answer} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(screen.getByLabelText("图表或数值")).toBeTruthy());
  // The chart is the default view of a trend.
  expect(
    screen.getByLabelText("cores时间趋势；缺失观测不补零、不跨缺口连线"),
  ).toBeTruthy();
  expect(
    screen.getByText(
      /固定步长 60 秒；端点均参与历史求值，使用显式 5 分钟回看。/,
    ),
  ).toBeTruthy();
  fireEvent.click(screen.getByText("数值"));
  await waitFor(() =>
    expect(
      screen.queryByLabelText("cores时间趋势；缺失观测不补零、不跨缺口连线"),
    ).toBeNull(),
  );
  expect(screen.getByText("2026-11-10T00:00:00Z")).toBeTruthy();
});

it("opens the observation point of a chart with its real time and unit", async () => {
  const trendResult: AssistantQueryResultV1 = {
    ...result,
    datasetId: "metrics.pod-cpu",
    dimension: "time",
    unit: "cores",
    timeRange: {
      from: "2026-11-10T00:00:00Z",
      to: "2026-11-10T00:01:00Z",
      timezone: "Asia/Shanghai",
    },
    stepSeconds: 60,
    rows: [{ dimension: "2026-11-10T00:00:00Z", value: 1 }],
  };
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  (
    getAssistantQueryResult as unknown as ReturnType<typeof vi.fn>
  ).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: {
      data: { ...stored, result: trendResult },
      meta: {},
      requestId: "unit",
    },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <QueryResultView id="query-result-1" answer={answer} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await waitFor(() =>
    expect(screen.getByLabelText("2026-11-10T00:00:00Z 1 cores")).toBeTruthy(),
  );
  fireEvent.click(screen.getByLabelText("2026-11-10T00:00:00Z 1 cores"));
  await waitFor(() => expect(screen.getByText("观测点与来源")).toBeTruthy());
  const drawer = screen
    .getByText("观测点与来源")
    .closest(".ant-drawer") as HTMLElement;
  expect(within(drawer).getByText("2026-11-10T00:00:00Z")).toBeTruthy();
  expect(within(drawer).getByText("1 cores")).toBeTruthy();
  expect(within(drawer).getByText("metrics.pod-cpu")).toBeTruthy();
  expect(within(drawer).getByText("第 1 行 / value")).toBeTruthy();
});

it("shows the caliber and the source of the submitted result in its own drawer", async () => {
  renderResult();
  await waitFor(() => expect(screen.getByText("查询口径与来源")).toBeTruthy());
  fireEvent.click(screen.getByText("查询口径与来源"));
  await waitFor(() => expect(screen.getByText("回答口径与来源")).toBeTruthy());
  expect(screen.getByText("resources.current")).toBeTruthy();
  expect(screen.getByText(result.scopeDigest)).toBeTruthy();
  expect(screen.getByText(stored.contentDigest!)).toBeTruthy();
});

it("carries the scope, the resource identity and the window into the assistant", () => {
  const href = assistantEntryHref({
    clusterUid: "cluster-a",
    canonicalId: "pod/ns/gateway-api",
    mode: "troubleshoot",
    from: "2026-11-10T00:00:00Z",
    to: "2026-11-10T00:10:00Z",
  });
  const params = new URLSearchParams(href.split("?")[1]);
  expect(params.get("clusterUid")).toBe("cluster-a");
  expect(params.get("canonicalId")).toBe("pod/ns/gateway-api");
  expect(params.get("mode")).toBe("troubleshoot");
  expect(params.get("from")).toBe("2026-11-10T00:00:00Z");
  expect(params.get("to")).toBe("2026-11-10T00:10:00Z");
});

it("keeps the original persisted row index of a day point", async () => {
  const { QueryDayTrend } =
    await import("../src/features/assistant/QueryDayTrend");
  const selected = vi.fn();
  render(
    <QueryDayTrend
      rows={[{ dimension: "2026-11-10", value: 7 }]}
      unit="resources"
      onPointSelect={selected}
    />,
  );
  fireEvent.click(screen.getByLabelText("2026-11-10 7 resources"));
  expect(selected).toHaveBeenCalledWith(expect.objectContaining({ row: 0 }));
});

it("closes the observation point when the result changes", async () => {
  const trend = {
    ...result,
    datasetId: "metrics.pod-cpu",
    dimension: "time",
    unit: "cores",
    timeRange: {
      from: "2026-11-10T00:00:00Z",
      to: "2026-11-10T00:01:00Z",
      timezone: "UTC",
    },
    stepSeconds: 60,
    rows: [{ dimension: "2026-11-10T00:00:00Z", value: 123 }],
  };
  vi.mocked(getAssistantQueryResult).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: { data: { ...stored, result: trend }, meta: {}, requestId: "unit" },
  } as never);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const tree = (id: string) => (
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <QueryResultView id={id} answer={answer} />
      </MemoryRouter>
    </QueryClientProvider>
  );
  const view = render(tree("query-a"));
  fireEvent.click(
    await screen.findByLabelText("2026-11-10T00:00:00Z 123 cores"),
  );
  await screen.findByText("观测点与来源");
  view.rerender(tree("query-b"));
  await waitFor(() =>
    expect(getAssistantQueryResult).toHaveBeenCalledWith(
      "query-b",
      expect.anything(),
    ),
  );
  expect(screen.queryByText("观测点与来源")).toBeNull();
});

it("opens the focused fact of the initial deep link", async () => {
  vi.mocked(getAssistantQueryResult).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: {
      data: stored,
      queryScope: {
        clusterUid: "cluster-a",
        namespaces: ["apps"],
        canonicalIds: [],
        clusterScoped: false,
        filters: [{ field: "kind", operator: "eq", value: "Pod" }],
        filtersKnown: true,
      },
      meta: {},
      requestId: "unit",
    },
  } as never);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <QueryResultView
          id="query-result-1"
          answer={answer}
          focusFactId={stored.facts[0].factId}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await screen.findByText("查询口径与来源");
  expect(screen.getByText("已提交统计 Fact")).toBeInTheDocument();
});

it("copies the committed numbers of the turn and refuses a lost grant", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
  vi.mocked(getAssistantTurn).mockImplementation(async (turnId: string) =>
    turnResponse(turnId, ["query-result-1"]),
  );
  vi.mocked(getAssistantQueryResult).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: {
      data: stored,
      queryScope: {
        clusterUid: "cluster-a",
        namespaces: ["apps"],
        canonicalIds: [],
        clusterScoped: false,
        filters: [{ field: "kind", operator: "eq", value: "Pod" }],
        filtersKnown: true,
      },
      meta: {},
      requestId: "unit",
    },
  } as never);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const tree = (turn: string) => (
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <AssistantTurnActions
          answer={{ ...answer, queryResultRefs: ["query-result-1"] }}
          identity={{ conversationId: "conv-1", turnId: turn }}
        />
      </MemoryRouter>
    </QueryClientProvider>
  );
  const view = render(tree("turn-1"));
  fireEvent.click(screen.getByText("复制摘要"));
  await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
  const copied = writeText.mock.calls[0][0] as string;
  expect(copied).toContain("12");
  expect(copied).toContain("resources");
  expect(copied).toContain("query-result-1");
  expect(copied).toContain("模型解释");
  // A refused read writes nothing to the clipboard.
  writeText.mockClear();
  vi.mocked(getAssistantQueryResult).mockRejectedValue(
    new (await import("../src/api/client")).ApiError(
      403,
      "forbidden",
      "当前授权已失效",
    ),
  );
  view.rerender(tree("turn-2"));
  fireEvent.click(screen.getByText("复制摘要"));
  await waitFor(() => expect(getAssistantQueryResult).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(screen.getByText("复制摘要")).toBeInTheDocument());
  expect(writeText).not.toHaveBeenCalled();
});

it("copies the committed numbers of the turn and exports the payload with its fixed references", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
  const download = captureDownload();
  const referenced: AssistantAnswerV1 = {
    ...answer,
    queryResultRefs: ["query-result-1"],
    evidenceRefs: ["EV-6101"],
    entityRefs: ["pod/ns/gateway-api"],
    formalRefs: [
      {
        kind: "rca",
        incidentId: "INC-7",
        revision: 3,
        contentDigest: "sha256:" + "6".repeat(64),
      },
    ],
  };
  const fresh = turnResponse("turn-1", ["query-result-1"], referenced);
  vi.mocked(getAssistantTurn).mockResolvedValue(fresh);
  vi.mocked(getAssistantQueryResult).mockResolvedValue(storedResponse());
  renderTurn("turn-1", {
    ...referenced,
    evidenceRefs: ["stale-cached-evidence"],
  });
  fireEvent.click(screen.getByText("复制摘要"));
  await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
  const copied = writeText.mock.calls[0][0] as string;
  expect(copied).toContain("12");
  expect(copied).toContain("resources");
  expect(copied).toContain("query-result-1");
  expect(copied).toContain("模型解释");
  expect(await screen.findByText("已复制摘要")).toBeTruthy();
  // The export downloads the payload of the same authorized read.
  fireEvent.click(screen.getByText("导出结果"));
  await waitFor(() => expect(download.create).toHaveBeenCalledTimes(1));
  expect(download.click).toHaveBeenCalledTimes(1);
  const payload = JSON.parse(
    await readBlob(download.create.mock.calls[0][0] as Blob),
  );
  expect(payload.schemaVersion).toBe("assistant-turn-export/v1");
  expect(payload.turnId).toBe("turn-1");
  expect(payload.queryResultRefs).toEqual(["query-result-1"]);
  expect(payload.evidenceRefs).toEqual(["EV-6101"]);
  expect(payload.entityRefs).toEqual(["pod/ns/gateway-api"]);
  expect(payload.formalRefs).toEqual([
    {
      kind: "rca",
      incidentId: "INC-7",
      revision: 3,
      contentDigest: "sha256:" + "6".repeat(64),
    },
  ]);
  // The numbers are the committed ones, the model explanation is no number.
  expect(payload.results[0].rows).toEqual([
    { dimension: "normal", value: 12 },
    { dimension: "abnormal", value: 3 },
  ]);
  expect(payload.results[0].facts[0].value).toBe(3);
  expect(payload.results[0].facts[0].factId).toBe(stored.facts[0].factId);
  expect(payload.sourceStatement).toBe(turnSourceStatement);
  expect(payload.summary).toContain("保留已提交证据");
});

it("reads the turn before copying a diagnosis without query references and refuses a lost grant", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
  const download = captureDownload();
  vi.mocked(getAssistantTurn).mockRejectedValue(
    new ApiError(403, "forbidden", "当前授权已失效"),
  );
  const { client } = renderTurn("turn-1", { ...answer, queryResultRefs: [] });
  const remove = vi.spyOn(client, "removeQueries");
  const before = notices().length;
  fireEvent.click(screen.getByText("复制摘要"));
  await waitFor(() =>
    expect(getAssistantTurn).toHaveBeenCalledWith("turn-1", expect.anything()),
  );
  // The refused turn stops the action before any result is read.
  expect(getAssistantQueryResult).not.toHaveBeenCalled();
  expect(writeText).not.toHaveBeenCalled();
  expect(download.create).not.toHaveBeenCalled();
  await waitFor(() => expect(notices().length).toBeGreaterThan(before));
  expect(notices()[notices().length - 1].textContent).toContain(
    "当前授权已失效，未复制也未导出任何内容。",
  );
  // Only the cache of this scope is dropped.
  expect(remove).toHaveBeenCalledWith({
    queryKey: ["assistant", expect.stringContaining("tenant-fixture")],
  });
  // The export of the same refused turn downloads nothing.
  fireEvent.click(screen.getByText("导出结果"));
  await waitFor(() => expect(getAssistantTurn).toHaveBeenCalledTimes(2));
  expect(download.create).not.toHaveBeenCalled();
});

it("fails closed without the identity of the turn", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
  const download = captureDownload();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <AssistantTurnActions
          answer={answer}
          identity={{ conversationId: "c" }}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  const before = notices().length;
  fireEvent.click(screen.getByText("复制摘要"));
  await waitFor(() => expect(notices().length).toBeGreaterThan(before));
  expect(getAssistantTurn).not.toHaveBeenCalled();
  expect(writeText).not.toHaveBeenCalled();
  expect(download.create).not.toHaveBeenCalled();
});

it("keeps a late clipboard completion of an older result apart from the new result", async () => {
  let resolveWrite!: () => void;
  const pending = new Promise<void>((resolve) => {
    resolveWrite = resolve;
  });
  const writeText = vi.fn().mockReturnValue(pending);
  Object.assign(navigator, { clipboard: { writeText } });
  vi.mocked(getAssistantQueryResult).mockResolvedValue(storedResponse());
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const tree = (id: string) => (
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <QueryResultView id={id} answer={answer} />
      </MemoryRouter>
    </QueryClientProvider>
  );
  const view = render(tree("a"));
  fireEvent.click(await screen.findByText("复制摘要"));
  await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
  // A -> B -> A: the scope of the result repeats itself.
  view.rerender(tree("b"));
  await waitFor(() =>
    expect(getAssistantQueryResult).toHaveBeenCalledWith(
      "b",
      expect.anything(),
    ),
  );
  view.rerender(tree("a"));
  await act(async () => {
    resolveWrite();
    await pending;
  });
  expect(screen.queryByText("已复制摘要")).toBeNull();
});

it("shows no message and clears no cache when the refusal of an older turn arrives late", async () => {
  let rejectRead!: (reason: unknown) => void;
  const pending = new Promise((_, reject) => {
    rejectRead = reject;
  });
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
  vi.mocked(getAssistantTurn).mockReturnValue(pending as never);
  const { client, tree, view } = renderTurn("turn-1", {
    ...answer,
    queryResultRefs: ["query-result-1"],
  });
  const remove = vi.spyOn(client, "removeQueries");
  fireEvent.click(screen.getByText("复制摘要"));
  await waitFor(() => expect(getAssistantTurn).toHaveBeenCalledTimes(1));
  const before = notices().length;
  // The operator moves to another turn before the refusal arrives.
  view.rerender(tree("turn-2"));
  await act(async () => {
    rejectRead(new ApiError(403, "forbidden", "当前授权已失效"));
    await pending.catch(() => undefined);
  });
  expect(notices().length).toBe(before);
  expect(remove).not.toHaveBeenCalled();
  expect(writeText).not.toHaveBeenCalled();
});

it("keeps the new turn operable while the request of an older turn is pending", async () => {
  const first = new Promise(() => {});
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
  vi.mocked(getAssistantTurn)
    .mockImplementationOnce(() => first as never)
    .mockImplementation(async (turnId: string) =>
      turnResponse(turnId, ["query-result-1"]),
    );
  vi.mocked(getAssistantQueryResult).mockResolvedValue(storedResponse());
  const { tree, view } = renderTurn("turn-1", {
    ...answer,
    queryResultRefs: ["query-result-1"],
  });
  fireEvent.click(screen.getByText("复制摘要"));
  await waitFor(() => expect(getAssistantTurn).toHaveBeenCalledTimes(1));
  view.rerender(tree("turn-2"));
  // The new turn is not stuck in the loading state of the older turn.
  await waitFor(() =>
    expect(
      screen.getByText("复制摘要").closest("button")?.className,
    ).not.toContain("ant-btn-loading"),
  );
  fireEvent.click(screen.getByText("复制摘要"));
  await waitFor(() => expect(getAssistantTurn).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
  expect(screen.getByText("已复制摘要")).toBeTruthy();
});

it("drops the copied state of a turn when the account or the scope generation changes", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
  vi.mocked(getAssistantTurn).mockImplementation(async (turnId: string) =>
    turnResponse(turnId, ["query-result-1"]),
  );
  vi.mocked(getAssistantQueryResult).mockResolvedValue(storedResponse());
  const { tree, view } = renderTurn("turn-1", {
    ...answer,
    queryResultRefs: ["query-result-1"],
  });
  fireEvent.click(screen.getByText("复制摘要"));
  await waitFor(() => expect(screen.getByText("已复制摘要")).toBeTruthy());
  // Another account is another authorization context.
  fixtureState.session.subject = "operator-other";
  view.rerender(tree("turn-1"));
  expect(screen.queryByText("已复制摘要")).toBeNull();
  fireEvent.click(screen.getByText("复制摘要"));
  await waitFor(() => expect(screen.getByText("已复制摘要")).toBeTruthy());
  // Another scope generation is another authorization context as well.
  fixtureState.cluster.generation = 2;
  view.rerender(tree("turn-1"));
  expect(screen.queryByText("已复制摘要")).toBeNull();
});

it("passes the saved result constraint to the receiving resource SDK instead of broadening the list", async () => {
  vi.mocked(getAssistantQueryResult).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: {
      data: stored,
      queryScope: {
        clusterUid: "cluster-a",
        namespaces: ["apps"],
        canonicalIds: ["pod-a"],
        clusterScoped: false,
        filters: [{ field: "kind", operator: "eq", value: "Pod" }],
        filtersKnown: true,
      },
      requestId: "unit",
    },
  } as never);
  vi.mocked(getResources).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: { data: [], meta: {}, requestId: "unit" },
  } as never);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/assistant"]}>
        <Routes>
          <Route
            path="/assistant"
            element={<QueryResultView id="query-result-1" answer={answer} />}
          />
          <Route path="/resources" element={<ResourcesPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  fireEvent.click(
    await screen.findByRole("link", { name: "查看 normal 的当前记录" }),
  );
  await waitFor(() =>
    expect(getResources).toHaveBeenCalledWith(
      expect.objectContaining({
        clusterUid: "cluster-a",
        assistantQueryResultId: "query-result-1",
        assistantGroup: "normal",
        health: "normal",
      }),
      expect.anything(),
    ),
  );
});

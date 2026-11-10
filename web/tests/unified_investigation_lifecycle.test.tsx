// UI mechanism fixture; no database, browser visual or live authorization claim.
// The page is mounted for real: the detail, the stop confirmation and the
// scope change are the behaviour of the mounted components, not of a helper.
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useSearchParams } from "react-router-dom";
import { AssistantPage } from "../src/pages/AssistantPage";
import {
  cancelAssistantTurn,
  getAssistantCapabilities,
  getAssistantConversation,
  getAssistantTurn,
  getInvestigation,
  getInvestigationSteps,
  listAssistantConversations,
  listAssistantMessages,
} from "../src/api/generated/platform";
import { ApiError, setNativeCredentials, clearCredentials } from "../src/api/client";

const cluster = vi.hoisted(() => ({ scopeKey: "scope-a", generation: 1 }));
// The account of the page. A new account is a new authorization context.
const account = vi.hoisted(() => ({ subject: "operator-fixture" }));
vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: {
      tenantId: "tenant-fixture",
      subject: account.subject,
      roles: ["operator"],
      clusterScopes: [],
      namespaceScopes: [],
    },
  }),
}));
vi.mock("../src/app/ClusterContext", () => ({
  useOperationalCluster: () => ({
    clusterUid: "cluster-a",
    candidates: [],
    scopeKey: cluster.scopeKey,
    generation: cluster.generation,
  }),
  useClusterDraft: vi.fn(),
}));
vi.mock("../src/api/generated/platform", async (original) => ({
  ...(await original<object>()),
  getAssistantCapabilities: vi.fn(),
  getAssistantConversation: vi.fn(),
  getAssistantTurn: vi.fn(),
  getInvestigation: vi.fn(),
  getInvestigationSteps: vi.fn(),
  listAssistantConversations: vi.fn(),
  listAssistantMessages: vi.fn(),
  cancelAssistantTurn: vi.fn(),
}));

const response = (data: unknown, status = 200) => ({
  status,
  headers: new Headers(),
  data: { data, meta: {}, requestId: "unit" },
});
const conversation = {
  tenantId: "tenant-fixture",
  conversationId: "conversation-a",
  ownerSubject: "operator-fixture",
  title: "会话 A",
  mode: "troubleshoot",
  authorizationDomain: "operational",
  clusterUid: "cluster-a",
  binding: { timezone: "Asia/Shanghai" },
  revision: 3,
  activeTurn: {
    turnId: "turn-active",
    conversationId: "conversation-a",
    jobId: "job-active",
    state: "running",
    createdAt: "2026-11-09T00:00:00Z",
    conversationRevision: 3,
    budget: { toolCalls: 2, modelRequests: 1, durationSeconds: 120 },
  },
  createdAt: "2026-11-09T00:00:00Z",
  updatedAt: "2026-11-09T00:00:00Z",
};
const job = {
  schemaVersion: "investigation-job/v3",
  tenantId: "tenant-fixture",
  jobId: "job-active",
  incidentId: "incident-a",
  subject: "operator-fixture",
  triggerRevision: 1,
  triggerKind: "manual",
  policyVersion: "policy-1",
  scope: { clusterUid: "cluster-a" },
  effectiveScopeDigest: `sha256:${"a".repeat(64)}`,
  toolCatalogDigest: `sha256:${"b".repeat(64)}`,
  state: "running",
  budget: {},
  budgetReserved: {},
  budgetConsumed: {},
  focus: "内存通道",
  expiresAt: "2026-11-09T10:00:00Z",
  eventSeq: 1,
  nextCallSeq: 1,
  result: {},
  conversationId: "conversation-a",
  turnId: "turn-active",
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function renderPage(initial = "/assistant?conversation=conversation-a") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[initial]}>
        <AssistantPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...utils, client };
}

// A page that keeps its router across a scope change, so the address of the
// page really survives the change of the authorization context.
function renderLinkedPage(initial: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function NewLink() {
    const [, setSearch] = useSearchParams();
    return (
      <>
        <button
          onClick={() =>
            setSearch({
              conversation: "conversation-a",
              investigationJob: "job-other",
              investigationTurn: "turn-other",
            })
          }
        >
          新的详情链接
        </button>
        <button
          onClick={() =>
            setSearch({
              conversation: "conversation-b",
              investigationJob: "job-active",
              investigationTurn: "turn-active",
            })
          }
        >
          另一个会话的链接
        </button>
      </>
    );
  }
  const tree = () => (
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[initial]}>
        <NewLink />
        <AssistantPage />
      </MemoryRouter>
    </QueryClientProvider>
  );
  const utils = render(tree());
  return { ...utils, client, tree };
}

beforeEach(() => {
  vi.clearAllMocks();
  cluster.scopeKey = "scope-a";
  cluster.generation = 1;
  account.subject = "operator-fixture";
  setNativeCredentials("tenant-fixture", "fixture-csrf-key-0001");
  vi.mocked(getAssistantCapabilities).mockResolvedValue(
    response({ enabled: true, taskKinds: [], catalog: { datasets: [] } }) as never,
  );
  vi.mocked(listAssistantConversations).mockResolvedValue(
    response([conversation]) as never,
  );
  vi.mocked(getAssistantConversation).mockResolvedValue(
    response(conversation) as never,
  );
  vi.mocked(listAssistantMessages).mockResolvedValue(response([]) as never);
  vi.mocked(getAssistantTurn).mockResolvedValue(response({}) as never);
  vi.mocked(getInvestigation).mockResolvedValue(response(job) as never);
  vi.mocked(getInvestigationSteps).mockResolvedValue(response([]) as never);
  vi.mocked(cancelAssistantTurn).mockResolvedValue(
    response({ accepted: true }) as never,
  );
});
afterEach(() => {
  cleanup();
  clearCredentials();
});

// W2: the detail belongs to the scope that opened it. A scope or account
// change closes it at once.
it("closes the open detail when the scope changes", async () => {
  const { rerender } = renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  fireEvent.click(await screen.findByRole("button", { name: "查看调查过程" }));
  expect(await screen.findByText("真实状态")).toBeInTheDocument();
  // The scope of the page changes to another authorized scope.
  cluster.scopeKey = "scope-b";
  cluster.generation = 2;
  rerender(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <MemoryRouter initialEntries={["/assistant?conversation=conversation-a"]}>
        <AssistantPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(screen.queryByText("真实状态")).toBeNull());
});

// W2: the confirmation freezes its target. A repeated confirmation sends no
// second request, and the request carries one idempotency key.
it("sends exactly one stop request for the frozen confirmation", async () => {
  const pending = deferred<never>();
  vi.mocked(cancelAssistantTurn).mockReturnValue(pending.promise as never);
  renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  fireEvent.click(await screen.findByRole("button", { name: "查看调查过程" }));
  fireEvent.click(await screen.findByRole("button", { name: "停止调查" }));
  expect(await screen.findByText("确认停止本轮调查")).toBeInTheDocument();
  // The confirmation is opened above the open drawer. The drawer isolates the
  // rest of the document for assistive technology, so the button of the
  // confirmation is addressed by its text.
  fireEvent.click(screen.getByText("确认停止"));
  await waitFor(() => expect(cancelAssistantTurn).toHaveBeenCalledTimes(1));
  // A second confirmation while the first request is in flight is refused
  // synchronously and sends nothing.
  fireEvent.click(screen.getByText("确认停止"));
  expect(cancelAssistantTurn).toHaveBeenCalledTimes(1);
  const options = vi.mocked(cancelAssistantTurn).mock.calls[0][2] as RequestInit;
  const key = (options.headers as Record<string, string>)["Idempotency-Key"];
  expect(key).toBeTruthy();
  pending.resolve({
    status: 200,
    headers: new Headers(),
    data: { data: { accepted: true }, meta: {}, requestId: "unit" },
  } as never);
  await waitFor(() =>
    expect(screen.queryByText("确认停止本轮调查")).toBeNull(),
  );
});

// W2: a detail of another conversation is refused. The confirmation never
// falls back to the active turn of the page.
it("refuses to stop a detail that belongs to another conversation", async () => {
  vi.mocked(getInvestigation).mockResolvedValue(
    response({
      ...job,
      jobId: "job-other",
      turnId: "turn-other",
      conversationId: "conversation-b",
    }) as never,
  );
  renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  fireEvent.click(await screen.findByRole("button", { name: "查看调查过程" }));
  fireEvent.click(await screen.findByRole("button", { name: "停止调查" }));
  // No confirmation of another conversation is opened and nothing is cancelled.
  expect(screen.queryByText("确认停止本轮调查")).toBeNull();
  expect(cancelAssistantTurn).not.toHaveBeenCalled();
});

// W2: the confirmation is bound to the turn that was opened. A detail of an
// older turn of the same conversation stops exactly that turn and never the
// active turn of the page.
it("stops exactly the turn of the opened detail", async () => {
  vi.mocked(listAssistantMessages).mockResolvedValue(
    response([
      {
        role: "assistant",
        turnId: "turn-old",
        createdAt: "2026-11-09T00:00:00Z",
        content: { type: "text", text: "旧轮次" },
      },
    ]) as never,
  );
  vi.mocked(getInvestigation).mockResolvedValue(
    response({ ...job, jobId: "job-old", turnId: "turn-old" }) as never,
  );
  vi.mocked(getAssistantTurn).mockImplementation((turnId: string) =>
    Promise.resolve(
      response({
        turnId,
        conversationId: "conversation-a",
        jobId: "job-old",
        state: "running",
        createdAt: "2026-11-09T00:00:00Z",
        conversationRevision: 3,
      }) as never,
    ),
  );
  renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  const buttons = await screen.findAllByRole("button", { name: "查看调查过程" });
  expect(buttons.length).toBeGreaterThan(1);
  fireEvent.click(buttons[0]);
  fireEvent.click(await screen.findByRole("button", { name: "停止调查" }));
  expect(await screen.findByText("确认停止本轮调查")).toBeInTheDocument();
  fireEvent.click(screen.getByText("确认停止"));
  await waitFor(() => expect(cancelAssistantTurn).toHaveBeenCalledTimes(1));
  expect(vi.mocked(cancelAssistantTurn).mock.calls[0][0]).toBe("turn-old");
});

// W2: a late answer of an older scope never clears the state of the scope
// that is shown now.
it("does not let a late stop answer touch the state of the new scope", async () => {
  const pending = deferred<never>();
  vi.mocked(cancelAssistantTurn).mockReturnValue(pending.promise as never);
  const { rerender, client } = renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  fireEvent.click(await screen.findByRole("button", { name: "查看调查过程" }));
  fireEvent.click(await screen.findByRole("button", { name: "停止调查" }));
  fireEvent.click(await screen.findByRole("button", { name: "确认停止" }));
  await waitFor(() => expect(cancelAssistantTurn).toHaveBeenCalledTimes(1));
  const removeQueries = vi.spyOn(client, "removeQueries");
  cluster.scopeKey = "scope-b";
  cluster.generation = 2;
  rerender(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/assistant?conversation=conversation-a"]}>
        <AssistantPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(screen.queryByText("确认停止本轮调查")).toBeNull());
  pending.reject(new Error("旧作用域的拒绝"));
  await new Promise((resolve) => setTimeout(resolve, 30));
  // The refusal of the old scope does not report an error on the new scope
  // and does not drop the cache of the new scope.
  expect(screen.queryByText("旧作用域的拒绝")).toBeNull();
  expect(removeQueries).not.toHaveBeenCalledWith({
    queryKey: ["assistant", expect.stringContaining("scope-b")],
  });
});

// W4: the same-incident history opens the unified detail of the exact
// conversation, turn and job through a link.
it("opens the unified detail of the same-incident history link", async () => {
  renderPage(
    "/assistant?conversation=conversation-a&investigationJob=job-active&investigationTurn=turn-active",
  );
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  expect(await screen.findByText("真实状态")).toBeInTheDocument();
  expect(getInvestigation).toHaveBeenCalledWith(
    "job-active",
    expect.anything(),
  );
  // The detail of the linked job offers the stop of exactly that turn.
  expect(screen.getByRole("button", { name: "停止调查" })).toBeInTheDocument();
});

// W2: the link belongs to the authorization context that opened it. A scope
// that goes away and comes back must not reopen the old detail with the old
// address, and a normal new link still opens.
it("does not reopen the old detail after the scope went away and came back", async () => {
  const { rerender, tree } = renderLinkedPage(
    "/assistant?conversation=conversation-a&investigationJob=job-active&investigationTurn=turn-active",
  );
  expect(await screen.findByText("真实状态")).toBeInTheDocument();
  // The scope of the page changes to another authorized scope.
  cluster.scopeKey = "scope-b";
  cluster.generation = 2;
  rerender(tree());
  await waitFor(() => expect(screen.queryByText("真实状态")).toBeNull());
  const readsBeforeReturn = vi.mocked(getInvestigation).mock.calls.length;
  // The same scope comes back with a new generation.
  cluster.scopeKey = "scope-a";
  cluster.generation = 3;
  rerender(tree());
  await new Promise((resolve) => setTimeout(resolve, 200));
  // The old address must not open the old detail again, and it must not read
  // the old job again either.
  expect(screen.queryByText("真实状态")).toBeNull();
  expect(vi.mocked(getInvestigation).mock.calls.length).toBe(readsBeforeReturn);
  // A normal new link still opens the detail of exactly that job.
  fireEvent.click(screen.getByText("新的详情链接"));
  expect(await screen.findByText("真实状态")).toBeInTheDocument();
  expect(getInvestigation).toHaveBeenCalledWith("job-other", expect.anything());
});

// W2: a new account is a new authorization context. The old link must not
// open the old detail for the new account.
it("does not open the old detail for a new account", async () => {
  const { rerender, tree } = renderLinkedPage(
    "/assistant?conversation=conversation-a&investigationJob=job-active&investigationTurn=turn-active",
  );
  expect(await screen.findByText("真实状态")).toBeInTheDocument();
  account.subject = "operator-other";
  rerender(tree());
  await waitFor(() => expect(screen.queryByText("真实状态")).toBeNull());
  await new Promise((resolve) => setTimeout(resolve, 200));
  expect(screen.queryByText("真实状态")).toBeNull();
});

// W2: a new session of the same account closes the detail of the old session.
// The old link of the old session is not reopened by the new session.
it("does not open the old detail in a new session of the same account", async () => {
  renderLinkedPage(
    "/assistant?conversation=conversation-a&investigationJob=job-active&investigationTurn=turn-active",
  );
  expect(await screen.findByText("真实状态")).toBeInTheDocument();
  fireEvent.click(screen.getByText("另一个会话的链接"));
  await waitFor(() => expect(screen.queryByText("真实状态")).toBeNull());
  await new Promise((resolve) => setTimeout(resolve, 200));
  expect(screen.queryByText("真实状态")).toBeNull();
});

// W2: a late answer of an older scope must not close a confirmation that is
// busy in the scope that is shown now, and must not release its busy state.
it("keeps the new confirmation busy when the older answer settles late", async () => {
  const first = deferred<never>();
  const second = deferred<never>();
  let calls = 0;
  vi.mocked(cancelAssistantTurn).mockImplementation(() => {
    calls += 1;
    return (calls === 1 ? first : second).promise as never;
  });
  const { rerender, tree } = renderLinkedPage(
    "/assistant?conversation=conversation-a",
  );
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  fireEvent.click(await screen.findByRole("button", { name: "查看调查过程" }));
  fireEvent.click(await screen.findByRole("button", { name: "停止调查" }));
  fireEvent.click(screen.getByText("确认停止"));
  await waitFor(() => expect(cancelAssistantTurn).toHaveBeenCalledTimes(1));
  // The scope changes while the request is in flight.
  cluster.scopeKey = "scope-b";
  cluster.generation = 2;
  rerender(tree());
  await waitFor(() => expect(screen.queryByText("确认停止本轮调查")).toBeNull());
  // A new confirmation in the new scope starts a new request.
  fireEvent.click(await screen.findByRole("button", { name: "查看调查过程" }));
  fireEvent.click(await screen.findByRole("button", { name: "停止调查" }));
  fireEvent.click(screen.getByText("确认停止"));
  await waitFor(() => expect(cancelAssistantTurn).toHaveBeenCalledTimes(2));
  // The late answer of the older request closes nothing.
  first.resolve({
    status: 200,
    headers: new Headers(),
    data: { data: { accepted: true }, meta: {}, requestId: "unit" },
  } as never);
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(screen.queryByText("确认停止本轮调查")).not.toBeNull();
  // The new confirmation is still busy, so a repeated confirmation sends
  // nothing.
  fireEvent.click(screen.getByText("确认停止"));
  expect(cancelAssistantTurn).toHaveBeenCalledTimes(2);
});

// W2: a refused stop drops the cached data of the scope that was refused and
// is reported. A conflict is not retried.
it("drops the cache of the refused scope and never retries a conflict", async () => {
  vi.mocked(cancelAssistantTurn).mockRejectedValue(
    new ApiError(403, "FORBIDDEN", "没有此范围的访问权限"),
  );
  const { client } = renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  const removeQueries = vi.spyOn(client, "removeQueries");
  fireEvent.click(await screen.findByRole("button", { name: "查看调查过程" }));
  fireEvent.click(await screen.findByRole("button", { name: "停止调查" }));
  fireEvent.click(screen.getByText("确认停止"));
  await waitFor(() =>
    expect(removeQueries).toHaveBeenCalledWith({
      queryKey: ["assistant", expect.stringContaining("scope-a")],
    }),
  );
  // The refusal is reported once and is not retried.
  expect(await screen.findByText(/没有此范围的访问权限/)).toBeInTheDocument();
  expect(cancelAssistantTurn).toHaveBeenCalledTimes(1);
});

it("reports a conflict of the stop request without retrying it", async () => {
  vi.mocked(cancelAssistantTurn).mockRejectedValue(
    new ApiError(409, "CONFLICT", "版本冲突，请刷新后重新检查变更"),
  );
  const { client } = renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  const removeQueries = vi.spyOn(client, "removeQueries");
  fireEvent.click(await screen.findByRole("button", { name: "查看调查过程" }));
  fireEvent.click(await screen.findByRole("button", { name: "停止调查" }));
  fireEvent.click(screen.getByText("确认停止"));
  expect(await screen.findByText(/版本冲突/)).toBeInTheDocument();
  // A conflict is never retried with a new revision and never drops the
  // cache of the scope.
  expect(cancelAssistantTurn).toHaveBeenCalledTimes(1);
  expect(removeQueries).not.toHaveBeenCalled();
});

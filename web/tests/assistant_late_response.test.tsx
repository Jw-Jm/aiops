// UI mechanism fixture; no database, browser visual or live authorization claim.
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
afterEach(cleanup);
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { AssistantPage } from "../src/pages/AssistantPage";
import {
  getAssistantCapabilities,
  getAssistantConversation,
  getAssistantTurn,
  listAssistantConversations,
  listAssistantMessages,
  submitAssistantMessage,
} from "../src/api/generated/platform";
import { setNativeCredentials } from "../src/api/client";

vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: {
      tenantId: "tenant-fixture",
      subject: "operator-fixture",
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
    scopeKey: "scope-a",
    generation: 1,
  }),
  useClusterDraft: vi.fn(),
}));
vi.mock("../src/api/generated/platform", async (original) => ({
  ...(await original<object>()),
  getAssistantCapabilities: vi.fn(),
  getAssistantConversation: vi.fn(),
  getAssistantTurn: vi.fn(),
  listAssistantConversations: vi.fn(),
  listAssistantMessages: vi.fn(),
  submitAssistantMessage: vi.fn(),
}));

const response = (data: unknown, status = 200) => ({
  status,
  headers: new Headers(),
  data: { data, meta: {}, requestId: "unit" },
});
const conversationA = {
  tenantId: "tenant-fixture",
  conversationId: "conversation-a",
  ownerSubject: "operator-fixture",
  title: "会话 A",
  mode: "troubleshoot",
  authorizationDomain: "operational",
  clusterUid: "cluster-a",
  binding: { timezone: "Asia/Shanghai" },
  revision: 3,
  createdAt: "2026-11-09T00:00:00Z",
  updatedAt: "2026-11-09T00:00:00Z",
};
const conversationB = { ...conversationA, conversationId: "conversation-b", title: "会话 B", revision: 7 };

beforeEach(() => {
  vi.clearAllMocks();
  setNativeCredentials("tenant-fixture", "fixture-csrf-key-0001");
  vi.mocked(getAssistantCapabilities).mockResolvedValue(
    response({ enabled: true, taskKinds: [], catalog: { datasets: [] } }) as never,
  );
  vi.mocked(listAssistantConversations).mockResolvedValue(
    response([conversationA, conversationB]) as never,
  );
  vi.mocked(getAssistantConversation).mockImplementation((id: string) =>
    Promise.resolve(response(id === "conversation-b" ? conversationB : conversationA) as never),
  );
  vi.mocked(listAssistantMessages).mockResolvedValue(response([]) as never);
  vi.mocked(getAssistantTurn).mockResolvedValue(response({}) as never);
});

// A pending submit of one conversation must not touch the page of another
// conversation, and it must not clear a draft that the operator wrote in the
// meantime.
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
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[initial]}>
        <AssistantPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

async function typeDraft(value: string) {
  fireEvent.change(await screen.findByLabelText("问题"), { target: { value } });
}

it("does not clear the draft of another conversation when a late answer arrives", async () => {
  const pending = deferred<never>();
  vi.mocked(submitAssistantMessage).mockReturnValue(pending.promise as never);
  renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  await typeDraft("会话 A 的问题");
  fireEvent.change(await screen.findByLabelText("调查重点"), {
    target: { value: "会话 A 的重点" },
  });
  fireEvent.change(await screen.findByLabelText("工具调用上限"), {
    target: { value: "2" },
  });
  fireEvent.click((await screen.findByText(/发\s*送/)).closest("button") as HTMLElement);
  await waitFor(() => expect(submitAssistantMessage).toHaveBeenCalledTimes(1));

  // The operator leaves for another conversation and writes a new draft there.
  fireEvent.click((await screen.findByText("会话 B")).closest("a, button, div") as HTMLElement);
  await waitFor(() =>
    expect(vi.mocked(getAssistantConversation).mock.calls.some((c) => c[0] === "conversation-b")).toBe(
      true,
    ),
  );
  await typeDraft("会话 B 的新草稿");

  // The pending answer of the first conversation arrives late.
  pending.resolve({
    status: 200,
    headers: new Headers(),
    data: {
      data: {
        turnId: "turn-a",
        conversationId: "conversation-a",
        jobId: "job-a",
        state: "queued",
        createdAt: "2026-11-09T00:00:00Z",
        conversationRevision: 4,
        budget: { toolCalls: 2, modelRequests: 1, durationSeconds: 120 },
      },
      meta: {},
      requestId: "unit",
    },
  } as never);
  await waitFor(() => expect(submitAssistantMessage).toHaveBeenCalledTimes(1));
  await new Promise((resolve) => setTimeout(resolve, 20));

  // The draft, the focus and the narrowing of the shown conversation are
  // untouched, and no turn of the other conversation was introduced.
  expect((screen.getByLabelText("问题") as HTMLTextAreaElement).value).toBe("会话 B 的新草稿");
  expect((screen.getByLabelText("调查重点") as HTMLInputElement).value).toBe("");
  expect((screen.getByLabelText("工具调用上限") as HTMLInputElement).value).toBe("");
  expect(screen.queryByText(/本轮实际额度/)).toBeNull();
  expect(screen.queryByText("会话已被其他操作修改，请刷新后重试")).toBeNull();
});

it("refuses the answer of an older generation after the same conversation was shown again", async () => {
  const pending = deferred<never>();
  vi.mocked(submitAssistantMessage).mockReturnValue(pending.promise as never);
  renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  await typeDraft("会话 A 的问题");
  fireEvent.click((await screen.findByText(/发\s*送/)).closest("button") as HTMLElement);
  await waitFor(() => expect(submitAssistantMessage).toHaveBeenCalledTimes(1));

  // A -> B -> A: the operator returns to the first conversation and writes a
  // new draft there. The answer of the first request belongs to an older
  // generation of that conversation and must be refused.
  fireEvent.click((await screen.findByText("会话 B")).closest("a, button, div") as HTMLElement);
  await waitFor(() =>
    expect(vi.mocked(getAssistantConversation).mock.calls.some((c) => c[0] === "conversation-b")).toBe(
      true,
    ),
  );
  fireEvent.click((await screen.findByText("会话 A")).closest("a, button, div") as HTMLElement);
  await waitFor(() =>
    expect(vi.mocked(getAssistantConversation).mock.calls.filter((c) => c[0] === "conversation-a").length).toBe(
      2,
    ),
  );
  await typeDraft("回到 A 之后的新草稿");

  pending.resolve({
    status: 200,
    headers: new Headers(),
    data: {
      data: {
        turnId: "turn-a",
        conversationId: "conversation-a",
        jobId: "job-a",
        state: "queued",
        createdAt: "2026-11-09T00:00:00Z",
        conversationRevision: 4,
        budget: { toolCalls: 2, modelRequests: 1, durationSeconds: 120 },
      },
      meta: {},
      requestId: "unit",
    },
  } as never);
  await new Promise((resolve) => setTimeout(resolve, 20));

  expect((screen.getByLabelText("问题") as HTMLTextAreaElement).value).toBe("回到 A 之后的新草稿");
  expect(screen.queryByText(/本轮实际额度/)).toBeNull();
});

it("does not show the error of a request that belongs to another conversation", async () => {
  const pending = deferred<never>();
  vi.mocked(submitAssistantMessage).mockReturnValue(pending.promise as never);
  renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  await typeDraft("会话 A 的问题");
  fireEvent.click((await screen.findByText(/发\s*送/)).closest("button") as HTMLElement);
  await waitFor(() => expect(submitAssistantMessage).toHaveBeenCalledTimes(1));

  fireEvent.click((await screen.findByText("会话 B")).closest("a, button, div") as HTMLElement);
  await waitFor(() =>
    expect(vi.mocked(getAssistantConversation).mock.calls.some((c) => c[0] === "conversation-b")).toBe(
      true,
    ),
  );

  pending.reject(new Error("会话 A 的失败"));
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(screen.queryByText("会话 A 的失败")).toBeNull();
});

it("clears the cached conversation of the scope when the grant is gone", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const removeQueries = vi.spyOn(client, "removeQueries");
  vi.mocked(submitAssistantMessage).mockResolvedValue({
    status: 403,
    headers: new Headers(),
    data: { code: "FORBIDDEN", message: "denied", requestId: "unit" },
  } as never);
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/assistant?conversation=conversation-a"]}>
        <AssistantPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  await typeDraft("统计失败操作");
  fireEvent.click((await screen.findByText(/发\s*送/)).closest("button") as HTMLElement);
  await waitFor(() => expect(screen.getByText("当前授权已变化，请刷新后重试")).toBeTruthy());
  // The cache of the scope was really dropped, not only a message shown.
  expect(removeQueries).toHaveBeenCalledWith({
    queryKey: ["assistant", expect.stringContaining("scope-a")],
  });
});

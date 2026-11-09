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
const conversation = {
  tenantId: "tenant-fixture",
  conversationId: "conversation-1",
  ownerSubject: "operator-fixture",
  title: "生产 Pod 排查",
  mode: "troubleshoot",
  authorizationDomain: "operational",
  clusterUid: "cluster-a",
  binding: { timezone: "Asia/Shanghai" },
  revision: 3,
  createdAt: "2026-11-09T00:00:00Z",
  updatedAt: "2026-11-09T00:00:00Z",
};

beforeEach(() => {
  vi.clearAllMocks();
  setNativeCredentials("tenant-fixture", "fixture-csrf-key-0001");
  vi.mocked(getAssistantCapabilities).mockResolvedValue(
    response({ enabled: true, taskKinds: [], catalog: { datasets: [] } }) as never,
  );
  vi.mocked(listAssistantConversations).mockResolvedValue(response([conversation]) as never);
  vi.mocked(getAssistantConversation).mockResolvedValue(response(conversation) as never);
  vi.mocked(listAssistantMessages).mockResolvedValue(response([]) as never);
  vi.mocked(getAssistantTurn).mockResolvedValue(response({}) as never);
  vi.mocked(submitAssistantMessage).mockResolvedValue(
    response({
      turnId: "turn-1",
      conversationId: "conversation-1",
      jobId: "job-1",
      state: "queued",
      createdAt: "2026-11-09T00:00:00Z",
      conversationRevision: 4,
      focus: "只看失败操作",
      budget: { toolCalls: 2, modelRequests: 1, durationSeconds: 120 },
    }) as never,
  );
});

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/assistant?conversation=conversation-1"]}>
        <AssistantPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

async function sendWithFocus() {
  renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  fireEvent.change(await screen.findByLabelText("问题"), {
    target: { value: "统计失败操作" },
  });
  fireEvent.change(await screen.findByLabelText("调查重点"), {
    target: { value: "只看失败操作" },
  });
  fireEvent.change(await screen.findByLabelText("工具调用上限"), {
    target: { value: "2" },
  });
  fireEvent.click((await screen.findByText(/发\s*送/)).closest("button") as HTMLElement);
}

it("submits the focus and the requested narrowing with the frozen revision", async () => {
  await sendWithFocus();
  await waitFor(() =>
    expect(submitAssistantMessage).toHaveBeenCalledWith(
      "conversation-1",
      expect.objectContaining({
        message: "统计失败操作",
        expectedConversationRevision: 3,
        focus: "只看失败操作",
        budgetTightening: expect.objectContaining({ toolCalls: 2 }),
      }),
      expect.anything(),
    ),
  );
});

it("shows the applied budget of the turn instead of the requested one", async () => {
  await sendWithFocus();
  await waitFor(() => expect(screen.getByText("本轮实际额度")).toBeTruthy());
  // The echo comes from the server, not from the requested narrowing.
  expect(screen.getByText(/工具调用 2 · 模型请求 1 · 时长 120 秒/)).toBeTruthy();
  expect(screen.getByText(/重点：只看失败操作/)).toBeTruthy();
});

it("reports a version conflict instead of retrying with a new revision", async () => {
  vi.mocked(submitAssistantMessage).mockResolvedValue({
    status: 409,
    headers: new Headers(),
    data: { code: "STALE_CONVERSATION", message: "stale", requestId: "unit" },
  } as never);
  await sendWithFocus();
  await waitFor(() =>
    expect(screen.getByText("会话已被其他操作修改，请刷新后重试")).toBeTruthy(),
  );
  // The page did not pick a new revision and resubmitted on its own.
  expect(vi.mocked(submitAssistantMessage).mock.calls.length).toBe(1);
});

it("clears the cached conversation when the grant is gone", async () => {
  vi.mocked(submitAssistantMessage).mockResolvedValue({
    status: 403,
    headers: new Headers(),
    data: { code: "FORBIDDEN", message: "denied", requestId: "unit" },
  } as never);
  await sendWithFocus();
  await waitFor(() =>
    expect(screen.getByText("当前授权已变化，请刷新后重试")).toBeTruthy(),
  );
});

// UI mechanism fixture; no database, browser visual or live authorization claim.
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
afterEach(cleanup);
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useLocation } from "react-router-dom";
import { AssistantPage } from "../src/pages/AssistantPage";
import {
  createAssistantConversation,
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
  createAssistantConversation: vi.fn(),
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
    response({
      enabled: true,
      taskKinds: [],
      catalog: { datasets: [] },
    }) as never,
  );
  vi.mocked(listAssistantConversations).mockResolvedValue(
    response([conversation]) as never,
  );
  vi.mocked(getAssistantConversation).mockResolvedValue(
    response(conversation) as never,
  );
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
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
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
  fireEvent.click(
    (await screen.findByText(/发\s*送/)).closest("button") as HTMLElement,
  );
}

const safeAnswer = {
  schemaVersion: "assistant-answer/v1",
  answerKind: "diagnosis",
  summary: "已提交证据",
  asOf: "2026-11-10T00:00:00Z",
  scopeSummary: "cluster-a",
  partial: true,
  factRefs: [],
  queryResultRefs: [],
  entityRefs: [],
  evidenceRefs: [],
  hypotheses: [],
  warnings: [],
  degradedSources: [],
  missingEvidence: [],
  nextChecks: [],
};
function LocationProbe() {
  const l = useLocation();
  return <output data-testid="location">{l.pathname + l.search}</output>;
}
function mountEntry(url: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[url]}>
        <AssistantPage />
        <LocationProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
function completed() {
  vi.mocked(getAssistantConversation).mockResolvedValue(
    response({
      ...conversation,
      binding: { timezone: "UTC", incidentId: "incident-a" },
      activeTurn: {
        turnId: "old-turn",
        conversationId: "conversation-1",
        jobId: "old-job",
        state: "partial",
        createdAt: "2026-11-10T00:00:00Z",
        conversationRevision: 3,
        answer: safeAnswer,
      },
    }) as never,
  );
  vi.mocked(listAssistantMessages).mockResolvedValue(
    response([
      {
        turnId: "old-turn",
        role: "user",
        sequence: 1,
        content: { text: "原问题与固定范围" },
      },
    ]) as never,
  );
}
it("submits the exact observation instants and canonical identity from a deep link", async () => {
  vi.mocked(getAssistantCapabilities).mockResolvedValue(
    response({
      enabled: true,
      taskKinds: ["resource_troubleshooting"],
      catalog: { datasets: [] },
    }) as never,
  );
  vi.mocked(createAssistantConversation).mockResolvedValue(
    response(conversation, 201) as never,
  );
  const to = new Date(Date.now() - 1000).toISOString();
  const from = new Date(Date.parse(to) - 1234567).toISOString();
  const canonical = "k8s+v1://tenant-fixture/cluster-a/core/Pod/default/web";
  mountEntry(
    "/assistant?" +
      new URLSearchParams({
        mode: "troubleshoot",
        canonicalId: canonical,
        from,
        to,
      }),
  );
  const button = await screen.findByRole("button", { name: "创建会话" });
  await waitFor(() => expect(button).not.toBeDisabled());
  fireEvent.click(button);
  await waitFor(() =>
    expect(createAssistantConversation).toHaveBeenCalledTimes(1),
  );
  expect(vi.mocked(createAssistantConversation).mock.calls[0][0]).toEqual(
    expect.objectContaining({
      clusterUid: "cluster-a",
      resourceCanonicalIds: [canonical],
      timeRange: { from, to, timezone: "Asia/Shanghai" },
    }),
  );
});
it("fallback answer suggestions only fill a draft and manual entry preserves the incident", async () => {
  completed();
  mountEntry("/assistant?conversation=conversation-1");
  fireEvent.click(await screen.findByText("按健康状态统计当前资源"));
  expect(screen.getByLabelText("问题")).toHaveValue(
    "按健康状态统计当前资源查看授权资源的当前分布。",
  );
  expect(submitAssistantMessage).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText("转入人工处置"));
  await waitFor(() =>
    expect(screen.getByTestId("location").textContent).toBe(
      "/events/incidents/incident-a?tab=action&clusterUid=cluster-a",
    ),
  );
  expect(submitAssistantMessage).not.toHaveBeenCalled();
});
it("explicit retry submits the original question with one new key and never auto-retries a conflict", async () => {
  completed();
  vi.mocked(submitAssistantMessage).mockResolvedValue({
    status: 409,
    headers: new Headers(),
    data: { code: "CONFLICT", message: "stale", requestId: "fixture" },
  } as never);
  mountEntry("/assistant?conversation=conversation-1");
  fireEvent.click(await screen.findByText("重新提问"));
  await waitFor(() => expect(submitAssistantMessage).toHaveBeenCalledTimes(1));
  const call = vi.mocked(submitAssistantMessage).mock.calls[0];
  expect(call[0]).toBe("conversation-1");
  expect(call[1]).toEqual(
    expect.objectContaining({
      message: "原问题与固定范围",
      parentTurnId: "old-turn",
      expectedConversationRevision: 3,
    }),
  );
  expect(
    (call[2]?.headers as Record<string, string>)["Idempotency-Key"],
  ).toBeTruthy();
  await new Promise((r) => setTimeout(r, 50));
  expect(submitAssistantMessage).toHaveBeenCalledTimes(1);
});

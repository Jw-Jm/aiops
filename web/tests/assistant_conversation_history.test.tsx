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
  renameAssistantConversation,
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
  renameAssistantConversation: vi.fn(),
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
  mode: "data_query",
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
  vi.mocked(getAssistantCapabilities).mockResolvedValue(response({ enabled: true, taskKinds: [], catalog: { datasets: [] } }) as never);
  vi.mocked(listAssistantConversations).mockResolvedValue(response([conversation]) as never);
  vi.mocked(getAssistantConversation).mockResolvedValue(response(conversation) as never);
  vi.mocked(listAssistantMessages).mockResolvedValue(response([]) as never);
  vi.mocked(getAssistantTurn).mockResolvedValue(response({}) as never);
  vi.mocked(renameAssistantConversation).mockResolvedValue(response({ ...conversation, title: "新的会话名", revision: 4 }) as never);
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

it("searches on the server and never filters the loaded page in the browser", async () => {
  renderPage();
  await waitFor(() => expect(listAssistantConversations).toHaveBeenCalled());
  vi.mocked(listAssistantConversations).mockClear();
  fireEvent.change(await screen.findByLabelText("搜索会话"), { target: { value: "Pod" } });
  await waitFor(
    () =>
      expect(listAssistantConversations).toHaveBeenCalledWith(
        expect.objectContaining({ q: "Pod", archived: "exclude" }),
        expect.anything(),
      ),
    { timeout: 2000 },
  );
  // The page keeps only what the server returned.
  expect(screen.getAllByText("生产 Pod 排查").length).toBeGreaterThan(0);
});

it("renames through the server with the current revision and reports a version conflict", async () => {
  renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  fireEvent.click((await screen.findByText(/改\s*名/)).closest("button") as HTMLElement);
  const field = await screen.findByLabelText("会话名称");
  fireEvent.change(field, { target: { value: "新的会话名" } });
  fireEvent.click((await screen.findByText(/保\s*存/)).closest("button") as HTMLElement);
  await waitFor(() =>
    expect(renameAssistantConversation).toHaveBeenCalledWith(
      "conversation-1",
      { title: "新的会话名", expectedConversationRevision: 3 },
      expect.anything(),
    ),
  );

  vi.mocked(renameAssistantConversation).mockResolvedValue({
    status: 409,
    headers: new Headers(),
    data: { code: "STALE_CONVERSATION", message: "stale", requestId: "unit" },
  } as never);
  fireEvent.click((await screen.findByText(/改\s*名/)).closest("button") as HTMLElement);
  fireEvent.change(await screen.findByLabelText("会话名称"), { target: { value: "再次改名" } });
  fireEvent.click((await screen.findByText(/保\s*存/)).closest("button") as HTMLElement);
  await waitFor(() =>
    expect(screen.getByText("会话已被其他操作修改，请刷新后重试")).toBeTruthy(),
  );
});

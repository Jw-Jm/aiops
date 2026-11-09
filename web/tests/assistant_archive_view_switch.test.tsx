// UI mechanism fixture; no database, browser visual or live authorization
// claim. The case below pins the archive view switch: the selected archive
// state is sent to the server, the browser never filters an already fetched
// page on its own.
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
  vi.mocked(getAssistantCapabilities).mockResolvedValue(
    response({ enabled: true, taskKinds: [], catalog: { datasets: [] } }) as never,
  );
  vi.mocked(listAssistantConversations).mockResolvedValue(
    response([conversation]) as never,
  );
  vi.mocked(getAssistantConversation).mockResolvedValue(response(conversation) as never);
  vi.mocked(listAssistantMessages).mockResolvedValue(response([]) as never);
  vi.mocked(getAssistantTurn).mockResolvedValue(response({}) as never);
});

it("sends the selected archive state to the server", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/assistant"]}>
        <AssistantPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(listAssistantConversations).toHaveBeenCalled());
  const selector = (await screen.findByRole("combobox", { name: "归档范围" })) as HTMLElement;
  // The antd select is driven through its own container: the dropdown is
  // rendered in a portal and the option is selected by its title.
  const container = (selector.closest(".ant-select") ?? selector.parentElement) as HTMLElement;
  fireEvent.mouseDown(container);
  const option = await screen.findByTitle("已归档");
  fireEvent.click(option);
  await waitFor(
    () =>
      expect(
        vi.mocked(listAssistantConversations).mock.calls.some(
          ([params]) => (params as { archived?: string }).archived === "only",
        ),
      ).toBe(true),
    { timeout: 2000 },
  );
});

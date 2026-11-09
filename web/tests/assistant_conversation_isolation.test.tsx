// UI mechanism fixture; no database, browser visual or live authorization claim.
// The cases below pin the scope and target isolation of the conversation
// management actions: a rename, an archive confirmation and a server side
// search must never act on, or leak into, another scope, another account or
// another conversation.
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
afterEach(cleanup);
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { AssistantPage } from "../src/pages/AssistantPage";
import {
  archiveAssistantConversation,
  getAssistantCapabilities,
  getAssistantConversation,
  getAssistantTurn,
  listAssistantConversations,
  listAssistantMessages,
  renameAssistantConversation,
} from "../src/api/generated/platform";
import { ApiError, setNativeCredentials } from "../src/api/client";

const cluster = vi.hoisted(() => ({ scopeGeneration: "scope-a", generation: 1 }));

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
    scopeKey: cluster.scopeGeneration,
    generation: cluster.generation,
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
  archiveAssistantConversation: vi.fn(),
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
const other = { ...conversation, conversationId: "conversation-2", title: "节点磁盘告警" };

beforeEach(() => {
  vi.clearAllMocks();
  cluster.scopeGeneration = "scope-a";
  cluster.generation = 1;
  setNativeCredentials("tenant-fixture", "fixture-csrf-key-0001");
  vi.mocked(getAssistantCapabilities).mockResolvedValue(
    response({ enabled: true, taskKinds: [], catalog: { datasets: [] } }) as never,
  );
  vi.mocked(listAssistantConversations).mockResolvedValue(
    response([conversation, other]) as never,
  );
  vi.mocked(getAssistantConversation).mockResolvedValue(response(conversation) as never);
  vi.mocked(listAssistantMessages).mockResolvedValue(response([]) as never);
  vi.mocked(getAssistantTurn).mockResolvedValue(response({}) as never);
  vi.mocked(renameAssistantConversation).mockResolvedValue(
    response({ ...conversation, title: "新的会话名", revision: 4 }) as never,
  );
  vi.mocked(archiveAssistantConversation).mockResolvedValue(
    response({ archived: true }) as never,
  );
});

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const tree = (c: QueryClient) => (
    <QueryClientProvider client={c}>
      <MemoryRouter initialEntries={["/assistant?conversation=conversation-1"]}>
        <AssistantPage />
      </MemoryRouter>
    </QueryClientProvider>
  );
  const view = render(tree(client));
  return { client, rerenderTree: () => view.rerender(tree(client)), ...view };
}

const openRename = async () => {
  fireEvent.click((await screen.findByText(/改\s*名/)).closest("button") as HTMLElement);
  return screen.findByLabelText("会话名称");
};

it("freezes the rename target and revision when the dialog opens", async () => {
  const { client } = renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  const field = await openRename();
  expect((field as HTMLInputElement).value).toBe("生产 Pod 排查");
  // A background refresh raises the revision of the conversation.
  vi.mocked(getAssistantConversation).mockResolvedValue(
    response({ ...conversation, revision: 5 }) as never,
  );
  await client.invalidateQueries({ queryKey: ["assistant"] });
  await waitFor(() =>
    expect(vi.mocked(getAssistantConversation).mock.results.length).toBeGreaterThan(0),
  );
  fireEvent.change(field, { target: { value: "新的会话名" } });
  fireEvent.click((await screen.findByText(/保\s*存/)).closest("button") as HTMLElement);
  await waitFor(() =>
    expect(renameAssistantConversation).toHaveBeenCalledWith(
      "conversation-1",
      { title: "新的会话名", expectedConversationRevision: 3 },
      expect.anything(),
    ),
  );
  // The newer revision of the background refresh is not used to overwrite the
  // change of somebody else.
  expect(vi.mocked(renameAssistantConversation).mock.calls[0][1]).toEqual({
    title: "新的会话名",
    expectedConversationRevision: 3,
  });
});

it("drops the rename draft and the pending rename when the scope changes", async () => {
  let settle: (() => void) | undefined;
  vi.mocked(renameAssistantConversation).mockImplementation(
    () =>
      new Promise((resolve) => {
        settle = () => resolve(response({ ...conversation, revision: 4 }) as never);
      }),
  );
  const { rerenderTree } = renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  const field = await openRename();
  fireEvent.change(field, { target: { value: "草稿标题" } });
  fireEvent.click((await screen.findByText(/保\s*存/)).closest("button") as HTMLElement);
  await waitFor(() => expect(renameAssistantConversation).toHaveBeenCalledTimes(1));
  // The scope changes while the request is in flight.
  cluster.scopeGeneration = "scope-b";
  rerenderTree();
  settle?.();
  await waitFor(() => expect(screen.queryByText(/失败|错误/)).toBeNull());
  // The late response of the previous scope does not reopen the dialog, does
  // not report an error on the new scope and is not sent again.
  expect(renameAssistantConversation).toHaveBeenCalledTimes(1);
  // The draft of the previous scope is gone: the reopened dialog shows the
  // current name of the conversation.
  const reopened = await openRename();
  expect((reopened as HTMLInputElement).value).toBe("生产 Pod 排查");
});

it("reports a version conflict without retrying against a newer revision", async () => {
  vi.mocked(renameAssistantConversation).mockResolvedValue({
    status: 409,
    headers: new Headers(),
    data: { code: "STALE_CONVERSATION", message: "stale", requestId: "unit" },
  } as never);
  renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  const field = await openRename();
  fireEvent.change(field, { target: { value: "新的会话名" } });
  fireEvent.click((await screen.findByText(/保\s*存/)).closest("button") as HTMLElement);
  await waitFor(() =>
    expect(screen.getByText("会话已被其他操作修改，请刷新后重试")).toBeTruthy(),
  );
  // The conflict is not resolved by silently taking the newest revision.
  expect(renameAssistantConversation).toHaveBeenCalledTimes(1);
  expect(vi.mocked(renameAssistantConversation).mock.calls[0][1]).toEqual({
    title: "新的会话名",
    expectedConversationRevision: 3,
  });
});

it("closes the rename dialog when another conversation is opened", async () => {
  renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  const field = await openRename();
  fireEvent.change(field, { target: { value: "草稿标题" } });
  fireEvent.click((await screen.findByText("节点磁盘告警")).closest("button") as HTMLElement);
  await waitFor(() =>
    expect(
      vi.mocked(getAssistantConversation).mock.calls.some(
        ([conversationId]) => conversationId === "conversation-2",
      ),
    ).toBe(true),
  );
  // The draft of the previous conversation is not kept.
  const reopened = await openRename();
  expect((reopened as HTMLInputElement).value).toBe("生产 Pod 排查");
});

it("keeps the search editable and the archive view switchable when the search failed", async () => {
  vi.mocked(listAssistantConversations).mockRejectedValue(
    new ApiError(500, "INVESTIGATION_UNAVAILABLE", "boom"),
  );
  renderPage();
  await waitFor(() => expect(listAssistantConversations).toHaveBeenCalled());
  // The failed search still shows the filter controls.
  const search = await screen.findByLabelText("搜索会话");
  fireEvent.change(search, { target: { value: "Pod" } });
  // The archive view control stays available while the search failed.
  expect(screen.getByRole("combobox", { name: "归档范围" })).toBeTruthy();
});

it("drops the cursor of another filter instead of paging with a foreign cursor", async () => {
  vi.mocked(listAssistantConversations).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: { data: [conversation], meta: { nextCursor: "cursor-2" }, requestId: "unit" },
  } as never);
  renderPage();
  await waitFor(() => expect(listAssistantConversations).toHaveBeenCalled());
  fireEvent.click((await screen.findByText(/下一页/)).closest("button") as HTMLElement);
  await waitFor(() =>
    expect(
      vi.mocked(listAssistantConversations).mock.calls.some(
        ([params]) => (params as { cursor?: string }).cursor === "cursor-2",
      ),
    ).toBe(true),
  );
  vi.mocked(listAssistantConversations).mockClear();
  fireEvent.change(await screen.findByLabelText("搜索会话"), { target: { value: "Pod" } });
  await waitFor(
    () =>
      expect(
        vi.mocked(listAssistantConversations).mock.calls.some(
          ([params]) =>
            (params as { q?: string; cursor?: string }).q === "Pod" &&
            (params as { cursor?: string }).cursor === undefined,
        ),
      ).toBe(true),
    { timeout: 2000 },
  );
});

const openArchiveConfirm = async () => {
  fireEvent.click((await screen.findByText(/^归\s*档$/)).closest("button") as HTMLElement);
  return waitFor(() => {
    const node = document.querySelector(".ant-popover") as HTMLElement | null;
    if (!node) throw new Error("popover not open");
    return node;
  });
};

it("archives the confirmed conversation", async () => {
  renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  const popover = await openArchiveConfirm();
  fireEvent.click(within(popover).getByRole("button", { name: /归\s*档/ }));
  await waitFor(() =>
    expect(archiveAssistantConversation).toHaveBeenCalledWith(
      "conversation-1",
      {},
      expect.anything(),
    ),
  );
});

it("does not archive another conversation when the confirmation was opened before a navigation", async () => {
  renderPage();
  await waitFor(() => expect(getAssistantConversation).toHaveBeenCalled());
  const popover = await openArchiveConfirm();
  // The navigation happens while the confirmation is open.
  fireEvent.click((await screen.findByText("节点磁盘告警")).closest("button") as HTMLElement);
  fireEvent.click(within(popover).getByRole("button", { name: /归\s*档/ }));
  await waitFor(() => expect(archiveAssistantConversation).not.toHaveBeenCalled());
});

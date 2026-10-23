import { beforeEach, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider, useLocation, useNavigate } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AppShell } from "../../src/layout/AppShell";
import { AssistantPage } from "../../src/pages/AssistantPage";
import { setCredentials } from "../../src/api/client";
import { streamJSONEvents } from "../../src/api/sse-reader";

const auth = vi.hoisted(() => ({ roles: ["operator"] as string[] }));
const api = vi.hoisted(() => ({
  clusters: vi.fn(),
  capabilities: vi.fn(),
  conversations: vi.fn(),
  conversation: vi.fn(),
  messages: vi.fn(),
  turn: vi.fn(),
  create: vi.fn(),
  submit: vi.fn(),
  resolve: vi.fn(),
  interpret: vi.fn(),
  archive: vi.fn(),
  cancel: vi.fn(),
}));

vi.mock("../../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: {
      tenantId: "tenant",
      subject: "operator-a",
      roles: auth.roles,
      authorizedClusters: [
        { clusterId: "id-a", clusterUid: "uid-a", displayName: "集群 A" },
        { clusterId: "id-b", clusterUid: "uid-b", displayName: "集群 B" },
      ],
      clusterScopes: ["uid-a", "uid-b"],
      namespaceScopes: [],
    },
    user: { profile: { preferred_username: "operator-a" } },
    loading: false,
  }),
}));
vi.mock("../../src/auth/oidc", () => ({
  getManager: async () => ({ signoutRedirect: async () => {} }),
}));
vi.mock("../../src/api/generated/platform", () => ({
  getClusters: api.clusters,
  getAssistantCapabilities: api.capabilities,
  listAssistantConversations: api.conversations,
  getAssistantConversation: api.conversation,
  listAssistantMessages: api.messages,
  getAssistantTurn: api.turn,
  createAssistantConversation: api.create,
  submitAssistantMessage: api.submit,
  resolveAssistantResource: api.resolve,
  interpretAssistantSymptom: api.interpret,
  archiveAssistantConversation: api.archive,
  cancelAssistantTurn: api.cancel,
  getGetAssistantConversationEventsUrl: (id: string) =>
    `/api/v1/assistant/conversations/${id}/events`,
}));
vi.mock("../../src/api/sse-reader", async (original) => ({
  ...(await original<object>()),
  streamJSONEvents: vi.fn(),
}));

const response = (status: number, body: unknown) => ({
  status,
  headers: new Headers(),
  data: body,
});
const envelope = (data: unknown, meta: Record<string, unknown> = {}) => ({
  data,
  meta,
});
const capabilities = {
  enabled: true,
  taskKinds: ["data_query", "incident_followup", "resource_troubleshooting"],
  catalog: { datasets: [] },
  sourceCapabilities: [],
  networkCapabilities: [],
  faultChainEnabled: false,
};
const successConversation = (id: string) =>
  response(201, envelope({ conversationId: id }));

function LocationProbe() {
  const location = useLocation();
  return (
    <span data-testid="location">
      {location.pathname + location.search + location.hash}
    </span>
  );
}

function NavigationProbe({ target }: { target: string }) {
  const navigate = useNavigate();
  return (
    <button onClick={() => navigate(target)}>navigate-target</button>
  );
}

function AssistantHarness({ target }: { target: string }) {
  return (
    <>
      <LocationProbe />
      <NavigationProbe target={target} />
      <AssistantPage />
    </>
  );
}

function renderShell(initial: string, target: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const router = createMemoryRouter(
    [
      {
        path: "/",
        element: <AppShell />,
        children: [
          { path: "assistant", element: <AssistantHarness target={target} /> },
          { path: "overview", element: <div>overview</div> },
        ],
      },
    ],
    { initialEntries: [initial] },
  );
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { client, router, unmount: view.unmount };
}

beforeEach(() => {
  setCredentials("fixture", "tenant");
  auth.roles = ["operator"];
  vi.mocked(streamJSONEvents).mockReset();
  api.clusters.mockResolvedValue(response(200, envelope([])));
  api.capabilities.mockResolvedValue(response(200, envelope(capabilities)));
  api.conversations.mockResolvedValue(response(200, envelope([])));
  api.conversation.mockReset();
  api.messages.mockReset();
  api.turn.mockReset();
  api.create.mockReset();
  api.submit.mockReset();
  api.resolve.mockReset();
  api.interpret.mockReset();
  api.archive.mockReset();
  api.cancel.mockReset();
});

it("keeps the full committed pathname, search, hash, scope and draft when a dirty cross-cluster navigation is cancelled", async () => {
  renderShell(
    "/assistant?clusterUid=uid-a&canonicalId=old-object#old-anchor",
    "/assistant?clusterUid=uid-b&canonicalId=new-object#new-anchor",
  );
  const draft = await screen.findByPlaceholderText("描述现象");
  fireEvent.change(draft, { target: { value: "old draft text" } });
  fireEvent.click(screen.getByText("navigate-target"));
  expect(await screen.findByText("切换运行集群？")).toBeInTheDocument();
  expect(screen.getByTestId("location")).toHaveTextContent(
    "/assistant?clusterUid=uid-a&canonicalId=old-object#old-anchor",
  );
  expect(screen.getByPlaceholderText("描述现象")).toHaveValue("old draft text");
  fireEvent.click(screen.getByText("保留当前范围"));
  expect(screen.getByTestId("location")).toHaveTextContent(
    "/assistant?clusterUid=uid-a&canonicalId=old-object#old-anchor",
  );
  expect(screen.getByPlaceholderText("描述现象")).toHaveValue("old draft text");
});

it("commits the confirmed deep link with pathname, search and hash, clears the old draft, and sends the new scope object", async () => {
  api.create.mockResolvedValueOnce(successConversation("conversation-new"));
  renderShell(
    "/assistant?clusterUid=uid-a&canonicalId=old-object#old-anchor",
    "/assistant?clusterUid=uid-b&canonicalId=new-object#new-anchor",
  );
  const draft = await screen.findByPlaceholderText("描述现象");
  fireEvent.change(draft, { target: { value: "old draft text" } });
  fireEvent.click(screen.getByText("navigate-target"));
  fireEvent.click(await screen.findByText("丢弃草稿并切换"));
  await waitFor(() =>
    expect(screen.getByTestId("location")).toHaveTextContent(
      "/assistant?clusterUid=uid-b&canonicalId=new-object#new-anchor",
    ),
  );
  expect(screen.getByPlaceholderText("描述现象")).toHaveValue("");
  fireEvent.click((await screen.findByText("创建会话")).closest("button")!);
  await waitFor(() => expect(api.create).toHaveBeenCalledTimes(1));
  expect(api.create.mock.calls[0][0]).toMatchObject({
    mode: "troubleshoot",
    authorizationDomain: "operational",
    clusterUid: "uid-b",
    resourceCanonicalIds: ["new-object"],
  });
  await waitFor(() =>
    expect(screen.getByTestId("location")).toHaveTextContent(
      "conversation=conversation-new",
    ),
  );
});

it("uses the new same-scope incident deep link in the real create body", async () => {
  api.create.mockResolvedValueOnce(successConversation("conversation-incident"));
  renderShell(
    "/assistant?clusterUid=uid-a",
    "/assistant?clusterUid=uid-a&incidentId=INC-2096",
  );
  await screen.findByText("问数");
  fireEvent.click(screen.getByText("navigate-target"));
  fireEvent.click(await screen.findByText("故障排查"));
  fireEvent.click((await screen.findByText("创建会话")).closest("button")!);
  await waitFor(() => expect(api.create).toHaveBeenCalledTimes(1));
  expect(api.create.mock.calls[0][0]).toMatchObject({
    mode: "troubleshoot",
    authorizationDomain: "operational",
    clusterUid: "uid-a",
    incidentId: "INC-2096",
  });
});

it("does not commit a valid late create success after switching to another cluster", async () => {
  const pending = {
    promise: undefined as unknown as Promise<ReturnType<typeof successConversation>>,
    resolve: undefined as unknown as (value: ReturnType<typeof successConversation>) => void,
  };
  pending.promise = new Promise((resolve) => {
    pending.resolve = resolve;
  });
  api.create.mockReturnValueOnce(pending.promise);
  renderShell(
    "/assistant?clusterUid=uid-a&canonicalId=old-object",
    "/assistant?clusterUid=uid-b&canonicalId=new-object",
  );
  const createButton = (await screen.findByText("创建会话")).closest("button")!;
  await waitFor(() => expect(createButton).toBeEnabled());
  fireEvent.click(createButton);
  await waitFor(() => expect(api.create).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByText("navigate-target"));
  await waitFor(() =>
    expect(screen.getByTestId("location")).toHaveTextContent("clusterUid=uid-b"),
  );
  await act(async () => {
    pending.resolve(successConversation("conversation-old"));
  });
  expect(api.create).toHaveBeenCalledTimes(1);
  expect(screen.getByTestId("location")).toHaveTextContent("clusterUid=uid-b");
  expect(screen.getByTestId("location")).not.toHaveTextContent(
    "conversation=conversation-old",
  );
});

it("discards a valid late create success after the real AssistantPage unmounts", async () => {
  const pending = {
    promise: undefined as unknown as Promise<ReturnType<typeof successConversation>>,
    resolve: undefined as unknown as (value: ReturnType<typeof successConversation>) => void,
  };
  pending.promise = new Promise((resolve) => {
    pending.resolve = resolve;
  });
  api.create.mockReturnValueOnce(pending.promise);
  const view = renderShell(
    "/assistant?clusterUid=uid-a&canonicalId=old-object",
    "/assistant?clusterUid=uid-b&canonicalId=new-object",
  );
  const createButton = (await screen.findByText("创建会话")).closest("button")!;
  await waitFor(() => expect(createButton).toBeEnabled());
  fireEvent.click(createButton);
  await waitFor(() => expect(api.create).toHaveBeenCalledTimes(1));
  view.unmount();
  await act(async () => {
    pending.resolve(successConversation("conversation-late"));
  });
  expect(api.create).toHaveBeenCalledTimes(1);
});

it("aborts the old conversation SSE when authorization scope changes", async () => {
  api.conversation.mockResolvedValue(
    response(
      200,
      envelope({
        conversationId: "conversation-old",
        title: "old",
        mode: "data_query",
        authorizationDomain: "operational",
        clusterUid: "uid-a",
        revision: 1,
        activeTurn: null,
      }),
    ),
  );
  api.messages.mockResolvedValue(response(200, envelope([])));
  const signals: AbortSignal[] = [];
  vi.mocked(streamJSONEvents).mockImplementation(
    async function* (...args: Parameters<typeof streamJSONEvents>) {
      const options = args[2];
      if (options?.signal) signals.push(options.signal);
      await new Promise(() => {});
    } as never,
  );
  renderShell(
    "/assistant?clusterUid=uid-a&conversation=conversation-old",
    "/assistant?clusterUid=uid-b&canonicalId=new-object",
  );
  await waitFor(() => expect(streamJSONEvents).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByText("navigate-target"));
  await waitFor(() => expect(signals[0]?.aborted).toBe(true));
});

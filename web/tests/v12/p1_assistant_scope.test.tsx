import { beforeEach, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AssistantPage } from "../../src/pages/AssistantPage";
import { ClusterScopeProvider, useClusterScope } from "../../src/app/ClusterContext";
import { setCredentials } from "../../src/api/client";

const api = vi.hoisted(() => ({
  capabilities: vi.fn(),
  conversations: vi.fn(),
  conversation: vi.fn(),
  messages: vi.fn(),
  create: vi.fn(),
  submit: vi.fn(),
  resolve: vi.fn(),
  interpret: vi.fn(),
}));
vi.mock("../../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: {
      tenantId: "tenant",
      subject: "operator-a",
      roles: ["operator"],
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
vi.mock("../../src/api/generated/platform", () => ({
  getAssistantCapabilities: api.capabilities,
  listAssistantConversations: api.conversations,
  getAssistantConversation: api.conversation,
  listAssistantMessages: api.messages,
  createAssistantConversation: api.create,
  submitAssistantMessage: api.submit,
  resolveAssistantResource: api.resolve,
  interpretAssistantSymptom: api.interpret,
  archiveAssistantConversation: vi.fn(),
  cancelAssistantTurn: vi.fn(),
  getAssistantTurn: vi.fn(),
}));
vi.mock("../../src/features/assistant/useConversationStream", () => ({
  useConversationStream: () => undefined,
}));

const envelope = (data: unknown, meta: unknown = {}) => ({ data, meta });
const capabilitiesEnvelope = {
  status: 200,
  headers: new Headers(),
  data: {
    data: {
    enabled: true,
    taskKinds: ["data_query", "incident_followup", "resource_troubleshooting"],
    catalog: { datasets: [] },
    sourceCapabilities: [],
    networkCapabilities: [],
    faultChainEnabled: false,
    },
  },
};

function ScopeControls() {
  const scope = useClusterScope();
  const [result, setResult] = useState("");
  return (
    <div>
      <span data-testid="scope-result">{result || scope.pendingSwitch}</span>
      <button onClick={() => setResult(scope.requestCluster("uid-b"))}>switch-scope</button>
    </div>
  );
}

function LocationProbe() {
  const location = useLocation();
  return <span data-testid="location">{location.pathname + location.search}</span>;
}

function renderAssistant(initial: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[initial]}>
        <ClusterScopeProvider
          clusters={[
            { clusterId: "id-a", clusterUid: "uid-a", displayName: "集群 A" },
            { clusterId: "id-b", clusterUid: "uid-b", displayName: "集群 B" },
          ]}
        >
          <LocationProbe />
          <ScopeControls />
          <AssistantPage />
        </ClusterScopeProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  // The page registers this hook through its context; the exposed function is
  // captured by ScopeControls through the shared provider below.
  return { ...utils, client };
}

beforeEach(() => {
  setCredentials("fixture", "tenant");
  api.capabilities.mockResolvedValue(capabilitiesEnvelope);
  api.conversations.mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: envelope([]),
  });
  api.create.mockReset();
  api.submit.mockReset();
  api.resolve.mockReset();
  api.interpret.mockReset();
});

it.each([
  ["incident", "/assistant?clusterUid=uid-a&incidentId=INC-2096"],
  ["resource", "/assistant?clusterUid=uid-a&canonicalId=pod-1"],
])("keeps the %s deep link in troubleshoot mode on first mount", async (_kind, initial) => {
  renderAssistant(initial);
  expect(await screen.findByText("故障排查")).toBeInTheDocument();
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((resolveIn, rejectIn) => {
    resolve = resolveIn;
    reject = rejectIn;
  });
  return { promise, resolve, reject };
}

// A request that belonged to the previous scope must not keep the new scope in a
// busy state, whatever the old request does when it finishes.
it.each(["resolve", "reject", "pending"] as const)(
  "releases the new scope after the old request %s",
  async (finish) => {
    const pending = deferred<{ conversationId: string }>();
    api.create.mockReturnValueOnce(pending.promise);
    renderAssistant("/assistant?clusterUid=uid-a");
    const createButton = (await screen.findByText("创建会话")).closest(
      "button",
    )!;
    await waitFor(() => expect(createButton).toBeEnabled());
    fireEvent.click(createButton);
    await waitFor(() => expect(api.create).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByText("switch-scope"));
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent("clusterUid=uid-b"),
    );
    if (finish === "resolve") {
      await act(async () => {
        pending.resolve({ conversationId: "conversation-old-scope" });
      });
    }
    if (finish === "reject") {
      await act(async () => {
        pending.reject(new Error("old scope failed"));
      });
    }
    const newButton = (await screen.findByText("创建会话")).closest("button")!;
    await waitFor(() => expect(newButton).not.toHaveClass("ant-btn-loading"));
    await waitFor(() => expect(newButton).toBeEnabled());
    api.create.mockResolvedValueOnce({
      conversationId: "conversation-new-scope",
    } as never);
    fireEvent.click(newButton);
    await waitFor(() => expect(api.create).toHaveBeenCalledTimes(2));
  },
);

it("does not navigate to a conversation created for the old scope after a same-tick scope switch", async () => {
  const pending = deferred<{ conversationId: string }>();
  api.create.mockReturnValueOnce(pending.promise);
  renderAssistant(
    "/assistant?clusterUid=uid-a&canonicalId=pod-1",
  );
  const createButton = (await screen.findByText("创建会话")).closest("button")!;
  await waitFor(() => expect(createButton).toBeEnabled());
  fireEvent.click(createButton);
  await waitFor(() => expect(api.create).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByText("switch-scope"));
  await waitFor(() =>
    expect(screen.getByTestId("location")).toHaveTextContent("clusterUid=uid-b"),
  );
  await act(async () => {
    pending.resolve({ conversationId: "conversation-old-scope" });
  });
  await waitFor(() => expect(api.create).toHaveBeenCalledTimes(1));
  expect(screen.getByTestId("location")).toHaveTextContent("clusterUid=uid-b");
  expect(screen.getByTestId("location")).not.toHaveTextContent(
    "conversation-old-scope",
  );
});

// The busy state belongs to the newest request: when an older request finishes
// while a newer one is still pending, the newer one keeps its loading state.
it("keeps the busy state of the newer request when the older one finishes", async () => {
  const first = deferred<{ conversationId: string }>();
  const second = deferred<{ conversationId: string }>();
  api.create.mockReturnValueOnce(first.promise);
  renderAssistant("/assistant?clusterUid=uid-a");
  const createButton = (await screen.findByText("创建会话")).closest(
    "button",
  )!;
  await waitFor(() => expect(createButton).toBeEnabled());
  fireEvent.click(createButton);
  await waitFor(() => expect(api.create).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByText("switch-scope"));
  await waitFor(() =>
    expect(screen.getByTestId("location")).toHaveTextContent("clusterUid=uid-b"),
  );
  api.create.mockReturnValueOnce(second.promise);
  const newButton = (await screen.findByText("创建会话")).closest("button")!;
  await waitFor(() => expect(newButton).toBeEnabled());
  fireEvent.click(newButton);
  await waitFor(() => expect(api.create).toHaveBeenCalledTimes(2));
  // The older request finishes now. The newer request is still pending and must
  // keep its loading state.
  await act(async () => {
    first.resolve({ conversationId: "conversation-old" });
    await Promise.resolve();
  });
  const pendingButton = (await screen.findByText("创建会话")).closest(
    "button",
  )!;
  await waitFor(() => expect(pendingButton).toHaveClass("ant-btn-loading"));
});

it("discards a late created conversation when AssistantPage unmounts", async () => {
  const pending = deferred<{ conversationId: string }>();
  api.create.mockReturnValueOnce(pending.promise);
  const view = renderAssistant(
    "/assistant?clusterUid=uid-a&canonicalId=pod-1",
  );
  expect(await screen.findByText("故障排查")).toBeInTheDocument();
  fireEvent.click((await screen.findByText("创建会话")).closest("button")!);
  await waitFor(() => expect(api.create).toHaveBeenCalledTimes(1));
  view.unmount();
  await act(async () => {
    pending.resolve({ conversationId: "conversation-after-unmount" });
  });
  expect(api.create).toHaveBeenCalledTimes(1);
});

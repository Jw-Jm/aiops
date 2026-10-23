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
  const promise = new Promise<T>((resolveIn) => {
    resolve = resolveIn;
  });
  return { promise, resolve };
}

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

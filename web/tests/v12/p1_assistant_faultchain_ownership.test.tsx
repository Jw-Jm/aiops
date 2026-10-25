// The fault-chain ownership of the real Assistant page: the page accepts a
// submission only while the state it was started for is still the state the page
// shows. A late answer of a previous scope, a previous binding revision or a
// previous turn is dropped, and a normal submission of the current state is
// accepted.
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
  collect: vi.fn(),
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
  collectAssistantFaultChain: api.collect,
  interpretAssistantSymptom: api.interpret,
  archiveAssistantConversation: vi.fn(),
  cancelAssistantTurn: vi.fn(),
  getAssistantTurn: vi.fn(),
}));
vi.mock("../../src/features/assistant/useConversationStream", () => ({
  useConversationStream: () => undefined,
}));

const envelope = (data: unknown, meta: unknown = {}) => ({ data, meta });
const capabilities = {
  status: 200,
  headers: new Headers(),
  data: {
    data: {
      enabled: true,
      taskKinds: ["data_query", "incident_followup", "resource_troubleshooting"],
      catalog: { datasets: [] },
      sourceCapabilities: [],
      networkCapabilities: [],
      faultChainEnabled: true,
    },
  },
};

function conversationFixture(revision: number) {
  return {
    status: 200,
    headers: new Headers(),
    data: {
      data: {
        tenantId: "tenant",
        conversationId: "conversation-a",
        ownerSubject: "operator-a",
        title: "故障排查",
        mode: "troubleshoot",
        authorizationDomain: "tenant",
        clusterUid: "uid-a",
        binding: {
          revision,
          executionScope: { namespaces: ["apps"], clusterScoped: false },
        },
        revision: 7,
        createdAt: "2026-10-12T00:00:00Z",
        updatedAt: "2026-10-12T00:00:00Z",
      },
    },
  };
}

function ScopeControls() {
  const scope = useClusterScope();
  return (
    <button onClick={() => scope.requestCluster("uid-b")}>switch-scope</button>
  );
}
function LocationProbe() {
  const location = useLocation();
  return <span data-testid="location">{location.pathname + location.search}</span>;
}

function renderAssistant(initial: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
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
  return client;
}

beforeEach(() => {
  api.capabilities.mockReset();
  api.conversations.mockReset();
  api.conversation.mockReset();
  api.messages.mockReset();
  api.create.mockReset();
  api.submit.mockReset();
  api.resolve.mockReset();
  api.collect.mockReset();
  api.interpret.mockReset();
  api.capabilities.mockResolvedValue(capabilities);
  api.conversations.mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: envelope([]),
  });
  api.conversation.mockResolvedValue(conversationFixture(7));
  api.messages.mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: envelope([]),
  });
  api.resolve.mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: {
      data: {
        candidates: [
          {
            canonicalId: "k8s+v1://tenant/uid-a/core/Pod/pod-1",
            namespace: "apps",
            name: "pod-1",
          },
        ],
      },
    },
  });
  setCredentials("fixture-token", "tenant");
});

async function startCollection() {
  fireEvent.change(await screen.findByLabelText("调查资源名称"), {
    target: { value: "pod-1" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "查找当前授权范围的资源" }),
  );
  fireEvent.mouseDown(screen.getByRole("combobox", { name: "调查目标资源" }));
  fireEvent.click(await screen.findByText("apps/pod-1"));
  fireEvent.click(screen.getByText("收集故障链证据"));
  await waitFor(() => expect(api.collect).toHaveBeenCalledTimes(1));
}

// A submission of the state the page currently shows is accepted: the page
// reloads the conversation after it took the answer.
it("accepts a fault-chain submission of the current state", async () => {
  api.collect.mockResolvedValue({
    status: 202,
    headers: new Headers(),
    data: { data: { turnId: "turn-current", conversationId: "conversation-a", state: "queued" } },
  });
  renderAssistant("/assistant?clusterUid=uid-a&conversation=conversation-a");
  await startCollection();
  await waitFor(() => expect(api.messages.mock.calls.length).toBeGreaterThan(1));
});

// A submission that started before the scope changed is dropped: the page does
// not reload with the answer of the previous scope.
it("drops a fault-chain submission of a previous scope", async () => {
  let finish!: (value: unknown) => void;
  const pending = new Promise((resolve) => {
    finish = resolve;
  });
  api.collect.mockReturnValue(pending);
  renderAssistant("/assistant?clusterUid=uid-a&conversation=conversation-a");
  await startCollection();
  fireEvent.click(screen.getByRole("button", { name: "switch-scope" }));
  await waitFor(() =>
    expect(screen.getByTestId("location")).toHaveTextContent("clusterUid=uid-b"),
  );
  // The scope switch itself reloads the conversation; the answer of the previous
  // scope must not trigger another reload.
  await waitFor(() => expect(api.messages.mock.calls.length).toBeGreaterThan(1));
  const afterSwitch = api.messages.mock.calls.length;
  await act(async () => {
    finish({
      status: 202,
      headers: new Headers(),
      data: { data: { turnId: "turn-old-scope", conversationId: "conversation-a", state: "queued" } },
    });
  });
  expect(screen.queryByText("turn-old-scope")).toBeNull();
  expect(api.messages.mock.calls.length).toBe(afterSwitch);
});

// The same conversation with a new binding revision is a new ownership: the
// answer of the previous revision is dropped.
it("drops a fault-chain submission of a previous binding revision", async () => {
  let finish!: (value: unknown) => void;
  const pending = new Promise((resolve) => {
    finish = resolve;
  });
  api.collect.mockReturnValue(pending);
  const client = renderAssistant("/assistant?clusterUid=uid-a&conversation=conversation-a");
  await startCollection();
  // The conversation is bound to another revision and the page reads it again.
  api.conversation.mockResolvedValue(conversationFixture(8));
  await act(async () => {
    await client.invalidateQueries({ queryKey: ["assistant"] });
  });
  await waitFor(() => expect(api.conversation.mock.calls.length).toBeGreaterThan(1));
  const afterRevision = api.messages.mock.calls.length;
  await act(async () => {
    finish({
      status: 202,
      headers: new Headers(),
      data: { data: { turnId: "turn-old-revision", conversationId: "conversation-a", state: "queued" } },
    });
  });
  expect(screen.queryByText("turn-old-revision")).toBeNull();
  expect(api.messages.mock.calls.length).toBe(afterRevision);
});

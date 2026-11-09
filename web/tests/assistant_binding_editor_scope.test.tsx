// UI mechanism fixture; no database, browser visual or live authorization
// claim. The cases below pin the lifetime of the binding editor: it belongs
// to one conversation and one authorization scope. A scope change under the
// same conversation id drops the draft, aborts the request in flight and never
// lets a late answer of the old scope touch the new one.
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
afterEach(cleanup);
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BindingEditor } from "../src/features/assistant/BindingEditor";
import type { AssistantConversationV1 } from "../src/api/generated/models";
import { rebindAssistantConversation } from "../src/api/generated/platform";
import { ApiError, setNativeCredentials } from "../src/api/client";

vi.mock("../src/api/generated/platform", async (original) => ({
  ...(await original<object>()),
  rebindAssistantConversation: vi.fn(),
}));

const conversation: AssistantConversationV1 = {
  tenantId: "tenant-fixture",
  conversationId: "conversation-1",
  ownerSubject: "operator-fixture",
  title: "生产 Pod 排查",
  mode: "data_query",
  authorizationDomain: "operational",
  clusterUid: "cluster-a",
  binding: {
    timezone: "Asia/Shanghai",
    executionScope: { namespaces: ["apps"], clusterScoped: false },
  },
  revision: 3,
  createdAt: "2026-11-09T00:00:00Z",
  updatedAt: "2026-11-09T00:00:00Z",
};

const response = (data: unknown, status = 200) => ({
  status,
  headers: new Headers(),
  data: { data, meta: {}, requestId: "unit" },
});

beforeEach(() => {
  vi.clearAllMocks();
  setNativeCredentials("tenant-fixture", "fixture-csrf-key-0001");
  vi.mocked(rebindAssistantConversation).mockResolvedValue(
    response({ ...conversation, revision: 4 }) as never,
  );
});

type EditorProps = React.ComponentProps<typeof BindingEditor>;

function renderEditor(scopeKey: string, overrides: Partial<EditorProps> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const props: EditorProps = {
    conversation,
    running: false,
    onChanged: vi.fn(),
    scopeKey,
    ...overrides,
  };
  const tree = (
    <QueryClientProvider client={client}>
      <BindingEditor {...props} />
    </QueryClientProvider>
  );
  const view = render(tree);
  return {
    client,
    props,
    rerender: (next: Partial<EditorProps>) =>
      view.rerender(
        <QueryClientProvider client={client}>
          <BindingEditor {...{ ...props, ...next }} />
        </QueryClientProvider>,
      ),
  };
}

const openEditor = async () => {
  fireEvent.click(
    (await screen.findByText(/调整后续轮次的范围或时间/)).closest("button") as HTMLElement,
  );
  return screen.findByLabelText("调整命名空间");
};

const submit = async () => {
  fireEvent.click(
    (await screen.findByText(/确认应用到后续轮次/)).closest("button") as HTMLElement,
  );
};

it("drops the draft and the pending request when the scope changes under the same conversation", async () => {
  let settle: (() => void) | undefined;
  vi.mocked(rebindAssistantConversation).mockImplementation(
    () =>
      new Promise((resolve) => {
        settle = () => resolve(response({ ...conversation, revision: 4 }) as never);
      }),
  );
  const view = renderEditor("scope-a");
  const field = await openEditor();
  fireEvent.change(field, { target: { value: "apps,private" } });
  fireEvent.click(
    (await screen.findByText(/确认应用到后续轮次/)).closest("button") as HTMLElement,
  );
  await waitFor(() => expect(rebindAssistantConversation).toHaveBeenCalledTimes(1));
  const signal = vi.mocked(rebindAssistantConversation).mock.calls[0][2]?.signal;
  // The scope changes while the request is in flight.
  view.rerender({ scopeKey: "scope-b" });
  expect(signal?.aborted).toBe(true);
  settle?.();
  await waitFor(() => expect(screen.queryByText(/失败|错误/)).toBeNull());
  // The editor of the new scope is closed and holds no draft of the old scope.
  expect(screen.queryByLabelText("调整命名空间")).toBeNull();
  const reopened = await openEditor();
  expect((reopened as HTMLInputElement).value).toBe("apps");
  // The late answer of the old scope does not report a change of the new scope.
  expect(view.props.onChanged).not.toHaveBeenCalled();
});

it("freezes the revision when the editor opens", async () => {
  const view = renderEditor("scope-a");
  await openEditor();
  // A background refresh raises the revision of the conversation.
  view.rerender({ conversation: { ...conversation, revision: 5 } });
  await submit();
  await waitFor(() =>
    expect(rebindAssistantConversation).toHaveBeenCalledWith(
      "conversation-1",
      expect.objectContaining({ expectedConversationRevision: 3 }),
      expect.anything(),
    ),
  );
  expect(vi.mocked(rebindAssistantConversation).mock.calls[0][1]).toEqual(
    expect.objectContaining({ expectedConversationRevision: 3 }),
  );
});

it("does not let a late failure of the old scope surface on the new scope", async () => {
  let fail: ((error: unknown) => void) | undefined;
  vi.mocked(rebindAssistantConversation).mockImplementation(
    () =>
      new Promise((_resolve, reject) => {
        fail = reject;
      }),
  );
  const view = renderEditor("scope-a");
  await openEditor();
  await submit();
  await waitFor(() => expect(rebindAssistantConversation).toHaveBeenCalledTimes(1));
  view.rerender({ scopeKey: "scope-b" });
  fail?.(new ApiError(500, "INVESTIGATION_UNAVAILABLE", "boom"));
  await waitFor(() => expect(screen.queryByText(/失败|错误/)).toBeNull());
  expect(view.props.onChanged).not.toHaveBeenCalled();
  expect(rebindAssistantConversation).toHaveBeenCalledTimes(1);
});

it("clears the cached scope of the assistant on a rejected authorization", async () => {
  vi.mocked(rebindAssistantConversation).mockResolvedValue({
    status: 403,
    headers: new Headers(),
    data: { code: "DENIED", message: "denied", requestId: "unit" },
  } as never);
  const view = renderEditor("scope-a");
  const remove = vi.spyOn(view.client, "removeQueries");
  await openEditor();
  await submit();
  await waitFor(() => expect(remove).toHaveBeenCalled());
  expect(vi.mocked(remove).mock.calls[0][0]).toEqual({ queryKey: ["assistant"] });
  expect(screen.getByText("没有调整此会话范围的权限")).toBeTruthy();
});

it("reports a version conflict without retrying against a newer revision", async () => {
  vi.mocked(rebindAssistantConversation).mockResolvedValue({
    status: 409,
    headers: new Headers(),
    data: { code: "STALE_CONVERSATION", message: "stale", requestId: "unit" },
  } as never);
  renderEditor("scope-a");
  await openEditor();
  await submit();
  await waitFor(() =>
    expect(screen.getByText("会话已被其他操作修改，请刷新后重试")).toBeTruthy(),
  );
  expect(rebindAssistantConversation).toHaveBeenCalledTimes(1);
});

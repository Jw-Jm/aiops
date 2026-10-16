import { beforeEach, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useConversationStream } from "../src/features/assistant/useConversationStream";
import { streamJSONEvents, SSEHttpError } from "../src/api/sse-reader";
import { setCredentials } from "../src/api/client";

vi.mock("../src/api/sse-reader", async (original) => ({
  ...(await original<typeof import("../src/api/sse-reader")>()),
  streamJSONEvents: vi.fn(),
}));
const current = ["assistant", "scope", "conversation", "messages"];
const capabilities = ["assistant", "scope", "capabilities", "cluster"];
const retained = ["assistant", "scope", "query", "result"];
const other = ["assistant", "other-scope", "conversation", "messages"];
beforeEach(() => setCredentials("FIXTURE", "tenant"));
function mount() {
  const client = new QueryClient();
  for (const key of [current, capabilities, retained, other])
    client.setQueryData(key, {});
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const hook = renderHook(
    () => useConversationStream("conversation", "scope"),
    {
      wrapper: ({ children }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    },
  );
  return { client, invalidate, ...hook };
}
function events(...types: string[]) {
  vi.mocked(streamJSONEvents).mockImplementation(async function* () {
    for (const type of types)
      yield { id: type, event: type, data: { type } } as never;
  });
}
it("does not turn evidence read auditing into another read", async () => {
  events(...Array(20).fill("turn.result_read"));
  const { client, invalidate } = mount();
  await waitFor(() => expect(streamJSONEvents).toHaveBeenCalled());
  expect(invalidate).not.toHaveBeenCalled();
  expect(client.getQueryState(retained)?.isInvalidated).toBe(false);
});
it("refreshes current progress without re-reading capabilities or retained results", async () => {
  events("turn.step_started", "turn.step_succeeded");
  const { client, invalidate } = mount();
  await waitFor(() => expect(invalidate).toHaveBeenCalledTimes(2));
  expect(client.getQueryState(current)?.isInvalidated).toBe(true);
  for (const key of [capabilities, retained, other])
    expect(client.getQueryState(key)?.isInvalidated).toBe(false);
});
it("refreshes the session at completion while preserving other authorization scopes", async () => {
  events("answer.ready", "turn.result_read");
  const { client, invalidate } = mount();
  await waitFor(() => expect(invalidate).toHaveBeenCalledTimes(1));
  for (const key of [current, capabilities, retained])
    expect(client.getQueryState(key)?.isInvalidated).toBe(true);
  expect(client.getQueryState(other)?.isInvalidated).toBe(false);
});
it.each([401, 403])("clears the denied session on SSE %s", async (status) => {
  vi.mocked(streamJSONEvents).mockImplementation(async function* () {
    throw new SSEHttpError(status, "denied");
  });
  const { client } = mount();
  await waitFor(() => expect(client.getQueryState(current)).toBeUndefined());
  expect(client.getQueryState(other)).toBeDefined();
});
it("invalidates the whole session when the event cursor expires", async () => {
  vi.mocked(streamJSONEvents).mockImplementation(async function* () {
    throw new SSEHttpError(410, "cursor expired");
  });
  const { client, invalidate } = mount();
  await waitFor(() => expect(invalidate).toHaveBeenCalledTimes(1));
  expect(client.getQueryState(capabilities)?.isInvalidated).toBe(true);
  expect(client.getQueryState(retained)?.isInvalidated).toBe(true);
});

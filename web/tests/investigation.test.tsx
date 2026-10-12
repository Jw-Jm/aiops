import { it, expect } from "vitest";
import {
  readJSONEventStream,
  decodeInvestigationEvent,
  streamInvestigationEventsWithResume,
} from "../src/api/sse-reader";
function body(value: string) {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(value));
      controller.close();
    },
  });
}
it("rejects oversized SSE frames and releases reader", async () => {
  const stream = body(
    "data: " + JSON.stringify({ large: "x".repeat(270000) }) + "\n\n",
  );
  await expect(async () => {
    for await (const _ of readJSONEventStream(stream, (x) => x)) {
    }
  }).rejects.toThrow("budget");
  expect(stream.locked).toBe(false);
});
it("requires an authenticated persisted event envelope", () => {
  expect(() => decodeInvestigationEvent({ message: "mock thought" })).toThrow(
    "missing",
  );
  expect(() =>
    decodeInvestigationEvent({
      eventId: "1",
      eventType: "step",
      jobId: "j",
      occurredAt: "now",
      payload: {},
    }),
  ).not.toThrow();
});
it("requires Bearer for reconnect", async () => {
  await expect(async () => {
    for await (const _ of streamInvestigationEventsWithResume("/test", {
      headers: {},
    })) {
    }
  }).rejects.toThrow("Bearer");
});

import { abortDelay } from "../src/features/investigation/delay";
import { vi } from "vitest";
it("releases every abort listener after repeated retry waits and cancellation", async () => {
  const c = new AbortController();
  const add = vi.spyOn(c.signal, "addEventListener"),
    remove = vi.spyOn(c.signal, "removeEventListener");
  for (let i = 0; i < 20; i++) await abortDelay(c.signal, 1);
  expect(add).toHaveBeenCalledTimes(20);
  expect(remove).toHaveBeenCalledTimes(20);
  const pending = abortDelay(c.signal, 60000);
  c.abort();
  await pending;
  expect(remove).toHaveBeenCalledTimes(21);
});

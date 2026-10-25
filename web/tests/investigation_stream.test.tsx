// Explicit transport fixture: lifecycle validation, not live model acceptance.
import { afterEach, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { useInvestigationStream } from "../src/features/investigation/useInvestigationStream";
import { authEvents, setCredentials, setNativeCredentials } from "../src/api/client";

const calls: {
  headers: Headers;
  signal: AbortSignal;
  controller: ReadableStreamDefaultController<Uint8Array>;
  credentials?: RequestCredentials;
}[] = [];
function transport(native = false) {
  calls.length = 0;
  if (native) {
    setNativeCredentials("fixture-tenant");
  } else {
    setCredentials("fixture-token", "fixture-tenant");
  }
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, options: RequestInit) => {
      let controller!: ReadableStreamDefaultController<Uint8Array>;
      const signal = options.signal as AbortSignal;
      const body = new ReadableStream<Uint8Array>({
        start(c) {
          controller = c;
          signal.addEventListener(
            "abort",
            () => c.error(new DOMException("aborted", "AbortError")),
            { once: true },
          );
        },
      });
      calls.push({
        headers: new Headers(options.headers),
        signal,
        controller,
        credentials: options.credentials,
      });
      return new Response(body, {
        headers: { "Content-Type": "text/event-stream" },
      });
    }),
  );
}
function emit(call: number, seq: number, jobId = "fixture-job") {
  calls[call].controller.enqueue(
    new TextEncoder().encode(
      `id: cursor-${jobId}-${seq}

data: ${JSON.stringify({ eventId: String(seq), eventType: "step", jobId, occurredAt: "2026-10-12T00:00:00Z", payload: {} })}

`,
    ),
  );
}
afterEach(() => {
  vi.unstubAllGlobals();
  setCredentials();
});

it("manual reconnect sends the last cursor, deduplicates replay, bounds the ring and preserves ended-job events", async () => {
  transport();
  const { result, rerender, unmount } = renderHook(
    ({ active }) => useInvestigationStream("fixture-job", active, () => {}),
    { initialProps: { active: true } },
  );
  await waitFor(() => expect(calls).toHaveLength(1));
  act(() => emit(0, 1));
  await waitFor(() => expect(result.current.events).toHaveLength(1));
  act(() => result.current.reconnect());
  await waitFor(() => expect(calls).toHaveLength(2));
  expect(calls[0].signal.aborted).toBe(true);
  expect(calls[1].headers.get("Authorization")).toBe("Bearer fixture-token");
  expect(calls[1].headers.get("Last-Event-ID")).toBe("cursor-fixture-job-1");
  act(() => {
    for (let i = 1; i <= 105; i++) emit(1, i);
  });
  await waitFor(() =>
    expect(result.current.events.at(-1)?.eventId).toBe("105"),
  );
  expect(result.current.events).toHaveLength(100);
  expect(result.current.events[0].eventId).toBe("6");
  rerender({ active: false });
  expect(calls[1].signal.aborted).toBe(true);
  expect(result.current.events).toHaveLength(100);
  expect(result.current.status).toBe("idle");
  unmount();
});

it("streams the investigation events with the native cookie session and keeps the order", async () => {
  transport(true);
  const { result, unmount } = renderHook(() =>
    useInvestigationStream("fixture-job", true, () => {}),
  );
  await waitFor(() => expect(calls).toHaveLength(1));
  expect(calls[0].credentials).toBe("same-origin");
  expect(calls[0].headers.get("Authorization")).toBeNull();
  act(() => emit(0, 1));
  await waitFor(() => expect(result.current.events).toHaveLength(1));
  act(() => emit(0, 2));
  await waitFor(() => expect(result.current.events).toHaveLength(2));
  expect(result.current.events.map((e) => e.eventId)).toEqual(["1", "2"]);
  unmount();
});

it("changing jobs starts from the new ledger without the previous cursor and aborts on unmount", async () => {
  transport();
  const { result, rerender, unmount } = renderHook(
    ({ jobId }) => useInvestigationStream(jobId, true, () => {}),
    { initialProps: { jobId: "fixture-job" } },
  );
  await waitFor(() => expect(calls).toHaveLength(1));
  act(() => emit(0, 1));
  await waitFor(() => expect(result.current.events).toHaveLength(1));
  rerender({ jobId: "other-job" });
  await waitFor(() => expect(calls).toHaveLength(2));
  expect(calls[0].signal.aborted).toBe(true);
  expect(calls[1].headers.has("Last-Event-ID")).toBe(false);
  expect(result.current.events).toHaveLength(0);
  unmount();
  expect(calls[1].signal.aborted).toBe(true);
});

it("permission changes stop the stream and discard buffered data and the old cursor", async () => {
  transport();
  const { result, unmount } = renderHook(() =>
    useInvestigationStream("fixture-job", true, () => {}),
  );
  await waitFor(() => expect(calls).toHaveLength(1));
  act(() => emit(0, 1));
  await waitFor(() => expect(result.current.events).toHaveLength(1));
  act(() => authEvents.dispatchEvent(new Event("authorizationChanged")));
  expect(calls[0].signal.aborted).toBe(true);
  expect(result.current.events).toHaveLength(0);
  expect(result.current.status).toBe("permission_changed");
  unmount();
});

it("clears retained ended-job events when authorization is later withdrawn", async () => {
  transport();
  const { result, rerender, unmount } = renderHook(
    ({ active }) => useInvestigationStream("fixture-job", active, () => {}),
    { initialProps: { active: true } },
  );
  await waitFor(() => expect(calls).toHaveLength(1));
  act(() => emit(0, 1));
  await waitFor(() => expect(result.current.events).toHaveLength(1));
  rerender({ active: false });
  expect(result.current.events).toHaveLength(1);
  act(() => authEvents.dispatchEvent(new Event("expired")));
  expect(result.current.events).toHaveLength(0);
  expect(result.current.status).toBe("permission_changed");
  unmount();
});
it("a direct SSE 403 discards buffered data and requests an identity recheck", async () => {
  transport();
  const forbidden = vi.fn();
  authEvents.addEventListener("forbidden", forbidden);
  const { result, unmount } = renderHook(() =>
    useInvestigationStream("fixture-job", true, () => {}),
  );
  await waitFor(() => expect(calls).toHaveLength(1));
  act(() => emit(0, 1));
  await waitFor(() => expect(result.current.events).toHaveLength(1));
  vi.mocked(fetch).mockResolvedValueOnce(
    new Response("grant revoked", { status: 403 }),
  );
  act(() => result.current.reconnect());
  await waitFor(() => expect(result.current.status).toBe("permission_revoked"));
  expect(result.current.events).toHaveLength(0);
  expect(forbidden).toHaveBeenCalledTimes(1);
  authEvents.removeEventListener("forbidden", forbidden);
  unmount();
});
it("a 410 requests ledger recovery and manual retry starts without the expired cursor", async () => {
  transport();
  const notify = vi.fn();
  const { result, unmount } = renderHook(() =>
    useInvestigationStream("fixture-job", true, notify),
  );
  await waitFor(() => expect(calls).toHaveLength(1));
  act(() => emit(0, 1));
  await waitFor(() => expect(result.current.events).toHaveLength(1));
  vi.mocked(fetch).mockResolvedValueOnce(
    new Response("cursor expired", { status: 410 }),
  );
  act(() => result.current.reconnect());
  await waitFor(() => expect(result.current.status).toBe("cursor_expired"));
  expect(notify).toHaveBeenCalledTimes(2);
  expect(result.current.events).toHaveLength(0);
  act(() => result.current.reconnect());
  await waitFor(() => expect(calls).toHaveLength(2));
  expect(calls[1].headers.has("Last-Event-ID")).toBe(false);
  act(() => emit(1, 1));
  await waitFor(() => expect(result.current.events).toHaveLength(1));
  unmount();
});

import { it, expect, vi } from "vitest";
import {
  checked,
  clearCredentials,
  setCredentials,
  requestOptions,
  authEvents,
} from "../src/api/client";
for (const status of [401, 403])
  it(`ignores delayed business ${status} authorization effects after a new credential generation`, async () => {
    setCredentials("fixture-old", "tenant-old");
    let resolve!: (v: {
      data: { code: string; message: string };
      status: number;
      headers: Headers;
    }) => void;
    const response = new Promise<{
      data: { code: string; message: string };
      status: number;
      headers: Headers;
    }>((r) => {
      resolve = r;
    });
    const pending = checked(response).catch((e) => e);
    const listener = vi.fn();
    authEvents.addEventListener(
      status === 401 ? "expired" : "forbidden",
      listener,
    );
    setCredentials("fixture-new", "tenant-new");
    resolve({
      status,
      data: {
        code: status === 401 ? "UNAUTHENTICATED" : "FORBIDDEN",
        message: "old fixture request",
      },
      headers: new Headers(),
    });
    expect((await pending).status).toBe(status);
    expect(listener).not.toHaveBeenCalled();
    expect(new Headers(requestOptions().headers).get("Authorization")).toBe(
      "Bearer fixture-new",
    );
    authEvents.removeEventListener(
      status === 401 ? "expired" : "forbidden",
      listener,
    );
  });

for (const change of ["identity", "authorization", "logout", "session"])
  it(`discards a delayed successful response after ${change} changes`, async () => {
    setCredentials("old", "tenant");
    let resolve!: (value: {
      status: 200;
      data: string;
      headers: Headers;
    }) => void;
    let currentSession = true;
    const pending = checked(
      new Promise<{ status: 200; data: string; headers: Headers }>((r) => {
        resolve = r;
      }),
      true,
      () => currentSession,
    );
    const rejected = expect(pending).rejects.toMatchObject({
      name: "AbortError",
    });
    if (change === "identity") setCredentials("new", "another-tenant");
    if (change === "authorization")
      authEvents.dispatchEvent(new Event("authorizationChanged"));
    if (change === "logout") clearCredentials();
    if (change === "session") currentSession = false;
    resolve({
      status: 200,
      data: "private old result",
      headers: new Headers(),
    });
    await rejected;
  });

it("keeps requests valid across a refresh of the same credentials", async () => {
  setCredentials("same", "same-tenant");
  let resolve!: (value: {
    status: 200;
    data: string;
    headers: Headers;
  }) => void;
  const pending = checked(
    new Promise<{ status: 200; data: string; headers: Headers }>((r) => {
      resolve = r;
    }),
  );
  setCredentials("same");
  setCredentials("same", "same-tenant");
  resolve({ status: 200, data: "current result", headers: new Headers() });
  expect(await pending).toBe("current result");
  expect(new Headers(requestOptions().headers).get("X-Tenant-ID")).toBe(
    "same-tenant",
  );
});

it("aborts in-flight requests and streams on revocation and retains caller cancellation", () => {
  setCredentials("active", "tenant");
  const caller = new AbortController();
  const active = requestOptions(caller.signal).signal!;
  const stream = requestOptions().signal!;
  authEvents.dispatchEvent(new Event("authorizationChanged"));
  expect(active.aborted).toBe(true);
  expect(stream.aborted).toBe(true);
  const next = requestOptions(caller.signal).signal!;
  expect(next.aborted).toBe(false);
  caller.abort();
  expect(next.aborted).toBe(true);
  expect(requestOptions().signal!.aborted).toBe(false);
});

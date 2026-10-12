import { it, expect, vi } from "vitest";
import {
  checked,
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

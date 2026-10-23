// R3 regression: a failed server revocation must stay visible and retryable,
// and must never be reported as a successful sign-out.
import { beforeEach, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AuthProvider, useAuth, type LogoutOutcome } from "../src/auth/AuthProvider";
import { getAuthSession, nativeLogin, nativeLogout } from "../src/api/generated/platform";
import type { NativeSessionViewV1 } from "../src/api/generated/models";

const oidc = vi.hoisted(() => ({ getManager: vi.fn(), restore: vi.fn() }));
vi.mock("../src/auth/oidc", () => ({
  getManager: oidc.getManager,
  restore: oidc.restore,
  login: vi.fn(),
  safeReturnTo: (value: string) => value,
}));
vi.mock("../src/api/generated/platform", () => ({
  getAuthSession: vi.fn(),
  nativeLogin: vi.fn(),
  nativeLogout: vi.fn(),
  changeNativePassword: vi.fn(),
}));

function view(sessionRef = "session:one", sessionVersion = 4): NativeSessionViewV1 {
  return {
    sessionRef,
    subject: "user:one",
    tenantId: "tenant-one",
    mode: "full",
    authMethods: ["password"],
    acr: "urn:ops:loa:1",
    issuedAt: "2026-10-22T00:00:00Z",
    absoluteExpiresAt: "2099-01-01T00:00:00Z",
    idleExpiresAt: "2099-01-01T00:00:00Z",
    mustChangePassword: false,
    accountRevision: 1,
    revocation: {
      credentialVersion: 2,
      sessionVersion,
      authorizationVersion: 3,
    },
    csrf: {
      headerName: "X-CSRF-Token",
      token: "csrf-token",
      cookieName: "__Host-ops-session",
      sameSite: "Strict",
    },
    authorization: {
      grants: [
        {
          bindingId: "00000000-0000-4000-8000-000000000101",
          userId: "00000000-0000-4000-8000-000000000001",
          tenantId: "00000000-0000-4000-8000-000000000002",
          role: "platform_admin",
          scope: { clusterUids: [], namespaces: [] },
          authorizationVersion: 3,
          grantedAt: "2026-10-22T00:00:00Z",
          revokedAt: null,
        },
      ],
    },
    authorizedClusters: [],
    administratorState: {
      activeAdministrators: 1,
      isAdministrator: true,
      isLastActiveAdministrator: true,
    },
  };
}

function response(data: NativeSessionViewV1, status = 200) {
  return { status, headers: new Headers(), data: { data, requestId: "test" } };
}

function Consumer() {
  const auth = useAuth();
  return (
    <div>
      <span data-testid="mode">{auth.authMode}</span>
      <span data-testid="logged">{String(auth.loggedOut)}</span>
      <span data-testid="roles">{auth.session?.roles.join(",") || "none"}</span>
      <span data-testid="pending">
        {auth.pendingRevocation
          ? `${auth.pendingRevocation.sessionRef}|${auth.pendingRevocation.expectedSessionVersion}|${auth.pendingRevocation.attempts}`
          : "none"}
      </span>
      <span data-testid="outcome">{lastOutcome?.status ?? "none"}</span>
      <button onClick={() => void auth.logout().then((value) => (lastOutcome = value))}>
        logout
      </button>
      <button onClick={() => void auth.logout().then((value) => (lastOutcome = value))}>
        logout-again
      </button>
      <button
        onClick={() =>
          void auth
            .retryRevocation()
            .then((value) => (lastOutcome = value))
        }
      >
        retry
      </button>
      <button
        onClick={() =>
          void auth.nativeLogin("other.user", "another-password", "/access/sources")
        }
      >
        login-other
      </button>
    </div>
  );
}
let lastOutcome: LogoutOutcome | null = null;

function renderProvider() {
  return render(
    <MemoryRouter>
      <AuthProvider>
        <Consumer />
      </AuthProvider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  window.sessionStorage.clear();
  lastOutcome = null;
  vi.mocked(getAuthSession).mockReset();
  vi.mocked(nativeLogout).mockReset();
  vi.mocked(nativeLogin).mockReset();
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify({ auth: { mode: "native" } }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
    ),
  );
});

it("keeps the exact session as unconfirmed on a 500 revocation failure", async () => {
  vi.mocked(getAuthSession).mockResolvedValue(response(view()) as never);
  vi.mocked(nativeLogout).mockRejectedValue(
    Object.assign(new Error("server error"), { status: 500 }),
  );
  renderProvider();
  expect(await screen.findByText("native")).toBeVisible();
  fireEvent.click(screen.getByText("logout"));
  await waitFor(() => expect(lastOutcome?.status).toBe("unconfirmed"));
  expect(screen.getByTestId("logged")).toHaveTextContent("true");
  expect(screen.getByTestId("roles")).toHaveTextContent("none");
  expect(screen.getByTestId("pending")).toHaveTextContent("session:one|4|1");
  expect(window.sessionStorage.getItem("v12:pending-revocation")).toContain("session:one");
});

it("treats a timeout and an offline browser as unconfirmed, not as success", async () => {
  vi.mocked(getAuthSession).mockResolvedValue(response(view()) as never);
  vi.mocked(nativeLogout).mockRejectedValue(new DOMException("timeout", "TimeoutError"));
  const first = renderProvider();
  expect(await screen.findByText("native")).toBeVisible();
  fireEvent.click(screen.getByText("logout"));
  await waitFor(() => expect(lastOutcome?.status).toBe("unconfirmed"));
  first.unmount();

  window.sessionStorage.clear();
  lastOutcome = null;
  vi.mocked(nativeLogout).mockRejectedValue(new TypeError("Failed to fetch"));
  renderProvider();
  expect(await screen.findByText("native")).toBeVisible();
  fireEvent.click(screen.getByText("logout"));
  await waitFor(() => expect(lastOutcome?.status).toBe("unconfirmed"));
  expect(screen.getByTestId("pending")).toHaveTextContent("session:one|4|1");
});

it("survives a reload after the failure and reports the same pending session", async () => {
  window.sessionStorage.setItem(
    "v12:pending-revocation",
    JSON.stringify({
      sessionRef: "session:one",
      expectedSessionVersion: 4,
      reason: "user_requested_logout",
      failedAt: "2026-10-22T10:00:00.000Z",
      attempts: 2,
      csrfToken: "csrf-token",
    }),
  );
  vi.mocked(getAuthSession).mockResolvedValue(response(view()) as never);
  renderProvider();
  expect(await screen.findByTestId("pending")).toHaveTextContent("session:one|4|2");
  await waitFor(() => expect(screen.getByTestId("logged")).toHaveTextContent("true"));
  expect(screen.getByTestId("roles")).toHaveTextContent("none");
  fireEvent.click(screen.getByText("logout-again"));
  await waitFor(() => expect(lastOutcome?.status).toBe("unconfirmed"));
  expect(screen.getByTestId("pending")).toHaveTextContent("session:one|4|2");
  expect(nativeLogout).not.toHaveBeenCalled();
});

it("retries only the recorded session and clears the notice on success", async () => {
  window.sessionStorage.setItem(
    "v12:pending-revocation",
    JSON.stringify({
      sessionRef: "session:one",
      expectedSessionVersion: 4,
      reason: "user_requested_logout",
      failedAt: "2026-10-22T10:00:00.000Z",
      attempts: 1,
      csrfToken: "csrf-token",
    }),
  );
  // The browser still carries the same session, because the revocation failed.
  vi.mocked(getAuthSession).mockResolvedValue(response(view("session:one", 4)) as never);
  vi.mocked(nativeLogout).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: { data: {}, requestId: "retry" },
  } as never);
  renderProvider();
  expect(await screen.findByTestId("pending")).toHaveTextContent("session:one|4|1");
  fireEvent.click(screen.getByText("retry"));
  await waitFor(() => expect(screen.getByTestId("pending")).toHaveTextContent("none"));
  expect(nativeLogout).toHaveBeenCalledWith(
    { expectedSessionVersion: 4, reason: "user_requested_logout" },
    expect.anything(),
  );
  // The retry carries the CSRF token of the session it must revoke, because
  // the local identity is already cleared.
  const options = vi.mocked(nativeLogout).mock.calls[0][1] as RequestInit;
  expect((options.headers as Record<string, string>)["X-CSRF-Token"]).toBe(
    "csrf-token",
  );
});

it("refuses a late retry when the browser now carries another account session", async () => {
  window.sessionStorage.setItem(
    "v12:pending-revocation",
    JSON.stringify({
      sessionRef: "session:one",
      expectedSessionVersion: 4,
      reason: "user_requested_logout",
      failedAt: "2026-10-22T10:00:00.000Z",
      attempts: 1,
      csrfToken: "csrf-token",
    }),
  );
  vi.mocked(getAuthSession).mockResolvedValue(response(view("session:two", 9)) as never);
  renderProvider();
  expect(await screen.findByText("native")).toBeVisible();
  fireEvent.click(screen.getByText("retry"));
  await waitFor(() => expect(lastOutcome?.status).toBe("superseded"));
  // The new identity is untouched and the old session was never revoked through
  // the new cookie.
  expect(nativeLogout).not.toHaveBeenCalled();
  expect(screen.getByTestId("roles")).toHaveTextContent("platform_admin");
  expect(screen.getByTestId("logged")).toHaveTextContent("false");
});

it("does not let a late logout response clear a newly adopted identity", async () => {
  let complete!: (value: unknown) => void;
  vi.mocked(getAuthSession)
    .mockResolvedValueOnce(response(view()) as never)
    .mockResolvedValue(response(view("session:two", 9)) as never);
  vi.mocked(nativeLogout).mockReturnValue(
    new Promise((resolve) => {
      complete = resolve;
    }) as never,
  );
  vi.mocked(nativeLogin).mockResolvedValue(response(view("session:two", 9)) as never);
  renderProvider();
  expect(await screen.findByText("native")).toBeVisible();
  fireEvent.click(screen.getByText("logout"));
  await waitFor(() => expect(screen.getByTestId("logged")).toHaveTextContent("true"));
  fireEvent.click(screen.getByText("login-other"));
  await waitFor(() => expect(screen.getByTestId("roles")).toHaveTextContent("platform_admin"));
  complete({ status: 200, headers: new Headers(), data: { data: {}, requestId: "late" } });
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(screen.getByTestId("roles")).toHaveTextContent("platform_admin");
  expect(screen.getByTestId("logged")).toHaveTextContent("false");
});

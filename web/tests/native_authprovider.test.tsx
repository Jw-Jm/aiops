import { beforeEach, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AuthProvider, useAuth } from "../src/auth/AuthProvider";
import { authEvents, requestOptions } from "../src/api/client";
import {
  getAuthSession,
  nativeLogout,
  changeNativePassword,
} from "../src/api/generated/platform";
import type { NativeSessionViewV1 } from "../src/api/generated/models";

const oidc = vi.hoisted(() => ({ getManager: vi.fn(), restore: vi.fn() }));
vi.mock("../src/auth/oidc", () => ({
  getManager: oidc.getManager,
  restore: oidc.restore,
  login: vi.fn(),
}));
vi.mock("../src/api/generated/platform", () => ({
  getAuthSession: vi.fn(),
  nativeLogin: vi.fn(),
  nativeLogout: vi.fn(),
  changeNativePassword: vi.fn(),
}));

function view(
  mode: "full" | "first_password_only" = "full",
): NativeSessionViewV1 {
  const full = mode === "full";
  return {
    sessionRef: "session:1",
    subject: "user:one",
    tenantId: "tenant-one",
    mode,
    authMethods: ["password"],
    acr: "urn:ops:loa:1",
    issuedAt: "2026-10-22T00:00:00Z",
    absoluteExpiresAt: "2099-01-01T00:00:00Z",
    idleExpiresAt: "2099-01-01T00:00:00Z",
    mustChangePassword: !full,
    accountRevision: 1,
    revocation: {
      credentialVersion: 1,
      sessionVersion: 1,
      authorizationVersion: 1,
    },
    csrf: {
      headerName: "X-CSRF-Token",
      token: "csrf-stable-token",
      cookieName: "__Host-ops-session",
      sameSite: "Strict",
    },
    authorization: {
      grants: full
        ? [
            {
              bindingId: "00000000-0000-4000-8000-000000000101",
              userId: "00000000-0000-4000-8000-000000000001",
              tenantId: "00000000-0000-4000-8000-000000000002",
              role: "operator",
              scope: { clusterUids: ["cluster-a"], namespaces: ["default"] },
              authorizationVersion: 1,
              grantedAt: "2026-10-22T00:00:00Z",
              revokedAt: null,
            },
          ]
        : [],
    },
    authorizedClusters: full
      ? [
          {
            clusterId: "00000000-0000-4000-8000-000000000003",
            clusterUid: "cluster-a",
            displayName: "Cluster A",
          },
        ]
      : [],
    administratorState: {
      activeAdministrators: 1,
      isAdministrator: false,
      isLastActiveAdministrator: false,
    },
  };
}

function response(data: NativeSessionViewV1, status = 200) {
  return {
    status,
    headers: new Headers(),
    data: { data, requestId: "test" },
  };
}

function Consumer() {
  const auth = useAuth();
  return (
    <div>
      <span data-testid="mode">{auth.authMode}</span>
      <span data-testid="roles">{auth.session?.roles.join(",") || "none"}</span>
      <span data-testid="clusters">
        {auth.session?.authorizedClusters
          .map((cluster) => cluster.clusterUid)
          .join(",") || "none"}
      </span>
      <span data-testid="scope">
        {auth.session?.clusterScopes.join(",") || "none"}
      </span>
      <span data-testid="namespace">
        {auth.session?.namespaceScopes
          .map((v) => `${v.clusterId}/${v.namespace}`)
          .join(",") || "none"}
      </span>
      <span data-testid="logged">{String(auth.loggedOut)}</span>
      <span data-testid="error">{auth.error?.message}</span>
      {auth.nativeSession?.mode === "first_password_only" ? (
        <button
          onClick={() =>
            void auth
              .changeNativePassword("current-password", "replacement-password")
              .catch(() => undefined)
          }
        >
          change
        </button>
      ) : null}
      <button onClick={() => void auth.refresh()}>refresh</button>
      <button onClick={() => void auth.logout()}>logout</button>
    </div>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  oidc.restore.mockReset();
  vi.mocked(getAuthSession).mockReset();
  vi.mocked(nativeLogout).mockReset();
  vi.mocked(changeNativePassword).mockReset();
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

it("starts in native mode without touching the OIDC manager and uses server clusters", async () => {
  vi.mocked(getAuthSession).mockResolvedValue(response(view()) as never);
  render(
    <MemoryRouter>
      <AuthProvider>
        <Consumer />
      </AuthProvider>
    </MemoryRouter>,
  );
  expect(await screen.findByText("native")).toBeVisible();
  expect(screen.getByTestId("roles")).toHaveTextContent("operator");
  expect(screen.getByTestId("clusters")).toHaveTextContent("cluster-a");
  expect(screen.getByTestId("scope")).toHaveTextContent("none");
  expect(screen.getByTestId("namespace")).toHaveTextContent(
    "00000000-0000-4000-8000-000000000003/default",
  );
  expect(screen.getByTestId("scope")).not.toHaveTextContent("cluster-a");
  expect(oidc.getManager).not.toHaveBeenCalled();
});

it("removes all business grants from a first-password session", async () => {
  vi.mocked(getAuthSession).mockResolvedValue(
    response(view("first_password_only")) as never,
  );
  render(
    <MemoryRouter>
      <AuthProvider>
        <Consumer />
      </AuthProvider>
    </MemoryRouter>,
  );
  expect(await screen.findByText("change")).toBeVisible();
  expect(screen.getByTestId("roles")).toHaveTextContent("none");
  expect(screen.getByTestId("clusters")).toHaveTextContent("none");
});

it("keeps a valid session for a wrong current password, but expires a genuinely invalid session", async () => {
  vi.mocked(getAuthSession).mockResolvedValue(
    response(view("first_password_only")) as never,
  );
  vi.mocked(changeNativePassword).mockResolvedValue({
    status: 401,
    headers: new Headers(),
    data: {
      code: "CURRENT_PASSWORD_INVALID",
      message: "current password was not accepted",
      requestId: "wrong-current",
    },
  } as never);
  render(
    <MemoryRouter>
      <AuthProvider>
        <Consumer />
      </AuthProvider>
    </MemoryRouter>,
  );
  fireEvent.click(await screen.findByText("change"));
  await waitFor(() => expect(changeNativePassword).toHaveBeenCalledTimes(1));
  await waitFor(() =>
    expect(screen.getByTestId("error")).toHaveTextContent("当前密码不正确。"),
  );
  await waitFor(() =>
    expect(screen.getByTestId("logged")).toHaveTextContent("false"),
  );
  expect(screen.getByText("change")).toBeVisible();
  expect(
    new Headers(requestOptions(undefined, true).headers).get("X-CSRF-Token"),
  ).toBe("csrf-stable-token");
  vi.mocked(changeNativePassword).mockResolvedValue({
    status: 401,
    headers: new Headers(),
    data: {
      code: "UNAUTHENTICATED",
      message: "session unavailable",
      requestId: "expired",
    },
  } as never);
  fireEvent.click(screen.getByText("change"));
  await waitFor(() =>
    expect(screen.getByTestId("logged")).toHaveTextContent("true"),
  );
});

it("sends the recovered CSRF token on password change and logout", async () => {
  vi.mocked(getAuthSession).mockResolvedValue(
    response(view("first_password_only")) as never,
  );
  vi.mocked(changeNativePassword).mockResolvedValue(response(view()) as never);
  vi.mocked(nativeLogout).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: { data: {}, requestId: "test" },
  } as never);
  render(
    <MemoryRouter>
      <AuthProvider>
        <Consumer />
      </AuthProvider>
    </MemoryRouter>,
  );
  fireEvent.click(await screen.findByText("change"));
  await waitFor(() => expect(changeNativePassword).toHaveBeenCalledTimes(1));
  expect(changeNativePassword).toHaveBeenCalledWith(
    expect.objectContaining({
      currentPassword: "current-password",
      newPassword: "replacement-password",
    }),
    expect.objectContaining({
      credentials: "same-origin",
      headers: expect.objectContaining({ "X-CSRF-Token": "csrf-stable-token" }),
    }),
  );
  fireEvent.click(screen.getByText("logout"));
  await waitFor(() => expect(nativeLogout).toHaveBeenCalledTimes(1));
  const options = vi.mocked(nativeLogout).mock.calls[0][1] as RequestInit;
  expect((options.headers as Record<string, string>)["X-CSRF-Token"]).toBe(
    "csrf-stable-token",
  );
});

it("expires native state on an expired event without reviving an old refresh", async () => {
  vi.mocked(getAuthSession).mockResolvedValue(response(view()) as never);
  render(
    <MemoryRouter>
      <AuthProvider>
        <Consumer />
      </AuthProvider>
    </MemoryRouter>,
  );
  expect(await screen.findByText("native")).toBeVisible();
  authEvents.dispatchEvent(new Event("expired"));
  await waitFor(() =>
    expect(screen.getByTestId("logged")).toHaveTextContent("true"),
  );
  expect(oidc.getManager).not.toHaveBeenCalled();
});

it("does not invalidate queries on a same-session refresh", async () => {
  vi.mocked(getAuthSession).mockResolvedValue(response(view()) as never);
  let changed = 0;
  const listener = () => {
    changed += 1;
  };
  authEvents.addEventListener("authorizationChanged", listener);
  render(
    <MemoryRouter>
      <AuthProvider>
        <Consumer />
      </AuthProvider>
    </MemoryRouter>,
  );
  expect(await screen.findByText("native")).toBeVisible();
  fireEvent.click(screen.getByText("refresh"));
  await waitFor(() => expect(getAuthSession).toHaveBeenCalledTimes(2));
  expect(changed).toBe(0);
  authEvents.removeEventListener("authorizationChanged", listener);
});

it("rejects a delayed refresh after logout started", async () => {
  let complete!: (value: unknown) => void;
  const delayed = new Promise((resolve) => {
    complete = resolve;
  });
  vi.mocked(getAuthSession)
    .mockResolvedValueOnce(response(view()) as never)
    .mockReturnValueOnce(delayed as never);
  vi.mocked(nativeLogout).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: { data: {}, requestId: "test" },
  } as never);
  render(
    <MemoryRouter>
      <AuthProvider>
        <Consumer />
      </AuthProvider>
    </MemoryRouter>,
  );
  expect(await screen.findByText("native")).toBeVisible();
  fireEvent.click(screen.getByText("refresh"));
  fireEvent.click(screen.getByText("logout"));
  await waitFor(() =>
    expect(screen.getByTestId("logged")).toHaveTextContent("true"),
  );
  complete(response(view()));
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(screen.getByTestId("logged")).toHaveTextContent("true");
  expect(screen.getByTestId("roles")).toHaveTextContent("none");
});

it("aborts old work and clears the identity while server logout is pending", async () => {
  let complete!: (value: unknown) => void;
  vi.mocked(getAuthSession).mockResolvedValue(response(view()) as never);
  vi.mocked(nativeLogout).mockReturnValue(
    new Promise((resolve) => {
      complete = resolve;
    }) as never,
  );
  render(
    <MemoryRouter>
      <AuthProvider>
        <Consumer />
      </AuthProvider>
    </MemoryRouter>,
  );
  expect(await screen.findByText("native")).toBeVisible();
  const oldSignal = requestOptions().signal;
  fireEvent.click(screen.getByText("logout"));
  await waitFor(() =>
    expect(screen.getByTestId("logged")).toHaveTextContent("true"),
  );
  expect(oldSignal?.aborted).toBe(true);
  const logoutOptions = vi.mocked(nativeLogout).mock.calls[0][1] as RequestInit;
  expect(logoutOptions.signal?.aborted).toBe(false);
  expect(
    (logoutOptions.headers as Record<string, string>)["X-CSRF-Token"],
  ).toBe("csrf-stable-token");
  complete({
    status: 200,
    headers: new Headers(),
    data: { data: {}, requestId: "logout" },
  });
});

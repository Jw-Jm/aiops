import { useState } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { AuthProvider, useAuth } from "../src/auth/AuthProvider";
import * as oidcModule from "../src/auth/oidc";
import { Login } from "../src/pages/Login";
import { FirstPassword } from "../src/pages/FirstPassword";
import type { NativeSessionViewV1 } from "../src/api/generated/models";

vi.mock("../src/auth/oidc", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../src/auth/oidc")>();
  return { ...actual, getManager: vi.fn(), restore: vi.fn() };
});

function view(mode: "full" | "first_password_only"): NativeSessionViewV1 {
  const full = mode === "full";
  return {
    sessionRef: "session:fetch",
    subject: "user:fetch",
    tenantId: "00000000-0000-4000-8000-000000000001",
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
      token: "derived-csrf-token",
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
              scope: { clusterUids: ["k8s-uid-a"], namespaces: ["default"] },
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
            clusterUid: "k8s-uid-a",
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

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

type FetchCall = { url: string; init?: RequestInit };

let calls: FetchCall[] = [];
let initialView: NativeSessionViewV1 | null = null;
let loginView: NativeSessionViewV1;
let changeView: NativeSessionViewV1;
let loginGate: Promise<void> | null = null;
let logoutGate: Promise<void> | null = null;
let loginFailure = false;

function installFetch() {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ url, init });
      if (url.endsWith("/runtime-config.json")) {
        return json({ auth: { mode: "native" } });
      }
      if (url.endsWith("/api/v1/auth/session")) {
        if (initialView) return json({ data: initialView, requestId: "init" });
        return json(
          {
            code: "UNAUTHENTICATED",
            message: "not signed in",
            requestId: "init",
            retryable: false,
          },
          401,
        );
      }
      if (url.endsWith("/api/v1/auth/login")) {
        if (loginGate) await loginGate;
        if (loginFailure) {
          return json(
            {
              code: "UNAUTHENTICATED",
              message: "rejected",
              requestId: "login",
              retryable: false,
            },
            401,
          );
        }
        return json({ data: loginView, requestId: "login" }, 201);
      }
      if (url.endsWith("/api/v1/auth/password:change")) {
        return json({ data: changeView, requestId: "change" }, 201);
      }
      if (url.endsWith("/api/v1/auth/logout")) {
        if (logoutGate) await logoutGate;
        return json({ data: {}, requestId: "logout" });
      }
      return json(
        { code: "NOT_FOUND", message: url, requestId: "x", retryable: false },
        404,
      );
    }),
  );
}

function LoginHarness() {
  const auth = useAuth();
  return (
    <div>
      <span data-testid="session">{auth.session ? "yes" : "no"}</span>
      <span data-testid="subject">{auth.session?.subject ?? "none"}</span>
      <button
        onClick={() =>
          void auth.nativeLogin("late.admin", "late-password").catch(() => {})
        }
      >
        login
      </button>
      <button onClick={() => void auth.logout()}>logout</button>
    </div>
  );
}

beforeEach(() => {
  vi.spyOn(oidcModule, "restore").mockResolvedValue(null);
  vi.spyOn(oidcModule, "getManager").mockResolvedValue({
    events: {
      addUserLoaded: vi.fn(),
      removeUserLoaded: vi.fn(),
      addAccessTokenExpired: vi.fn(),
      removeAccessTokenExpired: vi.fn(),
    },
    signoutRedirect: vi.fn(),
  } as never);
  initialView = null;
  logoutGate = null;
  loginGate = null;
  loginFailure = false;
  loginView = view("full");
  changeView = view("full");
  installFetch();
});

it("posts the real Login form through generated fetch and does not return to /login", async () => {
  render(
    <MemoryRouter initialEntries={["/login"]}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/overview" element={<div>overview home</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
  fireEvent.change(await screen.findByLabelText("用户名"), {
    target: { value: "fetch.admin" },
  });
  fireEvent.change(screen.getByLabelText("密码"), {
    target: { value: "native-fetch-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: /登\s*录/ }));
  expect(await screen.findByText("overview home")).toBeVisible();
  const loginCall = calls.find((call) =>
    call.url.endsWith("/api/v1/auth/login"),
  );
  expect(loginCall).toBeTruthy();
  expect(JSON.parse(String(loginCall?.init?.body))).toEqual({
    loginName: "fetch.admin",
    password: "native-fetch-password",
  });
  expect(calls.some((call) => call.url.includes("/api/v1/clusters"))).toBe(
    false,
  );
});

it("recovers CSRF on first-password route and sends it with the real generated change request", async () => {
  initialView = view("first_password_only");
  changeView = view("full");
  changeView.authorization.grants[0] = {
    ...changeView.authorization.grants[0],
    role: "platform_admin",
    scope: { clusterUids: [], namespaces: [] },
  };
  changeView.authorizedClusters = [];
  render(
    <MemoryRouter initialEntries={["/login"]}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/auth/first-password" element={<FirstPassword />} />
          <Route path="/access/sources" element={<div>admin home</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
  fireEvent.change(await screen.findByLabelText("当前密码"), {
    target: { value: "initial-password" },
  });
  fireEvent.change(screen.getByLabelText("新密码"), {
    target: { value: "replacement-password" },
  });
  fireEvent.change(screen.getByLabelText("确认新密码"), {
    target: { value: "replacement-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: /设置密码并继续/ }));
  expect(await screen.findByText("admin home")).toBeVisible();
  const changeCall = calls.find((call) =>
    call.url.endsWith("/api/v1/auth/password:change"),
  );
  expect(changeCall).toBeTruthy();
  expect(
    (changeCall?.init?.headers as Record<string, string>)["X-CSRF-Token"],
  ).toBe("derived-csrf-token");
  expect(JSON.parse(String(changeCall?.init?.body))).toEqual({
    currentPassword: "initial-password",
    newPassword: "replacement-password",
    expectedCredentialVersion: 1,
  });
});

it("accepts the legacy SP08 OIDC runtime config without auth.mode", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) =>
      String(input).endsWith("/runtime-config.json")
        ? json({
            oidc: { authority: "https://identity.example" },
            release: "r1",
          })
        : json(
            {
              code: "UNAUTHENTICATED",
              message: "no session",
              requestId: "x",
              retryable: false,
            },
            401,
          ),
    ),
  );
  function Probe() {
    const auth = useAuth();
    return <span data-testid="mode">{auth.authMode}</span>;
  }
  render(
    <MemoryRouter>
      <AuthProvider>
        <Probe />
      </AuthProvider>
    </MemoryRouter>,
  );
  await waitFor(() =>
    expect(screen.getByTestId("mode")).toHaveTextContent("oidc"),
  );
});

it("fails closed on explicit malformed auth mode", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) =>
      String(input).endsWith("/runtime-config.json")
        ? json({ auth: { mode: "mystery" } })
        : json(
            {
              code: "UNAUTHENTICATED",
              message: "no session",
              requestId: "x",
              retryable: false,
            },
            401,
          ),
    ),
  );
  function Probe() {
    const auth = useAuth();
    return (
      <div>
        <span data-testid="failed">{String(auth.configFailed)}</span>
        <span data-testid="mode">{auth.authMode}</span>
      </div>
    );
  }
  render(
    <MemoryRouter>
      <AuthProvider>
        <Probe />
      </AuthProvider>
    </MemoryRouter>,
  );
  await waitFor(() =>
    expect(screen.getByTestId("failed")).toHaveTextContent("true"),
  );
  expect(screen.getByTestId("mode")).toHaveTextContent("oidc");
});

it("does not adopt a late nativeLogin success after logout started", async () => {
  let resolveLogin!: () => void;
  loginGate = new Promise<void>((resolve) => {
    resolveLogin = resolve;
  });
  render(
    <MemoryRouter initialEntries={["/"]}>
      <AuthProvider>
        <LoginHarness />
      </AuthProvider>
    </MemoryRouter>,
  );
  await waitFor(() =>
    expect(calls.some((call) => call.url.endsWith("/auth/session"))).toBe(true),
  );
  fireEvent.click(screen.getByText("login"));
  await waitFor(() =>
    expect(calls.some((call) => call.url.endsWith("/auth/login"))).toBe(true),
  );
  const loginSignal = calls.find((call) => call.url.endsWith("/auth/login"))
    ?.init?.signal;
  fireEvent.click(screen.getByText("logout"));
  expect(loginSignal?.aborted).toBe(true);
  resolveLogin();
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(screen.getByTestId("session")).toHaveTextContent("no");
});

it("does not surface a late nativeLogin failure after logout started", async () => {
  loginFailure = true;
  let resolveLogin!: () => void;
  loginGate = new Promise<void>((resolve) => {
    resolveLogin = resolve;
  });
  render(
    <MemoryRouter initialEntries={["/"]}>
      <AuthProvider>
        <LoginHarness />
      </AuthProvider>
    </MemoryRouter>,
  );
  await waitFor(() =>
    expect(calls.some((call) => call.url.endsWith("/auth/session"))).toBe(true),
  );
  fireEvent.click(screen.getByText("login"));
  await waitFor(() =>
    expect(calls.some((call) => call.url.endsWith("/auth/login"))).toBe(true),
  );
  fireEvent.click(screen.getByText("logout"));
  resolveLogin();
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(screen.getByTestId("session")).toHaveTextContent("no");
});

it("keeps a replacement identity when the previous logout completes late", async () => {
  initialView = view("full");
  loginView = {
    ...view("full"),
    subject: "user:replacement",
    sessionRef: "session:replacement",
  };
  let resolveLogout!: () => void;
  logoutGate = new Promise<void>((resolve) => {
    resolveLogout = resolve;
  });
  render(
    <MemoryRouter>
      <AuthProvider>
        <LoginHarness />
      </AuthProvider>
    </MemoryRouter>,
  );
  await waitFor(() =>
    expect(screen.getByTestId("session")).toHaveTextContent("yes"),
  );
  fireEvent.click(screen.getByText("logout"));
  await waitFor(() =>
    expect(screen.getByTestId("session")).toHaveTextContent("no"),
  );
  fireEvent.click(screen.getByText("login"));
  await waitFor(() =>
    expect(screen.getByTestId("subject")).toHaveTextContent("user:replacement"),
  );
  await act(async () => {
    resolveLogout();
  });
  expect(screen.getByTestId("subject")).toHaveTextContent("user:replacement");
});

function LoginPageHarness() {
  const auth = useAuth();
  const [visible, setVisible] = useState(true);
  return (
    <div>
      <span data-testid="session">{auth.session ? "yes" : "no"}</span>
      <button onClick={() => setVisible(false)}>leave</button>
      {visible ? <Login /> : <span>other page</span>}
    </div>
  );
}

it("aborts an in-flight login when the login page unmounts", async () => {
  let resolveLogin!: () => void;
  loginGate = new Promise<void>((resolve) => {
    resolveLogin = resolve;
  });
  render(
    <MemoryRouter initialEntries={["/login"]}>
      <AuthProvider>
        <LoginPageHarness />
      </AuthProvider>
    </MemoryRouter>,
  );
  fireEvent.change(await screen.findByLabelText("用户名"), {
    target: { value: "cancel.admin" },
  });
  fireEvent.change(screen.getByLabelText("密码"), {
    target: { value: "cancel-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: /登\s*录/ }));
  await waitFor(() =>
    expect(calls.some((call) => call.url.endsWith("/api/v1/auth/login"))).toBe(
      true,
    ),
  );
  const signal = calls.find((call) => call.url.endsWith("/api/v1/auth/login"))
    ?.init?.signal;
  fireEvent.click(screen.getByText("leave"));
  expect(signal?.aborted).toBe(true);
  await act(async () => {
    resolveLogin();
  });
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(screen.getByTestId("session")).toHaveTextContent("no");
});

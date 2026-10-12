import { it, expect, vi } from "vitest";
import {
  render,
  screen,
  fireEvent,
  act,
  waitFor,
} from "@testing-library/react";
import { User } from "oidc-client-ts";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { AuthProvider, useAuth } from "../src/auth/AuthProvider";
import { Guard } from "../src/auth/guards";
import { authEvents } from "../src/api/client";
import { getAuthSession } from "../src/api/generated/platform";
const events = vi.hoisted(() => ({
  addUserLoaded: vi.fn(),
  removeUserLoaded: vi.fn(),
  addAccessTokenExpired: vi.fn(),
  removeAccessTokenExpired: vi.fn(),
}));
vi.mock("../src/auth/oidc", () => ({
  getManager: async () => ({ events, signoutRedirect: vi.fn() }),
  restore: async () =>
    new User({
      access_token: "test-token",
      token_type: "Bearer",
      profile: {
        sub: "test-subject",
        iss: "https://identity.example.invalid",
        aud: "test-client",
        iat: 1,
        exp: 9999999999,
      },
    }),
  login: vi.fn(),
}));
vi.mock("../src/api/generated/platform", () => ({ getAuthSession: vi.fn() }));
function Consumer() {
  const auth = useAuth();
  return (
    <>
      <button onClick={() => void auth.refresh()}>刷新测试授权</button>
      <span>{auth.user ? "身份已验证" : "身份未验证"}</span>
      <Routes>
        <Route element={<Guard />}>
          <Route path="/" element={<div>已授权工作区</div>} />
        </Route>
      </Routes>
    </>
  );
}
it("keeps a valid OIDC identity distinct from absent grants and can recover fresh authorization", async () => {
  vi.mocked(getAuthSession).mockResolvedValueOnce({
    status: 403,
    headers: new Headers(),
    data: {
      code: "FORBIDDEN" as const,
      message: "no active role binding",
      requestId: "test-request",
      retryable: false,
    },
  });
  render(
    <MemoryRouter>
      <AuthProvider>
        <Consumer />
      </AuthProvider>
    </MemoryRouter>,
  );
  expect(await screen.findByText("当前身份没有可用授权")).toBeVisible();
  expect(screen.getByText("身份已验证")).toBeVisible();
  expect(screen.queryByText("已授权工作区")).not.toBeInTheDocument();
  expect(screen.queryByText("使用 OIDC 登录")).not.toBeInTheDocument();
  vi.mocked(getAuthSession).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: {
      requestId: "test",
      data: {
        authorizedClusters: [],
        subject: "test-subject",
        tenantId: "test-tenant",
        tenantIds: ["test-tenant"],
        roles: ["operator"],
        clusterScopes: [],
        namespaceScopes: [],
        tokenExpiresAt: "2099-01-01T00:00:00Z",
        acr: "urn:ops:loa:1",
      },
    },
  });
  fireEvent.click(screen.getByText("刷新测试授权"));
  expect(await screen.findByText("已授权工作区")).toBeVisible();
  expect(screen.queryByText("当前身份没有可用授权")).not.toBeInTheDocument();
});

it("refreshes the current authenticated identity immediately after an API denial, then returns to login on expiry", async () => {
  vi.mocked(getAuthSession)
    .mockReset()
    .mockResolvedValue({
      status: 200,
      headers: new Headers(),
      data: {
        requestId: "test",
        data: {
          authorizedClusters: [],
          subject: "test-subject",
          tenantId: "test-tenant",
          tenantIds: ["test-tenant"],
          roles: ["operator"],
          clusterScopes: [],
          namespaceScopes: [],
          tokenExpiresAt: "2099-01-01T00:00:00Z",
          acr: "urn:ops:loa:1",
        },
      },
    });
  render(
    <MemoryRouter>
      <AuthProvider>
        <Consumer />
      </AuthProvider>
    </MemoryRouter>,
  );
  expect(await screen.findByText("已授权工作区")).toBeVisible();
  vi.mocked(getAuthSession).mockResolvedValue({
    status: 403,
    headers: new Headers(),
    data: {
      code: "FORBIDDEN" as const,
      message: "revoked",
      requestId: "test",
      retryable: false,
    },
  });
  act(() => authEvents.dispatchEvent(new Event("forbidden")));
  expect(await screen.findByText("当前身份没有可用授权")).toBeVisible();
  act(() => authEvents.dispatchEvent(new Event("expired")));
  expect(await screen.findByText("使用 OIDC 登录")).toBeVisible();
  expect(screen.getByText("身份未验证")).toBeVisible();
  expect(screen.queryByText("当前身份没有可用授权")).not.toBeInTheDocument();
  expect(screen.queryByText("身份会话不可用")).not.toBeInTheDocument();
});

const grantedResponse = () => ({
  status: 200 as const,
  headers: new Headers(),
  data: {
    requestId: "test",
    data: {
      authorizedClusters: [],
      subject: "test-subject",
      tenantId: "test-tenant",
      tenantIds: ["test-tenant"],
      roles: ["operator" as const],
      clusterScopes: [],
      namespaceScopes: [],
      tokenExpiresAt: "2099-01-01T00:00:00Z",
      acr: "urn:ops:loa:1",
    },
  },
});
const deniedResponse = () => ({
  status: 403 as const,
  headers: new Headers(),
  data: {
    code: "FORBIDDEN" as const,
    message: "revoked",
    requestId: "test",
    retryable: false,
  },
});
for (const lateResponse of [grantedResponse, deniedResponse])
  it(
    "ignores a delayed " +
      lateResponse().status +
      " authorization response after expiry",
    async () => {
      let complete!: (
        value: Awaited<ReturnType<typeof getAuthSession>>,
      ) => void;
      const delayed = new Promise<Awaited<ReturnType<typeof getAuthSession>>>(
        (resolve) => {
          complete = resolve;
        },
      );
      vi.mocked(getAuthSession)
        .mockReset()
        .mockResolvedValueOnce(grantedResponse())
        .mockReturnValueOnce(delayed);
      render(
        <MemoryRouter>
          <AuthProvider>
            <Consumer />
          </AuthProvider>
        </MemoryRouter>,
      );
      expect(await screen.findByText("已授权工作区")).toBeVisible();
      fireEvent.click(screen.getByText("刷新测试授权"));
      await waitFor(() => expect(getAuthSession).toHaveBeenCalledTimes(2));
      act(() => authEvents.dispatchEvent(new Event("expired")));
      await act(async () => complete(lateResponse()));
      expect(screen.getByText("使用 OIDC 登录")).toBeVisible();
      expect(screen.queryByText("已授权工作区")).not.toBeInTheDocument();
      expect(
        screen.queryByText("当前身份没有可用授权"),
      ).not.toBeInTheDocument();
    },
  );
it("rechecks a forbidden notification received during a stale successful refresh", async () => {
  let complete!: (value: Awaited<ReturnType<typeof getAuthSession>>) => void;
  const delayed = new Promise<Awaited<ReturnType<typeof getAuthSession>>>(
    (resolve) => {
      complete = resolve;
    },
  );
  vi.mocked(getAuthSession)
    .mockReset()
    .mockResolvedValueOnce(grantedResponse())
    .mockReturnValueOnce(delayed)
    .mockResolvedValueOnce(deniedResponse());
  render(
    <MemoryRouter>
      <AuthProvider>
        <Consumer />
      </AuthProvider>
    </MemoryRouter>,
  );
  expect(await screen.findByText("已授权工作区")).toBeVisible();
  fireEvent.click(screen.getByText("刷新测试授权"));
  await waitFor(() => expect(getAuthSession).toHaveBeenCalledTimes(2));
  act(() => authEvents.dispatchEvent(new Event("forbidden")));
  await act(async () => complete(grantedResponse()));
  expect(await screen.findByText("当前身份没有可用授权")).toBeVisible();
  expect(getAuthSession).toHaveBeenCalledTimes(3);
});

it("does not let an old delayed 401 expire a newly accepted identity", async () => {
  let complete!: (value: Awaited<ReturnType<typeof getAuthSession>>) => void;
  const delayed = new Promise<Awaited<ReturnType<typeof getAuthSession>>>(
    (resolve) => {
      complete = resolve;
    },
  );
  vi.mocked(getAuthSession)
    .mockReset()
    .mockResolvedValueOnce(grantedResponse())
    .mockReturnValueOnce(delayed)
    .mockResolvedValueOnce(grantedResponse());
  render(
    <MemoryRouter>
      <AuthProvider>
        <Consumer />
      </AuthProvider>
    </MemoryRouter>,
  );
  expect(await screen.findByText("已授权工作区")).toBeVisible();
  fireEvent.click(screen.getByText("刷新测试授权"));
  await waitFor(() => expect(getAuthSession).toHaveBeenCalledTimes(2));
  await act(async () => {
    await events.addUserLoaded.mock.calls.at(-1)![0](
      new User({
        access_token: "new-test-token",
        token_type: "Bearer",
        profile: {
          sub: "test-subject",
          iss: "https://identity.example.invalid",
          aud: "test-client",
          iat: 1,
          exp: 9999999999,
        },
      }),
    );
  });
  await act(async () =>
    complete({
      status: 401,
      headers: new Headers(),
      data: {
        code: "UNAUTHENTICATED",
        message: "old token expired",
        requestId: "test",
        retryable: false,
      },
    }),
  );
  expect(screen.getByText("已授权工作区")).toBeVisible();
  expect(screen.queryByText("使用 OIDC 登录")).not.toBeInTheDocument();
});

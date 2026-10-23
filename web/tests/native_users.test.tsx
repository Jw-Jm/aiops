// UI mechanism fixture; no database, browser visual or live authorization claim.
import { beforeEach, expect, it, vi } from "vitest";
import {
  render,
  screen,
  fireEvent,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { UsersPage } from "../src/pages/admin/UsersPage";
import {
  createNativeUser,
  getNativeUsers,
  getNativeSubjectMappings,
  createNativeSubjectMapping,
  createRoleBinding,
} from "../src/api/generated/platform";
import { setNativeCredentials } from "../src/api/client";
vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    authMode: "native",
    nativeSession: {
      sessionRef: "session:fixture",
      revocation: { sessionVersion: 1 },
      csrf: { token: "fixture-csrf-key-0001" },
    },
  }),
}));
vi.mock("../src/app/ClusterContext", () => ({
  useClusterScope: () => ({ configCandidates: [] }),
  useConfigDraft: vi.fn(),
}));
vi.mock("../src/auth/NativeStepUpControl", () => ({
  NativeStepUpControl: ({
    onReady,
  }: {
    onReady: (expires: string, id: string) => void;
  }) => (
    <button onClick={() => onReady("2099-01-01T00:00:00Z", "fixture-proof")}>
      fixture verification
    </button>
  ),
}));
vi.mock("../src/api/generated/platform", async (original) => ({
  ...(await original<object>()),
  getNativeUsers: vi.fn(),
  createNativeUser: vi.fn(),
  getNativeSubjectMappings: vi.fn(),
  createNativeSubjectMapping: vi.fn(),
  createRoleBinding: vi.fn(),
}));
const response = (data: unknown, status = 200) => ({
  status,
  headers: new Headers(),
  data: { data, meta: {}, requestId: "unit" },
});
beforeEach(() => {
  vi.clearAllMocks();
  setNativeCredentials("fixture-tenant", "fixture-csrf-key-0001");
  vi.mocked(getNativeSubjectMappings).mockResolvedValue(response([]) as never);
  vi.mocked(createNativeSubjectMapping).mockResolvedValue(
    response({}, 201) as never,
  );
  vi.mocked(getNativeUsers).mockResolvedValue(response([]) as never);
  vi.mocked(createNativeUser).mockResolvedValue(response({}, 201) as never);
  vi.mocked(createRoleBinding).mockResolvedValue(response({}, 201) as never);
});

it("sends explicit empty scope arrays for an independent administrator grant", async () => {
  const userId = "018f0f2b-91c2-7d42-a8dc-f719c5987201";
  vi.mocked(getNativeUsers).mockResolvedValue(
    response([
      {
        userId,
        loginName: "native.user",
        displayName: "Native User",
        status: "active",
        revision: 4,
        roleGrants: [],
        revocation: { authorizationVersion: 3 },
        mustChangePassword: false,
      },
    ]) as never,
  );
  render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: {
            queries: { retry: false },
            mutations: { retry: false },
          },
        })
      }
    >
      <UsersPage />
    </QueryClientProvider>,
  );
  await screen.findByText("Native User");
  fireEvent.click(screen.getByRole("button", { name: "配置授权" }));
  const dialog = within(screen.getByRole("dialog", { name: "配置用户授权" }));
  fireEvent.mouseDown(dialog.getByRole("combobox", { name: "角色" }));
  fireEvent.click(await screen.findByText("平台管理员"));
  fireEvent.change(dialog.getByLabelText("操作原因"), {
    target: { value: "independent administrator grant" },
  });
  fireEvent.click(dialog.getByText("fixture verification"));
  fireEvent.click(dialog.getByRole("button", { name: "确认提交" }));
  await waitFor(() => expect(createRoleBinding).toHaveBeenCalledTimes(1));
  expect(vi.mocked(createRoleBinding).mock.calls[0][0]).toEqual({
    userId,
    role: "platform_admin",
    scope: { clusterUids: [], namespaces: [] },
    expectedAuthorizationVersion: 3,
    reason: "independent administrator grant",
  });
});
it("requires matching passwords and a fresh operation proof; creation sends no role/tenant claims", async () => {
  render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: {
            queries: { retry: false },
            mutations: { retry: false },
          },
        })
      }
    >
      <UsersPage />
    </QueryClientProvider>,
  );
  await waitFor(() => expect(getNativeUsers).toHaveBeenCalled());
  fireEvent.click(screen.getByRole("button", { name: /创建用户/ }));
  const dialog = within(screen.getByRole("dialog", { name: "创建内置账号" }));
  fireEvent.change(dialog.getByLabelText("用户名"), {
    target: { value: "new.user" },
  });
  fireEvent.change(dialog.getByLabelText("姓名"), {
    target: { value: "New User" },
  });
  fireEvent.change(dialog.getByLabelText("密码"), {
    target: { value: "fixture-password-only" },
  });
  expect(dialog.getByRole("button", { name: "确认提交" })).toBeDisabled();
  fireEvent.change(dialog.getByLabelText("确认密码"), {
    target: { value: "fixture-password-only" },
  });
  fireEvent.click(dialog.getByText("fixture verification"));
  expect(dialog.getByRole("button", { name: "确认提交" })).toBeEnabled();
  fireEvent.change(dialog.getByLabelText("姓名"), {
    target: { value: "Updated User" },
  });
  expect(dialog.getByRole("button", { name: "确认提交" })).toBeDisabled();
  fireEvent.click(dialog.getByText("fixture verification"));
  fireEvent.click(dialog.getByRole("button", { name: "确认提交" }));
  await waitFor(() => expect(createNativeUser).toHaveBeenCalledTimes(1));
  expect(vi.mocked(createNativeUser).mock.calls[0][0]).toEqual({
    loginName: "new.user",
    displayName: "Updated User",
    initialPassword: "fixture-password-only",
  });
  expect(
    new Headers(vi.mocked(createNativeUser).mock.calls[0][1]?.headers).get(
      "X-Ops-Step-Up-Challenge",
    ),
  ).toBe("fixture-proof");
});

it("binds historical mapping to exact reviewed subject, native ID and version; editing invalidates proof", async () => {
  const userId = "018f0f2b-91c2-7d42-a8dc-f719c5987201";
  vi.mocked(getNativeUsers).mockResolvedValue(
    response([
      {
        userId,
        loginName: "native.user",
        displayName: "Native User",
        status: "active",
        revision: 4,
        roleGrants: [],
        mustChangePassword: false,
      },
    ]) as never,
  );
  render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: {
            queries: { retry: false },
            mutations: { retry: false },
          },
        })
      }
    >
      <UsersPage />
    </QueryClientProvider>,
  );
  await screen.findByText("Native User");
  fireEvent.click(screen.getByRole("tab", { name: "历史主体映射" }));
  await waitFor(() => expect(getNativeSubjectMappings).toHaveBeenCalled());
  fireEvent.click(screen.getByRole("button", { name: "映射历史主体" }));
  const dialog = within(screen.getByRole("dialog", { name: "映射历史主体" }));
  expect(dialog.getByRole("button", { name: "确认映射" })).toBeDisabled();
  fireEvent.mouseDown(dialog.getByRole("combobox", { name: "映射目标用户" }));
  fireEvent.click(
    await screen.findByText(`Native User · native.user · ${userId}`),
  );
  fireEvent.change(dialog.getByLabelText("历史主体"), {
    target: { value: "legacy:exact-subject" },
  });
  fireEvent.change(dialog.getByLabelText("已核对清单摘要"), {
    target: { value: "sha256:" + "a".repeat(64) },
  });
  fireEvent.click(dialog.getByText("fixture verification"));
  expect(dialog.getByRole("button", { name: "确认映射" })).toBeEnabled();
  fireEvent.change(dialog.getByLabelText("历史主体"), {
    target: { value: "legacy:another-subject" },
  });
  expect(dialog.getByRole("button", { name: "确认映射" })).toBeDisabled();
  fireEvent.click(dialog.getByText("fixture verification"));
  fireEvent.click(dialog.getByRole("button", { name: "确认映射" }));
  await waitFor(() =>
    expect(createNativeSubjectMapping).toHaveBeenCalledTimes(1),
  );
  expect(vi.mocked(createNativeSubjectMapping).mock.calls[0][0]).toEqual({
    legacySubject: "legacy:another-subject",
    userId,
    basis: "verified_inventory",
    evidenceDigest: "sha256:" + "a".repeat(64),
    expectedUserRevision: 4,
  });
});

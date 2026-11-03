import { beforeEach, expect, it, vi } from "vitest";
import {
  fireEvent,
  render as renderUI,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
function render(ui: ReactNode) {
  return renderUI(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: { queries: { retry: false, gcTime: 0 } },
        })
      }
    >
      {ui}
    </QueryClientProvider>,
  );
}
import { MemoryRouter } from "react-router-dom";
import { ApiError, setNativeCredentials } from "../src/api/client";
import { AccountSecurity } from "../src/pages/AccountSecurity";
import {
  createNativeMFAEnrollment,
  createNativeStepUpChallenge,
  getNativeMFAStatus,
  getNativeUsers,
  resetNativeUserMFA,
  verifyNativeMFAEnrollment,
  verifyNativeStepUpChallenge,
} from "../src/api/generated/platform";
const auth = vi.hoisted(() => ({
  authMode: "native",
  refresh: vi.fn(),
  nativeSession: {
    subject: "user:one",
    sessionRef: "session:one",
    accountRevision: 1,
    acr: "urn:ops:loa:1",
    revocation: { credentialVersion: 1, sessionVersion: 1 },
    authorization: { grants: [] as { role: string }[] },
  },
}));
vi.mock("../src/auth/AuthProvider", () => ({ useAuth: () => auth }));
vi.mock("../src/api/generated/platform", () => ({
  createNativeMFAEnrollment: vi.fn(),
  createNativeStepUpChallenge: vi.fn(),
  getNativeMFAStatus: vi.fn(),
  getNativeUsers: vi.fn(),
  verifyNativeMFAEnrollment: vi.fn(),
  verifyNativeStepUpChallenge: vi.fn(),
  resetNativeUserMFA: vi.fn(),
  verifyNativeRecoveryCode: vi.fn(),
}));
const response = (data: unknown) => ({
  status: 200,
  headers: new Headers(),
  data: { data, meta: {}, requestId: "unit" },
});
beforeEach(() => {
  vi.clearAllMocks();
  auth.nativeSession.authorization.grants = [];
  vi.mocked(getNativeUsers).mockResolvedValue(response([]) as never);
  setNativeCredentials("tenant-one", "synthetic-csrf");
  vi.mocked(verifyNativeStepUpChallenge).mockReset();
  vi.mocked(getNativeMFAStatus).mockResolvedValue(
    response({ state: "confirmed", recoveryCodesRemaining: 10 }) as never,
  );
  vi.mocked(createNativeMFAEnrollment).mockResolvedValue(
    response({
      enrollmentId: "new-factor",
      otpauthUri: "otpauth://totp/unit?secret=SYNTHETIC",
      expiresAt: "2099-01-01T00:00:00Z",
    }) as never,
  );
  vi.mocked(createNativeStepUpChallenge).mockResolvedValue(
    response({ challengeId: "challenge", revision: 1 }) as never,
  );
});
it("creates a challenge from the binding form, retries errors at the server revision and separately confirms the new factor", async () => {
  vi.mocked(verifyNativeStepUpChallenge)
    .mockResolvedValueOnce(
      response({
        completedMethods: [],
        revision: 2,
        failureCode: "invalid_credential",
      }) as never,
    )
    .mockResolvedValueOnce(
      response({
        completedMethods: ["password"],
        revision: 3,
        failureCode: null,
      }) as never,
    )
    .mockResolvedValueOnce(
      response({
        completedMethods: ["password"],
        revision: 4,
        failureCode: "invalid_credential",
        stepUpSession: null,
      }) as never,
    )
    .mockResolvedValueOnce(
      response({
        completedMethods: ["password", "totp"],
        revision: 5,
        failureCode: null,
        stepUpSession: { sessionRef: "stepup:proof" },
      }) as never,
    );
  vi.mocked(verifyNativeMFAEnrollment).mockResolvedValue(
    response({ recoveryCodes: { codes: [] } }) as never,
  );
  render(
    <MemoryRouter>
      <AccountSecurity />
    </MemoryRouter>,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "重新绑定双因素" }),
  );
  await waitFor(() =>
    expect(createNativeStepUpChallenge).toHaveBeenCalledTimes(1),
  );
  const password = screen.getByLabelText("当前密码");
  fireEvent.change(password, { target: { value: "wrong" } });
  fireEvent.click(screen.getByRole("button", { name: "验证密码" }));
  await screen.findByText("当前密码不正确，挑战仍待完成。");
  fireEvent.change(password, { target: { value: "right" } });
  fireEvent.click(screen.getByRole("button", { name: "验证密码" }));
  await waitFor(() =>
    expect(verifyNativeStepUpChallenge).toHaveBeenCalledTimes(2),
  );
  await screen.findByText("密码已验证，请提交身份验证器中的 6 位动态码。");
  const totp = screen.getByLabelText("动态码");
  fireEvent.change(totp, { target: { value: "000000" } });
  fireEvent.click(screen.getByRole("button", { name: "验证动态码" }));
  await screen.findByText("动态码不正确或该挑战已失效。");
  fireEvent.change(totp, { target: { value: "123456" } });
  fireEvent.click(screen.getByRole("button", { name: "验证动态码" }));
  await screen.findByLabelText("新绑定动态码");
  expect(
    vi
      .mocked(verifyNativeStepUpChallenge)
      .mock.calls.map((c) => c[1].expectedChallengeRevision),
  ).toEqual([1, 2, 3, 4]);
  expect(verifyNativeMFAEnrollment).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("新绑定动态码"), {
    target: { value: "654321" },
  });
  fireEvent.click(screen.getByRole("button", { name: "确认新绑定" }));
  await waitFor(() =>
    expect(verifyNativeMFAEnrollment).toHaveBeenCalledWith(
      "new-factor",
      expect.objectContaining({
        totpCode: "654321",
        stepUp: { challengeId: "challenge", sessionRef: "session:one" },
      }),
      expect.anything(),
    ),
  );
});

it("cancels in-flight verification and discards a late factor response", async () => {
  let complete!: (value: never) => void;
  vi.mocked(verifyNativeStepUpChallenge).mockReturnValue(
    new Promise((resolve) => {
      complete = resolve;
    }) as never,
  );
  render(
    <MemoryRouter>
      <AccountSecurity />
    </MemoryRouter>,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "重新绑定双因素" }),
  );
  await waitFor(() =>
    expect(createNativeStepUpChallenge).toHaveBeenCalledTimes(1),
  );
  fireEvent.change(screen.getByLabelText("当前密码"), {
    target: { value: "synthetic-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: "验证密码" }));
  await waitFor(() =>
    expect(verifyNativeStepUpChallenge).toHaveBeenCalledTimes(1),
  );
  const signal = (
    vi.mocked(verifyNativeStepUpChallenge).mock.calls[0][2] as RequestInit
  ).signal;
  fireEvent.click(screen.getByRole("button", { name: "取消绑定" }));
  expect(signal?.aborted).toBe(true);
  complete(
    response({
      completedMethods: ["password"],
      revision: 2,
      failureCode: null,
    }) as never,
  );
  await screen.findByText(/已停止等待本次验证/);
  expect(screen.queryByLabelText("当前密码")).toBeNull();
  expect(screen.queryByLabelText("新绑定动态码")).toBeNull();
  expect(verifyNativeMFAEnrollment).not.toHaveBeenCalled();
});

it("surfaces the server step-up cooldown and clears the challenge instead of hinting at a wrong code", async () => {
  vi.mocked(verifyNativeStepUpChallenge).mockRejectedValue(
    new ApiError(429, "RATE_LIMITED", "budget", "req-rate"),
  );
  render(
    <MemoryRouter>
      <AccountSecurity />
    </MemoryRouter>,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "重新绑定双因素" }),
  );
  await waitFor(() =>
    expect(createNativeStepUpChallenge).toHaveBeenCalledTimes(1),
  );
  fireEvent.change(screen.getByLabelText("当前密码"), {
    target: { value: "right" },
  });
  fireEvent.click(screen.getByRole("button", { name: "验证密码" }));
  await screen.findByText(/验证失败次数已达上限/);
  expect(screen.queryByText("当前密码不正确，挑战仍待完成。")).toBeNull();
});

it("reports password-work capacity as retryable and keeps the challenge open", async () => {
  vi.mocked(verifyNativeStepUpChallenge).mockRejectedValue(
    new ApiError(503, "UNAVAILABLE", "busy", "req-busy"),
  );
  render(
    <MemoryRouter>
      <AccountSecurity />
    </MemoryRouter>,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "重新绑定双因素" }),
  );
  await waitFor(() =>
    expect(createNativeStepUpChallenge).toHaveBeenCalledTimes(1),
  );
  fireEvent.change(screen.getByLabelText("当前密码"), {
    target: { value: "right" },
  });
  fireEvent.click(screen.getByRole("button", { name: "验证密码" }));
  await screen.findByText(/本次请求未判定为密码错误/);
  expect(screen.getByLabelText("当前密码")).toBeTruthy();
});

it("uses the selected server-owned account and versions for administrator MFA reset", async () => {
  auth.nativeSession.authorization.grants = [{ role: "platform_admin" }];
  vi.mocked(getNativeUsers).mockResolvedValue(
    response([
      {
        userId: "018f0f2b-91c2-7d42-a8dc-f719c5987201",
        loginName: "target.user",
        displayName: "Target",
        revision: 7,
        revocation: { credentialVersion: 3 },
      },
    ]) as never,
  );
  vi.mocked(verifyNativeStepUpChallenge)
    .mockResolvedValueOnce(response({ stepUpSession: null }) as never)
    .mockResolvedValueOnce(
      response({ stepUpSession: { sessionRef: "stepup:proof" } }) as never,
    );
  vi.mocked(resetNativeUserMFA).mockResolvedValue(
    response({
      sessionsRevoked: 1,
      credentialVersion: 4,
      auditRecordId: "audit:reset",
    }) as never,
  );
  render(
    <MemoryRouter>
      <AccountSecurity />
    </MemoryRouter>,
  );
  const targetPicker = await screen.findByRole("combobox", {
    name: "目标账号（本页）",
  });
  await waitFor(() =>
    expect((targetPicker as HTMLInputElement).disabled).toBe(false),
  );
  fireEvent.mouseDown(targetPicker);
  fireEvent.click(await screen.findByText("Target / target.user"));
  expect(screen.queryByLabelText("目标账号 revision")).toBeNull();
  expect(screen.queryByLabelText("目标凭据版本")).toBeNull();
  fireEvent.change(screen.getByLabelText("确认登录名"), {
    target: { value: "target.user" },
  });
  fireEvent.change(screen.getByLabelText("原因"), {
    target: { value: "reviewed recovery" },
  });
  fireEvent.change(screen.getByLabelText("我的当前密码"), {
    target: { value: "actor password" },
  });
  fireEvent.change(screen.getByLabelText("我的动态码"), {
    target: { value: "123456" },
  });
  fireEvent.click(screen.getByRole("button", { name: "执行管理员重置" }));
  await waitFor(() => expect(resetNativeUserMFA).toHaveBeenCalledOnce());
  expect(
    vi.mocked(createNativeStepUpChallenge).mock.calls[0][0].target,
  ).toEqual({
    kind: "user",
    userId: "018f0f2b-91c2-7d42-a8dc-f719c5987201",
    revision: 7,
  });
  expect(vi.mocked(resetNativeUserMFA).mock.calls[0][1]).toMatchObject({
    userId: "018f0f2b-91c2-7d42-a8dc-f719c5987201",
    expectedUserRevision: 7,
    expectedCredentialVersion: 3,
    confirmLoginName: "target.user",
  });
});

it("does not invent an unconfigured factor or zero recovery codes while status is loading", async () => {
  let finish!: (value: never) => void;
  vi.mocked(getNativeMFAStatus).mockReturnValueOnce(
    new Promise((resolve) => {
      finish = resolve;
    }) as never,
  );
  render(
    <MemoryRouter>
      <AccountSecurity />
    </MemoryRouter>,
  );
  await waitFor(() => expect(getNativeMFAStatus).toHaveBeenCalledTimes(1));
  expect(screen.queryByText("未启用")).toBeNull();
  expect(screen.queryByText("剩余恢复码")).toBeNull();
  expect(screen.queryByRole("button", { name: "启用双因素" })).toBeNull();
  finish(response({ state: "confirmed", recoveryCodesRemaining: 7 }) as never);
  await screen.findByText("已启用");
  expect(screen.getByText("7")).toBeTruthy();
});

it("shows status failure with retry instead of offering enrollment from an unknown state", async () => {
  vi.mocked(getNativeMFAStatus).mockRejectedValueOnce(
    new ApiError(503, "UNAVAILABLE", "状态读取失败", "status-request"),
  );
  render(
    <MemoryRouter>
      <AccountSecurity />
    </MemoryRouter>,
  );
  await screen.findByText(/状态读取失败/);
  expect(screen.queryByText("未启用")).toBeNull();
  expect(screen.queryByText("剩余恢复码")).toBeNull();
  expect(screen.queryByRole("button", { name: "启用双因素" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
  await screen.findByRole("button", { name: "重新绑定双因素" });
  expect(getNativeMFAStatus).toHaveBeenCalledTimes(2);
});

it("discards a binding and its old assurance when the real challenge returns conflict", async () => {
  vi.mocked(verifyNativeStepUpChallenge).mockRejectedValue(
    new ApiError(409, "CONFLICT", "challenge not pending", "expired-request"),
  );
  render(
    <MemoryRouter>
      <AccountSecurity />
    </MemoryRouter>,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "重新绑定双因素" }),
  );
  await screen.findByLabelText("当前密码");
  fireEvent.change(screen.getByLabelText("当前密码"), {
    target: { value: "current-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: "验证密码" }));
  await screen.findByText("本次验证已失效或版本已变化，请重新开始验证。");
  expect(screen.queryByLabelText("当前密码")).toBeNull();
  expect(screen.queryByLabelText("新绑定动态码")).toBeNull();
  expect(screen.queryByText(/otpauth:\/\//)).toBeNull();
  expect(screen.queryByText(/已创建双因素绑定挑战/)).toBeNull();
  expect(verifyNativeMFAEnrollment).not.toHaveBeenCalled();
  const restart = await screen.findByRole("button", { name: /重新绑定双因素/ });
  await waitFor(() =>
    expect((restart as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(restart);
  await waitFor(() => expect(createNativeMFAEnrollment).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(createNativeStepUpChallenge).toHaveBeenCalledTimes(2));
});

it("does not call an expired verification a wrong password or retain its pending key", async () => {
  vi.mocked(verifyNativeStepUpChallenge).mockResolvedValue(
    response({
      challengeState: "expired",
      completedMethods: ["password"],
      revision: 2,
      failureCode: "invalid_credential",
    }) as never,
  );
  render(
    <MemoryRouter>
      <AccountSecurity />
    </MemoryRouter>,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "重新绑定双因素" }),
  );
  await screen.findByLabelText("当前密码");
  fireEvent.change(screen.getByLabelText("当前密码"), {
    target: { value: "current-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: "验证密码" }));
  await screen.findByText("本次验证已失效，请重新开始验证。");
  expect(screen.queryByText("当前密码不正确，挑战仍待完成。")).toBeNull();
  expect(screen.queryByText(/otpauth:\/\//)).toBeNull();
  expect(screen.queryByLabelText("当前密码")).toBeNull();
});

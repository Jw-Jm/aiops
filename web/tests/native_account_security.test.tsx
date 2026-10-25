import { beforeEach, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ApiError, setNativeCredentials } from "../src/api/client";
import { AccountSecurity } from "../src/pages/AccountSecurity";
import {
  createNativeMFAEnrollment,
  createNativeStepUpChallenge,
  getNativeMFAStatus,
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
    authorization: { grants: [] },
  },
}));
vi.mock("../src/auth/AuthProvider", () => ({ useAuth: () => auth }));
vi.mock("../src/api/generated/platform", () => ({
  createNativeMFAEnrollment: vi.fn(),
  createNativeStepUpChallenge: vi.fn(),
  getNativeMFAStatus: vi.fn(),
  verifyNativeMFAEnrollment: vi.fn(),
  verifyNativeStepUpChallenge: vi.fn(),
  resetNativeUserMFA: vi.fn(),
  verifyNativeRecoveryCode: vi.fn(),
}));
const response = (data: unknown) => ({
  status: 200,
  headers: new Headers(),
  data: { data, requestId: "unit" },
});
beforeEach(() => {
  vi.clearAllMocks();
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

import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { NativeStepUpControl } from "../src/auth/NativeStepUpControl";
import { setNativeCredentials } from "../src/api/client";
import {
  createNativeStepUpChallenge,
  verifyNativeStepUpChallenge,
} from "../src/api/generated/platform";
vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({ nativeSession: { revocation: { sessionVersion: 4 } } }),
}));
vi.mock("../src/api/generated/platform", () => ({
  createNativeStepUpChallenge: vi.fn(),
  verifyNativeStepUpChallenge: vi.fn(),
}));
const response = (data: unknown) => ({
  status: 200,
  headers: new Headers(),
  data: { data, requestId: "unit" },
});
beforeEach(() => {
  vi.clearAllMocks();
  setNativeCredentials("tenant", "csrf");
});
it("binds the exact request and retains the verified password when retrying TOTP", async () => {
  vi.mocked(createNativeStepUpChallenge).mockResolvedValue(
    response({ challengeId: "challenge", revision: 1 }) as never,
  );
  vi.mocked(verifyNativeStepUpChallenge)
    .mockResolvedValueOnce(
      response({
        completedMethods: ["password"],
        revision: 2,
        failureCode: null,
        verified: false,
      }) as never,
    )
    .mockResolvedValueOnce(
      response({
        completedMethods: ["password"],
        revision: 3,
        failureCode: "invalid_credential",
        challengeState: "pending",
      }) as never,
    )
    .mockResolvedValueOnce(
      response({
        completedMethods: ["password", "totp"],
        revision: 4,
        failureCode: null,
        verified: true,
        stepUpSession: { absoluteExpiresAt: "2099-01-01T00:00:00Z" },
      }) as never,
    );
  const ready = vi.fn();
  render(
    <NativeStepUpControl
      intent={{
        operation: "source_administration",
        target: {
          kind: "source_registration",
          registrationId: "source",
          revision: 7,
        },
        write: {
          method: "PATCH",
          path: "/api/v1/admin/source-registrations/source",
          body: { status: "disabled", expectedRevision: 7 },
        },
      }}
      onReady={ready}
    />,
  );
  fireEvent.change(screen.getByLabelText("验证密码"), {
    target: { value: "password" },
  });
  fireEvent.change(screen.getByLabelText("验证动态码"), {
    target: { value: "111111" },
  });
  fireEvent.click(screen.getByRole("button", { name: "验证本次操作" }));
  await screen.findByText("验证未通过，请核对凭据后重试。");
  expect(ready).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("验证动态码"), {
    target: { value: "222222" },
  });
  fireEvent.click(await screen.findByRole("button", { name: "验证本次操作" }));
  await waitFor(() =>
    expect(ready).toHaveBeenCalledWith("2099-01-01T00:00:00.000Z", "challenge"),
  );
  expect(
    vi
      .mocked(verifyNativeStepUpChallenge)
      .mock.calls.map((c) => [c[1].method, c[1].expectedChallengeRevision]),
  ).toEqual([
    ["password", 1],
    ["totp", 2],
    ["totp", 3],
  ]);
  const target = vi.mocked(createNativeStepUpChallenge).mock.calls[0][0].target;
  expect(target.write).toEqual({
    method: "PATCH",
    path: "/api/v1/admin/source-registrations/source",
    bodyDigest: expect.stringMatching(/^sha256:[a-f0-9]{64}$/),
  });
});

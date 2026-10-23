import { expect, it } from "vitest";
import { nativeWriteDigest } from "../src/auth/nativeWriteDigest";
it("matches the Go password-binding vector and separates session secrets", async () => {
  const body = {
    displayName: "Fixture",
    initialPassword: "fixture-password-only",
    loginName: "fixture",
  };
  const digest = await nativeWriteDigest(
    "fixture-csrf-key-0001",
    "POST",
    "/api/v1/admin/users",
    body,
  );
  expect(digest).toBe(
    "sha256:fc2b165f95a07f306fb21f5e74d14bcc50cfdb31f6b16429262f8e6f3dfae586",
  );
  expect(
    await nativeWriteDigest(
      "fixture-csrf-key-0002",
      "POST",
      "/api/v1/admin/users",
      body,
    ),
  ).not.toBe(digest);
  expect(
    await nativeWriteDigest(
      "fixture-csrf-key-0001",
      "POST",
      "/api/v1/admin/users",
      {
        loginName: body.loginName,
        initialPassword: body.initialPassword,
        displayName: body.displayName,
      },
    ),
  ).toBe(digest);
  await expect(
    nativeWriteDigest("", "POST", "/api/v1/admin/users", body),
  ).rejects.toThrow();
});

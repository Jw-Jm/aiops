import { it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryState } from "../src/components/QueryState";
import { ApiError } from "../src/api/client";
it("provides recoverable conflict and request identity", () => {
  render(
    <QueryState
      loading={false}
      error={
        new ApiError(409, "CONFLICT", "expectedRevision changed", "req-test")
      }
      retry={() => {}}
    />,
  );
  expect(
    screen.getByText("版本冲突，请刷新后重新检查变更"),
  ).toBeInTheDocument();
  expect(screen.getByText("请求 ID：req-test")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "重新加载" })).toBeInTheDocument();
});
it("distinguishes permission denial from deleted references", () => {
  render(
    <QueryState
      loading={false}
      error={new ApiError(403, "FORBIDDEN", "role revoked")}
    />,
  );
  expect(screen.getByText("没有此范围的访问权限")).toBeInTheDocument();
});

import { verifyAuditPage } from "../src/pages/admin/auditProof";
it("independently hashes exported canonical bytes and keeps archive chain unverified", async () => {
  const bytes = '{"actual":false,"count":0}';
  const hash = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(bytes),
  );
  const digest =
    "sha256:" +
    Array.from(new Uint8Array(hash), (b) =>
      b.toString(16).padStart(2, "0"),
    ).join("");
  const good = await verifyAuditPage({
    data: [
      { digestVerified: true, canonicalJSON: bytes, exportDigest: digest },
    ],
    meta: { chainVerification: "unverified" },
  });
  expect(good.status).toBe("verified");
  expect(good.chain).toBe("unverified");
  const corrupt = await verifyAuditPage({
    data: [
      {
        digestVerified: true,
        canonicalJSON: bytes + " ",
        exportDigest: digest,
      },
    ],
    meta: { chainVerification: "failed" },
  });
  expect(corrupt.failed).toBe(1);
  expect(corrupt.status).toBe("failed");
  expect(corrupt.chain).toBe("failed");
  expect(
    (await verifyAuditPage({ data: [{ digestVerified: false }] })).status,
  ).toBe("failed");
  expect((await verifyAuditPage({ data: [] })).status).toBe("unverified");
});

it("distinguishes a degraded source from a feature that is disabled", () => {
  const view = render(
    <QueryState
      loading={false}
      error={
        new ApiError(
          503,
          "SOURCE_DEGRADED",
          "upstream unavailable",
          "source-request",
        )
      }
    />,
  );
  expect(screen.getByText("数据来源暂不可用")).toBeVisible();
  expect(screen.getByText(/来源未能完成此次查询/)).toBeVisible();
  expect(screen.queryByText("此功能未启用")).not.toBeInTheDocument();
  view.rerender(
    <QueryState
      loading={false}
      error={new ApiError(503, "CAPABILITY_DISABLED", "deferred")}
    />,
  );
  expect(screen.getByText("此功能未启用")).toBeVisible();
  expect(screen.queryByText("数据来源暂不可用")).not.toBeInTheDocument();
});

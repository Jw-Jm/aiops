import { test, expect } from "./session";
test("real Incident audit distinguishes human confirmation, execution and post-check, and independently verifies exported digests", async ({
  page,
  config,
}) => {
  await page.goto(
    config.baseURL + "/incidents/" + config.incidentId + "?tab=audit",
  );
  await expect(
    page.getByText("按操作过程追查记录", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("接口暂时不可用", { exact: true })).toHaveCount(
    0,
  );
  await expect(
    page.getByRole("button", { name: "评估实际命令风险", exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "本人确认风险", exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "命令执行结束", exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "核对故障是否恢复", exact: true }).first(),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "校验当前页摘要", exact: true })
    .click();
  await expect(
    page.getByText("记录摘要：验证通过", { exact: true }),
  ).toBeVisible();
  const response = page.waitForResponse(
    (r) =>
      new URL(r.url()).pathname === "/api/v1/audit-records" &&
      new URL(r.url()).searchParams.get("verifyChain") === "true",
  );
  await page
    .getByRole("button", { name: "验证当前页签名归档链", exact: true })
    .click();
  const result = await response;
  expect(result.status()).toBe(200);
  const data = await result.json();
  expect(["verified", "failed", "partial", "unavailable"]).toContain(
    data.meta.chainVerification,
  );
  await expect(page.locator(".ant-skeleton")).toHaveCount(0);
  await page.screenshot({
    path: config.screenshotDirectory + "/operator-audit-readable.png",
    animations: "disabled",
  });
  console.log(
    "Signed archive chain status:",
    data.meta.chainVerification,
    "Reason:",
    data.meta.chainReason,
  );
});

test("opening real audit event details preserves the current cursor page", async ({
  page,
  config,
}) => {
  await page.goto(config.baseURL + "/admin/audit");
  await expect(
    page.getByRole("button", { name: "下一页", exact: true }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "下一页", exact: true }).click();
  await expect
    .poll(() => new URL(page.url()).searchParams.get("auditCursor"))
    .toBeTruthy();
  await expect(page.locator(".ant-skeleton")).toHaveCount(0);
  const cursor = new URL(page.url()).searchParams.get("auditCursor");
  await page.locator(".ant-table-tbody tr .ant-btn-link").first().click();
  await expect(
    page.getByRole("dialog", { name: "审计事件详情" }),
  ).toBeVisible();
  await expect(
    page
      .getByRole("dialog")
      .getByText("完整审计记录与校验标识", { exact: true }),
  ).toBeVisible();
  expect(new URL(page.url()).searchParams.get("auditCursor")).toBe(cursor);
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "关闭", exact: true })
    .click();
  expect(new URL(page.url()).searchParams.get("auditCursor")).toBe(cursor);
  expect(new URL(page.url()).searchParams.get("auditId")).toBeNull();
  await expect(
    page.getByRole("button", { name: "回到第一页", exact: true }),
  ).toBeEnabled();
});

test("a bounded actual historical command range verifies the signed archive chain", async ({
  page,
  config,
}) => {
  const loaded = page.waitForResponse(
    (r) =>
      new URL(r.url()).pathname === "/api/v1/audit-records" &&
      new URL(r.url()).searchParams.get("operation") === "command.completed",
  );
  await page.goto(config.baseURL + "/admin/audit?operation=command.completed");
  const response = await loaded;
  expect(response.status()).toBe(200);
  const rows = (await response.json()).data;
  const historical = rows.find(
    (r: { createdAt?: string; occurredAt?: string }) => {
      const time = Date.parse(r.occurredAt ?? r.createdAt ?? "");
      return Number.isFinite(time) && time < Date.now() - 600000;
    },
  );
  expect(
    historical,
    "Requires an actual archived command record older than the archive interval",
  ).toBeTruthy();
  const time = Date.parse(historical.occurredAt ?? historical.createdAt);
  const params = new URLSearchParams({
    operation: "command.completed",
    from: new Date(time - 1000).toISOString(),
    to: new Date(time + 1000).toISOString(),
  });
  await page.goto(config.baseURL + "/admin/audit?" + params);
  await expect(page.locator(".ant-skeleton")).toHaveCount(0);
  const verified = page.waitForResponse(
    (r) =>
      new URL(r.url()).pathname === "/api/v1/audit-records" &&
      new URL(r.url()).searchParams.get("verifyChain") === "true",
  );
  await page
    .getByRole("button", { name: "验证当前页签名归档链", exact: true })
    .click();
  const result = await verified;
  expect(result.status()).toBe(200);
  const data = await result.json();
  expect(data.data.length).toBeGreaterThan(0);
  expect(data.meta.chainVerification).toBe("verified");
  await page.screenshot({
    path: config.screenshotDirectory + "/actual-bounded-signed-audit.png",
    animations: "disabled",
  });
  console.log(
    "Actual bounded signed archive verification:",
    JSON.stringify(data.meta),
  );
});

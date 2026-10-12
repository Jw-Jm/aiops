import { test, expect } from "./session";
import { readFile, writeFile } from "node:fs/promises";
test("current real model investigation resumes with Bearer and cursor and restores its persistent result", async ({
  page,
  config,
}) => {
  const streamRequests: { bearer: boolean; cursor: boolean }[] = [];
  page.on("request", (request) => {
    if (
      /\/api\/v1\/investigations\/[^/]+\/events$/.test(
        new URL(request.url()).pathname,
      )
    ) {
      const headers = request.headers();
      streamRequests.push({
        bearer: !!headers.authorization?.startsWith("Bearer "),
        cursor: !!headers["last-event-id"],
      });
    }
  });
  await page.goto(
    config.baseURL + "/incidents/" + config.incidentId + "?tab=investigation",
  );
  const created = page.waitForResponse(
    (r) =>
      r.request().method() === "POST" &&
      new URL(r.url()).pathname.endsWith("/investigations"),
  );
  await page.getByRole("button", { name: "开始智能调查", exact: true }).click();
  const response = await created;
  expect(response.status()).toBe(202);
  const job = (await response.json()).data;
  expect(job.jobId).toBeTruthy();
  await expect(
    page.getByText("实时更新：实时连接正常", { exact: true }),
  ).toBeVisible({ timeout: 30000 });
  await page.getByRole("button", { name: "重新连接", exact: true }).click();
  await expect
    .poll(() => streamRequests.some((r) => r.cursor && r.bearer), {
      timeout: 15000,
    })
    .toBe(true);
  expect(streamRequests.every((r) => r.bearer)).toBe(true);
  await expect(
    page.getByText("调查已结束，结果来自持久记录", { exact: true }),
  ).toBeVisible({ timeout: 120000 });
  await expect(
    page.getByText("分析现有证据", { exact: true }).first(),
  ).toBeVisible();
  await page.getByText("最近实时事件（最多 100 条）", { exact: true }).click();
  await expect(
    page
      .locator("details")
      .filter({
        has: page.getByText("最近实时事件（最多 100 条）", { exact: true }),
      })
      .getByRole("row")
      .nth(1),
  ).toBeVisible();
  await page.screenshot({
    path: config.screenshotDirectory + "/operator-model-resume-readable.png",
    animations: "disabled",
  });
  await page.reload();
  await expect(
    page.getByText("调查已结束，结果来自持久记录", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("分析现有证据", { exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "重新连接", exact: true }),
  ).toHaveCount(0);
  const inputPath = process.env.OPS_WEB_QA_INPUT!;
  const privateInput = JSON.parse(await readFile(inputPath, "utf8"));
  privateInput.modelJobId = job.jobId;
  await writeFile(inputPath, JSON.stringify(privateInput, null, 2), {
    mode: 0o600,
  });
  console.log(
    "Actual investigation:",
    job.jobId,
    "triggerRevision:",
    job.triggerRevision,
    "Bearer reconnect with cursor: verified; persistent restoration: verified",
  );
});

import { test, expect } from "./session";
test("actual model outage explains the failure while deterministic diagnosis and evidence remain available", async ({
  page,
  config,
}) => {
  test.skip(
    process.env.OPS_WEB_QA_MODEL_OUTAGE !== "1",
    "Requires explicitly injected and recoverable actual model connectivity outage",
  );
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
  if (process.env.OPS_WEB_QA_REUSE_OUTAGE === "1") {
    expect(job.state).toBe("failed");
    expect(job.errorCode).toBe("MODEL_UNAVAILABLE");
    console.log(
      "Reopening persisted actual outage job; no new model-call claim",
    );
  } else expect(["queued", "running"]).toContain(job.state);
  await expect(
    page.getByText("调查已结束，结果来自持久记录", { exact: true }),
  ).toBeVisible({ timeout: 120000 });
  await expect(
    page.getByText("模型暂不可用；仍可查看故障、证据和规则诊断结果。").first(),
  ).toBeVisible();
  await page.screenshot({
    path:
      config.screenshotDirectory + "/operator-model-unavailable-readable.png",
    animations: "disabled",
  });
  await page.getByRole("tab", { name: "根因分析", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "证据核对结果", exact: true }),
  ).toBeVisible();
  await page.getByRole("tab", { name: "故障证据", exact: true }).click();
  await expect(
    page.getByRole("button", { name: /^资源状态 · / }).first(),
  ).toBeVisible();
  await page
    .getByRole("button", { name: /^资源状态 · / })
    .first()
    .click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(
    page
      .getByRole("dialog")
      .getByRole("heading", { name: "实际观测内容", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("dialog").locator(".ant-skeleton")).toHaveCount(
    0,
  );
  await expect(page.getByText("数据来源暂不可用", { exact: true })).toHaveCount(
    0,
  );
  console.log(
    "Actual model connectivity outage job:",
    job.jobId,
    "deterministic RCA and Evidence remain available",
  );
});

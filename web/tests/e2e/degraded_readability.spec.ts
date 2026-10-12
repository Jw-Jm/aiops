import { test, expect } from "./session";
// This scenario requires an explicitly selected real degraded environment.
// Its evidence is separate from the healthy full-business run.
test.skip(
  process.env.OPS_WEB_QA_EXPECT_DEGRADED !== "1",
  "Select a genuinely degraded source environment explicitly",
);
test("real archive degradation is explicit and deterministic Incident records remain readable", async ({
  page,
  config,
}) => {
  await page.goto(
    config.baseURL + "/incidents/" + config.incidentId + "?tab=overview",
  );
  await expect(
    page.getByRole("heading", { name: "当前故障", exact: true }),
  ).toBeVisible();
  await page.getByRole("tab", { name: "故障证据", exact: true }).click();
  const evidenceResponse = page.waitForResponse((r) =>
    new URL(r.url()).pathname.startsWith("/api/v1/evidence/"),
  );
  await page
    .getByRole("button", { name: /^资源状态 · / })
    .first()
    .click();
  const dialog = page.getByRole("dialog");
  const response = await evidenceResponse;
  expect(response.status()).toBe(503);
  const body = await response.json();
  expect(["SOURCE_DEGRADED", "GRAPH_NOT_READY"]).toContain(body.code);
  const title =
    body.code === "SOURCE_DEGRADED" ? "数据来源暂不可用" : "资源查询暂不可用";
  await expect(dialog.getByText(title, { exact: true })).toBeVisible();
  await expect(
    dialog.getByText(
      body.code === "SOURCE_DEGRADED"
        ? /来源未能完成此次查询/
        : /采集与关系图同步尚未完成/,
    ),
  ).toBeVisible();
  console.log("Actual degraded evidence response:", body.code);
  await expect(dialog.getByText(/请求 ID：/)).toBeVisible();
  await page.screenshot({
    path: config.screenshotDirectory + "/actual-source-degraded.png",
    animations: "disabled",
  });
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();
  await page.getByRole("tab", { name: "根因分析", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "证据核对结果", exact: true }),
  ).toBeVisible();
  await page.goto(config.baseURL + "/capabilities");
  await expect(
    page.getByRole("heading", { name: "能力与来源", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("cell", { name: "智能调查", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("虚拟机管理（KubeVirt）", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("开发和真实运行验收延期，本轮不启用").first(),
  ).toBeVisible();
  await page.screenshot({
    path: config.screenshotDirectory + "/operator-capabilities-readable.png",
    animations: "disabled",
  });
});

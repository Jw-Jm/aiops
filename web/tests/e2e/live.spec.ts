import { test, expect, passwordLogin } from "./session";
test("live OIDC refresh, resource/finding navigation, scoped capabilities and ledger restoration", async ({
  page,
  config,
}) => {
  await expect(page.getByText("可见资源", { exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByText("可见资源", { exact: true })).toBeVisible();
  await page.getByRole("menuitem", { name: "资源目录" }).click();
  await expect(page.getByRole("heading", { name: "资源目录" })).toBeVisible();
  await page.goto(
    config.baseURL + "/incidents/" + config.incidentId + "?tab=overview",
  );
  await expect(
    page.getByRole("heading", { name: "当前故障", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".ant-skeleton")).toHaveCount(0);
  await expect(
    page.getByRole("link", { name: "1 · 核对故障告警 了解异常现象与来源" }),
  ).toBeVisible();
  await expect(page.getByText("接口暂时不可用", { exact: true })).toHaveCount(
    0,
  );
  await page.screenshot({
    path: config.screenshotDirectory + "/operator-fault-overview.png",
    animations: "disabled",
  });
  for (const tab of [
    "处理时间线",
    "故障告警",
    "影响资源",
    "诊断关系图",
    "故障证据",
    "根因分析",
    "智能调查",
    "操作审计",
  ]) {
    await page.getByRole("tab", { name: tab, exact: true }).click();
    await expect(
      page.getByRole("tab", { name: tab, exact: true }),
    ).toHaveAttribute("aria-selected", "true");
    await expect(
      page.getByText("没有此范围的访问权限", { exact: true }),
    ).toHaveCount(0);
    await expect(page.locator(".ant-skeleton")).toHaveCount(0);
    await expect(
      page.getByText("正在加载工作台区域…", { exact: true }),
    ).toHaveCount(0);
    await expect(page.getByText("接口暂时不可用", { exact: true })).toHaveCount(
      0,
    );
    if (tab === "故障证据") {
      await page
        .getByRole("button", { name: /^资源状态 · / })
        .first()
        .click();
      await expect(
        page
          .getByRole("dialog")
          .getByRole("heading", { name: "实际观测内容", exact: true }),
      ).toBeVisible();
      await expect(page.locator(".ant-skeleton")).toHaveCount(0);
      await page.screenshot({
        path: config.screenshotDirectory + "/operator-evidence-detail.png",
        animations: "disabled",
      });
      await page
        .getByRole("dialog")
        .getByRole("button", { name: "关闭" })
        .click();
    }
    await page.screenshot({
      path:
        config.screenshotDirectory +
        "/live-" +
        tab.replaceAll(" ", "-") +
        ".png",
      animations: "disabled",
    });
  }
  if (config.modelJobId) {
    await page.goto(
      config.baseURL +
        "/incidents/" +
        config.incidentId +
        "?tab=investigation&jobId=" +
        config.modelJobId,
    );
    await expect(
      page.getByText("已完成", { exact: true }).first(),
    ).toBeVisible();
    await expect(
      page.getByText("根因未确定", { exact: true }).first(),
    ).toBeVisible();
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(config.baseURL + "/overview");
  await expect(page.getByRole("button", { name: "打开导航" })).toBeVisible();
  await expect(page.locator(".ant-skeleton")).toHaveCount(0);
  await page.screenshot({
    path: config.screenshotDirectory + "/live-narrow.png",
    animations: "disabled",
  });
});
test("live namespace command: blank input, review invalidation, confirmation, output and durable post-check", async ({
  page,
  config,
}) => {
  await page.goto(
    config.baseURL + "/incidents/" + config.incidentId + "?tab=action",
  );
  const editor = page.getByRole("textbox", { name: "实际 Bash 命令" });
  await expect(editor).toHaveText("");
  await page.locator("#command-profile").click();
  await page.getByText(config.namespaceProfile, { exact: true }).click();
  await editor.fill(config.readCommand);
  await page.getByRole("button", { name: "2 · 评估实际命令风险" }).click();
  await page.getByRole("checkbox").waitFor();
  await editor.fill(config.readCommand + " ");
  await expect(page.getByRole("checkbox")).toHaveCount(0);
  await page.getByRole("button", { name: "2 · 评估实际命令风险" }).click();
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "4 · 本人确认风险" }).click();
  await expect(
    page.getByRole("button", { name: "5 · 提交本人命令" }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "5 · 提交本人命令" }).click();
  await expect(page.getByText("已完成", { exact: true }).first()).toBeVisible({
    timeout: 60000,
  });
  await expect(page.getByText("不适用", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("检查完成", { exact: true }).first()).toBeVisible(
    { timeout: 60000 },
  );
  const terminalBounds = await page
    .locator(".execution-terminal")
    .boundingBox();
  expect(terminalBounds?.height).toBeGreaterThan(200);
  expect(terminalBounds?.height).toBeLessThanOrEqual(360);
  await page.getByText("无终端依赖的文本输出", { exact: true }).click();
  await expect(
    page
      .locator(".execution-terminal + details pre")
      .filter({ hasText: "persistentvolumeclaim/" }),
  ).toBeVisible();
  await expect(page.getByText("退出码", { exact: true }).first()).toBeVisible();
  await page.screenshot({
    path: config.screenshotDirectory + "/live-namespace-execution.png",
    animations: "disabled",
  });
  await page.reload();
  await expect(editor).toHaveText("");
  await expect(page.getByText("已完成", { exact: true }).first()).toBeVisible();
});
test("live cluster command requires fresh password and OTP, memory restoration and explicit strong session", async ({
  page,
  config,
}) => {
  await page.goto(
    config.baseURL + "/incidents/" + config.incidentId + "?tab=action",
  );
  await page.locator("#command-profile").click();
  await page.getByText(config.clusterProfile, { exact: true }).click();
  await page
    .getByRole("textbox", { name: "实际 Bash 命令" })
    .fill(config.clusterCommand);
  await page.getByRole("button", { name: "2 · 评估实际命令风险" }).click();
  await page.getByRole("button", { name: "3 · 重新验证身份" }).click();
  await passwordLogin(page, config, true);
  const editor = page.getByRole("textbox", { name: "实际 Bash 命令" });
  await expect(editor).toHaveText("");
  await page.locator("#command-profile").click();
  await page.getByText(config.clusterProfile, { exact: true }).click();
  await editor.fill(config.clusterCommand);
  await page.getByRole("button", { name: "2 · 评估实际命令风险" }).click();
  await expect(
    page.getByRole("button", { name: "登记本次强身份验证" }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "登记本次强身份验证" }).click();
  await page.getByRole("checkbox").check();
  await expect(
    page.getByRole("button", { name: "4 · 本人确认风险" }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "4 · 本人确认风险" }).click();
  await expect(
    page.getByRole("button", { name: "5 · 提交本人命令" }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "5 · 提交本人命令" }).click();
  await expect(page.getByText("已完成", { exact: true }).first()).toBeVisible({
    timeout: 60000,
  });
  await expect(page.getByText("检查完成", { exact: true }).first()).toBeVisible(
    { timeout: 60000 },
  );
  const terminalBounds = await page
    .locator(".execution-terminal")
    .boundingBox();
  expect(terminalBounds?.height).toBeGreaterThan(200);
  expect(terminalBounds?.height).toBeLessThanOrEqual(360);
  await page.getByText("无终端依赖的文本输出", { exact: true }).click();
  await expect(
    page
      .locator(".execution-terminal + details pre")
      .filter({ hasText: "persistentvolumeclaim/" }),
  ).toBeVisible();
  await expect(page.getByText("退出码", { exact: true }).first()).toBeVisible();
  await page.screenshot({
    path: config.screenshotDirectory + "/live-cluster-execution.png",
    animations: "disabled",
  });
});

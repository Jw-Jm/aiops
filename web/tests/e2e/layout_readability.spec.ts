import { test, expect } from "./session";
test("real desktop and narrow window layouts keep primary controls accessible", async ({
  page,
  config,
}) => {
  for (const size of [
    { width: 1440, height: 900 },
    { width: 1280, height: 800 },
    { width: 390, height: 844 },
  ]) {
    await page.setViewportSize(size);
    for (const path of [
      "/overview",
      "/resources",
      "/findings",
      "/incidents",
      "/capabilities",
    ]) {
      await page.goto(config.baseURL + path);
      await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
      await expect(page.locator(".ant-skeleton")).toHaveCount(0);
      const width = await page.evaluate(() => ({
        page: document.documentElement.scrollWidth,
        window: innerWidth,
      }));
      expect(width.page).toBeLessThanOrEqual(width.window + 1);
      if (size.width === 390)
        await expect(
          page.getByRole("button", { name: "打开导航", exact: true }),
        ).toBeVisible();
    }
    await page.screenshot({
      path:
        config.screenshotDirectory + "/readable-layout-" + size.width + ".png",
      animations: "disabled",
    });
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(config.baseURL + "/overview");
  await expect(
    page.getByRole("heading", { name: "运行总览", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "跳到主要内容", exact: true }),
  ).toBeAttached();
  await page.keyboard.press("Tab");
  await expect(
    page.getByRole("link", { name: "跳到主要内容", exact: true }),
  ).toBeFocused();
  if (config.modelJobId) {
    await page.goto(
      config.baseURL +
        "/incidents/" +
        config.incidentId +
        "?tab=investigation&jobId=" +
        config.modelJobId,
    );
    await expect(
      page.getByRole("heading", { name: "调查额度", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("调查已结束，结果来自持久记录", { exact: true }),
    ).toBeVisible();
    await page.getByText("查看全部额度与预留", { exact: true }).click();
    await expect(
      page.getByRole("columnheader", { name: "已预留", exact: true }),
    ).toBeVisible();
    await page.screenshot({
      path: config.screenshotDirectory + "/operator-investigation-readable.png",
      animations: "disabled",
    });
  }
});

import { test, expect } from "./session";
test("real time filters preserve UTC scope and historical step input remains explicitly absent", async ({
  page,
  config,
}) => {
  for (const [path, prefix, label] of [
    ["findings", "observed", "告警观测时间"],
    ["incidents", "opened", "故障开始时间"],
  ]) {
    const responses: string[] = [];
    const listener = (r: any) => {
      if (new URL(r.url()).pathname === "/api/v1/" + path)
        responses.push(r.url());
    };
    page.on("response", listener);
    await page.goto(
      config.baseURL +
        "/" +
        path +
        "?" +
        prefix +
        "From=2026-10-01T00%3A00%3A00Z&" +
        prefix +
        "To=2026-10-12T23%3A59%3A59Z",
    );
    const field = page.getByLabel(label + " · 起（UTC+08:00）");
    await expect(field).toHaveValue("2026-10-01T08:00");
    await expect
      .poll(() =>
        responses.some(
          (x) =>
            new URL(x).searchParams.get(prefix + "From") ===
            "2026-10-01T00:00:00Z",
        ),
      )
      .toBe(true);
    await field.fill("2026-10-02T09:30");
    await field.blur();
    await expect
      .poll(() => new URL(page.url()).searchParams.get(prefix + "From"))
      .toBe("2026-10-02T01:30:00.000Z");
    await expect
      .poll(() =>
        responses.some(
          (x) =>
            new URL(x).searchParams.get(prefix + "From") ===
            "2026-10-02T01:30:00.000Z",
        ),
      )
      .toBe(true);
    page.off("response", listener);
  }
  if (!config.modelJobId) throw Error("Actual persistent job required");
  await page.goto(
    config.baseURL +
      "/incidents/" +
      config.incidentId +
      "?tab=investigation&jobId=" +
      config.modelJobId,
  );
  await expect(
    page.getByText("调查已结束，结果来自持久记录", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "展开行", exact: true })
    .first()
    .click();
  await expect(
    page.getByText("此历史步骤未记录输入摘要；不推测原始输入。", {
      exact: true,
    }),
  ).toBeVisible();
  await page.screenshot({
    path: config.screenshotDirectory + "/closeout-historical-input.png",
    animations: "disabled",
  });
  await page.getByRole("button", {name:/^查看证据 /}).first().click();
  await expect(page.getByRole("dialog").getByRole("heading",{name:"实际观测内容",exact:true})).toBeVisible();
  await expect(page.locator(".ant-skeleton")).toHaveCount(0);
  await page.screenshot({path:config.screenshotDirectory+"/closeout-step-evidence.png",animations:"disabled"});
});

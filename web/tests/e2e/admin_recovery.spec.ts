import { test, expect, passwordLogin } from "./session";
import { readFile, writeFile } from "node:fs/promises";
import { getUpdateRegistryDraftUrl } from "../../src/api/generated/platform";
import type { RegistryDraftUpdateRequest } from "../../src/api/generated/models";
test("actual administrator draft conflict is recoverable without losing local edits", async ({
  page,
  config,
}) => {
  let authorization = "";
  page.on("request", (r) => {
    if (new URL(r.url()).pathname === "/api/v1/auth/session")
      authorization = r.headers().authorization ?? authorization;
  });
  await page.goto(config.baseURL + "/admin/policies");
  await page
    .getByRole("button", { name: "验证本人身份（多因素）", exact: true })
    .click();
  await passwordLogin(page, config, true);
  await page
    .getByRole("button", { name: "启用本次配置修改", exact: true })
    .click();
  await expect(page.getByText(/^有效至 /)).toBeVisible();
  const name = "sp08-web-recovery-qa-" + Date.now();
  const content = (comment: string) => ({
    schemaVersion: "policy-registry/v1",
    name,
    modules: [
      {
        id: "baseline",
        content:
          "package ops.policy\n# " + comment + "\ndefault allow := false",
      },
    ],
  });
  await page
    .getByRole("textbox", { name: "策略逻辑名称", exact: true })
    .fill(name);
  const editor = page.getByRole("textbox", {
    name: "策略草稿内容 JSON",
    exact: true,
  });
  await editor.fill(
    JSON.stringify(
      content("initial actual QA draft; not published or activated"),
    ),
  );
  const create = page.waitForResponse(
    (r) =>
      r.request().method() === "POST" &&
      new URL(r.url()).pathname.endsWith("/registry-drafts"),
  );
  await page.getByRole("button", { name: "创建策略草稿", exact: true }).click();
  const created = await create;
  expect(created.status()).toBe(201);
  const draft = (await created.json()).data;
  const local = JSON.stringify(content("local change retained"));
  await editor.fill(local);
  const body: RegistryDraftUpdateRequest = {
    expectedRevision: draft.revision,
    content: content("server concurrent change"),
  };
  const status = await page.evaluate(
    async ({ url, authorization, body }) => {
      const r = await fetch(url, {
        method: "PATCH",
        headers: {
          Authorization: authorization,
          "Content-Type": "application/json",
          "Idempotency-Key": crypto.randomUUID(),
        },
        body: JSON.stringify(body),
      });
      return r.status;
    },
    { url: getUpdateRegistryDraftUrl(draft.draftId), authorization, body },
  );
  expect(status).toBe(200);
  await page
    .getByRole("button", { name: "按修订号更新草稿", exact: true })
    .click();
  await expect(
    page.getByText("版本冲突，请刷新后重新检查变更", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "重新读取草稿（保留本地修改）", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "服务端草稿 · 修订号 2", exact: true }),
  ).toBeVisible();
  await expect(editor).toHaveValue(local);
  const merged = JSON.stringify(
    content("local change retained; server concurrent change"),
  );
  await editor.fill(merged);
  await page
    .getByRole("checkbox", { name: "已比较并在上方完成手动合并", exact: true })
    .check();
  await page
    .getByRole("button", {
      name: "保留合并内容，采用已读取修订号",
      exact: true,
    })
    .click();
  const update = page.waitForResponse(
    (r) =>
      r.request().method() === "PATCH" &&
      new URL(r.url()).pathname.endsWith("/registry-drafts/" + draft.draftId),
  );
  await page
    .getByRole("button", { name: "按修订号更新草稿", exact: true })
    .click();
  const saved = await update;
  expect(saved.status()).toBe(200);
  expect((await saved.json()).data.revision).toBe(3);
  await page
    .getByRole("button", { name: "重新读取草稿（保留本地修改）", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "服务端草稿 · 修订号 3", exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: config.screenshotDirectory + "/admin-draft-conflict-recovered.png",
    animations: "disabled",
  });
  const inputPath = process.env.OPS_WEB_QA_INPUT!;
  const privateInput = JSON.parse(await readFile(inputPath, "utf8"));
  privateInput.registryDraftId = draft.draftId;
  await writeFile(inputPath, JSON.stringify(privateInput, null, 2), {
    mode: 0o600,
  });
  console.log(
    "Actual isolated QA draft:",
    draft.draftId,
    "revision1→conflict→read2→merged3; not published or activated",
  );
});
test("actual administration pages remain readable in desktop and narrow windows", async ({
  page,
  config,
}) => {
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    for (const section of [
      "sources",
      "policies",
      "execution-profiles",
      "deployment",
    ]) {
      await page.goto(config.baseURL + "/admin/" + section);
      await expect(
        page.getByRole("heading", { level: 1, name: "平台管理" }),
      ).toBeVisible();
      await expect(page.locator(".ant-skeleton")).toHaveCount(0);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      ).toBe(true);
      await expect(
        page.getByText("接口暂时不可用", { exact: true }),
      ).toHaveCount(0);
      await page.screenshot({
        path:
          config.screenshotDirectory +
          "/admin-" +
          section +
          "-" +
          width +
          ".png",
        animations: "disabled",
      });
    }
  }
});

test("actual source editing starts from the loaded status without changing the source", async ({
  page,
  config,
}) => {
  const loaded = page.waitForResponse(
    (r) => new URL(r.url()).pathname === "/api/v1/admin/source-registrations",
  );
  await page.goto(config.baseURL + "/admin/sources");
  const response = await loaded;
  expect(response.status()).toBe(200);
  const source = (await response.json()).data.find(
    (r: { status: string }) => r.status === "active" || r.status === "disabled",
  );
  expect(source).toBeTruthy();
  await page
    .getByRole("button", {
      name:
        source.name ??
        source.logicalName ??
        source.instanceKey ??
        source.sourceId ??
        source.sourceRegistrationId,
      exact: true,
    })
    .click();
  await expect(
    page.getByRole("combobox", { name: "来源新状态" }),
  ).toBeVisible();
  await expect(
    page
      .locator(".ant-select")
      .filter({ has: page.getByRole("combobox", { name: "来源新状态" }) }),
  ).toContainText(source.status === "active" ? "启用" : "停用");
  await expect(
    page.getByRole("button", { name: "保存来源状态", exact: true }),
  ).toBeDisabled();
});

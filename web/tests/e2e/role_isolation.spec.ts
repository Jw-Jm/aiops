import { test, expect, chromium, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { execFileSync } from "node:child_process";
import { input, passwordLogin } from "./session";
import {
  getGetAuthSessionUrl,
  getGetSourceRegistrationsUrl,
  getAssessCommandRiskUrl,
} from "../../src/api/generated/platform";

async function roleContext(role: string) {
  const config = await input();
  if (!config.roleIdentitiesFile)
    throw Error("External real OIDC role identity input is required");
  const identities = JSON.parse(
    await readFile(config.roleIdentitiesFile, "utf8"),
  );
  const identity = identities[role];
  if (!identity) throw Error("Requested QA role identity is missing");
  const browser = await chromium.connectOverCDP(process.env.OPS_WEB_QA_CDP!);
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();
  let authorization = "";
  page.on("request", (r) => {
    if (new URL(r.url()).pathname === "/api/v1/auth/session")
      authorization = r.headers()["authorization"] ?? authorization;
  });
  await page.goto(config.baseURL + "/overview");
  await page.getByRole("button", { name: "使用 OIDC 登录" }).click();
  await passwordLogin(page, {
    ...config,
    username: identity.username,
    passwordFile: identity.passwordFile,
  });
  await expect.poll(() => Boolean(authorization)).toBe(true);
  return { config, browser, context, page, authorization: () => authorization };
}
async function status(
  page: Page,
  authorization: string,
  url: string,
  method = "GET",
  extraHeaders: Record<string, string> = {},
  body?: unknown,
) {
  return page.evaluate(
    async ({ authorization, url, method, extraHeaders, body }) => {
      const r = await fetch(url, {
        method,
        headers: {
          Authorization: authorization,
          ...extraHeaders,
          ...(method === "POST"
            ? {
                "Content-Type": "application/json",
                "Idempotency-Key": crypto.randomUUID(),
              }
            : {}),
        },
        ...(method === "POST" ? { body: JSON.stringify(body ?? {}) } : {}),
      });
      return r.status;
    },
    { authorization, url, method, extraHeaders, body },
  );
}
for (const role of ["operator", "platform_admin", "no_grant"])
  test("real OIDC role isolation: " + role, async () => {
    const t = await roleContext(role);
    try {
      if (role === "no_grant") {
        expect(
          await status(t.page, t.authorization(), getGetAuthSessionUrl()),
        ).toBe(403);
      } else {
        const roles = await t.page.evaluate(
          async ({ url, authorization }) =>
            (
              await (
                await fetch(url, { headers: { Authorization: authorization } })
              ).json()
            ).data.roles,
          { url: getGetAuthSessionUrl(), authorization: t.authorization() },
        );
        expect(roles).toEqual([role]);
      }
      expect(
        await status(
          t.page,
          t.authorization(),
          getGetSourceRegistrationsUrl({ limit: 10 }),
        ),
      ).toBe(role === "platform_admin" ? 200 : 403);
      if (role !== "operator")
        expect(
          await status(
            t.page,
            t.authorization(),
            getAssessCommandRiskUrl(),
            "POST",
            {},
            t.config.riskRequest,
          ),
        ).toBe(403);
      expect(
        await status(t.page, t.authorization(), getGetAuthSessionUrl(), "GET", {
          "X-Tenant-ID": crypto.randomUUID(),
        }),
      ).toBe(403);
      if (role === "operator") {
        await expect(
          t.page.getByRole("menuitem", { name: "平台管理" }),
        ).toHaveCount(0);
        await t.page.goto(t.config.baseURL + "/admin/sources");
        await expect(
          t.page.getByText("没有此角色权限", { exact: true }),
        ).toBeVisible();
      } else if (role === "platform_admin") {
        await t.page.goto(t.config.baseURL + "/admin/sources");
        await expect(
          t.page.getByRole("heading", { name: "平台管理" }),
        ).toBeVisible();
        await expect(
          t.page.getByRole("textbox", { name: "实际 Bash 命令" }),
        ).toHaveCount(0);
      } else
        await expect(
          t.page.getByText("当前身份没有可用授权", { exact: true }),
        ).toBeVisible();
      await t.page.screenshot({
        path: t.config.screenshotDirectory + "/role-" + role + ".png",
        animations: "disabled",
      });
    } finally {
      await t.context.close();
      await t.browser.close();
    }
  });

test("real grant revocation invalidates a pending human risk confirmation", async () => {
  const t = await roleContext("operator");
  if (!t.config.roleGrantHelper)
    throw Error("External QA-only role grant helper required");
  try {
    await t.page.goto(
      t.config.baseURL + "/incidents/" + t.config.incidentId + "?tab=action",
    );
    await t.page.locator("#command-profile").click();
    await t.page.getByText(t.config.namespaceProfile, { exact: true }).click();
    await t.page
      .getByRole("textbox", { name: "实际 Bash 命令" })
      .fill(t.config.readCommand);
    await t.page.getByRole("button", { name: "2 · 评估实际命令风险" }).click();
    await expect(t.page.getByRole("checkbox")).toBeVisible();
    execFileSync(
      "python3",
      [t.config.roleGrantHelper, "operator", "disabled"],
      { stdio: "pipe" },
    );
    await t.page.getByRole("checkbox").check();
    await t.page.getByRole("button", { name: "4 · 本人确认风险" }).click();
    await expect(
      t.page.getByText("当前身份没有可用授权", { exact: true }),
    ).toBeVisible({ timeout: 10000 });
    await expect(t.page.getByRole("checkbox")).toHaveCount(0);
    await expect(
      t.page.getByRole("textbox", { name: "实际 Bash 命令" }),
    ).toHaveCount(0);
    expect(
      await status(
        t.page,
        t.authorization(),
        getAssessCommandRiskUrl(),
        "POST",
      ),
    ).toBe(403);
    await t.page.screenshot({
      path: t.config.screenshotDirectory + "/role-revoked.png",
      animations: "disabled",
    });
  } finally {
    execFileSync("python3", [t.config.roleGrantHelper, "operator", "active"], {
      stdio: "pipe",
    });
    await t.context.close();
    await t.browser.close();
  }
});

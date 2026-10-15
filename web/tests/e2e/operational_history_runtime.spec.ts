import { test, expect, chromium } from "@playwright/test";
import { readFile, writeFile } from "node:fs/promises";

// Run on the owned remote runtime against a real partial turn. The separate
// network oracle test retains its strict counts; this checks historical gaps.
test("actual operational history gap, retained source and owner isolation", async () => {
  test.setTimeout(120000);
  const privateDir = process.env.OPS_ASSISTANT_RUNTIME_PRIVATE!;
  const turnId = process.env.OPS_OPERATIONAL_HISTORY_TURN!;
  if (!privateDir || !turnId)
    throw Error("owned runtime and actual partial turn required");
  const identities = JSON.parse(
    await readFile(privateDir + "/identities.json", "utf8"),
  );
  const api = JSON.parse(
    await readFile(privateDir + "/api-address.json", "utf8"),
  );
  const base = "https://192.168.0.63:18643";
  const browser = await chromium.connectOverCDP(process.env.OPS_WEB_QA_CDP!);
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
  });
  async function login(ctx: any, user: any) {
    const page = await ctx.newPage();
    page.setDefaultTimeout(15000);
    let token = "";
    page.on("request", (r: any) => {
      if (r.url().includes("/api/")) token = r.headers().authorization || token;
    });
    await page.goto(base + "/assistant");
    await page.getByRole("button", { name: "使用 OIDC 登录" }).click();
    await page.locator("#username").fill(user.username);
    await page.locator("#password").fill(user.password);
    await page.locator("#kc-login").click();
    await page.waitForURL(
      (u: URL) =>
        u.origin === base &&
        !u.pathname.startsWith("/oidc") &&
        !u.pathname.startsWith("/auth/"),
    );
    await page.goto(base + "/assistant");
    await expect(page.getByRole("button", { name: /创建会话/ })).toBeVisible();
    return {
      page,
      headers: () => ({ Authorization: token, "X-Tenant-ID": api.tenantId }),
    };
  }
  try {
    const { page, headers } = await login(context, identities.operator);
    const turnResponse = await context.request.get(
      base + "/api/v1/assistant/turns/" + turnId,
      { headers: headers() },
    );
    expect(turnResponse.status()).toBe(200);
    const turn = (await turnResponse.json()).data;
    expect(turn.state).toBe("partial");
    const path = base + "/api/v1/assistant/turns/" + turnId;
    const response = await context.request.get(path + "/fault-chain-context", {
      headers: headers(),
    });
    expect(response.status()).toBe(200);
    const chain = (await response.json()).data;
    expect(chain.request.schemaVersion).toBe("fault-chain-request/v2");
    const history = chain.sourceSteps.find(
      (s: any) =>
        s.tool === "resource_history" &&
        s.canonicalId === chain.request.targetCanonicalId,
    );
    expect(history).toBeTruthy();
    const sourceResponse = await context.request.get(
      path + "/source-steps/" + history.stepId,
      { headers: headers() },
    );
    expect(sourceResponse.status()).toBe(200);
    const source = (await sourceResponse.json()).data;
    expect(source.tool).toBe("resource_history");
    expect(source.data.canonicalId).toBe(chain.request.targetCanonicalId);
    expect(source.data.window).toEqual(chain.request.fault);
    expect(source.data.records).toEqual([]);
    expect(source.data.missingEvidence).toContain("resource_history_missing");
    await page.goto(base + "/assistant?conversation=" + turn.conversationId);
    await expect(
      page.getByRole("heading", { name: "资源关联观察" }),
    ).toBeVisible();
    const locator = page.getByRole("button", {
      name: "定位来源步骤 " + history.stepId,
      exact: true,
    });
    await locator.focus();
    await page.keyboard.press("Enter");
    const dialog = page.getByRole("dialog");
    await expect(
      dialog.getByText("原始已提交来源（安全投影）", { exact: true }),
    ).toBeVisible();
    await expect(dialog.locator("pre")).toContainText(
      "resource_history_missing",
    );
    await expect(dialog.locator("pre")).toContainText(
      chain.request.targetCanonicalId,
    );
    await page
      .getByRole("button", { name: /^(关闭|Close)$/ })
      .last()
      .click();
    await page.reload();
    await expect(
      page.getByRole("heading", { name: "资源关联观察" }),
    ).toBeVisible();
    await page.setViewportSize({ width: 390, height: 844 });
    await page
      .getByRole("button", { name: "准备解释这些证据" })
      .first()
      .click();
    await expect(page.locator("#assistant-message")).toHaveValue(
      /get_assistant_context/,
    );
    await expect(page.locator("#assistant-message")).toBeFocused();
    const retained = await context.request.get(path + "/fault-chain-context", {
      headers: headers(),
    });
    expect((await retained.json()).data).toEqual(chain);
    const adminContext = await browser.newContext();
    try {
      const admin = await login(adminContext, identities.admin);
      for (const suffix of [
        "/fault-chain-context",
        "/source-steps/" + history.stepId,
      ]) {
        const foreign = await adminContext.request.get(path + suffix, {
          headers: admin.headers(),
        });
        expect([403, 404]).toContain(foreign.status());
      }
    } finally {
      await adminContext.close();
    }
    const receipt = {
      passed: true,
      runtimeId: api.runtimeId,
      profileReference: api.profileReference,
      modelQualityAcceptance: false,
      oidc: "actual-PKCE-RS256",
      operationalHistoryGap: true,
      turnId,
      sourceStepId: history.stepId,
      frozenWindowVerified: true,
      missingHistoryNotBackfilled: true,
      retainedAfterReloadAndDraft: true,
      ownerIsolation: true,
      keyboard: true,
      narrowScreen: true,
      networkOracleAcceptance: false,
    };
    await writeFile(
      privateDir + "/browser-receipt.json",
      JSON.stringify(receipt),
      { mode: 0o600 },
    );
  } finally {
    await context.close();
    await browser.close();
  }
});

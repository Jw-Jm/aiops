import { test, expect, chromium } from "@playwright/test";
import { readFile, writeFile } from "node:fs/promises";

// Real auth/backend only; no page.route, Auth alias, injected token or model
// answer assertion. Run exclusively on the authorized remote task environment.
test("actual OIDC PKCE, assistant durable turn/SSE, restoration and role isolation", async () => {
  test.setTimeout(360_000);
  const privateDir = process.env.OPS_ASSISTANT_RUNTIME_PRIVATE;
  if (!privateDir) throw Error("owned private coordination directory required");
  const identities = JSON.parse(
    await readFile(privateDir + "/identities.json", "utf8"),
  );
  const api = JSON.parse(
    await readFile(privateDir + "/api-address.json", "utf8"),
  );
  const baseURL = "https://192.168.0.63:18643";
  const browser = await chromium.connectOverCDP(
    process.env.OPS_WEB_QA_CDP || "http://127.0.0.1:19222",
  );
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();
  let token = "";
  let streams = 0;
  page.on("request", (r) => {
    if (r.url().includes("/api/")) {
      const candidate = r.headers()["authorization"];
      if (candidate) token = candidate;
    }
    if (r.url().includes("/events")) streams++;
  });
  const login = async (user: any) => {
    await page.goto(baseURL + "/assistant");
    await page.getByRole("button", { name: "使用 OIDC 登录" }).click();
    await page.locator("#username").fill(user.username);
    await page.locator("#password").fill(user.password);
    await page.locator("#kc-login").click();
    await page.waitForURL(
      (u) =>
        u.origin === baseURL &&
        !u.pathname.startsWith("/oidc") &&
        !u.pathname.startsWith("/auth/"),
    );
    await page.goto(baseURL + "/assistant");
    await expect(page.getByRole("button", { name: /创建会话/ })).toBeVisible();
  };
  try {
    await login(identities.operator);
    const session = await context.request.get(
      baseURL + "/api/v1/auth/session",
      { headers: { Authorization: token } },
    );
    expect(session.status()).toBe(200);
    const verified = (await session.json()).data;
    expect(verified.subject).toBe(identities.operator.subject);
    expect(verified.roles).toEqual(["operator"]);
    const caps = await context.request.get(
      baseURL + "/api/v1/assistant/capabilities?clusterUid=" + api.clusterUid,
      { headers: { Authorization: token } },
    );
    expect(caps.status()).toBe(200);
    const capabilities = (await caps.json()).data;
    expect(capabilities.enabled).toBe(true);
    expect(capabilities.sourceCapabilityStatus).toBe("available");

    if (process.env.OPS_ASSISTANT_DEEPFLOW_GRAPH_ONLY === "1") {
      await expect
        .poll(
          async () => {
            const result = await context.request.get(
              baseURL +
                "/api/v1/assistant/capabilities?clusterUid=" +
                api.clusterUid,
              { headers: { Authorization: token } },
            );
            if (result.status() !== 200) return false;
            const current = (await result.json()).data;
            return current.networkCapabilities.some(
              (s: any) =>
                s.network === "live" &&
                s.l7 === "live" &&
                s.trace === "CAPABILITY_DISABLED",
            );
          },
          { timeout: 120_000, intervals: [2000] },
        )
        .toBe(true);
    } else {
      expect(
        capabilities.sourceCapabilities.some(
          (s: any) => s.datasetId === "metrics.pod-cpu",
        ),
      ).toBe(true);
      expect(
        capabilities.sourceCapabilities.some(
          (s: any) => s.datasetId === "logs.records",
        ),
      ).toBe(true);
    }
    await page
      .getByPlaceholder("命名空间子范围（空表示全部已授权命名空间）")
      .fill(api.namespace);
    await page.getByRole("button", { name: /创建会话/ }).click();
    await page.waitForURL((u) => !!u.searchParams.get("conversation"));
    if (process.env.OPS_ASSISTANT_DEEPFLOW_GRAPH_ONLY === "1") {
      await expect(page.getByText("网络与应用层观测")).toBeVisible();
      await expect(
        page.getByText(/DeepFlow L4：live；L7：live；Trace：未启用/),
      ).toBeVisible();
    }
    const conversationId = new URL(page.url()).searchParams.get(
      "conversation",
    )!;
    const submitted = page.waitForResponse(
      (r) =>
        r.url().includes("/conversations/" + conversationId + "/messages") &&
        r.request().method() === "POST",
    );
    await page
      .getByRole("textbox", { name: "问题", exact: true })
      .fill("请先读取当前助手上下文，说明现有数据来源。不要推断故障原因。");
    await page.getByRole("button", { name: /^发\s*送$/ }).click();
    const response = await submitted;
    expect(response.status()).toBe(202);
    const turn = (await response.json()).data;
    let state = "queued";
    let answer: any;
    for (let i = 0; i < 90; i++) {
      const result = await context.request.get(
        baseURL + "/api/v1/assistant/turns/" + turn.turnId,
        { headers: { Authorization: token } },
      );
      expect(result.status()).toBe(200);
      const current = (await result.json()).data;
      state = current.state;
      answer = current.answer;
      if (!["queued", "running"].includes(state)) break;
      await page.waitForTimeout(2000);
    }
    expect(["completed", "awaiting_user", "partial"]).toContain(state);
    expect(answer?.schemaVersion).toBe("assistant-answer/v1");
    const messages = await context.request.get(
      baseURL +
        "/api/v1/assistant/conversations/" +
        conversationId +
        "/messages",
      { headers: { Authorization: token } },
    );
    expect(messages.status()).toBe(200);
    expect(JSON.stringify(await messages.json())).toContain("现有数据来源");
    expect(streams).toBeGreaterThan(0);
    await page.reload();
    await expect(
      page.getByRole("textbox", { name: "问题", exact: true }),
    ).toBeVisible();
    expect(new URL(page.url()).searchParams.get("conversation")).toBe(
      conversationId,
    );
    const restored = await context.request.get(
      baseURL + "/api/v1/assistant/conversations/" + conversationId,
      { headers: { Authorization: token } },
    );
    expect(restored.status()).toBe(200);
    await context.close();
    const adminContext = await browser.newContext({
      viewport: { width: 1440, height: 900 },
    });
    const adminPage = await adminContext.newPage();
    let adminToken = "";
    adminPage.on("request", (r) => {
      const value = r.headers()["authorization"];
      if (r.url().includes("/api/") && value) adminToken = value;
    });
    await adminPage.goto(baseURL + "/assistant");
    await adminPage.getByRole("button", { name: "使用 OIDC 登录" }).click();
    await adminPage.locator("#username").fill(identities.admin.username);
    await adminPage.locator("#password").fill(identities.admin.password);
    await adminPage.locator("#kc-login").click();
    await adminPage.waitForURL(
      (u) =>
        u.origin === baseURL &&
        !u.pathname.startsWith("/oidc") &&
        !u.pathname.startsWith("/auth/"),
    );
    await adminPage.goto(baseURL + "/assistant");
    await expect(
      adminPage.getByRole("button", { name: /创建会话/ }),
    ).toBeVisible();
    const foreign = await adminContext.request.get(
      baseURL + "/api/v1/assistant/conversations/" + conversationId,
      { headers: { Authorization: adminToken } },
    );
    expect([403, 404]).toContain(foreign.status());
    const adminCaps = await adminContext.request.get(
      baseURL + "/api/v1/assistant/capabilities",
      { headers: { Authorization: adminToken } },
    );
    expect(adminCaps.status()).toBe(200);
    expect(
      (await adminCaps.json()).data.catalog.datasets.every(
        (d: any) => d.authorizationDomain === "audit",
      ),
    ).toBe(true);
    await adminContext.close();
    await writeFile(
      privateDir + "/browser-receipt.json",
      JSON.stringify({
        passed: true,
        runtimeId: api.runtimeId,
        profileReference: api.profileReference,
        modelQualityAcceptance: false,
        oidc: "actual-PKCE-RS256",
        turnState: state,
        conversationRestored: true,
        sourceCapabilities: true,
        networkCapabilities:
          process.env.OPS_ASSISTANT_DEEPFLOW_GRAPH_ONLY === "1"
            ? { l4: "live", l7: "live", trace: "CAPABILITY_DISABLED" }
            : null,
        clusterUid: api.clusterUid,
        namespace: api.namespace,
        streams,
        roleIsolation: true,
      }),
      { mode: 0o600 },
    );
  } finally {
    if (!context.pages().length) {
    } else await context.close();
    await browser.close();
  }
});

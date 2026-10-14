import { test, expect, chromium } from "@playwright/test";
import { readFile, writeFile } from "node:fs/promises";

test("actual OIDC fault-chain collection, stored counts, restoration and owner isolation", async () => {
  test.skip(
    process.env.OPS_FAULT_CHAIN_RUNTIME !== "1",
    "isolated remote runtime required",
  );
  test.setTimeout(360000);
  const privateDir = process.env.OPS_ASSISTANT_RUNTIME_PRIVATE!;
  const identities = JSON.parse(
    await readFile(privateDir + "/identities.json", "utf8"),
  );
  const api = JSON.parse(
    await readFile(privateDir + "/api-address.json", "utf8"),
  );
  const proof = JSON.parse(
    await readFile(process.env.OPS_FAULT_CHAIN_PROOF!, "utf8"),
  );
  const pod = proof.nativePods.find((p: any) =>
    p.name.startsWith("fault-chain-client-"),
  );
  const base = "https://192.168.0.63:18643";
  const browser = await chromium.connectOverCDP(process.env.OPS_WEB_QA_CDP!);
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
  });
  let token = "",
    streams = 0;
  const page = await context.newPage();
  page.on("request", (r) => {
    if (r.url().includes("/api/")) token = r.headers().authorization || token;
    if (r.url().includes("/events")) streams++;
  });
  async function login(p: any, user: any) {
    await p.goto(base + "/assistant");
    await p.getByRole("button", { name: "使用 OIDC 登录" }).click();
    await p.locator("#username").fill(user.username);
    await p.locator("#password").fill(user.password);
    await p.locator("#kc-login").click();
    await p.waitForURL(
      (u: URL) =>
        u.origin === base &&
        !u.pathname.startsWith("/oidc") &&
        !u.pathname.startsWith("/auth/"),
    );
    await p.goto(base + "/assistant");
    await expect(p.getByRole("button", { name: /创建会话/ })).toBeVisible();
  }
  try {
    await login(page, identities.operator);
    const headers = () => ({
      Authorization: token,
      "X-Tenant-ID": api.tenantId,
    });
    const resolved = await context.request.post(
      base + "/api/v1/assistant/resources:resolve",
      {
        headers: headers(),
        data: {
          clusterUid: api.clusterUid,
          namespaceSelection: [api.namespace],
          includeClusterScoped: false,
          selector: { kind: "Pod", name: pod.name, namespace: api.namespace },
        },
      },
    );
    expect(resolved.status(), String((await resolved.json()).code || "")).toBe(
      200,
    );
    const candidates = (await resolved.json()).data.candidates;
    expect(candidates).toHaveLength(1);
    const from = new Date(proof.window.fromUnix * 1000).toISOString();
    const to = new Date(proof.window.toUnix * 1000).toISOString();
    const created = await context.request.post(
      base + "/api/v1/assistant/conversations",
      {
        headers: { ...headers(), "Idempotency-Key": crypto.randomUUID() },
        data: {
          mode: "troubleshoot",
          title: "真实故障链验收",
          authorizationDomain: "operational",
          clusterUid: api.clusterUid,
          namespaceSelection: [api.namespace],
          includeClusterScoped: false,
          resourceCanonicalIds: [candidates[0].canonicalId],
          symptom: "受控 HTTP 响应异常",
          timezone: "UTC",
          timeRange: { from, to, timezone: "UTC" },
        },
      },
    );
    expect(created.status()).toBe(201);
    const conversation = (await created.json()).data;
    await page.goto(
      base + "/assistant?conversation=" + conversation.conversationId,
    );
    await page
      .getByRole("textbox", { name: "调查 Pod 名称", exact: true })
      .fill(pod.name);
    await page.getByRole("button", { name: "查找当前授权范围的 Pod" }).click();
    await page.getByRole("combobox", { name: "调查目标 Pod" }).click();
    await page
      .getByTitle(api.namespace + "/" + pod.name, { exact: true })
      .click();
    await page
      .getByRole("textbox", { name: "故障开始（UTC）", exact: true })
      .fill(from.slice(0, 19));
    await page
      .getByRole("textbox", { name: "故障结束（UTC）", exact: true })
      .fill(to.slice(0, 19));
    await page
      .getByRole("checkbox", { name: "与前一等长窗口比较，间隔 2 分钟" })
      .uncheck();
    const submitted = page.waitForResponse(
      (r) =>
        r.url().endsWith("/fault-chain-investigations") &&
        r.request().method() === "POST",
    );
    await page
      .getByRole("button", { name: "收集故障链证据", exact: true })
      .click();
    const response = await submitted;
    expect(response.status()).toBe(202);
    const turn = (await response.json()).data;
    let complete: any;
    await expect
      .poll(
        async () => {
          const result = await context.request.get(
            base + "/api/v1/assistant/turns/" + turn.turnId,
            { headers: headers() },
          );
          expect(result.status()).toBe(200);
          complete = (await result.json()).data;
          return !["queued", "running"].includes(complete.state);
        },
        { timeout: 120000, intervals: [1000] },
      )
      .toBe(true);
    expect(["partial", "completed"]).toContain(complete.state);
    expect(complete.answer.faultChainContextRef).toBeTruthy();
    await expect(
      page.getByText("窗口顺序不代表传播顺序", { exact: false }),
    ).toBeVisible({ timeout: 30000 });
    const chainResponse = await context.request.get(
      base + "/api/v1/assistant/turns/" + turn.turnId + "/fault-chain-context",
      { headers: headers() },
    );
    expect(chainResponse.status()).toBe(200);
    const chain = (await chainResponse.json()).data;
    expect(chain.evidenceRefs.length).toBeGreaterThan(0);
    const datasets: any = {};
    for (const id of chain.queryResultRefs) {
      const result = await context.request.get(
        base + "/api/v1/assistant/query-results/" + id,
        { headers: headers() },
      );
      expect(result.status()).toBe(200);
      const query = (await result.json()).data.result;
      expect(query.exact).toBe(true);
      expect(
        query.statistics.find((s: any) => s.key === "current.responses").value,
      ).toBe(1000);
      datasets["network.http-5xx"] = query.statistics.find(
        (s: any) => s.key === "current.5xx",
      ).value;
      datasets[query.datasetId] = query.rows.find(
        (r: any) => r.dimension === "current",
      ).value;
    }
    expect(datasets["network.http-5xx-rate"]).toBe(5);
    expect(datasets["network.http-5xx"]).toBe(50);
    await page.reload();
    await expect(
      page.getByText("窗口顺序不代表传播顺序", { exact: false }),
    ).toBeVisible({ timeout: 30000 });
    const adminContext = await browser.newContext();
    const admin = await adminContext.newPage();
    let adminToken = "";
    admin.on("request", (r) => {
      if (r.url().includes("/api/"))
        adminToken = r.headers().authorization || adminToken;
    });
    await login(admin, identities.admin);
    const foreign = await adminContext.request.get(
      base + "/api/v1/assistant/turns/" + turn.turnId + "/fault-chain-context",
      { headers: { Authorization: adminToken, "X-Tenant-ID": api.tenantId } },
    );
    expect([403, 404]).toContain(foreign.status());
    await adminContext.close();
    await writeFile(
      privateDir + "/browser-receipt.json",
      JSON.stringify({
        passed: true,
        runtimeId: api.runtimeId,
        profileReference: api.profileReference,
        modelQualityAcceptance: false,
        oidc: "actual-PKCE-RS256",
        faultChain: true,
        turnId: turn.turnId,
        queryCount: chain.queryResultRefs.length,
        evidenceCount: chain.evidenceRefs.length,
        oracleResponses: 1000,
        oracle5xx: 50,
        restored: true,
        ownerIsolation: true,
        streams,
      }),
      { mode: 0o600 },
    );
  } finally {
    await context.close();
    await browser.close();
  }
});

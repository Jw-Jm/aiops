// Standalone assistant transport fixtures. Run only against the isolated harness.
import { test as baseTest, expect, chromium } from "@playwright/test";
const test = baseTest.extend({
  browser: async ({}, use) => {
    const browser = await chromium.connectOverCDP(
      process.env.OPS_ASSISTANT_UI_CDP || "http://127.0.0.1:19222",
    );
    try {
      await use(browser);
    } finally {
      await browser.close();
    }
  },
});
const base = process.env.OPS_ASSISTANT_UI_URL;
test.skip(!base, "isolated assistant fixture harness required");
const c = {
  tenantId: "fixture-tenant",
  ownerSubject: "fixture-owner",
  conversationId: "conversation-fixture",
  title: "专项浏览器 Fixture",
  mode: "data_query",
  authorizationDomain: "operational",
  clusterUid: "fixture-cluster",
  revision: 2,
  binding: { timezone: "UTC", executionScope: { namespaces: ["apps"] } },
  createdAt: "2026-10-13T00:00:00Z",
  updatedAt: "2026-10-13T00:00:00Z",
  activeTurn: {
    turnId: "turn-fixture",
    state: "completed",
    createdAt: "2026-10-13T00:00:00Z",
  },
};
const answer = {
  schemaVersion: "assistant-answer/v1",
  answerKind: "diagnosis",
  summary: "关系需要补证",
  factRefs: [],
  queryResultRefs: ["query-fixture"],
  evidenceRefs: [],
  entityRefs: [],
  missingEvidence: ["缺少因果时间证据"],
  scopeSummary: "apps",
  asOf: "2026-10-13T00:00:00Z",
  degradedSources: [],
  warnings: [],
  hypotheses: [],
  nextChecks: [],
  faultChain: [
    {
      from: "pod-a",
      to: "service-b",
      kind: "depends_on",
      assessment: "hypothesis",
      relationDigest: "sha256:fixture",
      supports: [],
      conflicts: [],
      missingEvidence: ["缺少时间先后"],
      nextChecks: ["检查日志"],
    },
  ],
  formalRefs: [
    {
      kind: "rca",
      incidentId: "incident-fixture",
      revision: 1,
      contentDigest: "sha256:fixture",
    },
  ],
};

test("partial reasons remain distinct from unexecuted suggestions", async ({
  page,
}) => {
  await routes(page);
  await page.route(
    "**/conversations/conversation-fixture/messages**",
    async (route) => {
      await route.fulfill({
        json: {
          data: [
            {
              messageId: "partial-message",
              turnId: "turn-fixture",
              sequence: 1,
              role: "assistant",
              content: {
                ...answer,
                partial: true,
                queryResultRefs: [],
                formalRefs: [],
                faultChain: [],
                warnings: [
                  "source_read_failed:query_logs:SOURCE_DEGRADED",
                  "source_read_partial:resource_history",
                  "model_answer_incomplete",
                  "MODEL_OUTPUT_INVALID",
                  "BUDGET_EXHAUSTED",
                ],
                nextChecks: ["核对退出日志"],
              },
            },
          ],
          meta: { nextCursor: "" },
        },
      });
    },
  );
  await page.goto(base + "?conversation=conversation-fixture");
  await expect(
    page.getByText("来源读取失败：query_logs:SOURCE_DEGRADED", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("来源证据不完整：resource_history", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("模型回答不完整", { exact: true })).toBeVisible();
  await expect(
    page.getByText("模型输出格式无效，已保留取证结果", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("本轮预算已耗尽", { exact: true })).toBeVisible();
  await expect(
    page.getByText("下一步只读检查（尚未执行）", { exact: true }),
  ).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByText("核对退出日志", { exact: true })).toBeVisible();
});
async function routes(page: any, overrides: { clarification?: boolean } = {}) {
  await page.route("**/api/v1/**", async (route: any) => {
    const u = new URL(route.request().url());
    let data: any = {};
    if (u.pathname.endsWith("/capabilities"))
      data = {
        enabled: true,
        taskKinds: [
          "data_query",
          "resource_troubleshooting",
          "incident_followup",
        ],
        catalog: {
          datasets: [
            {
              id: "executions.success-rate",
              authorizationDomain: "operational",
            },
          ],
        },
        sourceCapabilities: [
          {
            datasetId: "logs.records",
            operation: "samples",
            collectorVersion: "fixture/v1",
            coverageFrom: "2026-10-12T00:00:00Z",
            coverageTo: "2026-10-13T00:00:00Z",
          },
        ],
        sourceCapabilityStatus: "available",
      };
    else if (u.pathname.endsWith("/conversations")) data = [c];
    else if (u.pathname.endsWith("/messages"))
      data = [
        {
          messageId: "message-fixture",
          turnId: "turn-fixture",
          sequence: 1,
          role: "assistant",
          content: answer,
        },
      ];
    else if (u.pathname.endsWith("/formal-references"))
      data = {
        reference: answer.formalRefs[0],
        metadata: { revision: 1, status: "hypothesis" },
      };
    else if (u.pathname.includes("/query-results/"))
      data = {
        queryResultId: "query-fixture",
        facts: [],
        result: {
          datasetId: "logs.records",
          status: "partial",
          exact: false,
          truncated: true,
          dimension: "",
          unit: "logs",
          rows: [{ dimension: "", value: 1 }],
          snapshotReadAt: "2026-10-13T00:00:00Z",
          scopeDigest: "sha256:fixture",
          warnings: ["bounded_samples_not_total"],
          degradedSources: [],
          samples: [
            {
              timestamp: "2026-10-13T00:00:00Z",
              level: "error",
              message: "password=[REDACTED] <script>fixture</script>",
            },
          ],
        },
      };
    else if (u.pathname.endsWith("/events")) {
      await route.fulfill({ contentType: "text/event-stream", body: "" });
      return;
    } else if (u.pathname.endsWith("/turns/turn-fixture"))
      data = overrides.clarification
        ? {
            ...c.activeTurn,
            state: "awaiting_user",
            answer: {
              ...answer,
              answerKind: "clarification",
              clarification: {
                clarificationId: "turn-fixture",
                question: "选择它的身份",
                missingFields: ["referentCanonicalId"],
                candidates: ["pod-a", "pod-b"],
              },
            },
          }
        : c.activeTurn;
    else
      data = overrides.clarification
        ? {
            ...c,
            activeTurn: {
              ...c.activeTurn,
              state: "awaiting_user",
              answer: {
                ...answer,
                answerKind: "clarification",
                clarification: {
                  clarificationId: "turn-fixture",
                  question: "选择它的身份",
                  missingFields: ["referentCanonicalId"],
                  candidates: ["pod-a", "pod-b"],
                },
              },
            },
          }
        : c;
    await route.fulfill({ json: { data, meta: { nextCursor: "" } } });
  });
}

test("exact version, chain, sanitized samples and scope editor", async ({
  page,
}) => {
  await routes(page);
  const failures: string[] = [];
  page.on("pageerror", (e) => failures.push(e.message));
  await page.goto(base + "?conversation=conversation-fixture");
  await expect(page.getByText("待验证的故障传播假设")).toBeVisible();
  await expect(
    page.getByText("脱敏日志样例；样例数不代表日志总量。"),
  ).toBeVisible();
  await expect(
    page.getByText("password=[REDACTED] <script>fixture</script>", {
      exact: true,
    }),
  ).toBeVisible();
  expect(
    await page
      .locator("script")
      .filter({ hasText: "fixture</script>" })
      .count(),
  ).toBe(0);
  await page.getByRole("button", { name: "查看 RCA 版本 1" }).click();
  await expect(page.getByText("已引用的正式版本")).toBeVisible();
  await page.getByRole("button", { name: "Close" }).click();
  await page.getByRole("button", { name: "调整后续轮次的范围或时间" }).click();
  await page.getByLabel("调整命名空间").fill("private");
  const request = page.waitForRequest(
    "**/conversations/conversation-fixture/binding",
  );
  await page.getByRole("button", { name: "确认应用到后续轮次" }).click();
  expect((await request).postDataJSON()).toMatchObject({
    expectedConversationRevision: 2,
    namespaceSelection: ["private"],
  });
  await expect(
    page.getByRole("button", { name: "设置保留", exact: true }),
  ).toHaveCount(0);
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
  }
  expect(failures).toEqual([]);
});

test("clarified referent is submitted explicitly", async ({ page }) => {
  await routes(page, { clarification: true });
  await page.goto(base + "?conversation=conversation-fixture");
  const select = page.getByRole("combobox", { name: "确认历史资源指代" });
  await select.focus();
  await select.press("ArrowDown");
  await select.press("Enter");
  await page.getByLabel("问题", { exact: true }).fill("继续检查它");
  const request = page.waitForRequest(
    "**/conversations/conversation-fixture/messages",
  );
  await page.getByRole("button", { name: /^发\s*送$/ }).click();
  expect((await request).postDataJSON()).toMatchObject({
    referentCanonicalId: "pod-a",
    clarificationId: "turn-fixture",
  });
});

test("independent administrator hold UI requires a reason", async ({
  page,
}) => {
  await routes(page);
  await page.goto(base + "?qaRole=admin");
  await expect(
    page.getByRole("button", { name: "设置保留", exact: true }),
  ).toBeDisabled();
  await page
    .getByLabel("保留轮次 ID")
    .fill("11111111-1111-4111-8111-111111111111");
  await page.getByLabel("保留原因").fill("Fixture审计保留");
  const request = page.waitForRequest("**/assistant/legal-holds");
  await page.getByRole("button", { name: "设置保留", exact: true }).click();
  expect((await request).postDataJSON()).toMatchObject({
    held: true,
    reason: "Fixture审计保留",
  });
});

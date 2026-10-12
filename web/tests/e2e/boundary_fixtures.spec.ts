// Explicit API transport fixtures. These validate real browser rendering, not real graph or command execution acceptance.
import { test, expect } from "./session";
import { resource } from "../fixtures";
import type {
  ResourceGraphV2,
  ResourceRelationV2,
} from "../../src/api/generated/models";

test("fixture cyclic graph, isolated node, Unicode identifiers and keyboard navigation", async ({
  page,
  config,
}) => {
  const nodes = Array.from({ length: 4 }, (_, i) => ({
    ...resource,
    canonicalId: resource.canonicalId + "/" + i,
    stableId: "fixture-" + i,
    name:
      i === 3
        ? "孤立节点 IPv6 ::1"
        : "循环节点 " + i + " 中文 Unicode 🧭 " + "长名称".repeat(12),
  }));
  const edges: ResourceRelationV2[] = [0, 1, 2].map((i) => ({
    from: nodes[i]!.canonicalId,
    to: nodes[(i + 1) % 3]!.canonicalId,
    kind: "depends_on",
    confidence: 0.75,
    observedAt: "2026-10-11T00:00:00Z",
    validFrom: "2026-10-11T00:00:00Z",
    ttl: 60,
    provenance: {
      sourceRegistrationId: "explicit-test-fixture",
      ruleVersion: "fixture-v1",
      observedAt: "2026-10-11T00:00:00Z",
      sourceValues: null,
    },
  }));
  const graph: ResourceGraphV2 = {
    schemaVersion: "resource-graph/v2",
    graphRevision: { ownerEpoch: 1, graphGeneration: 1 },
    ownerInstance: "explicit-test-fixture",
    collectedAt: new Date().toISOString(),
    freshness: "stale",
    partial: false,
    warnings: [],
    degradedSources: [],
    nodes,
    edges,
    budgets: {
      maxDepth: 2,
      storageMaxDepth: 2,
      maxNodes: 200,
      maxEdges: 400,
      nodeCount: 4,
      edgeCount: 3,
      truncated: false,
    },
    rankedEvidence: null,
    conflicts: null,
  };
  await page.route("**/api/v1/resources/neighbors?*", async (route) => {
    const p = new URL(route.request().url()).searchParams;
    expect(p.get("canonicalId")).toBe(nodes[0]!.canonicalId);
    expect(p.get("depth")).toBe("2");
    expect(p.get("maxNodes")).toBe("200");
    await route.fulfill({ json: { data: graph } });
  });
  await page.route("**/api/v1/resources/by-canonical-id?*", (route) =>
    route.fulfill({ json: { data: graph } }),
  );
  await page.goto(
    config.baseURL +
      "/resources/detail?tab=graph&canonicalId=" +
      encodeURIComponent(nodes[0]!.canonicalId),
  );
  await expect(page.locator(".graph-canvas canvas").first()).toBeVisible();
  for (const [label, index] of [
    ["路径起点", 0],
    ["路径终点", 3],
  ] as const) {
    const box = page.getByRole("combobox", { name: label });
    await box.focus();
    await box.press("ArrowDown");
    for (let i = 0; i < index; i++) await box.press("ArrowDown");
    await box.press("Enter");
  }
  await page.getByRole("button", { name: "高亮关系路径", exact: true }).click();
  await expect(page.getByRole("status")).toHaveText(
    "两节点之间没有可达关系；孤立节点或断开图不会虚构路径。",
  );
  await expect(
    page.getByText("观测已过期", { exact: true }).first(),
  ).toBeVisible();
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    console.log(
      "Fixture graph overflow inspection",
      await page.evaluate(() => ({
        width: innerWidth,
        scroll: document.documentElement.scrollWidth,
        offenders: Array.from(document.querySelectorAll("main *"))
          .map((el) => ({
            tag: el.tagName,
            class: el.className,
            left: el.getBoundingClientRect().left,
            right: el.getBoundingClientRect().right,
          }))
          .filter((x) => x.right > innerWidth + 1)
          .slice(0, 12),
      })),
    );
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
    await page.screenshot({
      path:
        config.screenshotDirectory + "/fixture-cyclic-graph-" + width + ".png",
      animations: "disabled",
    });
  }
});

test("fixture command SSE cursor expiry, duplicates, bounded output and uncertain execution", async ({
  page,
  config,
}) => {
  const id = "explicit-fixture-execution";
  let attempts = 0;
  const event = (seq: number, eventType: string, payload: unknown) =>
    `id: ${id}:${seq}\ndata: ${JSON.stringify({ eventId: id + ":" + seq, eventType, executionId: id, occurredAt: new Date().toISOString(), payload })}\n\n`;
  await page.route("**/api/v1/command-executions/" + id, (route) =>
    route.fulfill({
      json: {
        data: {
          executionId: id,
          state: "execution_unknown",
          suggestionMatch: "not_applicable",
          outputTruncated: false,
          extensions: {},
        },
      },
    }),
  );
  await page.route(
    "**/api/v1/command-executions/" + id + "/post-check",
    (route) =>
      route.fulfill({
        json: {
          data: { status: "execution_unknown", verdict: "inconclusive" },
        },
      }),
  );
  await page.route(
    "**/api/v1/command-executions/" + id + "/events",
    async (route) => {
      attempts++;
      expect(route.request().headers().authorization).toMatch(/^Bearer /);
      if (attempts === 1)
        await route.fulfill({
          contentType: "text/event-stream",
          body: event(1, "output", {
            stream: "stdout",
            bytes: Buffer.from("OLD_UNTRUSTED").toString("base64"),
          }),
        });
      else if (attempts === 2) {
        expect(route.request().headers()["last-event-id"]).toBe(id + ":1");
        await route.fulfill({
          status: 410,
          json: { code: "CURSOR_EXPIRED", message: "explicit test fixture" },
        });
      } else {
        expect(route.request().headers()["last-event-id"]).toBeUndefined();
        const output = event(1, "output", {
          stream: "stdout",
          bytes: Buffer.from("中文输出\n" + "line\n".repeat(15000)).toString(
            "base64",
          ),
        });
        await route.fulfill({
          contentType: "text/event-stream",
          body:
            output +
            output +
            event(2, "output", {
              stream: "stderr",
              bytes: Buffer.from("stderr Unicode 🧭").toString("base64"),
            }) +
            event(2, "output", {
              stream: "stderr",
              bytes: Buffer.from("stderr Unicode 🧭").toString("base64"),
            }) +
            event(3, "state", { state: "execution_unknown" }),
        });
      }
    },
  );
  await page.goto(
    config.baseURL +
      "/incidents/" +
      config.incidentId +
      "?tab=action&executionId=" +
      id,
  );
  await expect(
    page.getByText("执行结果不确定：请核对目标状态，暂勿重复提交", {
      exact: true,
    }),
  ).toBeVisible();
  await expect.poll(() => attempts).toBe(3);
  await page.getByText("无终端依赖的文本输出", { exact: true }).click();
  const output = page.locator(".execution-terminal + details pre");
  await expect(output).toContainText("stderr Unicode 🧭");
  const body = await output.innerText();
  expect(body).not.toContain("OLD_UNTRUSTED");
  expect(body.match(/stderr Unicode 🧭/g)?.length).toBe(1);
  expect(body.length).toBeLessThanOrEqual(65536);
  await expect(
    page.getByText("执行实时输出连接中断", { exact: true }),
  ).toHaveCount(0);
  await expect(page.getByText("输出已截断", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("textbox", { name: "实际 Bash 命令" }),
  ).toHaveText("");
  await expect(
    page.getByRole("button", { name: "2 · 评估实际命令风险", exact: true }),
  ).toBeDisabled();
  await page.screenshot({
    path: config.screenshotDirectory + "/fixture-uncertain-bounded-output.png",
    animations: "disabled",
  });
  await page.goto(config.baseURL + "/overview");
  await expect(page.getByText("可见资源", { exact: true })).toBeVisible();
  expect(attempts).toBe(3);
});

test("fixture command suggestion match labels preserve all four backend outcomes", async ({
  page,
  config,
}) => {
  const labels = {
    exact: "与建议完全一致",
    modified: "修改了建议命令",
    unrelated: "与建议无关",
    not_applicable: "不适用",
  };
  for (const [match, label] of Object.entries(labels)) {
    const id = "explicit-fixture-match-" + match;
    await page.route("**/api/v1/command-executions/" + id, (route) =>
      route.fulfill({
        json: {
          data: {
            executionId: id,
            state: "succeeded",
            suggestionMatch: match,
            exitCode: 0,
            outputTruncated: false,
            extensions: {},
          },
        },
      }),
    );
    await page.route(
      "**/api/v1/command-executions/" + id + "/events",
      (route) =>
        route.fulfill({
          contentType: "text/event-stream",
          body: `id: ${id}:1\ndata: ${JSON.stringify({ eventId: id + ":1", eventType: "state", executionId: id, occurredAt: new Date().toISOString(), payload: { state: "succeeded" } })}\n\n`,
        }),
    );
    await page.route(
      "**/api/v1/command-executions/" + id + "/post-check",
      (route) =>
        route.fulfill({
          json: { data: { status: "completed", verdict: "not_resolved" } },
        }),
    );
    await page.goto(
      config.baseURL +
        "/incidents/" +
        config.incidentId +
        "?tab=action&executionId=" +
        id,
    );
    await expect(page.getByText(label, { exact: true }).first()).toBeVisible();
    await expect(
      page.getByRole("textbox", { name: "实际 Bash 命令" }),
    ).toHaveText("");
  }
});

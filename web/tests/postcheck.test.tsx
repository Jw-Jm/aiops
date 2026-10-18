import { it, expect, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  waitFor,
  fireEvent,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { PostCheckPanel } from "../src/features/action/PostCheckPanel";
import { setCredentials } from "../src/api/client";
import { MemoryRouter, useLocation } from "react-router-dom";
it("distinguishes pending checks from final verdicts and stops polling final results", async () => {
  setCredentials("TEST-FIXTURE");
  for (const [status, verdict, label] of [
    ["pending", "inconclusive", "等待检查"],
    ["completed", "not_resolved", "未确认修复"],
    ["completed", "resolved", "已恢复"],
    ["completed", "inconclusive", "尚无定论"],
  ]) {
    const fetcher = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            data: {
              executionId: "test",
              status,
              verdict,
              checkedAt: status === "completed" ? "2026-10-11T00:00:00Z" : null,
            },
            requestId: "fixture-request",
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        ),
    );
    vi.stubGlobal("fetch", fetcher);
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <PostCheckPanel executionId="test" />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await waitFor(() =>
      expect(screen.getAllByText(label).length).toBeGreaterThan(0),
    );
    expect(fetcher).toHaveBeenCalledTimes(1);
    cleanup();
    client.clear();
  }
  vi.unstubAllGlobals();
});

function CurrentLocation() {
  return <output data-testid="location">{useLocation().search}</output>;
}
it("displays frozen verification and keeps explicit follow-up/rollback links without copying commands", async () => {
  const data = {
    executionId: "original",
    executionState: "succeeded",
    status: "completed",
    verdict: "inconclusive",
    detailStatus: "recorded",
    report: {
      schemaVersion: "command-post-check/v1",
      reason: "source_unavailable",
      completedAt: "2026-10-18T00:00:00Z",
      coverage: "recipe_target_only",
      facts: {
        checkedAt: "2026-10-18T00:01:00Z",
        collectedAfter: "0001-01-01T00:00:00Z",
        recipeName: "node-failure",
        recipeVersion: "recipe",
        evidenceRefs: ["evidence-1"],
      },
    },
    relatedExecutions: [
      {
        executionId: "parent",
        relation: "follow_up",
        direction: "parent",
        state: "failed",
      },
      {
        executionId: "child",
        relation: "rollback",
        direction: "child",
        state: "prepared",
      },
    ],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify({ data }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter
        initialEntries={[
          "/?tab=action&executionId=original&actionPlanId=old-plan",
        ]}
      >
        <PostCheckPanel executionId="original" />
        <CurrentLocation />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(await screen.findByText("未取得合格来源事实")).toBeVisible();
  expect(screen.getByText(/观测：未取得/)).toBeVisible();
  expect(screen.getByText(/仅覆盖当前配方的目标异常/)).toBeVisible();
  const evidence = new URLSearchParams(
    screen
      .getByRole("link", { name: "验证证据 evidence-1" })
      .getAttribute("href")!
      .slice(1),
  );
  expect(evidence.get("tab")).toBe("evidence");
  expect(evidence.get("evidenceId")).toBe("evidence-1");
  const parent = new URLSearchParams(
    screen
      .getByRole("link", { name: /原执行.*parent/ })
      .getAttribute("href")!
      .slice(1),
  );
  expect(parent.get("executionId")).toBe("parent");
  expect(parent.has("relatedExecutionId")).toBe(false);
  fireEvent.click(
    screen.getByRole("button", { name: "关联本次执行，准备下一轮处置" }),
  );
  let url = new URLSearchParams(screen.getByTestId("location").textContent!);
  expect(url.get("relatedExecutionId")).toBe("original");
  expect(url.get("executionRelation")).toBe("follow_up");
  expect(url.has("executionId")).toBe(false);
  expect(url.has("actionPlanId")).toBe(false);
  fireEvent.click(
    screen.getByRole("button", { name: "关联本次执行，准备人工回退" }),
  );
  url = new URLSearchParams(screen.getByTestId("location").textContent!);
  expect(url.get("executionRelation")).toBe("rollback");
  cleanup();
  client.clear();
  vi.unstubAllGlobals();
});

it("discloses unsupported post-check targets instead of treating command success as recovery", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            data: {
              executionId: "pod-execution",
              status: "completed",
              verdict: "inconclusive",
              applicability: {
                status: "unsupported",
                targetKind: "Pod",
                reason: "no_trusted_post_check",
              },
            },
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        ),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <PostCheckPanel executionId="pod-execution" />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(
    await screen.findByText("此目标没有已发布的可信后检查判据"),
  ).toBeVisible();
  expect(screen.getAllByText("尚无定论").length).toBeGreaterThan(0);
  expect(screen.queryByText("已恢复")).not.toBeInTheDocument();
  cleanup();
  client.clear();
  vi.unstubAllGlobals();
});

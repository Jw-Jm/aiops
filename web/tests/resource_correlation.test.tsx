import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ResourceCorrelation } from "../src/features/assistant/ResourceCorrelation";
import { setCredentials } from "../src/api/client";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("locates the committed source step for support and keeps its gaps visible", async () => {
  setCredentials("FIXTURE", "tenant");
  const step = "00000000-0000-4000-8000-000000000001";
  const fetch = vi.fn(
    async (_input: RequestInfo | URL) =>
      new Response(
        JSON.stringify({
          data: {
            tool: "resource_history",
            data: { window: { from: "2026-10-15T00:00:00Z" }, records: [] },
          },
          requestId: "fixture",
        }),
        { headers: { "Content-Type": "application/json" } },
      ),
  );
  vi.stubGlobal("fetch", fetch);
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <ResourceCorrelation
        turnId="turn"
        scopeKey="owner-scope"
        context={{
          sourceSteps: [
            {
              stepId: step,
              tool: "resource_history",
              canonicalId: "uid",
              contentDigest: "digest",
            },
          ],
          resourceAssessments: [
            {
              canonicalId: "uid",
              ruleId: "PVC/pending",
              reason: "Pending 未确认 CSI 故障",
              supports: [step],
              conflicts: [],
              factRefs: [],
              missingEvidence: ["history_missing"],
              sourceQuality: "observation_only",
              nextChecks: ["resource_history"],
            },
          ],
        }}
      />
    </QueryClientProvider>,
  );
  expect(screen.getByText(/history_missing/)).toBeVisible();
  expect(screen.getByText("Pending 未确认 CSI 故障")).toBeVisible();
  fireEvent.click(screen.getAllByRole("button", { name: /定位来源步骤/ })[0]);
  expect(await screen.findByText(/2026-10-15T00:00:00Z/)).toBeVisible();
  expect(String(fetch.mock.calls[0][0])).toContain(
    `/turns/turn/source-steps/${step}`,
  );
});

import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { FaultChainPanel } from "../src/features/assistant/FaultChainPanel";
import { setCredentials } from "../src/api/client";
import type { AssistantAnswerV1 } from "../src/api/generated/models";

vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: {
      tenantId: "fixture-tenant",
      subject: "fixture-owner",
      roles: ["operator"],
      namespaceScopes: [{ namespace: "apps" }],
    },
  }),
}));
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("links the selected edge to explicit conflict and missing evidence without claiming time order", async () => {
  setCredentials("FIXTURE", "fixture-tenant");
  const window = {
    from: "2026-10-14T00:00:00Z",
    to: "2026-10-14T00:10:00Z",
    timezone: "UTC",
  };
  const edges = ["a", "b"].map((name, i) => ({
    relation: {
      from: name,
      to: "dependency-" + name,
      kind: "communicates_with",
    },
    relationDigest: "digest-" + i,
    callDirection: "unknown",
    temporalAssessment: "unreliable",
    comparisonStatus: "baseline_missing",
    queryResultRefs: [],
    observations: [
      {
        ruleId: "R6/fixture",
        reason: "竞争原因尚未排除：" + name,
        supports: [],
        conflicts: [],
        factRefs: [],
        ...(i === 0
          ? {
              resourceHealth: {
                canonicalId: "shared-node",
                stepId: "fixture-health-step",
                namespace: "",
                observedAt: window.from,
                conditionType: "Ready",
                conditionStatus: "False",
                sources: [
                  {
                    sourceRegistrationId: "fixture-source",
                    revision: 3,
                    scopeDigest: "fixture-digest",
                  },
                ],
              },
              relatedRelations: [
                {
                  from: "a",
                  to: "shared-node",
                  kind: "scheduled_on",
                  observedAt: window.from,
                },
              ],
            }
          : {}),
      },
    ],
    missingEvidence: ["缺少因果证据：" + name],
    nextChecks: ["检查应用日志：" + name],
  }));
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            data: {
              schemaVersion: "fault-chain-context/v1",
              ruleVersion: "fault-chain-rules/v1",
              request: {
                schemaVersion: "fault-chain-request/v1",
                targetCanonicalId: "a",
                fault: window,
                maxEdges: 4,
              },
              asOf: window.to,
              status: "partial",
              edges,
              queryResultRefs: [],
              evidenceRefs: [],
              missingEvidence: [],
            },
            requestId: "fixture",
          }),
          { headers: { "Content-Type": "application/json" } },
        ),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const answer: AssistantAnswerV1 = {
    schemaVersion: "assistant-answer/v1",
    answerKind: "diagnosis",
    summary: "观测假设",
    factRefs: [],
    queryResultRefs: [],
    evidenceRefs: [],
    entityRefs: [],
    missingEvidence: [],
    scopeSummary: "apps",
    asOf: window.to,
    partial: true,
    degradedSources: [],
    warnings: [],
    nextChecks: [],
    hypotheses: [],
  };
  const prepare = vi.fn();
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <FaultChainPanel
          answer={answer}
          turnId="fixture-turn"
          onReadOnlyCheck={prepare}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(await screen.findByText("缺少因果证据：a")).toBeDefined();
  expect(screen.getByText("时间先后：未验证")).toBeDefined();
  expect(
    screen
      .getByRole("link", { name: "健康观测资源：shared-node" })
      .getAttribute("href"),
  ).toContain("canonicalId=shared-node");
  expect(screen.getByText(/已提交状态：Ready=False/)).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: /健康观测：/ }));
  expect(document.activeElement?.textContent).toBe("竞争原因尚未排除：a");
  fireEvent.click(
    screen.getByRole("button", { name: "定位节点 dependency-b" }),
  );
  expect(
    screen
      .getByRole("button", { name: "链路 2 · communicates_with" })
      .getAttribute("aria-pressed"),
  ).toBe("true");
  fireEvent.click(
    screen.getByRole("button", { name: "链路 2 · communicates_with" }),
  );
  expect(await screen.findByText("缺少因果证据：b")).toBeDefined();
  expect(screen.queryByText("缺少因果证据：a")).toBeNull();
  expect(screen.getByText("恢复未验证")).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "准备解释这些证据" }));
  expect(prepare).toHaveBeenCalledWith(
    expect.stringContaining("get_assistant_context"),
  );
  expect(screen.getByText("证据已提交；模型解释使用独立轮次")).toBeDefined();
  client.clear();
});

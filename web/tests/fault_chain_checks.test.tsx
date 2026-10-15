import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { FaultChainGapCheck } from "../src/features/assistant/FaultChainGapCheck";
import { FaultChainPanel } from "../src/features/assistant/FaultChainPanel";
import { QueryResultView } from "../src/features/assistant/QueryResultView";
import { setCredentials } from "../src/api/client";
import type {
  AssistantAnswerV1,
  AssistantConversationV1,
  AssistantFaultChainContextV1,
} from "../src/api/generated/models";
vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: { tenantId: "tenant", subject: "owner", roles: ["operator"] },
  }),
}));
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
const window = {
  from: "2026-10-14T00:00:00Z",
  to: "2026-10-14T00:01:00Z",
  timezone: "UTC",
};
const digest = "sha256:" + "1".repeat(64);
const context: AssistantFaultChainContextV1 = {
  schemaVersion: "fault-chain-context/v1",
  ruleVersion: "fault-chain-rules/v1",
  asOf: window.to,
  status: "partial",
  request: {
    schemaVersion: "fault-chain-request/v1",
    targetCanonicalId: "target",
    fault: window,
    baseline: {
      ...window,
      from: "2026-10-13T23:58:00Z",
      to: "2026-10-13T23:59:00Z",
    },
    maxEdges: 4,
  },
  edges: [
    {
      relation: {
        from: "target",
        to: "peer",
        kind: "communicates_with",
        observedAt: window.from,
        validFrom: window.from,
        ttl: 120,
        confidence: 1,
        provenance: {
          sourceRegistrationId: "fixture",
          ruleVersion: "fixture/v1",
          sourceValues: null,
          observedAt: window.from,
        },
      },
      relationDigest: digest,
      callDirection: "incoming",
      temporalAssessment: "unreliable",
      comparisonStatus: "baseline_missing",
      queryResultRefs: [],
      observations: [],
      missingEvidence: ["network_window_statistics"],
      nextChecks: [],
    },
  ],
  queryResultRefs: [],
  evidenceRefs: [],
  missingEvidence: [],
};
const conversation = {
  conversationId: "conversation",
  revision: 7,
} as AssistantConversationV1;
function response(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
it("submits a fixed readonly check tied to an old turn with unchanged windows, relation and direction", async () => {
  setCredentials("FIXTURE", "tenant");
  const submitted = vi.fn(),
    fetcher = vi.fn(async () =>
      response({ data: { turnId: "new-turn", state: "queued" } }, 202),
    );
  vi.stubGlobal("fetch", fetcher);
  render(
    <FaultChainGapCheck
      context={context}
      edge={context.edges[0]}
      gap="network_window_statistics"
      turnId="old-turn"
      controls={{ conversation, disabled: false, onSubmitted: submitted }}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "执行冻结窗口统计检查" }));
  expect(
    await screen.findByText("检查已提交到独立轮次；原缺口保留。"),
  ).toBeDefined();
  const [url, options] = fetcher.mock.calls[0] as unknown as [
    string,
    RequestInit,
  ];
  expect(url).toContain(
    "/conversations/conversation/fault-chain-investigations",
  );
  expect(new Headers(options.headers).get("Idempotency-Key")).toBeTruthy();
  const request = JSON.parse(options.body as string);
  expect(request.expectedConversationRevision).toBe(7);
  expect(request.parentTurnId).toBe("old-turn");
  expect(request.faultChainRequest).toEqual({
    ...context.request,
    readOnlyCheck: {
      originTurnId: "old-turn",
      relationDigest: digest,
      gapCode: "network_window_statistics",
      canonicalId: "peer",
      tool: "execute_analysis_query",
      direction: "incoming",
    },
  });
  expect(submitted).toHaveBeenCalledWith({
    turnId: "new-turn",
    state: "queued",
  });
  expect(context.edges[0].missingEvidence).toEqual([
    "network_window_statistics",
  ]);
  expect(screen.getByRole("button").hasAttribute("disabled")).toBe(true);
});
it("blocks concurrent submission and reports scope denial without resubmission or scope expansion", async () => {
  setCredentials("FIXTURE", "tenant");
  const submitted = vi.fn(),
    fetcher = vi.fn(async () =>
      response({ code: "DENIED", message: "范围不足" }, 403),
    );
  vi.stubGlobal("fetch", fetcher);
  const props = {
    context,
    edge: context.edges[0],
    gap: "network_window_statistics",
    turnId: "old-turn",
    controls: { conversation, disabled: true, onSubmitted: submitted },
  };
  const view = render(<FaultChainGapCheck {...props} />);
  fireEvent.click(screen.getByRole("button"));
  expect(fetcher).not.toHaveBeenCalled();
  view.rerender(
    <FaultChainGapCheck
      {...props}
      controls={{ ...props.controls, disabled: false }}
    />,
  );
  fireEvent.click(screen.getByRole("button"));
  expect(await screen.findByText("DENIED · 范围不足")).toBeDefined();
  expect(
    screen.getByRole("link", { name: "检查现有调查范围" }).getAttribute("href"),
  ).toBe("#assistant-binding-editor");
  expect(submitted).not.toHaveBeenCalled();
  expect(fetcher).toHaveBeenCalledTimes(1);
});
it("links a comparison value and its original Fact to authorized Evidence readback", async () => {
  setCredentials("FIXTURE", "tenant");
  const query = {
    data: {
      queryResultId: "query",
      contentDigest: digest,
      facts: [
        {
          schemaVersion: "query-fact/v1",
          factId: digest,
          row: 2,
          column: "value",
          value: 4,
          unit: "percentage_points",
          exact: true,
        },
      ],
      result: {
        schemaVersion: "query-result/v1",
        datasetId: "network.http-5xx-rate",
        status: "complete",
        exact: true,
        unit: "percent",
        dimension: "comparison",
        rows: [
          { dimension: "baseline", value: 1 },
          { dimension: "current", value: 5 },
          { dimension: "increase", value: 4 },
        ],
        timeRange: window,
        warnings: [],
        degradedSources: [],
        snapshotReadAt: window.to,
        scopeDigest: digest,
        derivationEvidenceRefs: ["evidence"],
        networkContext: {
          population: "completed_http_session_observations",
          observationPoint: "s-p",
          timeReliable: false,
        },
      },
    },
  };
  const fetcher = vi.fn(async (url: string) =>
    url.includes("/evidence/")
      ? response({
          data: {
            sourceSystem: "fixture-source",
            observedFrom: window.from,
            observedTo: window.to,
            timeReliable: false,
            replayState: "archived_verified",
            factSlice: { responses: 1000 },
          },
        })
      : response(query),
  );
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <QueryResultView
          id="query"
          answer={{ factRefs: [] } as unknown as AssistantAnswerV1}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: `定位 Fact ${digest}` }),
  );
  expect(await screen.findByText("4 percentage_points")).toBeDefined();
  expect(screen.getByText("第 3 行 / value")).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "查看证据 evidence" }));
  expect(await screen.findByText("fixture-source")).toBeDefined();
  expect(
    fetcher.mock.calls.some(([url]) => url.includes("/evidence/evidence")),
  ).toBe(true);
  client.clear();
});

it("reads the authorized original round from a check result even when it is outside the visible message page", async () => {
  setCredentials("FIXTURE", "tenant");
  const result = {
    ...context,
    edges: [],
    request: {
      ...context.request,
      readOnlyCheck: {
        originTurnId: "original",
        gapCode: "network_window_statistics",
        tool: "execute_analysis_query",
        canonicalId: "peer",
        direction: "incoming",
        relationDigest: digest,
      },
    },
  };
  const fetcher = vi.fn(async (url: string) =>
    response({ data: url.includes("/original/") ? context : result }),
  );
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <FaultChainPanel
          answer={{ factRefs: [] } as unknown as AssistantAnswerV1}
          turnId="check-result"
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "查看原调查轮次与缺口" }),
  );
  expect(
    await screen.findByRole("button", { name: "定位节点 peer" }),
  ).toBeDefined();
  expect(
    fetcher.mock.calls.some(([url]) =>
      url.includes("/original/fault-chain-context"),
    ),
  ).toBe(true);
  expect(
    screen.getByRole("dialog", { name: "原调查已提交证据与缺口" }),
  ).toBeDefined();
  client.clear();
});

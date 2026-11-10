// UI mechanism fixture; no database, browser visual or live authorization claim.
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import {
  InvestigationStopConfirm,
  freezeStopTarget,
  stopTargetMatches,
} from "../src/features/assistant/InvestigationStopConfirm";
import { UnifiedInvestigationHistory } from "../src/features/assistant/UnifiedInvestigationHistory";
import { UnifiedInvestigationDetail } from "../src/features/assistant/UnifiedInvestigationDetail";
import { setNativeCredentials, clearCredentials } from "../src/api/client";

const api = vi.hoisted(() => ({
  getInvestigation: vi.fn(),
  getInvestigationSteps: vi.fn(),
  getAssistantTurn: vi.fn(),
}));
vi.mock("../src/api/generated/platform", () => ({
  getInvestigation: api.getInvestigation,
  getInvestigationSteps: api.getInvestigationSteps,
  getAssistantTurn: api.getAssistantTurn,
}));
vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: {
      tenantId: "tenant-1",
      subject: "operator-a",
      roles: ["operator"],
      clusterScopes: [],
      namespaceScopes: [],
    },
  }),
}));

const base = {
  jobId: "job-1",
  turnId: "turn-1",
  conversationId: "conv-1",
  conversationRevision: 7,
  subject: "operator-a",
  tenantId: "tenant-1",
  scopeKey: "scope-1",
  epoch: 3,
  idempotencyKey: "idem-1",
};

const job = {
  schemaVersion: "investigation-job/v3",
  tenantId: "tenant-1",
  jobId: "job-1",
  incidentId: "incident-1",
  subject: "operator-a",
  triggerRevision: 4,
  triggerKind: "manual",
  policyVersion: "policy-1",
  scope: { clusterUid: "cluster-a" },
  effectiveScopeDigest: `sha256:${"a".repeat(64)}`,
  toolCatalogDigest: `sha256:${"b".repeat(64)}`,
  state: "succeeded",
  budget: { toolCalls: 4, modelRequests: 1, durationSeconds: 120 },
  budgetReserved: { toolCalls: 4, modelRequests: 1, durationSeconds: 120 },
  budgetConsumed: { toolCalls: 2, modelRequests: 1, durationSeconds: 30 },
  focus: "内存通道",
  expiresAt: "2026-11-09T10:00:00Z",
  eventSeq: 7,
  nextCallSeq: 2,
  result: { evidenceRefs: ["evidence-1"], formalRefs: [] },
  conversationId: "conv-1",
  turnId: "turn-1",
};

const reply = (data: unknown) =>
  Promise.resolve({
    status: 200,
    headers: new Headers(),
    data: { data, meta: {}, requestId: "unit" },
  });

function renderDetail(props: {
  jobId?: string;
  turnId?: string;
  onClose?: () => void;
  onStop?: (target: { jobId: string; turnId: string }) => void;
}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <UnifiedInvestigationDetail
          jobId={props.jobId}
          turnId={props.turnId}
          scopeKey="scope-1"
          onClose={props.onClose ?? (() => {})}
          onStop={props.onStop}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  setNativeCredentials("tenant-1", "fixture-csrf-key-0001");
  api.getInvestigation.mockImplementation(() => reply(job));
  api.getInvestigationSteps.mockImplementation(() =>
    reply([
      {
        stepId: "step-1",
        tool: "cluster.read",
        state: "succeeded",
        startedAt: "2026-11-09T09:59:00Z",
        completedAt: "2026-11-09T09:59:10Z",
      },
    ]),
  );
  api.getAssistantTurn.mockImplementation(() =>
    reply({
      turnId: "turn-1",
      conversationId: "conv-1",
      jobId: "job-1",
      state: "succeeded",
      createdAt: "2026-11-09T09:58:00Z",
      conversationRevision: 7,
    }),
  );
});
afterEach(() => {
  cleanup();
  clearCredentials();
});

it("freezes the job, turn, conversation, revision and subject generation", () => {
  const frozen = freezeStopTarget(base);
  expect(frozen).toEqual(base);
  // A later change of the source object cannot move the frozen target.
  const mutated = { ...base, jobId: "job-2" };
  expect(frozen.jobId).toBe("job-1");
  expect(mutated.jobId).toBe("job-2");
});

it("refuses a stop whose subject, scope, conversation or generation changed", () => {
  const frozen = freezeStopTarget(base);
  const current = {
    subject: "operator-a",
    scopeKey: "scope-1",
    conversationId: "conv-1",
    epoch: 3,
  };
  expect(stopTargetMatches(frozen, current)).toBe(true);
  expect(stopTargetMatches(frozen, { ...current, subject: "operator-b" })).toBe(
    false,
  );
  expect(stopTargetMatches(frozen, { ...current, scopeKey: "scope-2" })).toBe(
    false,
  );
  expect(
    stopTargetMatches(frozen, { ...current, conversationId: "conv-2" }),
  ).toBe(false);
  expect(stopTargetMatches(frozen, { ...current, epoch: 4 })).toBe(false);
});

it("opens the stop confirmation without sending any request", async () => {
  const originalFetch = globalThis.fetch;
  const calls: string[] = [];
  globalThis.fetch = (async (url: string) => {
    calls.push(String(url));
    return new Response("{}", { status: 200 });
  }) as typeof fetch;
  try {
    render(
      <InvestigationStopConfirm
        target={freezeStopTarget(base)}
        onCancel={() => {}}
        onConfirm={() => {}}
      />,
    );
    expect(calls).toEqual([]);
    // The dialog shows the frozen identifiers, not a live query result.
    expect(screen.getByText(/job-1/)).toBeInTheDocument();
    expect(screen.getByText(/turn-1/)).toBeInTheDocument();
    expect(screen.getByText(/conv-1/)).toBeInTheDocument();
    expect(
      screen.getByText("停止后已提交的事实会保留，但未确认的根因不会成立。"),
    ).toBeInTheDocument();
  } finally {
    globalThis.fetch = originalFetch;
  }
});

it("cancels nothing when the confirmation is dismissed", async () => {
  const user = userEvent.setup();
  const originalFetch = globalThis.fetch;
  const calls: string[] = [];
  globalThis.fetch = (async (url: string) => {
    calls.push(String(url));
    return new Response("{}", { status: 200 });
  }) as typeof fetch;
  try {
    render(
      <InvestigationStopConfirm
        target={freezeStopTarget(base)}
        onCancel={() => {}}
        onConfirm={() => {}}
      />,
    );
    await user.click(screen.getByRole("button", { name: "继续运行" }));
    expect(calls).toEqual([]);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

it("lists the investigations of the conversation and opens one on request", async () => {
  const user = userEvent.setup();
  const opened: string[] = [];
  render(
    <UnifiedInvestigationHistory
      entries={[
        { turnId: "turn-1", createdAt: "2026-11-09T10:00:00Z" },
        { turnId: "turn-2", createdAt: "2026-11-09T10:05:00Z", active: true },
      ]}
      onOpen={(turnId) => opened.push(turnId)}
    />,
  );
  expect(screen.getByText("turn-1")).toBeInTheDocument();
  expect(screen.getByText("turn-2")).toBeInTheDocument();
  const buttons = screen.getAllByRole("button", { name: "查看调查过程" });
  expect(buttons).toHaveLength(2);
  await user.click(buttons[1]);
  expect(opened).toEqual(["turn-2"]);
});

// W1: the detail that is opened with a job id must not stay in the loading
// skeleton. The turn query is disabled in that case, and a disabled query is
// pending for its whole lifetime.
it("loads the detail of a job id without waiting for a disabled turn query", async () => {
  renderDetail({ jobId: "job-1" });
  expect(await screen.findByText("公开步骤")).toBeInTheDocument();
  // The real state of the job is shown, so the skeleton is really gone.
  expect(screen.getByText("真实状态")).toBeInTheDocument();
  expect(screen.getByText("job-1")).toBeInTheDocument();
  expect(api.getInvestigation).toHaveBeenCalledWith(
    "job-1",
    expect.anything(),
  );
  // The turn query is disabled and must not be sent at all.
  expect(api.getAssistantTurn).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.getByText("内存通道")).toBeInTheDocument());
});

// W1: the second entry path resolves the job through the turn.
it("resolves the job of a turn id and shows the public steps", async () => {
  renderDetail({ turnId: "turn-1" });
  expect(await screen.findByText("公开步骤")).toBeInTheDocument();
  expect(api.getAssistantTurn).toHaveBeenCalledWith(
    "turn-1",
    expect.anything(),
  );
  expect(api.getInvestigation).toHaveBeenCalledWith(
    "job-1",
    expect.anything(),
  );
});

// W1: a running job is refreshed until it reaches a terminal state.
it("keeps refreshing a running job until it is finished", async () => {
  let calls = 0;
  api.getInvestigation.mockImplementation(() => {
    calls += 1;
    return reply(calls === 1 ? { ...job, state: "running" } : job);
  });
  renderDetail({ jobId: "job-1" });
  expect(await screen.findByText("公开步骤")).toBeInTheDocument();
  await waitFor(() => expect(api.getInvestigation).toHaveBeenCalledTimes(2), {
    timeout: 8000,
  });
});

// W1: the error state of the detail is recoverable.
it("shows the error of a refused detail and retries on request", async () => {
  api.getInvestigation.mockImplementation(() =>
    Promise.resolve({
      status: 403,
      headers: new Headers(),
      data: { code: "FORBIDDEN", message: "denied", requestId: "unit" },
    }),
  );
  renderDetail({ jobId: "job-1" });
  expect(
    await screen.findByText("没有此范围的访问权限"),
  ).toBeInTheDocument();
});

// W1: the steps of a running job follow the state of the job. The step list
// itself carries no job state, so a poll that reads the step payload never
// runs. The mounted detail must really show the step that appeared while the
// job was still running, and the terminal job must report its final consumed
// budget.
it("refreshes the steps of a running job and shows the step that appeared", async () => {
  let jobCalls = 0;
  api.getInvestigation.mockImplementation(() => {
    jobCalls += 1;
    if (jobCalls <= 2)
      return reply({
        ...job,
        state: "running",
        budgetConsumed: { toolCalls: 1, modelRequests: 0, durationSeconds: 10 },
      });
    return reply({
      ...job,
      state: "succeeded",
      budgetConsumed: { toolCalls: 3, modelRequests: 1, durationSeconds: 45 },
    });
  });
  let stepCalls = 0;
  api.getInvestigationSteps.mockImplementation(() => {
    stepCalls += 1;
    const first = {
      stepId: "step-1",
      toolName: "model",
      state: "succeeded",
      startedAt: "2026-11-09T09:59:00Z",
      completedAt: "2026-11-09T09:59:10Z",
    };
    if (stepCalls === 1) return reply([first]);
    return reply([
      first,
      {
        stepId: "step-2",
        toolName: "query_metrics",
        state: "running",
        startedAt: "2026-11-09T10:00:00Z",
      },
    ]);
  });
  renderDetail({ jobId: "job-1" });
  expect(await screen.findByText("公开步骤")).toBeInTheDocument();
  expect(await screen.findByText("分析现有证据")).toBeInTheDocument();
  // The step that appeared while the job was running is really displayed.
  expect(
    await screen.findByText("查询监控指标", {}, { timeout: 10000 }),
  ).toBeInTheDocument();
  // The terminal job reports the final consumed budget.
  expect(
    await screen.findByText(/工具调用：3 次/, {}, { timeout: 10000 }),
  ).toBeInTheDocument();
  // The poll stops with the job: no further step read is started.
  const settled = stepCalls;
  await new Promise((resolve) => setTimeout(resolve, 3000));
  expect(stepCalls).toBe(settled);
}, 20000);

// W1: the retry of a detail with a known job id must not read a turn. A turn
// request with an undefined turn id is a request for a job that does not
// exist.
it("retries the job of a known job id without reading a turn", async () => {
  const user = userEvent.setup();
  let calls = 0;
  api.getInvestigation.mockImplementation(() => {
    calls += 1;
    if (calls === 1) return Promise.reject(new Error("首次读取失败"));
    return reply(job);
  });
  renderDetail({ jobId: "job-1" });
  expect(await screen.findByText("接口暂时不可用")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "重新加载" }));
  expect(await screen.findByText("真实状态")).toBeInTheDocument();
  expect(api.getInvestigation).toHaveBeenCalledTimes(2);
  expect(api.getAssistantTurn).not.toHaveBeenCalled();
});

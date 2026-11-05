import { it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { setNativeCredentials, clearCredentials } from "../src/api/client";
import type { IncidentV2 } from "../src/api/generated/models";
import { InvestigationPanel } from "../src/features/investigation/InvestigationPanel";
const api = vi.hoisted(() => ({
  cancel: vi.fn(),
  create: vi.fn(),
  history: vi.fn(),
  job: vi.fn(),
  steps: vi.fn(),
}));
vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({ session: { subject: "user:reader" } }),
}));
vi.mock("../src/features/investigation/useInvestigationStream", () => ({
  useInvestigationStream: () => ({ status: "idle", reconnect: vi.fn() }),
}));
vi.mock("../src/api/generated/platform", () => ({
  createInvestigation: api.create,
  cancelInvestigation: api.cancel,
  getIncidentInvestigations: api.history,
  getInvestigation: api.job,
  getInvestigationSteps: api.steps,
}));
afterEach(() => clearCredentials());
it("retains the mapped investigation result and evidence while refusing cancellation of the old owner task", async () => {
  setNativeCredentials("tenant", "csrf");
  const reply = (data: unknown) =>
    Promise.resolve({ status: 200, data: { data, requestId: "test" } });
  api.history.mockImplementation(() => reply([]));
  api.steps.mockImplementation(() => reply([]));
  api.job.mockImplementation(() =>
    reply({
      jobId: "old-job",
      subject: "legacy-owner",
      state: "running",
      triggerRevision: 1,
      budget: {},
      budgetConsumed: {},
      budgetReserved: {},
      result: { summary: "保留的旧调查", evidenceRefs: ["old-evidence"] },
    }),
  );
  const onEvidence = vi.fn();
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={["/incident?jobId=old-job"]}>
        <InvestigationPanel
          incident={{ incidentId: "incident", revision: 1 } as IncidentV2}
          onEvidence={onEvidence}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(await screen.findByText("历史调查只读")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "取消调查" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "取消调查" }));
  expect(api.cancel).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: /查看结果证据/ }));
  expect(onEvidence).toHaveBeenCalledWith("old-evidence");
});

it("sends the focus and budget tightening with the manual trigger and projects the stored focus", async () => {
  setNativeCredentials("tenant", "csrf");
  const reply = (data: unknown) =>
    Promise.resolve({ status: 200, data: { data, requestId: "test" } });
  api.history.mockImplementation(() => reply([]));
  api.steps.mockImplementation(() => reply([]));
  api.job.mockImplementation(() =>
    reply({
      jobId: "focus-job",
      subject: "user:reader",
      state: "succeeded",
      triggerRevision: 1,
      budget: {},
      budgetConsumed: {},
      budgetReserved: {},
      focus: "内存通道与节点运行环境",
      result: { summary: "已完成", evidenceRefs: [] },
    }),
  );
  api.create.mockImplementation(() =>
    Promise.resolve({
      status: 202,
      data: { data: { jobId: "focus-job", state: "queued" }, requestId: "test" },
    }),
  );
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={["/incident"]}>
        <InvestigationPanel
          incident={{ incidentId: "incident", revision: 1 } as IncidentV2}
          onEvidence={vi.fn()}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  fireEvent.change(screen.getByLabelText("调查重点"), {
    target: { value: "优先核对内存通道" },
  });
  fireEvent.change(screen.getByLabelText("收紧时长秒"), {
    target: { value: "120" },
  });
  fireEvent.click(screen.getByRole("button", { name: "开始智能调查" }));
  await waitFor(() =>
    expect(api.create).toHaveBeenCalledWith(
      "incident",
      {
        triggerRevision: 1,
        triggerKind: "manual",
        focus: "优先核对内存通道",
        tightening: { durationSeconds: 120 },
      },
      expect.anything(),
    ),
  );
  expect(
    await screen.findByText(/本次调查重点：内存通道与节点运行环境/),
  ).toBeInTheDocument();
});

it("rejects out-of-range tightening locally without calling the API", async () => {
  setNativeCredentials("tenant", "csrf");
  const reply = (data: unknown) =>
    Promise.resolve({ status: 200, data: { data, requestId: "test" } });
  api.history.mockImplementation(() => reply([]));
  api.steps.mockImplementation(() => reply([]));
  api.job.mockImplementation(() => reply({}));
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={["/incident"]}>
        <InvestigationPanel
          incident={{ incidentId: "incident", revision: 1 } as IncidentV2}
          onEvidence={vi.fn()}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  fireEvent.change(screen.getByLabelText("收紧工具调用次数"), {
    target: { value: "999" },
  });
  fireEvent.click(screen.getByRole("button", { name: "开始智能调查" }));
  await waitFor(() =>
    expect(screen.getByText("工具调用收紧必须是 1–40 的整数")).toBeInTheDocument(),
  );
  expect(api.create).not.toHaveBeenCalled();
});

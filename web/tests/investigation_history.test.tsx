import { it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { setNativeCredentials, clearCredentials } from "../src/api/client";
import type { IncidentV2 } from "../src/api/generated/models";
import { InvestigationPanel } from "../src/features/investigation/InvestigationPanel";
const api = vi.hoisted(() => ({
  cancel: vi.fn(),
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
  createInvestigation: vi.fn(),
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

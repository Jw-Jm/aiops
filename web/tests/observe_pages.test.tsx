// UI mechanism fixture; no database, browser visual or live authorization claim.
import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { NetworkPage } from "../src/pages/NetworkPage";
import { HardwarePage } from "../src/pages/HardwarePage";
import { getResources, queryEvidence } from "../src/api/generated/platform";
import { setNativeCredentials } from "../src/api/client";

vi.mock("../src/app/ClusterContext", () => ({
  useOperationalCluster: () => ({ clusterUid: "fixture-cluster" }),
}));
vi.mock("../src/api/generated/platform", async (original) => ({
  ...(await original<object>()),
  getResources: vi.fn(),
  queryEvidence: vi.fn(),
}));

const pod = {
  canonicalId: "k8s+v1://tenant/fixture-cluster/core/Pod/default/web-0",
  kind: "Pod",
  name: "web-0",
  namespace: "default",
};
const hardware = {
  canonicalId: "hardware+v1://tenant/fixture-cluster/redfish/System/node-9",
  kind: "System",
  name: "node-9",
  namespace: null,
};
const evidence = {
  schemaVersion: "evidence-result/v2",
  evidence: [
    {
      evidenceId: "019a0000-0000-7000-8000-0000000000e1",
      sourceSystem: "deepflow",
      observedFrom: "2026-11-04T10:00:00Z",
      observedTo: "2026-11-04T11:00:00Z",
      replayState: "source_available",
      factSlice: { retransmissions: 3 },
    },
  ],
  partial: false,
  freshness: "fresh",
  degradedSources: [],
  warnings: [],
  queryHash: "fixture",
};

beforeEach(() => {
  vi.clearAllMocks();
  setNativeCredentials("fixture-tenant", "fixture-csrf-key-0001");
  vi.mocked(queryEvidence).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: { data: evidence, requestId: "unit" },
  } as never);
});

function renderPage(page: React.ReactElement) {
  // The observe pages carry the entry into the assistant, so they render a
  // link that needs the router context.
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>{page}</MemoryRouter>
    </QueryClientProvider>,
  );
}

it("queries network evidence for the selected resource", async () => {
  vi.mocked(getResources).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: { data: [pod], meta: {}, requestId: "unit" },
  } as never);
  renderPage(<NetworkPage />);
  fireEvent.mouseDown(await screen.findByLabelText("目标资源"));
  fireEvent.click(await screen.findByText("Pod/web-0（default）"));
  fireEvent.click(screen.getByRole("button", { name: "查询最近一小时" }));
  await waitFor(() =>
    expect(queryEvidence).toHaveBeenCalledWith(
      expect.objectContaining({
        resourceCanonicalId: pod.canonicalId,
        type: "deepflow",
        queryTemplate: "GetResourceNetworkHealth",
      }),
      expect.anything(),
    ),
  );
  expect(await screen.findByText("deepflow")).toBeInTheDocument();
  const sent = vi.mocked(queryEvidence).mock.calls[0][0];
  const href = screen
    .getByRole("link", { name: "按此范围询问 AI" })
    .getAttribute("href")!;
  const link = new URL(href, "https://fixture.local");
  expect(link.searchParams.get("canonicalId")).toBe(sent.resourceCanonicalId);
  expect(link.searchParams.get("from")).toBe(sent.timeRange.from);
  expect(link.searchParams.get("to")).toBe(sent.timeRange.to);
  expect(Date.parse(sent.timeRange.to) - Date.parse(sent.timeRange.from)).toBe(
    3600000,
  );
});

it("queries hardware health for the selected hardware resource", async () => {
  vi.mocked(getResources).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: { data: [hardware], meta: {}, requestId: "unit" },
  } as never);
  renderPage(<HardwarePage />);
  fireEvent.mouseDown(await screen.findByLabelText("硬件资源"));
  fireEvent.click(await screen.findByText("System/node-9"));
  fireEvent.click(screen.getByRole("button", { name: "查询当前健康" }));
  await waitFor(() =>
    expect(queryEvidence).toHaveBeenCalledWith(
      expect.objectContaining({
        resourceCanonicalId: hardware.canonicalId,
        type: "hardware",
        queryTemplate: "hardware-health/v1",
      }),
      expect.anything(),
    ),
  );
  expect(getResources).toHaveBeenCalledWith(
    expect.objectContaining({ domain: "hardware" }),
    expect.anything(),
  );
});

import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MetricsPage } from "../src/pages/MetricsPage";
import { metrics } from "../src/api/metrics";
import { observation } from "../src/api/observation";
vi.mock("../src/app/ClusterContext", () => ({
  useOperationalCluster: () => ({ clusterUid: "cluster", invalid: false }),
}));
vi.mock("../src/api/metrics", () => ({ metrics: vi.fn() }));
vi.mock("../src/api/observation", () => ({ observation: vi.fn() }));
const caps = () => ({
  sourceCapabilities: ["metrics.pod-cpu", "metrics.pod-memory"].map(
    (datasetId) => ({
      sourceId: "source",
      datasetId,
      operation: "trend",
      collectorVersion: "native/v1",
      proofDigest: "proof",
      coverageFrom: new Date(Date.now() - 3600000).toISOString(),
      coverageTo: new Date(Date.now() - 1000).toISOString(),
      verifiedUntil: new Date(Date.now() + 30000).toISOString(),
      maxWindowSeconds: 1800,
      status: "continuous_verified",
    }),
  ),
});
const result = {
  datasetId: "metrics.pod-cpu",
  status: "partial",
  exact: false,
  unit: "cpu_cores",
  rows: [{ dimension: "2026-11-03T00:00:00Z", value: 0.1 }],
  warnings: ["collection_coverage_unknown"],
  timeRange: {
    from: "2026-11-03T00:00:00Z",
    to: "2026-11-03T00:01:00Z",
    timezone: "UTC",
  },
  sourceManifest: {
    sourceId: "source",
    revision: 1,
    qualificationDigest: "proof",
    queryHash: "hash",
  },
  snapshotReadAt: "2026-11-03T00:02:00Z",
};
function view() {
  return render(
    <MemoryRouter initialEntries={["/observe/metrics?canonicalId=pod-one"]}>
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <MetricsPage />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}
beforeEach(() => {
  vi.mocked(observation).mockResolvedValue({
    resources: [
      {
        canonicalId: "pod-one",
        uid: "uid",
        displayName: "web-one",
        clusterUid: "cluster",
        namespaceScope: {
          kind: "namespaced",
          namespace: "apps",
          namespaceUid: "ns",
        },
      },
    ],
    sources: [],
    budget: {
      maxRows: 20,
      maxBytes: 65536,
      maxDurationSeconds: 8,
      maxSourceCalls: 2,
    },
  });
  vi.mocked(metrics).mockImplementation(
    async (path) => (path === "capabilities" ? caps() : result) as never,
  );
});
afterEach(() => vi.clearAllMocks());
it("sends a bounded fixed template and renders partial real points", async () => {
  view();
  const button = await screen.findByRole("button", { name: "查询指标" });
  await waitFor(() => expect(button).toBeEnabled());
  fireEvent.click(button);
  await screen.findByRole("img", { name: "指标时间趋势" });
  expect(screen.getByRole("status")).toHaveTextContent("采集覆盖未知");
  const call = vi.mocked(metrics).mock.calls.find((c) => c[0] === "query")!;
  expect(call[1]).toBe("cluster");
  expect(call[4]).toMatchObject({
    canonicalId: "pod-one",
    intent: {
      datasetId: "metrics.pod-cpu",
      operation: "trend",
      stepSeconds: 60,
      limit: 20,
      dimensions: ["time"],
    },
  });
});
it("keeps an absent source disabled without inventing zero", async () => {
  vi.mocked(metrics).mockResolvedValue({ sourceCapabilities: [] });
  view();
  await screen.findByText("当前没有合格的指标来源");
  expect(screen.getByRole("button", { name: "查询指标" })).toBeDisabled();
  expect(
    screen.queryByRole("img", { name: "指标时间趋势" }),
  ).not.toBeInTheDocument();
});
it("does not draw zero when the fixed window has no observations", async () => {
  vi.mocked(metrics).mockImplementation(
    async (path) =>
      (path === "capabilities"
        ? caps()
        : { ...result, status: "no_data", rows: [] }) as never,
  );
  view();
  const button = screen.getByRole("button", { name: "查询指标" });
  await waitFor(() => expect(button).toBeEnabled());
  fireEvent.click(button);
  await screen.findByText("所选范围没有指标样本，不能推断为零。");
  expect(
    screen.queryByRole("img", { name: "指标时间趋势" }),
  ).not.toBeInTheDocument();
});
it("aborts and discards the old result when metric selection changes", async () => {
  let finish: (v: unknown) => void = () => {};
  vi.mocked(metrics).mockImplementation(async (path) =>
    path === "capabilities"
      ? (caps() as never)
      : ((await new Promise<unknown>((resolve) => {
          finish = resolve;
        })) as never),
  );
  view();
  const button = screen.getByRole("button", { name: "查询指标" });
  await waitFor(() => expect(button).toBeEnabled());
  fireEvent.click(button);
  await waitFor(() =>
    expect(vi.mocked(metrics).mock.calls.some((c) => c[0] === "query")).toBe(
      true,
    ),
  );
  const call = vi.mocked(metrics).mock.calls.find((c) => c[0] === "query")!;
  fireEvent.mouseDown(screen.getByRole("combobox", { name: "指标类型" }));
  fireEvent.click(await screen.findByText("内存工作集"));
  expect(call[3]?.aborted).toBe(true);
  await act(async () => finish(result));
  expect(
    screen.queryByRole("img", { name: "指标时间趋势" }),
  ).not.toBeInTheDocument();
});
it("leaves a break in the line when a timestamp is missing", async () => {
  vi.mocked(metrics).mockImplementation(
    async (path) =>
      (path === "capabilities"
        ? caps()
        : {
            ...result,
            stepSeconds: 60,
            timeRange: { ...result.timeRange, to: "2026-11-03T00:02:00Z" },
            rows: [
              { dimension: "2026-11-03T00:00:00Z", value: 1 },
              { dimension: "2026-11-03T00:02:00Z", value: 2 },
            ],
          }) as never,
  );
  view();
  const button = screen.getByRole("button", { name: "查询指标" });
  await waitFor(() => expect(button).toBeEnabled());
  fireEvent.click(button);
  const chart = await screen.findByRole("img", { name: "指标时间趋势" });
  const line = chart.querySelector("path")?.getAttribute("d");
  expect(line?.match(/M/g)).toHaveLength(2);
  expect(line).not.toContain("L");
});

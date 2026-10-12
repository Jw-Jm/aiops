// Explicit list API fixture; real authorization and list rendering are validated separately.
import { it, expect, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { FindingsPage } from "../src/pages/FindingsPage";
import { QueryState } from "../src/components/QueryState";
import { setCredentials } from "../src/api/client";
const api = vi.hoisted(() => ({ findings: vi.fn(), incidents: vi.fn() }));
vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: { authorizedClusters: [{ clusterUid: "fixture-cluster" }] },
  }),
}));
vi.mock("../src/api/generated/platform", async (original) => ({
  ...(await original<object>()),
  getFindings: api.findings,
  getIncidents: api.incidents,
}));
for (const incidents of [false, true])
  it(`restores ${incidents ? "Incident opened" : "Finding observed"} time filters from URL and preserves them on cursor pagination`, async () => {
    const method = incidents ? api.incidents : api.findings;
    method.mockReset();
    method.mockResolvedValue({
      status: 200,
      headers: new Headers(),
      data: {
        data: [
          {
            findingId: "fixture-f",
            incidentId: "fixture-i",
            resourceCanonicalId: "",
            severity: "unknown",
            state: "open",
            observedAt: "2026-10-12T00:00:00Z",
          },
        ],
        meta: { nextCursor: "fixture-next" },
      },
    });
    setCredentials("fixture", "fixture-tenant");
    const prefix = incidents ? "opened" : "observed";
    const from = "2026-10-12T00:00:00Z";
    const to = "2026-10-12T01:00:00Z";
    render(
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <MemoryRouter
          initialEntries={[
            "/?" +
              new URLSearchParams({
                [prefix + "From"]: from,
                [prefix + "To"]: to,
              }),
          ]}
        >
          <FindingsPage incidents={incidents} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await waitFor(() =>
      expect(method).toHaveBeenCalledWith(
        expect.objectContaining({
          [prefix + "From"]: from,
          [prefix + "To"]: to,
          limit: 50,
        }),
        expect.anything(),
      ),
    );
    expect(
      screen.getByLabelText(
        (incidents ? "故障开始时间" : "告警观测时间") + " · 起（UTC+08:00）",
      ),
    ).toHaveValue("2026-10-12T08:00");
    fireEvent.click(await screen.findByRole("button", { name: "下一页" }));
    await waitFor(() =>
      expect(method).toHaveBeenCalledWith(
        expect.objectContaining({
          [prefix + "From"]: from,
          [prefix + "To"]: to,
          cursor: "fixture-next",
        }),
        expect.anything(),
      ),
    );
  });
it("distinguishes absent cluster scope from empty authorized data", () => {
  render(<QueryState loading={false} error={null} noScope empty />);
  expect(screen.getByText("没有可用的集群范围")).toBeVisible();
  expect(
    screen.queryByText("当前筛选范围没有正式数据"),
  ).not.toBeInTheDocument();
});

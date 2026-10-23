import { it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OverviewPage } from "../src/pages/OverviewPage";
import { ResourcesPage } from "../src/pages/ResourcesPage";
import { resource } from "./fixtures";
import { setCredentials } from "../src/api/client";
vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: { authorizedClusters: [{ clusterUid: "test-cluster" }] },
  }),
}));
vi.mock("../src/api/generated/platform", () => ({
  getOverview: vi.fn(async () => ({
    status: 200,
    headers: new Headers(),
    data: {
      requestId: "test",
      data: {
        counts: { resource: null, finding: 0 },
        resourceError: { code: "GRAPH_NOT_READY" },
        resourceObservation: {
          partial: true,
          degradedSources: ["source-unavailable"],
        },
      },
    },
  })),
  getCapabilities: vi.fn(async () => ({
    status: 200,
    headers: new Headers(),
    data: {
      requestId: "test",
      data: {
        investigation: { status: "unverified" },
        commandExecution: { status: "unverified" },
      },
    },
  })),
  getResources: vi.fn(async () => ({
    status: 200,
    headers: new Headers(),
    data: {
      requestId: "test",
      data: [resource],
      meta: {
        partial: true,
        freshness: "stale",
        degradedSources: ["test-unavailable"],
        nextCursor: "opaque cursor+/",
      },
    },
  })),
}));
it("renders partial source, long real fields and cursor pagination", async () => {
  setCredentials("test-token");
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <ResourcesPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(
    await screen.findByText("此查询返回部分或来源降级结果"),
  ).toBeInTheDocument();
  expect(await screen.findByText(resource.name)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "下一页" })).not.toBeDisabled();
  expect(screen.getByText("观测已过期")).toBeInTheDocument();
});

it("shows missing resource counts as unknown and exposes degraded observation on the overview", async () => {
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <OverviewPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(
    await screen.findByText("资源观测暂不可用，资源和健康统计未知"),
  ).toBeVisible();
  expect(screen.getByText("此查询返回部分或来源降级结果")).toBeVisible();
  expect(screen.getAllByText("未知").length).toBeGreaterThan(0);
  expect(screen.getByText("0")).toBeVisible();
  expect(screen.queryByText("空值（null）")).not.toBeInTheDocument();
});

it("shows scoped active counts, domain navigation and a bounded priority incident without inventing impact", async () => {
  const { getOverview } = await import("../src/api/generated/platform");
  vi.mocked(getOverview).mockResolvedValueOnce({
    status: 200,
    headers: new Headers(),
    data: {
      requestId: "test",
      data: {
        counts: {
          resource: 7,
          activeFinding: 3,
          openIncident: 2,
          criticalIncident: 1,
          runningInvestigation: 1,
          server: null,
        },
        distribution: {
          domains: {
            kubernetes: { total: 7, byHealth: { normal: 6, abnormal: 1 } },
          },
        },
        priorityIncidents: [
          {
            incidentId: "incident-one",
            title: "节点未就绪",
            resourceCanonicalId: "k8s+v1://tenant/cluster/core/Node/uid",
            severity: "critical",
            state: "open",
            startedAt: null,
            durationSeconds: null,
            rcaStatus: "unverified",
            impact: null,
          },
        ],
      },
    },
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/overview?clusterUid=test-cluster"]}>
        <OverviewPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(
    await screen.findByRole("link", { name: "节点未就绪" }),
  ).toHaveAttribute("href", "/events/incidents/incident-one");
  expect(screen.getByText("活动告警").closest(".stat")).toHaveTextContent("3");
  expect(
    screen.getByText("未确认故障（open）").closest(".stat"),
  ).toHaveTextContent("2");
  expect(screen.getByText("我的运行中调查").closest(".stat")).toHaveTextContent(
    "1",
  );
  expect(screen.getByRole("link", { name: "Kubernetes资源" })).toHaveAttribute(
    "href",
    "/resources?clusterUid=test-cluster&domain=kubernetes",
  );
  const health = screen
    .getAllByRole("link")
    .find((link) =>
      link.getAttribute("href")?.includes("domain=kubernetes&health=abnormal"),
    );
  expect(health).toHaveTextContent("异常");
  expect(screen.getByText("未知（时间未验证）")).toBeVisible();
  expect(screen.getByText("尚无可授权读取的影响证据")).toBeVisible();
  client.clear();
});

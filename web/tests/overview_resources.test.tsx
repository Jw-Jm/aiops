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
  expect(screen.getByText("未知")).toBeVisible();
  expect(screen.getByText("0")).toBeVisible();
  expect(screen.queryByText("空值（null）")).not.toBeInTheDocument();
});

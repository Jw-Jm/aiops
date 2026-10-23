import { expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AppShell } from "../../src/layout/AppShell";
import { setCredentials } from "../../src/api/client";
const auth = vi.hoisted(() => ({ roles: ["operator"] as string[] }));

vi.mock("../../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: {
      tenantId: "tenant",
      subject: "operator-a",
      roles: auth.roles,
      authorizedClusters: [
        { clusterId: "id-a", clusterUid: "uid-a", displayName: "集群 A" },
        { clusterId: "id-b", clusterUid: "uid-b", displayName: "集群 B" },
      ],
      clusterScopes: ["uid-a", "uid-b"],
      namespaceScopes: [],
    },
    user: { profile: { preferred_username: "operator-a" } },
    loading: false,
  }),
}));
vi.mock("../../src/auth/oidc", () => ({
  getManager: async () => ({ signoutRedirect: async () => {} }),
}));
vi.mock("../../src/api/generated/platform", () => ({
  getClusters: vi.fn(async () => ({
    status: 200,
    headers: new Headers(),
    data: {
      data: [
        { clusterId: "id-c", clusterUid: "uid-c", displayName: "注册 C" },
      ],
    },
  })),
}));

function renderShell(initial: string, content: "operator" | "admin") {
  const router = createMemoryRouter(
    [
      {
        path: "/",
        element: <AppShell />,
        children: [
          { path: "overview", element: <div>overview content</div> },
          { path: "events/alerts", element: <div>alerts content</div> },
          { path: "access/sources", element: <div>sources content</div> },
        ],
      },
    ],
    { initialEntries: [initial] },
  );
  if (content === "admin") setCredentials("admin-token", "tenant");
  const client = new QueryClient();
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { router, ...view };
}

it("keeps the selected cluster on real navigation links", () => {
  renderShell("/overview?clusterUid=uid-b", "operator");
  fireEvent.click(screen.getByText("事件响应"));
  expect(screen.getByRole("link", { name: /故障告警/ })).toHaveAttribute(
    "href",
    "/events/alerts?clusterUid=uid-b",
  );
});

it("does not expose operator menus to a pure administrator", async () => {
  auth.roles = ["platform_admin"];
  renderShell("/access/sources", "admin");
  expect(screen.queryByText("智能助手")).toBeNull();
  await waitFor(() =>
    expect(screen.getByText("注册 C")).toBeInTheDocument(),
  );
  await waitFor(() =>
    expect(screen.getByRole("link", { name: /数据来源/ })).toHaveAttribute(
      "href",
      "/access/sources?configClusterUid=uid-c",
    ),
  );
  expect(screen.getByLabelText("配置集群")).toBeEnabled();
});

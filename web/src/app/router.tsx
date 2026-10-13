import { createBrowserRouter, Navigate } from "react-router-dom";
import { Guard } from "../auth/guards";
import { Callback, SilentCallback } from "../auth/Callback";
import { AppShell } from "../layout/AppShell";
import { Result } from "antd";
export const router = createBrowserRouter([
  { path: "/auth/callback", element: <Callback /> },
  { path: "/auth/silent-callback", element: <SilentCallback /> },
  {
    element: <Guard />,
    children: [
      {
        element: <AppShell />,
        children: [
          { index: true, element: <Navigate to="/overview" replace /> },
          {
            path: "/assistant",
            lazy: async () => ({
              Component: (await import("../pages/AssistantPage")).AssistantPage,
            }),
          },
          {
            path: "/overview",
            lazy: async () => ({
              Component: (await import("../pages/OverviewPage")).OverviewPage,
            }),
          },
          {
            path: "/resources",
            lazy: async () => ({
              Component: (await import("../pages/ResourcesPage")).ResourcesPage,
            }),
          },
          {
            path: "/resources/detail",
            lazy: async () => ({
              Component: (await import("../pages/ResourceDetailPage"))
                .ResourceDetailPage,
            }),
          },
          {
            path: "/findings",
            lazy: async () => ({
              Component: (await import("../pages/FindingsPage")).FindingsPage,
            }),
          },
          {
            path: "/incidents",
            lazy: async () => {
              const { FindingsPage } = await import("../pages/FindingsPage");
              return { Component: () => <FindingsPage incidents /> };
            },
          },
          {
            path: "/incidents/:incidentId",
            lazy: async () => ({
              Component: (await import("../pages/IncidentWorkspace"))
                .IncidentWorkspace,
            }),
          },
          {
            path: "/capabilities",
            lazy: async () => ({
              Component: (await import("../pages/CapabilitiesPage"))
                .CapabilitiesPage,
            }),
          },
          {
            element: <Guard role="platform_admin" />,
            children: [
              ...(
                [
                  "sources",
                  "policies",
                  "execution-profiles",
                  "deployment",
                ] as const
              ).map((section) => ({
                path: "/admin/" + section,
                lazy: async () => {
                  const { AdminPage } =
                    await import("../pages/admin/AdminPage");
                  return { Component: () => <AdminPage section={section} /> };
                },
              })),
              {
                path: "/admin/audit",
                lazy: async () => ({
                  Component: (await import("../pages/admin/AuditPage"))
                    .AuditPage,
                }),
              },
            ],
          },
          { path: "*", element: <Result status="404" title="页面不存在" /> },
        ],
      },
    ],
  },
]);

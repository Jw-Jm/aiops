import {
  createBrowserRouter,
  Navigate,
  useLocation,
  useParams,
} from "react-router-dom";
import { Result } from "antd";
import { Guard } from "../auth/guards";
import { useAuth } from "../auth/AuthProvider";
import { Callback, SilentCallback } from "../auth/Callback";
import { AppShell } from "../layout/AppShell";
import { UnavailablePage } from "../pages/UnavailablePage";

export function legacyRedirectTarget(
  pathname: string,
  search: string,
  incidentId?: string,
) {
  const map: Record<string, string> = {
    "/findings": "/events/alerts",
    "/incidents": "/events/incidents",
    "/admin/sources": "/access/sources",
    "/admin/policies": "/settings/policies",
    "/admin/execution-profiles": "/settings/execution",
    "/admin/deployment": "/settings/deployment",
    "/admin/audit": "/settings/audit",
  };
  if (pathname.startsWith("/incidents/") && incidentId) {
    return {
      pathname: `/events/incidents/${encodeURIComponent(incidentId)}`,
      search,
    };
  }
  return { pathname: map[pathname] ?? pathname, search };
}
function Legacy({ to }: { to: string }) {
  const location = useLocation();
  return <Navigate to={{ pathname: to, search: location.search }} replace />;
}
function LegacyIncident() {
  const { incidentId } = useParams();
  const location = useLocation();
  return (
    <Navigate
      to={legacyRedirectTarget(location.pathname, location.search, incidentId)}
      replace
    />
  );
}
function Home() {
  const { session } = useAuth();
  return (
    <Navigate
      to={session?.roles.includes("operator") ? "/overview" : "/access/sources"}
      replace
    />
  );
}
const unavailable = (title: string, description: string, message: string) => (
  <UnavailablePage title={title} description={description} message={message} />
);

export const router = createBrowserRouter([
  { path: "/auth/callback", element: <Callback /> },
  { path: "/auth/silent-callback", element: <SilentCallback /> },
  {
    path: "/login",
    lazy: async () => ({ Component: (await import("../pages/Login")).Login }),
  },
  {
    path: "/auth/first-password",
    lazy: async () => ({
      Component: (await import("../pages/FirstPassword")).FirstPassword,
    }),
  },
  {
    element: <Guard />,
    children: [
      {
        element: <AppShell />,
        children: [
          { index: true, element: <Home /> },
          {
            path: "/account/security",
            lazy: async () => ({
              Component: (await import("../pages/AccountSecurity"))
                .AccountSecurity,
            }),
          },
          {
            element: <Guard role="operator" />,
            children: [
              {
                path: "/overview",
                lazy: async () => ({
                  Component: (await import("../pages/OverviewPage"))
                    .OverviewPage,
                }),
              },
              {
                path: "/assistant",
                lazy: async () => ({
                  Component: (await import("../pages/AssistantPage"))
                    .AssistantPage,
                }),
              },
              {
                path: "/resources",
                lazy: async () => ({
                  Component: (await import("../pages/ResourcesPage"))
                    .ResourcesPage,
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
                path: "/resources/topology",
                lazy: async () => ({
                  Component: (await import("../pages/TopologyPage"))
                    .TopologyPage,
                }),
              },
              {
                path: "/events/alerts",
                lazy: async () => ({
                  Component: (await import("../pages/FindingsPage"))
                    .FindingsPage,
                }),
              },
              {
                path: "/events/incidents",
                lazy: async () => {
                  const { FindingsPage } =
                    await import("../pages/FindingsPage");
                  return { Component: () => <FindingsPage incidents /> };
                },
              },
              {
                path: "/events/incidents/:incidentId",
                lazy: async () => ({
                  Component: (await import("../pages/IncidentWorkspace"))
                    .IncidentWorkspace,
                }),
              },
              {
                path: "/observe/logs",
                lazy: async () => ({
                  Component: (await import("../pages/LogsPage")).LogsPage,
                }),
              },
              {
                path: "/observe/metrics",
                lazy: async () => ({
                  Component: (await import("../pages/MetricsPage")).MetricsPage,
                }),
              },
              {
                path: "/observe/network",
                lazy: async () => ({
                  Component: (await import("../pages/NetworkPage")).NetworkPage,
                }),
              },
              {
                path: "/observe/hardware",
                lazy: async () => ({
                  Component: (await import("../pages/HardwarePage"))
                    .HardwarePage,
                }),
              },
              {
                path: "/jobs/commands",
                lazy: async () => ({
                  Component: (await import("../pages/CommandExecutionsPage"))
                    .CommandExecutionsPage,
                }),
              },
              ...["inspections"].map((key) => ({
                path: `/jobs/${key}`,
                element: unavailable(
                  "巡检任务",
                  "查看执行或巡检任务与回执。",
                  "当前版本暂不提供该任务入口。",
                ),
              })),
              {
                path: "/knowledge",
                element: <Navigate to="/knowledge/cases" replace />,
              },
              {
                path: "/knowledge/cases",
                element: unavailable(
                  "故障案例",
                  "检索已发布的故障案例。",
                  "当前版本暂不提供知识库。你可以先查看故障处理记录。",
                ),
              },
              {
                path: "/knowledge/runbooks",
                element: unavailable(
                  "操作手册",
                  "检索已发布的操作手册。",
                  "当前版本暂不提供操作手册。",
                ),
              },
              {
                path: "/capabilities",
                lazy: async () => ({
                  Component: (await import("../pages/CapabilitiesPage"))
                    .CapabilitiesPage,
                }),
              },
            ],
          },
          {
            element: <Guard role="platform_admin" />,
            children: [
              {
                path: "/access/clusters",
                lazy: async () => ({
                  Component: (await import("../pages/admin/ClustersPage"))
                    .ClustersPage,
                }),
              },
              {
                path: "/access/sources",
                lazy: async () => {
                  const { AdminPage } =
                    await import("../pages/admin/AdminPage");
                  return { Component: () => <AdminPage section="sources" /> };
                },
              },
              {
                path: "/access/models",
                element: unavailable(
                  "模型服务",
                  "配置模型连接与准入状态。",
                  "当前版本暂不提供模型服务配置。",
                ),
              },
              {
                path: "/access/readiness",
                lazy: async () => ({
                  Component: (await import("../pages/CapabilitiesPage"))
                    .CapabilitiesPage,
                }),
              },
              {
                path: "/settings/permissions",
                lazy: async () => ({
                  Component: (await import("../pages/admin/UsersPage"))
                    .UsersPage,
                }),
              },
              {
                path: "/settings/policies",
                lazy: async () => {
                  const { AdminPage } =
                    await import("../pages/admin/AdminPage");
                  return { Component: () => <AdminPage section="policies" /> };
                },
              },
              {
                path: "/settings/execution",
                lazy: async () => {
                  const { AdminPage } =
                    await import("../pages/admin/AdminPage");
                  return {
                    Component: () => <AdminPage section="execution-profiles" />,
                  };
                },
              },
              {
                path: "/settings/deployment",
                lazy: async () => {
                  const { AdminPage } =
                    await import("../pages/admin/AdminPage");
                  return {
                    Component: () => <AdminPage section="deployment" />,
                  };
                },
              },
              {
                path: "/settings/audit/assistant",
                lazy: async () => {
                  const { AssistantPage } =
                    await import("../pages/AssistantPage");
                  return {
                    Component: () => <AssistantPage initialDomain="audit" />,
                  };
                },
              },
              {
                path: "/settings/audit",
                lazy: async () => ({
                  Component: (await import("../pages/admin/AuditPage"))
                    .AuditPage,
                }),
              },
            ],
          },
          { path: "/findings", element: <Legacy to="/events/alerts" /> },
          { path: "/incidents", element: <Legacy to="/events/incidents" /> },
          { path: "/incidents/:incidentId", element: <LegacyIncident /> },
          { path: "/admin/sources", element: <Legacy to="/access/sources" /> },
          {
            path: "/admin/policies",
            element: <Legacy to="/settings/policies" />,
          },
          {
            path: "/admin/execution-profiles",
            element: <Legacy to="/settings/execution" />,
          },
          {
            path: "/admin/deployment",
            element: <Legacy to="/settings/deployment" />,
          },
          { path: "/admin/audit", element: <Legacy to="/settings/audit" /> },
          { path: "*", element: <Result status={404} title="页面不存在" /> },
        ],
      },
    ],
  },
]);

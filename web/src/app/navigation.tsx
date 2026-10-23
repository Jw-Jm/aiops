import {
  ApartmentOutlined,
  AlertOutlined,
  BookOutlined,
  CloudServerOutlined,
  DashboardOutlined,
  DatabaseOutlined,
  FileSearchOutlined,
  LineChartOutlined,
  NodeIndexOutlined,
  PlayCircleOutlined,
  RobotOutlined,
  SettingOutlined,
  TeamOutlined,
} from "@ant-design/icons";
import type { ReactNode } from "react";

export type NavChild = {
  path: string;
  name: string;
  available: boolean;
};
export type NavEntry = {
  name: string;
  icon: ReactNode;
  path?: string;
  available?: boolean;
  children?: NavChild[];
};
export type NavGroup = {
  label: string;
  role?: "operator" | "platform_admin";
  items: NavEntry[];
};

// Frozen source NAV (prototype.html around line 3684): 7 modules, with
// knowledge split into cases/runbooks. The manifest keeps /knowledge as the
// module route; the router maps it to the cases surface.
export const NAV: NavGroup[] = [
  {
    label: "工作空间",
    role: "operator",
    items: [
      { name: "运行总览", icon: <DashboardOutlined />, path: "/overview", available: true },
      { name: "智能助手", icon: <RobotOutlined />, path: "/assistant", available: true },
    ],
  },
  {
    label: "运行与处置",
    role: "operator",
    items: [
      {
        name: "资源管理",
        icon: <DatabaseOutlined />,
        children: [
          { path: "/resources", name: "资源目录", available: true },
          { path: "/resources/topology", name: "关系拓扑", available: false },
        ],
      },
      {
        name: "监控与观测",
        icon: <LineChartOutlined />,
        children: [
          { path: "/observe/metrics", name: "指标查询", available: false },
          { path: "/observe/logs", name: "日志查询", available: false },
          { path: "/observe/network", name: "网络观测", available: false },
          { path: "/observe/hardware", name: "硬件健康", available: false },
        ],
      },
      {
        name: "事件响应",
        icon: <AlertOutlined />,
        children: [
          { path: "/events/alerts", name: "故障告警", available: true },
          { path: "/events/incidents", name: "故障处理", available: true },
        ],
      },
      {
        name: "运维作业",
        icon: <PlayCircleOutlined />,
        children: [
          { path: "/jobs/commands", name: "命令执行", available: false },
          { path: "/jobs/inspections", name: "巡检任务", available: false },
        ],
      },
    ],
  },
  {
    label: "经验沉淀",
    role: "operator",
    items: [
      {
        name: "知识管理",
        icon: <BookOutlined />,
        children: [
          { path: "/knowledge/cases", name: "故障案例", available: false },
          { path: "/knowledge/runbooks", name: "操作手册", available: false },
        ],
      },
    ],
  },
  {
    label: "平台配置",
    role: "platform_admin",
    items: [
      {
        name: "接入管理",
        icon: <CloudServerOutlined />,
        children: [
          { path: "/access/clusters", name: "集群接入", available: false },
          { path: "/access/sources", name: "数据来源", available: true },
          { path: "/access/models", name: "模型服务", available: false },
          { path: "/access/readiness", name: "接入状态", available: true },
        ],
      },
      {
        name: "系统管理",
        icon: <SettingOutlined />,
        children: [
          { path: "/settings/permissions", name: "用户与权限", available: true },
          { path: "/settings/policies", name: "诊断策略", available: true },
          { path: "/settings/execution", name: "命令执行配置", available: true },
          { path: "/settings/deployment", name: "部署信息", available: true },
          { path: "/settings/audit", name: "操作审计", available: true },
        ],
      },
    ],
  },
];

export function scopedHref(path: string, uid: string) {
  return uid ? `${path}?clusterUid=${encodeURIComponent(uid)}` : path;
}

export const NAV_ITEMS = NAV.flatMap((group) =>
  group.items.flatMap((item) =>
    item.children
      ? item.children.map((child) => ({
          ...child,
          icon: item.icon,
          module: item.name,
        }))
      : [{ path: item.path!, name: item.name, icon: item.icon, available: !!item.available, module: item.name }],
  ),
);

export function navItem(pathname: string) {
  return NAV_ITEMS.find(
    (item) =>
      pathname === item.path ||
      (item.path !== "/resources" && pathname.startsWith(item.path + "/")) ||
      (item.path === "/resources" &&
        pathname.startsWith("/resources/") &&
        pathname !== "/resources/topology") ||
      (item.path === "/events/incidents" &&
        pathname.startsWith("/events/incidents/")),
  );
}

export function navGroup(pathname: string) {
  return NAV.find((group) =>
    group.items.some((item) =>
      item.children
        ? item.children.some((child) => child.path === navItem(pathname)?.path)
        : item.path === navItem(pathname)?.path,
    ),
  );
}

export function navModule(pathname: string) {
  const path = navItem(pathname)?.path;
  for (const group of NAV)
    for (const item of group.items)
      if (item.children?.some((child) => child.path === path)) return item.name;
  return navItem(pathname)?.name;
}

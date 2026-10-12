import { Layout, Menu, Typography, Button, Space, Tag, Drawer } from "antd";
import {
  DashboardOutlined,
  DatabaseOutlined,
  AlertOutlined,
  ApartmentOutlined,
  SettingOutlined,
  LogoutOutlined,
  MenuOutlined,
} from "@ant-design/icons";
import { Link, Outlet, useLocation } from "react-router-dom";
import { useState } from "react";
import { useAuth } from "../auth/AuthProvider";
import { getManager } from "../auth/oidc";
const nav = [
  { key: "/overview", icon: <DashboardOutlined />, label: "运行总览" },
  { key: "/resources", icon: <DatabaseOutlined />, label: "资源目录" },
  { key: "/findings", icon: <AlertOutlined />, label: "故障告警" },
  { key: "/incidents", icon: <ApartmentOutlined />, label: "故障处理" },
  { key: "/capabilities", icon: <SettingOutlined />, label: "能力与来源" },
];
export function AppShell() {
  const { session, user } = useAuth();
  const location = useLocation();
  const [open, setOpen] = useState(false);
  const items = [
    ...nav,
    ...(session?.roles.includes("platform_admin")
      ? [
          {
            key: "/admin/sources",
            icon: <SettingOutlined />,
            label: "平台管理",
          },
          {
            key: "/admin/audit",
            icon: <DatabaseOutlined />,
            label: "审计记录",
          },
        ]
      : []),
  ].map((i) => ({
    ...i,
    label: (
      <Link to={i.key} onClick={() => setOpen(false)}>
        {i.label}
      </Link>
    ),
  }));
  const menu = (
    <>
      <Link className="brand" to="/overview">
        <b>O</b>
        <span>
          OPS<small>智能运维工作台</small>
        </span>
      </Link>
      <Menu
        theme="dark"
        mode="inline"
        items={items}
        selectedKeys={[
          items.find((i) => location.pathname.startsWith(i.key))?.key || "",
        ]}
      />
      <div className="sidebar-note">
        证据先行 · 人工授权
        <br />
        时区 UTC+08:00
      </div>
    </>
  );
  return (
    <Layout className="app">
      <a className="skip-link" href="#main">
        跳到主要内容
      </a>
      <Layout.Sider width={220} className="desktop-nav">
        {menu}
      </Layout.Sider>
      <Drawer
        placement="left"
        open={open}
        onClose={() => setOpen(false)}
        className="mobile-nav"
        styles={{ body: { padding: 0, background: "#172a3a" } }}
      >
        {menu}
      </Drawer>
      <Layout>
        <Layout.Header className="topbar">
          <Space>
            <Button
              className="mobile-menu"
              icon={<MenuOutlined />}
              aria-label="打开导航"
              onClick={() => setOpen(true)}
            />
            <span className="environment-label">运维工作空间</span>
          </Space>
          <Space wrap>
            <Typography.Text
              ellipsis
              className="subject"
              title={session?.subject}
            >
              {user?.profile.preferred_username ??
                user?.profile.name ??
                "当前用户"}
            </Typography.Text>
            {session?.roles.map((r) => (
              <Tag key={r} title={r}>
                {r === "operator"
                  ? "运维操作员"
                  : r === "platform_admin"
                    ? "平台管理员"
                    : r}
              </Tag>
            ))}
            <Button
              icon={<LogoutOutlined />}
              onClick={() => void getManager().then((m) => m.signoutRedirect())}
            >
              退出
            </Button>
          </Space>
        </Layout.Header>
        <Layout.Content id="main" tabIndex={-1} className="main">
          <Outlet />
        </Layout.Content>
        <Layout.Footer className="footer">
          OPS · 正式数据工作台{" "}
          <span>确定性诊断 / 模型调查 / 人工操作独立记录</span>
        </Layout.Footer>
      </Layout>
    </Layout>
  );
}

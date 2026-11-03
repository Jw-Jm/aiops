import { Alert, Button, Result, Spin } from "antd";
import { Navigate, Outlet, useLocation } from "react-router-dom";
import { ApiError } from "../api/client";
import { useAuth } from "./AuthProvider";
import { login } from "./oidc";
import { useState } from "react";
import { MyAccount } from "./MyAccount";
import { PageHeader, Surface } from "../components/v12/primitives";

function NativeRoleDenied({ role }: { role: "operator" | "platform_admin" }) {
  const auth = useAuth();
  const [account, setAccount] = useState(false);
  const [error, setError] = useState<string>();
  return (
    <div className="v12-denied-page">
      <PageHeader
        title="访问范围受限"
        description="角色权限分别授予，平台管理员不自动获得运维数据权限。"
      />
      <Surface
        title={`需要${role === "operator" ? "运维操作员" : "平台管理员"}权限`}
      >
        <div className="v12-denied-body">
          <svg
            aria-hidden="true"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
          >
            <circle cx="10" cy="10" r="6" />
            <path d="m15 15 6 6" />
          </svg>
          <strong>当前身份没有此功能权限</strong>
          <p>请使用账户菜单退出后，以拥有对应授权的平台账号登录。</p>
          <Button type="primary" onClick={() => setAccount(true)}>
            查看当前身份
          </Button>
        </div>
      </Surface>
      {error ? <Alert type="error" title={error} /> : null}
      {account ? (
        <MyAccount
          onClose={() => setAccount(false)}
          onLogout={() => {
            setAccount(false);
            void auth
              .logout()
              .catch((cause) =>
                setError(
                  cause instanceof Error
                    ? cause.message
                    : "退出未得到服务器确认，请重试。",
                ),
              );
          }}
        />
      ) : null}
    </div>
  );
}
export function hasRole(
  roles: readonly string[],
  role: "operator" | "platform_admin",
) {
  return roles.includes(role);
}
export function Guard({ role }: { role?: "operator" | "platform_admin" }) {
  const auth = useAuth();
  const location = useLocation();
  if (auth.loading)
    return (
      <div className="login-page">
        <Spin size="large" tip="恢复身份会话…" />
      </div>
    );
  if (auth.configFailed)
    return (
      <Result
        status="500"
        title="认证服务配置不可用"
        subTitle={auth.error?.message || "无法加载认证运行配置，已停止登录。"}
      />
    );
  if (auth.nativeSession?.mode === "first_password_only")
    return <Navigate to="/auth/first-password" replace />;
  if (auth.loggedOut) {
    if (auth.authMode === "native")
      return (
        <Navigate
          to="/login"
          replace
          state={{ returnTo: location.pathname + location.search }}
        />
      );
    return (
      <div className="login-page">
        <div className="login-brand">
          OPS<span>智能运维工作台</span>
        </div>
        <Result
          title="让故障信息形成清晰的行动路径"
          subTitle="统一资源、证据、调查与人工处置。使用组织身份登录。"
          extra={
            <Button type="primary" size="large" onClick={() => void login()}>
              使用 OIDC 登录
            </Button>
          }
        />
      </div>
    );
  }
  if (auth.user && auth.error instanceof ApiError && auth.error.status === 403)
    return (
      <Result
        status="403"
        title="当前身份没有可用授权"
        subTitle="身份验证已完成，但运维权限尚未分配或已被撤销。请联系平台管理员核对授权。"
        extra={
          <>
            <Button onClick={() => void auth.refresh()}>重新检查授权</Button>
            <Button onClick={() => void auth.logout()}>退出</Button>
          </>
        }
      />
    );
  if (!auth.session) {
    if (auth.authMode === "native")
      return (
        <Navigate
          to="/login"
          replace
          state={{ returnTo: location.pathname + location.search }}
        />
      );
    return (
      <div className="login-page">
        <div className="login-brand">
          OPS<span>智能运维工作台</span>
        </div>
        <Result
          title="让故障信息形成清晰的行动路径"
          subTitle="统一资源、证据、调查与人工处置。使用组织身份登录。"
          extra={
            <Button type="primary" size="large" onClick={() => void login()}>
              使用 OIDC 登录
            </Button>
          }
        />
        {auth.error && (
          <Alert
            type="error"
            showIcon
            title="身份会话不可用"
            description={auth.error.message}
          />
        )}
      </div>
    );
  }
  if (auth.nativeSession?.mustReenrollMfa) {
    return location.pathname === "/account/security" ? (
      <Outlet />
    ) : (
      <Navigate to="/account/security" replace />
    );
  }
  if (auth.session.roles.length === 0 && auth.authMode !== "native")
    return (
      <Result
        status="403"
        title="未分配可用角色"
        subTitle="当前身份已登录，但运维权限尚未分配或已被撤销。请联系平台管理员核对授权。"
        extra={<Button onClick={() => void auth.logout()}>退出</Button>}
      />
    );
  if (role && !hasRole(auth.session.roles, role)) {
    if (auth.authMode === "native") return <NativeRoleDenied role={role} />;
    return (
      <Result
        status="403"
        title="没有此角色权限"
        subTitle="运维操作员与平台管理员分别授权；管理权限不会自动赋予执行权限。"
      />
    );
  }
  return <Outlet />;
}

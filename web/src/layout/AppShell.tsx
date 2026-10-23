import { Alert, Button, Select, Space, Tag, Typography } from "antd";
import { LogoutOutlined, SwapOutlined } from "@ant-design/icons";
import { Link, Outlet, useBlocker, useLocation, useNavigate } from "react-router-dom";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useAuth } from "../auth/AuthProvider";
import { ClusterScopeProvider, useClusterScope } from "../app/ClusterContext";
import { NAV, navGroup, navItem, navModule, scopedHref, type NavEntry } from "../app/navigation";
import { V12Drawer, ConfirmDialog } from "../components/v12/Layer";
import { Field } from "../components/v12/primitives";
import { checked, requestOptions } from "../api/client";
import { getClusters } from "../api/generated/platform";
import { list, record, text } from "../components/data";
import type { AuthorizedClusterV1 } from "../api/generated/models";

function scopedPath(
  path: string,
  scope: ReturnType<typeof useClusterScope>,
  configure: boolean,
) {
  const uid = configure ? scope.configClusterUid : scope.clusterUid;
  if (!uid) return path;
  return configure
    ? `${path}?configClusterUid=${encodeURIComponent(uid)}`
    : scopedHref(path, uid);
}

function NavList() {
  const { session } = useAuth();
  const location = useLocation();
  const scope = useClusterScope();
  const roles = session?.roles ?? [];
  const current = navItem(location.pathname);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  return (
    <nav className="v12-nav-scroll" aria-label="平台功能">
      {NAV.filter((group) => !group.role || roles.includes(group.role)).map((group) => (
        <div key={group.label}>
          <div className="v12-nav-label">{group.label}</div>
          {group.items.map((item: NavEntry) => {
            const active =
              item.path === current?.path ||
              item.children?.some((child) => child.path === current?.path);
            if (!item.children) {
              return (
                <Link
                  key={item.path}
                  to={scopedPath(item.path!, scope, false)}
                  className="v12-nav-link"
                  aria-current={active ? "page" : undefined}
                >
                  <span aria-hidden="true">{item.icon}</span>
                  <span>{item.name}</span>
                </Link>
              );
            }
            return (
              <details
                key={item.name}
                className="v12-nav-group"
                open={active || !!expanded[item.name]}
                onToggle={(event) =>
                  setExpanded((previous) => ({
                    ...previous,
                    [item.name]: (event.target as HTMLDetailsElement).open,
                  }))
                }
              >
                <summary className="v12-nav-summary">
                  <span aria-hidden="true">{item.icon}</span>
                  <span>{item.name}</span>
                  <span className="v12-nav-chevron" aria-hidden="true">›</span>
                </summary>
                {item.children.map((child) => {
                  const childActive = child.path === current?.path;
                  return (
                    <Link
                      key={child.path}
                      to={scopedPath(child.path, scope, group.role === "platform_admin")}
                      className="v12-nav-link v12-nav-child"
                      aria-current={childActive ? "page" : undefined}
                    >
                      <span className="v12-nav-dot" aria-hidden="true" />
                      <span>{child.name}</span>
                      {!child.available ? (
                        <Tag style={{ marginInlineStart: "auto", fontSize: 10 }}>
                          未接入
                        </Tag>
                      ) : null}
                    </Link>
                  );
                })}
              </details>
            );
          })}
        </div>
      ))}
    </nav>
  );
}

function ClusterControls({
  onConfirmPending,
  onCancelPending,
}: {
  onConfirmPending: () => void;
  onCancelPending: () => void;
}) {
  const scope = useClusterScope();
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState(scope.clusterUid);
  const location = useLocation();
  const configPage =
    location.pathname.startsWith("/access/") ||
    location.pathname.startsWith("/settings/");
  const current = scope.candidates.find(
    (cluster) => cluster.clusterUid === scope.clusterUid,
  );
  useEffect(() => setSelected(scope.clusterUid), [scope.clusterUid]);
  const apply = () => {
    const result = scope.requestCluster(selected);
    if (result !== "pending") setOpen(false);
  };
  return (
    <>
      <div className="v12-scope">
        <span>运行集群</span>
        <strong>{current?.displayName || "未选择"}</strong>
        <code title={scope.clusterUid || undefined}>
          {scope.clusterUid || "无授权集群"}
        </code>
        <Button
          size="small"
          type="text"
          icon={<SwapOutlined />}
          aria-label="切换运行集群"
          onClick={() => setOpen(true)}
        >
          切换
        </Button>
      </div>
      {configPage ? (
        <div className="v12-scope">
          <span>配置集群</span>
          <Select
            size="small"
            aria-label="配置集群"
            value={scope.configClusterUid || undefined}
            placeholder={
              scope.configLoading
                ? "读取注册集群"
                : scope.configInvalid
                  ? "当前配置集群未授权"
                  : "选择注册集群"
            }
            loading={scope.configLoading}
            disabled={!scope.configCandidates.length}
            options={scope.configCandidates.map((cluster) => ({
              value: cluster.clusterUid,
              label: cluster.displayName,
            }))}
            onChange={(value) => scope.requestConfigCluster(value)}
          />
          {scope.configError ? (
            <Tag color="warning">注册集群不可用</Tag>
          ) : null}
        </div>
      ) : null}
      <V12Drawer
        open={open}
        title="切换运行集群"
        onClose={() => setOpen(false)}
        footer={
          <div className="v12-layer-footer">
            <Button onClick={() => setOpen(false)}>取消</Button>
            <Button type="primary" onClick={apply} disabled={!selected}>
              应用范围
            </Button>
          </div>
        }
      >
        <div className="v12-layer-form">
          <Field
            label="运行集群"
            htmlFor="v12-operational-cluster"
            help="切换会清理旧范围的查询缓存与对象引用，并重新读取；未提交的草稿会先确认。不能以集群名称代替 UID，也不会回退到全部集群。"
          >
            <Select
              id="v12-operational-cluster"
              data-autofocus
              value={selected || undefined}
              placeholder="选择已授权集群"
              options={scope.candidates.map((cluster) => ({
                value: cluster.clusterUid,
                label: `${cluster.displayName} · ${cluster.clusterUid}`,
              }))}
              onChange={setSelected}
            />
          </Field>
        </div>
      </V12Drawer>
      <ConfirmDialog
        open={scope.pendingSwitch !== null}
        title="切换运行集群？"
        description="当前页面有未提交草稿。切换会丢弃草稿、清空对象参数与旧查询结果；取消将保留原范围与草稿。"
        confirmLabel="丢弃草稿并切换"
        cancelLabel="保留当前范围"
        danger
        onConfirm={() => {
          onConfirmPending();
          setOpen(false);
        }}
        onCancel={onCancelPending}
      />
      <ConfirmDialog
        open={scope.pendingConfig !== null}
        title="切换配置集群？"
        description="当前配置页有未提交内容。切换会清空选择、发布回执与再次验证状态，并按新集群重新读取；取消保留当前配置。"
        confirmLabel="丢弃并切换"
        cancelLabel="保留当前配置"
        danger
        onConfirm={scope.confirmConfigPending}
        onCancel={scope.cancelConfigPending}
      />
    </>
  );
}

function Shell() {
  const { session, user, loading, logout } = useAuth();
  const location = useLocation();
  const navigate = useNavigate();
  const scope = useClusterScope();
  const [signingOut, setSigningOut] = useState(false);
  const [signOutError, setSignOutError] = useState<Error | null>(null);
  const signingOutRef = useRef(false);
  // Logout revokes the server session and deletes the cookie before the shell
  // leaves the route. A second click or a late failure never resurrects the old
  // identity, and the error is surfaced instead of ending as a silent no-op.
  const signOut = async () => {
    if (signingOutRef.current) return;
    signingOutRef.current = true;
    setSigningOut(true);
    try {
      const outcome = await logout();
      // A failed server revocation is not a successful sign-out. The local
      // identity is already gone, so the truthful report belongs to the login
      // route, which keeps the unconfirmed notice and the retry action.
      if (outcome.status !== "confirmed") {
        navigate("/login", { replace: true, state: { revocation: outcome.status } });
      }
    } catch (cause) {
      // logout() only throws for an unexpected client-side failure. Report it
      // instead of ending as a silent no-op.
      setSignOutError(cause instanceof Error ? cause : new Error("退出失败"));
    } finally {
      signingOutRef.current = false;
      setSigningOut(false);
    }
  };
  const requestNavigationRef = useRef(scope.requestUrlNavigation);
  requestNavigationRef.current = scope.requestUrlNavigation;
  const allowNextNavigationRef = useRef(false);
  const blocker = useBlocker(({ nextLocation }) => {
    if (allowNextNavigationRef.current) return false;
    return requestNavigationRef.current({
      pathname: nextLocation.pathname,
      search: nextLocation.search,
      hash: nextLocation.hash,
    });
  });
  useEffect(() => {
    allowNextNavigationRef.current = false;
  }, [location.key]);
  useEffect(() => {
    if (!scope.pendingSwitch && blocker.state === "blocked") blocker.reset();
  }, [blocker, scope.pendingSwitch]);
  const cancelPendingNavigation = () => {
    scope.cancelPending();
    if (blocker.state === "blocked") blocker.reset();
  };
  const confirmPendingNavigation = () => {
    if (!scope.pendingTarget) return;
    allowNextNavigationRef.current = true;
    if (blocker.state === "blocked") blocker.proceed();
    else navigate(scope.pendingTarget);
  };
  const group = navGroup(location.pathname);
  const item = navItem(location.pathname);
  const navScroll = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    const node = document.querySelector<HTMLElement>(".v12-nav-scroll");
    if (node) node.scrollTop = Number(sessionStorage.getItem("v12:nav-scroll") || 0);
    navScroll.current = node;
    return () => {
      if (node) sessionStorage.setItem("v12:nav-scroll", String(node.scrollTop));
    };
  }, []);
  useEffect(() => {
    if (navScroll.current) {
      sessionStorage.setItem("v12:nav-scroll", String(navScroll.current.scrollTop));
    }
  }, [location.pathname]);
  return (
    <div className="app v12-shell">
      <a className="skip-link" href="#main">
        跳到主要内容
      </a>
      <aside className="v12-sider desktop-nav" aria-label="主导航">
        <Link className="v12-brand" to={session?.roles.includes("operator") ? "/overview" : "/access/sources"}>
          <span className="v12-brand-mark">O</span>
          <span className="v12-brand-text">
            OPS
            <small>智能运维工作台</small>
          </span>
        </Link>
        <NavList />
        <div className="v12-nav-note">
          证据先行 · 人工授权
          <br />
          Asia/Shanghai · UTC+08:00
        </div>
      </aside>
      <div className="v12-main-shell" style={{ minWidth: 0 }}>
        <header className="v12-header topbar">
          <Space wrap>
            <div className="v12-breadcrumb">
              {group?.label ? <span>{group.label} / </span> : null}
              {navModule(location.pathname) ? (
                <span>{navModule(location.pathname)} / </span>
              ) : null}
              <strong>{item?.name || "未匹配页面"}</strong>
            </div>
            <ClusterControls
              onConfirmPending={confirmPendingNavigation}
              onCancelPending={cancelPendingNavigation}
            />
          </Space>
          <div className="v12-header-user">
            <Typography.Text ellipsis className="subject" title={session?.subject}>
              {user?.profile.preferred_username ||
                user?.profile.name ||
                session?.subject ||
                "当前用户"}
            </Typography.Text>
            {session?.roles.map((role) => (
              <Tag key={role} title={role}>
                {role === "operator"
                  ? "运维操作员"
                  : role === "platform_admin"
                    ? "平台管理员"
                    : role}
              </Tag>
            ))}
            <Link to="/account/security" className="v12-account-link">
              账号安全
            </Link>
            <Button
              // The icon is decorative: automation and assistive technology
              // depend on a stable accessible name, not on the glyph.
              icon={<LogoutOutlined aria-hidden="true" />}
              aria-label={signingOut ? "正在退出" : "退出"}
              aria-busy={signingOut}
              loading={signingOut}
              onClick={() => void signOut()}
            >
              退出
            </Button>
          </div>
        </header>
        {signOutError ? (
          <Alert
            type="error"
            showIcon
            style={{ margin: "12px 30px 0" }}
            message="退出请求未完成"
            description={signOutError.message}
          />
        ) : null}
        {scope.invalid ? (
          <Alert
            type="error"
            showIcon
            style={{ margin: "12px 30px 0" }}
            message="集群不在当前授权范围"
            description="当前 URL 的 clusterUid 未授权；页面不会回退到全部集群或第一个集群。请从运行集群切换器选择已授权 UID。"
          />
        ) : null}
        {scope.configInvalid ? (
          <Alert
            type="error"
            showIcon
            style={{ margin: "12px 30px 0" }}
            message="配置集群不可用"
            description="当前 URL 的 configClusterUid 不在管理员注册列表或读取失败；配置页不会回退到运行集群。"
          />
        ) : null}
        {scope.loading ? (
          <Alert
            type="info"
            showIcon
            style={{ margin: "12px 30px 0" }}
            message="正在读取授权集群"
          />
        ) : null}
        <main id="main" tabIndex={-1} className="main v12-main">
          {scope.urlTransition || scope.urlUnauthorizedTransition ? (
            <div className="v12-empty" role="status">
              正在切换授权范围；不会用旧集群读取新对象。
            </div>
          ) : (
            <Outlet />
          )}
        </main>
        <footer className="v12-footer">
          <span>OPS · 正式数据工作台</span>
          <span>确定性诊断 / 模型调查 / 人工操作独立记录</span>
        </footer>
      </div>
    </div>
  );
}

export function AppShell() {
  const { session, loading } = useAuth();
  const admin = session?.roles.includes("platform_admin") ?? false;
  const config = useQuery({
    queryKey: ["config-clusters"],
    enabled: admin,
    queryFn: async ({ signal }) => {
      let cursor: string | undefined;
      const all: unknown[] = [];
      do {
        const response = await checked(
          getClusters({ limit: 100, cursor }, requestOptions(signal)),
        );
        const envelope = record(response);
        all.push(...list(envelope.data));
        const next = record(envelope.meta).nextCursor;
        cursor = typeof next === "string" && next ? next : undefined;
      } while (cursor && !signal.aborted);
      return { data: all };
    },
  });
  const configCandidates: AuthorizedClusterV1[] = list(
    record(config.data).data,
  ).map((row) => {
    const value = record(row);
    return {
      clusterId: text(value.clusterId),
      clusterUid: text(value.clusterUid),
      displayName: text(value.displayName) || text(value.clusterUid),
    };
  });
  return (
    <ClusterScopeProvider
      clusters={session?.authorizedClusters ?? []}
      configCandidates={configCandidates}
      configLoading={config.isPending && admin}
      configError={config.error instanceof Error ? config.error : null}
      loading={loading}
    >
      <Shell />
    </ClusterScopeProvider>
  );
}

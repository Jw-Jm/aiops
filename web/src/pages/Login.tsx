import { useEffect, useRef, useState } from "react";
import { Alert, Button, Form, Input, Result, Space, Spin, Typography } from "antd";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { ApiError } from "../api/client";
import { useAuth } from "../auth/AuthProvider";

function loginMessage(error: unknown) {
  if (!(error instanceof ApiError)) return "登录服务暂时不可用，请稍后重试。";
  if (error.status === 429) return "尝试次数过多，登录暂时不可用。请稍后再试。";
  if (error.status === 401) return "登录名或密码不正确。";
  if (error.status === 403)
    return "浏览器请求未通过同源检查。请从本站登录页重新提交。";
  return error.message;
}

export function Login() {
  const auth = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [retrying, setRetrying] = useState(false);
  const [revocationMessage, setRevocationMessage] = useState<string | null>(null);
  const inFlight = useRef<AbortController | null>(null);
  // The retry only targets the session recorded at failure time. It never
  // touches the identity currently in use, so a late retry cannot sign a new
  // account out or resurrect the old one.
  const retryRevocation = async () => {
    setRetrying(true);
    try {
      const outcome = await auth.retryRevocation();
      if (outcome.status === "superseded") {
        setRevocationMessage(
          "当前浏览器已切换为另一个账号的会话，旧会话不会在此处被撤销；请联系管理员撤销它。",
        );
      } else if (outcome.status === "unconfirmed") {
        setRevocationMessage("仍未得到服务器确认，请检查网络后重试。");
      } else {
        setRevocationMessage(null);
      }
    } finally {
      setRetrying(false);
    }
  };
  // Navigating away or unmounting cancels the pending login instead of letting a
  // late success replace the identity of whatever screen the user moved to.
  useEffect(
    () => () => {
      inFlight.current?.abort();
      inFlight.current = null;
    },
    [],
  );
  const returnTo =
    typeof location.state === "object" &&
    location.state !== null &&
    "returnTo" in location.state &&
    typeof location.state.returnTo === "string"
      ? location.state.returnTo
      : location.pathname === "/login"
        ? ""
        : location.pathname + location.search;

  if (auth.loading) {
    return (
      <div className="login-page">
        <Spin size="large" tip="恢复身份会话…" />
      </div>
    );
  }
  if (auth.configFailed) {
    return (
      <Result
        status="500"
        title="认证服务配置不可用"
        subTitle={auth.error?.message || "无法加载认证运行配置。"}
      />
    );
  }
  if (auth.authMode !== "native") {
    return <Navigate to="/" replace />;
  }
  if (auth.nativeSession?.mode === "first_password_only") {
    return <Navigate to="/auth/first-password" replace />;
  }
  if (auth.session) {
    return (
      <Navigate
        to={
          auth.session.roles.includes("operator")
            ? "/overview"
            : "/access/sources"
        }
        replace
      />
    );
  }

  return (
    <div className="v12-login">
      <aside className="v12-login-art">
        <div>
          <div className="v12-login-brand">
            <span className="v12-login-symbol" aria-hidden="true">
              <svg
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
              >
                <path d="m12 3 9 5-9 5-9-5zM3 12l9 5 9-5M3 16l9 5 9-5" />
              </svg>
            </span>
            智能运维平台
          </div>
          <h1>
            从运行状态，
            <br />
            到每一次可靠处置。
          </h1>
          <p>统一资源、观测、调查与人工处置，让每个结论都有依据。</p>
          <div className="v12-login-grid">
            {[
              "资源与关系",
              "指标与日志",
              "告警与故障",
              "智能分析",
              "人工处置",
              "知识沉淀",
            ].map((name) => (
              <div key={name}>{name}</div>
            ))}
          </div>
        </div>
        <p>平台内置账号 · 明确授权 · 全程可追溯</p>
      </aside>
      <main className="v12-login-main">
        <section className="login-card" aria-labelledby="native-login-title">
          <span className="v12-login-tag">内置账号</span>
          <h2 id="native-login-title">登录智能运维平台</h2>
          <p className="login-hint">使用管理员创建的平台账号登录。</p>
          {auth.pendingRevocation ? (
            <Alert
              type="warning"
              showIcon
              style={{ marginBottom: 16 }}
              message="上次退出的服务器会话尚未确认撤销"
              description={
                <>
                  <div>
                    本机身份已清除，但服务器未能确认撤销该会话；此 Cookie
                    可能仍然有效。
                    {auth.pendingRevocation.attempts > 1
                      ? `已重试 ${auth.pendingRevocation.attempts} 次。`
                      : ""}
                  </div>
                  <Space style={{ marginTop: 8 }}>
                    <Button
                      size="small"
                      loading={retrying}
                      aria-busy={retrying}
                      onClick={() => void retryRevocation()}
                    >
                      重试撤销
                    </Button>
                    <Typography.Text type="secondary">
                      重复登录不会清除这条记录。
                    </Typography.Text>
                  </Space>
                  {revocationMessage ? (
                    <div role="status" style={{ marginTop: 8 }}>
                      {revocationMessage}
                    </div>
                  ) : null}
                </>
              }
            />
          ) : null}
          <Form
            layout="vertical"
            requiredMark={false}
            onFinish={async (values) => {
              inFlight.current?.abort();
              const controller = new AbortController();
              inFlight.current = controller;
              setSubmitting(true);
              setError(null);
              try {
                const next = await auth.nativeLogin(
                  String(values.loginName ?? ""),
                  String(values.password ?? ""),
                  returnTo,
                  controller.signal,
                );
                if (controller.signal.aborted) return;
                navigate(next, { replace: true });
              } catch (cause) {
                if (controller.signal.aborted) return;
                if (cause instanceof DOMException && cause.name === "AbortError")
                  return;
                setError(
                  cause instanceof Error ? cause : new Error("登录失败"),
                );
              } finally {
                if (inFlight.current === controller) inFlight.current = null;
                if (!controller.signal.aborted) setSubmitting(false);
              }
            }}
          >
            <Form.Item
              name="loginName"
              label="用户名"
              rules={[{ required: true, message: "请输入登录名" }]}
            >
              <Input autoComplete="username" />
            </Form.Item>
            <Form.Item
              name="password"
              label="密码"
              rules={[{ required: true, message: "请输入密码" }]}
            >
              <Input.Password
                autoComplete="current-password"
                visibilityToggle={false}
              />
            </Form.Item>
            <div className="v12-login-error">
              {error ? (
                <Alert type="error" showIcon title={loginMessage(error)} />
              ) : null}
            </div>
            <Button
              type="primary"
              size="large"
              htmlType="submit"
              block
              loading={submitting}
              aria-busy={submitting}
            >
              登录
              <svg
                className="v12-login-arrow"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.55"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <path d="M4 12h16m-6-6 6 6-6 6" />
              </svg>
            </Button>
          </Form>
          <p className="v12-login-footer">
            账号由平台管理员创建并分配权限。
            <br />
            请使用本人账号登录。
          </p>
        </section>
      </main>
    </div>
  );
}

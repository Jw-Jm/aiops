import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Form,
  Input,
  Result,
  Space,
  Spin,
  Typography,
} from "antd";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { ApiError } from "../api/client";
import { useAuth } from "../auth/AuthProvider";
import { NativeLoginLayout } from "../components/NativeLoginLayout";

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
  const [revocationMessage, setRevocationMessage] = useState<string | null>(
    null,
  );
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
    <NativeLoginLayout>
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
            setError(cause instanceof Error ? cause : new Error("登录失败"));
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
    </NativeLoginLayout>
  );
}

import { useState } from "react";
import { Alert, Button, Form, Input } from "antd";
import { Navigate, useNavigate } from "react-router-dom";
import { ApiError } from "../api/client";
import { useAuth } from "../auth/AuthProvider";

function changeMessage(error: unknown) {
  if (!(error instanceof ApiError)) return "修改服务暂时不可用，请稍后重试。";
  if (error.status === 429)
    return "尝试次数过多，修改暂时不可用。请稍后再试。";
  if (error.status === 409) return "凭据或会话状态已变化，请重新登录后再试。";
  if (error.status === 401) return "当前密码不正确。";
  if (error.status === 403)
    return "浏览器请求未通过同源检查。请重新登录后继续。";
  return error.message;
}

export function FirstPassword() {
  const auth = useAuth();
  const navigate = useNavigate();
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const view = auth.nativeSession;

  if (auth.loading) {
    return (
      <div className="login-page">
        <div className="login-card">正在恢复首次登录会话…</div>
      </div>
    );
  }
  if (!view || view.mode !== "first_password_only") {
    const home = auth.session?.roles.includes("operator")
      ? "/overview"
      : "/access/sources";
    return <Navigate to={auth.session ? home : "/login"} replace />;
  }

  return (
    <div className="login-page">
      <div className="login-brand">
        OPS<span>设置初始密码</span>
      </div>
      <section className="login-card" aria-labelledby="first-password-title">
        <h1 id="first-password-title">设置新的登录密码</h1>
        <p className="login-hint">
          出于安全要求，请重新输入当前初始密码；修改成功后旧会话会失效。
        </p>
        <Form
          layout="vertical"
          onFinish={async (values) => {
            setSubmitting(true);
            setError(null);
            try {
              await auth.changeNativePassword(
                String(values.currentPassword ?? ""),
                String(values.newPassword ?? ""),
              );
              navigate("/access/sources", { replace: true });
            } catch (cause) {
              setError(
                cause instanceof Error ? cause : new Error("修改密码失败"),
              );
            } finally {
              setSubmitting(false);
            }
          }}
        >
          <Form.Item
            name="currentPassword"
            label="当前密码"
            rules={[{ required: true, message: "请输入当前密码" }]}
          >
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Form.Item
            name="newPassword"
            label="新密码"
            extra="至少 12 个字符；不要复用当前密码。"
            rules={[{ required: true, message: "请输入新密码" }]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            name="confirmPassword"
            label="确认新密码"
            dependencies={["newPassword"]}
            rules={[
              { required: true, message: "请再次输入新密码" },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  return !value || value === getFieldValue("newPassword")
                    ? Promise.resolve()
                    : Promise.reject(new Error("两次输入的新密码不一致"));
                },
              }),
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          {error ? (
            <Alert type="error" showIcon title={changeMessage(error)} />
          ) : null}
          <Button
            type="primary"
            size="large"
            htmlType="submit"
            block
            loading={submitting}
          >
            设置密码并继续
          </Button>
          <Button
            block
            style={{ marginTop: 12 }}
            onClick={() => void auth.logout()}
          >
            退出
          </Button>
        </Form>
      </section>
    </div>
  );
}

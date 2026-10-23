import { useId, useState } from "react";
import { Alert, Button, Form, Input } from "antd";
import { Navigate, useNavigate } from "react-router-dom";
import { ApiError } from "../api/client";
import { useAuth } from "../auth/AuthProvider";
import { V12Modal } from "../components/v12/Layer";
import { NativeLoginLayout } from "../components/NativeLoginLayout";

function changeMessage(error: unknown) {
  if (!(error instanceof ApiError)) return "修改服务暂时不可用，请稍后重试。";
  if (error.status === 429) return "尝试次数过多，修改暂时不可用。请稍后再试。";
  if (error.status === 409) return "凭据或会话状态已变化，请重新登录后再试。";
  if (error.status === 401) return "当前密码不正确。";
  if (error.status === 403)
    return "浏览器请求未通过同源检查。请重新登录后继续。";
  return error.message;
}

export function FirstPassword() {
  const auth = useAuth();
  const navigate = useNavigate();
  const formId = useId();
  const [submitting, setSubmitting] = useState(false);
  const [closing, setClosing] = useState(false);
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

  const cancel = async () => {
    if (submitting || closing) return;
    setClosing(true);
    try {
      await auth.logout();
    } catch (cause) {
      setError(
        cause instanceof Error
          ? cause
          : new Error("退出未得到服务器确认，请重试。"),
      );
    } finally {
      setClosing(false);
    }
  };

  return (
    <div className="v12-first-password-page">
      <div inert aria-hidden="true">
        <NativeLoginLayout>
          <Form layout="vertical" requiredMark={false} disabled>
            <Form.Item label="用户名">
              <Input autoComplete="off" />
            </Form.Item>
            <Form.Item label="密码">
              <Input type="password" autoComplete="off" />
            </Form.Item>
            <Button type="primary" size="large" block disabled>
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
      </div>
      <V12Modal
        open
        centered
        closeDisabled={submitting || closing}
        width={660}
        rootClassName="v12-first-password-modal"
        title="首次登录：修改密码"
        onClose={() => void cancel()}
        footer={
          <>
            <Button
              autoInsertSpace={false}
              disabled={submitting || closing}
              loading={closing}
              onClick={() => void cancel()}
            >
              取消
            </Button>
            <Button
              type="primary"
              htmlType="submit"
              form={formId}
              loading={submitting}
              disabled={closing || submitting}
            >
              保存密码
            </Button>
          </>
        }
      >
        <Form
          id={formId}
          layout="vertical"
          requiredMark={false}
          disabled={submitting || closing}
          onFinish={async (values) => {
            if (submitting || closing) return;
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
            <Input type="password" autoComplete="current-password" />
          </Form.Item>
          <Form.Item
            name="newPassword"
            label="密码"
            extra="12～256 个字符；不要复用当前密码。"
            rules={[
              { required: true, message: "请输入新密码" },
              {
                validator: (_, value: string | undefined) =>
                  !value ||
                  ([...value].length >= 12 && [...value].length <= 256)
                    ? Promise.resolve()
                    : Promise.reject(new Error("密码须为 12～256 个字符")),
              },
            ]}
          >
            <Input type="password" autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            name="confirmPassword"
            label="确认密码"
            dependencies={["newPassword"]}
            rules={[
              { required: true, message: "请再次输入新密码" },
              ({ getFieldValue }) => ({
                validator: (_, value) =>
                  !value || value === getFieldValue("newPassword")
                    ? Promise.resolve()
                    : Promise.reject(new Error("两次输入的新密码不一致")),
              }),
            ]}
          >
            <Input type="password" autoComplete="new-password" />
          </Form.Item>
          <p className="v12-first-password-note">
            仅修改当前账号密码，不扩大角色与数据权限。取消将退出当前会话。
          </p>
          {error ? (
            <Alert type="error" showIcon title={changeMessage(error)} />
          ) : null}
        </Form>
      </V12Modal>
    </div>
  );
}

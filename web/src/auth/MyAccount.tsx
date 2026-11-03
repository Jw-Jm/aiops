import { useEffect, useId, useRef, useState } from "react";
import { Alert, Button, Form, Input } from "antd";
import { useAuth } from "./AuthProvider";
import { V12Modal } from "../components/v12/Layer";
import { ApiError } from "../api/client";
import { Note } from "../components/v12/primitives";

export function MyAccount({
  onClose,
  onLogout,
}: {
  onClose: () => void;
  onLogout: () => void;
}) {
  const auth = useAuth();
  const view = auth.nativeSession;
  const [password, setPassword] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();
  const inFlight = useRef(false);
  const active = useRef(true);
  const id = useId();
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);
  if (!view) return null;
  const granted = (role: string) =>
    view.authorization.grants.some((g) => g.role === role && !g.revokedAt);
  const close = () => {
    if (!inFlight.current) onClose();
  };
  return (
    <V12Modal
      open
      title={password ? "修改我的密码" : "我的账号"}
      onClose={close}
      closeDisabled={pending}
      footer={
        password ? (
          <>
            <Button
              disabled={pending}
              onClick={() => {
                setPassword(false);
                setError(undefined);
              }}
            >
              取消
            </Button>
            <Button
              type="primary"
              htmlType="submit"
              form={id}
              loading={pending}
              disabled={pending}
            >
              保存密码
            </Button>
          </>
        ) : (
          <>
            <Button onClick={() => setPassword(true)}>修改密码</Button>
            <Button danger onClick={onLogout}>
              退出登录
            </Button>
          </>
        )
      }
    >
      {password ? (
        <Form
          id={id}
          layout="vertical"
          requiredMark={false}
          disabled={pending}
          onFinish={async (values) => {
            if (inFlight.current) return;
            inFlight.current = true;
            setPending(true);
            setError(undefined);
            try {
              await auth.changeNativePassword(
                String(values.currentPassword),
                String(values.password),
              );
              if (active.current) {
                setPassword(false);
              }
            } catch (cause) {
              if (active.current)
                setError(
                  cause instanceof ApiError &&
                    cause.code === "CURRENT_PASSWORD_INVALID"
                    ? "当前密码不正确。"
                    : cause instanceof Error
                      ? cause.message
                      : "修改失败，请稍后重试。",
                );
            } finally {
              inFlight.current = false;
              if (active.current) setPending(false);
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
            name="password"
            label="密码"
            extra="12～256 个字符。"
            rules={[
              { required: true, message: "请输入新密码" },
              {
                validator: (_, value) =>
                  !value ||
                  (Array.from(value).length >= 12 &&
                    Array.from(value).length <= 256)
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
            dependencies={["password"]}
            rules={[
              { required: true, message: "请确认新密码" },
              ({ getFieldValue }) => ({
                validator: (_, value) =>
                  !value || value === getFieldValue("password")
                    ? Promise.resolve()
                    : Promise.reject(new Error("两次输入的密码不一致")),
              }),
            ]}
          >
            <Input type="password" autoComplete="new-password" />
          </Form.Item>
          <p className="v12-first-password-note">
            仅修改当前账号密码，不扩大角色与数据权限。
          </p>
          {error ? <Alert type="error" showIcon title={error} /> : null}
        </Form>
      ) : (
        <>
          <dl className="v12-key-values">
            {Object.entries({
              用户名: view.loginName || "当前版本未提供",
              姓名: view.displayName || "当前版本未提供",
              账号类型: "平台内置账号",
              运维操作员: granted("operator") ? "已授权" : "未授权",
              平台管理员: granted("platform_admin") ? "已授权" : "未授权",
              可操作集群: granted("operator")
                ? view.authorizedClusters
                    .map((c) => c.displayName)
                    .join("、") || "无可用授权集群"
                : "未授予运维数据权限",
            }).map(([label, value]) => (
              <div key={label}>
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
          <Note>退出会撤销当前登录会话，不会取消后台任务。</Note>
        </>
      )}
    </V12Modal>
  );
}

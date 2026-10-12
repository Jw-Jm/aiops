import { useEffect, useState } from "react";
import { Alert, Spin } from "antd";
import { useNavigate } from "react-router-dom";
import { getManager, safeReturnTo } from "./oidc";
import { useAuth } from "./AuthProvider";
let callback:
  | ReturnType<Awaited<ReturnType<typeof getManager>>["signinRedirectCallback"]>
  | undefined;
export function Callback() {
  const auth = useAuth();
  const navigate = useNavigate();
  const [error, setError] = useState<Error>();
  useEffect(() => {
    if (!callback)
      callback = getManager().then((m) => m.signinRedirectCallback());
    void callback
      .then(async (user) => {
        await auth.accept(user);
        navigate(
          safeReturnTo(
            (user.state as { returnTo?: string } | undefined)?.returnTo,
          ),
          { replace: true },
        );
      })
      .catch(setError);
  }, []);
  return error ? (
    <Alert
      type="error"
      showIcon
      title="登录回调失败"
      description={error.message}
    />
  ) : (
    <Spin tip="验证登录回调…" />
  );
}
export function SilentCallback() {
  useEffect(() => {
    void getManager().then((m) => m.signinSilentCallback());
  }, []);
  return null;
}

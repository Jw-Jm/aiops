import { useMutation } from "@tanstack/react-query";
import { Alert, Button, Space } from "antd";
import { useAuth } from "./AuthProvider";
import { login } from "./oidc";
import { createStepUpSession } from "../api/generated/platform";
import { checked, requestOptions } from "../api/client";
import { record, displayTime } from "../components/data";
import { QueryState } from "../components/QueryState";
export function StepUpControl({
  onReady,
}: {
  onReady: (expiresAt: string) => void;
}) {
  const { session, authMode } = useAuth();
  const register = useMutation({
    mutationFn: () =>
      checked(createStepUpSession({}, requestOptions(undefined, true))),
    onSuccess: (r) => {
      const expires = record(record(r).data).expiresAt;
      if (typeof expires === "string") onReady(expires);
    },
  });
  if (authMode === "native")
    return (
      <Alert
        type="info"
        title="请在具体操作处验证身份"
        description="原生再次验证绑定目标和提交内容。请先在账号安全中启用双因素。"
      />
    );
  return (
    <>
      <Alert
        type="info"
        showIcon
        title="修改配置前验证本人身份"
        description="读取配置不需要额外验证。先完成多因素登录，再点击“启用本次配置修改”；有效期结束后需要重新验证。"
      />
      <Space wrap>
        <Button onClick={() => void login(true)}>验证本人身份（多因素）</Button>
        <Button
          disabled={session?.acr !== "urn:ops:loa:2"}
          loading={register.isPending}
          onClick={() => register.mutate()}
        >
          启用本次配置修改
        </Button>
        {register.isSuccess && (
          <span>
            有效至 {displayTime(record(record(register.data).data).expiresAt)}
          </span>
        )}
      </Space>
      {register.error && <QueryState loading={false} error={register.error} />}
    </>
  );
}

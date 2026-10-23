import { useEffect, useRef, useState } from "react";
import { Alert, Button, Form, Input, Space } from "antd";
import {
  createNativeStepUpChallenge,
  verifyNativeStepUpChallenge,
} from "../api/generated/platform";
import type {
  NativeStepUpTargetV1,
  NativeStepUpChallengeCreateRequestV1Operation,
  NativeStepUpVerifySuccessV1,
} from "../api/generated/models";
import { ApiError, checked, requestOptions } from "../api/client";
import { nativeWriteDigest } from "./nativeWriteDigest";
import { useAuth } from "./AuthProvider";

export type NativeWriteIntent = {
  operation: NativeStepUpChallengeCreateRequestV1Operation;
  target: NativeStepUpTargetV1;
  command?: Record<string, unknown>;
  write?: { method: string; path: string; body: unknown };
};
export function NativeStepUpControl({
  intent,
  onReady,
}: {
  intent: NativeWriteIntent;
  onReady: (expiresAt: string, challengeId: string) => void;
}) {
  const auth = useAuth();
  const [challenge, setChallenge] = useState<{
    id: string;
    revision: number;
    completed: string[];
  } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const active = useRef(true);
  const controller = useRef(new AbortController());
  useEffect(() => {
    active.current = true;
    controller.current = new AbortController();
    return () => {
      controller.current.abort();
      active.current = false;
    };
  }, []);
  const verify = async (password: string, totp: string) => {
    if (busy || !auth.nativeSession) return;
    setBusy(true);
    setError("");
    let current =
      challenge?.completed.includes("password") &&
      challenge.completed.includes("totp")
        ? null
        : challenge;
    try {
      if (!current) {
        const target = { ...intent.target };
        if (intent.write) {
          target.write = {
            method: intent.write.method,
            path: intent.write.path,
            bodyDigest: await nativeWriteDigest(
              auth.nativeSession.csrf?.token ?? "",
              intent.write.method,
              intent.write.path,
              intent.write.body,
            ),
          };
        }
        const result = await checked(
          createNativeStepUpChallenge(
            {
              operation: intent.operation,
              target,
              ...(intent.command ? { command: intent.command as never } : {}),
              expectedSessionVersion:
                auth.nativeSession.revocation.sessionVersion,
            },
            requestOptions(controller.current.signal, true),
          ),
          false,
          () => active.current,
        );
        current = {
          id: result.data.challengeId,
          revision: result.data.revision,
          completed: [],
        };
        setChallenge(current);
      }
      for (const method of ["password", "totp"] as const) {
        if (current.completed.includes(method)) continue;
        const result: NativeStepUpVerifySuccessV1 = await checked(
          verifyNativeStepUpChallenge(
            current.id,
            {
              method,
              credential: method === "password" ? password : totp,
              expectedChallengeRevision: current.revision,
            },
            requestOptions(controller.current.signal, true),
          ),
          false,
          () => active.current,
        );
        current = {
          ...current,
          revision: result.data.revision,
          completed: result.data.completedMethods,
        };
        setChallenge(current);
        if (result.data.failureCode) {
          if (result.data.challengeState !== "pending") setChallenge(null);
          throw new Error(
            result.data.challengeState === "pending"
              ? "验证未通过，请核对凭据后重试。"
              : "挑战已失效，请重新验证。",
          );
        }
        if (result.data.verified && result.data.stepUpSession)
          onReady(
            new Date(
              Math.min(
                Date.parse(result.data.stepUpSession.absoluteExpiresAt),
                Date.parse(
                  result.data.stepUpSession.idleExpiresAt ||
                    result.data.stepUpSession.absoluteExpiresAt,
                ),
              ),
            ).toISOString(),
            current.id,
          );
      }
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) setChallenge(null);
      if (active.current) setError(e instanceof Error ? e.message : "验证失败");
    } finally {
      if (active.current) setBusy(false);
    }
  };
  return (
    <Space orientation="vertical">
      <Alert
        type="info"
        title="验证本次具体操作"
        description="验证与当前目标、修订号和提交内容绑定。修改内容后须重新验证。"
      />
      <Form
        layout="inline"
        onFinish={(v) => verify(String(v.password ?? ""), String(v.totp ?? ""))}
      >
        <Form.Item
          name="password"
          label="验证密码"
          rules={[{ required: true }]}
        >
          <Input.Password autoComplete="current-password" />
        </Form.Item>
        <Form.Item
          name="totp"
          label="验证动态码"
          rules={[{ required: true }, { pattern: /^[0-9]{6}$/ }]}
        >
          <Input
            autoComplete="one-time-code"
            inputMode="numeric"
            maxLength={6}
          />
        </Form.Item>
        <Button
          htmlType="submit"
          aria-label="验证本次操作"
          aria-busy={busy}
          loading={busy}
        >
          验证本次操作
        </Button>
      </Form>
      {error ? <Alert type="error" title={error} /> : null}
    </Space>
  );
}

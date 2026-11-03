// Native account security: TOTP enrolment, password+TOTP step-up, recovery
// codes and administrator MFA reset. Every assurance level shown here comes
// from a server response; nothing is derived from a checkbox or a cookie.
import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Divider,
  Form,
  Input,
  Select,
  Result,
  Space,
  Tag,
  Typography,
} from "antd";
import { useAuth } from "../auth/AuthProvider";
import { ApiError, checked as checkedAPI, requestOptions } from "../api/client";
import {
  createNativeMFAEnrollment,
  createNativeStepUpChallenge,
  getNativeMFAStatus,
  getNativeUsers,
  resetNativeUserMFA,
  verifyNativeMFAEnrollment,
  verifyNativeRecoveryCode,
  verifyNativeStepUpChallenge,
} from "../api/generated/platform";
import type {
  NativeMFAStatusV1,
  NativeStepUpSessionV1,
} from "../api/generated/models";
import { PageHeading } from "../components/PageHeading";
import { QueryState } from "../components/QueryState";
import { useQuery } from "@tanstack/react-query";

type Target = Record<string, unknown> & { kind: string; revision: number };

type Challenge = {
  id: string;
  revision: number;
  operation: string;
  target: Target;
};

function operationLabel(operation: string) {
  switch (operation) {
    case "mfa_enrollment":
      return "双因素绑定";
    case "mfa_reset":
      return "管理员重置 MFA";
    case "recovery_code_regenerate":
      return "恢复码验证";
    default:
      return operation;
  }
}

export function AccountSecurity() {
  const auth = useAuth();
  const session = auth.nativeSession;
  const [adminCursor, setAdminCursor] = useState<string>();
  const [adminForm] = Form.useForm();
  const adminUsers = useQuery({
    queryKey: ["native-users", adminCursor],
    enabled:
      auth.authMode === "native" &&
      !!session?.authorization.grants.some(
        (g) => g.role === "platform_admin" && !g.revokedAt,
      ),
    queryFn: ({ signal }) =>
      checkedAPI(
        getNativeUsers(
          { limit: 50, cursor: adminCursor },
          requestOptions(signal),
        ),
        false,
      ),
  });
  const statusQuery = useQuery({
    queryKey: ["native-mfa-status", session?.subject, session?.sessionRef],
    enabled: auth.authMode === "native" && !!session,
    queryFn: ({ signal }) =>
      checkedAPI(getNativeMFAStatus(requestOptions(signal)), false),
  });
  const status = statusQuery.data?.data as NativeMFAStatusV1 | undefined;
  const load = () => statusQuery.refetch();
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  const [enrollment, setEnrollment] = useState<{
    id: string;
    revision: number;
    uri: string;
    expiresAt: string;
  } | null>(null);
  const [challenge, setChallenge] = useState<Challenge | null>(null);
  const [stepUp, setStepUp] = useState<NativeStepUpSessionV1 | null>(null);
  const [completed, setCompleted] = useState<string[]>([]);
  const [codes, setCodes] = useState<string[] | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const inFlight = useRef(false);
  const active = useRef(true);
  const operationGeneration = useRef(0);
  const controller = useRef(new AbortController());
  useEffect(() => {
    active.current = true;
    controller.current = new AbortController();
    return () => {
      active.current = false;
      operationGeneration.current++;
      controller.current.abort();
    };
  }, []);
  function checked(
    promise: Promise<{ data: unknown; status: number; headers: Headers }>,
    notify = false,
  ) {
    const generation = operationGeneration.current;
    return checkedAPI(
      promise,
      notify,
      () => active.current && generation === operationGeneration.current,
    );
  }

  if (auth.authMode !== "native" || !session || !session.accountRevision) {
    return (
      <Result
        status="warning"
        title="当前认证模式不支持内置账号安全设置"
        subTitle="该页面只适用于平台内置账号会话。"
      />
    );
  }

  const targetForSelf = (): Target => ({
    kind: "user",
    userId: session.subject.replace(/^user:/, ""),
    revision: session.accountRevision!,
  });

  const cancelBinding = (message: string | null) => {
    operationGeneration.current++;
    controller.current.abort();
    controller.current = new AbortController();
    inFlight.current = false;
    setBusy(false);
    setEnrollment(null);
    setChallenge(null);
    setStepUp(null);
    setCompleted([]);
    setCodes(null);
    setError(null);
    setNotice(message);
  };

  const guard = async (action: () => Promise<void>) => {
    if (inFlight.current) return;
    inFlight.current = true;
    const generation = operationGeneration.current;
    setBusy(true);
    setError(null);
    try {
      await action();
    } catch (cause) {
      if (generation !== operationGeneration.current || !active.current) return;
      if (cause instanceof DOMException && cause.name === "AbortError") return;
      if (cause instanceof ApiError) {
        if (cause.status === 409 && (enrollment || challenge)) {
          cancelBinding(null);
          setError(new Error("本次验证已失效或版本已变化，请重新开始验证。"));
          return;
        }
        // The account/factor failure budget and password-work capacity are server
        // decisions, not client-side state: show the cooldown truthfully and let
        // the server decide when the next attempt is allowed.
        if (cause.code === "RATE_LIMITED") {
          setChallenge(null);
          setStepUp(null);
          setCompleted([]);
          setError(
            new Error(
              "该账户的验证失败次数已达上限，已进入服务端冷却时间，请稍后重新开始验证。",
            ),
          );
          return;
        }
        if (cause.code === "UNAVAILABLE") {
          setError(
            new Error(
              "密码校验暂时繁忙，本次请求未判定为密码错误，请稍后重试。",
            ),
          );
          return;
        }
        setError(new Error(cause.message));
      } else setError(cause instanceof Error ? cause : new Error("操作失败"));
    } finally {
      if (generation === operationGeneration.current && active.current) {
        inFlight.current = false;
        setBusy(false);
      }
    }
  };

  const beginChallenge = async (operation: string, target: Target) => {
    const payload = (await checked(
      createNativeStepUpChallenge(
        {
          operation: operation as never,
          target: target as never,
          expectedSessionVersion: session.revocation.sessionVersion,
        },
        requestOptions(controller.current.signal, true),
      ),
      false,
    )) as unknown as { data: { challengeId: string; revision: number } };
    const created: Challenge = {
      id: payload.data.challengeId,
      revision: payload.data.revision,
      operation,
      target,
    };
    setChallenge(created);
    setStepUp(null);
    setCompleted([]);
    setCodes(null);
    setNotice(`已创建${operationLabel(operation)}挑战，请提交当前密码。`);
  };

  const submitPassword = (password: string) =>
    guard(async () => {
      if (!challenge) return;
      const payload = (await checked(
        verifyNativeStepUpChallenge(
          challenge.id,
          {
            method: "password",
            credential: password,
            expectedChallengeRevision: challenge.revision,
          },
          requestOptions(controller.current.signal, true),
        ),
        false,
      )) as unknown as {
        data: {
          verified: boolean;
          challengeState: string;
          revision: number;
          completedMethods: string[];
          failureCode: string | null;
        };
      };
      setCompleted(payload.data.completedMethods);
      setChallenge(
        ["revoked", "expired"].includes(payload.data.challengeState)
          ? null
          : { ...challenge, revision: payload.data.revision },
      );
      const closed = ["revoked", "expired"].includes(
        payload.data.challengeState,
      );
      if (payload.data.failureCode || closed) {
        if (closed) cancelBinding(null);
        setError(
          new Error(
            closed
              ? "本次验证已失效，请重新开始验证。"
              : "当前密码不正确，挑战仍待完成。",
          ),
        );
        return;
      }
      setNotice("密码已验证，请提交身份验证器中的 6 位动态码。");
    });

  const submitTOTP = (code: string) =>
    guard(async () => {
      if (!challenge) return;
      const payload = (await checked(
        verifyNativeStepUpChallenge(
          challenge.id,
          {
            method: "totp",
            credential: code,
            expectedChallengeRevision: challenge.revision,
          },
          requestOptions(controller.current.signal, true),
        ),
        false,
      )) as unknown as {
        data: {
          verified: boolean;
          challengeState: string;
          revision: number;
          completedMethods: string[];
          acr: string;
          stepUpSession: NativeStepUpSessionV1 | null;
          failureCode: string | null;
        };
      };
      setCompleted(payload.data.completedMethods);
      setChallenge(
        ["revoked", "expired"].includes(payload.data.challengeState)
          ? null
          : { ...challenge, revision: payload.data.revision },
      );
      if (payload.data.failureCode || !payload.data.stepUpSession) {
        const closed = ["revoked", "expired"].includes(
          payload.data.challengeState,
        );
        if (closed) cancelBinding(null);
        setError(
          new Error(
            closed
              ? "本次验证已失效，请重新开始验证。"
              : "动态码不正确或该挑战已失效。",
          ),
        );
        return;
      }
      setStepUp(payload.data.stepUpSession);
      setNotice("密码与动态码均已通过，已获得再次验证会话。");
      if (challenge.operation === "mfa_enrollment" && enrollment) {
        setNotice(
          "本人身份已验证，请输入新绑定身份验证器的动态码以确认。旧绑定在确认成功前仍有效。",
        );
      }
    });

  const confirmEnrollment = async (code: string) => {
    if (!enrollment || !challenge) return;
    // The confirmation code must come from the same pending secret; the server
    // rechecks the bound step-up inside the write transaction.
    const payload = (await checked(
      verifyNativeMFAEnrollment(
        enrollment.id,
        {
          totpCode: code,
          expectedEnrollmentRevision: enrollment.revision,
          stepUp: {
            challengeId: challenge.id,
            sessionRef: session.sessionRef,
          },
        },
        requestOptions(controller.current.signal, true),
      ),
      false,
    )) as unknown as { data: { recoveryCodes: { codes: string[] } } };
    setCodes(payload.data.recoveryCodes.codes);
    setEnrollment(null);
    setChallenge(null);
    setStepUp(null);
    setNotice("双因素已启用。恢复码只显示这一次，请离线保存。");
    await load();
    await auth.refresh();
  };

  const startEnrollment = () =>
    guard(async () => {
      const payload = (await checked(
        createNativeMFAEnrollment(
          {
            method: "totp",
            expectedCredentialVersion: session.revocation.credentialVersion,
          },
          requestOptions(controller.current.signal, true),
        ),
        false,
      )) as unknown as {
        data: {
          enrollmentId: string;
          otpauthUri: string;
          expiresAt: string;
        };
      };
      // A freshly created enrolment has revision 1.
      setEnrollment({
        id: payload.data.enrollmentId,
        revision: 1,
        uri: payload.data.otpauthUri,
        expiresAt: payload.data.expiresAt,
      });
      setCodes(null);
      setNotice("已生成一次性绑定密钥。把它加入身份验证器后完成验证。");
      await beginChallenge("mfa_enrollment", {
        kind: "mfa_enrollment",
        enrollmentId: payload.data.enrollmentId,
        revision: 1,
      });
    });

  const consumeRecovery = (code: string) =>
    guard(async () => {
      if (!challenge) return;
      const payload = (await checked(
        verifyNativeRecoveryCode(
          {
            challengeId: challenge.id,
            recoveryCode: code,
            expectedChallengeRevision: challenge.revision,
          },
          requestOptions(controller.current.signal, true),
        ),
        false,
      )) as unknown as {
        data: {
          verified: boolean;
          challengeRevision?: number;
          challengeState?: string;
          sessionsRevoked: number;
          recoveryCodesRemaining: number;
          mustReenrollMfa: boolean;
        };
      };
      if (payload.data.challengeRevision)
        setChallenge({
          ...challenge,
          revision: payload.data.challengeRevision,
        });
      if (
        payload.data.challengeState &&
        payload.data.challengeState !== "pending"
      )
        setChallenge(null);
      if (!payload.data.verified) {
        setError(new Error("恢复码无效或已使用。"));
        return;
      }
      setNotice(
        `恢复码已使用：已撤销 ${payload.data.sessionsRevoked} 个会话，必须重新登录并重新绑定双因素。`,
      );
      auth.refresh().catch(() => undefined);
    });

  const stateTag = () => {
    switch (status?.state) {
      case "confirmed":
        return <Tag color="green">已启用</Tag>;
      case "pending_confirmation":
        return <Tag color="gold">待确认</Tag>;
      case "expired":
        return <Tag>已过期</Tag>;
      case "reset_required":
        return <Tag color="red">必须重新设置</Tag>;
      default:
        return <Tag>未启用</Tag>;
    }
  };

  const isAdmin = session.authorization.grants.some(
    (grant) => grant.role === "platform_admin",
  );

  return (
    <div className="v12-page">
      <PageHeading
        title="账号安全"
        description="双因素、再次验证与恢复。只有密码加动态码的真实挑战才会提升保证级别。"
      />
      {error ? (
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 16 }}
          message="操作未完成"
          description={error.message}
        />
      ) : null}
      {notice ? (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
          message={notice}
        />
      ) : null}

      <Card title="双因素状态" size="small" style={{ marginBottom: 16 }}>
        <QueryState
          loading={statusQuery.isFetching || statusQuery.isPending}
          error={statusQuery.error}
          retry={() => void load()}
        >
          <Descriptions column={2} size="small">
            <Descriptions.Item label="状态">{stateTag()}</Descriptions.Item>
            <Descriptions.Item label="当前保证级别">
              {session.acr === "urn:ops:loa:2" ? (
                <Tag color="blue">LoA 2（密码 + 动态码）</Tag>
              ) : (
                <Tag>LoA 1（仅密码）</Tag>
              )}
            </Descriptions.Item>
            <Descriptions.Item label="剩余恢复码">
              {status?.recoveryCodesRemaining ?? 0}
            </Descriptions.Item>
            <Descriptions.Item label="确认时间">
              {status?.confirmedAt ?? "—"}
            </Descriptions.Item>
          </Descriptions>
          <Space style={{ marginTop: 12 }}>
            <Button onClick={() => void load()} loading={busy}>
              刷新状态
            </Button>
            {status?.state === "confirmed" ? (
              <Button onClick={() => void startEnrollment()} loading={busy}>
                重新绑定双因素
              </Button>
            ) : (
              <Button
                type="primary"
                onClick={() => void startEnrollment()}
                loading={busy}
              >
                启用双因素
              </Button>
            )}
          </Space>
        </QueryState>
      </Card>

      {enrollment ? (
        <Card title="完成绑定" size="small" style={{ marginBottom: 16 }}>
          <Typography.Paragraph>
            此密钥只显示一次，页面刷新后不会再返回。请使用身份验证器扫描或手工录入。
          </Typography.Paragraph>
          <Typography.Paragraph copyable={{ text: enrollment.uri }}>
            <code>{enrollment.uri}</code>
          </Typography.Paragraph>
          <Typography.Text type="secondary">
            过期时间：{enrollment.expiresAt}
          </Typography.Text>
          <Divider />
          <Space direction="vertical" style={{ width: "100%" }}>
            <Form
              layout="inline"
              onFinish={(values) =>
                submitPassword(String(values.password ?? ""))
              }
            >
              <Form.Item
                name="password"
                label="当前密码"
                rules={[{ required: true, message: "请输入当前密码" }]}
              >
                <Input.Password autoComplete="current-password" />
              </Form.Item>
              <Button
                htmlType="submit"
                loading={busy}
                disabled={!challenge || completed.includes("password")}
              >
                验证密码
              </Button>
            </Form>
            <Form
              layout="inline"
              onFinish={(values) => submitTOTP(String(values.code ?? ""))}
            >
              <Form.Item
                name="code"
                label="动态码"
                rules={[
                  { required: true, message: "请输入 6 位动态码" },
                  { pattern: /^[0-9]{6}$/, message: "动态码为 6 位数字" },
                ]}
              >
                <Input
                  autoComplete="one-time-code"
                  inputMode="numeric"
                  maxLength={6}
                />
              </Form.Item>
              <Button
                htmlType="submit"
                type="primary"
                loading={busy}
                disabled={!challenge || !completed.includes("password")}
              >
                验证动态码
              </Button>
            </Form>
            {stepUp && challenge?.operation === "mfa_enrollment" ? (
              <Form
                layout="inline"
                onFinish={(values) =>
                  guard(() => confirmEnrollment(String(values.newCode ?? "")))
                }
              >
                <Form.Item
                  name="newCode"
                  label="新绑定动态码"
                  rules={[{ required: true }, { pattern: /^[0-9]{6}$/ }]}
                >
                  <Input
                    autoComplete="one-time-code"
                    inputMode="numeric"
                    maxLength={6}
                  />
                </Form.Item>
                <Button htmlType="submit" type="primary" loading={busy}>
                  确认新绑定
                </Button>
              </Form>
            ) : null}
            <Button
              onClick={() =>
                cancelBinding(
                  "已停止等待本次验证。服务器可能已处理请求，请刷新状态确认；未确认的密钥将到期失效。",
                )
              }
            >
              取消绑定
            </Button>
            <Typography.Text type="secondary">
              已完成验证：
              {completed.length
                ? completed
                    .map((method) =>
                      method === "password"
                        ? "密码"
                        : method === "totp"
                          ? "动态码"
                          : method,
                    )
                    .join(" + ")
                : "无"}
            </Typography.Text>
          </Space>
        </Card>
      ) : null}

      {codes ? (
        <Card
          title="恢复码（仅显示一次）"
          size="small"
          style={{ marginBottom: 16 }}
        >
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 12 }}
            message="请离线保存。每个恢复码只能使用一次，且恢复后只得到 LoA 1 并强制重新绑定双因素。"
          />
          <Space wrap>
            {codes.map((code) => (
              <Tag key={code} style={{ fontFamily: "monospace" }}>
                {code}
              </Tag>
            ))}
          </Space>
        </Card>
      ) : null}

      <Card title="使用恢复码" size="small" style={{ marginBottom: 16 }}>
        <Typography.Paragraph type="secondary">
          需要先创建恢复挑战，再提交一个恢复码。恢复码不会产生再次验证会话。
        </Typography.Paragraph>
        <Space direction="vertical" style={{ width: "100%" }}>
          <Button
            onClick={() =>
              void guard(() =>
                beginChallenge("recovery_code_regenerate", targetForSelf()),
              )
            }
            loading={busy}
          >
            创建恢复挑战
          </Button>
          {challenge?.operation === "recovery_code_regenerate" ? (
            <Form
              layout="inline"
              onFinish={(values) =>
                consumeRecovery(String(values.recoveryCode ?? "").toUpperCase())
              }
            >
              <Form.Item
                name="recoveryCode"
                label="恢复码"
                rules={[
                  { required: true, message: "请输入恢复码" },
                  {
                    pattern: /^[A-Za-z0-9]{4}-[A-Za-z0-9]{4}$/,
                    message: "恢复码格式为 XXXX-XXXX",
                  },
                ]}
              >
                <Input maxLength={9} />
              </Form.Item>
              <Button htmlType="submit" loading={busy}>
                提交恢复码
              </Button>
            </Form>
          ) : null}
        </Space>
      </Card>

      {isAdmin ? (
        <Card title="管理员重置 MFA（高风险）" size="small">
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 12 }}
            message="需由当前管理员使用密码和动态码验证本次重置；目标账号的现有会话与恢复码将失效。"
          />
          <Form
            form={adminForm}
            layout="vertical"
            disabled={busy || adminUsers.isPending || !!adminUsers.error}
            onFinish={(values) =>
              guard(async () => {
                const targetUserId = String(values.userId ?? "");
                const target = adminUsers.data?.data.find(
                  (user) => user.userId === targetUserId,
                );
                if (!target)
                  throw new Error("请选择当前页的目标账号，再核对登录名。");
                const revision = target.revision;
                const credentialVersion = target.revocation.credentialVersion;
                // 1) Create the target-bound challenge and complete password+TOTP.
                const created = (await checked(
                  createNativeStepUpChallenge(
                    {
                      operation: "mfa_reset",
                      target: {
                        kind: "user",
                        userId: targetUserId,
                        revision,
                      } as never,
                      expectedSessionVersion: session.revocation.sessionVersion,
                    },
                    requestOptions(controller.current.signal, true),
                  ),
                  false,
                )) as unknown as {
                  data: { challengeId: string; revision: number };
                };
                let challengeRevision = created.data.revision;
                const password = (await checked(
                  verifyNativeStepUpChallenge(
                    created.data.challengeId,
                    {
                      method: "password",
                      credential: String(values.password ?? ""),
                      expectedChallengeRevision: challengeRevision,
                    },
                    requestOptions(controller.current.signal, true),
                  ),
                  false,
                )) as unknown as {
                  data: { stepUpSession: NativeStepUpSessionV1 | null };
                };
                challengeRevision += 1;
                const totp = (await checked(
                  verifyNativeStepUpChallenge(
                    created.data.challengeId,
                    {
                      method: "totp",
                      credential: String(values.totp ?? ""),
                      expectedChallengeRevision: challengeRevision,
                    },
                    requestOptions(controller.current.signal, true),
                  ),
                  false,
                )) as unknown as {
                  data: { stepUpSession: NativeStepUpSessionV1 | null };
                };
                if (!password.data.stepUpSession && !totp.data.stepUpSession) {
                  setError(new Error("再次验证未完成，未执行重置。"));
                  return;
                }
                const payload = (await checked(
                  resetNativeUserMFA(
                    targetUserId,
                    {
                      userId: targetUserId,
                      confirmLoginName: String(values.confirmLoginName ?? ""),
                      reason: String(values.reason ?? ""),
                      expectedUserRevision: revision,
                      expectedCredentialVersion: credentialVersion,
                      stepUp: {
                        challengeId: created.data.challengeId,
                        sessionRef: session.sessionRef,
                      },
                    },
                    requestOptions(controller.current.signal, true),
                  ),
                  false,
                )) as unknown as {
                  data: {
                    sessionsRevoked: number;
                    credentialVersion: number;
                    auditRecordId: string;
                  };
                };
                void adminUsers.refetch();
                adminForm.resetFields([
                  "password",
                  "totp",
                  "confirmLoginName",
                  "userId",
                ]);
                setNotice(
                  `重置完成：撤销 ${payload.data.sessionsRevoked} 个会话。该账号需要重新登录并设置双因素。`,
                );
              })
            }
          >
            <Form.Item
              name="userId"
              label="目标账号（本页）"
              rules={[{ required: true, message: "请选择目标账号" }]}
            >
              <Select
                aria-label="目标账号（本页）"
                showSearch
                optionFilterProp="label"
                options={(adminUsers.data?.data ?? []).map((user) => ({
                  value: user.userId,
                  label: `${user.displayName} / ${user.loginName}`,
                }))}
                onChange={() =>
                  adminForm.setFieldValue("confirmLoginName", undefined)
                }
              />
            </Form.Item>
            <QueryState
              loading={adminUsers.isPending}
              error={adminUsers.error}
              retry={() => void adminUsers.refetch()}
            />
            {adminCursor || adminUsers.data?.meta.nextCursor ? (
              <Space style={{ marginBottom: 16 }}>
                <Button
                  disabled={!adminCursor || busy}
                  onClick={() => {
                    setAdminCursor(undefined);
                    adminForm.setFieldValue("userId", undefined);
                  }}
                >
                  首页
                </Button>
                <Button
                  disabled={!adminUsers.data?.meta.nextCursor || busy}
                  onClick={() => {
                    setAdminCursor(adminUsers.data?.meta.nextCursor);
                    adminForm.setFieldValue("userId", undefined);
                  }}
                >
                  下一页
                </Button>
              </Space>
            ) : null}
            <Form.Item
              name="confirmLoginName"
              label="确认登录名"
              rules={[{ required: true, message: "请再次输入目标登录名" }]}
            >
              <Input autoComplete="off" />
            </Form.Item>
            <Form.Item
              name="reason"
              label="原因"
              rules={[
                { required: true, message: "请输入原因" },
                { min: 5, message: "至少 5 个字符" },
              ]}
            >
              <Input />
            </Form.Item>
            <Space>
              <Form.Item
                name="password"
                label="我的当前密码"
                rules={[{ required: true, message: "必填" }]}
              >
                <Input.Password autoComplete="current-password" />
              </Form.Item>
              <Form.Item
                name="totp"
                label="我的动态码"
                rules={[
                  { required: true, message: "必填" },
                  { pattern: /^[0-9]{6}$/, message: "6 位数字" },
                ]}
              >
                <Input maxLength={6} autoComplete="one-time-code" />
              </Form.Item>
            </Space>
            <Button
              htmlType="submit"
              danger
              loading={busy}
              disabled={busy || adminUsers.isPending || !!adminUsers.error}
            >
              执行管理员重置
            </Button>
          </Form>
          {stepUp ? (
            <Typography.Text type="secondary">
              本次操作已完成密码和动态码验证。
            </Typography.Text>
          ) : null}
        </Card>
      ) : null}
    </div>
  );
}

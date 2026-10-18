import { getResourceByCanonicalID } from "../../api/generated/platform";
import { resourceIdentifier } from "../../components/FaultSummary";
import { matchesAssessment } from "./assessmentBinding";
import { abortDelay } from "../investigation/delay";
import { useState, useEffect, useRef } from "react";
import { useQuery, useMutation } from "@tanstack/react-query";
import {
  Input,
  InputNumber,
  Select,
  Button,
  Checkbox,
  Alert,
  Space,
} from "antd";
import { useSearchParams } from "react-router-dom";
import {
  getExecutionProfiles,
  getActionPlan,
  assessCommandRisk,
  acknowledgeCommandRisk,
  createCommandExecution,
  getCommandExecution,
  createStepUpSession,
  getStreamCommandExecutionEventsUrl,
  cancelCommandExecution,
} from "../../api/generated/platform";
import type {
  IncidentV2,
  CommandRiskAssessmentRequest,
  CommandExecutionRequest,
} from "../../api/generated/models";
import {
  checked,
  requestOptions,
  authEvents,
  clearCredentials,
} from "../../api/client";
import {
  streamJSONEvents,
  decodeCommandExecutionEvent,
  SSEHttpError,
} from "../../api/sse-reader";
import { record, list, text, boundedText } from "../../components/data";
import { Details } from "../../components/Details";
import { QueryState } from "../../components/QueryState";
import { Status } from "../../components/Status";
import { login } from "../../auth/oidc";
import { useAuth } from "../../auth/AuthProvider";
import { CommandEditor } from "./CommandEditor";
import { SuggestionCard } from "./SuggestionCard";
import { RiskPanel } from "./RiskPanel";
import { ExecutionOutput } from "./ExecutionOutput";
import { commandSequence, base64Bytes, archivedPreview } from "./output";
import { PostCheckPanel } from "./PostCheckPanel";
export function profileChoice(value: Record<string, unknown>) {
  return text(value.executionProfileId) + "@" + text(value.version);
}
export function requestFingerprint(request: CommandRiskAssessmentRequest) {
  return JSON.stringify(request);
}
export function CommandComposer({ incident }: { incident: IncidentV2 }) {
  const [url, setUrl] = useSearchParams();
  const auth = useAuth();
  const [command, setCommand] = useState("");
  const [target, setTarget] = useState(incident.resourceCanonicalId);
  const [verifiedTarget, setVerifiedTarget] = useState(
    incident.resourceCanonicalId,
  );
  const targetResource = useQuery({
    queryKey: ["resource", verifiedTarget],
    enabled: verifiedTarget === target && !!verifiedTarget,
    queryFn: ({ signal }) =>
      checked(
        getResourceByCanonicalID(
          { canonicalId: verifiedTarget },
          requestOptions(signal),
        ),
      ),
  });
  const targetName = list(record(record(targetResource.data).data).nodes).map(
    record,
  )[0]?.name;
  const [profile, setProfile] = useState("");
  const [timeout, setTimeoutSeconds] = useState(60);
  const [maxBytes, setMaxBytes] = useState(1048576);
  const [planInput, setPlanInput] = useState("");
  const [assessment, setAssessment] = useState<Record<string, unknown>>();
  const [ack, setAck] = useState<Record<string, unknown>>();
  const [accepted, setAccepted] = useState(false);
  const [output, setOutput] = useState("");
  const [streamError, setStreamError] = useState<Error>();
  const [truncated, setTruncated] = useState(false);
  const authorizationEpoch = useRef(0);
  const fingerprint = useRef("");
  const submissionKey = useRef(crypto.randomUUID());
  const planId = url.get("actionPlanId") || undefined;
  const executionId = url.get("executionId") || undefined;
  const relatedExecutionId = url.get("relatedExecutionId") || undefined;
  const executionRelation = url.get("executionRelation") || undefined;
  const validRelation =
    executionRelation === "follow_up" || executionRelation === "rollback";
  const invalidRelation =
    !!relatedExecutionId !== !!executionRelation ||
    (!!executionRelation && !validRelation);
  useEffect(() => {
    setCommand("");
  }, [relatedExecutionId, executionRelation]);
  const profileCursor = url.get("profileCursor") || undefined;
  const [selectedConfig, setSelectedConfig] =
    useState<Record<string, unknown>>();
  const changeProfilePage = (cursor?: string) => {
    const n = new URLSearchParams(url);
    if (cursor) n.set("profileCursor", cursor);
    else n.delete("profileCursor");
    setUrl(n);
  };
  const profiles = useQuery({
    queryKey: ["execution-profiles", profileCursor],
    queryFn: ({ signal }) =>
      checked(
        getExecutionProfiles(
          { limit: 50, cursor: profileCursor },
          requestOptions(signal),
        ),
      ),
  });
  const configs = list(record(profiles.data).data).map(record);
  const config =
    configs.find((p) => profileChoice(p) === profile) ??
    (selectedConfig && profileChoice(selectedConfig) === profile
      ? selectedConfig
      : undefined);
  const selectableConfigs =
    config && !configs.some((p) => profileChoice(p) === profileChoice(config))
      ? [config, ...configs]
      : configs;
  const plan = useQuery({
    queryKey: ["action-plan", planId],
    enabled: !!planId,
    queryFn: ({ signal }) =>
      checked(getActionPlan(planId!, requestOptions(signal))),
  });
  const request: CommandRiskAssessmentRequest = {
    ...(relatedExecutionId
      ? {
          relatedExecutionId,
          executionRelation: validRelation ? executionRelation : undefined,
        }
      : {}),
    incidentId: incident.incidentId,
    actionPlanId: planId ?? null,
    actualCommand: command,
    target,
    shell: "bash",
    executionProfileId:
      typeof config?.executionProfileId === "string"
        ? config.executionProfileId
        : "",
    executionProfileVersion: Number(config?.version),
    executionOptions: {
      timeoutSeconds: timeout,
      maxOutputBytes: maxBytes,
      workingDirectory: "",
      environment: {},
    },
  };
  const current = requestFingerprint(request);
  fingerprint.current = current;
  useEffect(() => {
    setAssessment(undefined);
    setAck(undefined);
    setAccepted(false);
    submissionKey.current = crypto.randomUUID();
  }, [current]);
  useEffect(() => {
    const invalidate = () => {
      authorizationEpoch.current++;
      setAssessment(undefined);
      setAck(undefined);
      setAccepted(false);
      stepUp.reset();
      confirm.reset();
      risk.reset();
    };
    authEvents.addEventListener("forbidden", invalidate);
    authEvents.addEventListener("expired", invalidate);
    authEvents.addEventListener("authorizationChanged", invalidate);
    return () => {
      authEvents.removeEventListener("forbidden", invalidate);
      authEvents.removeEventListener("expired", invalidate);
      authEvents.removeEventListener("authorizationChanged", invalidate);
    };
  }, []);
  const risk = useMutation({
    mutationFn: async () => {
      const snapshot = current;
      const epoch = authorizationEpoch.current;
      const response = await checked(
        assessCommandRisk(request, requestOptions(undefined, true)),
      );
      if (!(await matchesAssessment(request, record(record(response).data))))
        throw new Error("风险评估与当前命令或执行范围不一致，请重新评估。");
      return { response, snapshot, epoch };
    },
    onSuccess: ({ response, snapshot, epoch }) => {
      if (
        epoch === authorizationEpoch.current &&
        snapshot === fingerprint.current
      ) {
        setAssessment(record(record(response).data));
        setAck(undefined);
        setAccepted(false);
      }
    },
  });
  const stepUp = useMutation({
    mutationFn: async () => {
      const epoch = authorizationEpoch.current;
      const response = await checked(
        createStepUpSession({}, requestOptions(undefined, true)),
      );
      if (epoch !== authorizationEpoch.current)
        throw new Error("授权已变化，请重新进行强身份验证");
      return response;
    },
  });
  const confirm = useMutation({
    mutationFn: async () => {
      const snapshot = current;
      const epoch = authorizationEpoch.current;
      const response = await checked(
        acknowledgeCommandRisk(
          {
            assessmentId: text(assessment?.assessmentId),
            executionRequestDigest: text(assessment?.executionRequestDigest),
          },
          requestOptions(undefined, true),
        ),
      );
      return { response, snapshot, epoch };
    },
    onSuccess: ({ response, snapshot, epoch }) => {
      if (
        epoch === authorizationEpoch.current &&
        snapshot === fingerprint.current
      )
        setAck(record(record(response).data));
    },
  });
  const submit = useMutation({
    mutationFn: () => {
      const body: CommandExecutionRequest = {
        ...request,
        riskAcknowledgementId: text(ack?.acknowledgementId),
      };
      return checked(
        createCommandExecution(
          body,
          requestOptions(undefined, true, submissionKey.current),
        ),
      );
    },
    onSuccess: (response) => {
      submissionKey.current = crypto.randomUUID();
      setAck(undefined);
      setAccepted(false);
      const id = record(record(response).data).executionId;
      if (typeof id === "string") {
        const n = new URLSearchParams(url);
        n.set("executionId", id);
        setUrl(n);
      }
    },
  });
  const execution = useQuery({
    queryKey: ["command-execution", executionId],
    enabled: !!executionId,
    queryFn: ({ signal }) =>
      checked(getCommandExecution(executionId!, requestOptions(signal))),
    refetchInterval: (q) =>
      ["prepared", "dispatching", "running"].includes(
        text(record(record(q.state.data).data).state),
      )
        ? 3000
        : false,
  });
  const executed = record(record(execution.data).data);
  const cancel = useMutation({
    mutationFn: () =>
      checked(
        cancelCommandExecution(executionId!, requestOptions(undefined, true)),
      ),
    onSuccess: () => void execution.refetch(),
  });
  useEffect(() => {
    if (!executionId) return;
    setOutput("");
    setTruncated(false);
    setStreamError(undefined);
    const controller = new AbortController();
    let cursor: string | undefined;
    let seq = 0n;
    const decoders = { stdout: new TextDecoder(), stderr: new TextDecoder() };
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const stop = () => {
      stopped = true;
      controller.abort();
      if (timer) clearTimeout(timer);
    };
    const revoke = () => {
      stop();
      setOutput("");
      setStreamError(undefined);
    };
    authEvents.addEventListener("expired", revoke);
    authEvents.addEventListener("forbidden", revoke);
    authEvents.addEventListener("authorizationChanged", revoke);
    async function run() {
      for (let attempt = 0; attempt < 8 && !stopped; attempt++) {
        try {
          const options = requestOptions(controller.signal);
          const headers = new Headers(options.headers);
          if (cursor) headers.set("Last-Event-ID", cursor);
          for await (const event of streamJSONEvents(
            getStreamCommandExecutionEventsUrl(executionId!),
            decodeCommandExecutionEvent,
            { ...options, headers },
          )) {
            setStreamError(undefined);
            const n = commandSequence(event.data.eventId, executionId!);
            if (n <= seq) continue;
            if (n !== seq + 1n || !event.id)
              throw new Error("执行 SSE 序列缺口");
            seq = n;
            cursor = event.id;
            const p = record(event.data.payload);
            const chunk =
              event.data.eventType === "output"
                ? (p.stream === "stderr"
                    ? decoders.stderr
                    : decoders.stdout
                  ).decode(base64Bytes(p.bytes), { stream: true })
                : "";
            if (chunk)
              setOutput((old) => {
                if (old.length + chunk.length > 65536) setTruncated(true);
                return boundedText(old + "[" + text(p.stream) + "] " + chunk);
              });
            if (
              event.data.eventType === "state" &&
              [
                "succeeded",
                "failed",
                "cancelled",
                "execution_unknown",
              ].includes(text(p.state))
            ) {
              void execution.refetch();
              return;
            }
          }
        } catch (e) {
          if (stopped) return;
          setStreamError(e as Error);
          if (e instanceof SSEHttpError && [401, 403, 410].includes(e.status)) {
            if (e.status === 401) clearCredentials();
            if (e.status === 403)
              authEvents.dispatchEvent(new Event("forbidden"));
            if (e.status === 410) {
              cursor = undefined;
              seq = 0n;
              setOutput("");
              decoders.stdout = new TextDecoder();
              decoders.stderr = new TextDecoder();
              void execution.refetch();
              continue;
            }
            setOutput("");
            return;
          }
        }
        await abortDelay(
          controller.signal,
          Math.min(10000, 1000 * (attempt + 1)),
        );
      }
    }
    void run();
    return () => {
      stop();
      authEvents.removeEventListener("expired", revoke);
      authEvents.removeEventListener("forbidden", revoke);
      authEvents.removeEventListener("authorizationChanged", revoke);
    };
  }, [executionId]);
  const needsStep = record(assessment?.risk).requiresStepUp === true;
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  const riskValid =
    !!assessment &&
    typeof assessment.expiresAt === "string" &&
    Date.parse(assessment.expiresAt) > now;
  const ackValid =
    !!ack &&
    typeof ack.expiresAt === "string" &&
    Date.parse(ack.expiresAt) > now;
  const unknown = executed.state === "execution_unknown";
  const activeExecution = ["prepared", "dispatching", "running"].includes(
    text(executed.state),
  );
  return (
    <>
      <SuggestionCard value={record(plan.data).data} />
      <details className="technical-details">
        <summary>通过编号查找其他处置建议</summary>
        <Space wrap className="status-note">
          <Input
            aria-label="处置建议编号"
            placeholder="可选：通过建议编号查询"
            value={planInput}
            onChange={(e) => setPlanInput(e.target.value)}
          />
          <Button
            disabled={!planInput}
            onClick={() => {
              const n = new URLSearchParams(url);
              n.set("actionPlanId", planInput);
              setUrl(n);
            }}
          >
            查看只读建议
          </Button>
        </Space>
      </details>
      {plan.error && <QueryState loading={false} error={plan.error} />}
      {relatedExecutionId && (
        <Alert
          showIcon
          type="info"
          title={
            executionRelation === "rollback" ? "准备关联回退" : "准备继续处置"
          }
          description={
            <>
              <p>
                关联执行：{relatedExecutionId}
                。仅允许同一故障、同一目标身份；请重新输入实际命令并确认风险，关联不会授予权限或自动执行。
              </p>
              <Button
                disabled={activeExecution || unknown}
                onClick={() => {
                  const n = new URLSearchParams(url);
                  n.delete("relatedExecutionId");
                  n.delete("executionRelation");
                  n.delete("executionId");
                  n.delete("actionPlanId");
                  setUrl(n);
                }}
              >
                取消关联，开始独立处置
              </Button>
            </>
          }
        />
      )}
      {invalidRelation && (
        <Alert
          type="error"
          title="执行关联参数不完整，请重新选择原执行及关联类型"
        />
      )}
      <h3>1 · 输入实际命令</h3>
      <div className="action-grid">
        <div className="field">
          <label>执行目标</label>
          <p className="target-name">
            {verifiedTarget === target && typeof targetName === "string"
              ? targetName
              : resourceIdentifier(target)}
          </p>
          <details className="technical-details">
            <summary>查看完整标识或更改目标</summary>
            <label htmlFor="command-target">执行目标（完整资源标识）</label>
            <Input
              id="command-target"
              value={target}
              onChange={(e) => setTarget(e.target.value)}
              onBlur={() => setVerifiedTarget(target)}
            />
          </details>
        </div>
        <div className="field">
          <label htmlFor="command-profile">执行配置与精确版本</label>
          <Select
            id="command-profile"
            value={profile || undefined}
            placeholder="选择已授权配置"
            onChange={(value) => {
              setSelectedConfig(
                selectableConfigs.find((p) => profileChoice(p) === value),
              );
              setProfile(value);
            }}
            options={selectableConfigs.map((p) => ({
              value: profileChoice(p),
              label:
                text(p.name ?? "未命名配置") +
                " · 版本 " +
                text(p.version) +
                " · " +
                ((
                  {
                    k8s_namespace: "Kubernetes 命名空间",
                    k8s_cluster: "Kubernetes 集群",
                    ssh: "SSH",
                    ssh_root: "SSH 管理员",
                  } as Record<string, string>
                )[text(p.type)] ?? text(p.type)),
            }))}
          />
          <Space wrap>
            <Button
              disabled={!profileCursor}
              onClick={() => changeProfilePage()}
            >
              最新配置
            </Button>
            <Button
              disabled={!record(record(profiles.data).meta).nextCursor}
              onClick={() =>
                changeProfilePage(
                  text(record(record(profiles.data).meta).nextCursor),
                )
              }
            >
              更多配置版本
            </Button>
          </Space>
        </div>
        <div className="field">
          <label htmlFor="command-timeout">超时（秒）</label>
          <InputNumber
            id="command-timeout"
            value={timeout}
            min={1}
            max={Number(config?.timeoutSeconds || 900)}
            onChange={(v) => setTimeoutSeconds(v ?? 60)}
          />
        </div>
        <div className="field">
          <label htmlFor="command-output">最大输出（字节）</label>
          <InputNumber
            id="command-output"
            value={maxBytes}
            min={1}
            max={Number(config?.maxOutputBytes || 10485760)}
            onChange={(v) => setMaxBytes(v ?? 1048576)}
          />
        </div>
      </div>
      <p className="status-note">
        Shell：Bash · 工作目录：默认空值 · 额外环境变量：无。建议不会自动填入。
      </p>
      <CommandEditor value={command} onChange={setCommand} />
      <QueryState loading={profiles.isPending} error={profiles.error} />
      <Space wrap className="status-note">
        <Button
          type="primary"
          disabled={
            !command.trim() ||
            !profile ||
            !target ||
            unknown ||
            activeExecution ||
            invalidRelation
          }
          loading={risk.isPending}
          onClick={() => risk.mutate()}
        >
          2 · 评估实际命令风险
        </Button>
      </Space>
      {[risk.error, stepUp.error, confirm.error, submit.error, cancel.error]
        .filter(Boolean)
        .map((e, i) => (
          <QueryState key={i} loading={false} error={e} />
        ))}
      {assessment && (
        <>
          <RiskPanel
            targetName={
              verifiedTarget === target && typeof targetName === "string"
                ? targetName
                : undefined
            }
            assessment={assessment}
            request={request}
            profileName={text(config?.name ?? config?.executionProfileId)}
          />
          {!riskValid && (
            <Alert type="warning" title="风险评估已过期，请重新评估" />
          )}
          {needsStep && (
            <Space wrap className="status-note">
              <Button onClick={() => void login(true)}>3 · 重新验证身份</Button>
              <Button
                disabled={auth.session?.acr !== "urn:ops:loa:2"}
                loading={stepUp.isPending}
                onClick={() => stepUp.mutate()}
              >
                登记本次强身份验证
              </Button>
              <span>重新验证身份后，需要重新输入并评估命令。</span>
            </Space>
          )}
          <Checkbox
            checked={accepted}
            disabled={!riskValid}
            onChange={(e) => setAccepted(e.target.checked)}
          >
            本人已核对实际命令、目标、风险与完整执行配置
          </Checkbox>
          <Space wrap className="status-note">
            <Button
              disabled={
                !accepted || !riskValid || (needsStep && !stepUp.isSuccess)
              }
              loading={confirm.isPending}
              onClick={() => confirm.mutate()}
            >
              4 · 本人确认风险
            </Button>
            <Button
              danger
              type="primary"
              disabled={
                !accepted ||
                !riskValid ||
                !ackValid ||
                unknown ||
                activeExecution
              }
              loading={submit.isPending}
              onClick={() => submit.mutate()}
            >
              5 · 提交本人命令
            </Button>
          </Space>
          {ack && !ackValid && (
            <Alert type="warning" title="本人确认已过期，请重新确认" />
          )}
        </>
      )}
      {executionId && (
        <>
          <h3>正式执行记录</h3>
          <QueryState
            loading={execution.isPending}
            error={execution.error}
            retry={() => void execution.refetch()}
          >
            <Status value={executed.state} />
            <Status value={executed.suggestionMatch} />
            <Details value={executed} />
            {unknown && (
              <Alert
                showIcon
                type="error"
                title="执行结果不确定：请核对目标状态，暂勿重复提交"
                description="已禁用新提交。先核对持久输出与目标事实；不能通过重试猜测命令是否已执行。"
              />
            )}
            <Button
              danger
              disabled={!activeExecution}
              loading={cancel.isPending}
              onClick={() => cancel.mutate()}
            >
              取消当前执行
            </Button>
            {streamError && (
              <Alert
                type="warning"
                title="执行实时输出连接中断"
                description={streamError.message}
              />
            )}
            <ExecutionOutput
              output={
                output ||
                (record(executed.extensions).outputPreviews
                  ? archivedPreview(record(executed.extensions).outputPreviews)
                  : "")
              }
              truncated={truncated || executed.outputTruncated === true}
            />
            <PostCheckPanel executionId={executionId} />
          </QueryState>
        </>
      )}
    </>
  );
}

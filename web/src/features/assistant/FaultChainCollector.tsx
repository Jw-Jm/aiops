import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Input,
  Select,
  Space,
  Typography,
} from "antd";
import {
  collectAssistantFaultChain,
  resolveAssistantResource,
} from "../../api/generated/platform";
import { checked, requestOptions } from "../../api/client";
import type {
  AssistantConversationV1,
  AssistantFaultChainRequestV1,
  AssistantTurnV1,
} from "../../api/generated/models";

export function FaultChainCollector({
  conversation,
  disabled,
  epoch,
  onSubmitted,
}: {
  conversation: AssistantConversationV1;
  disabled: boolean;
  // epoch identifies the page scope this collector was mounted for. The page
  // advances it on every scope, conversation, binding or turn change, so a late
  // answer of an older epoch is never accepted.
  epoch: number;
  onSubmitted: (turn: AssistantTurnV1, epoch: number) => void;
}) {
  const [name, setName] = useState("");
  const [kind, setKind] = useState("Pod");
  const [target, setTarget] = useState<string>();
  const [options, setOptions] = useState<{ value: string; label: string }[]>(
    [],
  );
  const [baseline, setBaseline] = useState(true);
  const [from, setFrom] = useState(() =>
    new Date(Date.now() - 600000).toISOString().slice(0, 19),
  );
  const [to, setTo] = useState(() => new Date().toISOString().slice(0, 19));
  const [recoveryFrom, setRecoveryFrom] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  // The request belongs to this mounted collector: when the page switches the
  // conversation or unmounts the collector, the request is cancelled instead of
  // answering for a conversation that is no longer shown. The controller of the
  // running request is captured before the call; the effect replaces the ref
  // with a fresh controller and aborts the captured one.
  const pending = useRef(new AbortController());
  // The live epoch of the mounted scope. A running request compares its own
  // epoch with this one, so an answer, an error or a busy release of an older
  // scope never touches the current state.
  const epochRef = useRef(epoch);
  epochRef.current = epoch;
  useEffect(() => {
    const controller = new AbortController();
    pending.current = controller;
    setBusy(false);
    setError(undefined);
    return () => controller.abort();
    // The state belongs to the mounted scope: a new conversation or a new page
    // epoch starts from a clean state.
  }, [conversation.conversationId, epoch]);
  const binding = conversation.binding.executionScope as
    | { namespaces?: string[]; clusterScoped?: boolean }
    | undefined;
  const namespaces = binding?.namespaces || [];
  const scopeReady =
    kind === "Node"
      ? !!binding?.clusterScoped && namespaces.length <= 1
      : namespaces.length === 1;
  // run executes one request of this collector. The controller of the running
  // request is captured here, so the success, the failure and the busy release
  // all belong to the same request: a request of an older conversation can
  // neither show an error nor release the busy state of the current one.
  async function run(
    action: (owns: () => boolean, signal: AbortSignal) => Promise<void>,
  ) {
    const controller = pending.current;
    const requestEpoch = epochRef.current;
    const owns = () =>
      !controller.signal.aborted &&
      pending.current === controller &&
      requestEpoch === epochRef.current;
    setBusy(true);
    setError(undefined);
    try {
      await action(owns, controller.signal);
    } catch (cause) {
      if (!owns()) return;
      setError(cause instanceof Error ? cause : new Error("调查请求失败"));
    } finally {
      if (owns()) setBusy(false);
    }
  }
  async function collect() {
    await run(async (owns, signal) => {
      if (!target) throw new Error("请先查找并选择真实资源身份。");
      const start = new Date(from + "Z"),
        end = new Date(to + "Z");
      const duration = end.getTime() - start.getTime();
      if (
        !Number.isFinite(duration) ||
        duration <= 0 ||
        duration > 3600000 ||
        end.getTime() > Date.now()
      )
        throw new Error("窗口须有效、已结束且不超过 60 分钟。");
      const timezone = String(conversation.binding.timezone || "Asia/Shanghai");
      const window = (start: Date, end: Date) => ({
        from: start.toISOString(),
        to: end.toISOString(),
        timezone,
      });
      const request: AssistantFaultChainRequestV1 = {
        schemaVersion: "fault-chain-request/v2",
        targetCanonicalId: target,
        fault: window(start, end),
        maxEdges: 4,
      };
      if (baseline)
        request.baseline = window(
          new Date(start.getTime() - 120000 - duration),
          new Date(start.getTime() - 120000),
        );
      if (recoveryFrom) {
        const recovery = new Date(recoveryFrom + "Z");
        request.recovery = window(
          recovery,
          new Date(recovery.getTime() + duration),
        );
      }
      const result = await checked(
        collectAssistantFaultChain(
          conversation.conversationId,
          {
            message: "收集故障链证据",
            expectedConversationRevision: conversation.revision,
            faultChainRequest: request,
          },
          requestOptions(signal, true),
        ),
      );
      if (!owns()) return;
      onSubmitted(result.data, epoch);
    });
  }
  return (
    <Space orientation="vertical" style={{ width: "100%" }}>
      <Typography.Text strong>故障链证据调查</Typography.Text>
      <Typography.Text type="secondary">
        在当前范围内收集观测、比较窗口与检查证据，不需要模型回答。
      </Typography.Text>
      {kind !== "Node" && namespaces.length !== 1 && (
        <Alert type="info" title="请先通过范围编辑选择一个 namespace。" />
      )}
      <Select
        aria-label="调查资源类型"
        value={kind}
        options={[
          { value: "Pod", label: "Pod" },
          { value: "Node", label: "Node" },
          { value: "PersistentVolumeClaim", label: "PVC" },
        ]}
        onChange={(value) => {
          setKind(value);
          setTarget(undefined);
          setOptions([]);
        }}
      />
      {kind === "Node" && !binding?.clusterScoped && (
        <Alert
          type="info"
          title="请通过现有范围编辑器显式选入集群资源；不会自动扩大权限。"
        />
      )}
      <Input
        aria-label="调查资源名称"
        placeholder="调查资源名称"
        value={name}
        onChange={(event) => {
          setName(event.target.value);
          setTarget(undefined);
          setOptions([]);
        }}
      />
      <Button
        disabled={disabled || busy || !name || !scopeReady}
        onClick={() =>
          void run(async (owns, signal) => {
            // The lookup belongs to the same request ownership as the collection:
            // it is cancelled with the conversation and its answer is dropped
            // when the collector no longer owns the state.
            const result = await checked(
              resolveAssistantResource(
                {
                  clusterUid: conversation.clusterUid!,
                  namespaceSelection: namespaces,
                  includeClusterScoped:
                    kind === "Node" && !!binding?.clusterScoped,
                  selector: {
                    kind,
                    name,
                    ...(kind === "Node" ? {} : { namespace: namespaces[0] }),
                  },
                },
                requestOptions(signal),
              ),
            );
            if (!owns()) return;
            setOptions(
              result.data.candidates.map((item) => ({
                value: item.canonicalId,
                label: `${item.namespace}/${item.name}`,
              })),
            );
            setTarget(undefined);
          })
        }
      >
        查找当前授权范围的资源
      </Button>
      <Select
        aria-label="调查目标资源"
        placeholder="选择真实资源"
        value={target}
        onChange={setTarget}
        options={options}
        style={{ width: "100%" }}
      />
      <label>
        故障开始（UTC）
        <Input
          type="datetime-local"
          step={1}
          aria-label="故障开始（UTC）"
          value={from}
          onChange={(event) => setFrom(event.target.value)}
        />
      </label>
      <label>
        故障结束（UTC）
        <Input
          type="datetime-local"
          step={1}
          aria-label="故障结束（UTC）"
          value={to}
          onChange={(event) => setTo(event.target.value)}
        />
      </label>
      <Checkbox
        checked={baseline}
        onChange={(event) => setBaseline(event.target.checked)}
      >
        与前一等长窗口比较，间隔 2 分钟
      </Checkbox>
      <label>
        恢复开始（UTC，可选）
        <Input
          type="datetime-local"
          step={1}
          aria-label="恢复开始（UTC，可选）"
          value={recoveryFrom}
          onChange={(event) => setRecoveryFrom(event.target.value)}
        />
      </label>
      {error && <Alert type="error" title={error.message} />}
      <Button
        disabled={disabled || busy || !target || !scopeReady}
        loading={busy}
        onClick={() => void collect()}
      >
        收集故障链证据
      </Button>
    </Space>
  );
}

import { useState } from "react";
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
  onSubmitted,
}: {
  conversation: AssistantConversationV1;
  disabled: boolean;
  onSubmitted: (turn: AssistantTurnV1) => void;
}) {
  const [name, setName] = useState("");
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
  const binding = conversation.binding.executionScope as
    | { namespaces?: string[] }
    | undefined;
  const namespaces = binding?.namespaces || [];
  async function run(action: () => Promise<void>) {
    setBusy(true);
    setError(undefined);
    try {
      await action();
    } catch (cause) {
      setError(cause instanceof Error ? cause : new Error("调查请求失败"));
    } finally {
      setBusy(false);
    }
  }
  async function collect() {
    await run(async () => {
      if (!target) throw new Error("请先查找并选择真实 Pod 身份。");
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
        schemaVersion: "fault-chain-request/v1",
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
          requestOptions(undefined, true),
        ),
      );
      onSubmitted(result.data);
    });
  }
  return (
    <Space orientation="vertical" style={{ width: "100%" }}>
      <Typography.Text strong>故障链证据调查</Typography.Text>
      <Typography.Text type="secondary">
        在当前范围内收集观测、比较窗口与检查证据，不需要模型回答。
      </Typography.Text>
      {namespaces.length !== 1 && (
        <Alert type="info" title="请先通过范围编辑选择一个 namespace。" />
      )}
      <Input
        aria-label="调查 Pod 名称"
        placeholder="调查 Pod 名称"
        value={name}
        onChange={(event) => {
          setName(event.target.value);
          setTarget(undefined);
          setOptions([]);
        }}
      />
      <Button
        disabled={disabled || busy || !name || namespaces.length !== 1}
        onClick={() =>
          void run(async () => {
            const result = await checked(
              resolveAssistantResource(
                {
                  clusterUid: conversation.clusterUid!,
                  namespaceSelection: namespaces,
                  includeClusterScoped: false,
                  selector: { kind: "Pod", name, namespace: namespaces[0] },
                },
                requestOptions(),
              ),
            );
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
        查找当前授权范围的 Pod
      </Button>
      <Select
        aria-label="调查目标 Pod"
        placeholder="选择真实 Pod"
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
        disabled={disabled || busy || !target || namespaces.length !== 1}
        loading={busy}
        onClick={() => void collect()}
      >
        收集故障链证据
      </Button>
    </Space>
  );
}

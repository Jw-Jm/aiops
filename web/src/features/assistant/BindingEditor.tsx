import { useState } from "react";
import { Alert, Button, Checkbox, Input, Space, Typography } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { checked, requestOptions } from "../../api/client";
import { rebindAssistantConversation } from "../../api/generated/platform";
import type { AssistantConversationV1 } from "../../api/generated/models";

export function BindingEditor({
  conversation,
  running,
  onChanged,
}: {
  conversation: AssistantConversationV1;
  running: boolean;
  onChanged: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [namespace, setNamespace] = useState("");
  const [includeCluster, setIncludeCluster] = useState(false);
  const [target, setTarget] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const client = useQueryClient();
  const start = () => {
    const scope = conversation.binding.executionScope as
      | { namespaces?: string[]; clusterScoped?: boolean }
      | undefined;
    setNamespace(scope?.namespaces?.join(",") || "");
    setIncludeCluster(scope?.clusterScoped === true);
    setTarget("");
    setFrom("");
    setTo("");
    setError(undefined);
    setOpen(!open);
  };
  const save = async () => {
    setBusy(true);
    setError(undefined);
    try {
      if (!!from !== !!to) throw new Error("请同时填写开始和结束时间");
      const window =
        from && to
          ? {
              from: new Date(from + "Z").toISOString(),
              to: new Date(to + "Z").toISOString(),
              timezone: String(conversation.binding.timezone),
            }
          : undefined;
      await checked(
        rebindAssistantConversation(
          conversation.conversationId,
          {
            expectedConversationRevision: conversation.revision,
            namespaceSelection: namespace
              .split(",")
              .map((x) => x.trim())
              .filter(Boolean),
            includeClusterScoped: includeCluster,
            ...(target.trim() ? { resourceCanonicalId: target.trim() } : {}),
            ...(window ? { timeRange: window } : {}),
          },
          requestOptions(undefined, true),
        ),
      );
      setOpen(false);
      onChanged();
      await client.invalidateQueries({ queryKey: ["assistant"] });
    } catch (e) {
      setError(e instanceof Error ? e.message : "调整失败");
    } finally {
      setBusy(false);
    }
  };
  if (conversation.authorizationDomain === "audit") return null;
  return (
    <Space orientation="vertical" style={{ width: "100%" }}>
      <Button
        disabled={running || busy || !!conversation.archivedAt}
        onClick={start}
      >
        调整后续轮次的范围或时间
      </Button>
      {open && (
        <>
          <Typography.Text>
            命名空间（逗号分隔；留空使用当前授权范围）
          </Typography.Text>
          <Input
            aria-label="调整命名空间"
            value={namespace}
            onChange={(e) => setNamespace(e.target.value)}
          />
          <Checkbox
            checked={includeCluster}
            onChange={(e) => setIncludeCluster(e.target.checked)}
          >
            包含当前授权的集群级资源
          </Checkbox>
          {conversation.mode === "troubleshoot" &&
            conversation.binding.resourceContext && (
              <Input
                aria-label="新目标 Canonical ID"
                placeholder="更换目标时填写真实 Canonical ID；留空保留目标"
                value={target}
                onChange={(e) => setTarget(e.target.value)}
              />
            )}
          {(conversation.mode === "data_query" ||
            conversation.binding.resourceContext) && (
            <>
              <Typography.Text>新时间窗（UTC；留空保留原窗口）</Typography.Text>
              <Input
                aria-label="调整开始时间 UTC"
                type="datetime-local"
                value={from}
                onChange={(e) => setFrom(e.target.value)}
              />
              <Input
                aria-label="调整结束时间 UTC"
                type="datetime-local"
                value={to}
                onChange={(e) => setTo(e.target.value)}
              />
            </>
          )}
          <Typography.Text type="secondary">
            提交前将重新检查授权及资源
            UID。已完成轮次保留原始窗口；收窄范围后，范围外历史不进入后续回答。
          </Typography.Text>
          {error && <Alert type="error" title={error} />}
          <Button loading={busy} onClick={() => void save()}>
            确认应用到后续轮次
          </Button>
        </>
      )}
    </Space>
  );
}

import { useEffect, useRef, useState } from "react";
import { Alert, Button, Checkbox, Input, Space, Typography } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError, checked, requestOptions } from "../../api/client";
import { rebindAssistantConversation } from "../../api/generated/platform";
import type { AssistantConversationV1 } from "../../api/generated/models";

// The rebind intent is frozen when the editor opens: the conversation and the
// revision that the user saw. A later navigation or a background refresh must
// not move the adjustment onto another conversation nor overwrite a change of
// somebody else.
type RebindIntent = {
  conversationId: string;
  revision: number;
  idempotencyKey: string;
  scopeKey: string;
};

export function BindingEditor({
  conversation,
  running,
  onChanged,
  scopeKey,
}: {
  conversation: AssistantConversationV1;
  running: boolean;
  onChanged: () => void;
  // The identity and the authorization scope the editor was opened under. The
  // editor belongs to one conversation *and* one scope: a cached conversation
  // of the same id under another account or another scope is another editor.
  scopeKey: string;
}) {
  const [open, setOpen] = useState(false);
  const [namespace, setNamespace] = useState("");
  const [includeCluster, setIncludeCluster] = useState(false);
  const [target, setTarget] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [intent, setIntent] = useState<RebindIntent>();
  const abort = useRef(new AbortController());
  const busyRef = useRef(false);
  const client = useQueryClient();
  // The editor belongs to one conversation and one authorization scope.
  // Another conversation, another account or another scope replaces the draft
  // and drops a request that is still in flight.
  useEffect(() => {
    abort.current.abort();
    abort.current = new AbortController();
    busyRef.current = false;
    setBusy(false);
    setOpen(false);
    setIntent(undefined);
    setNamespace("");
    setIncludeCluster(false);
    setTarget("");
    setFrom("");
    setTo("");
    setError(undefined);
  }, [conversation.conversationId, scopeKey]);
  useEffect(() => () => abort.current.abort(), []);
  const start = () => {
    const scope = conversation.binding.executionScope as
      | { namespaces?: string[]; clusterScoped?: boolean }
      | undefined;
    abort.current.abort();
    abort.current = new AbortController();
    setNamespace(scope?.namespaces?.join(",") || "");
    setIncludeCluster(scope?.clusterScoped === true);
    setTarget("");
    setFrom("");
    setTo("");
    setError(undefined);
    setIntent({
      conversationId: conversation.conversationId,
      revision: conversation.revision,
      idempotencyKey: crypto.randomUUID(),
      scopeKey,
    });
    setOpen(!open);
  };
  const save = async () => {
    const frozen = intent;
    // A second submit of the same intent would either duplicate the request
    // or conflict with the first one.
    if (!frozen || busyRef.current) return;
    // The intent belongs to the scope it was opened under. A scope change
    // drops it before the request is sent.
    if (frozen.scopeKey !== scopeKey) return;
    const signal = abort.current.signal;
    busyRef.current = true;
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
          frozen.conversationId,
          {
            expectedConversationRevision: frozen.revision,
            namespaceSelection: namespace
              .split(",")
              .map((x) => x.trim())
              .filter(Boolean),
            includeClusterScoped: includeCluster,
            ...(target.trim() ? { resourceCanonicalId: target.trim() } : {}),
            ...(window ? { timeRange: window } : {}),
          },
          requestOptions(signal, true, frozen.idempotencyKey),
        ),
      );
      if (signal.aborted || abort.current.signal !== signal) return;
      setOpen(false);
      setIntent(undefined);
      onChanged();
      await client.invalidateQueries({ queryKey: ["assistant"] });
    } catch (e) {
      if (signal.aborted || abort.current.signal !== signal) return;
      if (e instanceof ApiError && [401, 403].includes(e.status)) {
        client.removeQueries({ queryKey: ["assistant"] });
      }
      setError(
        e instanceof ApiError && e.status === 409
          ? "会话已被其他操作修改，请刷新后重试"
          : e instanceof ApiError && e.status === 403
            ? "没有调整此会话范围的权限"
            : e instanceof Error
              ? e.message
              : "调整失败",
      );
    } finally {
      if (abort.current.signal === signal) {
        busyRef.current = false;
        setBusy(false);
      }
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
          <Button loading={busy} disabled={busy} onClick={() => void save()}>
            确认应用到后续轮次
          </Button>
        </>
      )}
    </Space>
  );
}

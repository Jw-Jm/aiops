import { useEffect, useRef, useState } from "react";
import { Button, Input, Table } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "../../auth/AuthProvider";
import {
  useClusterDraft,
  useOperationalCluster,
} from "../../app/ClusterContext";
import { checked, requestOptions } from "../../api/client";
import {
  createIncidentCaseDraft,
  getIncidentCaseDraftVersion,
  listIncidentCaseDrafts,
} from "../../api/generated/platform";
import type { IncidentCaseDraftCreateV1 } from "../../api/generated/models";
import { Field, Note } from "../../components/v12/primitives";
import { V12Drawer, V12Modal } from "../../components/v12/Layer";
import { QueryState } from "../../components/QueryState";

type Props = {
  incidentId: string;
  revision: number;
  state: string;
  canonicalId: string;
};

export function IncidentCaseDrafts(props: Props) {
  const { scopeKey } = useOperationalCluster();
  const auth = useAuth();
  const identity = `${scopeKey}:${auth.session?.subject}:${auth.nativeSession?.sessionRef ?? ""}:${props.incidentId}`;
  return (
    <CaseDrafts
      key={identity}
      {...props}
      identity={identity}
      canWrite={!!auth.session?.roles.includes("operator")}
    />
  );
}

function CaseDrafts({
  incidentId,
  revision,
  state,
  canonicalId,
  identity,
  canWrite,
}: Props & { identity: string; canWrite: boolean }) {
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const [cursor, setCursor] = useState("");
  const [selected, setSelected] = useState("");
  const [input, setInput] = useState<IncidentCaseDraftCreateV1>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const active = useRef<AbortController | undefined>(undefined);
  const submitted = useRef<{ payload: string; key: string } | undefined>(
    undefined,
  );
  useEffect(() => () => active.current?.abort(), []);
  useClusterDraft(open);
  const key = ["incident-case-drafts", identity];
  const drafts = useQuery({
    queryKey: [...key, cursor],
    enabled: canWrite,
    queryFn: ({ signal }) =>
      checked(
        listIncidentCaseDrafts(
          { incidentId, cursor: cursor || undefined },
          requestOptions(signal),
        ),
      ),
  });
  const detail = useQuery({
    queryKey: ["incident-case-draft", identity, selected],
    enabled: !!selected && canWrite,
    queryFn: async ({ signal }) => {
      const out = await checked(
        getIncidentCaseDraftVersion(selected, 1, requestOptions(signal)),
      );
      if (out.data.incidentId !== incidentId)
        throw new Error("案例来源与当前故障不一致");
      return out;
    },
  });
  if (!canWrite) return null;
  function begin() {
    setInput({
      kind: "case",
      incidentId,
      expectedRevision: revision,
      title: `故障 ${incidentId} · 案例`,
      summary: `关联故障 ${incidentId}，结论与恢复情况待核对。`,
      content: `关联故障：${incidentId}\n记录版本：${revision}\n当前状态：${state}\n资源：${canonicalId}\n\n事实与证据\n请按引用版本核对。\n\n假设与缺口\n尚未验证的内容保持未知。\n\n处置与恢复验证\n请人工补充结果，不以退出码代替业务恢复。`,
    });
    setError(undefined);
    setOpen(true);
  }
  async function save() {
    if (!input || active.current) return;
    const controller = new AbortController();
    active.current = controller;
    const payload = JSON.stringify(input);
    if (submitted.current?.payload !== payload)
      submitted.current = { payload, key: crypto.randomUUID() };
    setBusy(true);
    setError(undefined);
    try {
      const out = await checked(
        createIncidentCaseDraft(
          input,
          requestOptions(controller.signal, true, submitted.current.key),
        ),
      );
      if (controller.signal.aborted) return;
      setOpen(false);
      setSelected(out.data.documentId);
      await client.invalidateQueries({ queryKey: key });
    } catch (failure) {
      if (!controller.signal.aborted) setError(failure as Error);
    } finally {
      if (!controller.signal.aborted) setBusy(false);
      if (active.current === controller) active.current = undefined;
    }
  }
  return (
    <section className="section" aria-label="故障案例草稿">
      <Button onClick={begin}>整理为案例</Button>
      <QueryState
        loading={drafts.isPending}
        error={drafts.error}
        retry={() => void drafts.refetch()}
      >
        {!!drafts.data?.data.length && (
          <Table
            size="small"
            pagination={false}
            rowKey="documentId"
            dataSource={drafts.data.data}
            columns={[
              { title: "案例草稿", dataIndex: "title" },
              { title: "来源版本", dataIndex: "incidentRevision" },
              {
                title: "操作",
                render: (_, draft) => (
                  <Button onClick={() => setSelected(draft.documentId)}>
                    查看草稿 v1
                  </Button>
                ),
              },
            ]}
          />
        )}
        {cursor && <Button onClick={() => setCursor("")}>案例第一页</Button>}
        {!!drafts.data?.meta.nextCursor && (
          <Button onClick={() => setCursor(drafts.data!.meta.nextCursor)}>
            下一页案例
          </Button>
        )}
      </QueryState>
      <V12Modal
        open={open}
        title="新建知识草稿"
        onClose={() => {
          if (!busy) setOpen(false);
        }}
        closeDisabled={busy}
        footer={
          <>
            <Button disabled={busy} onClick={() => setOpen(false)}>
              取消
            </Button>
            <Button
              type="primary"
              loading={busy}
              disabled={
                !input?.title.trim() ||
                !input.summary.trim() ||
                !input.content.trim()
              }
              onClick={() => void save()}
            >
              保存草稿
            </Button>
          </>
        }
      >
        {input && (
          <>
            <Field label="标题">
              <Input
                aria-label="案例标题"
                maxLength={160}
                value={input.title}
                onChange={(e) => setInput({ ...input, title: e.target.value })}
              />
            </Field>
            <Field label="内容摘要">
              <Input
                aria-label="案例内容摘要"
                maxLength={500}
                value={input.summary}
                onChange={(e) =>
                  setInput({ ...input, summary: e.target.value })
                }
              />
            </Field>
            <Field label="正文与适用边界">
              <Input.TextArea
                aria-label="案例正文与适用边界"
                rows={12}
                maxLength={16000}
                value={input.content}
                onChange={(e) =>
                  setInput({ ...input, content: e.target.value })
                }
              />
            </Field>
            <Note>
              保存为故障案例草稿 v1。请核对事实、固定证据版本与恢复情况。
            </Note>
          </>
        )}
        {error && <QueryState loading={false} error={error} />}
      </V12Modal>
      <V12Drawer
        open={!!selected}
        title="故障案例草稿 v1"
        onClose={() => setSelected("")}
      >
        <QueryState
          loading={detail.isPending}
          error={detail.error}
          retry={() => void detail.refetch()}
        >
          {detail.data && (
            <article>
              <h3>{detail.data.data.title}</h3>
              <p>{detail.data.data.summary}</p>
              <p>
                来源故障 {detail.data.data.incidentId} · 记录版本{" "}
                {detail.data.data.incidentRevision} · 草稿
              </p>
              <div style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
                {detail.data.data.content}
              </div>
            </article>
          )}
        </QueryState>
      </V12Drawer>
    </section>
  );
}

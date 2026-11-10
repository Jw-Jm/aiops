import { useEffect, useRef, useState } from "react";
import { Button, Input, Table } from "antd";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  claimIncident,
  completeIncidentFollowUp,
  createIncidentComment,
  createIncidentFollowUp,
  listIncidentFollowUps,
  reopenIncidentFollowUp,
  transferIncident,
} from "../../api/collab";
import { getIncidentTimeline } from "../../api/generated/platform";
import { checked, requestOptions } from "../../api/client";
import { Field, Note } from "../../components/v12/primitives";
import { V12Modal } from "../../components/v12/Layer";
import { QueryState } from "../../components/QueryState";
import { record, list, text, displayTime } from "../../components/data";

type DialogMode = "assign" | "comment" | "task" | null;

// 故障协作卡片：当前处理人、认领 / 分派转交、处理意见与业务影响、复盘待办。
// 布局与交互对照原型 vcollab 面板（slices SL-256~259）：认领是直接操作，
// 分派转交 / 记录意见 / 添加待办是对话框；评论与待办不改变故障事实。
export function IncidentCollaboration({
  incidentId,
  state,
  revision,
  assigneeSubject,
  currentSubject,
  isAdmin,
  onChanged,
}: {
  incidentId: string;
  state: string;
  revision: number;
  assigneeSubject: string;
  currentSubject: string;
  isAdmin: boolean;
  onChanged: () => void | Promise<void>;
}) {
  const [dialog, setDialog] = useState<DialogMode>(null);
  const active = useRef(true);
  const controller = useRef(new AbortController());
  useEffect(() => {
    active.current = true;
    controller.current = new AbortController();
    return () => {
      active.current = false;
      controller.current.abort();
    };
  }, []);
  const options = () => requestOptions(controller.current.signal, true);

  const [commentCursor, setCommentCursor] = useState<string>();
  const [taskCursor, setTaskCursor] = useState<string>();
  const comments = useQuery({
    queryKey: ["incident-collab-comments", incidentId, commentCursor],
    queryFn: async ({ signal }) =>
      checked(
        getIncidentTimeline(
          incidentId,
          { limit: 50, eventType: "comment", cursor: commentCursor },
          requestOptions(signal),
        ),
      ),
  });
  const followUps = useQuery({
    queryKey: ["incident-followups", incidentId, taskCursor],
    queryFn: async ({ signal }) =>
      checked(
        listIncidentFollowUps(
          incidentId,
          { limit: 50, cursor: taskCursor },
          requestOptions(signal),
        ),
      ),
  });

  const claim = useMutation({
    mutationFn: () =>
      checked(
        claimIncident(incidentId, { expectedRevision: revision }, options()),
        false,
        () => active.current,
      ),
    onSuccess: () => (active.current ? onChanged() : undefined),
  });
  const transfer = useMutation({
    mutationFn: (v: { toSubject: string; reason: string }) =>
      checked(
        transferIncident(
          incidentId,
          { expectedRevision: revision, ...v },
          options(),
        ),
        false,
        () => active.current,
      ),
    onSuccess: async () => {
      if (active.current) {
        setDialog(null);
        await onChanged();
      }
    },
  });
  const comment = useMutation({
    mutationFn: (v: { body: string; impact: string }) =>
      checked(
        createIncidentComment(
          incidentId,
          { body: v.body, impact: v.impact || undefined },
          options(),
        ),
        false,
        () => active.current,
      ),
    onSuccess: async () => {
      if (active.current) {
        setDialog(null);
        await Promise.all([onChanged(), comments.refetch()]);
      }
    },
  });
  const createTask = useMutation({
    mutationFn: (v: { title: string; ownerSubject: string }) =>
      checked(
        createIncidentFollowUp(
          incidentId,
          { title: v.title, ownerSubject: v.ownerSubject || undefined },
          options(),
        ),
        false,
        () => active.current,
      ),
    onSuccess: async () => {
      if (active.current) {
        setDialog(null);
        await followUps.refetch();
      }
    },
  });
  const completeTask = useMutation({
    mutationFn: (v: { followUpId: string; note: string }) =>
      checked(
        completeIncidentFollowUp(
          incidentId,
          v.followUpId,
          { note: v.note },
          options(),
        ),
        false,
        () => active.current,
      ),
    onSuccess: () => (active.current ? followUps.refetch() : undefined),
  });
  const reopenTask = useMutation({
    mutationFn: (followUpId: string) =>
      checked(
        reopenIncidentFollowUp(incidentId, followUpId, options()),
        false,
        () => active.current,
      ),
    onSuccess: () => (active.current ? followUps.refetch() : undefined),
  });

  const closed = state === "closed";
  const commentRows = comments.error ? [] : list(record(comments.data).data);
  const latestImpact = (() => {
    for (let i = commentRows.length - 1; i >= 0; i--) {
      const impact = record(record(commentRows[i]).payload).impact;
      if (typeof impact === "string" && impact.trim()) return impact;
    }
    return "";
  })();
  const taskRows = followUps.error ? [] : list(record(followUps.data).data);
  const mayTransfer =
    !closed &&
    assigneeSubject !== "" &&
    (assigneeSubject === currentSubject || isAdmin);
  const dialogBusy =
    transfer.isPending || comment.isPending || createTask.isPending;

  return (
    <section className="section" aria-label="故障协作">
      <h2>故障协作</h2>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 12,
          flexWrap: "wrap",
        }}
      >
        <span className="muted small">当前处理人</span>
        <strong>{assigneeSubject || "尚未分派"}</strong>
        <span style={{ flex: 1 }} />
        <Button
          size="small"
          disabled={closed || !!assigneeSubject}
          loading={claim.isPending}
          onClick={() => claim.mutate()}
        >
          认领
        </Button>
        <Button
          size="small"
          disabled={!mayTransfer}
          onClick={() => setDialog("assign")}
        >
          分派 / 转交
        </Button>
      </div>
      <p className="small sub space">
        业务影响（本页最近记录）：{latestImpact || "待评估"}
      </p>
      <QueryState
        loading={comments.isPending}
        error={comments.error}
        retry={() => void comments.refetch()}
      />
      <QueryState
        loading={followUps.isPending}
        error={followUps.error}
        retry={() => void followUps.refetch()}
      />
      {commentRows.length > 0 && (
        <Table
          rowKey={(r) => text(record(r).timelineId)}
          dataSource={commentRows}
          pagination={false}
          size="small"
          columns={[
            {
              title: "时间 / 记录人",
              width: 220,
              render: (_, r) =>
                `${displayTime(record(r).createdAt)} · ${text(record(r).actor)}`,
            },
            {
              title: "处理意见",
              render: (_, r) => text(record(record(r).payload).text),
            },
          ]}
        />
      )}
      <div>
        <Button
          disabled={!commentCursor}
          onClick={() => setCommentCursor(undefined)}
        >
          意见第一页
        </Button>
        <Button
          disabled={!record(record(comments.data).meta).nextCursor}
          onClick={() =>
            setCommentCursor(
              String(record(record(comments.data).meta).nextCursor),
            )
          }
        >
          下一页意见
        </Button>
      </div>
      {taskRows.length > 0 && (
        <Table
          rowKey={(r) => text(record(r).followUpId)}
          dataSource={taskRows}
          pagination={false}
          size="small"
          columns={[
            {
              title: "复盘待办",
              render: (_, r) => <strong>{text(record(r).title)}</strong>,
            },
            {
              title: "责任人",
              width: 160,
              render: (_, r) =>
                text(record(r).ownerSubject) || text(record(r).creatorSubject),
            },
            {
              title: "状态",
              width: 110,
              render: (_, r) =>
                text(record(r).state) === "done"
                  ? "已完成"
                  : text(record(r).state) === "cancelled"
                    ? "已取消"
                    : "进行中",
            },
            {
              title: "操作",
              width: 260,
              render: (_, r) => {
                const followUpId = text(record(r).followUpId);
                const done = text(record(r).state) === "done";
                const mayAct =
                  text(record(r).creatorSubject) === currentSubject ||
                  text(record(r).ownerSubject) === currentSubject ||
                  assigneeSubject === currentSubject ||
                  isAdmin;
                if (!mayAct || closed) return "-";
                return done ? (
                  <Button
                    size="small"
                    disabled={reopenTask.isPending}
                    loading={
                      reopenTask.isPending &&
                      reopenTask.variables === followUpId
                    }
                    onClick={() => reopenTask.mutate(followUpId)}
                  >
                    重新打开
                  </Button>
                ) : (
                  <CompleteTaskForm
                    disabled={completeTask.isPending}
                    busy={
                      completeTask.isPending &&
                      completeTask.variables?.followUpId === followUpId
                    }
                    onComplete={(note) =>
                      completeTask.mutate({ followUpId, note })
                    }
                  />
                );
              },
            },
          ]}
        />
      )}
      <div>
        <Button disabled={!taskCursor} onClick={() => setTaskCursor(undefined)}>
          待办第一页
        </Button>
        <Button
          disabled={!record(record(followUps.data).meta).nextCursor}
          onClick={() =>
            setTaskCursor(
              String(record(record(followUps.data).meta).nextCursor),
            )
          }
        >
          下一页待办
        </Button>
      </div>
      <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
        <Button
          size="small"
          type="text"
          disabled={closed}
          onClick={() => setDialog("comment")}
        >
          记录意见 / 影响
        </Button>
        <Button
          size="small"
          type="text"
          disabled={closed}
          onClick={() => setDialog("task")}
        >
          添加复盘待办
        </Button>
      </div>
      {claim.error && (
        <QueryState loading={false} error={claim.error} retry={onChanged} />
      )}
      {transfer.error && (
        <QueryState loading={false} error={transfer.error} retry={onChanged} />
      )}
      {comment.error && (
        <QueryState
          loading={false}
          error={comment.error}
          retry={() => void 0}
        />
      )}
      {createTask.error && (
        <QueryState
          loading={false}
          error={createTask.error}
          retry={() => void 0}
        />
      )}
      {completeTask.error && (
        <QueryState
          loading={false}
          error={completeTask.error}
          retry={() => void 0}
        />
      )}
      {reopenTask.error && (
        <QueryState
          loading={false}
          error={reopenTask.error}
          retry={() => void 0}
        />
      )}

      <V12Modal
        open={dialog === "assign"}
        title="分派 / 转交故障"
        onClose={() => setDialog(null)}
      >
        <AssignForm busy={dialogBusy} onSubmit={(v) => transfer.mutate(v)} />
      </V12Modal>
      <V12Modal
        open={dialog === "comment"}
        title="记录处理意见"
        onClose={() => setDialog(null)}
      >
        <CommentForm
          busy={dialogBusy}
          initialImpact={latestImpact}
          onSubmit={(v) => comment.mutate(v)}
        />
      </V12Modal>
      <V12Modal
        open={dialog === "task"}
        title="添加复盘待办"
        onClose={() => setDialog(null)}
      >
        <TaskForm
          busy={dialogBusy}
          defaultOwner={assigneeSubject || currentSubject}
          onSubmit={(v) => createTask.mutate(v)}
        />
      </V12Modal>
    </section>
  );
}

function AssignForm({
  busy,
  onSubmit,
}: {
  busy: boolean;
  onSubmit: (v: { toSubject: string; reason: string }) => void;
}) {
  const [toSubject, setToSubject] = useState("");
  const [reason, setReason] = useState("");
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (toSubject.trim() && reason.trim())
          onSubmit({ toSubject: toSubject.trim(), reason: reason.trim() });
      }}
      style={{ display: "grid", gap: 14 }}
    >
      <Field label="处理人" htmlFor="collab-owner">
        <Input
          id="collab-owner"
          aria-label="处理人"
          placeholder="接手人的账号主体（需具备该故障的处理权限）"
          maxLength={256}
          value={toSubject}
          disabled={busy}
          onChange={(e) => setToSubject(e.target.value)}
        />
      </Field>
      <Field label="分派或转交原因" htmlFor="collab-assign-reason">
        <Input.TextArea
          id="collab-assign-reason"
          aria-label="分派或转交原因"
          rows={3}
          maxLength={1000}
          value={reason}
          disabled={busy}
          onChange={(e) => setReason(e.target.value)}
        />
      </Field>
      <Note>
        转交只更换处理人，不改变故障状态与恢复事实；接手人须具备当前故障范围的处理权限。
      </Note>
      <Button
        type="primary"
        htmlType="submit"
        disabled={busy || !toSubject.trim() || !reason.trim()}
        loading={busy}
      >
        保存记录
      </Button>
    </form>
  );
}

function CommentForm({
  busy,
  initialImpact,
  onSubmit,
}: {
  busy: boolean;
  initialImpact: string;
  onSubmit: (v: { body: string; impact: string }) => void;
}) {
  const [body, setBody] = useState("");
  const [impact, setImpact] = useState(initialImpact);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (body.trim()) onSubmit({ body: body.trim(), impact: impact.trim() });
      }}
      style={{ display: "grid", gap: 14 }}
    >
      <Field label="处理意见" htmlFor="collab-comment">
        <Input.TextArea
          id="collab-comment"
          aria-label="处理意见"
          rows={4}
          maxLength={4000}
          value={body}
          disabled={busy}
          onChange={(e) => setBody(e.target.value)}
        />
      </Field>
      <Field
        label="业务影响"
        htmlFor="collab-impact"
        help="留空沿用最近一次评估；填写后作为当前业务影响展示。"
      >
        <Input
          id="collab-impact"
          aria-label="业务影响"
          maxLength={1000}
          value={impact}
          disabled={busy}
          onChange={(e) => setImpact(e.target.value)}
        />
      </Field>
      <Button
        type="primary"
        htmlType="submit"
        disabled={busy || !body.trim()}
        loading={busy}
      >
        保存记录
      </Button>
    </form>
  );
}

function TaskForm({
  busy,
  defaultOwner,
  onSubmit,
}: {
  busy: boolean;
  defaultOwner: string;
  onSubmit: (v: { title: string; ownerSubject: string }) => void;
}) {
  const [title, setTitle] = useState("");
  const [owner, setOwner] = useState(defaultOwner);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (title.trim())
          onSubmit({ title: title.trim(), ownerSubject: owner.trim() });
      }}
      style={{ display: "grid", gap: 14 }}
    >
      <Field label="复盘待办" htmlFor="collab-task">
        <Input.TextArea
          id="collab-task"
          aria-label="复盘待办"
          rows={3}
          maxLength={200}
          value={title}
          disabled={busy}
          onChange={(e) => setTitle(e.target.value)}
        />
      </Field>
      <Field
        label="责任人"
        htmlFor="collab-task-owner"
        help="默认当前处理人；填写其他主体时需具备该故障的处理权限。"
      >
        <Input
          id="collab-task-owner"
          aria-label="责任人"
          maxLength={256}
          value={owner}
          disabled={busy}
          onChange={(e) => setOwner(e.target.value)}
        />
      </Field>
      <Button
        type="primary"
        htmlType="submit"
        disabled={busy || !title.trim()}
        loading={busy}
      >
        保存记录
      </Button>
    </form>
  );
}

function CompleteTaskForm({
  disabled,
  busy,
  onComplete,
}: {
  disabled: boolean;
  busy: boolean;
  onComplete: (note: string) => void;
}) {
  const [note, setNote] = useState("");
  return (
    <span style={{ display: "inline-flex", gap: 8 }}>
      <Input
        aria-label="完成说明"
        placeholder="完成说明"
        size="small"
        maxLength={1000}
        style={{ width: 160 }}
        value={note}
        disabled={disabled}
        onChange={(e) => setNote(e.target.value)}
      />
      <Button
        size="small"
        disabled={disabled || !note.trim()}
        loading={busy}
        onClick={() => onComplete(note.trim())}
      >
        完成
      </Button>
    </span>
  );
}

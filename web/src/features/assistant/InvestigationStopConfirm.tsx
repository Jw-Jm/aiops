import { Alert, Modal, Space, Typography } from "antd";

// A stop confirmation is bound to the exact work it was opened for. The
// target is frozen when the dialog opens: the job, the turn, the
// conversation, the conversation revision and the subject generation. A
// later navigation, a new turn or a new subject can therefore never make
// the confirmation stop another job.
export type StopTarget = {
  jobId: string;
  turnId: string;
  conversationId: string;
  conversationRevision: number;
  subject: string;
  tenantId: string;
  scopeKey: string;
  epoch: number;
  // The idempotency key of this confirmation. It is frozen when the
  // confirmation opens, so a repeated confirmation of the same target can
  // never cancel a second job under a new key.
  idempotencyKey: string;
};

export function freezeStopTarget(input: StopTarget): StopTarget {
  return {
    jobId: input.jobId,
    turnId: input.turnId,
    conversationId: input.conversationId,
    conversationRevision: input.conversationRevision,
    subject: input.subject,
    tenantId: input.tenantId,
    scopeKey: input.scopeKey,
    epoch: input.epoch,
    idempotencyKey: input.idempotencyKey,
  };
}

// The confirmation may only act on the work it was opened for. A changed
// subject, a changed scope, another conversation or a new page generation
// refuses the request instead of cancelling something else.
export function stopTargetMatches(
  target: StopTarget,
  current: {
    subject: string;
    scopeKey: string;
    conversationId: string;
    epoch: number;
  },
): boolean {
  return (
    target.subject === current.subject &&
    target.scopeKey === current.scopeKey &&
    target.conversationId === current.conversationId &&
    target.epoch === current.epoch
  );
}

// The dialog itself never sends a request. Opening it only freezes the
// target, so a confirmation that is opened and abandoned cancels nothing.
export function InvestigationStopConfirm({
  target,
  confirming = false,
  error,
  onCancel,
  onConfirm,
}: {
  target: StopTarget;
  confirming?: boolean;
  error?: unknown;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <Modal
      title="确认停止本轮调查"
      open
      onCancel={onCancel}
      okText="确认停止"
      cancelText="继续运行"
      okButtonProps={{ danger: true, loading: confirming }}
      onOk={onConfirm}
    >
      <Space orientation="vertical" size="small" style={{ width: "100%" }}>
        <Alert
          type="warning"
          title="停止后已提交的事实会保留，但未确认的根因不会成立。"
        />
        <Typography.Text type="secondary">
          停止请求只作用于下面这份已冻结的目标，不会影响其它调查。
        </Typography.Text>
        <ul aria-label="已冻结的停止目标">
          <li>调查作业：{target.jobId}</li>
          <li>轮次：{target.turnId}</li>
          <li>会话：{target.conversationId}</li>
          <li>会话版本：{target.conversationRevision}</li>
        </ul>
        {error ? (
          <Alert
            type="error"
            title={
              error instanceof Error ? error.message : "停止请求未被接受"
            }
          />
        ) : null}
      </Space>
    </Modal>
  );
}

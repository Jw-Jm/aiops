import { useState } from "react";
import { Alert, Button, Card, Input, Popconfirm, Space } from "antd";
import { checked, requestOptions } from "../../api/client";
import { setAssistantLegalHold } from "../../api/generated/platform";

export function LegalHoldControl() {
  const [turnId, setTurnId] = useState("");
  const [reason, setReason] = useState("");
  const [pending, setPending] = useState<"hold" | "release">();
  const [status, setStatus] = useState<{ ok: boolean; message: string }>();
  async function apply(held: boolean) {
    setPending(held ? "hold" : "release");
    setStatus(undefined);
    try {
      await checked(
        setAssistantLegalHold(
          { turnId: turnId.trim(), reason: reason.trim(), held },
          requestOptions(undefined, true),
        ),
      );
      setStatus({
        ok: true,
        message: held
          ? "法律保留已设置；关联正文、结果和历史依赖不会被普通 TTL 清理。"
          : "法律保留已解除；其他正式证据或历史依赖保护仍会生效。",
      });
    } catch (error) {
      setStatus({
        ok: false,
        message: error instanceof Error ? error.message : "操作失败",
      });
    } finally {
      setPending(undefined);
    }
  }
  return (
    <Card title="会话法律保留" size="small">
      <Space orientation="vertical" style={{ width: "100%" }}>
        <Input
          aria-label="保留轮次 ID"
          placeholder="由审计流程提供的轮次 UUID"
          value={turnId}
          onChange={(e) => setTurnId(e.target.value)}
        />
        <Input.TextArea
          aria-label="保留原因"
          placeholder="设置或解除原因（写入审计）"
          maxLength={1024}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
        />
        <Space>
          <Button
            disabled={!turnId.trim() || !reason.trim() || !!pending}
            loading={pending === "hold"}
            onClick={() => void apply(true)}
          >
            设置保留
          </Button>
          <Popconfirm
            title="解除法律保留？"
            description="解除后该轮次不再受法律保留保护，可能被普通 TTL 清理。"
            okText="解除保留"
            cancelText="取消"
            onConfirm={() => void apply(false)}
          >
            <Button
              danger
              disabled={!turnId.trim() || !reason.trim() || !!pending}
              loading={pending === "release"}
            >
              解除保留
            </Button>
          </Popconfirm>
        </Space>
        {status && (
          <Alert
            type={status.ok ? "success" : "error"}
            title={status.message}
          />
        )}
      </Space>
    </Card>
  );
}

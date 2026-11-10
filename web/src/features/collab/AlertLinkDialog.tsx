import { useEffect, useRef, useState } from "react";
import { Button, Input, Select } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { linkIncidentAlerts } from "../../api/collab";
import { getIncident, getIncidents } from "../../api/generated/platform";
import { checked, requestOptions } from "../../api/client";
import { Field, Note } from "../../components/v12/primitives";
import { V12Modal } from "../../components/v12/Layer";
import { record, list, text } from "../../components/data";

// 关联 / 纠正故障：对照原型 v11-alert-link 对话框。保存前需勾选核对确认；
// 保存只调整关联关系，不改变来源告警的触发 / 恢复状态。
export function AlertLinkDialog({
  open,
  findingId,
  currentIncidentId,
  clusterUid,
  onClose,
}: {
  open: boolean;
  findingId: string;
  currentIncidentId: string;
  clusterUid: string;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [target, setTarget] = useState<string | undefined>(undefined);
  const [reason, setReason] = useState("");
  const [reviewed, setReviewed] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const active = useRef(true);
  const controller = useRef(new AbortController());
  useEffect(() => {
    active.current = true;
    controller.current = new AbortController();
    return () => {
      active.current = false;
      controller.current.abort();
    };
  }, [open]);
  const incidents = useQuery({
    queryKey: ["incidents", clusterUid],
    enabled: open,
    queryFn: async ({ signal }) =>
      checked(
        getIncidents(
          { clusterUid, limit: 50, state: "open" },
          requestOptions(signal),
        ),
      ),
  });
  const targetRevision = useQuery({
    queryKey: ["incident", target],
    enabled: !!target,
    queryFn: async ({ signal }) =>
      checked(getIncident(target!, requestOptions(signal))),
  });
  const link = useMutation({
    mutationFn: async () => {
      if (!target) throw new Error("请选择要关联的故障。");
      const revision = Number(
        record(record(targetRevision.data).data).revision,
      );
      if (!revision) throw new Error("请先刷新目标故障的版本后重试。");
      return checked(
        linkIncidentAlerts(
          target,
          {
            expectedRevision: revision,
            findingIds: [findingId],
            action: "link",
            reason: reason.trim(),
          },
          requestOptions(controller.current.signal, true),
        ),
        false,
        () => active.current,
      );
    },
    onSuccess: async () => {
      if (!active.current) return;
      setReason("");
      setReviewed(false);
      setError(null);
      await Promise.all([
        client.invalidateQueries({ queryKey: ["incidents"] }),
        client.invalidateQueries({ queryKey: ["findings"] }),
      ]);
      onClose();
    },
  });
  const rows = list(record(incidents.data).data);
  return (
    <V12Modal open={open} title="关联告警与故障" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!target || !reason.trim() || !reviewed) {
            setError("请核对目标与关联原因并确认。");
            return;
          }
          setError(null);
          link.mutate();
        }}
        style={{ display: "grid", gap: 14 }}
      >
        <Field label="关联故障" htmlFor="alert-link-incident">
          <Select
            id="alert-link-incident"
            aria-label="关联故障"
            placeholder="选择要关联的故障"
            value={target}
            disabled={link.isPending}
            onChange={setTarget}
            options={rows.map((row) => ({
              value: text(record(row).incidentId),
              label: `${text(record(row).incidentId)} · ${text(record(row).resourceCanonicalId)}`,
            }))}
          />
        </Field>
        <Field label="关联或纠正原因" htmlFor="alert-link-reason">
          <Input.TextArea
            id="alert-link-reason"
            aria-label="关联或纠正原因"
            rows={3}
            maxLength={1000}
            value={reason}
            disabled={link.isPending}
            onChange={(e) => setReason(e.target.value)}
          />
        </Field>
        <Note>
          保留来源原始告警；同一故障聚合相关告警，不删除来源记录。
          {currentIncidentId
            ? ` 该告警当前已关联故障 ${currentIncidentId}，保存前需先在原故障的故障告警页中解除关联。`
            : ""}
        </Note>
        <label
          style={{
            display: "flex",
            gap: 8,
            alignItems: "flex-start",
            fontSize: 12,
            color: "#505a70",
          }}
        >
          <input
            type="checkbox"
            checked={reviewed}
            disabled={link.isPending}
            onChange={(e) => setReviewed(e.target.checked)}
          />
          <span>已核对目标、范围和变更内容。</span>
        </label>
        {error && (
          <p role="alert" className="small" style={{ color: "#b42318" }}>
            {error}
          </p>
        )}
        <Button
          type="primary"
          htmlType="submit"
          disabled={link.isPending}
          loading={link.isPending}
        >
          保存关联
        </Button>
      </form>
    </V12Modal>
  );
}

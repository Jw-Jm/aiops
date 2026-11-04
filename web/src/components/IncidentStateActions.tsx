import { useEffect, useRef, useState } from "react";
import { Button, Input, Space } from "antd";
import { useMutation } from "@tanstack/react-query";
import { transitionIncident } from "../api/generated/platform";
import { checked, requestOptions } from "../api/client";
import { QueryState } from "./QueryState";

export function IncidentStateActions({
  incidentId,
  state,
  revision,
  onChanged,
}: {
  incidentId: string;
  state: string;
  revision: number;
  onChanged: () => void | Promise<void>;
}) {
  const [reason, setReason] = useState("");
  const active = useRef(true),
    controller = useRef(new AbortController());
  useEffect(() => {
    active.current = true;
    controller.current = new AbortController();
    return () => {
      active.current = false;
      controller.current.abort();
    };
  }, []);
  const change = useMutation({
    mutationFn: (targetState: string) =>
      checked(
        transitionIncident(
          incidentId,
          {
            expectedRevision: revision,
            targetState,
            reason: reason.trim(),
          },
          requestOptions(controller.current.signal, true),
        ),
        false,
        () => active.current,
      ),
    onSuccess: async () => {
      if (active.current) {
        setReason("");
        await onChanged();
      }
    },
  });
  const actions = [
    ...(state === "open" ? [["acknowledged", "确认受理"]] : []),
    ...(["open", "acknowledged"].includes(state)
      ? [["mitigating", "开始处置"]]
      : []),
    ...(state === "resolved" ? [["closed", "关闭故障"]] : []),
  ];
  return (
    <section className="section" aria-label="故障状态处理">
      <Space wrap>
        {actions.length > 0 && (
          <Input
            aria-label="处理说明"
            placeholder="填写本次状态变更的处理说明"
            maxLength={1000}
            value={reason}
            disabled={change.isPending}
            onChange={(e) => setReason(e.target.value)}
            style={{ width: 360 }}
          />
        )}
        {actions.map(([target, label]) => (
          <Button
            key={target}
            disabled={!reason.trim() || change.isPending}
            loading={change.isPending && change.variables === target}
            onClick={() => change.mutate(target)}
          >
            {label}
          </Button>
        ))}
        <Button disabled={change.isPending} onClick={onChanged}>
          刷新故障
        </Button>
      </Space>
      {change.error && (
        <QueryState loading={false} error={change.error} retry={onChanged} />
      )}
    </section>
  );
}

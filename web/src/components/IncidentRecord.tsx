import { Button, Space } from "antd";
import { Details } from "./Details";
import { ObservationFacts } from "./ObservationFacts";
import { Status } from "./Status";
import { symptomLabel } from "./FaultSummary";
import { record, list, text, displayTime } from "./data";
const timelineLabels: Record<string, string> = {
  finding: "告警关联与状态更新",
  resolved: "系统确认故障恢复",
  manual_override: "人工变更故障状态",
  suppression_expired: "故障抑制期限结束",
  merge: "故障记录合并",
  split: "故障记录拆分",
};
export function IncidentRecord({
  value,
  kind,
  onEvidence,
}: {
  value: unknown;
  kind: "finding" | "timeline";
  onEvidence: (id: string) => void;
}) {
  const row = record(value);
  return (
    <article>
      <h3>
        {kind === "finding"
          ? symptomLabel(row.normalizedSymptom ?? row.ruleId)
          : (timelineLabels[text(row.kind)] ?? text(row.kind))}
      </h3>
      {kind === "finding" ? (
        <>
          <Space wrap>
            <Status value={row.severity} />
            <Status value={row.lifecycleState} />
            <span>观测：{displayTime(row.observedAt)}</span>
          </Space>
          <p>
            命名空间：{text(row.namespace)} · 首次出现：
            {displayTime(row.startsAt)}
          </p>
          {row.payload !== null && row.payload !== undefined && (
            <ObservationFacts value={row.payload} />
          )}
          <Space wrap className="status-note">
            {list(row.evidenceRefs).map((ref, index) =>
              typeof ref === "string" ? (
                <Button key={ref} title={ref} onClick={() => onEvidence(ref)}>
                  查看关联证据 {index + 1}
                </Button>
              ) : null,
            )}
          </Space>
        </>
      ) : (
        <>
          <p>
            <time>{displayTime(row.createdAt)}</time> ·{" "}
            {row.actor === "correlation/v1" ? "系统自动关联" : text(row.actor)}
          </p>
          <p>
            {row.reason === "settle_window_and_sources_healthy"
              ? "恢复稳定窗口已满足，所需来源健康"
              : text(row.reason)}
          </p>
          {record(row.payload).from !== undefined && (
            <Space>
              原状态 <Status value={record(row.payload).from} /> → 当前状态{" "}
              <Status value={record(row.payload).to} />
            </Space>
          )}
        </>
      )}
      <details className="technical-details">
        <summary>
          完整{kind === "finding" ? "告警" : "时间线"}记录与来源规则
        </summary>
        <Details value={row} />
      </details>
    </article>
  );
}

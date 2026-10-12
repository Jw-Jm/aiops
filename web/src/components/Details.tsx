import { Button, Descriptions, Typography } from "antd";
import { publicValue, record, text, displayTime } from "./data";
import { Status } from "./Status";
import { fieldLabels, technicalFields, stateLabels } from "./presentation";
export function FullText({ value }: { value: unknown }) {
  const s = text(value);
  const content = (
    <Typography.Paragraph className="full-text" copyable={{ text: s }}>
      {typeof value === "boolean" ? (value ? "是（true）" : "否（false）") : s}
    </Typography.Paragraph>
  );
  return s.length > 160 ? (
    <details className="long-field">
      <summary title={s}>{s.slice(0, 100)}… · 查看完整内容</summary>
      {content}
    </details>
  ) : (
    content
  );
}
export function Details({ value }: { value: unknown }) {
  const entries = Object.entries(record(publicValue(value)));
  const render = (fields: typeof entries) => (
    <Descriptions
      column={{ xs: 1, sm: 1, md: 1, xl: 2 }}
      bordered
      size="small"
      items={fields.map(([key, v]) => ({
        key,
        label: fieldLabels[key] ?? key,
        children:
          typeof v === "string" &&
          /^(\d{4}-\d{2}-\d{2}T)/.test(v) &&
          Number.isFinite(Date.parse(v)) ? (
            <time dateTime={v} title={v}>
              {displayTime(v)}
            </time>
          ) : typeof v === "string" &&
            [
              "state",
              "lifecycleState",
              "severity",
              "status",
              "health",
              "source",
              "freshness",
              "verdict",
              "relation",
              "suggestionMatch",
              "replayState",
              "type",
            ].includes(key) &&
            stateLabels[v] ? (
            <Status value={v} />
          ) : Array.isArray(v) && v.length === 0 ? (
            <span>无条目（空列表）</span>
          ) : typeof v === "object" && v !== null ? (
            <details>
              <summary>
                {Array.isArray(v)
                  ? "查看 " + v.length + " 项内容"
                  : "查看详细内容"}
              </summary>
              <pre className="json-view">{JSON.stringify(v, null, 2)}</pre>
            </details>
          ) : (
            <FullText value={v} />
          ),
      }))}
    />
  );
  const primary = entries.filter(
    ([key]) => fieldLabels[key] && !technicalFields.has(key),
  );
  const technical = entries.filter(
    ([key]) => !fieldLabels[key] || technicalFields.has(key),
  );
  return (
    <div className="details-content">
      {primary.length > 0 && render(primary)}
      {technical.length > 0 && (
        <details className="technical-details">
          <summary>技术信息与完整标识（{technical.length} 项）</summary>
          {render(technical)}
        </details>
      )}
    </div>
  );
}
export function JSONView({ value }: { value: unknown }) {
  return (
    <pre className="json-view">
      {JSON.stringify(publicValue(value), null, 2) ?? "未提供"}
    </pre>
  );
}

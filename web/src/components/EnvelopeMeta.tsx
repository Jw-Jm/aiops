import { Alert, Space } from "antd";
import { record, list, text, displayTime } from "./data";
import { Status } from "./Status";
export function EnvelopeMeta({ value }: { value: unknown }) {
  const m = record(value);
  return (
    <>
      {(m.partial === true || list(m.degradedSources).length > 0) && (
        <Alert
          showIcon
          type="warning"
          title="此查询返回部分或来源降级结果"
          description={[...list(m.warnings), ...list(m.degradedSources)]
            .map(text)
            .join(" · ")}
        />
      )}
      <Space wrap className="status-note">
        {"freshness" in m && <Status value={m.freshness} />}
        {"collectedAt" in m && <span>观测：{displayTime(m.collectedAt)}</span>}
        {"graphGeneration" in m && (
          <span>图版本：{text(m.graphGeneration)}</span>
        )}
      </Space>
    </>
  );
}

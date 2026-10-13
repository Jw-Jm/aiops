import type { AssistantQueryResultV1 } from "../../api/generated/models";

export function QueryTimeTrend({ result }: { result: AssistantQueryResultV1 }) {
  if (!result.timeRange || !result.stepSeconds || !result.rows.length)
    return null;
  const start = Date.parse(result.timeRange.from),
    end = Date.parse(result.timeRange.to);
  const points = result.rows
    .map((row) => ({ ...row, time: Date.parse(row.dimension) }))
    .filter((row) => Number.isFinite(row.time) && Number.isFinite(row.value))
    .sort((a, b) => a.time - b.time);
  if (!points.length || end <= start) return null;
  const maximum = Math.max(1, ...points.map((point) => point.value));
  const x = (time: number) => 40 + ((time - start) / (end - start)) * 580;
  const y = (value: number) => 140 - (value / maximum) * 115;
  return (
    <figure style={{ margin: "12px 0" }}>
      <svg
        viewBox="0 0 640 180"
        width="100%"
        role="img"
        aria-label={`${result.unit}时间趋势；缺失观测不补零、不跨缺口连线`}
      >
        <line x1="40" y1="140" x2="620" y2="140" stroke="currentColor" />
        {points.map((point, index) => (
          <g key={point.dimension}>
            {index > 0 &&
              point.time - points[index - 1].time ===
                result.stepSeconds! * 1000 && (
                <line
                  x1={x(points[index - 1].time)}
                  y1={y(points[index - 1].value)}
                  x2={x(point.time)}
                  y2={y(point.value)}
                  stroke="var(--ant-color-primary, #1677ff)"
                />
              )}
            <circle
              cx={x(point.time)}
              cy={y(point.value)}
              r="3"
              fill="var(--ant-color-primary, #1677ff)"
            >
              <title>
                {point.dimension}：{point.value} {result.unit}
              </title>
            </circle>
          </g>
        ))}
        <text x="40" y="162" fontSize="11" fill="currentColor">
          {result.timeRange.from}
        </text>
        <text
          x="620"
          y="162"
          textAnchor="end"
          fontSize="11"
          fill="currentColor"
        >
          {result.timeRange.to}
        </text>
      </svg>
      <figcaption>
        固定步长 {result.stepSeconds} 秒；端点均参与历史求值，使用显式 5
        分钟回看。 缺失观测保留为空缺，数值和绝对时间见下表。
      </figcaption>
    </figure>
  );
}

import type { AssistantQueryResultV1RowsItem } from "../../api/generated/models";

// Platform rows supply all numbers. Missing days remain gaps instead of zeroes.
export function QueryDayTrend({
  rows,
  unit,
  onPointSelect,
}: {
  rows: AssistantQueryResultV1RowsItem[];
  unit: string;
  onPointSelect?: (point: {
    dimension: string;
    value: number;
    unit: string;
    row: number;
  }) => void;
}) {
  // The index of the submitted row is kept while the points are filtered and
  // sorted. The point of a day must locate the row of the persisted result,
  // so the index may never be renumbered by the chart.
  const points = rows.map((row, index) => ({ ...row, index, time: Date.parse(row.dimension + "T00:00:00Z") })).filter(row => /^\d{4}-\d{2}-\d{2}$/.test(row.dimension) && Number.isFinite(row.value) && Number.isFinite(row.time)).sort((a, b) => a.time - b.time);
  if (!points.length) return null;
  const first = points[0].time;
  const days = Math.max(1, Math.round((points.at(-1)!.time - first) / 86400000) + 1);
  const maximum = Math.max(1, ...points.map(row => row.value));
  const width = 580 / days;
  return <figure style={{ margin: "12px 0" }}>
    <svg viewBox="0 0 640 180" width="100%" role="img" aria-label={`按天统计的${unit}趋势；缺失日期不补零`}>
      <line x1="40" y1="140" x2="620" y2="140" stroke="currentColor" />
      {points.map(row => {
        const x = 40 + (row.time - first) / 86400000 * width;
        const height = 115 * row.value / maximum;
        // The row index is the index of the submitted row, so the point can
        // be located in the persisted result and in its facts.
        const rowIndex = row.index;
        return <g key={row.dimension}>
          <rect x={x + width * .1} y={140 - height} width={width * .8} height={Math.max(1, height)} fill="var(--ant-color-primary, #1677ff)"><title>{`${row.dimension}：${row.value} ${unit}`}</title></rect>
          {onPointSelect && <rect
            x={x + width * .1}
            y={140 - height}
            width={width * .8}
            height={Math.max(1, height)}
            fill="transparent"
            style={{ cursor: "pointer" }}
            role="button"
            tabIndex={0}
            aria-label={`${row.dimension} ${row.value} ${unit}`}
            onClick={() => onPointSelect({ dimension: row.dimension, value: row.value, unit, row: rowIndex })}
            onKeyDown={(event) => {
              if (event.key !== "Enter" && event.key !== " ") return;
              event.preventDefault();
              onPointSelect({ dimension: row.dimension, value: row.value, unit, row: rowIndex });
            }}
          />}
        </g>;
      })}
      <text x="40" y="162" fontSize="12" fill="currentColor">{points[0].dimension}</text>
      <text x="620" y="162" textAnchor="end" fontSize="12" fill="currentColor">{points.at(-1)!.dimension}</text>
    </svg>
    <figcaption>按查询时区分日；未出现的日期没有补零。完整数值见下表。</figcaption>
  </figure>;
}

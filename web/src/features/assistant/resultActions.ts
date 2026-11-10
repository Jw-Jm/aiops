import type {
  AssistantQueryResultV1,
  AssistantStoredQueryResultV1,
} from "../../api/generated/models";

// The copy and the export of one submitted result. The payload contains the
// numbers of the persisted result, the fixed identifiers of the facts and the
// provenance of the source. It contains no authorization binding, no grant
// snapshot and no raw model input: those are not part of the result
// contract, so they can never be copied or exported.
export interface ResultCopyPayload {
  schemaVersion: "assistant-result-export/v1";
  queryResultId: string;
  contentDigest?: string;
  datasetId: string;
  scopeDigest: string;
  planDigest: string;
  catalogDigest: string;
  status: string;
  exact: boolean;
  truncated: boolean;
  dimension: string;
  unit: string;
  snapshotReadAt: string;
  timeRange?: { from: string; to: string; timezone: string };
  comparisonTimeRange?: { from: string; to: string; timezone: string };
  rows: { dimension: string; value: number }[];
  statistics: { key: string; value: number; unit: string }[];
  samples?: { timestamp: string; level: string; message: string }[];
  facts: {
    factId: string;
    row: number;
    column: string;
    value: number;
    unit: string;
    exact: boolean;
  }[];
  sourceVersion: string;
  sourceManifest?: {
    sourceId: string;
    revision: number;
    queryHash: string;
    templateVersion: string;
    coverageFrom: string;
    coverageTo: string;
  };
  derivationEvidenceRefs: string[];
  warnings: string[];
  degradedSources: string[];
  sourceStatement: string;
}

export const resultSourceStatement =
  "数字来自平台已提交并持久化的查询结果；缺失观测不补零，样本数不代表总量，模型解释不改变任何数字。";

export function resultSourceVersion(result: AssistantQueryResultV1) {
  if (result.graphRevision)
    return `Owner ${result.graphRevision.ownerEpoch} / Graph ${result.graphRevision.graphGeneration}`;
  if (result.sourceManifest)
    return `${result.sourceManifest.sourceId} / revision ${result.sourceManifest.revision} / ${result.sourceManifest.templateVersion}`;
  return result.controlPlaneSnapshot || "";
}

export function buildResultCopyPayload(
  queryResultId: string,
  stored: AssistantStoredQueryResultV1 | undefined,
): ResultCopyPayload | undefined {
  if (!stored) return undefined;
  const result = stored.result;
  return {
    schemaVersion: "assistant-result-export/v1",
    queryResultId,
    contentDigest: stored.contentDigest,
    datasetId: result.datasetId,
    scopeDigest: result.scopeDigest,
    planDigest: result.planDigest,
    catalogDigest: result.catalogDigest,
    status: result.status,
    exact: result.exact,
    truncated: result.truncated,
    dimension: result.dimension,
    unit: result.unit,
    snapshotReadAt: result.snapshotReadAt,
    timeRange: result.timeRange,
    comparisonTimeRange: result.comparisonTimeRange,
    rows: result.rows.map((row) => ({
      dimension: row.dimension,
      value: row.value,
    })),
    statistics: (result.statistics || []).map((item) => ({
      key: item.key,
      value: item.value,
      unit: item.unit,
    })),
    samples: (result.samples || []).map((sample) => ({
      timestamp: sample.timestamp,
      level: sample.level,
      message: sample.message,
    })),
    facts: stored.facts.map((fact) => ({
      factId: fact.factId,
      row: fact.row,
      column: fact.column,
      value: fact.value,
      unit: fact.unit,
      exact: fact.exact,
    })),
    sourceVersion: resultSourceVersion(result),
    sourceManifest: result.sourceManifest
      ? {
          sourceId: result.sourceManifest.sourceId,
          revision: result.sourceManifest.revision,
          queryHash: result.sourceManifest.queryHash,
          templateVersion: result.sourceManifest.templateVersion,
          coverageFrom: result.sourceManifest.coverageFrom,
          coverageTo: result.sourceManifest.coverageTo,
        }
      : undefined,
    derivationEvidenceRefs: result.derivationEvidenceRefs || [],
    warnings: result.warnings,
    degradedSources: result.degradedSources,
    sourceStatement: resultSourceStatement,
  };
}

export function resultCopyText(payload: ResultCopyPayload) {
  const lines = [
    `查询结果 ${payload.queryResultId}`,
    `口径：${payload.datasetId}（维度 ${payload.dimension || "整体"}，单位 ${payload.unit}）`,
    `状态：${payload.status}${payload.exact ? "（精确）" : "（部分）"}`,
    `快照读取时间：${payload.snapshotReadAt}`,
    `范围摘要：${payload.scopeDigest}`,
    `来源版本：${payload.sourceVersion}`,
  ];
  if (payload.timeRange)
    lines.push(
      `查询时间窗：${payload.timeRange.from} ～ ${payload.timeRange.to} (${payload.timeRange.timezone})`,
    );
  if (payload.comparisonTimeRange)
    lines.push(
      `对照时间窗：${payload.comparisonTimeRange.from} ～ ${payload.comparisonTimeRange.to} (${payload.comparisonTimeRange.timezone})`,
    );
  if (payload.sourceManifest)
    lines.push(
      `来源覆盖：${payload.sourceManifest.coverageFrom} ～ ${payload.sourceManifest.coverageTo}；查询摘要：${payload.sourceManifest.queryHash}`,
    );
  if (payload.truncated) lines.push("结果已截断，不代表全部匹配记录。");
  for (const item of payload.statistics)
    lines.push(`统计 ${item.key}：${item.value} ${item.unit}`);
  for (const row of payload.rows)
    lines.push(`${row.dimension || "整体"}：${row.value} ${payload.unit}`);
  if (payload.samples?.length)
    lines.push(`日志样例 ${payload.samples.length} 条（样例数不代表日志总量）`);
  lines.push(
    `引用事实：${
      payload.facts
        .map((fact) => `${fact.factId}（第 ${fact.row + 1} 行 ${fact.column}）`)
        .join("，") || "无"
    }`,
  );
  if (payload.warnings.length)
    lines.push(`警告：${payload.warnings.join("，")}`);
  if (payload.degradedSources.length)
    lines.push(`降级来源：${payload.degradedSources.join("，")}`);
  lines.push(payload.sourceStatement);
  return lines.join("\n");
}

export function downloadResultExport(
  payload: ResultCopyPayload,
  fileName: string,
) {
  const blob = new Blob([JSON.stringify(payload, null, 2)], {
    type: "application/json;charset=utf-8",
  });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = fileName;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

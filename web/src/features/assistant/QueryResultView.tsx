import { useQuery } from "@tanstack/react-query";
import { Alert, Descriptions, Table, Tag, Typography, List, Space } from "antd";
import { checked, requestOptions } from "../../api/client";
import { getAssistantQueryResult } from "../../api/generated/platform";
import type { AssistantAnswerV1 } from "../../api/generated/models";
import { QueryDayTrend } from "./QueryDayTrend";
import { QueryTimeTrend } from "./QueryTimeTrend";
import { useAuth } from "../../auth/AuthProvider";
import { QueryState } from "../../components/QueryState";
const comparisonLabels: Record<string, string> = {
  baseline: "对照窗口末端",
  current: "当前窗口末端",
  increase: "增加量",
  decrease: "减少量",
  unchanged: "变化量（无变化）",
};
export function QueryResultView({
  id,
  answer,
}: {
  id: string;
  answer: AssistantAnswerV1;
}) {
  const { session } = useAuth();
  const scopeKey = JSON.stringify([
    session?.tenantId,
    session?.subject,
    session?.roles,
    session?.clusterScopes,
    session?.namespaceScopes,
  ]);
  const query = useQuery({
    queryKey: ["assistant", scopeKey, "result", id],
    enabled: !!session,
    queryFn: ({ signal }) =>
      checked(getAssistantQueryResult(id, requestOptions(signal))),
  });
  const stored = query.data?.data;
  const result = stored?.result;
  const network = result?.networkContext;
  return (
    <QueryState
      loading={query.isPending}
      error={query.error}
      retry={() => void query.refetch()}
    >
      {result && (
        <>
          <Tag color={result.exact ? "green" : "orange"}>
            {result.status === "no_data"
              ? "无数据"
              : result.status === "degraded"
                ? "来源降级"
                : result.exact
                  ? network
                    ? "观测总体的精确统计"
                    : "精确结果"
                  : "部分结果"}
          </Tag>
          {network && (
            <Alert
              type="info"
              title={`统计范围：${network.population}；采集位置：${network.observationPoint}；不代表所有用户请求。${network.timeReliable ? "" : "源时间尚未验证，不能判定异常先后。"}`}
            />
          )}
          {result.statistics && (
            <Descriptions
              size="small"
              column={1}
              items={result.statistics.map((item) => ({
                key: item.key,
                label: item.key,
                children: `${item.value} ${item.unit}`,
              }))}
            />
          )}
          {result.dimension === "day" && result.rows.length > 0 && (
            <QueryDayTrend rows={result.rows} unit={result.unit} />
          )}
          {result.dimension === "time" && <QueryTimeTrend result={result} />}
          {result.truncated && <Tag color="orange">结果截断</Tag>}
          {result.samples && (
            <>
              <Alert type="info" title="脱敏日志样例；样例数不代表日志总量。" />
              <List
                dataSource={result.samples}
                renderItem={(sample) => (
                  <List.Item>
                    <Space orientation="vertical">
                      <Typography.Text>
                        {sample.timestamp} · {sample.level}
                      </Typography.Text>
                      <Typography.Paragraph style={{ whiteSpace: "pre-wrap" }}>
                        {sample.message}
                      </Typography.Paragraph>
                    </Space>
                  </List.Item>
                )}
              />
            </>
          )}
          {result.status === "no_data" || result.status === "degraded" ? (
            <Alert
              type="warning"
              title="没有可用观测，不能据此判断为零或正常。"
            />
          ) : (
            <Table
              size="small"
              pagination={false}
              rowKey={(_, index) => String(index)}
              dataSource={result.rows}
              columns={[
                {
                  title: result.dimension || "范围",
                  dataIndex: "dimension",
                  render: (value: string) =>
                    result.dimension === "comparison"
                      ? (network && value === "baseline"
                          ? "基线窗口"
                          : network && value === "current"
                            ? "所选窗口"
                            : comparisonLabels[value]) || value
                      : value || "当前授权范围",
                },
                {
                  title:
                    result.unit === "resources"
                      ? "资源数量"
                      : `数值（${result.unit}）`,
                  dataIndex: "value",
                  render: (value: number, row: { dimension: string }) =>
                    `${value}${network && result.datasetId === "network.http-5xx-rate" && result.rows.length === 3 && ["increase", "decrease", "unchanged"].includes(row.dimension) ? " pp" : ""}`,
                },
              ]}
            />
          )}
          {stored && (
            <Typography.Paragraph>
              {stored.facts
                .filter((fact) =>
                  answer.factRefs.some(
                    (ref) =>
                      ref.queryResultId === id && ref.factId === fact.factId,
                  ),
                )
                .map((fact) => (
                  <Tag key={fact.factId}>
                    {(fact.column === "statistic"
                      ? result.statistics?.[fact.row]?.key
                      : result.rows[fact.row]?.dimension) || "合计"}
                    ：{fact.value} {fact.unit}
                    {fact.exact ? "" : "（部分）"}
                  </Tag>
                ))}
            </Typography.Paragraph>
          )}
          <Descriptions
            size="small"
            column={1}
            items={[
              { key: "dataset", label: "口径", children: result.datasetId },
              {
                key: "asOf",
                label: "快照读取时间",
                children: result.snapshotReadAt,
              },
              { key: "scope", label: "范围摘要", children: result.scopeDigest },
              ...(result.timeRange
                ? [
                    {
                      key: "window",
                      label: "查询时间窗",
                      children: `${result.timeRange.from} ～ ${result.timeRange.to} (${result.timeRange.timezone})`,
                    },
                  ]
                : []),
              ...(result.comparisonTimeRange
                ? [
                    {
                      key: "comparison",
                      label: "对照时间窗",
                      children: `${result.comparisonTimeRange.from} ～ ${result.comparisonTimeRange.to} (${result.comparisonTimeRange.timezone})；${network ? "网络按等长窗口观测统计比较；5xx 比例的绝对变化单位为百分点。" : "内存/状态比较窗口末端，CPU 比较窗口平均 rate，重启/日志比较窗口增量。"}百分比以非零基线计算；缺失不会补零。`,
                    },
                  ]
                : []),
              {
                key: "source",
                label: "来源版本",
                children: result.graphRevision
                  ? `Owner ${result.graphRevision.ownerEpoch} / Graph ${result.graphRevision.graphGeneration}`
                  : result.sourceManifest
                    ? `${result.sourceManifest.sourceId} / revision ${result.sourceManifest.revision} / ${result.sourceManifest.templateVersion}`
                    : result.controlPlaneSnapshot || "来源版本不可用",
              },
              ...(result.sourceManifest
                ? [
                    {
                      key: "coverage",
                      label: "来源覆盖时间",
                      children: `${result.sourceManifest.coverageFrom} ～ ${result.sourceManifest.coverageTo}`,
                    },
                    {
                      key: "query",
                      label: "已执行查询摘要",
                      children: result.sourceManifest.queryHash,
                    },
                  ]
                : []),
              {
                key: "warnings",
                label: "缺失与警告",
                children:
                  [...result.warnings, ...result.degradedSources].join("，") ||
                  "无",
              },
            ]}
          />
        </>
      )}
    </QueryState>
  );
}

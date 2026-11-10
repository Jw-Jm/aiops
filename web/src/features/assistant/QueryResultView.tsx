import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Drawer,
  Descriptions,
  Segmented,
  Table,
  Tag,
  Typography,
  List,
  Space,
  message,
} from "antd";
import { Link } from "react-router-dom";
import { ApiError, checked, requestOptions } from "../../api/client";
import { getAssistantQueryResult } from "../../api/generated/platform";
import type {
  AssistantAnswerV1,
  AssistantQueryResultV1,
  AssistantQueryScopeV1,
} from "../../api/generated/models";
import { QueryDayTrend } from "./QueryDayTrend";
import { QueryTimeTrend } from "./QueryTimeTrend";
import { useAuth } from "../../auth/AuthProvider";
import { AssistantEvidence } from "./AssistantEvidence";
import { QueryState } from "../../components/QueryState";
import { useAssistantScopeKey } from "./scope";
import { countDrillTarget } from "./drill";
import {
  buildResultCopyPayload,
  downloadResultExport,
  resultCopyText,
  resultSourceStatement,
  resultSourceVersion,
} from "./resultActions";

// The link of the count drill down. It carries the identity of the submitted
// snapshot: the dataset, the scope digest, the time window, the source
// version and the snapshot time. The list it opens is the current authorized
// projection and states the difference itself.
function drillHrefFor(
  result: AssistantQueryResultV1 | undefined,
  dimensionValue?: string,
  countValue?: number,
  queryScope?: AssistantQueryScopeV1,
  queryResultId?: string,
) {
  if (!result || !queryResultId || !queryScope?.filtersKnown) return undefined;
  const target = countDrillTarget(result, dimensionValue);
  if (!target) return undefined;
  const params = new URLSearchParams(target.params);
  params.set("assistantQueryResultId", queryResultId);
  params.set("clusterUid", queryScope.clusterUid);
  if (dimensionValue !== undefined)
    params.set("assistantGroup", dimensionValue);
  params.set("aiFrom", "assistant");
  params.set("aiDataset", result.datasetId);
  params.set("aiScopeDigest", result.scopeDigest);
  params.set("aiSnapshotAt", result.snapshotReadAt);
  if (result.timeRange)
    params.set(
      "aiWindow",
      `${result.timeRange.from} ～ ${result.timeRange.to}`,
    );
  params.set(
    "aiVersion",
    result.graphRevision
      ? `Owner ${result.graphRevision.ownerEpoch} / Graph ${result.graphRevision.graphGeneration}`
      : result.sourceManifest
        ? `${result.sourceManifest.sourceId} / revision ${result.sourceManifest.revision}`
        : result.controlPlaneSnapshot || "",
  );
  if (countValue !== undefined && Number.isFinite(countValue))
    params.set("aiCount", String(countValue));
  return `${target.path}?${params.toString()}`;
}
const comparisonLabels: Record<string, string> = {
  baseline: "对照窗口末端",
  current: "当前窗口末端",
  increase: "增加量",
  decrease: "减少量",
  unchanged: "变化量（无变化）",
};
const statisticLabels: Record<string, string> = {
  observations: "观测记录数",
  responses: "完成 HTTP 响应数",
  "5xx": "HTTP 5xx 响应数",
  mean_response_ms: "平均 HTTP 响应耗时",
  retransmissions: "TCP 重传计数",
  failure_observations: "TCP 错误观测数",
};
const statisticUnits: Record<string, string> = {
  observations: "条",
  responses: "个响应",
  milliseconds: "毫秒",
  retransmissions: "次",
};
function statisticLabel(key: string) {
  const separator = key.lastIndexOf(".");
  const window = key.slice(0, separator);
  const metric = key.slice(separator + 1);
  const windowLabel =
    window === "baseline"
      ? "对照窗口"
      : window === "current"
        ? "所选窗口"
        : window;
  return `${windowLabel} · ${statisticLabels[metric] || metric}`;
}
// The frozen context of one click: the scope, the result and the epoch of the
// render the click happened in. Every await boundary compares it with the
// context of the current render.
interface FrozenContext {
  scope: string;
  epoch: number;
}

export function QueryResultView({
  id,
  answer,
  focusFactId,
  anchorId,
}: {
  id: string;
  answer: AssistantAnswerV1;
  focusFactId?: string;
  anchorId?: string;
}) {
  const location = useRef<HTMLDivElement>(null);
  const { session } = useAuth();
  const client = useQueryClient();
  // The cache key of the result contains the scope generation of the parent
  // scope. A result of an older generation is not a result of the current
  // authorization context, so it must never be served from the cache of the
  // current scope.
  const scopeKey = useAssistantScopeKey();
  // Every interaction of this view belongs to one result of one authorization
  // context. The scope of the intent carries the tenant, the subject, the
  // granted scopes and the scope generation of the parent.
  const intentScope = `${scopeKey}|${id}`;
  // The epoch counts the changes of the context. It grows with every new
  // scope or result, so the late answer of an older context never matches the
  // new one, even when the scope string repeats itself (A -> B -> A).
  const contextRef = useRef({ scope: intentScope, epoch: 0 });
  if (contextRef.current.scope !== intentScope)
    contextRef.current = {
      scope: intentScope,
      epoch: contextRef.current.epoch + 1,
    };
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);
  const [intent, setIntent] = useState<{
    scope: string;
    factId?: string;
    chartPoint?: {
      dimension: string;
      value: number;
      unit: string;
      row: number;
    };
    sourceOpen?: boolean;
    copied?: boolean;
    view: "chart" | "values";
  }>({ scope: intentScope, view: "chart" });
  const [actionBusy, setActionBusy] = useState(false);
  const actionLock = useRef(false);
  // A new result or a new authorization context closes every drawer of the
  // previous one and drops its copied state. The state of the new result is
  // never mixed with the state of the old one. The busy state is dropped as
  // well, so the request of an older context never strands the new one.
  useEffect(() => {
    setIntent({ scope: intentScope, view: "chart" });
    actionLock.current = false;
    setActionBusy(false);
  }, [intentScope]);
  const query = useQuery({
    queryKey: ["assistant", scopeKey, "result", id],
    enabled: !!session,
    queryFn: ({ signal }) =>
      checked(getAssistantQueryResult(id, requestOptions(signal))),
  });
  // A lost grant or an expired session drops the cached results of this scope
  // only. A late refusal of an older scope never touches the cache of the
  // scope that is shown now.
  useEffect(() => {
    const failure = query.error;
    if (!failure) return;
    if (failure instanceof ApiError && [401, 403].includes(failure.status))
      client.removeQueries({ queryKey: ["assistant", scopeKey] });
  }, [query.error, client, scopeKey]);
  const stored = query.data?.data;
  const result = stored?.result;
  const network = result?.networkContext;
  // The interaction state of another result is never rendered.
  const factId = intent.scope === intentScope ? intent.factId : undefined;
  const chartPoint =
    intent.scope === intentScope ? intent.chartPoint : undefined;
  const sourceOpen =
    intent.scope === intentScope ? Boolean(intent.sourceOpen) : false;
  const copied = intent.scope === intentScope ? Boolean(intent.copied) : false;
  const view = intent.scope === intentScope ? intent.view : "chart";
  const fact = stored?.facts.find((item) => item.factId === factId);
  // The deep link of a fact opens the fact of a loaded and authorized result
  // only. A focus that changes inside the same result opens the new fact; a
  // scope or result change closes the drawer instead of showing a fact of a
  // previous context.
  useEffect(() => {
    if (!focusFactId) return;
    const target = stored?.facts.find((item) => item.factId === focusFactId);
    if (!target) return;
    setIntent((previous) =>
      previous.scope === intentScope
        ? { ...previous, factId: focusFactId }
        : { scope: intentScope, view: "chart", factId: focusFactId },
    );
    location.current?.focus();
    location.current?.scrollIntoView?.({ block: "nearest" });
  }, [focusFactId, stored, intentScope]);
  // The chart and the numeric table show the same submitted numbers. The view
  // switch only changes the presentation; it never changes a value.
  const trend = result?.dimension === "day" || result?.dimension === "time";
  // The frozen context of one click: the scope, the result and the epoch of
  // the render the click happened in. Every await boundary compares it with
  // the context of the current render.
  function freezeContext(): FrozenContext {
    return { scope: intentScope, epoch: contextRef.current.epoch };
  }
  function isCurrent(frozen: FrozenContext) {
    return (
      mountedRef.current &&
      contextRef.current.scope === frozen.scope &&
      contextRef.current.epoch === frozen.epoch
    );
  }
  // The copy and the export read the result again under the current
  // authorization. A refused read writes nothing to the clipboard and
  // downloads nothing.
  async function readCurrentResult(frozen: FrozenContext) {
    const fresh = await checked(getAssistantQueryResult(id, requestOptions()));
    // A late answer of an older context never writes into the new one.
    if (!isCurrent(frozen)) return undefined;
    return fresh.data;
  }
  function refuseAction(failure: unknown) {
    if (failure instanceof ApiError && [401, 403].includes(failure.status))
      client.removeQueries({ queryKey: ["assistant", scopeKey] });
    message.error(
      failure instanceof ApiError && [401, 403].includes(failure.status)
        ? "当前授权已失效，未复制也未导出任何内容。"
        : "复制或导出未成功，请重新授权后再试。",
    );
  }
  async function copyResult() {
    if (actionLock.current) return;
    actionLock.current = true;
    const frozen = freezeContext();
    setActionBusy(true);
    try {
      const fresh = await readCurrentResult(frozen);
      if (!fresh) return;
      const payload = buildResultCopyPayload(id, fresh);
      if (!payload) return;
      await navigator.clipboard.writeText(resultCopyText(payload));
      // The frozen scope, not the current one: the late completion of an
      // older context never marks the new result copied.
      if (!isCurrent(frozen)) return;
      setIntent((previous) =>
        previous.scope === frozen.scope
          ? { ...previous, copied: true }
          : previous,
      );
    } catch (failure) {
      // A late failure of an older context shows no message in the new
      // context and clears no cache of the new context.
      if (isCurrent(frozen)) refuseAction(failure);
    } finally {
      if (isCurrent(frozen)) {
        actionLock.current = false;
        setActionBusy(false);
      }
    }
  }
  async function exportResult() {
    if (actionLock.current) return;
    actionLock.current = true;
    const frozen = freezeContext();
    setActionBusy(true);
    try {
      const fresh = await readCurrentResult(frozen);
      if (!fresh) return;
      const payload = buildResultCopyPayload(id, fresh);
      if (!payload) return;
      if (!isCurrent(frozen)) return;
      downloadResultExport(payload, `assistant-result-${id}.json`);
    } catch (failure) {
      if (isCurrent(frozen)) refuseAction(failure);
    } finally {
      if (isCurrent(frozen)) {
        actionLock.current = false;
        setActionBusy(false);
      }
    }
  }
  // The drill down of a count leads to the real records of the same scope.
  // The link carries the dataset, the scope digest, the time window and the
  // source version of the submitted result, so the list can state that it is
  // the current authorized projection and not the snapshot the count came
  // from.
  const drillHref = drillHrefFor(
    result,
    undefined,
    undefined,
    query.data?.queryScope,
    id,
  );
  function valueLink(
    value: string,
    row: number,
    column: "value" | "statistic",
  ) {
    const ref = stored?.facts.find(
      (item) => item.row === row && item.column === column,
    );
    return ref ? (
      <Button
        type="link"
        size="small"
        aria-label={`定位 Fact ${ref.factId}`}
        onClick={() =>
          setIntent((previous) =>
            previous.scope === intentScope
              ? { ...previous, factId: ref.factId }
              : { scope: intentScope, view: "chart", factId: ref.factId },
          )
        }
      >
        {value}
      </Button>
    ) : (
      value
    );
  }
  return (
    <div
      id={anchorId || `query-result-${id}`}
      ref={location}
      tabIndex={-1}
      style={{ overflowWrap: "anywhere" }}
    >
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
                title={`统计范围：${network.population === "completed_http_session_observations" ? "完成的 HTTP 会话观测" : network.population === "scoped_single_observation_point_records" ? "授权范围内单一采集点的记录" : network.population}；采集位置：${network.observationPoint}；不代表所有用户请求。${network.timeReliable ? "" : "源时间尚未验证，不能判定异常先后。"}`}
              />
            )}
            {result.statistics && (
              <Descriptions
                size="small"
                column={1}
                items={result.statistics.map((item, row) => ({
                  key: item.key,
                  label: statisticLabel(item.key),
                  children: valueLink(
                    `${item.value} ${statisticUnits[item.unit] || item.unit}`,
                    row,
                    "statistic",
                  ),
                }))}
              />
            )}
            {trend && (
              <Space size="small">
                <Segmented
                  size="small"
                  aria-label="图表或数值"
                  value={view}
                  onChange={(next) =>
                    setIntent((previous) =>
                      previous.scope === intentScope
                        ? { ...previous, view: next as "chart" | "values" }
                        : previous,
                    )
                  }
                  options={[
                    { label: "图表", value: "chart" },
                    { label: "数值", value: "values" },
                  ]}
                />
              </Space>
            )}
            {result.dimension === "day" &&
              result.rows.length > 0 &&
              view === "chart" && (
                <QueryDayTrend
                  rows={result.rows}
                  unit={result.unit}
                  onPointSelect={(point) =>
                    setIntent((previous) =>
                      previous.scope === intentScope
                        ? { ...previous, chartPoint: point }
                        : previous,
                    )
                  }
                />
              )}
            {result.dimension === "time" && view === "chart" && (
              <QueryTimeTrend
                result={result}
                onPointSelect={(point) =>
                  setIntent((previous) =>
                    previous.scope === intentScope
                      ? { ...previous, chartPoint: point }
                      : previous,
                  )
                }
              />
            )}
            {result.truncated && <Tag color="orange">结果截断</Tag>}
            {result.samples && (
              <>
                <Alert
                  type="info"
                  title="脱敏日志样例；样例数不代表日志总量。"
                />
                <List
                  dataSource={result.samples}
                  renderItem={(sample) => (
                    <List.Item>
                      <Space orientation="vertical">
                        <Typography.Text>
                          {sample.timestamp} · {sample.level}
                        </Typography.Text>
                        <Typography.Paragraph
                          style={{ whiteSpace: "pre-wrap" }}
                        >
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
            ) : !trend || view === "values" ? (
              <>
                {drillHref && (
                  <Space wrap size="small">
                    <Link to={drillHref} aria-label="查看当前符合条件的记录">
                      查看当前符合条件的记录 →
                    </Link>
                  </Space>
                )}
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
                      render: (
                        value: number,
                        row: { dimension: string },
                        index: number,
                      ) =>
                        valueLink(
                          `${value}${network && (result.datasetId === "network.http-5xx-rate" || result.datasetId === "network.http-summary") && result.rows.length === 3 && ["increase", "decrease", "unchanged"].includes(row.dimension) ? " pp" : ""}`,
                          index,
                          "value",
                        ),
                    },
                    {
                      title: "记录",
                      render: (
                        _value: unknown,
                        row: { dimension: string; value: number },
                      ) => {
                        const href = drillHrefFor(
                          result,
                          row.dimension,
                          row.value,
                          query.data?.queryScope,
                          id,
                        );
                        if (!href) return null;
                        return (
                          <Link
                            to={href}
                            aria-label={`查看 ${row.dimension} 的当前记录`}
                          >
                            查看符合条件的记录 →
                          </Link>
                        );
                      },
                    },
                  ]}
                />
              </>
            ) : null}
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
            <Space wrap size="small">
              <Button
                size="small"
                onClick={() =>
                  setIntent((previous) =>
                    previous.scope === intentScope
                      ? { ...previous, sourceOpen: true }
                      : previous,
                  )
                }
              >
                查询口径与来源
              </Button>
              <Button
                size="small"
                loading={actionBusy}
                onClick={() => void copyResult()}
              >
                {copied ? "已复制摘要" : "复制摘要"}
              </Button>
              <Button
                size="small"
                loading={actionBusy}
                onClick={() => void exportResult()}
              >
                导出结果
              </Button>
            </Space>
          </>
        )}
      </QueryState>
      {sourceOpen && (
        <Drawer
          title="回答口径与来源"
          open
          onClose={() =>
            setIntent((previous) =>
              previous.scope === intentScope
                ? { ...previous, sourceOpen: false }
                : previous,
            )
          }
          width="min(760px, 100vw)"
        >
          {result && (
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
                {
                  key: "scope",
                  label: "范围摘要",
                  children: result.scopeDigest,
                },
                {
                  key: "plan",
                  label: "查询计划摘要",
                  children: result.planDigest,
                },
                {
                  key: "catalog",
                  label: "口径目录摘要",
                  children: result.catalogDigest,
                },
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
                  children: resultSourceVersion(result),
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
                      {
                        key: "binding",
                        label: "来源绑定摘要",
                        children: result.sourceManifest.bindingDigest,
                      },
                    ]
                  : []),
                {
                  key: "digest",
                  label: "持久结果摘要",
                  children: stored?.contentDigest,
                },
                {
                  key: "warnings",
                  label: "缺失与警告",
                  children:
                    [...result.warnings, ...result.degradedSources].join(
                      "，",
                    ) || "无",
                },
                {
                  key: "statement",
                  label: "数据声明",
                  children: resultSourceStatement,
                },
              ]}
            />
          )}
        </Drawer>
      )}
      {!!factId && (
        <Drawer
          title="已提交统计 Fact"
          open
          onClose={() =>
            setIntent((previous) =>
              previous.scope === intentScope
                ? { ...previous, factId: undefined }
                : previous,
            )
          }
          width="min(760px, 100vw)"
        >
          {fact ? (
            <>
              <Descriptions
                column={1}
                items={[
                  { key: "fact", label: "Fact", children: fact.factId },
                  {
                    key: "value",
                    label: "原值",
                    children: `${fact.value} ${fact.unit}${fact.exact ? "" : "（部分）"}`,
                  },
                  {
                    key: "row",
                    label: "定位",
                    children: `第 ${fact.row + 1} 行 / ${fact.column}`,
                  },
                  { key: "query", label: "QueryResult", children: id },
                  {
                    key: "digest",
                    label: "持久结果摘要",
                    children: stored?.contentDigest,
                  },
                  {
                    key: "window",
                    label: "原查询窗口",
                    children: result?.timeRange
                      ? `${result.timeRange.from} ～ ${result.timeRange.to}`
                      : "未指定",
                  },
                ]}
              />
              <AssistantEvidence refs={result?.derivationEvidenceRefs || []} />
            </>
          ) : (
            <Alert
              type="warning"
              title={
                query.error
                  ? "统计读取未获授权或失败，请重试原结果。"
                  : "未找到该 Fact，不能用其他数字代替。"
              }
            />
          )}
        </Drawer>
      )}
      {!!chartPoint && (
        <Drawer
          title="观测点与来源"
          open
          onClose={() =>
            setIntent((previous) =>
              previous.scope === intentScope
                ? { ...previous, chartPoint: undefined }
                : previous,
            )
          }
          width="min(760px, 100vw)"
        >
          {chartPoint && result ? (
            <Descriptions
              column={1}
              items={[
                {
                  key: "time",
                  label: "观测时间",
                  children: chartPoint.dimension,
                },
                {
                  key: "value",
                  label: "观测值",
                  children: `${chartPoint.value} ${chartPoint.unit}${
                    result.exact ? "" : "（部分结果）"
                  }`,
                },
                {
                  key: "row",
                  label: "定位",
                  children: `第 ${chartPoint.row + 1} 行 / value`,
                },
                {
                  key: "query",
                  label: "QueryResult",
                  children: id,
                },
                {
                  key: "dataset",
                  label: "口径",
                  children: result.datasetId,
                },
                {
                  key: "scope",
                  label: "范围摘要",
                  children: result.scopeDigest,
                },
                {
                  key: "window",
                  label: "查询时间窗",
                  children: result.timeRange
                    ? `${result.timeRange.from} ～ ${result.timeRange.to} (${result.timeRange.timezone})`
                    : "未指定",
                },
                {
                  key: "statement",
                  label: "数据声明",
                  children:
                    "该点是平台已提交并持久化的结果；缺失观测保留为空缺，不补零，模型解释不改变该数值。",
                },
              ]}
            />
          ) : (
            <Alert type="warning" title="未选择观测点。" />
          )}
        </Drawer>
      )}
    </div>
  );
}

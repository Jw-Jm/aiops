import { useResourceNames, resourceLabel } from "../components/ResourceLabel";
import { symptomLabel, resourceIdentifier } from "../components/FaultSummary";
import { fieldLabels, stateLabel } from "../components/presentation";
import { useQuery } from "@tanstack/react-query";
import { Input, Button, Select } from "antd";
import { DataTable as Table } from "../components/v12/primitives";
import { Link, useSearchParams } from "react-router-dom";
import { getFindings, getIncidents } from "../api/generated/platform";
import { checked, requestOptions } from "../api/client";
import { PageHeading } from "../components/PageHeading";
import { ScopeFilter } from "../components/ScopeFilter";
import { useOperationalCluster } from "../app/ClusterContext";
import { QueryState } from "../components/QueryState";
import { Status } from "../components/Status";
import { Details, FullText } from "../components/Details";
import { EnvelopeMeta } from "../components/EnvelopeMeta";
import { list, record, text, displayTime } from "../components/data";
export function FindingsPage({ incidents = false }: { incidents?: boolean }) {
  const [url, setUrl] = useSearchParams();
  const { clusterUid: cluster, invalid } = useOperationalCluster();
  const params = {
    clusterUid: cluster,
    limit: 50,
    ...Object.fromEntries(
      [
        "state",
        "severity",
        "resourceCanonicalId",
        "cursor",
        ...(incidents
          ? ["rcaStatus", "openedFrom", "openedTo"]
          : ["sourceSystem", "observedFrom", "observedTo"]),
      ]
        .filter((k) => url.has(k))
        .map((k) => [k, url.get(k)!]),
    ),
  };
  const update = (k: string, v: string) => {
    const n = new URLSearchParams(url);
    n.delete("cursor");
    if (v) n.set(k, v);
    else n.delete(k);
    setUrl(n);
  };
  const q = useQuery({
    queryKey: [incidents ? "incidents" : "findings", params],
    enabled: !!cluster,
    queryFn: async ({ signal }) =>
      incidents
        ? checked(getIncidents(params, requestOptions(signal)))
        : checked(getFindings(params, requestOptions(signal))),
  });
  const rows = list(record(q.data).data).map(record);
  const resourceNames = useResourceNames(
    cluster,
    rows.map((row) => text(row.resourceCanonicalId)),
  );
  const meta = record(q.data).meta;
  return (
    <>
      <PageHeading
        title={incidents ? "故障处理" : "故障告警"}
        description={
          incidents
            ? "选择故障，查看影响资源、确认原因并跟进处置。"
            : "查看检测到的异常，展开告警可检查证据和来源。"
        }
      />
      <section className="section">
        <div className="filters">
          <ScopeFilter value={cluster} />
          {[
            "state",
            "severity",
            incidents ? "rcaStatus" : "sourceSystem",
            "resourceCanonicalId",
          ].map((k) => (
            <div className="filter" key={k}>
              <label htmlFor={"finding-" + k}>{fieldLabels[k] ?? k}</label>
              {["state", "severity", "rcaStatus"].includes(k) ? (
                <Select
                  id={"finding-" + k}
                  allowClear
                  placeholder="全部"
                  value={url.get(k) || undefined}
                  options={(k === "severity"
                    ? ["critical", "warning", "info", "unknown"]
                    : k === "rcaStatus"
                      ? ["confirmed", "probable", "unresolved"]
                      : incidents
                        ? [
                            "open",
                            "acknowledged",
                            "investigating",
                            "mitigating",
                            "suppressed",
                            "resolved",
                            "closed",
                          ]
                        : ["firing", "resolved"]
                  ).map((value) => ({ value, label: stateLabel(value) }))}
                  onChange={(v) => update(k, v ?? "")}
                />
              ) : (
                <Input
                  id={"finding-" + k}
                  placeholder={
                    k === "resourceCanonicalId"
                      ? "可选：完整资源标识"
                      : "可选：来源名称"
                  }
                  defaultValue={url.get(k) || ""}
                  key={k + location.search}
                  onPressEnter={(e) => update(k, e.currentTarget.value)}
                  onBlur={(e) => {
                    if (e.target.value !== (url.get(k) || ""))
                      update(k, e.target.value);
                  }}
                />
              )}
            </div>
          ))}
          {(incidents
            ? ["openedFrom", "openedTo"]
            : ["observedFrom", "observedTo"]
          ).map((key, index) => (
            <div className="filter" key={key}>
              <label htmlFor={"finding-" + key}>
                {incidents ? "故障开始时间" : "告警观测时间"} ·{" "}
                {index === 0 ? "起" : "止"}（UTC+08:00）
              </label>
              <Input
                id={"finding-" + key}
                type="datetime-local"
                step="1"
                key={key + location.search}
                defaultValue={
                  url.get(key) && Number.isFinite(Date.parse(url.get(key)!))
                    ? new Date(Date.parse(url.get(key)!) + 8 * 3600000)
                        .toISOString()
                        .slice(0, 19)
                    : ""
                }
                onBlur={(e) => {
                  const raw = e.currentTarget.value;
                  const value = raw
                    ? new Date(raw + "+08:00").toISOString()
                    : "";
                  if (value !== (url.get(key) ?? "")) update(key, value);
                }}
              />
            </div>
          ))}
        </div>
        <QueryState
          loading={q.isPending && !!cluster}
          error={q.error}
          noScope={!cluster || invalid}
          empty={rows.length === 0}
          retry={() => void q.refetch()}
        >
          <EnvelopeMeta value={meta} />
          <Table
            rowKey={(r) => text(r.incidentId ?? r.findingId)}
            dataSource={rows}
            pagination={false}
            scroll={{ x: 780 }}
            expandable={
              !incidents
                ? { expandedRowRender: (r) => <Details value={r} /> }
                : undefined
            }
            columns={[
              {
                title: incidents ? "待处理故障" : "故障现象",
                width: 260,
                render: (_, r) =>
                  incidents ? (
                    <Link
                      to={
                        "/events/incidents/" + encodeURIComponent(text(r.incidentId))
                      }
                    >
                      {resourceLabel(r.resourceCanonicalId, resourceNames)}
                    </Link>
                  ) : (
                    <span>{symptomLabel(r.normalizedSymptom ?? r.ruleId)}</span>
                  ),
              },
              {
                title: "状态",
                width: 150,
                render: (_, r) => (
                  <Status value={r.state ?? r.lifecycleState} />
                ),
              },
              {
                title: "严重程度",
                dataIndex: "severity",
                width: 130,
                render: (v) => <Status value={v} />,
              },
              {
                title: "资源",
                width: 260,
                render: (_, r) => (
                  <Link
                    to={
                      "/resources/detail?canonicalId=" +
                      encodeURIComponent(text(r.resourceCanonicalId))
                    }
                  >
                    <span title={text(r.resourceCanonicalId)}>
                      {resourceLabel(r.resourceCanonicalId, resourceNames)}
                    </span>
                  </Link>
                ),
              },
              {
                title: "最后观测",
                width: 200,
                render: (_, r) =>
                  displayTime(r.lastObservedAt ?? r.updatedAt ?? r.observedAt),
              },
            ]}
          />
          <div className="pagination">
            <span>本页 {rows.length} 条 · 每页最多 50 条</span>
            <div>
              <Button
                disabled={!url.has("cursor")}
                onClick={() => update("cursor", "")}
              >
                回到第一页
              </Button>{" "}
              <Button
                disabled={!record(meta).nextCursor}
                onClick={() => {
                  const n = new URLSearchParams(url);
                  n.set("cursor", text(record(meta).nextCursor));
                  setUrl(n);
                }}
              >
                下一页
              </Button>
            </div>
          </div>
        </QueryState>
      </section>
    </>
  );
}

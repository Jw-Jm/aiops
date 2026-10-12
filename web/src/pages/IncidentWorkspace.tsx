import { IncidentRecord } from "../components/IncidentRecord";
import { FaultSummary } from "../components/FaultSummary";
import { RegionBoundary } from "../components/RegionBoundary";
import { lazy, Suspense } from "react";
import { useQuery } from "@tanstack/react-query";
import { Tabs, Button, Space, Table } from "antd";
import { Link, useParams, useSearchParams } from "react-router-dom";
import {
  getIncident,
  getIncidentTimeline,
  getFindings,
  getIncidentRCA,
  getResourceByCanonicalID,
} from "../api/generated/platform";
import type {
  IncidentSuccessV2,
  CurrentRCASuccessV2,
} from "../api/generated/models";
import { checked, requestOptions } from "../api/client";
import { PageHeading } from "../components/PageHeading";
import { QueryState } from "../components/QueryState";
import { Details, FullText } from "../components/Details";
import { Status } from "../components/Status";
import { record, list, text } from "../components/data";
const GraphView = lazy(() =>
  import("../features/graph/GraphView").then((m) => ({ default: m.GraphView })),
);
const RCAPanel = lazy(() =>
  import("../features/rca/RCAPanel").then((m) => ({ default: m.RCAPanel })),
);
const EvidencePanel = lazy(() =>
  import("../features/evidence/EvidencePanel").then((m) => ({
    default: m.EvidencePanel,
  })),
);
const InvestigationPanel = lazy(() =>
  import("../features/investigation/InvestigationPanel").then((m) => ({
    default: m.InvestigationPanel,
  })),
);
const CommandComposer = lazy(() =>
  import("../features/action/CommandComposer").then((m) => ({
    default: m.CommandComposer,
  })),
);
const AuditPage = lazy(() =>
  import("./admin/AuditPage").then((m) => ({ default: m.AuditPage })),
);
import { useAuth } from "../auth/AuthProvider";
export function IncidentWorkspace() {
  const { incidentId = "" } = useParams();
  const [url, setUrl] = useSearchParams();
  const tab = url.get("tab") || "overview";
  const auth = useAuth();
  const q = useQuery({
    queryKey: ["incident", incidentId],
    queryFn: async ({ signal }) =>
      checked(getIncident(incidentId, requestOptions(signal))),
  });
  const incident = (q.data as IncidentSuccessV2 | undefined)?.data;
  const rca = useQuery({
    queryKey: ["rca", incidentId],
    enabled: tab === "graph",
    queryFn: async ({ signal }) =>
      checked(getIncidentRCA(incidentId, requestOptions(signal))),
  });
  const content = useQuery({
    queryKey: ["incident-region", incidentId, tab, url.get("cursor")],
    enabled: !!incident && ["timeline", "findings", "resources"].includes(tab),
    queryFn: async ({ signal }) =>
      tab === "timeline"
        ? checked(
            getIncidentTimeline(
              incidentId,
              { limit: 50, cursor: url.get("cursor") || undefined },
              requestOptions(signal),
            ),
          )
        : tab === "findings"
          ? checked(
              getFindings(
                {
                  clusterUid: incident!.clusterUid,
                  incidentId,
                  limit: 50,
                  cursor: url.get("cursor") || undefined,
                },
                requestOptions(signal),
              ),
            )
          : checked(
              getResourceByCanonicalID(
                { canonicalId: incident!.resourceCanonicalId },
                requestOptions(signal),
              ),
            ),
  });
  const evidence = (id: string) => {
    const n = new URLSearchParams(url);
    n.set("tab", "evidence");
    n.set("evidenceId", id);
    setUrl(n);
  };
  const region = (
    <QueryState
      loading={content.isPending}
      error={content.error}
      retry={() => void content.refetch()}
    >
      {tab === "resources" ? (
        <Space orientation="vertical" className="full-width">
          <Space wrap>
            <Status value={record(record(content.data).data).freshness} />
            <Link
              to={
                "/resources/detail?canonicalId=" +
                encodeURIComponent(incident?.resourceCanonicalId ?? "")
              }
            >
              查看资源详情与关系
            </Link>
          </Space>
          {list(record(record(content.data).data).nodes).map((node, index) => (
            <Details
              key={text(record(node).canonicalId ?? index)}
              value={node}
            />
          ))}
        </Space>
      ) : Array.isArray(record(content.data).data) ? (
        <>
          <Table
            rowKey={(r, i) =>
              text(record(r).timelineId ?? record(r).findingId ?? i)
            }
            dataSource={list(record(content.data).data)}
            pagination={false}
            scroll={{ x: 600 }}
            columns={[
              {
                title: tab === "findings" ? "异常现象与关联证据" : "处理事件",
                render: (_, r) => (
                  <IncidentRecord
                    value={r}
                    kind={tab === "findings" ? "finding" : "timeline"}
                    onEvidence={evidence}
                  />
                ),
              },
            ]}
          />
          <Button
            disabled={!record(record(content.data).meta).nextCursor}
            onClick={() => {
              const n = new URLSearchParams(url);
              n.set(
                "cursor",
                text(record(record(content.data).meta).nextCursor),
              );
              setUrl(n);
            }}
          >
            下一页
          </Button>
        </>
      ) : (
        <Details value={record(content.data).data} />
      )}
    </QueryState>
  );
  return (
    <>
      <PageHeading
        title="故障处理工作台"
        description="先了解故障现象与影响，再核对证据、调查原因和执行本人确认的命令。"
        extra={
          <Link to="/incidents">
            <Button>返回故障列表</Button>
          </Link>
        }
      />
      <QueryState
        loading={q.isPending}
        error={q.error}
        retry={() => void q.refetch()}
      >
        {incident && (
          <>
            <section className="section">
              <Space wrap>
                <Status value={incident.state} />
                <span className="muted">记录版本 {incident.revision}</span>
                <details className="inline-details">
                  <summary>故障编号</summary>
                  <FullText value={incidentId} />
                </details>
              </Space>
              <Suspense fallback={<div role="status">正在加载工作台区域…</div>}>
                <Tabs
                  activeKey={tab}
                  destroyOnHidden
                  onChange={(v) => {
                    const n = new URLSearchParams(url);
                    n.set("tab", v);
                    n.delete("cursor");
                    setUrl(n);
                  }}
                  items={[
                    {
                      key: "overview",
                      label: "故障概况",
                      children: <FaultSummary incident={incident} />,
                    },
                    { key: "timeline", label: "处理时间线", children: region },
                    { key: "findings", label: "故障告警", children: region },
                    { key: "resources", label: "影响资源", children: region },
                    {
                      key: "graph",
                      label: "诊断关系图",
                      children: (
                        <QueryState
                          loading={rca.isPending}
                          error={rca.error}
                          retry={() => void rca.refetch()}
                        >
                          <p className="status-note">
                            此图展示该次根因分析记录的资源关系。请核对观测版本和新鲜度，历史关系可能已经变化。
                          </p>
                          <GraphView
                            scopeKey={"incident:" + incidentId}
                            value={
                              (rca.data as CurrentRCASuccessV2 | undefined)
                                ?.data.revision.inputManifest.graph
                            }
                          />
                        </QueryState>
                      ),
                    },
                    {
                      key: "evidence",
                      label: "故障证据",
                      children: <EvidencePanel incidentId={incidentId} />,
                    },
                    {
                      key: "rca",
                      label: "根因分析",
                      children: (
                        <RCAPanel
                          incidentId={incidentId}
                          onEvidence={evidence}
                        />
                      ),
                    },
                    {
                      key: "investigation",
                      label: "智能调查",
                      children: (
                        <InvestigationPanel
                          incident={incident}
                          onEvidence={evidence}
                        />
                      ),
                    },
                    {
                      key: "action",
                      label: "人工处置",
                      children: auth.session?.roles.includes("operator") ? (
                        <CommandComposer incident={incident} />
                      ) : (
                        <p>
                          人工命令需要运维操作员授权。管理权限不赋予执行权限。
                        </p>
                      ),
                    },
                    {
                      key: "audit",
                      label: "操作审计",
                      children: auth.session?.roles.includes(
                        "platform_admin",
                      ) ? (
                        <AuditPage incidentId={incidentId} embedded />
                      ) : (
                        <p>
                          审计记录需要平台管理员授权；操作流程仍显示自身执行与后检查结果。
                        </p>
                      ),
                    },
                  ].map((item) => ({
                    ...item,
                    children: (
                      <RegionBoundary key={item.key}>
                        {item.children}
                      </RegionBoundary>
                    ),
                  }))}
                />
              </Suspense>
            </section>
          </>
        )}
      </QueryState>
    </>
  );
}

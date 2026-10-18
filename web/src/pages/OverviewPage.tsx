import { useQuery } from "@tanstack/react-query";
import { Button, Alert, Table, Space } from "antd";
import { Link, useSearchParams } from "react-router-dom";
import { ScopeFilter, useClusters } from "../components/ScopeFilter";
import { getOverview, getCapabilities } from "../api/generated/platform";
import { checked, requestOptions } from "../api/client";
import { PageHeading } from "../components/PageHeading";
import { QueryState } from "../components/QueryState";
import { EnvelopeMeta } from "../components/EnvelopeMeta";
import { Details } from "../components/Details";
import { record, text, list, displayTime } from "../components/data";
import { Status } from "../components/Status";
export function OverviewPage() {
  const [url, setUrl] = useSearchParams();
  const clusters = useClusters();
  const cluster = url.get("clusterUid") || clusters[0] || "";
  const q = useQuery({
    queryKey: ["overview", cluster],
    enabled: !!cluster,
    queryFn: ({ signal }) =>
      checked(getOverview({ clusterUid: cluster }, requestOptions(signal))),
  });
  const capabilities = useQuery({
    queryKey: ["capabilities", cluster],
    enabled: !!cluster,
    queryFn: ({ signal }) =>
      checked(getCapabilities({ clusterUid: cluster }, requestOptions(signal))),
  });
  const d = record(record(q.data).data);
  const counts = record(d.counts);
  const domains = record(record(d.distribution).domains);
  const resourceLink = (domain: string, health?: string) =>
    "/resources?" +
    new URLSearchParams({
      clusterUid: cluster,
      domain,
      ...(health ? { health } : {}),
    }).toString();
  return (
    <>
      <PageHeading
        title="运行总览"
        description="先确认异常资源，再进入故障处理查看原因和处置进度。"
        extra={
          <Button
            onClick={() => {
              void q.refetch();
              void capabilities.refetch();
            }}
          >
            刷新观测
          </Button>
        }
      />
      <div className="filters">
        <ScopeFilter
          value={cluster}
          onChange={(v) => setUrl({ clusterUid: v })}
        />
      </div>
      <QueryState
        loading={q.isPending && !!cluster}
        error={q.error}
        noScope={!cluster}
        retry={() => void q.refetch()}
      >
        <EnvelopeMeta value={d.resourceObservation} />
        {d.resourceError !== null && d.resourceError !== undefined && (
          <Alert
            showIcon
            type="warning"
            title="资源观测暂不可用，资源和健康统计未知"
            description="告警和故障记录仍可查看。请刷新观测或检查能力与来源，不要把未知计数当作零。"
          />
        )}
        <div className="stats">
          {Object.entries(counts).map(([k, v]) => (
            <div className="stat" key={k}>
              <small>
                {(
                  {
                    finding: "告警记录总数",
                    activeFinding: "活动告警",
                    openIncident: "未确认故障（open）",
                    criticalIncident: "严重未确认故障",
                    runningInvestigation: "我的运行中调查",
                    incident: "故障记录",
                    resource: "可见资源",
                    cluster: "当前集群",
                    node: "可见节点",
                    server: "已观测硬件服务器",
                  } as Record<string, string>
                )[k] ?? k}
              </small>
              <strong>
                {v === null || v === undefined ? "未知" : text(v)}
              </strong>
            </div>
          ))}
        </div>
        {Object.keys(d).length > 0 && (
          <section className="section">
            <h2 className="section-title">健康分布与资源类型</h2>
            <p className="status-note">
              按资源归属分组；网络资源的健康不等于链路质量。未取得的分布显示未知。
            </p>
            <div className="stats">
              {(
                [
                  ["hardware", "硬件"],
                  ["kubernetes", "Kubernetes"],
                  ["network", "网络"],
                  ["storage", "存储"],
                ] as const
              ).map(([domain, label]) => {
                const group = record(domains[domain]);
                const health = record(group.byHealth);
                return (
                  <div className="stat" key={domain}>
                    <Link to={resourceLink(domain)}>{label}资源</Link>
                    <strong>
                      {typeof group.total === "number" ? group.total : "未知"}
                    </strong>
                    <Space wrap>
                      {Object.entries(health).map(([state, count]) => (
                        <Link key={state} to={resourceLink(domain, state)}>
                          <Status value={state} /> {text(count)}
                        </Link>
                      ))}
                    </Space>
                  </div>
                );
              })}
            </div>
            <Details
              value={{
                clusterUid: d.clusterUid,
                observedAt: d.observedAt,
                scope: d.scope,
                resourceObservation: d.resourceObservation,
                resourceError: d.resourceError,
              }}
            />
          </section>
        )}
      </QueryState>
      {Object.keys(d).length > 0 && (
        <section className="section">
          <h2 className="section-title">重点待处理故障</h2>
          <p className="status-note">
            仅列当前范围内 open 状态的 critical / warning 故障，严重优先，最多 5
            条。影响范围来自已授权的冻结 RCA Revision。
          </p>
          <Table<Record<string, unknown>>
            rowKey="incidentId"
            pagination={false}
            scroll={{ x: 900 }}
            dataSource={list(d.priorityIncidents).map(record)}
            locale={{ emptyText: "当前范围内没有符合条件的重点故障" }}
            columns={[
              {
                title: "故障",
                key: "title",
                render: (_, row) => (
                  <Link
                    to={
                      "/incidents/" + encodeURIComponent(String(row.incidentId))
                    }
                  >
                    {text(row.title)}
                  </Link>
                ),
              },
              {
                title: "主资源",
                dataIndex: "resourceCanonicalId",
                render: (id: unknown) => (
                  <Link
                    to={
                      "/resources/detail?canonicalId=" +
                      encodeURIComponent(String(id))
                    }
                  >
                    {text(id)}
                  </Link>
                ),
              },
              {
                title: "严重度",
                dataIndex: "severity",
                render: (v: unknown) => <Status value={v} />,
              },
              {
                title: "状态",
                dataIndex: "state",
                render: (v: unknown) => <Status value={v} />,
              },
              {
                title: "开始时间",
                dataIndex: "startedAt",
                render: displayTime,
              },
              {
                title: "持续时间",
                dataIndex: "durationSeconds",
                render: (v: unknown) =>
                  typeof v === "number"
                    ? Math.floor(v) + " 秒"
                    : "未知（时间未验证）",
              },
              {
                title: "RCA",
                dataIndex: "rcaStatus",
                render: (v: unknown) => <Status value={v} />,
              },
              {
                title: "影响摘要",
                dataIndex: "impact",
                render: (v: unknown) =>
                  v
                    ? "直接 " +
                      text(record(v).direct) +
                      " / 间接 " +
                      text(record(v).indirect) +
                      (record(v).partial ? "（部分）" : "")
                    : "尚无可授权读取的影响证据",
              },
            ]}
          />
        </section>
      )}
      <section className="section">
        <h2 className="section-title">故障处理入口</h2>
        <div className="filters">
          <Link to={"/incidents?clusterUid=" + encodeURIComponent(cluster)}>
            <Button type="primary">开始处理故障</Button>
          </Link>
          <Link to={"/findings?clusterUid=" + encodeURIComponent(cluster)}>
            <Button>查看故障告警</Button>
          </Link>
          <Link to={"/resources?clusterUid=" + encodeURIComponent(cluster)}>
            <Button>查询资源</Button>
          </Link>
        </div>
        <QueryState loading={capabilities.isPending} error={capabilities.error}>
          {capabilities.data && "data" in capabilities.data && (
            <>
              <p>
                调查{" "}
                <Status value={capabilities.data.data.investigation.status} />
                　人工命令{" "}
                <Status
                  value={capabilities.data.data.commandExecution.status}
                />
              </p>
              <Link to="/capabilities">查看完整能力及来源说明 →</Link>
            </>
          )}
        </QueryState>
      </section>
      <Alert
        type="info"
        showIcon
        title="硬件与虚拟化能力说明"
        description="虚拟机功能尚未启用。硬件设备因缺少管理接口尚未完成验收，不计入可用能力。模型调查结果需要结合证据核实。"
      />
    </>
  );
}

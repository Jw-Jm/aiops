import { useQuery } from "@tanstack/react-query";
import { Button, Alert } from "antd";
import { Link, useSearchParams } from "react-router-dom";
import { ScopeFilter, useClusters } from "../components/ScopeFilter";
import { getOverview, getCapabilities } from "../api/generated/platform";
import { checked, requestOptions } from "../api/client";
import { PageHeading } from "../components/PageHeading";
import { QueryState } from "../components/QueryState";
import { EnvelopeMeta } from "../components/EnvelopeMeta";
import { Details } from "../components/Details";
import { record, text } from "../components/data";
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
                    finding: "告警记录",
                    incident: "故障记录",
                    resource: "可见资源",
                    cluster: "当前集群",
                    node: "可见节点",
                    server: "硬件设备（待验收）",
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
            <div className="filters">
              {Object.entries(record(record(d.distribution).health)).map(
                ([k, v]) => (
                  <span key={k}>
                    <Status value={k} /> {text(v)}
                  </span>
                ),
              )}
            </div>
            <div className="filters">
              {Object.entries(record(record(d.distribution).resourceKinds)).map(
                ([k, v]) => (
                  <span key={k}>
                    {k}：{text(v)}
                  </span>
                ),
              )}
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

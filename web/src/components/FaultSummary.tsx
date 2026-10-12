import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Space } from "antd";
import { Link } from "react-router-dom";
import {
  getFindings,
  getResourceByCanonicalID,
} from "../api/generated/platform";
import type { IncidentV2 } from "../api/generated/models";
import { checked, requestOptions } from "../api/client";
import { QueryState } from "./QueryState";
import { Details } from "./Details";
import { Status } from "./Status";
import { record, list, text, displayTime } from "./data";
export function symptomLabel(value: unknown) {
  const s = text(value);
  const labels: Record<string, string> = {
    PVCUnbound: "存储卷尚未绑定",
    NotReady: "节点未就绪",
    NodeReadyUnknown: "节点就绪状态未知",
    Unschedulable: "工作负载无法调度",
    MemoryPressure: "节点内存紧张",
    DiskPressure: "节点磁盘空间紧张",
    PIDPressure: "节点进程数量压力",
    FailedMount: "存储卷挂载失败",
    FailedAttachVolume: "存储卷附加失败",
    ProvisioningFailed: "存储卷创建失败",
    PodRuntimeFault: "工作负载运行异常",
    NodeAnalyzerFault: "节点分析器发现异常",
    StorageAnalyzerFault: "存储分析器发现异常",
    PodCrashLoop: "容器反复重启",
    CrashLoopBackOff: "容器反复重启",
    PodPending: "工作负载等待调度",
    NodeNotReady: "节点尚未就绪",
    ImagePullBackOff: "容器镜像拉取失败",
    FailedScheduling: "工作负载调度失败",
    OOMKilled: "容器因内存不足退出",
  };
  const key = s.split("/").find((k) => Object.hasOwn(labels, k));
  return key ? labels[key] : s;
}
export function resourceIdentifier(value: unknown) {
  const s = text(value);
  try {
    const parts = new URL(s).pathname.split("/").filter(Boolean);
    return (
      (parts.at(-2) ?? "资源") + " · 标识 " + (parts.at(-1) ?? "").slice(-8)
    );
  } catch {
    return s;
  }
}
export function FaultSummary({ incident }: { incident: IncidentV2 }) {
  const resource = useQuery({
    queryKey: ["resource", incident.resourceCanonicalId],
    queryFn: ({ signal }) =>
      checked(
        getResourceByCanonicalID(
          { canonicalId: incident.resourceCanonicalId },
          requestOptions(signal),
        ),
      ),
  });
  const findings = useQuery({
    queryKey: ["incident-summary-findings", incident.incidentId],
    queryFn: ({ signal }) =>
      checked(
        getFindings(
          {
            incidentId: incident.incidentId,
            clusterUid: incident.clusterUid,
            limit: 50,
          },
          requestOptions(signal),
        ),
      ),
  });
  const node = list(record(record(resource.data).data).nodes).map(record)[0];
  const rows = list(record(findings.data).data).map(record);
  return (
    <div className="fault-summary">
      <h2 className="section-title">当前故障</h2>
      <QueryState
        loading={resource.isPending}
        error={resource.error}
        retry={() => void resource.refetch()}
      >
        <p className="fault-resource">
          {node
            ? text(node.name)
            : resourceIdentifier(incident.resourceCanonicalId)}
        </p>
        {!node && !resource.isPending && (
          <p className="muted">
            当前查询未返回资源名称，请核对资源是否已删除或来源是否失效。
          </p>
        )}
      </QueryState>
      <Space wrap>
        <Status value={incident.state} />
        <span>命名空间：{text(incident.namespace)}</span>
        <span>最近观测：{displayTime(incident.lastObservedAt)}</span>
      </Space>
      <QueryState
        loading={findings.isPending}
        error={findings.error}
        retry={() => void findings.refetch()}
      >
        {rows.length > 0 ? (
          rows.map((row) => (
            <Alert
              key={text(row.findingId)}
              className="status-note"
              showIcon
              type={row.severity === "critical" ? "error" : "warning"}
              title={symptomLabel(row.normalizedSymptom ?? row.ruleId)}
              description={
                <>
                  <Status value={row.lifecycleState} />
                  <span>
                    {" "}
                    ·{" "}
                    {symptomLabel(
                      record(row.payload).message ??
                        record(row.payload).reason ??
                        "查看故障告警，核对检测规则与原始证据。",
                    )}
                  </span>
                </>
              }
            />
          ))
        ) : (
          <p className="muted">
            当前没有可见的关联告警，不能仅凭故障记录判断原因。
          </p>
        )}
      </QueryState>
      <h3>下一步</h3>
      <p className="muted">
        先核对告警和证据。原因不明确时发起智能调查；准备好命令后再进入人工处置。
      </p>
      <div className="workflow-links">
        {[
          ["findings", "1 · 核对故障告警", "了解异常现象与来源"],
          ["rca", "2 · 查看根因分析", "区分已确认原因与待查线索"],
          ["investigation", "3 · 智能调查", "读取证据，整理可能原因"],
          ["action", "4 · 人工处置", "输入命令、评估风险并本人确认"],
        ].map(([tab, title, description]) => (
          <Link key={tab} to={"?tab=" + tab}>
            <strong>{title}</strong>
            <span>{description}</span>
          </Link>
        ))}
      </div>
      <details className="technical-details">
        <summary>完整故障记录</summary>
        <Details value={incident} />
      </details>
    </div>
  );
}

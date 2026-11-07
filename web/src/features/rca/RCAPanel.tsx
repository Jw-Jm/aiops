import { stateLabel, diagnosisLabel } from "../../components/presentation";
import { resourceIdentifier } from "../../components/FaultSummary";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Select, Space, Descriptions } from "antd";
import {
  getIncidentRCA,
  getRCARevisions,
  getRCARevision,
} from "../../api/generated/platform";
import type {
  CurrentRCASuccessV2,
  RCARevisionPageV2,
  RCARevisionSuccessV2,
} from "../../api/generated/models";
import { checked, requestOptions } from "../../api/client";
import { QueryState } from "../../components/QueryState";
import { Details } from "../../components/Details";
import { Status } from "../../components/Status";
import { revisionDiff } from "./diff";
export function RCAPanel({
  incidentId,
  onEvidence,
}: {
  incidentId: string;
  onEvidence: (id: string) => void;
}) {
  const [compare, setCompare] = useState<number>();
  const [revisionCursor, setRevisionCursor] = useState<string>();
  const q = useQuery({
    queryKey: ["rca", incidentId],
    queryFn: ({ signal }) =>
      checked(getIncidentRCA(incidentId, requestOptions(signal))),
  });
  const revisions = useQuery({
    queryKey: ["rca-revisions", incidentId, revisionCursor],
    queryFn: ({ signal }) =>
      checked(
        getRCARevisions(
          incidentId,
          { limit: 50, cursor: revisionCursor },
          requestOptions(signal),
        ),
      ),
  });
  const old = useQuery({
    queryKey: ["rca-revision", incidentId, compare],
    enabled: compare !== undefined,
    queryFn: ({ signal }) =>
      checked(
        getRCARevision(incidentId, String(compare!), requestOptions(signal)),
      ),
  });
  const current = (q.data as CurrentRCASuccessV2 | undefined)?.data;
  const revision = current?.revision;
  const previous = (old.data as RCARevisionSuccessV2 | undefined)?.data;
  return (
    <>
      <QueryState
        loading={q.isPending}
        error={q.error}
        retry={() => void q.refetch()}
      >
        {revision && (
          <>
            <Alert
              type={current?.currentEligible ? "info" : "warning"}
              showIcon
              title={
                current?.currentEligible
                  ? "此分析与当前故障版本一致"
                  : "这是历史分析，故障状态可能已发生变化"
              }
              description={current?.currentDegradedSources
                .map(diagnosisLabel)
                .join(" · ")}
            />
            <Space wrap>
              <Status value={revision.result.status} />
              <span>
                分析版本 {revision.revision} · {stateLabel(revision.source)} ·{" "}
                {revision.actor === "deterministic-worker"
                  ? "自动规则诊断"
                  : revision.actor}
              </span>
            </Space>
            {revision.result.partial && (
              <Alert
                type="warning"
                showIcon
                title="部分结果：分析未完整完成"
                description="本次分析因预算、中断或来源缺失没有跑完全部检查，以下候选和证据核对都基于不完整的输入，不能当作完整结论使用。"
              />
            )}
            <p className="status-note">
              {revision.result.status === "confirmed"
                ? "规则诊断已确认根因，仍需通过证据核对判断依据后才能据此处置。"
                : revision.result.status === "probable"
                  ? "当前结论是最可能的假设，尚未确认。处置前必须先核对支持与矛盾证据。"
                  : "根因未确定：以下候选均为待验证假设，不能据此认定故障原因。"}
              {" "}规则诊断与模型判断分别记录。
            </p>
            {revision.result.candidates.map((c) => (
              <section className="section" key={c.candidateKey}>
                <h3>
                  {(
                    {
                      dimm_hardware_fault: "内存硬件故障",
                      csi_provisioner_failure: "存储供应组件异常",
                      node_runtime_failure: "节点运行环境异常",
                      pod_image_pull_failure: "镜像拉取失败",
                      service_no_ready_endpoints: "服务无可用后端",
                      pod_crash_loop: "容器崩溃循环",
                      pod_oom_killed: "内存超限被终止（OOMKilled）",
                      pod_probe_failure: "容器探针失败",
                      deployment_rollout_stuck: "发布卡住（进度超时）",
                      pod_scheduling_failure: "调度失败",
                    } as Record<string, string>
                  )[c.candidateType] ?? c.candidateType}
                  {revision.result.status !== "confirmed" ? "（待验证假设）" : ""}
                </h3>
                <Descriptions
                  size="small"
                  column={1}
                  items={[
                    {
                      key: "source",
                      label: "判断来源",
                      children: stateLabel(c.source),
                    },
                    {
                      key: "target",
                      label: "关联资源",
                      children: resourceIdentifier(c.resourceCanonicalId),
                    },
                    {
                      key: "score",
                      label: "候选排序分数",
                      children:
                        String(c.score) + "（用于比较候选，不是故障概率）",
                    },
                    {
                      key: "basis",
                      label: "分析依据",
                      children: c.provenance.length ? (
                        <ul>
                          {c.provenance.map((value, index) => (
                            <li key={index} title={value}>
                              {diagnosisLabel(value)}
                            </li>
                          ))}
                        </ul>
                      ) : (
                        "未提供分析依据，不能据此确认原因"
                      ),
                    },
                  ]}
                />
                <p className="muted">
                  通过下方证据核对判断依据；缺少证据或存在冲突时，需要继续调查。
                </p>
                <details className="technical-details">
                  <summary>完整候选记录</summary>
                  <Details value={c} />
                </details>
                <Space wrap>
                  {c.evidenceRefs.map((id) => (
                    <Button key={id} onClick={() => onEvidence(id)}>
                      查看证据 {id.slice(-8)}
                    </Button>
                  ))}
                </Space>
              </section>
            ))}
            <h3>证据核对结果</h3>
            <Descriptions
              column={1}
              size="small"
              bordered
              items={[
                {
                  key: "supporting",
                  label: "支持判断的证据",
                  children: revision.result.evidenceBundle.supporting.length
                    ? revision.result.evidenceBundle.supporting.map((id) => (
                        <Button
                          type="link"
                          key={id}
                          onClick={() => onEvidence(id)}
                        >
                          查看证据 {id.slice(-8)}
                        </Button>
                      ))
                    : "尚无支持证据",
                },
                {
                  key: "missing",
                  label: "仍缺少的证据",
                  children: revision.result.evidenceBundle.missing.length ? (
                    <ul>
                      {revision.result.evidenceBundle.missing.map(
                        (value, index) => (
                          <li key={index} title={value}>
                            {diagnosisLabel(value)}
                          </li>
                        ),
                      )}
                    </ul>
                  ) : (
                    "未报告缺失证据"
                  ),
                },
                {
                  key: "contradicting",
                  label: "与判断矛盾的证据",
                  children: revision.result.evidenceBundle.contradicting.length
                    ? revision.result.evidenceBundle.contradicting.map((id) => (
                        <Button
                          type="link"
                          key={id}
                          onClick={() => onEvidence(id)}
                        >
                          查看矛盾证据 {id.slice(-8)}
                        </Button>
                      ))
                    : "未报告矛盾证据",
                },
                ...(revision.result.evidenceBundle.degradedSources.length
                  ? [
                      {
                        key: "degraded",
                        label: "分析时不可用来源",
                        children: (
                          <ul>
                            {revision.result.evidenceBundle.degradedSources.map(
                              (value, index) => (
                                <li key={index} title={value}>
                                  {diagnosisLabel(value)}
                                </li>
                              ),
                            )}
                          </ul>
                        ),
                      },
                    ]
                  : []),
              ]}
            />
            <details className="technical-details">
              <summary>完整分析结果</summary>
              <Details value={revision.result} />
            </details>
            <Space wrap className="status-note">
              <span>对比历史分析</span>
              <Select
                aria-label="比较历史 RCA revision"
                value={compare}
                style={{ minWidth: 180 }}
                onChange={setCompare}
                options={(
                  revisions.data as RCARevisionPageV2 | undefined
                )?.data.map((r) => ({
                  value: r.revision,
                  label: "分析版本 " + r.revision,
                }))}
              />
              <Button
                disabled={!revisionCursor}
                onClick={() => setRevisionCursor(undefined)}
              >
                最新分析
              </Button>
              <Button
                disabled={
                  !(revisions.data as RCARevisionPageV2 | undefined)?.meta
                    .nextCursor
                }
                onClick={() =>
                  setRevisionCursor(
                    (revisions.data as RCARevisionPageV2 | undefined)?.meta
                      .nextCursor ?? undefined,
                  )
                }
              >
                更早分析
              </Button>
            </Space>
            <QueryState loading={old.isFetching} error={old.error}>
              {previous && <Details value={revisionDiff(previous, revision)} />}
            </QueryState>
          </>
        )}
      </QueryState>
      {revisions.error && (
        <QueryState loading={false} error={revisions.error} />
      )}
    </>
  );
}

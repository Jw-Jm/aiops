import { investigationFailure } from "../../components/investigationFailure";
import { record, text, list } from "../../components/data";
import { Table, Button, Space } from "antd";
import type { InvestigationStepV2 } from "../../api/generated/models";
import { Status } from "../../components/Status";
import { Details, FullText } from "../../components/Details";
export function StepLedger({
  steps,
  onEvidence,
}: {
  steps: InvestigationStepV2[];
  onEvidence?: (id: string) => void;
}) {
  return (
    <Table<InvestigationStepV2>
      rowKey="stepId"
      dataSource={steps.slice(0, 128)}
      pagination={{ pageSize: 20, showSizeChanger: false }}
      scroll={{ x: 650 }}
      expandable={{
        expandedRowRender: (s) => (
          <>
            <h4>输入范围和预算</h4>
            {Object.keys(record(s.argsSummary)).length ? (
              <Details value={s.argsSummary} />
            ) : (
              <p>此历史步骤未记录输入摘要；不推测原始输入。</p>
            )}
            <h4>结果详情</h4>
            <Details
              value={{
                toolName: s.toolName,
                argsDigest: s.argsDigest,
                result: s.result,
                errorCode: s.errorCode,
              }}
            />
          </>
        ),
      }}
      columns={[
        {
          title: "关联证据",
          render: (_, row) => (
            <Space wrap>
              {list(record(row.result).evidenceRefs)
                .filter(
                  (id): id is string => typeof id === "string" && id.length > 0,
                )
                .slice(0, 128)
                .map((id) => (
                  <Button
                    key={id}
                    title={id}
                    disabled={!onEvidence}
                    onClick={() => onEvidence?.(id)}
                  >
                    查看证据 {id.slice(-8)}
                  </Button>
                ))}
            </Space>
          ),
        },
        {
          title: "调查步骤",
          dataIndex: "toolName",
          render: (value: string) => (
            <span title={value}>
              {(
                {
                  model: "分析现有证据",
                  get_incident_context: "读取故障上下文",
                  get_resource_context: "读取资源详情",
                  get_resource_health: "检查资源健康",
                  get_dependencies: "查看依赖关系",
                  get_impact_scope: "检查影响范围",
                  get_findings: "读取故障告警",
                  get_ranked_evidence: "整理关联证据",
                  get_evidence_conflicts: "检查证据冲突",
                  get_recent_changes: "检查近期变更",
                  get_root_cause_candidates: "读取根因候选",
                  query_metrics: "查询监控指标",
                  query_logs: "查询日志",
                  query_deepflow: "查询网络观测",
                  query_kubernetes: "查询 Kubernetes 状态",
                  query_hardware: "查询硬件状态",
                } as Record<string, string>
              )[value] ?? value}
            </span>
          ),
        },
        {
          title: "状态",
          dataIndex: "state",
          render: (v) => <Status value={v} />,
        },
        {
          title: "结果摘要",
          render: (_, row) => {
            const result = record(row.result);
            const summary = result.summary ?? record(result.data).summary;
            return typeof summary === "string" ? (
              <FullText value={summary} />
            ) : row.result !== undefined ? (
              "结果已记录，展开查看详情"
            ) : (
              "尚未产生结果"
            );
          },
        },
        {
          title: "错误",
          dataIndex: "errorCode",
          render: (v) =>
            v ? (
              <span title={v}>
                {investigationFailure(v) ?? "步骤失败，展开查看错误详情"}
              </span>
            ) : (
              <span>无</span>
            ),
        },
      ]}
    />
  );
}

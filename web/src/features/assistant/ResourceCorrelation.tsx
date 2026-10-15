import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  Drawer,
  List,
  Space,
  Tag,
  Typography,
} from "antd";
import type { AssistantFaultChainContextV1 } from "../../api/generated/models";
import { getAssistantResourceSourceStep } from "../../api/generated/platform";
import { checked, requestOptions } from "../../api/client";
import { QueryState } from "../../components/QueryState";

export function ResourceCorrelation({
  context,
  turnId,
  scopeKey,
  onReadOnlyCheck,
}: {
  context: Pick<
    AssistantFaultChainContextV1,
    "sourceSteps" | "resourceAssessments"
  >;
  turnId: string;
  scopeKey: string;
  onReadOnlyCheck?: (message: string) => void;
}) {
  const [step, setStep] = useState<string>();
  const query = useQuery({
    queryKey: ["assistant", scopeKey, "source-step", turnId, step],
    enabled: !!step,
    queryFn: ({ signal }) =>
      checked(
        getAssistantResourceSourceStep(turnId, step!, requestOptions(signal)),
      ),
  });
  if (!context.sourceSteps?.length) return null;
  const locate = (id: string) => (
    <Button size="small" key={id} onClick={() => setStep(id)}>
      定位来源步骤 {id}
    </Button>
  );
  return (
    <Space orientation="vertical" style={{ width: "100%" }}>
      <Typography.Title level={5}>资源关联观察</Typography.Title>
      <Alert
        type="info"
        title="健康快照、Finding、原生事件和执行记录分别保留。无历史不补零，版本变化不代表实际配置变化，指标改善不代表完整恢复。"
      />
      <List
        dataSource={context.resourceAssessments || []}
        locale={{
          emptyText: "没有可发布的有限规则判断；查看已收集来源及缺口。",
        }}
        renderItem={(a) => (
          <List.Item>
            <Card size="small" title={a.ruleId} style={{ width: "100%" }}>
              <div>{a.canonicalId}</div>
              <p>{a.reason}</p>
              <Tag>{a.sourceQuality}</Tag>
              <div>
                支持：{a.supports.length ? a.supports.map(locate) : "无"}
              </div>
              <div>
                反证：{a.conflicts.length ? a.conflicts.map(locate) : "无"}
              </div>
              <div>缺口：{a.missingEvidence.join("、")}</div>
              <div>
                已发布只读检查：
                {a.nextChecks
                  .filter((tool) =>
                    [
                      "resource_history",
                      "resource_findings",
                      "resource_changes",
                      "execute_analysis_query",
                    ].includes(tool),
                  )
                  .map((tool) => (
                    <Button
                      key={tool}
                      size="small"
                      disabled={!onReadOnlyCheck}
                      onClick={() =>
                        onReadOnlyCheck?.(
                          `请在原冻结窗口和当前授权范围内，对资源 ${a.canonicalId} 使用已发布只读工具 ${tool} 补证。窗口角色 fault 由服务器解析，不改 UID、范围、窗口或规则；若需要更多权限，提示使用现有范围编辑器。`,
                        )
                      }
                    >
                      准备补查 {tool}
                    </Button>
                  ))}
              </div>
            </Card>
          </List.Item>
        )}
      />
      <List
        size="small"
        dataSource={context.sourceSteps}
        renderItem={(s) => (
          <List.Item>
            {s.tool} · {s.canonicalId}
            {locate(s.stepId)}
          </List.Item>
        )}
      />
      <Drawer
        title="原始已提交来源（安全投影）"
        open={!!step}
        onClose={() => setStep(undefined)}
        width="min(960px, 100vw)"
      >
        <QueryState
          loading={query.isPending}
          error={query.error}
          retry={() => void query.refetch()}
        >
          <pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
            {query.data ? JSON.stringify(query.data.data, null, 2) : ""}
          </pre>
        </QueryState>
      </Drawer>
    </Space>
  );
}

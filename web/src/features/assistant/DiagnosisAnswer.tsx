import { Alert, Card, List, Space, Tag, Typography } from "antd";
import { Link } from "react-router-dom";
import type { AssistantAnswerV1 } from "../../api/generated/models";
import { AssistantEvidence } from "./AssistantEvidence";
import { FormalReferenceView } from "./FormalReferenceView";
import { QueryResultView } from "./QueryResultView";
import type { FaultChainCheckControls } from "./FaultChainGapCheck";
import { ResourceCorrelation } from "./ResourceCorrelation";
import { FaultChainPanel } from "./FaultChainPanel";

function warningLabel(value: string) {
  if (value.startsWith("source_read_failed:"))
    return `来源读取失败：${value.slice(19)}`;
  if (value.startsWith("source_read_partial:"))
    return `来源证据不完整：${value.slice(20)}`;
  return (
    (
      {
        model_answer_incomplete: "模型回答不完整",
        model_explanation_incomplete: "模型未提供可用解释",
        MODEL_OUTPUT_INVALID: "模型输出格式无效，已保留取证结果",
        model_explanation_not_available: "模型解释不可用，已提交证据仍可读取",
        remaining_checks_not_completed: "剩余检查尚未完成",
        model_unsupported_reason_not_provided: "模型未说明无法解释的原因",
        query_result_incomplete: "查询结果不完整",
        query_not_executed: "应执行的查询尚未执行",
        BUDGET_EXHAUSTED: "本轮预算已耗尽",
        TIME_BUDGET_EXHAUSTED: "本轮执行时间已耗尽",
        CANCELLED: "本轮已取消",
        "unreliable-event-time": "事件时间资格不足",
      } as Record<string, string>
    )[value] || value
  );
}
export function DiagnosisAnswer({
  answer,
  turnId,
  onReadOnlyCheck,
  checkControls,
}: {
  answer: AssistantAnswerV1;
  turnId?: string;
  onReadOnlyCheck?: (message: string) => void;
  checkControls?: FaultChainCheckControls;
}) {
  return (
    <Space orientation="vertical" style={{ width: "100%" }}>
      <Space>
        <Tag>
          {answer.answerKind === "data"
            ? "数据回答"
            : answer.answerKind === "diagnosis"
              ? "诊断假设"
              : answer.answerKind === "clarification"
                ? "需要澄清"
                : "暂不支持"}
        </Tag>
        {answer.partial && <Tag color="orange">部分回答</Tag>}
        <Typography.Text type="secondary">{answer.asOf}</Typography.Text>
      </Space>
      {answer.answerKind === "data" ? (
        <Typography.Text type="secondary">
          以下数字取自服务端验证的查询结果。
        </Typography.Text>
      ) : (
        <Typography.Paragraph style={{ whiteSpace: "pre-wrap" }}>
          {answer.summary}
        </Typography.Paragraph>
      )}
      {answer.clarification && (
        <Alert
          type="info"
          title={answer.clarification.question}
          description={[
            ...answer.clarification.missingFields,
            ...answer.clarification.candidates,
          ].join("；")}
        />
      )}
      {!answer.faultChainContextRef &&
        !!answer.resourceSourceSteps?.length &&
        turnId && (
          <ResourceCorrelation
            onReadOnlyCheck={onReadOnlyCheck}
            context={{ sourceSteps: answer.resourceSourceSteps }}
            turnId={turnId}
            scopeKey={turnId}
          />
        )}
      {answer.faultChainContextRef && turnId && (
        <FaultChainPanel
          answer={answer}
          turnId={turnId}
          onReadOnlyCheck={onReadOnlyCheck}
          checkControls={checkControls}
        />
      )}
      {!answer.faultChainContextRef &&
        answer.queryResultRefs.map((id) => (
          <QueryResultView key={id} id={id} answer={answer} />
        ))}
      {answer.hypotheses.map((h) => (
        <Card key={h.key} size="small" title={h.summary}>
          <Typography.Paragraph type="secondary">
            此处是待验证的假设；资源关系说明依赖或影响，不能单独证明因果。
          </Typography.Paragraph>
          <Typography.Text>支持证据</Typography.Text>
          {h.supports.length > 0 ? (
            <AssistantEvidence refs={h.supports} />
          ) : (
            <Typography.Paragraph type="secondary">
              尚无已引用的支持证据
            </Typography.Paragraph>
          )}
          <Typography.Text>冲突证据</Typography.Text>
          {h.conflicts.length > 0 ? (
            <AssistantEvidence refs={h.conflicts} />
          ) : (
            <Typography.Paragraph type="secondary">
              尚无已引用的冲突证据，不代表已排除其他原因
            </Typography.Paragraph>
          )}
          <List
            size="small"
            dataSource={[
              `待补证据：${h.missingEvidence.join("、") || "本轮未列出"}`,
              `下一步检查：${h.nextChecks.join("、") || "本轮未列出"}`,
            ]}
            renderItem={(item) => <List.Item>{item}</List.Item>}
          />
        </Card>
      ))}
      {!answer.faultChainContextRef &&
        (answer.faultChain || []).map((link) => (
          <Card
            key={link.relationDigest}
            size="small"
            title={`${link.from} → ${link.to}`}
          >
            <Tag>关系：{link.kind}</Tag>
            <Tag color="orange">待验证的故障传播假设</Tag>
            <Typography.Paragraph>
              关系观测不能单独证明因果。
            </Typography.Paragraph>
            <Typography.Text>支持证据</Typography.Text>
            <AssistantEvidence refs={link.supports} />
            <Typography.Text>冲突证据</Typography.Text>
            {link.conflicts.length ? (
              <AssistantEvidence refs={link.conflicts} />
            ) : (
              <Typography.Paragraph>
                尚无已引用冲突，不代表已排除其他原因。
              </Typography.Paragraph>
            )}
            <Typography.Paragraph>
              待补证据：{link.missingEvidence.join("；")}
            </Typography.Paragraph>
            <Typography.Paragraph>
              下一步：{link.nextChecks.join("；") || "请追问需要补充的检查"}
            </Typography.Paragraph>
          </Card>
        ))}
      {turnId &&
        (answer.formalRefs || []).map((ref, index) => (
          <FormalReferenceView
            key={`${ref.kind}:${ref.contentDigest}`}
            refValue={ref}
            index={index}
            turnId={turnId}
          />
        ))}
      {answer.entityRefs.map((id) => (
        <Link
          key={id}
          to={`/resources/detail?canonicalId=${encodeURIComponent(id)}`}
        >
          查看资源：{id}
        </Link>
      ))}
      {answer.evidenceRefs.length > 0 && (
        <AssistantEvidence refs={answer.evidenceRefs} />
      )}
      {[
        ...answer.missingEvidence,
        ...answer.warnings,
        ...answer.degradedSources,
        ...answer.nextChecks,
      ].length > 0 && (
        <Space orientation="vertical" style={{ width: "100%" }}>
          {[
            {
              title: "待补证据",
              items: answer.missingEvidence.map(warningLabel),
            },
            { title: "本轮状态原因", items: answer.warnings.map(warningLabel) },
            { title: "退化来源", items: answer.degradedSources },
            { title: "下一步只读检查（尚未执行）", items: answer.nextChecks },
          ]
            .filter((group) => group.items.length > 0)
            .map((group) => (
              <List
                key={group.title}
                size="small"
                header={group.title}
                dataSource={group.items}
                renderItem={(item) => <List.Item>{item}</List.Item>}
              />
            ))}
        </Space>
      )}
      <Typography.Text type="secondary">{answer.scopeSummary}</Typography.Text>
    </Space>
  );
}

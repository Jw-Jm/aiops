import { Alert, Card, List, Space, Tag, Typography } from "antd";
import { Link } from "react-router-dom";
import type { AssistantAnswerV1 } from "../../api/generated/models";
import { AssistantEvidence } from "./AssistantEvidence";
import { QueryResultView } from "./QueryResultView";
export function DiagnosisAnswer({ answer }: { answer: AssistantAnswerV1 }) {
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
      {answer.queryResultRefs.map((id) => (
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
        <List
          size="small"
          dataSource={[
            ...answer.missingEvidence,
            ...answer.warnings,
            ...answer.degradedSources,
            ...answer.nextChecks,
          ]}
          renderItem={(item) => <List.Item>{item}</List.Item>}
        />
      )}
      <Typography.Text type="secondary">{answer.scopeSummary}</Typography.Text>
    </Space>
  );
}

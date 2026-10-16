import { afterEach, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { DiagnosisAnswer } from "../src/features/assistant/DiagnosisAnswer";
import type { AssistantAnswerV1 } from "../src/api/generated/models";

afterEach(cleanup);
it("separates source failures, model incompleteness and unexecuted suggestions", () => {
  const answer: AssistantAnswerV1 = {
    schemaVersion: "assistant-answer/v1",
    answerKind: "diagnosis",
    summary: "保留已提交证据",
    asOf: "2026-10-16T00:00:00Z",
    scopeSummary: "bound",
    partial: true,
    factRefs: [],
    queryResultRefs: [],
    entityRefs: [],
    evidenceRefs: [],
    hypotheses: [],
    missingEvidence: ["缺少退出日志"],
    degradedSources: [],
    warnings: [
      "source_read_failed:query_logs:SOURCE_DEGRADED",
      "source_read_partial:resource_history",
      "model_answer_incomplete",
      "BUDGET_EXHAUSTED",
    ],
    nextChecks: ["读取日志"],
  };
  render(<DiagnosisAnswer answer={answer} />);
  expect(
    screen.getByText("来源读取失败：query_logs:SOURCE_DEGRADED"),
  ).toBeTruthy();
  expect(screen.getByText("来源证据不完整：resource_history")).toBeTruthy();
  expect(screen.getByText("模型回答不完整")).toBeTruthy();
  expect(screen.getByText("本轮预算已耗尽")).toBeTruthy();
  expect(screen.getByText("下一步只读检查（尚未执行）")).toBeTruthy();
  expect(screen.getByText("缺少退出日志")).toBeTruthy();
});

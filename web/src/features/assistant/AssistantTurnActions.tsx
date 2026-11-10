import { useEffect, useRef, useState } from "react";
import { Button, Space, Tooltip, Typography, message } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import type {
  AssistantAnswerV1,
  AssistantStoredQueryResultV1,
} from "../../api/generated/models";
import { ApiError, checked, requestOptions } from "../../api/client";
import {
  getAssistantQueryResult,
  getAssistantTurn,
} from "../../api/generated/platform";
import { useAssistantScopeKey } from "./scope";
import {
  buildResultCopyPayload,
  resultCopyText,
  type ResultCopyPayload,
} from "./resultActions";

// The turn level output actions of one assistant answer. Every action of this
// bar works on the answer the user is looking at: the summary is copied and
// exported from the structured answer of this turn, never from the document
// body, so a second conversation that happens to be mounted on the same page
// can never leak into the copy or the export.
//
// The content is limited to the public business fields of the answer: the
// fixed identifiers of the referenced facts, the committed numbers of the
// submitted query results, the references, the hypotheses and the warnings.
// The authorization binding, the grant snapshot and the raw model input are
// not part of the answer contract and are therefore never copied or exported.
export type TurnCopyResult = ResultCopyPayload;

export interface TurnCopyPayload {
  schemaVersion: "assistant-turn-export/v1";
  conversationId?: string;
  turnId?: string;
  asOf: string;
  scopeSummary: string;
  answerKind: string;
  // The summary of the model is an explanation, never a number. A data answer
  // does not show it in the product either, so it is not exported as the
  // answer of the turn.
  summary?: string;
  partial: boolean;
  queryResultRefs: string[];
  factRefs: { queryResultId: string; factId: string }[];
  evidenceRefs: string[];
  formalRefs: {
    kind: string;
    incidentId: string;
    revision: number;
    contentDigest: string;
  }[];
  entityRefs: string[];
  results: TurnCopyResult[];
  hypotheses: {
    key: string;
    summary: string;
    supports: string[];
    conflicts: string[];
    missingEvidence: string[];
  }[];
  warnings: string[];
  degradedSources: string[];
  missingEvidence: string[];
  nextChecks: string[];
  clarification?: {
    clarificationId: string;
    question: string;
    missingFields: string[];
    candidates: string[];
  };
  sourceStatement: string;
}

// The source statement travels with every copy and every export: the numbers
// of the answer come from the persisted query results of the platform, and
// the explanation of the model never replaces a number.
export const turnSourceStatement =
  "数据与数字来自平台已提交并持久化的查询结果；模型解释只解释已提交结果，不改变任何数字。";

// The frozen context of one click: the scope, the turn and the epoch of the
// render the click happened in. Every await boundary compares it with the
// context of the current render.
interface FrozenContext {
  scope: string;
  epoch: number;
}

function turnResult(
  queryResultId: string,
  stored: AssistantStoredQueryResultV1,
): TurnCopyResult {
  return buildResultCopyPayload(queryResultId, stored)!;
}

export function buildTurnCopyPayload(
  answer: AssistantAnswerV1,
  identity?: { conversationId?: string; turnId?: string },
  results: TurnCopyResult[] = [],
): TurnCopyPayload {
  return {
    schemaVersion: "assistant-turn-export/v1",
    conversationId: identity?.conversationId,
    turnId: identity?.turnId,
    asOf: answer.asOf,
    scopeSummary: answer.scopeSummary,
    answerKind: answer.answerKind,
    summary: answer.answerKind === "data" ? undefined : answer.summary,
    partial: answer.partial,
    queryResultRefs: answer.queryResultRefs || [],
    factRefs: answer.factRefs.map((ref) => ({
      queryResultId: ref.queryResultId,
      factId: ref.factId,
    })),
    evidenceRefs: answer.evidenceRefs || [],
    formalRefs: (answer.formalRefs || []).map((ref) => ({
      kind: ref.kind,
      incidentId: ref.incidentId,
      revision: ref.revision,
      contentDigest: ref.contentDigest,
    })),
    entityRefs: answer.entityRefs || [],
    results,
    hypotheses: answer.hypotheses.map((hypothesis) => ({
      key: hypothesis.key,
      summary: hypothesis.summary,
      supports: hypothesis.supports,
      conflicts: hypothesis.conflicts,
      missingEvidence: hypothesis.missingEvidence,
    })),
    warnings: answer.warnings,
    degradedSources: answer.degradedSources,
    missingEvidence: answer.missingEvidence,
    nextChecks: answer.nextChecks,
    clarification: answer.clarification
      ? {
          clarificationId: answer.clarification.clarificationId,
          question: answer.clarification.question,
          missingFields: answer.clarification.missingFields,
          candidates: answer.clarification.candidates,
        }
      : undefined,
    sourceStatement: turnSourceStatement,
  };
}

export function turnCopyText(payload: TurnCopyPayload) {
  const lines = [
    `助手回答摘要（${payload.answerKind}）`,
    `数据时点：${payload.asOf}`,
    `范围摘要：${payload.scopeSummary}`,
    payload.partial ? "完整性：部分结果" : "完整性：已提交结果",
  ];
  if (payload.summary)
    lines.push(
      `模型解释（非数据来源，数字以已提交结果为准）：${payload.summary}`,
    );
  for (const result of payload.results) {
    lines.push(resultCopyText(result));
  }
  if (payload.queryResultRefs.length)
    lines.push(`引用查询结果：${payload.queryResultRefs.join("，")}`);
  if (payload.factRefs.length) {
    lines.push(
      `引用事实：${payload.factRefs
        .map((ref) => `${ref.queryResultId}/${ref.factId}`)
        .join("，")}`,
    );
  }
  if (payload.evidenceRefs.length)
    lines.push(`引用证据：${payload.evidenceRefs.join("，")}`);
  if (payload.formalRefs.length)
    lines.push(
      `正式引用：${payload.formalRefs
        .map(
          (ref) =>
            `${ref.kind}/${ref.incidentId}#${ref.revision}/${ref.contentDigest}`,
        )
        .join("，")}`,
    );
  if (payload.entityRefs.length)
    lines.push(`引用实体：${payload.entityRefs.join("，")}`);
  for (const hypothesis of payload.hypotheses) {
    lines.push(
      `假设 ${hypothesis.key}：${hypothesis.summary}${
        hypothesis.supports.length
          ? `（支持：${hypothesis.supports.join("，")}）`
          : ""
      }${
        hypothesis.conflicts.length
          ? `（冲突：${hypothesis.conflicts.join("，")}）`
          : ""
      }${
        hypothesis.missingEvidence.length
          ? `（待补证据：${hypothesis.missingEvidence.join("，")}）`
          : ""
      }`,
    );
  }
  if (payload.clarification) {
    lines.push(
      `待澄清：${payload.clarification.question}（缺失：${payload.clarification.missingFields.join("，")}）`,
    );
  }
  if (payload.warnings.length)
    lines.push(`警告：${payload.warnings.join("，")}`);
  if (payload.degradedSources.length)
    lines.push(`降级来源：${payload.degradedSources.join("，")}`);
  if (payload.missingEvidence.length)
    lines.push(`待补证据：${payload.missingEvidence.join("，")}`);
  if (payload.nextChecks.length)
    lines.push(`下一步只读检查（尚未执行）：${payload.nextChecks.join("，")}`);
  lines.push(payload.sourceStatement);
  return lines.join("\n");
}

export function downloadTurnExport(payload: TurnCopyPayload, fileName: string) {
  const blob = new Blob([JSON.stringify(payload, null, 2)], {
    type: "application/json;charset=utf-8",
  });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = fileName;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

// The suggestions of the answer are drafts: they fill the composer of the
// operator and never run a query or switch a resource on their own.
export function AssistantTurnActions({
  answer,
  identity,
  suggestions,
  onDraft,
  onManual,
  onRetry,
  retryLabel = "重新提问",
  manualLabel = "转入人工处置",
}: {
  answer: AssistantAnswerV1;
  identity?: { conversationId?: string; turnId?: string };
  suggestions?: { key: string; label: string; draft: string }[];
  onDraft?: (draft: string) => void;
  onManual?: () => void;
  onRetry?: () => void;
  retryLabel?: string;
  manualLabel?: string;
}) {
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);
  const actionLock = useRef(false);
  const client = useQueryClient();
  // The action belongs to the turn and to the authorization context it was
  // shown in. A scope, account or turn change closes the action: the late
  // answer of an older context never writes into the new one.
  const scopeKey = useAssistantScopeKey();
  const intentScope = `${scopeKey}|${identity?.turnId || "current"}`;
  // The epoch counts the changes of the context. It grows with every new
  // scope or turn, so the late answer of an older context never matches the
  // new one, even when the scope string repeats itself (A -> B -> A).
  const contextRef = useRef({ scope: intentScope, epoch: 0 });
  if (contextRef.current.scope !== intentScope)
    contextRef.current = {
      scope: intentScope,
      epoch: contextRef.current.epoch + 1,
    };
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);
  // A new turn or a new authorization context closes the action: the state of
  // one turn is never shown on another turn. The busy state is dropped as
  // well, so the request of an older context never strands the new one.
  useEffect(() => {
    setCopied(false);
    actionLock.current = false;
    setBusy(false);
  }, [intentScope]);
  function freezeContext(): FrozenContext {
    return { scope: intentScope, epoch: contextRef.current.epoch };
  }
  function isCurrent(frozen: FrozenContext) {
    return (
      mountedRef.current &&
      contextRef.current.scope === frozen.scope &&
      contextRef.current.epoch === frozen.epoch
    );
  }
  // The copy and the export read the turn and its committed results again
  // under the current authorization. The read of the turn is the authorization
  // check of the action: an answer without query references is copied and
  // exported only after the turn itself was read successfully.
  async function readCommittedResults(
    frozen: FrozenContext,
    turnId: string,
  ): Promise<
    { answer: AssistantAnswerV1; results: TurnCopyResult[] } | undefined
  > {
    const turn = await checked(getAssistantTurn(turnId, requestOptions()));
    if (!isCurrent(frozen)) return undefined;
    if (
      turn.data.turnId !== turnId ||
      turn.data.conversationId !== identity?.conversationId ||
      !turn.data.answer
    )
      throw new Error("轮次或会话已变化，未复制也未导出任何内容。");
    const references = turn.data.answer.queryResultRefs || [];
    const loaded: TurnCopyResult[] = [];
    for (const reference of references) {
      const fresh = await checked(
        getAssistantQueryResult(reference, requestOptions()),
      );
      if (!isCurrent(frozen)) return undefined;
      loaded.push(turnResult(reference, fresh.data));
    }
    return { answer: turn.data.answer, results: loaded };
  }
  function refuse(failure: unknown) {
    if (failure instanceof ApiError && [401, 403].includes(failure.status))
      client.removeQueries({ queryKey: ["assistant", scopeKey] });
    message.error(
      failure instanceof ApiError && [401, 403].includes(failure.status)
        ? "当前授权已失效，未复制也未导出任何内容。"
        : "复制或导出未成功，请重新授权后再试。",
    );
  }
  // One action of the bar. The context is frozen at the start of the click
  // and every await boundary compares it with the context of the current
  // render: the late answer of an older context writes nothing, shows nothing
  // and clears nothing of the new context.
  async function runAction(
    run: (
      frozen: FrozenContext,
      results: TurnCopyResult[],
      freshAnswer: AssistantAnswerV1,
    ) => void | Promise<void>,
  ) {
    if (actionLock.current) return;
    actionLock.current = true;
    const frozen = freezeContext();
    setBusy(true);
    try {
      const turnId = identity?.turnId;
      if (!turnId) {
        // Without the identity of the turn the action cannot be authorized:
        // it fails closed instead of publishing the cached answer.
        message.error(
          "当前回答没有轮次标识，无法重新校验授权，未复制也未导出任何内容。",
        );
        return;
      }
      const results = await readCommittedResults(frozen, turnId);
      if (!results) return;
      if (!isCurrent(frozen)) return;
      await run(frozen, results.results, results.answer);
    } catch (failure) {
      if (isCurrent(frozen)) refuse(failure);
    } finally {
      if (isCurrent(frozen)) {
        actionLock.current = false;
        setBusy(false);
      }
    }
  }
  function copy() {
    return runAction(async (frozen, results, freshAnswer) => {
      const payload = buildTurnCopyPayload(freshAnswer, identity, results);
      await navigator.clipboard.writeText(turnCopyText(payload));
      if (!isCurrent(frozen)) return;
      setCopied(true);
    });
  }
  function exportResult() {
    return runAction((frozen, results, freshAnswer) => {
      if (!isCurrent(frozen)) return;
      const payload = buildTurnCopyPayload(freshAnswer, identity, results);
      downloadTurnExport(
        payload,
        `assistant-turn-${identity?.turnId || "current"}.json`,
      );
    });
  }
  return (
    <Space orientation="vertical" size="small" style={{ width: "100%" }}>
      <Space wrap size="small">
        <Button size="small" loading={busy} onClick={() => void copy()}>
          {copied ? "已复制摘要" : "复制摘要"}
        </Button>
        <Button size="small" loading={busy} onClick={() => void exportResult()}>
          导出结果
        </Button>
        {onManual && (
          <Tooltip title="只带入已重新授权的故障与资源身份，不自动填写命令，也不自动执行。">
            <Button size="small" onClick={onManual}>
              {manualLabel}
            </Button>
          </Tooltip>
        )}
        {onRetry && (
          <Button size="small" onClick={onRetry}>
            {retryLabel}
          </Button>
        )}
      </Space>
      {suggestions && suggestions.length > 0 && onDraft && (
        <Space wrap size="small" aria-label="本轮建议">
          {suggestions.map((suggestion) => (
            <Button
              key={suggestion.key}
              size="small"
              type="text"
              onClick={() => onDraft(suggestion.draft)}
            >
              {suggestion.label}
            </Button>
          ))}
        </Space>
      )}
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        {turnSourceStatement}
      </Typography.Text>
    </Space>
  );
}

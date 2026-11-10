import { LegalHoldControl } from "../features/assistant/LegalHoldControl";
import { FaultChainCollector } from "../features/assistant/FaultChainCollector";
import { BindingEditor } from "../features/assistant/BindingEditor";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  Col,
  Input,
  InputNumber,
  List,
  Modal,
  Popconfirm,
  Row,
  Select,
  Space,
  Tag,
  Typography,
} from "antd";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useAuth } from "../auth/AuthProvider";
import { useOperationalCluster, useClusterDraft } from "../app/ClusterContext";
import { ApiError, checked, requestOptions } from "../api/client";
import {
  archiveAssistantConversation,
  cancelAssistantTurn,
  createAssistantConversation,
  getAssistantCapabilities,
  getAssistantConversation,
  getAssistantTurn,
  listAssistantConversations,
  listAssistantMessages,
  renameAssistantConversation,
  resolveAssistantResource,
  interpretAssistantSymptom,
  submitAssistantMessage,
} from "../api/generated/platform";
import type {
  AssistantAnswerV1,
  AssistantCreateConversationV1,
  AssistantTurnV1,
  AssistantResourceCandidateV1,
} from "../api/generated/models";
import { observation, type ObservationResult } from "../api/observation";
import { PageHeading } from "../components/PageHeading";
import { QueryState } from "../components/QueryState";
import { DiagnosisAnswer } from "../features/assistant/DiagnosisAnswer";
import { ConversationContext } from "../features/assistant/ConversationContext";
import { useConversationStream } from "../features/assistant/useConversationStream";
import { UnifiedInvestigationDetail } from "../features/assistant/UnifiedInvestigationDetail";
import {
  UnifiedInvestigationHistory,
  type InvestigationHistoryEntry,
} from "../features/assistant/UnifiedInvestigationHistory";
import {
  InvestigationStopConfirm,
  freezeStopTarget,
  stopTargetMatches,
  type StopTarget,
} from "../features/assistant/InvestigationStopConfirm";

// The archive view of the conversation list. The value is sent to the server:
// the browser never filters an already fetched page.
type ArchiveView = "exclude" | "include" | "only";
// A rename is bound to the conversation and the revision that were shown when
// the dialog was opened.
type RenameIntent = {
  conversationId: string;
  revision: number;
  scope: string;
  idempotencyKey: string;
};
// An archive confirmation is bound to the conversation it was opened for.
type ArchiveIntent = { conversationId: string; scope: string };

export function AssistantPage({
  initialDomain,
}: { initialDomain?: "audit" } = {}) {
  const { session } = useAuth();
  const client = useQueryClient();
  const [url, setUrl] = useSearchParams();
  const navigate = useNavigate();
  const id = url.get("conversation") || undefined;
  const logQuery = url.get("logQuery");
  const {
    clusterUid: scopedCluster,
    candidates,
    scopeKey: scopeGeneration,
    generation,
  } = useOperationalCluster();
  const scopeKey = JSON.stringify([
    session?.tenantId,
    session?.subject,
    session?.roles,
    session?.clusterScopes,
    session?.namespaceScopes,
    scopeGeneration,
    generation,
  ]);
  const logContext = useQuery({
    queryKey: ["assistant-log-context", scopeKey, logQuery],
    enabled: !!logQuery && !id && !!scopedCluster,
    queryFn: ({ signal }) =>
      observation<ObservationResult>(
        `queries/${encodeURIComponent(logQuery!)}/results`,
        scopedCluster,
        signal,
      ),
  });
  const linkedLog = !logContext.error ? logContext.data : undefined;
  const [mode, setMode] = useState<"data_query" | "troubleshoot">(
    url.get("mode") === "data_query" || url.get("mode") === "troubleshoot"
      ? (url.get("mode") as "data_query" | "troubleshoot")
      : url.get("incidentId") || url.get("canonicalId") || logQuery
        ? "troubleshoot"
        : "data_query",
  );
  const [domain, setDomain] = useState<"operational" | "audit">(
    initialDomain ??
      (session?.roles.includes("operator") ? "operational" : "audit"),
  );
  const [cluster, setCluster] = useState(scopedCluster);
  useEffect(() => {
    setCluster(scopedCluster);
  }, [scopedCluster]);
  const [namespace, setNamespace] = useState("");
  const clusterScopedGranted =
    session?.clusterScopes.includes(
      candidates.find((candidate) => candidate.clusterUid === cluster)
        ?.clusterId ?? "",
    ) ?? false;
  const [targetName, setTargetName] = useState("");
  const [targetKind, setTargetKind] = useState("Pod");
  const [resolution, setResolution] = useState<{
    key: string;
    candidates: AssistantResourceCandidateV1[];
    missing: string[];
    truncated: boolean;
  }>();
  const [selectedTarget, setSelectedTarget] = useState<string>();
  const targetKey = JSON.stringify([
    scopeKey,
    mode,
    domain,
    cluster,
    namespace,
    targetKind,
    targetName,
  ]);
  const targetCandidates =
    resolution?.key === targetKey ? resolution.candidates : [];
  const boundTarget =
    linkedLog?.resource.canonicalId ||
    url.get("canonicalId") ||
    (targetCandidates.some((c) => c.canonicalId === selectedTarget)
      ? selectedTarget
      : undefined);
  const boundObject =
    logQuery || url.get("incidentId") || url.get("canonicalId") || "";
  const previousObjectRef = useRef<string | undefined>(undefined);
  useEffect(() => {
    const previous = previousObjectRef.current;
    previousObjectRef.current = boundObject;
    if (boundObject === previous || !boundObject) return;
    // An explicit mode of the deep link wins: the operator asked for the
    // question mode of a bound object on purpose.
    if (url.get("mode") === "data_query") {
      setDomain("operational");
      return;
    }
    setMode("troubleshoot");
    setDomain("operational");
  }, [boundObject, url]);
  const [symptom, setSymptom] = useState("");
  const [windowMinutes, setWindowMinutes] = useState<number | "custom">(30);
  // The requested window is an exact instant. The form shows the same instant
  // in the UTC form its label declares and the request sends that instant, so
  // the shown window and the submitted window are the same one.
  const [windowFrom, setWindowFrom] = useState(() =>
    new Date(Date.now() - 30 * 60000).toISOString(),
  );
  const [windowTo, setWindowTo] = useState(() => new Date().toISOString());
  // The deep link of an observation page carries the window that was actually
  // observed. The new session form starts with exactly that window, so the
  // question of the operator is asked in the scope and the time window the
  // page showed.
  useEffect(() => {
    const from = url.get("from");
    const to = url.get("to");
    if (!from || !to) return;
    const fromValue = new Date(from);
    const toValue = new Date(to);
    if (!Number.isFinite(fromValue.getTime())) return;
    if (!Number.isFinite(toValue.getTime())) return;
    setWindowMinutes("custom");
    setWindowFrom(fromValue.toISOString());
    setWindowTo(toValue.toISOString());
  }, [url]);
  const [message, setMessage] = useState("");
  // The operator focus and the requested budget narrowing belong to the
  // current scope and conversation. They are draft state: a scope change, an
  // account change or a different conversation drops them, so a draft never
  // leaks into another authorization context.
  const [focus, setFocus] = useState("");
  const [tightenToolCalls, setTightenToolCalls] = useState<number | null>(null);
  const [tightenModelRequests, setTightenModelRequests] = useState<
    number | null
  >(null);
  const [tightenDurationSeconds, setTightenDurationSeconds] = useState<
    number | null
  >(null);
  useClusterDraft(
    symptom.trim().length > 0 ||
      message.trim().length > 0 ||
      focus.trim().length > 0 ||
      targetName.trim().length > 0 ||
      !!selectedTarget,
  );
  // Initialization reads the deep-link object. Cleanup runs only after an
  // actual scope transition, never on first mount or a same-scope deep link.
  const previousScopeRef = useRef(scopeKey);
  // The busy state belongs to the newest request. A request that lost the scope or
  // was superseded must not release the loading state of the current one.
  const busyEpoch = useRef(0);
  useEffect(() => {
    const previousScope = previousScopeRef.current;
    previousScopeRef.current = scopeKey;
    if (previousScope === scopeKey) return;
    // The request that was in flight belonged to the previous scope: it no longer
    // owns the busy state, and the new scope starts without a pending request.
    busyEpoch.current += 1;
    setBusy(false);
    setSymptom("");
    setMessage("");
    setFocus("");
    setTightenToolCalls(null);
    setTightenModelRequests(null);
    setTightenDurationSeconds(null);
    setTargetName("");
    setSelectedTarget(undefined);
    setNamespace("");
    setTurn(undefined);
    setReferent(undefined);
    setError(undefined);
    setMessageCursor(undefined);
    setMode(
      url.get("incidentId") || url.get("canonicalId") || logQuery
        ? "troubleshoot"
        : "data_query",
    );
    setDomain(session?.roles.includes("operator") ? "operational" : "audit");
  }, [scopeKey, session?.roles, url]);
  const [referent, setReferent] = useState<string>();
  // The search term is applied on the server. The browser never filters an
  // already fetched page and calls it a search.
  const [searchTerm, setSearchTerm] = useState("");
  const [searchQuery, setSearchQuery] = useState("");
  const [archiveView, setArchiveView] = useState<ArchiveView>("exclude");
  // The cursor belongs to the scope and the filter that produced it. A cursor
  // of another search, another archive view or another account is never sent:
  // the server refuses such a cursor and the refusal would be meaningless.
  const [pagination, setPagination] = useState<{
    scope: string;
    query: string;
    archive: ArchiveView;
    cursor?: string;
  }>({ scope: scopeKey, query: "", archive: "exclude" });
  const listCursor =
    pagination.scope === scopeKey &&
    pagination.query === searchQuery &&
    pagination.archive === archiveView
      ? pagination.cursor
      : undefined;
  const [messageCursor, setMessageCursor] = useState<string>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [turn, setTurn] = useState<AssistantTurnV1>();
  // The public investigation detail of the unified entry. It is opened by
  // the turn of the conversation or by an entry of the investigation history.
  const [investigationDetail, setInvestigationDetail] = useState<{
    jobId?: string;
    turnId?: string;
  }>();
  // The stop confirmation of the public detail. The target is frozen when
  // the confirmation opens and the dialog itself never sends a request.
  const [stopTarget, setStopTarget] = useState<StopTarget>();
  const [stopBusy, setStopBusy] = useState(false);
  const [stopError, setStopError] = useState<unknown>();
  // The stop request owns the confirmation only while it is the newest one.
  // The synchronous guard rejects a second confirmation while the first
  // request is still in flight, and the epoch rejects a late answer of an
  // older confirmation.
  const stopBusyRef = useRef(false);
  const stopEpochRef = useRef(0);
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [renameBusy, setRenameBusy] = useState(false);
  const [renameError, setRenameError] = useState<string>();
  // The rename intent is frozen when the dialog opens: the target and the
  // revision are the ones the user saw. A later navigation, a background
  // refresh or a revoked grant must not move the rename onto another
  // conversation and must not overwrite a change of somebody else.
  const [renameIntent, setRenameIntent] = useState<RenameIntent>();
  // The archive confirmation freezes its target as well: the confirmed
  // conversation is the one that was shown when the confirmation opened.
  const [archiveIntent, setArchiveIntent] = useState<ArchiveIntent>();
  const renameAbort = useRef(new AbortController());
  const renameBusyRef = useRef(false);
  useEffect(() => {
    setTurn(undefined);
    setMessage("");
    setFocus("");
    setTightenToolCalls(null);
    setTightenModelRequests(null);
    setTightenDurationSeconds(null);
    setReferent(undefined);
    setError(undefined);
    setMessageCursor(undefined);
    // The open investigation detail and the stop confirmation belong to the
    // conversation and the authorization context they were opened for. A new
    // conversation, a new scope or a new account closes them at once, so a
    // detail of the previous context can never stay on the page.
    setInvestigationDetail(undefined);
    setStopTarget(undefined);
    setStopError(undefined);
    setStopBusy(false);
    stopBusyRef.current = false;
    // A request that is still in flight belongs to the previous context. Its
    // late answer, its late refusal and its late completion must not write
    // into the new context, and must not clear a confirmation that is busy in
    // the new context.
    stopEpochRef.current += 1;
  }, [scopeKey, id]);
  useEffect(() => {
    // A new account or scope starts without the search of the previous
    // subject: the old cursor and the old filter never address a result of
    // the new scope.
    setSearchTerm("");
    setSearchQuery("");
    setArchiveView("exclude");
    setPagination({ scope: scopeKey, query: "", archive: "exclude" });
  }, [scopeKey]);
  useEffect(() => {
    // The rename dialog and the archive confirmation belong to the scope and
    // the conversation they were opened for. A pending rename of the previous
    // scope is aborted and its result is dropped.
    renameAbort.current.abort();
    renameAbort.current = new AbortController();
    renameBusyRef.current = false;
    setRenameBusy(false);
    setRenameOpen(false);
    setRenameIntent(undefined);
    setRenameValue("");
    setRenameError(undefined);
    setArchiveIntent(undefined);
  }, [scopeKey, id]);
  useEffect(() => {
    const timer = setTimeout(() => setSearchQuery(searchTerm.trim()), 300);
    return () => clearTimeout(timer);
  }, [searchTerm]);
  // The same-incident history opens the unified detail of the exact
  // conversation, turn and job through a link. The detail belongs to the
  // scope that is shown now: a scope or account change closes it and the
  // link of the previous scope never reopens it.
  const urlDetailJobId = url.get("investigationJob") || undefined;
  const urlDetailTurnId = url.get("investigationTurn") || undefined;
  const detailLinkRef = useRef<
    { scope: string; jobId?: string; turnId?: string } | undefined
  >(undefined);
  // A link that was left with an authorization context is consumed. The same
  // old URL must not reopen the old detail in a later generation of that
  // context; a normal new link still opens.
  const consumedDetailRef = useRef<Set<string>>(new Set());
  const detailLinkKey = (jobId?: string, turnId?: string) =>
    `${jobId ?? ""}|${turnId ?? ""}`;
  useEffect(() => {
    const applied = detailLinkRef.current;
    if (applied && applied.scope !== scopeKey) {
      // The context of the link is left: the link is consumed and the detail
      // is closed. The URL parameter may survive the change, so the consumed
      // link is remembered instead of only being cleared.
      detailLinkRef.current = undefined;
      consumedDetailRef.current.add(
        detailLinkKey(applied.jobId, applied.turnId),
      );
      setInvestigationDetail(undefined);
    }
    if (!urlDetailJobId && !urlDetailTurnId) {
      // The link was consumed by the reader. A later link to the same job is a
      // new link and opens the detail again.
      detailLinkRef.current = undefined;
      return;
    }
    const key = detailLinkKey(urlDetailJobId, urlDetailTurnId);
    if (consumedDetailRef.current.has(key)) return;
    if (
      applied &&
      applied.scope === scopeKey &&
      applied.jobId === urlDetailJobId &&
      applied.turnId === urlDetailTurnId
    )
      return;
    detailLinkRef.current = {
      scope: scopeKey,
      jobId: urlDetailJobId,
      turnId: urlDetailTurnId,
    };
    setInvestigationDetail({ jobId: urlDetailJobId, turnId: urlDetailTurnId });
  }, [scopeKey, urlDetailJobId, urlDetailTurnId]);
  const clearDetailLink = () => {
    const next = new URLSearchParams(url);
    next.delete("investigationJob");
    next.delete("investigationTurn");
    setUrl(next, { replace: true });
  };
  const conversation = useQuery({
    queryKey: ["assistant", scopeKey, id, "conversation"],
    enabled: !!id,
    queryFn: ({ signal }) =>
      checked(getAssistantConversation(id!, requestOptions(signal))),
  });
  const canWriteConversation =
    !!conversation.data &&
    conversation.data.data.ownerSubject === session?.subject;
  const historicalReadOnly = !!conversation.data && !canWriteConversation;
  const capabilityCluster = conversation.data?.data.clusterUid || cluster;
  const capabilityDomain =
    conversation.data?.data.authorizationDomain || domain;
  const capabilities = useQuery({
    queryKey: [
      "assistant",
      scopeKey,
      "capabilities",
      capabilityCluster,
      capabilityDomain,
    ],
    queryFn: ({ signal }) =>
      checked(
        getAssistantCapabilities(
          capabilityDomain === "operational" && capabilityCluster
            ? { clusterUid: capabilityCluster }
            : {},
          requestOptions(signal),
        ),
      ),
  });
  const sourceCapabilities = capabilities.data?.data.sourceCapabilities || [];
  const networkCapabilities = capabilities.data?.data.networkCapabilities || [];
  const visibleDatasets = capabilities.data?.data.catalog.datasets;
  const taskKinds = capabilities.data?.data.taskKinds || [];
  const requestedTask =
    mode === "data_query"
      ? "data_query"
      : url.get("incidentId")
        ? "incident_followup"
        : "resource_troubleshooting";
  const taskAvailable = taskKinds.includes(requestedTask);
  const queryExamples: Record<string, string> = {
    "resources.current":
      "当前授权范围内有多少 Pod？按健康状态分组，说明未知和降级状态。",
    "findings.occurrences":
      "过去七天的 Finding 有多少？按天统计，并说明时间是否可靠。",
    "incidents.records": "过去七天新增多少 Incident？按当前状态分组。",
    "executions.records": "过去七天我提交的执行任务有多少？按状态分组。",
    "incidents.recovery-duration":
      "过去七天已恢复 Incident 的平均恢复时长是多少？说明可靠时间与缺失。",
    "executions.success-rate":
      "过去七天本人执行成功率是多少？明确成功和失败分母，排除未知和取消。",
    "audit.events": "过去七天的平台审计事件有多少？按操作类型分组。",
  };
  // The suggestions of the assistant are drafts. They fill the composer of
  // the operator and are never sent on their own, so a suggestion never runs
  // a query or switches the scope of the session by itself.
  const turnSuggestions = useMemo(() => {
    const list: { key: string; label: string; draft: string }[] = [];
    const bound =
      boundTarget || url.get("canonicalId") || url.get("incidentId");
    if (mode === "troubleshoot") {
      if (bound) {
        list.push({
          key: "cpu-trend",
          label: "查看 CPU 趋势",
          draft: `${bound} 的 CPU 使用情况如何？说明缺失观测与查询来源。`,
        });
        list.push({
          key: "restart",
          label: "核对告警支持证据",
          draft: `${bound} 为什么重复重启？核对支持、冲突与待补证据。`,
        });
        list.push({
          key: "error-logs",
          label: "查看错误日志",
          draft: `查看 ${bound} 最近错误日志脱敏样例及其适用范围。`,
        });
      }
      list.push({
        key: "health-distribution",
        label: "按健康状态统计当前资源",
        draft: "按健康状态统计当前资源查看授权资源的当前分布。",
      });
    } else {
      list.push({
        key: "health-distribution",
        label: "按健康状态统计当前资源",
        draft: "按健康状态统计当前资源查看授权资源的当前分布。",
      });
      list.push({
        key: "cpu-trend",
        label: "查看 CPU 趋势",
        draft: "查看当前授权范围内 CPU 使用情况最高的资源趋势与缺失观测。",
      });
      list.push({
        key: "alert-evidence",
        label: "核对告警支持证据",
        draft: "核对当前告警的支持证据、冲突证据与待补证据。",
      });
      list.push({
        key: "error-logs",
        label: "查看错误日志",
        draft: "查看当前授权范围最近错误日志脱敏样例及其适用范围。",
      });
    }
    return list;
  }, [boundTarget, mode, url]);
  const conversations = useQuery({
    queryKey: [
      "assistant",
      scopeKey,
      "conversations",
      listCursor,
      searchQuery,
      archiveView,
    ],
    queryFn: ({ signal }) =>
      checked(
        listAssistantConversations(
          {
            limit: 20,
            cursor: listCursor,
            q: searchQuery || undefined,
            archived: archiveView,
          },
          requestOptions(signal),
        ),
      ),
  });
  const messages = useQuery({
    queryKey: ["assistant", scopeKey, id, "messages", messageCursor],
    enabled: !!id,
    queryFn: ({ signal }) =>
      checked(
        listAssistantMessages(
          id!,
          { limit: 100, cursor: messageCursor },
          requestOptions(signal),
        ),
      ),
  });
  const currentTurn = useQuery({
    queryKey: ["assistant", scopeKey, id, "turn", turn?.turnId],
    enabled: !!turn?.turnId,
    queryFn: ({ signal }) =>
      checked(getAssistantTurn(turn!.turnId, requestOptions(signal))),
    refetchInterval: (query) =>
      ["queued", "running"].includes(query.state.data?.data.state || "")
        ? 2000
        : false,
  });
  const scopeRef = useRef(scopeKey);
  const scopeAbort = useRef(new AbortController());
  // The conversation lifecycle generation. It advances whenever the page
  // leaves the conversation a request was started for, so a late answer of an
  // older conversation is refused even when the same conversation is shown
  // again (A -> B -> A). The scope is part of the key, so a scope or account
  // change advances the generation as well.
  const conversationKey = `${scopeKey}|${id ?? ""}`;
  const [conversationLifecycle, setConversationLifecycle] = useState({
    key: conversationKey,
    epoch: 1,
  });
  if (conversationLifecycle.key !== conversationKey) {
    // The conversation changed during this render. React re-renders the page
    // with the new generation before it commits, so the request ownership and
    // the acceptance check always use the same generation.
    setConversationLifecycle({
      key: conversationKey,
      epoch: conversationLifecycle.epoch + 1,
    });
  }
  const conversationEpochRef = useRef(conversationLifecycle.epoch);
  conversationEpochRef.current = conversationLifecycle.epoch;
  // The requests that belong to the conversation shown now. Leaving that
  // conversation aborts them, so a late answer of another conversation is
  // dropped instead of being committed into the current page.
  const conversationAbort = useRef(new AbortController());
  // The conversation currently shown. A child that answers for another
  // conversation must not write into this page.
  const conversationRef = useRef(id);
  useEffect(() => {
    conversationRef.current = id;
  }, [id]);
  useEffect(() => {
    conversationAbort.current.abort();
    conversationAbort.current = new AbortController();
    // The busy state belongs to the newest request. The request of the
    // conversation that was left no longer owns it.
    busyEpoch.current += 1;
    setBusy(false);
    // The state belongs to the mounted scope: a new conversation or a new page
    // epoch starts from a clean state.
  }, [conversationLifecycle.epoch]);
  const unmountedRef = useRef(false);
  useEffect(() => {
    scopeRef.current = scopeKey;
    scopeAbort.current.abort();
    scopeAbort.current = new AbortController();
  }, [scopeKey]);
  useEffect(
    () => () => {
      unmountedRef.current = true;
      scopeAbort.current.abort();
    },
    [],
  );
  const streamError = useConversationStream(
    canWriteConversation ? id : undefined,
    scopeKey,
  );
  const active =
    currentTurn.data?.data || turn || conversation.data?.data.activeTurn;
  const running = active && ["queued", "running"].includes(active.state);
  // The ownership of a fault-chain submission is the state the page actually
  // shows: the conversation, the scope, the bound revision of the conversation
  // and the turn that is active. The epoch advances whenever that ownership
  // changes, so a late answer of an older ownership is refused even when the
  // same conversation is shown again (A -> B -> A).
  const ownershipKey = [
    id ?? "",
    scopeKey,
    conversation.data?.data.binding.revision ?? "",
    active?.turnId ?? "",
  ].join("|");
  const [ownership, setOwnership] = useState({ key: ownershipKey, epoch: 1 });
  if (ownership.key !== ownershipKey) {
    // The ownership changed during this render. React re-renders the page with
    // the new epoch before it commits, so the children and the acceptance check
    // always use the same epoch.
    setOwnership({ key: ownershipKey, epoch: ownership.epoch + 1 });
  }
  const submissionEpoch = ownership.epoch;
  const epochRef = useRef(submissionEpoch);
  epochRef.current = submissionEpoch;
  const reload = () =>
    client.invalidateQueries({ queryKey: ["assistant", scopeKey] });
  // The stop confirmation is bound to the exact work it was opened for. The
  // job, the turn, the conversation, the conversation revision and the
  // subject generation are frozen when the confirmation opens. Opening it
  // sends no request at all, and a later navigation, a new turn or a changed
  // authorization cannot move the cancellation onto another job.
  const openStopConfirm = (target: {
    jobId: string;
    turnId?: string;
    conversationId?: string;
  }) => {
    if (!id) return;
    const frozenTurnId = target.turnId;
    const frozenConversationId = target.conversationId;
    // The confirmation is bound to the work that was opened. It never falls
    // back to the active turn of the page, and it only belongs to the
    // conversation that is shown now: a detail of another conversation is
    // refused instead of stopping the current one.
    if (!frozenTurnId || !frozenConversationId) return;
    if (frozenConversationId !== id) return;
    setStopError(undefined);
    setStopTarget(
      freezeStopTarget({
        jobId: target.jobId,
        turnId: frozenTurnId,
        conversationId: id,
        conversationRevision:
          conversation.data?.data.revision ?? active?.conversationRevision ?? 0,
        subject: session?.subject ?? "",
        tenantId: session?.tenantId ?? "",
        scopeKey,
        epoch: submissionEpoch,
        // The idempotency key is frozen here. A repeated confirmation of the
        // same target carries the same key, so it can never cancel a second
        // job under a fresh key.
        idempotencyKey: crypto.randomUUID(),
      }),
    );
  };
  const confirmStop = async () => {
    const target = stopTarget;
    if (!target) return;
    // A second confirmation while the first request is in flight is refused
    // synchronously, before any request is sent.
    if (stopBusyRef.current) return;
    // The confirmation may only act on the work it was opened for.
    if (
      !stopTargetMatches(target, {
        subject: session?.subject ?? "",
        scopeKey,
        conversationId: id ?? "",
        epoch: submissionEpoch,
      })
    ) {
      setStopError(
        new Error(
          "当前会话、授权或轮次已变化，停止请求已被拒绝，请重新打开确认",
        ),
      );
      return;
    }
    const epoch = (stopEpochRef.current += 1);
    // The request belongs to the conversation and the scope it was confirmed
    // in. Leaving that conversation or scope aborts it.
    const signal = AbortSignal.any([
      scopeAbort.current.signal,
      conversationAbort.current.signal,
    ]);
    const canCommit = () =>
      !unmountedRef.current &&
      scopeRef.current === scopeKey &&
      conversationRef.current === target.conversationId &&
      !signal.aborted &&
      epoch === stopEpochRef.current;
    stopBusyRef.current = true;
    setStopBusy(true);
    setStopError(undefined);
    try {
      await checked(
        cancelAssistantTurn(
          target.turnId,
          {},
          requestOptions(signal, true, target.idempotencyKey),
        ),
      );
      if (!canCommit()) return;
      setStopTarget(undefined);
      await reload();
    } catch (cause) {
      if (!canCommit()) return;
      const e = cause instanceof Error ? cause : new Error("停止请求未被接受");
      // A lost grant or an expired session drops the cached data of this
      // scope only. A late refusal of an older scope never touches the cache
      // of the scope that is shown now.
      if (e instanceof ApiError && [401, 403].includes(e.status))
        client.removeQueries({ queryKey: ["assistant", scopeKey] });
      setStopError(e);
    } finally {
      // Only the newest confirmation may release the busy state. An older
      // request that settles late must not clear the state of a newer one.
      if (epoch === stopEpochRef.current) {
        stopBusyRef.current = false;
        setStopBusy(false);
      }
    }
  };
  // The investigation history of the conversation. The entries are the turns
  // the conversation produced, so a v3 investigation started from the unified
  // entry is never missing here. Reading an entry never starts another job.
  const investigationEntries: InvestigationHistoryEntry[] = (() => {
    const seen = new Set<string>();
    const entries: InvestigationHistoryEntry[] = [];
    for (const item of (messages.data?.data || []).slice().reverse()) {
      if (!item?.turnId || seen.has(item.turnId)) continue;
      seen.add(item.turnId);
      entries.push({
        turnId: item.turnId,
        createdAt: item.createdAt,
        active: item.turnId === active?.turnId,
      });
    }
    return entries.slice(0, 20);
  })();
  // A fault-chain child answers for the conversation it was mounted with. The page
  // re-checks the live scope and the live conversation before the answer may
  // become the current turn: a late answer of another conversation, another
  // scope or an unmounted page is dropped instead of overwriting this one.
  const acceptFaultChainTurn = (
    next: AssistantTurnV1,
    submittedConversationId: string,
    submittedEpoch: number,
  ) => {
    if (unmountedRef.current) return;
    if (submittedEpoch !== epochRef.current) return;
    if (scopeRef.current !== scopeKey) return;
    if (conversationRef.current !== submittedConversationId) return;
    setTurn(next);
    setMessageCursor(undefined);
    void reload();
  };
  async function mutate(
    action: (signal: AbortSignal, canCommit: () => boolean) => Promise<void>,
    bound?: { conversation: string },
  ) {
    const runScope = scopeKey;
    const runEpoch = conversationEpochRef.current;
    const runConversation = bound?.conversation;
    // A request that belongs to a conversation is cancelled when the page
    // leaves that conversation, so a late answer of another conversation never
    // reaches this page.
    const signal = bound
      ? AbortSignal.any([
          scopeAbort.current.signal,
          conversationAbort.current.signal,
        ])
      : scopeAbort.current.signal;
    const epoch = (busyEpoch.current += 1);
    const canCommit = () =>
      !unmountedRef.current &&
      scopeRef.current === runScope &&
      !signal.aborted &&
      (runConversation === undefined ||
        (conversationEpochRef.current === runEpoch &&
          conversationRef.current === runConversation));
    setBusy(true);
    setError(undefined);
    try {
      await action(signal, canCommit);
      if (!canCommit()) return;
      await reload();
    } catch (cause) {
      if (!canCommit()) return;
      const e = cause instanceof Error ? cause : new Error("操作失败");
      // A version conflict is reported as such. The page never fetches a new
      // revision and retries silently under it. A lost grant is reported and
      // the cached conversation of that scope is dropped.
      setError(
        e instanceof ApiError && e.status === 409
          ? new Error("会话已被其他操作修改，请刷新后重试")
          : e instanceof ApiError && e.status === 403
            ? new Error("当前授权已变化，请刷新后重试")
            : e instanceof ApiError && e.status === 401
              ? new Error("登录状态已失效，请重新登录")
              : e,
      );
      if (e instanceof ApiError && [401, 403].includes(e.status))
        client.removeQueries({ queryKey: ["assistant", scopeKey] });
    } finally {
      // Only the newest request may release the busy state; an older request that
      // finished after a scope change must not clear the current loading state.
      if (epoch === busyEpoch.current) setBusy(false);
    }
  }
  function openRename() {
    const target = conversation.data?.data;
    if (!target) return;
    // The intent is fixed here: the conversation and the revision that the
    // user saw when the dialog opened.
    renameAbort.current.abort();
    renameAbort.current = new AbortController();
    setRenameIntent({
      conversationId: target.conversationId,
      revision: target.revision,
      scope: scopeKey,
      idempotencyKey: crypto.randomUUID(),
    });
    setRenameValue(target.title || "");
    setRenameError(undefined);
    setRenameOpen(true);
  }
  async function rename() {
    const intent = renameIntent;
    // A second submit of the same intent would either duplicate the request
    // or conflict with the first one. The dialog stays closed until the
    // request is settled.
    if (!intent || renameBusyRef.current) return;
    if (intent.scope !== scopeKey) return;
    const signal = renameAbort.current.signal;
    renameBusyRef.current = true;
    setRenameBusy(true);
    setRenameError(undefined);
    try {
      await checked(
        renameAssistantConversation(
          intent.conversationId,
          { title: renameValue, expectedConversationRevision: intent.revision },
          requestOptions(signal, true, intent.idempotencyKey),
        ),
      );
      if (signal.aborted || renameAbort.current.signal !== signal) return;
      setRenameOpen(false);
      setRenameIntent(undefined);
      setRenameValue("");
      await reload();
    } catch (cause) {
      if (signal.aborted || renameAbort.current.signal !== signal) return;
      const e = cause instanceof Error ? cause : new Error("改名失败");
      if (e instanceof ApiError && [401, 403].includes(e.status)) {
        // The grant of the previous subject is gone: the cached conversation
        // of that scope must not stay on the screen.
        client.removeQueries({ queryKey: ["assistant", scopeKey] });
      }
      setRenameError(
        e instanceof ApiError && e.status === 409
          ? "会话已被其他操作修改，请刷新后重试"
          : e instanceof ApiError && e.status === 403
            ? "没有修改此会话的权限"
            : e instanceof ApiError && e.status === 401
              ? "登录状态已失效，请重新登录"
              : e.message,
      );
    } finally {
      if (renameAbort.current.signal === signal) {
        renameBusyRef.current = false;
        setRenameBusy(false);
      }
    }
  }
  async function create() {
    await mutate(async (signal, canCommit) => {
      const log = logQuery
        ? (await logContext.refetch({ throwOnError: true })).data!
        : undefined;
      if (!canCommit()) return;
      const now = new Date();
      const body: AssistantCreateConversationV1 = {
        title:
          mode === "data_query"
            ? "运维问数"
            : symptom.slice(0, 100) || "资源排查",
        mode,
        authorizationDomain: domain,
        timezone:
          (log?.window.timezone ??
            Intl.DateTimeFormat().resolvedOptions().timeZone) ||
          "Asia/Shanghai",
        ...(domain === "operational"
          ? {
              clusterUid: cluster,
              namespaceSelection: log
                ? [log.resource.namespaceScope.namespace]
                : namespace
                  ? [namespace]
                  : [],
              includeClusterScoped: log
                ? false
                : !namespace && clusterScopedGranted,
            }
          : {}),
      };
      if (mode === "troubleshoot") {
        body.symptom = symptom;
        if (log) {
          body.timeRange = {
            from: log.window.from,
            to: log.window.to,
            timezone: log.window.timezone,
          };
          body.resourceCanonicalIds = [log.resource.canonicalId];
        } else if (url.get("incidentId"))
          body.incidentId = url.get("incidentId")!;
        else {
          const from =
            windowMinutes === "custom"
              ? new Date(windowFrom)
              : new Date(now.getTime() - windowMinutes * 60000);
          const to = windowMinutes === "custom" ? new Date(windowTo) : now;
          if (
            !Number.isFinite(from.getTime()) ||
            !Number.isFinite(to.getTime()) ||
            to <= from ||
            to.getTime() - from.getTime() > 60 * 60000 ||
            to > now
          ) {
            throw new Error(
              "调查时间须有效、开始早于结束、跨度不超过 60 分钟，且结束不能晚于现在。",
            );
          }
          body.timeRange = {
            from: from.toISOString(),
            to: to.toISOString(),
            timezone: body.timezone,
          };
          body.resourceCanonicalIds = boundTarget ? [boundTarget] : [];
        }
      }
      const out = await checked(
        createAssistantConversation(body, requestOptions(signal, true)),
      );
      if (!canCommit()) return;
      setTurn(undefined);
      setUrl((previous) => {
        const next = new URLSearchParams(previous);
        next.set("clusterUid", cluster);
        next.set("conversation", out.data.conversationId);
        next.delete("logQuery");
        return next;
      });
    });
  }
  // The requested narrowing is sent as-is: the server clamps it to the
  // admission and echoes the budget it actually applied. An invalid value is
  // refused by the server, so the page never claims a larger budget.
  const tightening =
    tightenToolCalls !== null ||
    tightenModelRequests !== null ||
    tightenDurationSeconds !== null
      ? {
          ...(tightenToolCalls !== null ? { toolCalls: tightenToolCalls } : {}),
          ...(tightenModelRequests !== null
            ? { modelRequests: tightenModelRequests }
            : {}),
          ...(tightenDurationSeconds !== null
            ? { durationSeconds: tightenDurationSeconds }
            : {}),
        }
      : undefined;
  const invalidTightening = [
    tightenToolCalls,
    tightenModelRequests,
    tightenDurationSeconds,
  ].some(
    (value) =>
      value !== null &&
      (!Number.isInteger(value) || value < 0 || value > 2_000_000_000),
  );
  async function send(options?: { text?: string; parentTurnId?: string }) {
    if (!id || !conversation.data || !canWriteConversation) return;
    // The revision and the idempotency key of this submission are frozen when
    // the operator sends: a late answer of another conversation may neither
    // pick up another revision nor reuse another key.
    const revision = conversation.data.data.revision;
    const idempotencyKey = crypto.randomUUID();
    // The retry sends the question of the retried turn again as a new turn.
    // It is an explicit new round of the operator: the old job and its fixed
    // output stay untouched, and nothing is replayed in the background.
    const submitted = options?.text ?? message;
    const parentTurnId =
      options?.parentTurnId ?? (active ? active.turnId : undefined);
    await mutate(
      async (signal, canCommit) => {
        // A retry is a new round of the operator. It is not an answer to a
        // pending clarification, so it never carries the clarification of
        // another turn.
        const clarification = options?.text
          ? undefined
          : active?.answer?.clarification?.clarificationId;
        const out = await checked(
          submitAssistantMessage(
            id,
            {
              message: submitted,
              expectedConversationRevision: revision,
              ...(parentTurnId ? { parentTurnId } : {}),
              ...(clarification ? { clarificationId: clarification } : {}),
              ...(referent ? { referentCanonicalId: referent } : {}),
              ...(focus.trim() ? { focus: focus.trim() } : {}),
              ...(tightening ? { budgetTightening: tightening } : {}),
            },
            requestOptions(signal, true, idempotencyKey),
          ),
        );
        if (!canCommit()) return;
        setTurn(out.data);
        setReferent(undefined);
        setMessage("");
        setFocus("");
        setTightenToolCalls(null);
        setTightenModelRequests(null);
        setTightenDurationSeconds(null);
        setMessageCursor(undefined);
      },
      { conversation: id },
    );
  }
  // The question of a turn is the message the operator sent for it. The retry
  // reuses exactly that text, so the new round asks the same question in the
  // current scope instead of a rewritten one.
  const questionOfTurn = (turnId?: string) => {
    if (!turnId) return "";
    const item = (messages.data?.data || []).find(
      (entry) =>
        entry.turnId === turnId &&
        entry.role === "user" &&
        "text" in entry.content,
    );
    return item && "text" in item.content ? item.content.text : "";
  };
  // The retry is an explicit new round of the operator: it submits the
  // question of the retried turn again. The old job and its fixed output stay
  // untouched, and nothing is replayed in the background.
  const retryTurn = (turnId?: string) => {
    const question = questionOfTurn(turnId);
    if (!question) return;
    void send({ text: question, parentTurnId: turnId });
  };
  // The manual disposition entry belongs to the incident of the session. The
  // incident page reads the incident again under the current authorization, so
  // the identity that is carried over is the re-authorized one. Nothing is
  // filled in, confirmed or executed by the assistant.
  const manualHref = (() => {
    const incidentId = conversation.data?.data.binding.incidentId;
    if (!incidentId || typeof incidentId !== "string") return undefined;
    return `/events/incidents/${incidentId}?${new URLSearchParams({
      tab: "action",
      ...(conversation.data?.data.clusterUid
        ? { clusterUid: conversation.data.data.clusterUid }
        : {}),
    })}`;
  })();
  return (
    <>
      <PageHeading
        title="AI 问数与故障排查"
        description="在当前授权范围内查询运维数据、追踪证据和故障假设。"
      />
      {error && (
        <Alert
          type="error"
          title={error.message}
          closable
          onClose={() => setError(undefined)}
        />
      )}
      {capabilities.data?.data.enabled === false && (
        <Alert
          type="warning"
          title="自然语言任务尚未启用"
          description="当前环境需要先完成模型 Profile 和 Worker 能力准入。"
        />
      )}
      {capabilities.error && (
        <Alert
          type="error"
          title="无法读取助手能力信息"
          description={
            <>
              <p>
                当前无法确认模型
                Profile、任务类型和来源能力，因此暂时无法创建会话。这不代表功能未启用。
              </p>
              <Button size="small" onClick={() => void capabilities.refetch()}>
                重新加载能力信息
              </Button>
            </>
          }
        />
      )}
      {session?.roles.includes("platform_admin") && <LegalHoldControl />}
      <Row gutter={16}>
        <Col xs={24} lg={7}>
          <Card
            title="我的会话"
            extra={
              <Button
                onClick={() => {
                  setUrl({});
                  setTurn(undefined);
                }}
              >
                新会话
              </Button>
            }
          >
            <Space orientation="vertical" style={{ width: "100%" }}>
              {/* The filter controls stay outside the result state: a failed
                  or still loading search must remain clearable and the
                  archive view must remain switchable. */}
              <Input
                allowClear
                aria-label="搜索会话"
                placeholder="按会话名称搜索（服务端检索）"
                value={searchTerm}
                onChange={(e) => setSearchTerm(e.target.value)}
              />
              <Select
                aria-label="归档范围"
                value={archiveView}
                onChange={setArchiveView}
                options={[
                  { value: "exclude", label: "未归档" },
                  { value: "include", label: "全部会话" },
                  { value: "only", label: "已归档" },
                ]}
              />
            </Space>
            <QueryState
              loading={conversations.isPending}
              error={conversations.error}
              retry={() => void conversations.refetch()}
            >
              <Space>
                <Button
                  disabled={!listCursor}
                  onClick={() =>
                    setPagination({
                      scope: scopeKey,
                      query: searchQuery,
                      archive: archiveView,
                    })
                  }
                >
                  最新会话
                </Button>
                <Button
                  disabled={!conversations.data?.meta?.nextCursor}
                  onClick={() =>
                    setPagination({
                      scope: scopeKey,
                      query: searchQuery,
                      archive: archiveView,
                      cursor: conversations.data?.meta?.nextCursor,
                    })
                  }
                >
                  下一页
                </Button>
              </Space>
              <List
                dataSource={conversations.data?.data || []}
                renderItem={(item) => (
                  <List.Item>
                    <Button
                      type={id === item.conversationId ? "primary" : "text"}
                      onClick={() => {
                        setUrl({ conversation: item.conversationId });
                        setTurn(undefined);
                      }}
                    >
                      {item.title}
                    </Button>
                  </List.Item>
                )}
              />
              {(conversations.data?.data || []).length === 0 && (
                <Typography.Text type="secondary">
                  {searchQuery ? "没有匹配的会话" : "暂无会话"}
                </Typography.Text>
              )}
            </QueryState>
          </Card>
        </Col>
        <Col xs={24} lg={17}>
          <Card
            title={conversation.data?.data.title || "新建会话"}
            extra={
              id && (
                <Space>
                  <Button
                    disabled={!canWriteConversation || busy || !!running}
                    onClick={openRename}
                  >
                    改名
                  </Button>
                  <Popconfirm
                    title="确认归档该会话？"
                    description="归档不会删除历史消息与固定引用。"
                    okText="归档"
                    cancelText="取消"
                    onConfirm={() => {
                      // The confirmation owns the conversation it was opened
                      // for. A navigation in the meantime must not archive
                      // another conversation.
                      const intent = archiveIntent;
                      if (!intent || intent.scope !== scopeKey) return;
                      void mutate(async (signal, canCommit) => {
                        await checked(
                          archiveAssistantConversation(
                            intent.conversationId,
                            {},
                            requestOptions(signal, true),
                          ),
                        );
                        if (!canCommit()) return;
                        const next = new URLSearchParams();
                        next.set("clusterUid", cluster);
                        setUrl(next);
                        setTurn(undefined);
                      });
                    }}
                  >
                    <Button
                      disabled={!canWriteConversation || busy || !!running}
                      onClick={() =>
                        setArchiveIntent({
                          conversationId: id,
                          scope: scopeKey,
                        })
                      }
                    >
                      归档
                    </Button>
                  </Popconfirm>
                </Space>
              )
            }
          >
            {historicalReadOnly && (
              <Alert
                type="info"
                title="历史会话只读"
                description="该会话保留原主体归属，仅供查看。请新建会话发起任务。"
              />
            )}
            {!id ? (
              <Space orientation="vertical" style={{ width: "100%" }}>
                {logQuery && (
                  <QueryState
                    loading={logContext.isPending}
                    error={logContext.error}
                    retry={() => void logContext.refetch()}
                  >
                    {linkedLog && (
                      <Alert
                        type="info"
                        title="按日志资源与时间创建排查会话"
                        description={
                          <>
                            <div>{linkedLog.resource.canonicalId}</div>
                            <div>
                              {linkedLog.window.from} – {linkedLog.window.to} ·{" "}
                              {linkedLog.window.timezone}
                            </div>
                            <div>
                              来源 {linkedLog.source.registrationId} · 版本{" "}
                              {linkedLog.source.registrationRevision}
                            </div>
                            <div>
                              AI
                              将按当前授权选择合格来源，日志样例不会自动作为回答证据。
                            </div>
                          </>
                        }
                      />
                    )}
                  </QueryState>
                )}
                <Select
                  disabled={!!logQuery}
                  aria-label="会话模式"
                  value={mode}
                  onChange={(value) => {
                    setMode(value);
                    if (value === "troubleshoot") setDomain("operational");
                  }}
                  options={[
                    ...(taskKinds.includes("data_query")
                      ? [{ value: "data_query", label: "问数" }]
                      : []),
                    ...(session?.roles.includes("operator") &&
                    taskKinds.includes(
                      url.get("incidentId")
                        ? "incident_followup"
                        : "resource_troubleshooting",
                    )
                      ? [{ value: "troubleshoot", label: "故障排查" }]
                      : []),
                  ]}
                />
                <Select
                  disabled={!!logQuery}
                  aria-label="授权域"
                  value={domain}
                  onChange={setDomain}
                  options={[
                    ...(session?.roles.includes("operator")
                      ? [{ value: "operational", label: "运维授权域" }]
                      : []),
                    ...(mode === "data_query" &&
                    session?.roles.includes("platform_admin")
                      ? [{ value: "audit", label: "管理员审计域" }]
                      : []),
                  ]}
                />
                {domain === "operational" && (
                  <>
                    <Select
                      disabled={!!logQuery}
                      aria-label="授权集群"
                      placeholder="选择授权集群"
                      value={cluster || undefined}
                      onChange={setCluster}
                      options={candidates.map((c) => ({
                        value: c.clusterUid,
                        label: c.displayName,
                      }))}
                    />
                    <Input
                      disabled={!!logQuery}
                      aria-label="命名空间子范围"
                      placeholder="命名空间子范围（空表示全部已授权命名空间）"
                      value={
                        linkedLog?.resource.namespaceScope.namespace ??
                        namespace
                      }
                      onChange={(e) => setNamespace(e.target.value)}
                    />
                  </>
                )}
                {mode === "troubleshoot" && (
                  <>
                    <Typography.Text>
                      目标：
                      {url.get("incidentId") ||
                        url.get("canonicalId") ||
                        boundTarget ||
                        "请填写资源名称并确认候选"}
                    </Typography.Text>
                    {!logQuery &&
                      !url.get("incidentId") &&
                      !url.get("canonicalId") && (
                        <>
                          <Select
                            value={targetKind}
                            onChange={setTargetKind}
                            options={[
                              "Pod",
                              "Deployment",
                              "StatefulSet",
                              "DaemonSet",
                              "Service",
                              "Node",
                              "PersistentVolumeClaim",
                            ].map((value) => ({ value, label: value }))}
                          />
                          <Input
                            placeholder="精确资源名称；同名资源将列出候选"
                            value={targetName}
                            onChange={(e) => setTargetName(e.target.value)}
                          />
                          <Button
                            loading={busy}
                            disabled={!cluster || !targetName}
                            onClick={() =>
                              void mutate(async (signal, canCommit) => {
                                const response = await checked(
                                  resolveAssistantResource(
                                    {
                                      clusterUid: cluster,
                                      namespaceSelection: namespace
                                        ? [namespace]
                                        : [],
                                      includeClusterScoped:
                                        !namespace && clusterScopedGranted,
                                      selector: {
                                        name: targetName,
                                        kind: targetKind,
                                        ...(namespace ? { namespace } : {}),
                                      },
                                    },
                                    requestOptions(signal),
                                  ),
                                );
                                if (!canCommit()) return;
                                setResolution({
                                  key: targetKey,
                                  candidates: response.data.candidates,
                                  missing: response.data.missingFields,
                                  truncated: response.data.truncated,
                                });
                                setSelectedTarget(undefined);
                              })
                            }
                          >
                            查找授权范围内的目标
                          </Button>
                          {resolution?.key === targetKey && (
                            <>
                              {resolution.missing.length > 0 && (
                                <Alert
                                  type="info"
                                  title={`请补充：${resolution.missing.join("、")}`}
                                />
                              )}
                              {resolution.truncated && (
                                <Alert
                                  type="warning"
                                  title="候选已截断，请先收窄 namespace。"
                                />
                              )}
                              <Select
                                placeholder="确认真实资源身份"
                                value={selectedTarget}
                                onChange={setSelectedTarget}
                                options={targetCandidates.map((c) => ({
                                  value: c.canonicalId,
                                  label: `${c.kind} ${c.namespace || "集群级"}/${c.name} · ${c.canonicalId.split("/").at(-1)}`,
                                }))}
                              />
                            </>
                          )}
                        </>
                      )}
                    <Input.TextArea
                      placeholder="描述现象"
                      value={symptom}
                      onChange={(e) => setSymptom(e.target.value)}
                      maxLength={4000}
                    />
                    {!logQuery && !url.get("incidentId") && (
                      <Button
                        disabled={!symptom || busy}
                        onClick={() =>
                          void mutate(async (signal, canCommit) => {
                            const response = await checked(
                              interpretAssistantSymptom(
                                { symptom },
                                requestOptions(signal),
                              ),
                            );
                            if (!canCommit()) return;
                            const intent = response.data;
                            if (intent.selector.kind)
                              setTargetKind(intent.selector.kind);
                            if (intent.selector.name)
                              setTargetName(intent.selector.name);
                            if (intent.selector.namespace)
                              setNamespace(intent.selector.namespace);
                            setSelectedTarget(undefined);
                            setResolution(undefined);
                            if (intent.status === "clarification")
                              throw new Error(
                                `请明确补充目标：${intent.missingFields.join("、")}；症状本身不能确定资源身份。`,
                              );
                          })
                        }
                      >
                        从症状识别目标，再查找并确认身份
                      </Button>
                    )}
                    {!logQuery && !url.get("incidentId") && (
                      <>
                        <Typography.Text>
                          资源调查时间（最长 60 分钟）
                        </Typography.Text>
                        <Select
                          aria-label="资源调查时间"
                          value={windowMinutes}
                          onChange={setWindowMinutes}
                          options={[
                            ...[5, 15, 30, 60].map((value) => ({
                              value,
                              label: `最近 ${value} 分钟`,
                            })),
                            { value: "custom", label: "指定绝对时间" },
                          ]}
                        />
                        {windowMinutes === "custom" && (
                          <>
                            <label>
                              开始时间（UTC）
                              <Input
                                aria-label="开始时间（UTC）"
                                type="datetime-local"
                                step="0.001"
                                value={windowFrom.slice(0, 23)}
                                onChange={(e) =>
                                  setWindowFrom(
                                    e.target.value
                                      ? new Date(
                                          `${e.target.value}Z`,
                                        ).toISOString()
                                      : "",
                                  )
                                }
                              />
                            </label>
                            <label>
                              结束时间（UTC）
                              <Input
                                aria-label="结束时间（UTC）"
                                type="datetime-local"
                                step="0.001"
                                value={windowTo.slice(0, 23)}
                                onChange={(e) =>
                                  setWindowTo(
                                    e.target.value
                                      ? new Date(
                                          `${e.target.value}Z`,
                                        ).toISOString()
                                      : "",
                                  )
                                }
                              />
                            </label>
                          </>
                        )}
                      </>
                    )}
                  </>
                )}
                <Button
                  type="primary"
                  loading={busy}
                  disabled={
                    !capabilities.data?.data.enabled ||
                    !taskAvailable ||
                    (!!logQuery && !linkedLog) ||
                    (mode === "troubleshoot" &&
                      !url.get("incidentId") &&
                      !boundTarget)
                  }
                  onClick={() => void create()}
                >
                  创建会话
                </Button>
                {capabilities.data?.data.enabled && !taskAvailable && (
                  <Alert
                    type="warning"
                    title="当前模型 Profile 未开放此会话模式，请选择已开放模式。"
                  />
                )}
              </Space>
            ) : (
              <QueryState
                loading={messages.isPending || conversation.isPending}
                error={messages.error || conversation.error}
                retry={() => void reload()}
              >
                {conversation.data?.data.authorizationDomain ===
                  "operational" && (
                  <Alert
                    type={
                      capabilities.data?.data.sourceCapabilityStatus ===
                      "available"
                        ? "info"
                        : "warning"
                    }
                    title="当前来源能力"
                    description={
                      sourceCapabilities.length
                        ? sourceCapabilities
                            .map(
                              (source) =>
                                `${String(source.datasetId)} / ${String(source.operation)} · ${String(source.collectorVersion)} · 覆盖 ${String(source.coverageFrom)} ～ ${String(source.coverageTo)}`,
                            )
                            .join("；")
                        : "当前没有可用的指标/日志分析准入证明，或 Worker 不可用；目录存在不代表来源可查询。若上方提示无法读取能力信息，则该状态未知。"
                    }
                  />
                )}
                {conversation.data?.data.authorizationDomain ===
                  "operational" && (
                  <Alert
                    type="info"
                    title="网络与应用层观测"
                    description={
                      networkCapabilities.length
                        ? networkCapabilities
                            .map(
                              (source) =>
                                `DeepFlow L4：${source.network}；L7：${source.l7}；Trace：未启用`,
                            )
                            .join("；")
                        : "当前没有通过范围验证的 DeepFlow 来源。"
                    }
                  />
                )}
                {conversation.data && (
                  <ConversationContext
                    readOnly={historicalReadOnly}
                    binding={conversation.data.data.binding}
                    domain={conversation.data.data.authorizationDomain}
                    cluster={conversation.data.data.clusterUid}
                  />
                )}
                {conversation.data && (
                  <div id="assistant-binding-editor">
                    <BindingEditor
                      key={`${id}:${scopeKey}`}
                      conversation={conversation.data.data}
                      running={Boolean(running)}
                      scopeKey={scopeKey}
                      onChanged={() => {
                        setTurn(undefined);
                        setMessageCursor(undefined);
                        setReferent(undefined);
                      }}
                    />
                  </div>
                )}
                {conversation.data?.data.mode === "troubleshoot" &&
                  capabilities.data?.data.faultChainEnabled && (
                    <FaultChainCollector
                      key={id}
                      conversation={conversation.data.data}
                      disabled={Boolean(running) || busy}
                      epoch={submissionEpoch}
                      focus={focus.trim() || undefined}
                      budgetTightening={tightening}
                      onSubmitted={(next, epoch) =>
                        acceptFaultChainTurn(
                          next,
                          conversation.data!.data.conversationId,
                          epoch,
                        )
                      }
                    />
                  )}
                {active?.answer?.clarification?.missingFields.includes(
                  "referentCanonicalId",
                ) && (
                  <Select
                    aria-label="确认历史资源指代"
                    disabled={!canWriteConversation}
                    placeholder="选择本轮指代的已验证资源"
                    value={referent}
                    onChange={setReferent}
                    options={active.answer.clarification.candidates.map(
                      (value) => ({ value, label: value }),
                    )}
                  />
                )}
                <Space>
                  <Button
                    disabled={!messageCursor}
                    onClick={() => setMessageCursor(undefined)}
                  >
                    最新消息
                  </Button>
                  <Button
                    disabled={!messages.data?.meta?.nextCursor}
                    onClick={() =>
                      setMessageCursor(messages.data?.meta?.nextCursor)
                    }
                  >
                    更早的消息
                  </Button>
                </Space>
                <List
                  dataSource={messages.data?.data || []}
                  renderItem={(item) => (
                    <List.Item>
                      <Card
                        size="small"
                        style={{ width: "100%" }}
                        title={
                          item.role === "user"
                            ? historicalReadOnly
                              ? "历史用户"
                              : "我"
                            : "助手"
                        }
                      >
                        {"text" in item.content ? (
                          <Typography.Paragraph
                            style={{ whiteSpace: "pre-wrap" }}
                          >
                            {item.content.text}
                          </Typography.Paragraph>
                        ) : (
                          <DiagnosisAnswer
                            answer={item.content as AssistantAnswerV1}
                            turnId={item.turnId}
                            checkControls={
                              capabilities.data?.data.faultChainEnabled
                                ? {
                                    conversation: conversation.data!.data,
                                    disabled:
                                      !canWriteConversation ||
                                      Boolean(running) ||
                                      busy,
                                    epoch: submissionEpoch,
                                    onSubmitted: (next, epoch) =>
                                      acceptFaultChainTurn(
                                        next,
                                        conversation.data!.data.conversationId,
                                        epoch,
                                      ),
                                  }
                                : undefined
                            }
                            onReadOnlyCheck={
                              canWriteConversation &&
                              item.turnId === active?.turnId
                                ? (draft) => {
                                    setMessage(draft);
                                    document
                                      .getElementById("assistant-message")
                                      ?.focus();
                                  }
                                : undefined
                            }
                            conversationId={
                              conversation.data?.data.conversationId
                            }
                            suggestions={
                              canWriteConversation &&
                              item.turnId === active?.turnId
                                ? turnSuggestions
                                : undefined
                            }
                            onManual={
                              manualHref && canWriteConversation
                                ? () => {
                                    navigate(manualHref);
                                  }
                                : undefined
                            }
                            onRetry={
                              canWriteConversation && !running && !busy
                                ? () => retryTurn(item.turnId)
                                : undefined
                            }
                          />
                        )}
                      </Card>
                    </List.Item>
                  )}
                />
                {investigationEntries.length > 0 && (
                  <UnifiedInvestigationHistory
                    entries={investigationEntries}
                    onOpen={(turnId) => setInvestigationDetail({ turnId })}
                  />
                )}
                {!messageCursor &&
                  active?.answer &&
                  !(messages.data?.data || []).some(
                    (item) =>
                      item.role === "assistant" &&
                      item.turnId === active.turnId,
                  ) && (
                    <Card size="small" title="本轮已保留的证据">
                      <DiagnosisAnswer
                        answer={active.answer}
                        turnId={active.turnId}
                        checkControls={
                          capabilities.data?.data.faultChainEnabled
                            ? {
                                conversation: conversation.data!.data,
                                disabled:
                                  !canWriteConversation ||
                                  Boolean(running) ||
                                  busy,
                                epoch: submissionEpoch,
                                onSubmitted: (next, epoch) =>
                                  acceptFaultChainTurn(
                                    next,
                                    conversation.data!.data.conversationId,
                                    epoch,
                                  ),
                              }
                            : undefined
                        }
                        conversationId={conversation.data?.data.conversationId}
                        // The fallback answer of a turn that is not in the
                        // message list is still the current turn: its
                        // suggestions fill the composer of the operator and
                        // never run a query on their own.
                        onReadOnlyCheck={
                          canWriteConversation
                            ? (draft) => {
                                setMessage(draft);
                                document
                                  .getElementById("assistant-message")
                                  ?.focus();
                              }
                            : undefined
                        }
                        suggestions={
                          canWriteConversation ? turnSuggestions : undefined
                        }
                        onManual={
                          manualHref && canWriteConversation
                            ? () => {
                                navigate(manualHref);
                              }
                            : undefined
                        }
                        onRetry={
                          canWriteConversation && !running && !busy
                            ? () => retryTurn(active.turnId)
                            : undefined
                        }
                      />
                    </Card>
                  )}
                {active && (
                  <Space>
                    <Tag>{active.state}</Tag>
                    {active.errorCode && (
                      <Typography.Text type="danger">
                        {active.errorCode}
                      </Typography.Text>
                    )}
                    {!active.answer &&
                      ["failed", "cancelled"].includes(active.state) &&
                      canWriteConversation &&
                      questionOfTurn(active.turnId) && (
                        <Button
                          disabled={busy || Boolean(running)}
                          onClick={() => retryTurn(active.turnId)}
                        >
                          以原问题重新提问
                        </Button>
                      )}
                    {active.jobId && (
                      <Button
                        onClick={() =>
                          setInvestigationDetail({
                            jobId: active.jobId,
                            turnId: active.turnId,
                          })
                        }
                      >
                        查看调查过程
                      </Button>
                    )}
                    {running && canWriteConversation && (
                      <Button
                        onClick={() =>
                          void mutate(async (signal, canCommit) => {
                            await checked(
                              cancelAssistantTurn(
                                active.turnId,
                                {},
                                requestOptions(signal, true),
                              ),
                            );
                          })
                        }
                      >
                        停止本轮
                      </Button>
                    )}
                  </Space>
                )}
                {streamError && (
                  <Alert
                    type="warning"
                    title="实时连接中断，持久任务仍在运行"
                    description={streamError.message}
                  />
                )}
                {conversation.data?.data.mode === "data_query" &&
                  Array.isArray(visibleDatasets) && (
                    <Space wrap aria-label="推荐问题">
                      {visibleDatasets.flatMap((dataset) => {
                        if (
                          !dataset ||
                          typeof dataset !== "object" ||
                          !("id" in dataset) ||
                          !("authorizationDomain" in dataset)
                        )
                          return [];
                        const key = String(dataset.id);
                        const example = queryExamples[key];
                        if (
                          !example ||
                          dataset.authorizationDomain !==
                            conversation.data?.data.authorizationDomain
                        )
                          return [];
                        return [
                          <Button
                            key={key}
                            disabled={
                              !canWriteConversation || !!running || busy
                            }
                            onClick={() => setMessage(example)}
                          >
                            {example}
                          </Button>,
                        ];
                      })}
                    </Space>
                  )}
                <Input.TextArea
                  aria-label="问题"
                  disabled={!canWriteConversation}
                  value={message}
                  id="assistant-message"
                  onChange={(e) => setMessage(e.target.value)}
                  maxLength={4000}
                  showCount
                  placeholder={
                    active?.state === "awaiting_user"
                      ? "补充澄清信息"
                      : "输入问题，例如：当前有多少异常 Pod？"
                  }
                />
                <Input.TextArea
                  aria-label="调查重点"
                  disabled={!canWriteConversation || !!running}
                  value={focus}
                  onChange={(e) => setFocus(e.target.value)}
                  maxLength={500}
                  showCount
                  rows={2}
                  placeholder="调查重点（可选，仅作为用户意图，不改变授权范围）"
                />
                <Space wrap>
                  <InputNumber
                    aria-label="工具调用上限"
                    disabled={!canWriteConversation || !!running}
                    value={tightenToolCalls}
                    min={0}
                    max={2000000000}
                    placeholder="工具调用上限"
                    onChange={(value) => setTightenToolCalls(value ?? null)}
                  />
                  <InputNumber
                    aria-label="模型请求上限"
                    disabled={!canWriteConversation || !!running}
                    value={tightenModelRequests}
                    min={0}
                    max={2000000000}
                    placeholder="模型请求上限"
                    onChange={(value) => setTightenModelRequests(value ?? null)}
                  />
                  <InputNumber
                    aria-label="运行时长上限（秒）"
                    disabled={!canWriteConversation || !!running}
                    value={tightenDurationSeconds}
                    min={0}
                    max={2000000000}
                    placeholder="运行时长上限（秒）"
                    onChange={(value) =>
                      setTightenDurationSeconds(value ?? null)
                    }
                  />
                  <Typography.Text type="secondary">
                    只能收紧服务端当次准入额度，实际生效值以本轮回显为准。
                    留空或填 0
                    表示该维度不收紧，仍沿用当次准入额度（不会禁止调用）；填写的值高于当次准入额度时按当次准入额度收紧，高于平台上限则整次提交被拒绝。
                  </Typography.Text>
                </Space>
                {invalidTightening && (
                  <Alert
                    type="error"
                    title="额度收紧值必须是非负整数，非法值不会被静默按默认值处理。"
                  />
                )}
                {active?.budget && (
                  <Card size="small" title="本轮实际额度">
                    <Typography.Text>
                      重点：{active.focus || "未指定"}
                    </Typography.Text>
                    <Typography.Paragraph>
                      工具调用 {active.budget.toolCalls} · 模型请求{" "}
                      {active.budget.modelRequests} · 时长{" "}
                      {active.budget.durationSeconds} 秒
                      {active.budget.consumed
                        ? ` · 已消耗工具调用 ${active.budget.consumed.toolCalls}`
                        : ""}
                    </Typography.Paragraph>
                  </Card>
                )}
                <Button
                  type="primary"
                  loading={busy}
                  disabled={
                    !canWriteConversation ||
                    !message.trim() ||
                    !!running ||
                    invalidTightening ||
                    !capabilities.data?.data.enabled
                  }
                  onClick={() => void send()}
                >
                  发送
                </Button>
              </QueryState>
            )}
          </Card>
          <Modal
            title="重命名会话"
            open={renameOpen}
            okText="保存"
            cancelText="取消"
            confirmLoading={renameBusy}
            okButtonProps={{ disabled: !renameIntent || renameBusy }}
            onCancel={() => {
              // A cancelled dialog drops its draft and its request. The
              // conversation keeps its name.
              renameAbort.current.abort();
              renameAbort.current = new AbortController();
              renameBusyRef.current = false;
              setRenameBusy(false);
              setRenameOpen(false);
              setRenameIntent(undefined);
              setRenameValue("");
              setRenameError(undefined);
            }}
            onOk={() => void rename()}
          >
            <Input
              aria-label="会话名称"
              maxLength={160}
              value={renameValue}
              onChange={(e) => setRenameValue(e.target.value)}
            />
            {renameError && <Alert type="error" title={renameError} />}
          </Modal>
          {investigationDetail && (
            <UnifiedInvestigationDetail
              jobId={investigationDetail.jobId}
              turnId={investigationDetail.turnId}
              scopeKey={scopeKey}
              onClose={() => {
                setInvestigationDetail(undefined);
                clearDetailLink();
              }}
              onStop={(target) => openStopConfirm(target)}
            />
          )}
          {stopTarget && (
            <InvestigationStopConfirm
              target={stopTarget}
              confirming={stopBusy}
              error={stopError}
              onCancel={() => {
                // A cancelled confirmation drops the frozen target and its
                // request. Nothing was cancelled.
                setStopTarget(undefined);
                setStopError(undefined);
              }}
              onConfirm={() => void confirmStop()}
            />
          )}
        </Col>
      </Row>
    </>
  );
}

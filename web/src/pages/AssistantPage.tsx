import { LegalHoldControl } from "../features/assistant/LegalHoldControl";
import { FaultChainCollector } from "../features/assistant/FaultChainCollector";
import { BindingEditor } from "../features/assistant/BindingEditor";
import { useState, useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  Col,
  Input,
  List,
  Row,
  Select,
  Space,
  Tag,
  Typography,
} from "antd";
import { useSearchParams } from "react-router-dom";
import { useAuth } from "../auth/AuthProvider";
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
import { PageHeading } from "../components/PageHeading";
import { QueryState } from "../components/QueryState";
import { DiagnosisAnswer } from "../features/assistant/DiagnosisAnswer";
import { ConversationContext } from "../features/assistant/ConversationContext";
import { useConversationStream } from "../features/assistant/useConversationStream";
export function AssistantPage() {
  const { session } = useAuth();
  const client = useQueryClient();
  const [url, setUrl] = useSearchParams();
  const id = url.get("conversation") || undefined;
  const scopeKey = JSON.stringify([
    session?.tenantId,
    session?.subject,
    session?.roles,
    session?.clusterScopes,
    session?.namespaceScopes,
  ]);
  const [mode, setMode] = useState<"data_query" | "troubleshoot">(
    url.get("incidentId") || url.get("canonicalId")
      ? "troubleshoot"
      : "data_query",
  );
  const [domain, setDomain] = useState<"operational" | "audit">(
    session?.roles.includes("operator") ? "operational" : "audit",
  );
  const [cluster, setCluster] = useState(
    url.get("clusterUid") || session?.authorizedClusters[0]?.clusterUid || "",
  );
  const [namespace, setNamespace] = useState("");
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
    url.get("canonicalId") ||
    (targetCandidates.some((c) => c.canonicalId === selectedTarget)
      ? selectedTarget
      : undefined);
  const [symptom, setSymptom] = useState("");
  const [windowMinutes, setWindowMinutes] = useState<number | "custom">(30);
  const [windowFrom, setWindowFrom] = useState(() =>
    new Date(Date.now() - 30 * 60000).toISOString().slice(0, 16),
  );
  const [windowTo, setWindowTo] = useState(() =>
    new Date().toISOString().slice(0, 16),
  );
  const [message, setMessage] = useState("");
  const [referent, setReferent] = useState<string>();
  const [listCursor, setListCursor] = useState<string>();
  const [messageCursor, setMessageCursor] = useState<string>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [turn, setTurn] = useState<AssistantTurnV1>();
  useEffect(() => {
    setTurn(undefined);
    setMessage("");
    setReferent(undefined);
    setError(undefined);
    setMessageCursor(undefined);
  }, [scopeKey, id]);
  useEffect(() => {
    setListCursor(undefined);
  }, [scopeKey]);
  const conversation = useQuery({
    queryKey: ["assistant", scopeKey, id, "conversation"],
    enabled: !!id,
    queryFn: ({ signal }) =>
      checked(getAssistantConversation(id!, requestOptions(signal))),
  });
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
  const conversations = useQuery({
    queryKey: ["assistant", scopeKey, "conversations", listCursor],
    queryFn: ({ signal }) =>
      checked(
        listAssistantConversations(
          { limit: 20, cursor: listCursor },
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
  const streamError = useConversationStream(id, scopeKey);
  const active =
    currentTurn.data?.data || turn || conversation.data?.data.activeTurn;
  const running = active && ["queued", "running"].includes(active.state);
  const reload = () =>
    client.invalidateQueries({ queryKey: ["assistant", scopeKey] });
  async function mutate(action: () => Promise<void>) {
    setBusy(true);
    setError(undefined);
    try {
      await action();
      await reload();
    } catch (cause) {
      const e = cause instanceof Error ? cause : new Error("操作失败");
      setError(e);
      if (e instanceof ApiError && [401, 403].includes(e.status))
        client.removeQueries({ queryKey: ["assistant", scopeKey] });
    } finally {
      setBusy(false);
    }
  }
  async function create() {
    await mutate(async () => {
      const now = new Date();
      const body: AssistantCreateConversationV1 = {
        title:
          mode === "data_query"
            ? "运维问数"
            : symptom.slice(0, 100) || "资源排查",
        mode,
        authorizationDomain: domain,
        timezone:
          Intl.DateTimeFormat().resolvedOptions().timeZone || "Asia/Shanghai",
        ...(domain === "operational"
          ? {
              clusterUid: cluster,
              namespaceSelection: namespace ? [namespace] : [],
              includeClusterScoped: !namespace,
            }
          : {}),
      };
      if (mode === "troubleshoot") {
        body.symptom = symptom;
        if (url.get("incidentId")) body.incidentId = url.get("incidentId")!;
        else {
          const from =
            windowMinutes === "custom"
              ? new Date(windowFrom + "Z")
              : new Date(now.getTime() - windowMinutes * 60000);
          const to =
            windowMinutes === "custom" ? new Date(windowTo + "Z") : now;
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
        createAssistantConversation(body, requestOptions(undefined, true)),
      );
      setTurn(undefined);
      setUrl({ conversation: out.data.conversationId });
    });
  }
  async function send() {
    if (!id || !conversation.data) return;
    await mutate(async () => {
      const clarification = active?.answer?.clarification?.clarificationId;
      const out = await checked(
        submitAssistantMessage(
          id,
          {
            message,
            expectedConversationRevision: conversation.data!.data.revision,
            ...(active ? { parentTurnId: active.turnId } : {}),
            ...(clarification ? { clarificationId: clarification } : {}),
            ...(referent ? { referentCanonicalId: referent } : {}),
          },
          requestOptions(undefined, true),
        ),
      );
      setTurn(out.data);
      setReferent(undefined);
      setMessage("");
      setMessageCursor(undefined);
    });
  }
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
            <QueryState
              loading={conversations.isPending}
              error={conversations.error}
              retry={() => void conversations.refetch()}
            >
              <Space>
                <Button
                  disabled={!listCursor}
                  onClick={() => setListCursor(undefined)}
                >
                  最新会话
                </Button>
                <Button
                  disabled={!conversations.data?.meta?.nextCursor}
                  onClick={() =>
                    setListCursor(conversations.data?.meta?.nextCursor)
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
            </QueryState>
          </Card>
        </Col>
        <Col xs={24} lg={17}>
          <Card
            title={conversation.data?.data.title || "新建会话"}
            extra={
              id && (
                <Button
                  disabled={busy || !!running}
                  onClick={() =>
                    void mutate(async () => {
                      await checked(
                        archiveAssistantConversation(
                          id,
                          {},
                          requestOptions(undefined, true),
                        ),
                      );
                      setUrl({});
                      setTurn(undefined);
                    })
                  }
                >
                  归档
                </Button>
              )
            }
          >
            {!id ? (
              <Space orientation="vertical" style={{ width: "100%" }}>
                <Select
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
                      placeholder="选择授权集群"
                      value={cluster || undefined}
                      onChange={setCluster}
                      options={session?.authorizedClusters.map((c) => ({
                        value: c.clusterUid,
                        label: c.displayName,
                      }))}
                    />
                    <Input
                      placeholder="命名空间子范围（空表示全部已授权命名空间）"
                      value={namespace}
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
                    {!url.get("incidentId") && !url.get("canonicalId") && (
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
                            void mutate(async () => {
                              const response = await checked(
                                resolveAssistantResource(
                                  {
                                    clusterUid: cluster,
                                    namespaceSelection: namespace
                                      ? [namespace]
                                      : [],
                                    includeClusterScoped: !namespace,
                                    selector: {
                                      name: targetName,
                                      kind: targetKind,
                                      ...(namespace ? { namespace } : {}),
                                    },
                                  },
                                  requestOptions(),
                                ),
                              );
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
                    {!url.get("incidentId") && (
                      <Button
                        disabled={!symptom || busy}
                        onClick={() =>
                          void mutate(async () => {
                            const response = await checked(
                              interpretAssistantSymptom(
                                { symptom },
                                requestOptions(),
                              ),
                            );
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
                    {!url.get("incidentId") && (
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
                                value={windowFrom}
                                onChange={(e) => setWindowFrom(e.target.value)}
                              />
                            </label>
                            <label>
                              结束时间（UTC）
                              <Input
                                aria-label="结束时间（UTC）"
                                type="datetime-local"
                                value={windowTo}
                                onChange={(e) => setWindowTo(e.target.value)}
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
                        : "当前没有可用的指标/日志分析准入证明，或 Worker 不可用；目录存在不代表来源可查询。"
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
                    binding={conversation.data.data.binding}
                    domain={conversation.data.data.authorizationDomain}
                    cluster={conversation.data.data.clusterUid}
                  />
                )}
                {conversation.data && (
                  <BindingEditor
                    key={id}
                    conversation={conversation.data.data}
                    running={Boolean(running)}
                    onChanged={() => {
                      setTurn(undefined);
                      setMessageCursor(undefined);
                      setReferent(undefined);
                    }}
                  />
                )}
                {conversation.data?.data.mode === "troubleshoot" &&
                  capabilities.data?.data.faultChainEnabled && (
                    <FaultChainCollector
                      key={id}
                      conversation={conversation.data.data}
                      disabled={Boolean(running) || busy}
                      onSubmitted={(next) => {
                        setTurn(next);
                        setMessageCursor(undefined);
                        void reload();
                      }}
                    />
                  )}
                {active?.answer?.clarification?.missingFields.includes(
                  "referentCanonicalId",
                ) && (
                  <Select
                    aria-label="确认历史资源指代"
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
                        title={item.role === "user" ? "我" : "助手"}
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
                          />
                        )}
                      </Card>
                    </List.Item>
                  )}
                />
                {active && (
                  <Space>
                    <Tag>{active.state}</Tag>
                    {active.errorCode && (
                      <Typography.Text type="danger">
                        {active.errorCode}
                      </Typography.Text>
                    )}
                    {running && (
                      <Button
                        onClick={() =>
                          void mutate(async () => {
                            await checked(
                              cancelAssistantTurn(
                                active.turnId,
                                {},
                                requestOptions(undefined, true),
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
                            disabled={!!running || busy}
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
                  value={message}
                  onChange={(e) => setMessage(e.target.value)}
                  maxLength={4000}
                  showCount
                  placeholder={
                    active?.state === "awaiting_user"
                      ? "补充澄清信息"
                      : "输入问题，例如：当前有多少异常 Pod？"
                  }
                />
                <Button
                  type="primary"
                  loading={busy}
                  disabled={
                    !message.trim() ||
                    !!running ||
                    !capabilities.data?.data.enabled
                  }
                  onClick={() => void send()}
                >
                  发送
                </Button>
              </QueryState>
            )}
          </Card>
        </Col>
      </Row>
    </>
  );
}

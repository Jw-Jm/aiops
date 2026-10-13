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
  const [message, setMessage] = useState("");
  const [listCursor, setListCursor] = useState<string>();
  const [messageCursor, setMessageCursor] = useState<string>();
  const [error, setError] = useState<Error>();
  const [busy, setBusy] = useState(false);
  const [turn, setTurn] = useState<AssistantTurnV1>();
  useEffect(() => {
    setTurn(undefined);
    setMessage("");
    setError(undefined);
    setMessageCursor(undefined);
  }, [scopeKey, id]);
  useEffect(() => {
    setListCursor(undefined);
  }, [scopeKey]);
  const capabilities = useQuery({
    queryKey: ["assistant", scopeKey, "capabilities"],
    queryFn: ({ signal }) =>
      checked(getAssistantCapabilities(requestOptions(signal))),
  });
  const visibleDatasets = capabilities.data?.data.catalog.datasets;
  const queryExamples: Record<string, string> = {
    "resources.current":
      "当前授权范围内有多少 Pod？按健康状态分组，说明未知和降级状态。",
    "findings.occurrences":
      "过去七天的 Finding 有多少？按天统计，并说明时间是否可靠。",
    "incidents.records": "过去七天新增多少 Incident？按当前状态分组。",
    "executions.records": "过去七天我提交的执行任务有多少？按状态分组。",
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
  const conversation = useQuery({
    queryKey: ["assistant", scopeKey, id, "conversation"],
    enabled: !!id,
    queryFn: ({ signal }) =>
      checked(getAssistantConversation(id!, requestOptions(signal))),
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
      const from = new Date(now.getTime() - 30 * 60 * 1000);
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
        body.timeRange = {
          from: from.toISOString(),
          to: now.toISOString(),
          timezone: body.timezone,
        };
        if (url.get("incidentId")) body.incidentId = url.get("incidentId")!;
        else body.resourceCanonicalIds = boundTarget ? [boundTarget] : [];
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
          },
          requestOptions(undefined, true),
        ),
      );
      setTurn(out.data);
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
                  value={mode}
                  onChange={(value) => {
                    setMode(value);
                    if (value === "troubleshoot") setDomain("operational");
                  }}
                  options={[
                    { value: "data_query", label: "问数" },
                    ...(session?.roles.includes("operator")
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
                  </>
                )}
                <Button
                  type="primary"
                  loading={busy}
                  disabled={
                    !capabilities.data?.data.enabled ||
                    (mode === "troubleshoot" &&
                      !url.get("incidentId") &&
                      !boundTarget)
                  }
                  onClick={() => void create()}
                >
                  创建会话
                </Button>
              </Space>
            ) : (
              <QueryState
                loading={messages.isPending || conversation.isPending}
                error={messages.error || conversation.error}
                retry={() => void reload()}
              >
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

import {
  ExecutionProfilePreview,
  parseSignedProfile,
} from "./ExecutionProfilePreview";
import { PolicyImpactPreview, type ImpactReceipt } from "./PolicyImpactPreview";
import { type NativeWriteIntent } from "../../auth/NativeStepUpControl";
import {
  administrativeWrite,
  useNativeWriteProof,
} from "../../auth/useNativeWriteProof";
import { useAuth } from "../../auth/AuthProvider";
import { StepUpControl } from "../../auth/StepUpControl";
import { RegistryEditor } from "./RegistryEditor";
import { FullText } from "../../components/Details";
import { useQuery, useMutation } from "@tanstack/react-query";
import { useState, useEffect } from "react";
import {
  Tabs,
  Table,
  Button,
  Input,
  InputNumber,
  Select,
  Alert,
  Space,
} from "antd";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  getSourceRegistrations,
  getPolicyBundles,
  getExecutionProfiles,
  getClusters,
  updateSourceRegistration,
  rollbackSourceRegistration,
  publishPolicyBundle,
  createExecutionProfile,
  retireExecutionProfile,
} from "../../api/generated/platform";
import { checked, requestOptions, ApiError } from "../../api/client";
import { PageHeading } from "../../components/PageHeading";
import { QueryState } from "../../components/QueryState";
import { Details } from "../../components/Details";
import { Status } from "../../components/Status";
import { record, list, text } from "../../components/data";
import { useClusterScope, useConfigDraft } from "../../app/ClusterContext";
import { Tag } from "antd";
export function AdminPage({
  section,
}: {
  section: "sources" | "policies" | "execution-profiles" | "deployment";
}) {
  const navigate = useNavigate();
  const { configClusterUid, configCandidates, configGeneration } =
    useClusterScope();
  const configCluster = configCandidates.find(
    (c) => c.clusterUid === configClusterUid,
  );
  const clusterScoped = section === "sources";
  const auth = useAuth();
  const [sourceAction, setSourceAction] = useState<"update" | "rollback">(
    "update",
  );
  const [stepExpires, setStepExpires] = useState("");
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  const oidcReady = Date.parse(stepExpires) > now;
  const [rollbackRevision, setRollbackRevision] = useState(1);
  const [url, setUrl] = useSearchParams();
  const [selected, setSelected] = useState<Record<string, unknown>>();
  const [status, setStatus] = useState<"active" | "disabled">("disabled");
  const [revision, setRevision] = useState(1);
  const [draft, setDraft] = useState("");
  const [signer, setSigner] = useState("");
  const [signature, setSignature] = useState("");
  const [publishReceipt, setPublishReceipt] = useState<ImpactReceipt>();
  const publishStamp = JSON.stringify([draft, revision, signer, signature]);
  const sourceId = text(selected?.sourceRegistrationId ?? selected?.sourceId);
  const sourceIntent: NativeWriteIntent = {
    operation: "source_administration",
    target: { kind: "source_registration", registrationId: sourceId, revision },
    write:
      sourceAction === "update"
        ? {
            method: "PATCH",
            path: `/api/v1/admin/source-registrations/${sourceId}`,
            body: { expectedRevision: revision, status },
          }
        : {
            method: "POST",
            path: `/api/v1/admin/source-registrations/${sourceId}/rollback`,
            body: {
              expectedRevision: revision,
              targetRevision: rollbackRevision,
            },
          },
  };
  const sourceProof = useNativeWriteProof(
    selected && section === "sources" ? sourceIntent : undefined,
    oidcReady,
  );
  const ready = sourceProof.ready;
  const sourceOptions = sourceProof.options;
  const publishReady =
    publishReceipt?.stamp === publishStamp && publishReceipt.expiresAt > now;
  const [content, setContent] = useState("");
  const [profileReviewed, setProfileReviewed] = useState<string>();
  const policyBody = {
    draftId: draft,
    expectedRevision: revision,
    signerKeyId: signer,
    signature,
  };
  const policyProof = useNativeWriteProof(
    section === "policies" && draft && signer && signature
      ? administrativeWrite(
          "registry_administration",
          "POST",
          "/api/v1/admin/policy-bundles:publish",
          policyBody,
        )
      : undefined,
    oidcReady,
  );
  let profileBody: ReturnType<typeof parseSignedProfile> | undefined;
  try {
    if (content) profileBody = parseSignedProfile(content);
  } catch {
    /* The preview reports invalid input. */
  }
  const profileProof = useNativeWriteProof(
    section === "execution-profiles" && profileBody
      ? administrativeWrite(
          "execution_profile_administration",
          "POST",
          "/api/v1/admin/execution-profiles",
          profileBody,
        )
      : undefined,
    oidcReady,
  );
  const retireProof = useNativeWriteProof(
    section === "execution-profiles" && selected
      ? administrativeWrite(
          "execution_profile_administration",
          "POST",
          `/api/v1/admin/execution-profiles/${text(selected.executionProfileId)}/versions/${Number(selected.version)}:retire`,
          undefined,
        )
      : undefined,
    oidcReady,
  );
  useConfigDraft(
    !!selected ||
      draft.trim().length > 0 ||
      content.trim().length > 0 ||
      !!publishReceipt,
  );
  useEffect(() => {
    setSelected(undefined);
    setRevision(1);
    setStatus("disabled");
    setDraft("");
    setSigner("");
    setSignature("");
    setPublishReceipt(undefined);
    setContent("");
    setProfileReviewed(undefined);
    setStepExpires("");
  }, [configClusterUid, configGeneration]);
  const cursor = url.get("cursor") || undefined;
  const query = useQuery({
    queryKey: ["admin", section, cursor, configClusterUid, configGeneration],
    enabled: !clusterScoped || !!configCluster?.clusterId,
    queryFn: async ({ signal }) => {
      if (section === "sources" && configCluster?.clusterId) {
        let next: string | undefined;
        const all: unknown[] = [];
        do {
          const response = await checked(
            getSourceRegistrations(
              { limit: 100, cursor: next },
              requestOptions(signal),
            ),
          );
          const envelope = record(response);
          all.push(...list(envelope.data));
          const cursorValue = record(envelope.meta).nextCursor;
          next =
            typeof cursorValue === "string" && cursorValue
              ? cursorValue
              : undefined;
        } while (next && !signal.aborted);
        return {
          data: all.filter(
            (row) => text(record(row).clusterId) === configCluster.clusterId,
          ),
          meta: {},
        };
      }
      return section === "sources"
        ? checked(
            getSourceRegistrations(
              { limit: 50, cursor },
              requestOptions(signal),
            ),
          )
        : section === "policies"
          ? checked(
              getPolicyBundles({ limit: 50, cursor }, requestOptions(signal)),
            )
          : section === "execution-profiles"
            ? checked(
                getExecutionProfiles(
                  { limit: 50, cursor },
                  requestOptions(signal),
                ),
              )
            : checked(
                getClusters({ limit: 50, cursor }, requestOptions(signal)),
              );
    },
  });
  const rows = list(record(query.data).data).map(record);
  const nextCursorValue = record(record(query.data).meta).nextCursor;
  const nextCursor =
    typeof nextCursorValue === "string" && nextCursorValue
      ? nextCursorValue
      : undefined;
  const runtime = useQuery({
    queryKey: ["runtime-config"],
    enabled: section === "deployment",
    queryFn: async () => {
      const r = await fetch("/runtime-config.json", { cache: "no-store" });
      if (!r.ok) throw new Error("发行配置不可用");
      return r.json();
    },
  });
  const update = useMutation({
    mutationFn: () =>
      checked(
        updateSourceRegistration(
          text(selected?.sourceRegistrationId ?? selected?.sourceId),
          { expectedRevision: revision, status },
          sourceOptions(),
        ),
      ),
    onSuccess: () => {
      setSelected(undefined);
      void query.refetch();
    },
  });
  const rollback = useMutation({
    mutationFn: () =>
      checked(
        rollbackSourceRegistration(
          text(selected?.sourceRegistrationId ?? selected?.sourceId),
          { expectedRevision: revision, targetRevision: rollbackRevision },
          sourceOptions(),
        ),
      ),
    onSuccess: () => void query.refetch(),
  });
  const publish = useMutation({
    mutationFn: () => {
      if (!publishReady) throw new Error("请重新读取并核对发布影响");
      return checked(publishPolicyBundle(policyBody, policyProof.options()));
    },
    onSuccess: () => {
      setPublishReceipt(undefined);
      void query.refetch();
    },
  });
  const publishProfile = useMutation({
    mutationFn: () => {
      if (profileReviewed !== content) throw new Error("请先核对执行配置影响");
      return checked(
        createExecutionProfile(
          parseSignedProfile(content),
          profileProof.options(),
        ),
      );
    },
    onSuccess: () => {
      setProfileReviewed(undefined);
      void query.refetch();
    },
  });
  const retire = useMutation({
    mutationFn: () =>
      checked(
        retireExecutionProfile(
          text(selected?.executionProfileId),
          Number(selected?.version),
          retireProof.options(),
        ),
      ),
    onSuccess: () => void query.refetch(),
  });
  return (
    <>
      <PageHeading
        title="平台管理"
        description="管理配置不赋予命令执行权限。更新使用已读取的 revision，发布需要可验证签名。"
        extra={
          <Tag color="geekblue">
            {clusterScoped
              ? `配置集群 · ${configCluster?.displayName || "未选择"}`
              : "租户级配置"}
          </Tag>
        }
      />
      <Tabs
        activeKey={section}
        onChange={(v) => {
          const path: Record<string, string> = {
            sources: "/access/sources",
            policies: "/settings/policies",
            "execution-profiles": "/settings/execution",
            deployment: "/settings/deployment",
            audit: "/settings/audit",
          };
          navigate(path[v] || "/access/sources");
        }}
        items={[
          { key: "sources", label: "来源" },
          { key: "policies", label: "策略" },
          { key: "execution-profiles", label: "执行配置" },
          { key: "deployment", label: "部署状态" },
          { key: "audit", label: "审计" },
        ]}
      />
      {clusterScoped && !configCluster ? (
        <Alert
          type="info"
          showIcon
          title="请选择配置集群"
          description="数据来源按注册集群读取；未选择时不会展示其他集群的来源。"
        />
      ) : null}
      <section className="section">
        <QueryState
          loading={query.isPending}
          error={query.error}
          retry={() => void query.refetch()}
          empty={rows.length === 0}
        >
          <Table
            rowKey={(r, i) =>
              text(
                r.sourceRegistrationId ??
                  r.sourceId ??
                  r.versionId ??
                  r.executionProfileId ??
                  r.clusterId ??
                  i,
              ) + text(r.version)
            }
            dataSource={rows}
            pagination={false}
            scroll={{ x: 780 }}
            expandable={{ expandedRowRender: (r) => <Details value={r} /> }}
            columns={[
              {
                title: "配置名称",
                render: (_, r) => (
                  <Button
                    type="link"
                    onClick={() => {
                      setSelected(r);
                      if (
                        section === "sources" &&
                        ["active", "disabled"].includes(text(r.status))
                      )
                        setStatus(r.status as "active" | "disabled");
                      setRevision(Number(r.revision ?? 1));
                    }}
                  >
                    {text(
                      r.name ??
                        r.logicalName ??
                        r.instanceKey ??
                        r.sourceId ??
                        r.sourceRegistrationId ??
                        r.executionProfileId ??
                        r.clusterId,
                    )}
                  </Button>
                ),
              },
              {
                title: "状态",
                render: (_, r) => (
                  <Status value={r.status ?? r.state ?? "unknown"} />
                ),
              },
              {
                title: "版本 / 修订号",
                render: (_, r) =>
                  text(r.version ?? r.versionNumber ?? r.revision),
              },
              {
                title: "内容摘要（Digest）",
                render: (_, r) => (
                  <FullText value={r.digest ?? r.profileDigest} />
                ),
              },
            ]}
          />
          <Space className="status-note">
            <Button disabled={!cursor} onClick={() => setUrl({})}>
              回到第一页
            </Button>
            <Button
              disabled={!nextCursor}
              onClick={() =>
                setUrl({
                  cursor: nextCursor!,
                })
              }
            >
              下一页
            </Button>
          </Space>
        </QueryState>
      </section>
      {section !== "deployment" && auth.authMode !== "native" && (
        <section className="section">
          <StepUpControl onReady={setStepExpires} />
        </section>
      )}
      {selected && (
        <section className="section">
          <h3>已选择配置</h3>
          <Details value={selected} />
          {section === "sources" && (
            <>
              <h4>来源变更影响范围</h4>
              <Details
                value={{
                  scope: selected.dataScopeMapping,
                  allowedSchemas: selected.allowedSchemas,
                  backendLogicalId: selected.backendLogicalId,
                }}
              />
              <p>
                启停或回滚影响此来源后续采集与上述授权范围；既有 Evidence
                与审计记录保留。
              </p>
            </>
          )}
          {section === "execution-profiles" && (
            <>
              <h4>执行配置作用域与引用</h4>
              <Details
                value={{
                  scope: selected.scope ?? selected.allowedTargets,
                  referenceCount: selected.referenceCount,
                  referenceCountScope: selected.referenceCountScope,
                }}
              />
              <p>
                版本发布不替换既有版本；引用信息缺失不代表无人使用。退役时服务端仍校验实际引用和权限。
              </p>
            </>
          )}
          {section === "sources" && (
            <>
              <Space wrap className="status-note">
                <label>
                  已读取的修订号{" "}
                  <InputNumber
                    min={1}
                    value={revision}
                    onChange={(v) => setRevision(v ?? 1)}
                  />
                </label>
                <Select
                  aria-label="来源新状态"
                  value={status}
                  onChange={setStatus}
                  options={[
                    { value: "active", label: "启用" },
                    { value: "disabled", label: "停用" },
                  ]}
                />
                {auth.authMode === "native" ? (
                  <Select
                    aria-label="待验证来源操作"
                    value={sourceAction}
                    onChange={setSourceAction}
                    options={[
                      { value: "update", label: "修改来源状态" },
                      { value: "rollback", label: "按修订号回滚" },
                    ]}
                  />
                ) : null}
                <Button
                  disabled={
                    !ready ||
                    (auth.authMode === "native" && sourceAction !== "update")
                  }
                  loading={update.isPending}
                  onClick={() => update.mutate()}
                >
                  保存来源状态
                </Button>
                <label>
                  回滚到修订号{" "}
                  <InputNumber
                    min={1}
                    value={rollbackRevision}
                    onChange={(v) => setRollbackRevision(v ?? 1)}
                  />
                </label>
                <Button
                  disabled={
                    !ready ||
                    rollbackRevision === revision ||
                    (auth.authMode === "native" && sourceAction !== "rollback")
                  }
                  loading={rollback.isPending}
                  onClick={() => rollback.mutate()}
                >
                  按修订号回滚来源
                </Button>
              </Space>
              {sourceProof.verification}
            </>
          )}
          {section === "execution-profiles" && (
            <>
              {retireProof.verification}
              <Button
                danger
                disabled={!retireProof.ready}
                loading={retire.isPending}
                onClick={() => retire.mutate()}
              >
                退役此精确版本
              </Button>
            </>
          )}
        </section>
      )}
      {section === "policies" && (
        <RegistryEditor
          selected={selected}
          ready={oidcReady}
          onChanged={() => void query.refetch()}
        />
      )}{" "}
      {section === "policies" && (
        <section className="section">
          <h3>发布已签名策略草稿</h3>
          <Alert
            showIcon
            type="warning"
            title="核对作用域和引用影响"
            description="发布只接受外部签名；私钥不进入浏览器。修订号冲突后需要重新读取草稿并重新核对。"
          />
          <div className="action-grid">
            <Input
              aria-label="策略 draftId"
              placeholder="已读取的草稿编号"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
            />
            <InputNumber
              aria-label="策略 expectedRevision"
              min={1}
              value={revision}
              onChange={(v) => setRevision(v ?? 1)}
            />
            <Input
              aria-label="签名 key ID"
              placeholder="外部签名密钥编号"
              value={signer}
              onChange={(e) => setSigner(e.target.value)}
            />
            <Input
              aria-label="公开签名"
              placeholder="外部生成的公开签名"
              value={signature}
              onChange={(e) => setSignature(e.target.value)}
            />
          </div>
          <PolicyImpactPreview
            stamp={publishStamp}
            draftId={draft}
            expectedRevision={revision}
            onReviewed={setPublishReceipt}
          />
          {policyProof.verification}
          <Button
            disabled={
              !policyProof.ready ||
              !draft ||
              !signer ||
              !signature ||
              !publishReady
            }
            loading={publish.isPending}
            onClick={() => publish.mutate()}
          >
            发布已签名版本
          </Button>
        </section>
      )}
      {section === "execution-profiles" && (
        <section className="section">
          <h3>发布签名执行配置</h3>
          <p>
            上传公共 Contract 的 profile / signerKeyId / signature
            JSON；版本不可覆盖，不保存私钥或凭据。
          </p>
          <Input.TextArea
            aria-label="签名执行配置 JSON"
            rows={6}
            value={content}
            onChange={(e) => setContent(e.target.value)}
          />
          <ExecutionProfilePreview
            content={content}
            onReviewed={setProfileReviewed}
          />
          {profileProof.verification}
          <Button
            disabled={
              !profileProof.ready || !content || profileReviewed !== content
            }
            loading={publishProfile.isPending}
            onClick={() => publishProfile.mutate()}
          >
            发布精确配置版本
          </Button>
        </section>
      )}
      {section === "deployment" && (
        <section className="section">
          <h3>前端发行物与运行配置</h3>
          <QueryState loading={runtime.isPending} error={runtime.error}>
            <Details value={runtime.data} />
          </QueryState>
          <p className="status-note">
            摘要与签名取自部署提供的公开发行回执；未提供的值显示为缺失，不将
            Ready 当成业务验收。
          </p>
        </section>
      )}
      {[
        update.error,
        rollback.error,
        publish.error,
        publishProfile.error,
        retire.error,
      ]
        .filter(Boolean)
        .map((e, i) => (
          <div key={i}>
            <QueryState
              loading={false}
              error={e}
              retry={() => void query.refetch()}
            />
            {e instanceof ApiError && e.status === 409 && (
              <Alert
                showIcon
                type="warning"
                title="配置已被其他请求修改"
                description="重新加载最新版本并核对差异，再填写 expectedRevision；不会自动覆盖。"
              />
            )}
          </div>
        ))}
    </>
  );
}

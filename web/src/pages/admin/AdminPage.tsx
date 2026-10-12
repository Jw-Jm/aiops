import {
  ExecutionProfilePreview,
  parseSignedProfile,
} from "./ExecutionProfilePreview";
import { PolicyImpactPreview, type ImpactReceipt } from "./PolicyImpactPreview";
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
import { Details, JSONView } from "../../components/Details";
import { Status } from "../../components/Status";
import { record, list, text } from "../../components/data";
export function AdminPage({
  section,
}: {
  section: "sources" | "policies" | "execution-profiles" | "deployment";
}) {
  const navigate = useNavigate();
  const [stepExpires, setStepExpires] = useState("");
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  const ready = Date.parse(stepExpires) > now;
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
  const publishReady =
    publishReceipt?.stamp === publishStamp && publishReceipt.expiresAt > now;
  const [content, setContent] = useState("");
  const [profileReviewed, setProfileReviewed] = useState<string>();
  const cursor = url.get("cursor") || undefined;
  const query = useQuery({
    queryKey: ["admin", section, cursor],
    queryFn: async ({ signal }) =>
      section === "sources"
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
              ),
  });
  const rows = list(record(query.data).data).map(record);
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
          requestOptions(undefined, true),
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
          requestOptions(undefined, true),
        ),
      ),
    onSuccess: () => void query.refetch(),
  });
  const publish = useMutation({
    mutationFn: () => {
      if (!publishReady) throw new Error("请重新读取并核对发布影响");
      return checked(
        publishPolicyBundle(
          {
            draftId: draft,
            expectedRevision: revision,
            signerKeyId: signer,
            signature,
          },
          requestOptions(undefined, true),
        ),
      );
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
          requestOptions(undefined, true),
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
          requestOptions(undefined, true),
        ),
      ),
    onSuccess: () => void query.refetch(),
  });
  return (
    <>
      <PageHeading
        title="平台管理"
        description="管理配置不赋予命令执行权限。更新使用已读取的 revision，发布需要可验证签名。"
      />
      <Tabs
        activeKey={section}
        onChange={(v) => navigate("/admin/" + v)}
        items={[
          { key: "sources", label: "来源" },
          { key: "policies", label: "策略" },
          { key: "execution-profiles", label: "执行配置" },
          { key: "deployment", label: "部署状态" },
          { key: "audit", label: "审计" },
        ]}
      />
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
              disabled={!record(record(query.data).meta).nextCursor}
              onClick={() =>
                setUrl({
                  cursor: text(record(record(query.data).meta).nextCursor),
                })
              }
            >
              下一页
            </Button>
          </Space>
        </QueryState>
      </section>
      {section !== "deployment" && (
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
              <Button
                disabled={!ready}
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
                disabled={!ready || rollbackRevision === revision}
                loading={rollback.isPending}
                onClick={() => rollback.mutate()}
              >
                按修订号回滚来源
              </Button>
            </Space>
          )}
          {section === "execution-profiles" && (
            <Button
              danger
              disabled={!ready}
              loading={retire.isPending}
              onClick={() => retire.mutate()}
            >
              退役此精确版本
            </Button>
          )}
        </section>
      )}
      {section === "policies" && (
        <RegistryEditor
          selected={selected}
          ready={ready}
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
          <Button
            disabled={
              !ready || !draft || !signer || !signature || !publishReady
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
          <Button
            disabled={!ready || !content || profileReviewed !== content}
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

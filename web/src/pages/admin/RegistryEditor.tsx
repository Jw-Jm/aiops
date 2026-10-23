import { useAuth } from "../../auth/AuthProvider";
import {
  administrativeWrite,
  useNativeWriteProof,
} from "../../auth/useNativeWriteProof";
import { PolicyImpactPreview, type ImpactReceipt } from "./PolicyImpactPreview";
import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import {
  Input,
  InputNumber,
  Button,
  Space,
  Alert,
  Select,
  Checkbox,
} from "antd";
import {
  getRegistryDraft,
  createRegistryDraft,
  updateRegistryDraft,
  activateRegistryVersion,
  retireRegistryVersion,
} from "../../api/generated/platform";
import type {
  RegistryActivationRequest,
  RegistryDraftCreateRequest,
} from "../../api/generated/models";
import { checked, requestOptions } from "../../api/client";
import { record, text } from "../../components/data";
import { Details, JSONView } from "../../components/Details";
import { QueryState } from "../../components/QueryState";
export function RegistryEditor({
  selected,
  ready,
  onChanged,
}: {
  selected?: Record<string, unknown>;
  ready: boolean;
  onChanged: () => void;
}) {
  const auth = useAuth();
  const [writeAction, setWriteAction] = useState<
    "create" | "update" | "activate" | "retire"
  >("create");
  const [logicalName, setLogicalName] = useState("");
  const [content, setContent] = useState("");
  const [draftId, setDraftId] = useState("");
  const [expectedRevision, setExpectedRevision] = useState(1);
  const [scopeType, setScopeType] =
    useState<RegistryActivationRequest["scopeType"]>("tenant");
  const [clusterId, setClusterId] = useState("");
  const [namespace, setNamespace] = useState("");
  const [targetVersion, setTargetVersion] = useState("");
  const [result, setResult] = useState<unknown>();
  const [impactReceipt, setImpactReceipt] = useState<ImpactReceipt>();
  const activationStamp = JSON.stringify([
    selected?.logicalName,
    selected?.versionId,
    targetVersion,
    scopeType,
    clusterId,
    namespace,
  ]);
  const impactReady =
    impactReceipt?.stamp === activationStamp &&
    impactReceipt.expiresAt > Date.now();
  const [reviewedMerge, setReviewedMerge] = useState(false);
  const currentDraft = useMutation({
    mutationFn: (id: string) => checked(getRegistryDraft(id, requestOptions())),
    onSuccess: () => setReviewedMerge(false),
  });
  const serverDraft =
    currentDraft.data?.data.draftId === draftId
      ? currentDraft.data.data
      : undefined;

  let draftContent: Record<string, unknown> | undefined;
  try {
    draftContent = record(JSON.parse(content));
  } catch {
    /* Parse failures remain visible when submitting. */
  }
  const createBody: RegistryDraftCreateRequest = {
    kind: "policy",
    logicalName,
    content: draftContent ?? {},
  };
  const updateBody = { expectedRevision, content: draftContent ?? {} };
  const activationBody: RegistryActivationRequest = {
    kind: "policy",
    logicalName: text(selected?.logicalName),
    versionId: targetVersion || text(selected?.versionId),
    scopeType,
    clusterId: scopeType === "tenant" ? null : clusterId,
    namespace: scopeType === "namespace" ? namespace : undefined,
    expectedRevision: impactReceipt?.revision ?? 0,
  };
  const retireBody = { expectedDigest: text(selected?.digest) };
  const intent =
    writeAction === "create"
      ? draftContent && logicalName
        ? administrativeWrite(
            "registry_administration",
            "POST",
            "/api/v1/admin/registry-drafts",
            createBody,
          )
        : undefined
      : writeAction === "update"
        ? draftContent && draftId
          ? administrativeWrite(
              "registry_administration",
              "PATCH",
              `/api/v1/admin/registry-drafts/${draftId}`,
              updateBody,
            )
          : undefined
        : writeAction === "activate"
          ? impactReady
            ? administrativeWrite(
                "registry_administration",
                "POST",
                "/api/v1/admin/registry-activations",
                activationBody,
              )
            : undefined
          : selected?.versionId && selected?.digest
            ? administrativeWrite(
                "registry_administration",
                "POST",
                `/api/v1/admin/registry-versions/${text(selected.versionId)}:retire`,
                retireBody,
              )
            : undefined;
  const proof = useNativeWriteProof(intent, ready);
  const canWrite = (action: typeof writeAction) =>
    proof.ready && (auth.authMode !== "native" || writeAction === action);
  const created = useMutation({
    mutationFn: () => {
      if (!draftContent) throw new Error("策略内容不是有效 JSON");
      return checked(createRegistryDraft(createBody, proof.options()));
    },
    onSuccess: (r) => {
      const d = record(record(r).data);
      setDraftId(text(d.draftId));
      setExpectedRevision(Number(d.revision));
      setResult(d);
      onChanged();
    },
  });
  const updated = useMutation({
    mutationFn: () =>
      checked(updateRegistryDraft(draftId, updateBody, proof.options())),
    onSuccess: (r) => {
      setResult(record(r).data);
      setExpectedRevision(Number(record(record(r).data).revision));
      onChanged();
    },
  });
  const activated = useMutation({
    mutationFn: () => {
      if (!impactReady || impactReceipt?.revision === undefined)
        throw new Error("请重新读取并核对影响预览");
      return checked(activateRegistryVersion(activationBody, proof.options()));
    },
    onSuccess: (r) => {
      setImpactReceipt(undefined);
      setResult(record(r).data);
      onChanged();
    },
  });
  const retired = useMutation({
    mutationFn: () =>
      checked(
        retireRegistryVersion(
          text(selected?.versionId),
          retireBody,
          proof.options(),
        ),
      ),
    onSuccess: (r) => {
      setResult(record(r).data);
      onChanged();
    },
  });
  return (
    <>
      {auth.authMode === "native" ? (
        <section className="section">
          <Select
            aria-label="待验证策略操作"
            value={writeAction}
            onChange={setWriteAction}
            options={[
              { value: "create", label: "创建策略草稿" },
              { value: "update", label: "更新草稿" },
              { value: "activate", label: "激活或回滚版本" },
              { value: "retire", label: "退役版本" },
            ]}
          />
          {proof.verification}
        </section>
      ) : null}
      <section className="section">
        <h3>策略草稿</h3>
        <p>
          创建后保留服务返回的草稿编号和修订号。JSON 内容遵循正式策略
          Contract；不接受私钥、密码或执行凭据。
        </p>
        <div className="action-grid">
          <Input
            aria-label="策略逻辑名称"
            placeholder="策略名称"
            value={logicalName}
            onChange={(e) => setLogicalName(e.target.value)}
          />
          <Input
            aria-label="更新策略 draftId"
            placeholder="已读取的草稿编号"
            value={draftId}
            onChange={(e) => setDraftId(e.target.value)}
          />
          <InputNumber
            aria-label="草稿 expectedRevision"
            min={1}
            value={expectedRevision}
            onChange={(v) => setExpectedRevision(v ?? 1)}
          />
        </div>
        <Input.TextArea
          aria-label="策略草稿内容 JSON"
          rows={7}
          value={content}
          onChange={(e) => {
            setContent(e.target.value);
            setReviewedMerge(false);
          }}
        />
        <Space wrap className="status-note">
          <Button
            aria-label="重新读取草稿（保留本地修改）"
            disabled={!draftId}
            loading={currentDraft.isPending}
            onClick={() => currentDraft.mutate(draftId)}
          >
            重新读取草稿（保留本地修改）
          </Button>
          <Button
            disabled={!canWrite("create") || !logicalName || !content}
            loading={created.isPending}
            onClick={() => created.mutate()}
          >
            创建策略草稿
          </Button>
          <Button
            aria-label="按修订号更新草稿"
            disabled={!canWrite("update") || !draftId || !content}
            loading={updated.isPending}
            onClick={() => updated.mutate()}
          >
            按修订号更新草稿
          </Button>
        </Space>
        {serverDraft && (
          <section aria-label="草稿冲突恢复">
            <h3>服务端草稿 · 修订号 {serverDraft.revision}</h3>
            <p>
              上方保留本地内容。先比较服务端内容与本地修改，再选择采用或手动合并。
            </p>
            <JSONView value={serverDraft.content} />
            <Space wrap>
              <Button
                onClick={() => {
                  setLogicalName(serverDraft.logicalName);
                  setContent(JSON.stringify(serverDraft.content, null, 2));
                  setExpectedRevision(serverDraft.revision);
                  updated.reset();
                  setReviewedMerge(false);
                }}
              >
                采用服务端内容与修订号
              </Button>
              <Checkbox
                checked={reviewedMerge}
                onChange={(e) => setReviewedMerge(e.target.checked)}
              >
                已比较并在上方完成手动合并
              </Checkbox>
              <Button
                disabled={!reviewedMerge}
                onClick={() => {
                  setExpectedRevision(serverDraft.revision);
                  updated.reset();
                  setReviewedMerge(false);
                }}
              >
                保留合并内容，采用已读取修订号
              </Button>
            </Space>
          </section>
        )}
        {currentDraft.error && (
          <QueryState
            loading={false}
            error={currentDraft.error}
            retry={() => currentDraft.mutate(draftId)}
          />
        )}
      </section>
      {selected && (
        <section className="section">
          <h3>作用域激活与回滚</h3>
          <Alert
            type="warning"
            showIcon
            title="影响所选作用域内的策略解析"
            description="激活既有签名版本也用于回滚。首次激活修订号为 0；已有作用域使用后端返回的当前修订号。未核对引用前不会自动覆盖。"
          />
          <div className="action-grid">
            <Select
              aria-label="激活作用域类型"
              value={scopeType}
              onChange={setScopeType}
              options={["tenant", "cluster", "namespace"].map((v) => ({
                value: v,
                label: (
                  {
                    tenant: "当前租户",
                    cluster: "指定集群",
                    namespace: "指定命名空间",
                  } as Record<string, string>
                )[v],
              }))}
            />
            <Input
              aria-label="激活 clusterId"
              disabled={scopeType === "tenant"}
              placeholder="集群注册编号（非 Kubernetes UID）"
              value={clusterId}
              onChange={(e) => setClusterId(e.target.value)}
            />
            <Input
              aria-label="激活 namespace"
              disabled={scopeType !== "namespace"}
              placeholder="命名空间名称"
              value={namespace}
              onChange={(e) => setNamespace(e.target.value)}
            />
            <p>
              绑定修订号取自最新影响预览：
              {impactReady ? impactReceipt?.revision : "尚未核对"}
            </p>
            <Input
              aria-label="回滚目标 versionId"
              placeholder="留空激活选中版本；填写既有签名版本用于回滚"
              value={targetVersion}
              onChange={(e) => setTargetVersion(e.target.value)}
            />
          </div>
          <PolicyImpactPreview
            stamp={activationStamp}
            logicalName={text(selected.logicalName)}
            versionId={targetVersion || text(selected.versionId)}
            scope={{
              type: scopeType,
              clusterId: scopeType === "tenant" ? undefined : clusterId,
              namespace: scopeType === "namespace" ? namespace : undefined,
            }}
            onReviewed={setImpactReceipt}
          />
          <Space className="status-note">
            <Button
              disabled={
                !canWrite("activate") ||
                !impactReady ||
                !selected.versionId ||
                (scopeType !== "tenant" && !clusterId) ||
                (scopeType === "namespace" && !namespace)
              }
              loading={activated.isPending}
              onClick={() => activated.mutate()}
            >
              激活 / 回滚既有签名版本
            </Button>
            <Button
              danger
              disabled={
                !canWrite("retire") ||
                !selected.digest ||
                selected.retiredAt != null ||
                (typeof selected.referenceCount === "number" &&
                  selected.referenceCount > 0)
              }
              loading={retired.isPending}
              onClick={() => retired.mutate()}
            >
              按已读取内容摘要退役版本
            </Button>
          </Space>
        </section>
      )}
      {result && <Details value={result} />}{" "}
      {[created.error, updated.error, activated.error, retired.error]
        .filter(Boolean)
        .map((e, i) => (
          <QueryState key={i} loading={false} error={e} retry={onChanged} />
        ))}
    </>
  );
}

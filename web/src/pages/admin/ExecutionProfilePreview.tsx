import { useEffect, useMemo, useState } from "react";
import { Alert, Checkbox } from "antd";
import type { PublishExecutionProfileRequest } from "../../api/generated/models";
import { Details } from "../../components/Details";
import { record } from "../../components/data";
export function parseSignedProfile(
  content: string,
): PublishExecutionProfileRequest {
  const body = record(JSON.parse(content));
  const p = record(body.profile);
  const strings = [
    "executionProfileId",
    "clusterUid",
    "namespace",
    "name",
    "networkPolicyRef",
    "credentialRef",
    "principal",
    "hostOnboardingRef",
  ];
  if (
    p.schemaVersion !== "execution-profile/v2" ||
    p.developmentFallback !== false ||
    !strings.every(
      (k) => typeof p[k] === "string" && String(p[k]).length <= 512,
    ) ||
    !["k8s_namespace", "k8s_cluster", "ssh_user", "ssh_root"].includes(
      String(p.type),
    )
  )
    throw new Error(
      "配置结构不符合 execution-profile/v2；请核对类型、引用和开发回退字段。",
    );
  if (
    !Number.isInteger(p.version) ||
    Number(p.version) < 1 ||
    !Number.isInteger(p.timeoutSeconds) ||
    Number(p.timeoutSeconds) < 1 ||
    Number(p.timeoutSeconds) > 900 ||
    !Number.isInteger(p.maxOutputBytes) ||
    Number(p.maxOutputBytes) < 1 ||
    Number(p.maxOutputBytes) > 10485760
  )
    throw new Error("版本、超时或输出预算超出 Contract 范围。");
  if (
    !Array.isArray(p.allowedTargets) ||
    p.allowedTargets.length < 1 ||
    p.allowedTargets.length > 100 ||
    !p.allowedTargets.every((v) => typeof v === "string" && v.length <= 2048) ||
    !Array.isArray(p.tools) ||
    p.tools.length < 1 ||
    p.tools.length > 40 ||
    !p.tools.every((v) => typeof v === "string")
  )
    throw new Error("允许目标或工具列表不符合 Contract 范围。");
  if (
    typeof p.toolImageDigest !== "string" ||
    !/^.+@sha256:[0-9a-f]{64}$/.test(p.toolImageDigest) ||
    typeof body.signerKeyId !== "string" ||
    !body.signerKeyId ||
    typeof body.signature !== "string" ||
    !body.signature
  )
    throw new Error("需要锁定工具镜像摘要、外部签名密钥编号和公开签名。");
  return body as unknown as PublishExecutionProfileRequest;
}
export function ExecutionProfilePreview({
  content,
  onReviewed,
}: {
  content: string;
  onReviewed: (stamp?: string) => void;
}) {
  const [accepted, setAccepted] = useState(false);
  const result = useMemo(() => {
    try {
      return { body: parseSignedProfile(content) };
    } catch (e) {
      return { error: e instanceof Error ? e.message : "配置无法解析" };
    }
  }, [content]);
  useEffect(() => {
    setAccepted(false);
    onReviewed(undefined);
  }, [content, onReviewed]);
  const p = result.body?.profile;
  return (
    <section aria-label="执行配置发布影响预览">
      <h3>发布前核对执行边界</h3>
      {content && result.error && (
        <Alert showIcon type="warning" title={result.error} />
      )}
      {p && (
        <>
          <Details
            value={{
              executionProfileId: p.executionProfileId,
              version: p.version,
              type: p.type,
              clusterUid: p.clusterUid,
              namespace: p.namespace,
              allowedTargets: p.allowedTargets,
              tools: p.tools,
              principal: p.principal,
              credentialRef: p.credentialRef,
              networkPolicyRef: p.networkPolicyRef,
              hostOnboardingRef: p.hostOnboardingRef,
              toolImageDigest: p.toolImageDigest,
              timeoutSeconds: p.timeoutSeconds,
              maxOutputBytes: p.maxOutputBytes,
              signerKeyId: result.body?.signerKeyId,
            }}
          />
          <p className="status-note">
            此操作新增不可变签名配置版本。既有执行仍引用原版本；发布不会启动命令或授予执行权限。凭据仅展示引用，服务端负责验证签名、目标范围及权限。
          </p>
          <Checkbox
            checked={accepted}
            onChange={(e) => {
              setAccepted(e.target.checked);
              onReviewed(e.target.checked ? content : undefined);
            }}
          >
            已核对执行身份、允许目标、网络策略与预算
          </Checkbox>
        </>
      )}
    </section>
  );
}

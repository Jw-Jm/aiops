import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Checkbox, Space, Table } from "antd";
import {
  getPolicyBundles,
  getRegistryDraft,
} from "../../api/generated/platform";
import { checked, requestOptions } from "../../api/client";
import { record, list, text } from "../../components/data";
import { Details, FullText } from "../../components/Details";
import { QueryState } from "../../components/QueryState";
export interface ImpactReceipt {
  stamp: string;
  expiresAt: number;
  revision?: number;
}
export function PolicyImpactPreview({
  stamp,
  logicalName,
  versionId,
  scope,
  draftId,
  expectedRevision,
  onReviewed,
}: {
  stamp: string;
  logicalName?: string;
  versionId?: string;
  scope?: { type: string; clusterId?: string; namespace?: string };
  draftId?: string;
  expectedRevision?: number;
  onReviewed: (receipt?: ImpactReceipt) => void;
}) {
  const [requested, setRequested] = useState("");
  const [accepted, setAccepted] = useState(false);
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  const q = useQuery({
    queryKey: ["policy-impact", stamp],
    enabled: requested === stamp,
    queryFn: async ({ signal }) => {
      const draft = draftId
        ? (await checked(getRegistryDraft(draftId, requestOptions(signal))))
            .data
        : undefined;
      const name = draft?.logicalName ?? logicalName;
      if (!name) throw new Error("先选择策略版本或读取草稿。");
      const versions: Record<string, unknown>[] = [];
      let cursor: string | undefined;
      const seen = new Set<string>();
      for (let page = 0; page < 10; page++) {
        const result = await checked(
          getPolicyBundles(
            { logicalName: name, limit: 50, cursor },
            requestOptions(signal),
          ),
        );
        for (const entry of list(result.data)) {
          const v = record(entry);
          const id = text(v.versionId);
          if (seen.has(id))
            throw new Error("预览期间版本列表发生变化，请重新读取。");
          seen.add(id);
          versions.push(v);
        }
        const next = record(result.meta).nextCursor;
        if (typeof next !== "string" || !next)
          return { draft, versions, complete: true, readAt: Date.now() };
        if (next === cursor)
          throw new Error("分页游标重复，不能认定影响范围已读取完整。");
        cursor = next;
      }
      return { draft, versions, complete: false, readAt: Date.now() };
    },
    staleTime: 0,
    refetchOnWindowFocus: false,
  });
  const value = requested === stamp ? q.data : undefined;
  const target = value?.versions.find((v) => v.versionId === versionId);
  const activations: Record<string, unknown>[] = (
    value?.versions ?? []
  ).flatMap((v) =>
    list(v.activations).map((a) => ({
      ...record(a),
      versionId: v.versionId,
      versionNumber: v.versionNumber,
      digest: v.digest,
    })),
  );
  const exact = scope
    ? activations.filter((a) => {
        const s = record(a.scope);
        return (
          s.type === scope.type &&
          (scope.type === "tenant" || s.clusterId === scope.clusterId) &&
          (scope.type !== "namespace" || s.namespace === scope.namespace)
        );
      })
    : [];
  const matchesDraft =
    !draftId ||
    (value?.draft?.revision === expectedRevision &&
      value?.draft?.status === "draft" &&
      value?.draft?.kind === "policy");
  const knownTarget =
    !versionId ||
    (!!target &&
      target.signatureVerified === true &&
      target.digestVerified === true &&
      !target.retiredAt);
  const valid =
    !!value?.complete &&
    matchesDraft &&
    knownTarget &&
    exact.length <= 1 &&
    now - value.readAt < 30000 &&
    !q.isFetching &&
    !q.error;
  useEffect(() => {
    setAccepted(false);
  }, [stamp, q.dataUpdatedAt]);
  useEffect(() => {
    onReviewed(
      accepted && valid && value
        ? {
            stamp,
            expiresAt: value.readAt + 30000,
            revision: scope ? Number(exact[0]?.revision ?? 0) : undefined,
          }
        : undefined,
    );
  }, [stamp, accepted, valid, q.dataUpdatedAt]);
  return (
    <section className="section" aria-label="配置变更影响预览">
      <h3>变更前核对影响与引用</h3>
      <Button
        loading={q.isFetching}
        onClick={() => {
          setAccepted(false);
          onReviewed(undefined);
          if (requested === stamp) void q.refetch();
          else setRequested(stamp);
        }}
      >
        读取最新影响预览
      </Button>
      {requested === stamp && (
        <QueryState
          loading={q.isPending}
          error={q.error}
          retry={() => void q.refetch()}
        >
          {value && (
            <>
              {!value.complete && (
                <Alert
                  showIcon
                  type="warning"
                  title="已达到 500 版本读取预算，未认定引用完整"
                  description="本次预览不能用于确认变更；需进一步核对完整注册表；保留历史版本与引用，本次不提交变更。"
                />
              )}
              {value.draft && (
                <Details
                  value={{
                    logicalName: value.draft.logicalName,
                    revision: value.draft.revision,
                    status: value.draft.status,
                    content: value.draft.content,
                  }}
                />
              )}
              {scope ? (
                <>
                  <Details
                    value={{
                      scope,
                      currentVersion: exact[0]?.versionId ?? null,
                      currentRevision: exact[0]?.revision ?? 0,
                      targetVersion: versionId,
                      targetDigest: target?.digest ?? null,
                    }}
                  />
                  <p>
                    此变更替换上述精确作用域的激活绑定。更具体作用域可能覆盖它；既有调查、RCA、审计的历史版本引用保持不变。
                  </p>
                </>
              ) : (
                <p>
                  发布仅新增不可变签名版本，不自动激活。下表为同名策略已有激活引用；发布本身不替换它们。
                </p>
              )}
              {!matchesDraft && (
                <Alert
                  showIcon
                  type="warning"
                  title="草稿状态或修订号已变化"
                  description="重新读取草稿，并基于最新内容重新生成外部签名。"
                />
              )}
              {!knownTarget && (
                <Alert
                  showIcon
                  type="warning"
                  title="目标版本未找到、已退役或摘要/签名未验证"
                />
              )}
              {exact.length > 1 && (
                <Alert
                  showIcon
                  type="error"
                  title="作用域返回冲突绑定，不能确认本次激活"
                />
              )}
              <Table
                pagination={{ pageSize: 10, showSizeChanger: false }}
                rowKey={(v) => text(v.versionId)}
                dataSource={value.versions}
                scroll={{ x: 650 }}
                expandable={{ expandedRowRender: (v) => <Details value={v} /> }}
                columns={[
                  {
                    title: "已发布版本",
                    render: (_, v) => <FullText value={v.versionId} />,
                  },
                  { title: "版本号", dataIndex: "versionNumber" },
                  {
                    title: "当前激活引用数",
                    render: (_, v) =>
                      typeof v.referenceCount === "number"
                        ? String(v.referenceCount)
                        : "未记录",
                  },
                  {
                    title: "签名",
                    render: (_, v) =>
                      v.signatureVerified === true ? "已验证" : "未验证",
                  },
                ]}
              />
              <h4>当前作用域引用关系</h4>
              <Table
                pagination={{ pageSize: 10, showSizeChanger: false }}
                rowKey={(a) => text(a.activationId)}
                dataSource={activations}
                scroll={{ x: 650 }}
                columns={[
                  {
                    title: "作用域",
                    render: (_, a) => <Details value={a.scope} />,
                  },
                  { title: "绑定修订号", dataIndex: "revision" },
                  {
                    title: "引用版本",
                    render: (_, a) => <FullText value={a.versionId} />,
                  },
                ]}
              />
              <p className="status-note">
                引用数来自当前注册表激活记录，不代表资源数量或历史运行引用数量。预览
                30 秒有效；变更字段后需重新读取。服务端仍以 expectedRevision
                和权限校验裁决。
              </p>
              <Checkbox
                checked={accepted && valid}
                disabled={!valid}
                onChange={(e) => setAccepted(e.target.checked)}
              >
                已核对版本、作用域及引用影响
              </Checkbox>
            </>
          )}
        </QueryState>
      )}
    </section>
  );
}

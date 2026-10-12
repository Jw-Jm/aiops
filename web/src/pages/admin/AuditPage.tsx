import { stateLabel } from "../../components/presentation";
import { useAuth } from "../../auth/AuthProvider";
import { useState } from "react";
import { Status } from "../../components/Status";
import { verifyAuditPage, type AuditProofResult } from "./auditProof";
import { useQuery } from "@tanstack/react-query";
import { Table, Input, Button, Space, Alert, Drawer } from "antd";
import { useSearchParams } from "react-router-dom";
import { getAuditRecords, getAuditRecord } from "../../api/generated/platform";
import { checked, requestOptions } from "../../api/client";
import { QueryState } from "../../components/QueryState";
import { PageHeading } from "../../components/PageHeading";
import { Details, FullText } from "../../components/Details";
import {
  record,
  list,
  text,
  publicValue,
  displayTime,
} from "../../components/data";
const eventLabels: Record<string, string> = {
  "action_plan.suggested": "生成处置建议",
  "command.risk_assessed": "评估实际命令风险",
  "command.risk_acknowledged": "本人确认风险",
  "command.prepared": "提交实际命令",
  "command.claimed": "执行器接收命令",
  "command.dispatching": "开始发送命令",
  "command.completed": "命令执行结束",
  "command.output_archived": "归档执行输出",
  "command.post_checked": "核对故障是否恢复",
  "command.execution_unknown": "执行结果不确定",
  "command.cancel_requested": "请求取消执行",
  "command.runner_termination_requested": "请求终止执行器",
  "command.dispatch_denied": "拒绝发送命令",
  "investigation.cancel": "取消智能调查",
};
const filterLabels: Record<string, string> = {
  subject: "操作人标识",
  operation: "事件类型",
  resource: "资源完整标识",
  from: "开始时间",
  to: "结束时间",
};
const entityLabels: Record<string, string> = {
  command: "命令处置",
  investigation: "智能调查",
  command_execution: "命令执行",
  command_risk_assessment: "风险评估",
  command_risk_acknowledgement: "风险确认",
  action_plan: "处置建议",
  investigation_job: "智能调查",
  incident: "故障",
  execution_profile: "执行配置",
};
export function AuditPage({
  incidentId,
  embedded = false,
}: {
  incidentId?: string;
  embedded?: boolean;
}) {
  const auth = useAuth();
  const [url, setUrl] = useSearchParams();
  const [proof, setProof] = useState<AuditProofResult>();
  const [proofError, setProofError] = useState<string>();
  const [verifiedPage, setVerifiedPage] = useState<unknown>();
  const verify = async () => {
    try {
      setProof(await verifyAuditPage(q.data));
      setVerifiedPage(q.data);
      setProofError(undefined);
    } catch {
      setProofError("浏览器摘要校验不可用；不能认定校验通过。");
    }
  };
  const params = {
    limit: 50,
    verifyChain: url.get("verifyChain") === "true" ? true : undefined,
    ...Object.fromEntries(
      ["subject", "operation", "incidentId", "resource", "from", "to"]
        .filter((k) => url.has(k))
        .map((k) => [k, url.get(k)!]),
    ),
    ...(incidentId ? { incidentId } : {}),
    cursor: url.get("auditCursor") || undefined,
  };
  const q = useQuery({
    queryKey: ["audit", params],
    queryFn: ({ signal }) =>
      checked(getAuditRecords(params, requestOptions(signal))),
  });
  const auditId = url.get("auditId");
  const detail = useQuery({
    queryKey: ["audit-detail", auditId],
    enabled: !!auditId,
    queryFn: ({ signal }) =>
      checked(getAuditRecord(auditId!, requestOptions(signal))),
  });
  const rows = list(record(q.data).data).map(record);
  const update = (k: string, v: string) => {
    const n = new URLSearchParams(url);
    if (k !== "auditId") {
      n.delete("auditCursor");
      n.delete("verifyChain");
    }
    if (v) n.set(k, v);
    else n.delete(k);
    setUrl(n);
  };
  const download = () => {
    const blob = new Blob([JSON.stringify(publicValue(q.data), null, 2)], {
      type: "application/json",
    });
    const address = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = address;
    a.download = "authorized-audit-page.json";
    a.click();
    setTimeout(() => URL.revokeObjectURL(address), 1000);
  };
  return (
    <>
      {!embedded && (
        <PageHeading
          title="审计记录"
          description="建议、实际命令、风险确认、执行和后检查保留独立事件与关联标识。"
        />
      )}
      <Alert
        showIcon
        type="info"
        title="按操作过程追查记录"
        description="建议、实际输入的命令、本人风险确认、执行结果和故障复验分别记录。命令退出码为 0，只表示命令执行成功；是否修复故障需查看复验结果。"
      />
      <section className="section">
        <div className="filters">
          {["subject", "operation", "resource", "from", "to"].map((k) => (
            <div className="filter" key={k}>
              <label htmlFor={"audit-" + k}>{filterLabels[k]}</label>
              <Input
                id={"audit-" + k}
                placeholder={
                  ["from", "to"].includes(k)
                    ? "例如 2026-10-12T08:00:00+08:00"
                    : k === "operation"
                      ? "例如 command.completed"
                      : undefined
                }
                defaultValue={url.get(k) || ""}
                key={k + location.search}
                onPressEnter={(e) => update(k, e.currentTarget.value)}
                onBlur={(e) => {
                  if (e.target.value !== (url.get(k) || ""))
                    update(k, e.target.value);
                }}
              />
            </div>
          ))}
        </div>
        <QueryState
          loading={q.isPending}
          error={q.error}
          retry={() => void q.refetch()}
          empty={rows.length === 0}
        >
          <Space wrap className="status-note">
            <span>签名归档链</span>
            <Status
              value={
                record(record(q.data).meta).chainVerification ?? "unverified"
              }
            />
            <details>
              <summary>查看归档链校验说明</summary>
              <p>
                记录摘要与签名归档链分别校验。尚未校验时不能据此认定归档完整有效。
              </p>
              <FullText value={record(record(q.data).meta).chainReason} />
            </details>
          </Space>
          <Table
            rowKey={(r) => text(r.auditId ?? r.eventId)}
            dataSource={rows}
            pagination={false}
            scroll={{ x: 850 }}
            expandable={{ expandedRowRender: (r) => <Details value={r} /> }}
            columns={[
              {
                title: "操作",
                render: (_, r) => (
                  <Button
                    type="link"
                    onClick={() =>
                      update("auditId", text(r.auditId ?? r.eventId))
                    }
                  >
                    {eventLabels[text(r.operation ?? r.eventType)] ??
                      text(r.operation ?? r.eventType)}
                  </Button>
                ),
              },
              {
                title: "主体",
                dataIndex: "subject",
                render: (v) => (
                  <>
                    <span>
                      {v === auth.session?.subject
                        ? "当前登录用户"
                        : "操作人标识"}
                    </span>
                    <details>
                      <summary>完整标识</summary>
                      <FullText value={v} />
                    </details>
                  </>
                ),
              },
              {
                title: "时间",
                render: (_, r) => displayTime(r.occurredAt ?? r.createdAt),
              },
              {
                title: "关联对象",
                render: (_, r) => (
                  <>
                    <span>
                      {entityLabels[text(r.entityKind)] ?? text(r.entityKind)}
                    </span>
                    <details>
                      <summary>完整标识</summary>
                      <FullText value={r.entityId} />
                    </details>
                  </>
                ),
              },
              {
                title: "记录摘要",
                render: (_, r) => (
                  <Status
                    value={
                      r.digestVerified === true
                        ? "verified"
                        : r.digestVerified === false
                          ? "failed"
                          : "unverified"
                    }
                  />
                ),
              },
            ]}
          />
          <Space wrap className="status-note">
            <Button
              onClick={() => {
                const n = new URLSearchParams(url);
                n.set("verifyChain", "true");
                setUrl(n);
              }}
            >
              验证当前页签名归档链
            </Button>
            <Button onClick={() => void verify()}>校验当前页摘要</Button>
            <Button onClick={download}>导出当前授权页及校验证据</Button>
            <Button
              disabled={!url.has("auditCursor")}
              onClick={() => update("auditCursor", "")}
            >
              回到第一页
            </Button>
            <Button
              disabled={!record(record(q.data).meta).nextCursor}
              onClick={() => {
                const n = new URLSearchParams(url);
                n.set(
                  "auditCursor",
                  text(record(record(q.data).meta).nextCursor),
                );
                setUrl(n);
              }}
            >
              下一页
            </Button>
          </Space>
          {proof && verifiedPage === q.data && (
            <Alert
              showIcon
              type={
                proof.status === "failed"
                  ? "error"
                  : proof.status === "verified"
                    ? "success"
                    : "warning"
              }
              title={"记录摘要：" + stateLabel(proof.status)}
              description={`浏览器独立计算 SHA-256：${proof.verified} 条有效，${proof.failed} 条失败，${proof.unverified} 条未验证。签名归档链：${stateLabel(proof.chain)}。单条摘要不代表完整归档链有效。`}
            />
          )}
          {proofError && <Alert type="error" showIcon title={proofError} />}
        </QueryState>
        {auditId && (
          <Drawer
            title="审计事件详情"
            open={!!auditId}
            size={760}
            onClose={() => update("auditId", "")}
          >
            <QueryState
              loading={detail.isPending}
              error={detail.error}
              retry={() => void detail.refetch()}
            >
              <h3>
                {eventLabels[
                  text(record(record(detail.data).data).eventType)
                ] ?? text(record(record(detail.data).data).eventType)}
              </h3>
              <p>{displayTime(record(record(detail.data).data).createdAt)}</p>
              <Details value={record(record(detail.data).data).record} />
              <details className="technical-details">
                <summary>完整审计记录与校验标识</summary>
                <Details value={record(detail.data).data} />
              </details>
            </QueryState>
          </Drawer>
        )}
      </section>
    </>
  );
}

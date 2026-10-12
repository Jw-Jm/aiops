import { stateLabel } from "../../components/presentation";
import { ObservationFacts } from "../../components/ObservationFacts";
import { useQuery } from "@tanstack/react-query";
import { Table, Button, Drawer } from "antd";
import { getIncidentEvidence, getEvidence } from "../../api/generated/platform";
import type { IncidentEvidencePageV2 } from "../../api/generated/models";
import { checked, requestOptions } from "../../api/client";
import { QueryState } from "../../components/QueryState";
import { Details, FullText } from "../../components/Details";
import { record, list, text, displayTime } from "../../components/data";
import { Status } from "../../components/Status";
import { useSearchParams } from "react-router-dom";
export function EvidencePanel({ incidentId }: { incidentId: string }) {
  const [url, setUrl] = useSearchParams();
  const id = url.get("evidenceId");
  const cursor = url.get("evidenceCursor") || undefined;
  const q = useQuery({
    queryKey: ["incident-evidence", incidentId, cursor],
    queryFn: ({ signal }) =>
      checked(
        getIncidentEvidence(
          incidentId,
          { limit: 50, cursor },
          requestOptions(signal),
        ),
      ),
  });
  const detail = useQuery({
    queryKey: ["evidence", id],
    enabled: !!id,
    queryFn: ({ signal }) => checked(getEvidence(id!, requestOptions(signal))),
  });
  const page = q.data as IncidentEvidencePageV2 | undefined;
  return (
    <>
      <p className="status-note">
        点击证据内容查看实际观测。保留期限表示原始来源的可回放期限，不代表当前观测仍然有效；请结合观测时间和故障版本核对。
      </p>
      <QueryState
        loading={q.isPending}
        error={q.error}
        empty={page?.data.length === 0}
        retry={() => void q.refetch()}
      >
        <Table
          rowKey={(r) => text(record(r).evidenceId)}
          dataSource={page?.data}
          pagination={false}
          scroll={{ x: 800 }}
          expandable={{ expandedRowRender: (r) => <Details value={r} /> }}
          columns={[
            {
              title: "证据内容",
              render: (_, r) => (
                <Button
                  type="link"
                  onClick={() => {
                    const next = new URLSearchParams(url);
                    next.set("evidenceId", text(record(r).evidenceId));
                    setUrl(next);
                  }}
                >
                  <span>
                    {stateLabel(text(record(r).type))} ·{" "}
                    {displayTime(record(r).observedTo)}
                  </span>
                </Button>
              ),
            },
            {
              title: "来源",
              render: (_, r) => <FullText value={record(r).sourceSystem} />,
            },
            {
              title: "回放状态",
              render: (_, r) => <Status value={record(r).replayState} />,
            },
            {
              title: "来源保留期限",
              render: (_, r) => (
                <>
                  <span>
                    {typeof record(r).sourceRetentionUntil === "string" &&
                    Number.isFinite(
                      Date.parse(String(record(r).sourceRetentionUntil)),
                    )
                      ? Date.parse(String(record(r).sourceRetentionUntil)) <
                        Date.now()
                        ? "保留期限已结束 · "
                        : "保留至 "
                      : "保留期限未提供 · "}
                  </span>
                  <span>{displayTime(record(r).sourceRetentionUntil)}</span>
                </>
              ),
            },
          ]}
        />
        <Button
          disabled={!cursor}
          onClick={() => {
            const next = new URLSearchParams(url);
            next.delete("evidenceCursor");
            setUrl(next);
          }}
        >
          回到第一页
        </Button>
        <Button
          disabled={!page?.meta.nextCursor}
          onClick={() => {
            const next = new URLSearchParams(url);
            next.set("evidenceCursor", String(page?.meta.nextCursor));
            setUrl(next);
          }}
        >
          下一页
        </Button>
      </QueryState>
      {id && (
        <Drawer
          title="证据详情"
          open={!!id}
          size={760}
          onClose={() => {
            const next = new URLSearchParams(url);
            next.delete("evidenceId");
            setUrl(next);
          }}
        >
          <div id="evidence-detail">
            <QueryState
              loading={detail.isPending}
              error={detail.error}
              retry={() => void detail.refetch()}
            >
              <Details
                value={{
                  type: record(record(detail.data).data).type,
                  sourceSystem: record(record(detail.data).data).sourceSystem,
                  observedFrom: record(record(detail.data).data).observedFrom,
                  observedTo: record(record(detail.data).data).observedTo,
                  timeReliable: record(record(detail.data).data).timeReliable,
                  replayState: record(record(detail.data).data).replayState,
                }}
              />
              <h4>实际观测内容</h4>
              <ObservationFacts
                value={record(record(detail.data).data).factSlice}
              />
              <details className="technical-details">
                <summary>原始证据与完整编号</summary>
                <Details value={record(detail.data).data} />
              </details>
            </QueryState>
          </div>
        </Drawer>
      )}
    </>
  );
}

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Drawer, Descriptions, Space, Typography } from "antd";
import { useAuth } from "../../auth/AuthProvider";
import { checked, requestOptions } from "../../api/client";
import { getEvidence } from "../../api/generated/platform";
import { QueryState } from "../../components/QueryState";
import { ObservationFacts } from "../../components/ObservationFacts";

export function AssistantEvidence({ refs }: { refs: string[] }) {
  const [id, setId] = useState<string>();
  const { session } = useAuth();
  const scopeKey = JSON.stringify([session?.tenantId, session?.subject, session?.roles, session?.clusterScopes, session?.namespaceScopes]);
  const detail = useQuery({
    queryKey: ["assistant", scopeKey, "evidence", id],
    enabled: !!id && !!session,
    queryFn: ({ signal }) => checked(getEvidence(id!, requestOptions(signal))),
  });
  const evidence = detail.data?.data;
  return <>
    <Space wrap>{refs.map(ref => <Button key={ref} size="small" onClick={() => setId(ref)}>查看证据 {ref}</Button>)}</Space>
    <Drawer title="已引用证据" open={!!id} onClose={() => setId(undefined)} width="min(760px, 100vw)">
      <QueryState loading={detail.isPending} error={detail.error} retry={() => void detail.refetch()}>
        {evidence && <>
          <Descriptions column={1} size="small" items={[
            { key: "source", label: "来源", children: evidence.sourceSystem },
            { key: "time", label: "观测范围", children: `${evidence.observedFrom} ～ ${evidence.observedTo}` },
            { key: "reliable", label: "时间可靠", children: evidence.timeReliable ? "是" : "否" },
            { key: "replay", label: "回放状态", children: evidence.replayState },
          ]} />
          <Typography.Title level={5}>实际观测内容</Typography.Title>
          <ObservationFacts value={evidence.factSlice} />
        </>}
      </QueryState>
    </Drawer>
  </>;
}

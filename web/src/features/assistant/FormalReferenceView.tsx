import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Drawer, Typography } from "antd";
import { Link } from "react-router-dom";
import { getAssistantFormalReference } from "../../api/generated/platform";
import type { AssistantAnswerV1FormalRefsItem } from "../../api/generated/models";
import { checked, requestOptions } from "../../api/client";
import { useAuth } from "../../auth/AuthProvider";
import { QueryState } from "../../components/QueryState";
import { Details } from "../../components/Details";
export function FormalReferenceView({
  refValue,
  index,
  turnId,
}: {
  refValue: AssistantAnswerV1FormalRefsItem;
  index: number;
  turnId: string;
}) {
  const [open, setOpen] = useState(false);
  const { session } = useAuth();
  const query = useQuery({
    queryKey: [
      "assistant-formal",
      session?.tenantId,
      session?.subject,
      turnId,
      index,
    ],
    enabled: open,
    queryFn: ({ signal }) =>
      checked(
        getAssistantFormalReference(turnId, { index }, requestOptions(signal)),
      ),
  });
  return (
    <>
      <Button onClick={() => setOpen(true)}>
        查看 {refValue.kind === "rca" ? "RCA" : "ActionPlan"} 版本{" "}
        {refValue.revision}
      </Button>
      <Drawer
        title="已引用的正式版本"
        open={open}
        onClose={() => setOpen(false)}
      >
        <Alert
          type="info"
          title="读取引用的具体版本，重新检查当前授权。版本变化或来源失效时不会替换为最新内容。"
        />
        <Typography.Paragraph>
          内容摘要：{refValue.contentDigest}
        </Typography.Paragraph>
        <QueryState
          loading={query.isPending}
          error={query.error}
          retry={() => void query.refetch()}
        >
          {query.data && <Details value={query.data.data.metadata} />}
        </QueryState>
        <Link
          to={`/incidents/${encodeURIComponent(refValue.incidentId)}?tab=rca`}
        >
          打开 Incident 当前工作区
        </Link>
      </Drawer>
    </>
  );
}

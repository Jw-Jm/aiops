import { useQuery } from "@tanstack/react-query";
import { getCommandPostCheck } from "../../api/generated/platform";
import { checked, requestOptions } from "../../api/client";
import { QueryState } from "../../components/QueryState";
import { Details } from "../../components/Details";
import { Status } from "../../components/Status";
import { record } from "../../components/data";
export function PostCheckPanel({ executionId }: { executionId: string }) {
  const q = useQuery({
    queryKey: ["postcheck", executionId],
    queryFn: ({ signal }) =>
      checked(getCommandPostCheck(executionId, requestOptions(signal))),
    refetchInterval: (q) =>
      q.state.error
        ? false
        : !["completed", "not_applicable", "execution_unknown"].includes(
              String(record(record(q.state.data).data).status),
            )
          ? 5000
          : false,
  });
  const data = record(record(q.data).data);
  return (
    <section className="section">
      <h3>后检查与后续建议</h3>
      <QueryState
        loading={q.isPending}
        error={q.error}
        retry={() => void q.refetch()}
      >
        <Status
          value={
            data.status === "completed"
              ? data.verdict
              : data.status || "pending"
          }
        />
        <Details value={data} />
        <p className="status-note">
          退出码 0
          仅表示命令完成。后检查未修复时，先核对新证据，再决定是否进入下一轮人工处置。
        </p>
      </QueryState>
    </section>
  );
}

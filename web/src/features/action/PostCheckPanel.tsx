import { Button, Space } from "antd";
import { useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { getCommandPostCheck } from "../../api/generated/platform";
import { checked, requestOptions } from "../../api/client";
import { QueryState } from "../../components/QueryState";
import { Details } from "../../components/Details";
import { Status } from "../../components/Status";
import { record } from "../../components/data";
export function PostCheckPanel({ executionId }: { executionId: string }) {
  const [url, setUrl] = useSearchParams();
  const reviewNext = (tab: string) => {
    const n = new URLSearchParams(url);
    n.set("tab", tab);
    if (tab === "rca") {
      n.delete("executionId");
      n.delete("actionPlanId");
    }
    setUrl(n);
  };
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
        <Space wrap>
          <Button onClick={() => reviewNext("evidence")}>
            核对当前故障证据
          </Button>
          <Button
            disabled={
              data.status !== "completed" || data.verdict === "resolved"
            }
            onClick={() => reviewNext("rca")}
          >
            复核根因，准备下一轮处置
          </Button>
        </Space>
        <p className="status-note">
          返回人工处置时重新输入实际命令、重新评估与本人确认。不会复制建议命令或自动执行；结果不确定时先核对目标事实。
        </p>
      </QueryState>
    </section>
  );
}

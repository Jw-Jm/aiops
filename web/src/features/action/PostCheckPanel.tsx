import { Alert, Button, Space } from "antd";
import { Link, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { getCommandPostCheck } from "../../api/generated/platform";
import { checked, requestOptions } from "../../api/client";
import { QueryState } from "../../components/QueryState";
import { Details } from "../../components/Details";
import { Status } from "../../components/Status";
import { record, list, text, displayTime } from "../../components/data";
const recordedTime = (value: unknown) =>
  !value || (typeof value === "string" && value.startsWith("0001-"))
    ? "未取得"
    : displayTime(value);
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
  const startRelated = (relation: "follow_up" | "rollback", probe?: string) => {
    const n = new URLSearchParams(url);
    n.set("tab", "action");
    n.set("relatedExecutionId", executionId);
    n.set("executionRelation", relation);
    if (probe) n.set("verificationMode", probe);
    else n.delete("verificationMode");
    n.delete("executionId");
    n.delete("actionPlanId");
    setUrl(n);
  };
  const executionLink = (id: string) => {
    const n = new URLSearchParams(url);
    n.set("tab", "action");
    n.set("executionId", id);
    n.delete("relatedExecutionId");
    n.delete("executionRelation");
    n.delete("actionPlanId");
    return "?" + n.toString();
  };
  const evidenceLink = (id: string) => {
    const n = new URLSearchParams(url);
    n.set("tab", "evidence");
    n.set("evidenceId", id);
    return "?" + n.toString();
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
  const report = record(data.report),
    facts = record(report.facts);
  const reasons: Record<string, string> = {
    verification_missing: "缺少已归档的实际探测回执",
    verification_unavailable: "实际探测未取得可靠结果",
    verification_failed: "实际连接或写读断言未通过",
    verification_passed: "实际探测和原生场景判据均通过",
    completion_time_missing: "缺少可信的执行完成时间",
    authorization_unavailable: "验证时授权不可用",
    source_unavailable: "未取得合格来源事实",
    facts_incomplete: "事实或配方资格不完整",
    evidence_missing: "缺少验证证据",
    observation_not_after_execution: "观测时间未晚于本次执行",
    check_precedes_observation: "验证与观测时间不一致",
    recipe_fault_present: "配方所检查的原异常仍存在",
    recipe_fault_absent: "配方所检查的原异常已消失",
    new_anomalies_observed: "执行后观测到新的 Warning 异常，不能判为恢复",
    rollback_goal_missing: "此历史回退没有明确的配置目标，无法验证效果",
    rollback_observation_missing: "缺少用于比较回退目标的配置观测",
    rollback_goal_not_met: "健康判据已通过，但配置尚未达到声明的回退目标",
    rollback_goal_met: "声明的回退配置匹配且场景健康判据通过",
    target_unsupported: "当前目标没有可信后检查适配器",
  };
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
        {record(data.applicability).status === "unsupported" && (
          <Alert
            type="info"
            showIcon
            title="此目标没有已发布的可信后检查判据"
            description="即使命令成功，也不能自动判断异常恢复。请核对执行后的原生事实和故障证据。"
          />
        )}
        {record(data.applicability).status === "requires_qualified_source" && (
          <p className="status-note">
            后检查须取得当前原生来源、已激活配方和执行后的合格归档证据；缺一项仍为尚无定论。
          </p>
        )}
        {data.detailStatus === "legacy_detail_unavailable" && (
          <Alert
            type="info"
            title="此历史记录没有结构化验证详情，不能补造验证依据"
          />
        )}
        {report.schemaVersion === "command-post-check/v1" && (
          <>
            <p>{reasons[text(report.reason)] ?? text(report.reason)}</p>
            <p>
              执行完成：{recordedTime(report.completedAt)}；观测：
              {recordedTime(facts.collectedAfter)}；验证：
              {recordedTime(facts.checkedAt)}
            </p>
            {text(facts.recipeName) && (
              <p>
                验证配方：{text(facts.recipeName)} · {text(facts.recipeVersion)}
              </p>
            )}
            {record(facts.verification).schemaVersion ===
              "command-verification/v1" && (
              <>
                <h4>实际探测回执</h4>
                <p>
                  位置：{text(record(facts.verification).vantage)}；类型：
                  {text(record(facts.verification).kind)}；结论：
                  {text(record(facts.verification).verdict)}；原因：
                  {text(record(facts.verification).reason)}
                </p>
                <p>
                  开始：{recordedTime(record(facts.verification).startedAt)}
                  ；结束：{recordedTime(record(facts.verification).finishedAt)}
                </p>
                <p>
                  这是一条已归档的 Runner
                  执行回执；仅证明该位置、该时刻的连接/响应或专用文件写读，不证明所有客户端、持久耐久性或业务语义。
                </p>
              </>
            )}
            {text(facts.observedSpecDigest) && (
              <p>观测配置摘要：{text(facts.observedSpecDigest)}</p>
            )}
            {text(report.rollbackSpecDigest) && (
              <p>期望回退配置摘要：{text(report.rollbackSpecDigest)}</p>
            )}
            {list(facts.newAnomalies).length > 0 && (
              <p>
                执行后 Warning：
                {list(facts.newAnomalies).map(String).join("、")}
              </p>
            )}
            {report.coverage === "native_related_health" && (
              <p>
                验证当前目标与已授权关联资源的场景健康，并检查执行后
                Warning。Service/EndpointSlice
                原生事实仅证明后端就绪；实际网络结论另看已归档探测回执。PVC
                验证原生挂载/CSI 状态，不证明应用读写成功。
              </p>
            )}
            <Space wrap>
              {list(facts.evidenceRefs).map((id) => (
                <Link key={String(id)} to={evidenceLink(String(id))}>
                  验证证据 {String(id)}
                </Link>
              ))}
            </Space>
            <p className="status-note">
              这是执行后的冻结验证记录，仅覆盖当前配方的目标异常，不代表当前健康状态或已排除全部新增故障。
            </p>
          </>
        )}
        {list(data.relatedExecutions).length > 0 && (
          <div>
            <h4>关联执行</h4>
            <Space wrap>
              {list(data.relatedExecutions)
                .map(record)
                .map((link) => (
                  <Link
                    key={text(link.executionId)}
                    to={executionLink(text(link.executionId))}
                  >
                    {link.direction === "parent" ? "原执行" : "后续执行"} ·{" "}
                    {link.relation === "rollback" ? "回退" : "继续处置"} ·{" "}
                    {text(link.executionId)}（{text(link.state)}）
                  </Link>
                ))}
            </Space>
            {data.linksTruncated === true && <p>仅显示最近 20 条后续执行。</p>}
          </div>
        )}
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
              data.status !== "completed" ||
              data.verdict === "resolved" ||
              !["succeeded", "failed"].includes(text(data.executionState))
            }
            onClick={() => startRelated("follow_up")}
          >
            关联本次执行，准备下一轮处置
          </Button>
          <Button onClick={() => reviewNext("rca")}>复核根因</Button>
          {["Service", "PersistentVolumeClaim"].includes(
            text(record(data.applicability).targetKind),
          ) && (
            <Button
              disabled={
                !["succeeded", "failed"].includes(text(data.executionState))
              }
              onClick={() =>
                startRelated(
                  "follow_up",
                  record(data.applicability).targetKind === "Service"
                    ? "tcp"
                    : "pvc_read_write",
                )
              }
            >
              关联本次执行，准备实际探测
            </Button>
          )}
          <Button
            disabled={
              !["succeeded", "failed"].includes(text(data.executionState))
            }
            onClick={() => startRelated("rollback")}
          >
            关联本次执行，准备人工回退
          </Button>
        </Space>
        <p className="status-note">
          返回人工处置时重新输入实际命令、重新评估与本人确认。不会复制建议命令或自动执行；结果不确定时先核对目标事实。
        </p>
      </QueryState>
    </section>
  );
}

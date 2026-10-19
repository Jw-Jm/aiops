import { Alert, Descriptions } from "antd";
import type { CommandRiskAssessmentRequest } from "../../api/generated/models";
import { Details, FullText } from "../../components/Details";
import { record, text, list, displayTime } from "../../components/data";
import { stateLabel } from "../../components/presentation";
export function RiskPanel({
  assessment,
  request,
  profileName,
  targetName,
}: {
  assessment: unknown;
  request: CommandRiskAssessmentRequest;
  profileName: string;
  targetName?: string;
}) {
  const a = record(assessment),
    risk = record(a.risk),
    binding = record(a.binding),
    options = record(binding.executionOptions);
  return (
    <section className="section">
      <h3>实际命令风险</h3>
      <Alert
        showIcon
        type="warning"
        title={
          "操作风险：" +
          stateLabel(text(risk.operationRisk)) +
          " · 数据风险：" +
          stateLabel(text(risk.dataRisk))
        }
        description="请逐项核对以下命令和执行范围。确认仅对本次请求有效，修改任何内容都需要重新评估。"
      />
      {typeof record(a.postCheckApplicability).status === "string" && (
        <Alert
          showIcon
          type="info"
          title="后检查适用范围"
          description={
            record(a.postCheckApplicability).status === "unsupported"
              ? "此目标没有已发布的可信后检查判据，执行后只能保留“尚无定论”。需要人工核对证据，命令成功不等于异常恢复。"
              : "此目标可使用已声明的原生场景判据；仍需当前来源、已激活配方及执行后的归档证据合格，才可判断恢复。"
          }
        />
      )}
      {text(binding.rollbackSpecDigest) && (
        <p>期望回退配置摘要：{text(binding.rollbackSpecDigest)}</p>
      )}
      {!!binding.verification && (
        <Alert
          type="warning"
          title="本次执行实际探测"
          description={
            <>
              <p>
                探测位置：Runner namespace {text(binding.verificationNamespace)}
                ；类型：{text(record(binding.verification).kind)}
              </p>
              <Details value={binding.verification} />
              <p>
                HTTP 只发送固定 GET，不发送业务凭据；PVC
                会创建、同步、读取并删除一个随机文件。缺权限、超时或清理失败均不能判为通过。
              </p>
            </>
          }
        />
      )}
      <h4>本次将执行的命令</h4>
      <pre className="command-review">{request.actualCommand}</pre>
      <Descriptions
        bordered
        size="small"
        column={1}
        items={[
          ...(binding.relatedExecutionId
            ? [
                {
                  key: "execution-relation",
                  label:
                    binding.executionRelation === "rollback"
                      ? "回退关联的原执行"
                      : "继续处置关联的原执行",
                  children: <FullText value={binding.relatedExecutionId} />,
                },
              ]
            : []),
          {
            key: "target",
            label: "执行目标",
            children: (
              <>
                <strong>{targetName ?? "资源名称尚未确认"}</strong>
                <details className="technical-details">
                  <summary>核对完整目标标识</summary>
                  <FullText value={binding.targetCanonicalId} />
                </details>
              </>
            ),
          },
          {
            key: "namespace",
            label: "命名空间",
            children:
              binding.namespace === null
                ? "集群级目标"
                : text(binding.namespace),
          },
          {
            key: "profile",
            label: "执行配置",
            children:
              profileName + " · 版本 " + text(binding.executionProfileVersion),
          },
          { key: "shell", label: "命令环境", children: text(binding.shell) },
          {
            key: "limits",
            label: "执行限制",
            children:
              "最多 " +
              text(options.timeoutSeconds) +
              " 秒 · 输出最多 " +
              text(options.maxOutputBytes) +
              " 字节",
          },
          {
            key: "directory",
            label: "工作目录",
            children:
              options.workingDirectory === ""
                ? "使用执行配置默认目录"
                : text(options.workingDirectory),
          },
          {
            key: "environment",
            label: "额外环境变量",
            children: <FullText value={options.environment} />,
          },
          {
            key: "step",
            label: "身份验证",
            children:
              risk.requiresStepUp === true
                ? "需要重新输入密码和动态验证码"
                : "当前身份验证满足要求",
          },
          {
            key: "expiry",
            label: "确认有效期",
            children: displayTime(a.expiresAt),
          },
        ]}
      />
      {list(risk.features).length > 0 && (
        <ul>
          {list(risk.features).map((reason, index) => (
            <li key={index}>
              {(
                {
                  parse_unknown: "无法可靠解析命令，需要人工核查",
                  compound_command: "包含多个组合命令",
                  redirect: "包含输出重定向",
                  heredoc: "包含多行输入",
                  subshell: "包含子进程或命令替换",
                  dynamic_expansion: "包含动态展开，实际效果可能变化",
                  control_flow: "包含条件或循环",
                  assignment: "包含变量赋值",
                  dynamic_executable: "实际执行程序由动态内容决定",
                  kubernetes_mutation: "可能修改 Kubernetes 资源",
                  privilege_change: "可能切换或提升权限",
                  network_or_exfiltration: "涉及网络访问或数据传输",
                  effect_unknown: "无法可靠判断命令效果",
                  environment_assignment: "包含环境变量赋值",
                  background: "包含后台执行",
                } as Record<string, string>
              )[text(reason)] ?? text(reason)}
            </li>
          ))}
        </ul>
      )}
      <details className="technical-details">
        <summary>完整风险评估记录</summary>
        <Details value={a} />
      </details>
    </section>
  );
}

import { Descriptions } from "antd";
import { FullText } from "./Details";
import { publicValue } from "./data";
import { fieldLabels } from "./presentation";

const observationLabels: Record<string, Record<string, string>> = {
  phase: {
    Pending: "等待就绪",
    Running: "运行中",
    Succeeded: "已成功完成",
    Failed: "已失败",
    Bound: "已绑定",
    Available: "可用",
    Lost: "已失联",
    Unknown: "未知",
  },
  reason: {
    PVCUnbound: "存储卷尚未绑定",
    FailedMount: "存储卷挂载失败",
    ProvisioningFailed: "存储卷创建失败",
    CrashLoopBackOff: "容器反复重启",
    NotReady: "节点未就绪",
  },
  nativeKind: {
    PersistentVolumeClaim: "持久存储卷申请",
    PersistentVolume: "持久存储卷",
    Pod: "工作负载",
    Node: "节点",
    Service: "服务",
  },
};
function ScalarFact({ field, value }: { field: string; value: unknown }) {
  const label =
    typeof value === "string" ? observationLabels[field]?.[value] : undefined;
  return label ? (
    <div>
      <span>{label}</span>
      <details>
        <summary>查看来源原文</summary>
        <FullText value={value} />
      </details>
    </div>
  ) : (
    <FullText value={value} />
  );
}
function Fact({ value }: { value: unknown }) {
  if (Array.isArray(value))
    return (
      <div className="observation-facts">
        {value.slice(0, 20).map((item, index) => (
          <section className="status-note" key={index}>
            <Fact value={item} />
          </section>
        ))}
        {value.length > 20 && (
          <p>仅展示前 20 项事实，完整内容可在原始观测中查看。</p>
        )}
      </div>
    );
  if (value !== null && typeof value === "object") {
    const entries = Object.entries(value);
    return (
      <>
        <Descriptions
          column={1}
          bordered
          size="small"
          items={entries.slice(0, 40).map(([key, item]) => ({
            key,
            label: fieldLabels[key] ?? key,
            children:
              item !== null && typeof item === "object" ? (
                <details>
                  <summary>
                    {Array.isArray(item)
                      ? "查看 " + item.length + " 项观测"
                      : "查看观测详情"}
                  </summary>
                  <Fact value={item} />
                </details>
              ) : (
                <ScalarFact field={key} value={item} />
              ),
          }))}
        />
        {entries.length > 40 && (
          <p>仅展示前 40 个字段，完整内容可在原始观测中查看。</p>
        )}
      </>
    );
  }
  return <FullText value={value} />;
}
export function ObservationFacts({ value }: { value: unknown }) {
  return <Fact value={publicValue(value)} />;
}

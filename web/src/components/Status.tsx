import { Tag, Tooltip } from "antd";
import {
  CheckCircleOutlined,
  ExclamationCircleOutlined,
  MinusCircleOutlined,
  ClockCircleOutlined,
} from "@ant-design/icons";
import { text } from "./data";
import { stateLabel } from "./presentation";
const meanings: Record<string, string> = {
  unknown: "未知：缺少可靠观测",
  partial: "部分结果：查询或证据不完整",
  degraded: "降级：部分来源不可用",
  conflicted: "冲突：来源给出不一致事实",
  unresolved: "未确定根因：证据不足以确认",
  disabled: "未启用：查看能力说明",
  unverified: "未验收：没有真实运行证明",
  execution_unknown: "执行结果不确定：请核对副作用，禁止盲目重试",
  not_resolved: "命令已完成，后检查未证明故障修复",
  stale: "已过期：需要新的观测",
  unavailable: "不可用：来源或能力无法提供结果",
};
export function Status({ value, reason }: { value: unknown; reason?: string }) {
  const s = text(value);
  const good = [
    "fresh",
    "succeeded",
    "ready",
    "active",
    "enabled",
    "resolved",
    "confirmed",
    "verified",
    "healthy",
    "normal",
    "connected",
    "source_available",
    "archived_verified",
  ].includes(s);
  const off = ["disabled", "unverified", "null", "not_applicable"].includes(s);
  const color = good
    ? "success"
    : off
      ? "default"
      : ["failed", "conflicted", "execution_unknown"].includes(s)
        ? "error"
        : "warning";
  const Icon = good
    ? CheckCircleOutlined
    : off
      ? MinusCircleOutlined
      : s === "running"
        ? ClockCircleOutlined
        : ExclamationCircleOutlined;
  return (
    <Tooltip
      title={[reason || meanings[s] || stateLabel(s), "系统状态：" + s].join(
        " · ",
      )}
    >
      <Tag color={color} icon={<Icon />}>
        {stateLabel(s)}
      </Tag>
    </Tooltip>
  );
}

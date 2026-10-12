import { Space, Table } from "antd";
import { investigationFailure } from "./investigationFailure";
import { Status } from "./Status";
import { Details } from "./Details";
import type { PlatformCapabilitiesV1 } from "../api/generated/models";
const names = {
  investigation: "智能调查",
  commandExecution: "人工命令处置",
  kubevirt: "虚拟机管理（KubeVirt）",
  cdi: "虚拟机数据导入（CDI）",
  pyrca: "统计根因分析（PyRCA）",
};
const reasons: Record<string, string> = {
  "not-configured": "尚未配置调查服务",
  "configured-awaiting-successful-job": "调查服务已配置，尚无成功运行记录",
  "last-job-completed; model-readiness-not-probed":
    "已有完成的调查；当前模型可用性尚未实时探测",
  "ADR0008-development-deferred": "开发和真实运行验收延期，本轮不启用",
  "ADR0020-not-admitted": "此组件尚未准入，当前环境不启用",
  "SP07-runtime-not-configured": "尚未配置命令执行服务",
  "SP07-configured; execution-authorization-required":
    "执行服务已配置；每条实际命令仍需执行权限、风险评估和本人确认",
};
export function CapabilityStatus({ value }: { value: PlatformCapabilitiesV1 }) {
  return (
    <>
      <Space wrap className="status-note">
        <span>API</span>
        <Status value={value.apiReady ? "ready" : "unavailable"} />
        <span>关系图</span>
        <Status
          value={
            value.graphReady === null
              ? "unknown"
              : value.graphReady
                ? "ready"
                : "unavailable"
          }
        />
        <span>来源</span>
        <Status
          value={
            value.sourceDegraded === null
              ? "unknown"
              : value.sourceDegraded
                ? "degraded"
                : "fresh"
          }
        />
      </Space>
      <Table
        rowKey="key"
        dataSource={(
          [
            "investigation",
            "commandExecution",
            "kubevirt",
            "cdi",
            "pyrca",
          ] as const
        ).map((key) => ({ key, name: names[key], capability: value[key] }))}
        pagination={false}
        scroll={{ x: 650 }}
        expandable={{
          expandedRowRender: (r) => <Details value={r.capability} />,
        }}
        columns={[
          { title: "功能", dataIndex: "name", width: 240 },
          {
            title: "当前状态",
            render: (_, r) => <Status value={r.capability.status} />,
            width: 150,
          },
          {
            title: "可用范围与说明",
            render: (_, r) => (
              <span title={r.capability.reason}>
                {investigationFailure(r.capability.reason) ??
                  reasons[r.capability.reason] ??
                  "暂未取得可解释的运行原因；展开查看来源详情"}
              </span>
            ),
          },
        ]}
      />
    </>
  );
}

import { Space, Table } from "antd";
import type {
  InvestigationJobV2,
  InvestigationUsageV1,
} from "../../api/generated/models";
import { displayTime } from "../../components/data";
const rows: { key: keyof InvestigationUsageV1; label: string; unit: string }[] =
  [
    { key: "toolCalls", label: "工具调用", unit: "次" },
    { key: "rawQueries", label: "来源查询", unit: "次" },
    { key: "modelRequests", label: "模型请求", unit: "次" },
    { key: "inputTokens", label: "模型输入", unit: "token" },
    { key: "outputTokens", label: "模型输出", unit: "token" },
    { key: "resultBytes", label: "结果大小", unit: "B" },
    { key: "graphNodes", label: "关系图节点", unit: "个" },
    { key: "evidenceItems", label: "证据条目", unit: "条" },
    { key: "modelCostMicros", label: "模型成本额度", unit: "微单位" },
  ];
export function BudgetSummary({ job }: { job: InvestigationJobV2 }) {
  const format = (value: number | undefined, unit: string) =>
    value === undefined ? "未提供" : value.toLocaleString("zh-CN") + " " + unit;
  return (
    <section aria-label="调查额度">
      <h3>调查额度</h3>
      <Space wrap className="status-note">
        <span>
          工具调用：{format(job.budgetConsumed.toolCalls, "次")} / 上限{" "}
          {format(job.budget.toolCalls, "次")}
        </span>
        <span>
          模型请求：{format(job.budgetConsumed.modelRequests, "次")} / 上限{" "}
          {format(job.budget.modelRequests, "次")}
        </span>
        <span>调查截止时间：{displayTime(job.expiresAt)}</span>
      </Space>
      <details>
        <summary>查看全部额度与预留</summary>
        <p>
          预留额度用于正在处理的请求，尚不代表实际消耗。调查时长上限{" "}
          {format(job.budget.durationSeconds, "秒")}。
        </p>
        <Table
          pagination={false}
          rowKey="key"
          size="small"
          dataSource={rows}
          scroll={{ x: 550 }}
          columns={[
            { title: "额度", dataIndex: "label" },
            {
              title: "已使用",
              render: (_, r) => format(job.budgetConsumed[r.key], r.unit),
            },
            {
              title: "已预留",
              render: (_, r) => format(job.budgetReserved[r.key], r.unit),
            },
            {
              title: "上限",
              render: (_, r) => format(job.budget[r.key], r.unit),
            },
          ]}
        />
      </details>
    </section>
  );
}

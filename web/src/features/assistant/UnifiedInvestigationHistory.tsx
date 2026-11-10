import { Button, List, Space, Typography } from "antd";

// The unified entry keeps the investigations of the conversation reachable.
// The entries are the turns the conversation actually produced, so a
// v3 investigation that the unified entry started is never missing from the
// history. Opening an entry only reads; it never starts another job.
export type InvestigationHistoryEntry = {
  turnId: string;
  createdAt?: string;
  active?: boolean;
};

export function UnifiedInvestigationHistory({
  entries,
  onOpen,
}: {
  entries: InvestigationHistoryEntry[];
  onOpen: (turnId: string) => void;
}) {
  if (entries.length === 0) return null;
  return (
    <section aria-label="调查记录">
      <Space orientation="vertical" size="small" style={{ width: "100%" }}>
        <Typography.Text strong>调查记录</Typography.Text>
        <List
          size="small"
          dataSource={entries}
          locale={{ emptyText: "当前会话还没有调查记录" }}
          renderItem={(entry) => (
            <List.Item
              actions={[
                <Button
                  key="open"
                  size="small"
                  onClick={() => onOpen(entry.turnId)}
                >
                  查看调查过程
                </Button>,
              ]}
            >
              <Space direction="vertical" size={0}>
                <Typography.Text code>{entry.turnId}</Typography.Text>
                <Typography.Text type="secondary">
                  {entry.active ? "当前轮次 · " : ""}
                  {entry.createdAt
                    ? new Date(entry.createdAt).toLocaleString()
                    : "时间未知"}
                </Typography.Text>
              </Space>
            </List.Item>
          )}
        />
      </Space>
    </section>
  );
}

import { Alert, Button, Empty, Skeleton } from "antd";
import type { ReactNode } from "react";
import { ApiError } from "../api/client";
const explanations: Record<string, { title: string; description: string }> = {
  SOURCE_DEGRADED: {
    title: "数据来源暂不可用",
    description:
      "来源未能完成此次查询。当前故障记录仍可查看；证据内容和故障复验需等待来源恢复后重新核对。",
  },
  GRAPH_NOT_READY: {
    title: "资源查询暂不可用",
    description:
      "关系查询可能尚未就绪或服务异常。请重新加载；若持续失败，请联系平台管理员。当前查询不能据此判断资源已删除。",
  },
  CAPABILITY_DISABLED: {
    title: "此功能未启用",
    description: "当前环境没有启用此功能，请查看能力与来源页面了解可用范围。",
  },
  SOURCE_NOT_CONFIGURED: {
    title: "数据来源尚未配置",
    description: "请联系平台管理员配置并验证对应来源。",
  },
  SOURCE_SCOPE_UNVERIFIED: {
    title: "数据来源的可访问范围尚未确认",
    description: "来源尚未通过作用域验证，当前不能读取该范围的数据。",
  },
  BUDGET_EXHAUSTED: {
    title: "查询额度已用尽",
    description: "请缩小时间、资源或深度范围后重新查询。",
  },
  MODEL_UNAVAILABLE: {
    title: "模型暂不可用",
    description: "智能调查暂时无法继续；仍可查看已有故障、证据和规则诊断结果。",
  },
  NETWORK_UNAVAILABLE: {
    title: "服务连接中断",
    description:
      "请检查连接并重新加载。若刚提交操作，请先核对持久记录，避免重复提交。",
  },
};
export function QueryState({
  loading,
  error,
  empty = false,
  noScope = false,
  retry,
  children,
}: {
  loading: boolean;
  error: unknown;
  empty?: boolean;
  noScope?: boolean;
  retry?: () => void;
  children?: ReactNode;
}) {
  if (loading) return <Skeleton active paragraph={{ rows: 4 }} />;
  if (error) {
    const e = error instanceof ApiError ? error : undefined;
    const explanation = e ? explanations[e.code] : undefined;
    return (
      <Alert
        showIcon
        type={e?.status === 403 ? "warning" : "error"}
        title={
          explanation?.title ??
          (e?.status === 403
            ? "没有此范围的访问权限"
            : e?.status === 404
              ? "引用已失效或资源已删除"
              : e?.status === 409
                ? "版本冲突，请刷新后重新检查变更"
                : e?.status === 410
                  ? "游标已过期，请重新加载持久记录"
                  : "接口暂时不可用")
        }
        description={
          <>
            {explanation ? (
              <>
                <p>{explanation.description}</p>
                <details>
                  <summary>查看接口错误详情</summary>
                  <p>
                    {error instanceof Error ? error.message : String(error)}
                  </p>
                </details>
              </>
            ) : (
              <p>{error instanceof Error ? error.message : String(error)}</p>
            )}
            {e?.requestId && <p>请求 ID：{e.requestId}</p>}
            {retry && <Button onClick={retry}>重新加载</Button>}
          </>
        }
      />
    );
  }
  if (noScope)
    return (
      <Alert
        showIcon
        type="info"
        title="没有可用的集群范围"
        description="当前身份尚未获得可用的集群范围，或授权范围内没有已登记集群。请联系平台管理员核对集群登记和本人的作用域授权。"
      />
    );
  if (empty) return <Empty description="当前筛选范围没有正式数据" />;
  return <>{children}</>;
}

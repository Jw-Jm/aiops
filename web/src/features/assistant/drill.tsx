import { Alert, Button } from "antd";
import { useSearchParams } from "react-router-dom";
import type { AssistantQueryResultV1 } from "../../api/generated/models";

// A count of the assistant answer is a number of a submitted snapshot. The
// list the drill down opens is the current authorized projection of the same
// scope, so the two are never presented as the same thing: the list states
// the snapshot the count came from and that it may have changed.
export interface CountDrillTarget {
  path: string;
  params: Record<string, string>;
}

// The datasets that have a real record list. A dataset without a list has no
// drill down: the button is not shown instead of leading to a wrong page.
const listTargets: Record<
  string,
  {
    path: string;
    filters: Record<string, string>;
  }
> = {
  "resources.current": {
    path: "/resources",
    filters: { kind: "kind", health: "health", namespace: "namespace" },
  },
  "findings.occurrences": {
    path: "/events/alerts",
    filters: {
      state: "state",
      severity: "severity",
      namespace: "namespace",
      day: "day",
    },
  },
  "incidents.records": {
    path: "/events/incidents",
    filters: {
      state: "state",
      severity: "severity",
      namespace: "namespace",
      day: "day",
    },
  },
};

// The drill down of a counted group. The dimension of the row is the filter
// of the list, so the list shows exactly the records the count was taken
// from. A dimension without a matching filter has no drill down.
export function countDrillTarget(
  result: AssistantQueryResultV1 | undefined,
  dimensionValue: string | undefined,
): CountDrillTarget | undefined {
  if (!result) return undefined;
  const target = listTargets[result.datasetId];
  if (!target) return undefined;
  const params: Record<string, string> = {};
  if (dimensionValue !== undefined && result.dimension) {
    const filter = target.filters[result.dimension];
    if (!filter) return undefined;
    if (filter !== "day") params[filter] = dimensionValue;
  } else if (result.dimension) {
    return undefined;
  }
  return { path: target.path, params };
}

// The provenance of the count that led to this list. The list is the current
// authorized projection; the count belongs to the snapshot that is named
// here. The list never claims that both are the same.
export function AssistantDrillNotice() {
  const [url, setUrl] = useSearchParams();
  const from = url.get("aiFrom");
  if (from !== "assistant") return null;
  const dataset = url.get("aiDataset") || "";
  const snapshot = url.get("aiSnapshotAt") || "";
  const scopeDigest = url.get("aiScopeDigest") || "";
  const window = url.get("aiWindow") || "";
  const count = url.get("aiCount") || "";
  const version = url.get("aiVersion") || "";
  return (
    <Alert
      type="info"
      title={
        count
          ? `助手快照口径：${dataset} 计数 ${count}${snapshot ? `（读取于 ${snapshot}）` : ""}${
              window ? `，窗口 ${window}` : ""
            }${version ? `，来源版本 ${version}` : ""}。`
          : `助手快照口径：${dataset}${snapshot ? `（读取于 ${snapshot}）` : ""}。`
      }
      action={
        <Button
          onClick={() =>
            setUrl(
              new URLSearchParams({
                ...(url.get("clusterUid")
                  ? { clusterUid: url.get("clusterUid")! }
                  : {}),
              }),
            )
          }
        >
          退出下钻，查看当前列表
        </Button>
      }
      description={`原查询范围和筛选保持约束，页面筛选只会进一步收紧。以下是当前授权范围内的实时投影（范围摘要 ${scopeDigest || "未提供"}），可能与快照计数不同；快照与列表不视为同一结果。`}
    />
  );
}

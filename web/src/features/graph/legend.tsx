import type { ResourceRelationV2 } from "../../api/generated/models";
import { record } from "../../components/data";
export function relationClass(
  edge: ResourceRelationV2,
): "dynamic" | "inferred" | "explicit" | "unknown" {
  if (edge.ttl > 0) return "dynamic";
  const values = record(edge.provenance.sourceValues);
  const declared =
    values.relationshipClass ?? values.relationClass ?? values.kind;
  if (declared === "inferred" || declared === "explicit") return declared;
  if (values.inferred === true || edge.confidence < 1) return "inferred";
  // Unclassified provenance stays unknown; confidence alone cannot prove an explicit relation.
  return "unknown";
}
export function GraphLegend() {
  return (
    <p className="status-note">
      动态关系：点划线 · 推断关系：点线 · 显式关系：实线 ·
      未分类：细实线。过期边淡化并标注
      已过期。选择起点和终点可高亮最短可达路径；该路径不构成根因结论。
    </p>
  );
}

import type { ResourceRelationV2 } from "../../api/generated/models";
export function graphWithinBudget(nodes: number, depth: number) {
  return nodes <= 200 && depth <= 2;
}
export function edgeExpired(edge: ResourceRelationV2, now = Date.now()) {
  const end = edge.validTo ? Date.parse(edge.validTo) : Infinity;
  const ttl =
    edge.ttl > 0 ? Date.parse(edge.observedAt) + edge.ttl * 1000 : Infinity;
  return now >= Math.min(end, ttl);
}
export function graphLayout(count: number) {
  return count > 80
    ? { name: "grid", animate: false }
    : {
        name: "breadthfirst",
        directed: true,
        circle: false,
        spacingFactor: 1.3,
        animate: false,
      };
}

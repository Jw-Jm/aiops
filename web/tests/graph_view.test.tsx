import { it, expect } from "vitest";
import { edgeExpired, graphWithinBudget } from "../src/features/graph/layout";
import type { ResourceRelationV2 } from "../src/api/generated/models";
it("enforces 200-node and depth-2 budget without expansion", () => {
  expect(graphWithinBudget(200, 2)).toBe(true);
  expect(graphWithinBudget(201, 2)).toBe(false);
  expect(graphWithinBudget(1, 3)).toBe(false);
});
it("treats TTL and explicit validTo as independent expiry bounds", () => {
  const edge = {
    observedAt: "2026-10-11T00:00:00Z",
    ttl: 60,
  } as ResourceRelationV2;
  expect(edgeExpired(edge, Date.parse("2026-10-11T00:01:00Z"))).toBe(true);
  expect(
    edgeExpired({ ...edge, ttl: 0 }, Date.parse("2026-10-12T00:00:00Z")),
  ).toBe(false);
  expect(
    edgeExpired(
      { ...edge, ttl: 0, validTo: "2026-10-11T00:00:30Z" },
      Date.parse("2026-10-11T00:00:31Z"),
    ),
  ).toBe(true);
});

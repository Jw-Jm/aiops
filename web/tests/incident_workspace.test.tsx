import { it, expect } from "vitest";
import { revisionDiff } from "../src/features/rca/diff";
import type { RCARevisionV2 } from "../src/api/generated/models";
it("compares candidate identity, removal, additions and state changes", () => {
  const candidate = (key: string, score: number) => ({
    candidateKey: key,
    candidateType: "cause",
    resourceCanonicalId: "test",
    source: "deterministic" as const,
    evidenceRefs: ["evidence-test"],
    score,
    provenance: [],
  });
  const before = {
    result: {
      status: "unresolved",
      candidates: [candidate("removed", 1), candidate("changed", 0.1)],
    },
  } as unknown as RCARevisionV2;
  const after = {
    result: {
      status: "conflicted",
      candidates: [candidate("new", 1), candidate("changed", 0.8)],
    },
  } as unknown as RCARevisionV2;
  expect(revisionDiff(before, after)).toEqual({
    added: ["new"],
    removed: ["removed"],
    changed: ["changed"],
    status: { before: "unresolved", after: "conflicted" },
    evidenceChanges: [],
    degradedSourceChanges: [],
  });
});

it("shows evidence-only revisions and source degradation even when candidates stay unchanged", () => {
  const before = {
    result: {
      status: "unresolved",
      candidates: [],
      evidenceBundle: {
        supporting: ["old"],
        missing: ["logs"],
        contradicting: [],
        degradedSources: ["source-a"],
        ranked: [],
        conflicts: [],
      },
    },
  } as unknown as RCARevisionV2;
  const after = {
    result: {
      ...before.result,
      evidenceBundle: {
        ...before.result.evidenceBundle,
        supporting: ["new"],
        missing: [],
        contradicting: ["conflict"],
        degradedSources: [],
      },
    },
  } as unknown as RCARevisionV2;
  const diff = revisionDiff(before, after);
  expect(diff.added).toEqual([]);
  expect(diff.evidenceChanges).toEqual([
    {
      before: before.result.evidenceBundle,
      after: after.result.evidenceBundle,
    },
  ]);
  expect(diff.degradedSourceChanges).toEqual([
    { before: ["source-a"], after: [] },
  ]);
});

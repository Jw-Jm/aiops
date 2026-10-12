import type { RCARevisionV2 } from "../../api/generated/models";
export function revisionDiff(before: RCARevisionV2, after: RCARevisionV2) {
  const a = new Map(before.result.candidates.map((c) => [c.candidateKey, c]));
  const b = new Map(after.result.candidates.map((c) => [c.candidateKey, c]));
  return {
    added: [...b.keys()].filter((k) => !a.has(k)),
    removed: [...a.keys()].filter((k) => !b.has(k)),
    changed: [...b.keys()].filter(
      (k) => a.has(k) && JSON.stringify(a.get(k)) !== JSON.stringify(b.get(k)),
    ),
    status: { before: before.result.status, after: after.result.status },
    evidenceChanges:
      JSON.stringify(before.result.evidenceBundle) ===
      JSON.stringify(after.result.evidenceBundle)
        ? []
        : [
            {
              before: before.result.evidenceBundle,
              after: after.result.evidenceBundle,
            },
          ],
    degradedSourceChanges:
      JSON.stringify(before.result.evidenceBundle?.degradedSources) ===
      JSON.stringify(after.result.evidenceBundle?.degradedSources)
        ? []
        : [
            {
              before: before.result.evidenceBundle?.degradedSources,
              after: after.result.evidenceBundle?.degradedSources,
            },
          ],
  };
}

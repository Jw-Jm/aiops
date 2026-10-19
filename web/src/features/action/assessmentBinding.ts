import type { CommandRiskAssessmentRequest } from "../../api/generated/models";
import { record } from "../../components/data";
export async function matchesAssessment(
  request: CommandRiskAssessmentRequest,
  assessment: unknown,
) {
  const binding = record(record(assessment).binding),
    options = record(binding.executionOptions);
  const hash = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(request.actualCommand),
  );
  const digest =
    "sha256:" +
    Array.from(new Uint8Array(hash), (b) =>
      b.toString(16).padStart(2, "0"),
    ).join("");
  const actualProbe = record(binding.verification),
    expectedProbe = request.verification;
  const probeMatches =
    (!binding.verification && !expectedProbe) ||
    (!!binding.verification &&
      !!expectedProbe &&
      actualProbe.kind === expectedProbe.kind &&
      (actualProbe.port ?? 0) === (expectedProbe.port ?? 0) &&
      (actualProbe.path ?? "") === (expectedProbe.path ?? "") &&
      (actualProbe.https ?? false) === (expectedProbe.https ?? false) &&
      (actualProbe.expectedStatus ?? 0) ===
        (expectedProbe.expectedStatus ?? 0) &&
      (actualProbe.expectedBodyDigest ?? "") ===
        (expectedProbe.expectedBodyDigest ?? ""));
  return (
    probeMatches &&
    binding.actualCommandDigest === digest &&
    binding.targetCanonicalId === request.target &&
    binding.shell === request.shell &&
    binding.executionProfileId === request.executionProfileId &&
    binding.executionProfileVersion === request.executionProfileVersion &&
    binding.incidentId === request.incidentId &&
    binding.actionPlanId === request.actionPlanId &&
    binding.relatedExecutionId === request.relatedExecutionId &&
    binding.executionRelation === request.executionRelation &&
    binding.rollbackSpecDigest === request.rollbackSpecDigest &&
    options.timeoutSeconds === request.executionOptions.timeoutSeconds &&
    options.maxOutputBytes === request.executionOptions.maxOutputBytes &&
    options.workingDirectory === request.executionOptions.workingDirectory &&
    JSON.stringify(options.environment) ===
      JSON.stringify(request.executionOptions.environment)
  );
}

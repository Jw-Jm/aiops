import { record, text } from "../../components/data";
export type AuditProofResult = {
  status: "verified" | "failed" | "unverified";
  verified: number;
  failed: number;
  unverified: number;
  chain: string;
};
export async function verifyAuditPage(
  value: unknown,
): Promise<AuditProofResult> {
  const page = record(value);
  const rows = Array.isArray(page.data) ? page.data : [];
  let verified = 0,
    failed = 0,
    unverified = 0;
  for (const raw of rows) {
    const row = record(raw);
    if (row.digestVerified === false) {
      failed++;
      continue;
    }
    if (
      typeof row.canonicalJSON !== "string" ||
      typeof row.exportDigest !== "string" ||
      row.digestVerified !== true
    ) {
      unverified++;
      continue;
    }
    const bytes = new TextEncoder().encode(row.canonicalJSON);
    const hash = await crypto.subtle.digest("SHA-256", bytes);
    const digest =
      "sha256:" +
      Array.from(new Uint8Array(hash), (b) =>
        b.toString(16).padStart(2, "0"),
      ).join("");
    if (digest === row.exportDigest) verified++;
    else failed++;
  }
  return {
    status: failed
      ? "failed"
      : rows.length && unverified === 0
        ? "verified"
        : "unverified",
    verified,
    failed,
    unverified,
    chain: text(record(page.meta).chainVerification ?? "unverified"),
  };
}

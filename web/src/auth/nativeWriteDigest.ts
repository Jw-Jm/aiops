function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === "object")
    return Object.fromEntries(
      Object.entries(value)
        .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
        .map(([key, item]) => [key, canonical(item)]),
    );
  return value;
}
const hex = (bytes: ArrayBuffer) =>
  Array.from(new Uint8Array(bytes), (x) =>
    x.toString(16).padStart(2, "0"),
  ).join("");
export async function nativeWriteDigest(
  csrf: string,
  method: string,
  path: string,
  body: unknown,
) {
  const encoder = new TextEncoder();
  let value = body;
  if (
    method === "POST" &&
    (path === "/api/v1/admin/users" ||
      (path.startsWith("/api/v1/admin/users/") &&
        path.endsWith("/password:reset"))) &&
    body &&
    typeof body === "object" &&
    "initialPassword" in body &&
    typeof body.initialPassword === "string"
  ) {
    if (csrf.length < 16) throw new Error("当前会话验证信息缺失，请重新登录。");
    const key = await crypto.subtle.importKey(
      "raw",
      encoder.encode(csrf),
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["sign"],
    );
    const digest = await crypto.subtle.sign(
      "HMAC",
      key,
      encoder.encode(body.initialPassword),
    );
    value = { ...body, initialPassword: "hmac-sha256:" + hex(digest) };
  }
  const bytes = encoder.encode(
    body === undefined ? "" : JSON.stringify(canonical(value)),
  );
  return "sha256:" + hex(await crypto.subtle.digest("SHA-256", bytes));
}

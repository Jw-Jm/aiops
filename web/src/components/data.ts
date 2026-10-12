export function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}
export function list(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}
export function text(value: unknown): string {
  if (value === undefined) return "未提供";
  if (value === null) return "空值（null）";
  if (value === "") return "空字符串";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}
export function displayTime(value: unknown): string {
  if (typeof value !== "string" || value === "") return text(value);
  const d = new Date(value);
  return Number.isNaN(d.getTime())
    ? value
    : new Intl.DateTimeFormat("zh-CN", {
        dateStyle: "medium",
        timeStyle: "medium",
        timeZoneName: undefined,
        timeZone: "Asia/Shanghai",
      }).format(d) + " UTC+08:00";
}
const sensitive =
  /^(authorization|access_token|refresh_token|id_token|password|secret|clientsecret|credential|privatekey|api_key|apikey|token|accessToken|refreshToken|idToken|client_secret|client_secret_key|credentials|raw_prompt|prompt|rawchainofthought|chain_of_thought|reasoning_content|thinking|rawprompt)$/i;
export function publicValue(value: unknown, depth = 0): unknown {
  if (depth > 12) return "[深层数据：请缩小查询]";
  if (Array.isArray(value))
    return [
      ...value.slice(0, 200).map((v) => publicValue(v, depth + 1)),
      ...(value.length > 200
        ? ["[仅展示前 200 项；完整数据需按公共分页查询]"]
        : []),
    ];
  if (value && typeof value === "object")
    return Object.fromEntries(
      Object.entries(value)
        .filter(([k]) => !sensitive.test(k))
        .map(([k, v]) => [k, publicValue(v, depth + 1)]),
    );
  return value;
}
export function boundedText(value: string, max = 65536): string {
  const limit = Math.max(0, Math.floor(max));
  if (value.length <= limit) return value;
  const marker = "[较早输出已从浏览器缓冲移除；请查看持久记录]\n";
  if (limit <= marker.length) return marker.slice(0, limit);
  let tail = value.slice(-(limit - marker.length));
  // Do not start with the second half of a UTF-16 surrogate pair.
  if (/^[\uDC00-\uDFFF]/.test(tail)) tail = tail.slice(1);
  return marker + tail;
}

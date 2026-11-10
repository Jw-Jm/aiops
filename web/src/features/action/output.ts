import { record, text } from "../../components/data";
export function commandSequence(eventId: string, executionId: string) {
  const prefix = executionId + ":";
  if (
    !eventId.startsWith(prefix) ||
    !/^\d+$/.test(eventId.slice(prefix.length))
  )
    throw new Error("执行 SSE eventId 与持久执行不匹配");
  return BigInt(eventId.slice(prefix.length));
}
export function base64Bytes(value: unknown): Uint8Array {
  if (typeof value !== "string") throw new Error("输出缺少 Base64 bytes");
  return Uint8Array.from(atob(value), (c) => c.charCodeAt(0));
}
export function archivedPreview(value: unknown) {
  return Object.entries(record(value))
    .map(([stream, v]) => {
      const p = record(v);
      if (p.encoding !== "base64") return "[" + stream + "] 未提供可验证编码";
      const decoder = new TextDecoder();
      try {
        const first = base64Bytes(p.first),
          last = base64Bytes(p.last);
        if (!first.length && !last.length) return "";
        if (
          first.length < 2048 &&
          first.length === last.length &&
          first.every((byte, i) => byte === last[i])
        )
          return `[${stream} · 已接收的归档片段]\n${decoder.decode(first)}`;
        return `[${stream} · 归档前部摘要]\n${decoder.decode(first)}\n[归档尾部摘要；可能与前部重叠，不表示重复执行]\n${decoder.decode(last)}`;
      } catch {
        return `[${stream}] 归档摘要编码无效，请核对原始归档`;
      }
    })
    .filter(Boolean)
    .join("\n");
}

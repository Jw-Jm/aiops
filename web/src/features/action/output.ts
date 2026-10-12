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
      return (
        "[" +
        stream +
        " · 归档前后摘要，非完整输出]\n" +
        decoder.decode(base64Bytes(p.first)) +
        "\n[…中间输出请核对归档…]\n" +
        decoder.decode(base64Bytes(p.last))
      );
    })
    .join("\n");
}

import { it, expect } from "vitest";
import { archivedPreview } from "../src/features/action/output";
it("shows a short archived command receipt once and omits empty streams", () => {
  const out = archivedPreview({
    stdout: {
      encoding: "base64",
      first: btoa("patched once"),
      last: btoa("patched once"),
    },
    stderr: { encoding: "base64", first: "", last: "" },
  });
  expect(out.match(/patched once/g)).toHaveLength(1);
  expect(out).not.toContain("stderr");
  expect(out).not.toContain("完整输出");
});
it("does not infer identical full-size chunks mean one execution or remove real tail bytes", () => {
  const bytes = "x".repeat(2048),
    out = archivedPreview({
      stdout: { encoding: "base64", first: btoa(bytes), last: btoa(bytes) },
    });
  expect(out).toContain("可能与前部重叠，不表示重复执行");
  expect(out.split("x").length - 1).toBe(4096);
});
it("discloses invalid legacy encoding without crashing execution detail", () => {
  expect(
    archivedPreview({ stdout: { encoding: "base64", first: "!", last: "!" } }),
  ).toContain("编码无效");
});

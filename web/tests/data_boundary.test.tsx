import { it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { Details } from "../src/components/Details";
import {
  text,
  publicValue,
  boundedText,
  displayTime,
} from "../src/components/data";
import { getGetResourceByCanonicalIDUrl } from "../src/api/generated/platform";
import { resource } from "./fixtures";
it("retains false, zero, legal empty values and null distinctly", () => {
  render(<Details value={resource.attributes} />);
  expect(screen.getByText("0")).toBeInTheDocument();
  expect(screen.getByText("否（false）")).toBeInTheDocument();
  expect(screen.getByText("空字符串")).toBeInTheDocument();
  expect(screen.getByText("空值（null）")).toBeInTheDocument();
  expect(text(undefined)).toBe("未提供");
});
it("round-trips complete Canonical ID through generated query encoder", () => {
  const u = new URL(
    getGetResourceByCanonicalIDUrl({ canonicalId: resource.canonicalId }),
    "https://test.invalid",
  );
  expect(u.searchParams.get("canonicalId")).toBe(resource.canonicalId);
});
it("removes sensitive and raw reasoning fields recursively", () => {
  expect(
    publicValue({
      safe: 0,
      nested: {
        password: "secret",
        chain_of_thought: "private",
        result: false,
      },
    }),
  ).toEqual({ safe: 0, nested: { result: false } });
});
it("bounds long output while retaining tail and explicit loss marker", () => {
  const s = boundedText("x".repeat(100000) + "TAIL");
  expect(s.endsWith("TAIL")).toBe(true);
  expect(s.length).toBeLessThanOrEqual(65536);
  expect(boundedText("🧭".repeat(100), 51).length).toBeLessThanOrEqual(51);
  expect(boundedText("🧭".repeat(100), 51).split("\n")[1]).not.toMatch(
    /^[\uDC00-\uDFFF]/,
  );
  expect(s).toContain("缓冲移除");
});
it("expresses offset consistently and does not silently coerce empty time", () => {
  expect(displayTime("2026-10-11T00:00:00Z")).toContain("08:00:00");
  expect(displayTime("")).toBe("空字符串");
});

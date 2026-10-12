import { it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { StepLedger } from "../src/features/investigation/StepLedger";
it("locates only declared evidence and shows safe input summary, not a fabricated historical input", () => {
  const locate = vi.fn();
  render(
    <StepLedger
      onEvidence={locate}
      steps={[
        {
          stepId: "fixture-step",
          toolName: "query_logs",
          argsDigest: "sha256:" + "a".repeat(64),
          argsSummary: {
            canonicalId: "fixture-canonical",
            limit: 0,
            rawQueryOmitted: true,
          },
          state: "succeeded",
          result: {
            summary: "bounded fixture result",
            evidenceRefs: ["fixture-evidence"],
          },
        },
        {
          stepId: "fixture-old",
          toolName: "model",
          argsDigest: "sha256:" + "b".repeat(64),
          state: "failed",
        },
      ]}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "查看证据 evidence" }));
  expect(locate).toHaveBeenCalledWith("fixture-evidence");
  fireEvent.click(screen.getAllByRole("button", { name: "Expand row" })[0]!);
  expect(screen.getByText("输入范围和预算")).toBeVisible();
  const canonical = screen.getByText("fixture-canonical");
  canonical.closest("details")!.open = true;
  expect(canonical).toBeVisible();
  fireEvent.click(screen.getAllByRole("button", { name: "Expand row" })[0]!);
  expect(
    screen.getByText("此历史步骤未记录输入摘要；不推测原始输入。"),
  ).toBeVisible();
});

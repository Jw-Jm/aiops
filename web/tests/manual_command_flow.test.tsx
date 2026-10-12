import { it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { CommandEditor } from "../src/features/action/CommandEditor";
import {
  requestFingerprint,
  profileChoice,
} from "../src/features/action/CommandComposer";
import { terminalSafe } from "../src/features/action/ExecutionOutput";
import type { CommandRiskAssessmentRequest } from "../src/api/generated/models";
it("does not populate actual command from a suggestion", () => {
  render(<CommandEditor value="" onChange={() => {}} />);
  expect(
    screen.getByRole("textbox", { name: "实际 Bash 命令" }).textContent,
  ).toBe("");
});
it("binds target, command, version and options in confirmation identity", () => {
  const a = {
    actualCommand: "kubectl get pods",
    target: "target",
    shell: "bash",
    incidentId: "i",
    executionProfileId: "p",
    executionProfileVersion: 1,
    executionOptions: {
      timeoutSeconds: 60,
      maxOutputBytes: 1000,
      workingDirectory: "",
      environment: {},
    },
  } satisfies CommandRiskAssessmentRequest;
  for (const change of [
    { actualCommand: "kubectl delete pods" },
    { target: "other" },
    { executionProfileVersion: 2 },
    { executionOptions: { ...a.executionOptions, timeoutSeconds: 90 } },
  ])
    expect(requestFingerprint({ ...a, ...change })).not.toBe(
      requestFingerprint(a),
    );
});
it("removes terminal control sequences and OSC clipboard payloads", () => {
  expect(
    terminalSafe("hello\x1b[31mRED\x1b[0m\x1b]52;c;PRIVATE\x07 tail"),
  ).toBe("helloRED tail");
});

import { commandSequence, base64Bytes } from "../src/features/action/output";
it("uses UUID-prefixed command event cursors and decodes UTF-8 Base64 output", () => {
  expect(commandSequence("execution-test:0003", "execution-test")).toBe(3n);
  expect(() => commandSequence("other:3", "execution-test")).toThrow("不匹配");
  expect(
    new TextDecoder().decode(
      base64Bytes(
        btoa(String.fromCharCode(...new TextEncoder().encode("中文 stderr"))),
      ),
    ),
  ).toBe("中文 stderr");
});

it("keeps published profile versions distinct in selection and confirmation", () => {
  const v1 = { executionProfileId: "shared-id", version: 1 },
    v2 = { executionProfileId: "shared-id", version: 2 };
  expect(profileChoice(v1)).not.toBe(profileChoice(v2));
  const candidates = [v1, v2];
  expect(
    candidates.find((p) => profileChoice(p) === profileChoice(v2))?.version,
  ).toBe(2);
});

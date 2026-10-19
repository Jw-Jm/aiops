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
    {
      relatedExecutionId: "previous-execution",
      executionRelation: "follow_up" as const,
    },
    {
      relatedExecutionId: "previous-execution",
      executionRelation: "rollback" as const,
    },
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

import { matchesAssessment } from "../src/features/action/assessmentBinding";
it("rejects a risk response bound to another execution relation", async () => {
  const request = {
    incidentId: "i",
    actualCommand: "printf done",
    target: "target",
    shell: "bash",
    executionProfileId: "p",
    executionProfileVersion: 1,
    executionOptions: {
      timeoutSeconds: 60,
      maxOutputBytes: 1024,
      workingDirectory: "",
      environment: {},
    },
    relatedExecutionId: "parent",
    executionRelation: "follow_up",
  } satisfies CommandRiskAssessmentRequest;
  const hash = Array.from(
    new Uint8Array(
      await crypto.subtle.digest(
        "SHA-256",
        new TextEncoder().encode(request.actualCommand),
      ),
    ),
    (b) => b.toString(16).padStart(2, "0"),
  ).join("");
  const binding = {
    actualCommandDigest: "sha256:" + hash,
    targetCanonicalId: request.target,
    shell: request.shell,
    executionProfileId: request.executionProfileId,
    executionProfileVersion: 1,
    incidentId: "i",
    executionOptions: request.executionOptions,
    relatedExecutionId: "parent",
    executionRelation: "follow_up",
  };
  expect(await matchesAssessment(request, { binding })).toBe(true);
  for (const changed of [
    { rollbackSpecDigest: "sha256:" + "a".repeat(64) },
    { relatedExecutionId: "other" },
    { executionRelation: "rollback" },
    { relatedExecutionId: undefined, executionRelation: undefined },
  ]) {
    expect(
      await matchesAssessment(request, { binding: { ...binding, ...changed } }),
    ).toBe(false);
  }
});

it("binds probe protocol and HTTP assertions into confirmation matching", async () => {
  const request = {
    incidentId: "i",
    actualCommand: "ops-verify http",
    target: "target",
    shell: "bash",
    executionProfileId: "p",
    executionProfileVersion: 1,
    relatedExecutionId: "parent",
    executionRelation: "follow_up",
    executionOptions: {
      timeoutSeconds: 30,
      maxOutputBytes: 4096,
      workingDirectory: "",
      environment: {},
    },
    verification: {
      kind: "http",
      port: 80,
      path: "/healthz",
      https: false,
      expectedStatus: 200,
    },
  } satisfies CommandRiskAssessmentRequest;
  const hash = Array.from(
    new Uint8Array(
      await crypto.subtle.digest(
        "SHA-256",
        new TextEncoder().encode(request.actualCommand),
      ),
    ),
    (b) => b.toString(16).padStart(2, "0"),
  ).join("");
  const binding = {
    actualCommandDigest: "sha256:" + hash,
    targetCanonicalId: request.target,
    shell: "bash",
    executionProfileId: "p",
    executionProfileVersion: 1,
    incidentId: "i",
    relatedExecutionId: "parent",
    executionRelation: "follow_up",
    executionOptions: request.executionOptions,
    verification: {
      kind: "http",
      port: 80,
      path: "/healthz",
      expectedStatus: 200,
    },
  };
  expect(await matchesAssessment(request, { binding })).toBe(true);
  for (const verification of [
    undefined,
    { ...binding.verification, port: 443 },
    { ...binding.verification, path: "/other" },
    { ...binding.verification, expectedStatus: 201 },
  ]) {
    expect(
      await matchesAssessment(request, {
        binding: { ...binding, verification },
      }),
    ).toBe(false);
  }
});

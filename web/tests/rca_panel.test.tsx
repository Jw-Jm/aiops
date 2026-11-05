import { it, expect, vi, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { setNativeCredentials, clearCredentials } from "../src/api/client";
import type { CurrentRCASuccessV2 } from "../src/api/generated/models";
import { RCAPanel } from "../src/features/rca/RCAPanel";

const api = vi.hoisted(() => ({
  current: vi.fn(),
  revisions: vi.fn(),
}));
vi.mock("../src/api/generated/platform", () => ({
  getIncidentRCA: api.current,
  getRCARevisions: api.revisions,
  getRCARevision: vi.fn(),
}));
afterEach(() => clearCredentials());

function rcaFixture(result: Record<string, unknown>): CurrentRCASuccessV2 {
  return {
    schemaVersion: "current-rca/v2",
    currentEligible: true,
    currentDegradedSources: [],
    semantics: "frozen-revision-with-current-reference-authorization",
    revision: {
      schemaVersion: "rca-revision/v2",
      incidentId: "incident",
      revision: 3,
      baseIncidentRevision: 3,
      inputDigest: "sha256:" + "a".repeat(64),
      actor: "deterministic-worker",
      source: "deterministic",
      superseded: false,
      inputManifest: {},
      createdAt: "2026-11-04T00:00:00Z",
      result,
    },
  } as unknown as CurrentRCASuccessV2;
}

function renderRCA(data: CurrentRCASuccessV2) {
  const reply = (body: unknown) =>
    Promise.resolve({ status: 200, data: { data: body, requestId: "test" } });
  api.current.mockImplementation(() => reply(data));
  api.revisions.mockImplementation(() =>
    Promise.resolve({
      status: 200,
      data: { data: [], meta: { nextCursor: null }, requestId: "test" },
    }),
  );
  setNativeCredentials("tenant", "csrf");
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <RCAPanel incidentId="incident" onEvidence={vi.fn()} />
    </QueryClientProvider>,
  );
}

it("marks probable partial analyses as hypotheses over incomplete input", async () => {
  renderRCA(
    rcaFixture({
      status: "probable",
      partial: true,
      candidates: [
        {
          candidateKey: "sha256:" + "b".repeat(64),
          candidateType: "dimm_hardware_fault",
          resourceCanonicalId: "test",
          source: "deterministic",
          evidenceRefs: [],
          score: 1,
          provenance: [],
        },
      ],
      evidenceBundle: {
        supporting: [],
        contradicting: [],
        missing: [],
        degradedSources: ["current-graph-required-source-degraded"],
        ranked: [],
        conflicts: [],
      },
    }),
  );
  expect(
    await screen.findByText("部分结果：分析未完整完成"),
  ).toBeInTheDocument();
  expect(
    screen.getByText(/当前结论是最可能的假设，尚未确认/),
  ).toBeInTheDocument();
  expect(screen.getByText("内存硬件故障（待验证假设）")).toBeInTheDocument();
  expect(screen.getByText("分析时不可用来源")).toBeInTheDocument();
});

it("presents a confirmed root cause without the hypothesis caveat", async () => {
  renderRCA(
    rcaFixture({
      status: "confirmed",
      partial: false,
      candidates: [
        {
          candidateKey: "sha256:" + "c".repeat(64),
          candidateType: "csi_provisioner_failure",
          resourceCanonicalId: "test",
          source: "deterministic",
          evidenceRefs: [],
          score: 1,
          provenance: [],
        },
      ],
      evidenceBundle: {
        supporting: [],
        contradicting: [],
        missing: [],
        degradedSources: [],
        ranked: [],
        conflicts: [],
      },
    }),
  );
  expect(
    await screen.findByText("存储供应组件异常"),
  ).toBeInTheDocument();
  expect(screen.queryByText(/待验证假设/)).toBeNull();
  expect(screen.queryByText("部分结果：分析未完整完成")).toBeNull();
  expect(screen.queryByText("分析时不可用来源")).toBeNull();
});

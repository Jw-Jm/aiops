import { it, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Status } from "../src/components/Status";
import { IncidentRecord } from "../src/components/IncidentRecord";
import { ObservationFacts } from "../src/components/ObservationFacts";
import { Details } from "../src/components/Details";
import {
  symptomLabel,
  resourceIdentifier,
} from "../src/components/FaultSummary";
it("expresses uncertain execution and unresolved causes in operator language", () => {
  render(
    <>
      <Status value="execution_unknown" />
      <Status value="unresolved" />
    </>,
  );
  expect(screen.getByText("执行结果不确定")).toBeVisible();
  expect(screen.getByText("根因未确定")).toBeVisible();
});
it("puts useful resource fields first and retains expandable full identity", () => {
  const id = "k8s+v1://tenant/cluster/core/Pod/long-identity";
  render(
    <Details
      value={{
        name: "中文服务",
        namespace: "业务",
        canonicalId: id,
        digest: "sha256:example",
      }}
    />,
  );
  expect(screen.getByText("中文服务")).toBeVisible();
  const details = screen.getByText(/技术信息与完整标识/).closest("details")!;
  expect(details.open).toBe(false);
  fireEvent.click(screen.getByText(/技术信息与完整标识/));
  expect(screen.getByText(id)).toBeInTheDocument();
});
it("explains known detector rules and never invents missing resource names", () => {
  expect(symptomLabel("kubernetes/PVCUnbound/v1")).toBe("存储卷尚未绑定");
  expect(symptomLabel("custom/unknown-rule/v1")).toBe("custom/unknown-rule/v1");
  expect(resourceIdentifier("k8s+v1://tenant/cluster/core/Pod/123456789")).toBe(
    "Pod · 标识 23456789",
  );
});

it("preserves scalar observations, nested arrays, empty values and unknown fact fields", () => {
  render(
    <ObservationFacts
      value={[
        "第一行\n第二行",
        0,
        false,
        null,
        "",
        ["嵌套日志"],
        { actualMeasurement: 42, password: "sensitive" },
      ]}
    />,
  );
  expect(screen.getByText("第一行 第二行")).toBeVisible();
  expect(screen.getByText("0")).toBeVisible();
  expect(screen.getByText("否（false）")).toBeVisible();
  expect(screen.getByText("空值（null）")).toBeVisible();
  expect(screen.getByText("空字符串")).toBeVisible();
  expect(screen.getByText("嵌套日志")).toBeVisible();
  expect(screen.getByText("actualMeasurement")).toBeVisible();
  expect(screen.getByText("42")).toBeVisible();
  expect(screen.queryByText("sensitive")).not.toBeInTheDocument();
});

it("explains observed storage phase and cause while retaining the source values", () => {
  render(
    <ObservationFacts
      value={{
        phase: "Pending",
        reason: "PVCUnbound",
        nativeKind: "PersistentVolumeClaim",
      }}
    />,
  );
  expect(screen.getByText("等待就绪")).toBeVisible();
  expect(screen.getByText("存储卷尚未绑定")).toBeVisible();
  expect(screen.getByText("持久存储卷申请")).toBeVisible();
  expect(screen.getByText("Pending")).toBeInTheDocument();
});

it("puts the fault symptom and evidence navigation before protocol identifiers", () => {
  render(
    <IncidentRecord
      kind="finding"
      onEvidence={() => {}}
      value={{
        normalizedSymptom: "PVCUnbound",
        severity: "warning",
        lifecycleState: "firing",
        namespace: "中文业务",
        observedAt: "2026-10-11T00:00:00Z",
        startsAt: "2026-10-10T00:00:00Z",
        payload: { phase: "Pending" },
        evidenceRefs: ["evidence-reference"],
      }}
    />,
  );
  expect(screen.getByRole("heading", { name: "存储卷尚未绑定" })).toBeVisible();
  expect(screen.getByRole("button", { name: "查看关联证据 1" })).toBeVisible();
  expect(screen.getByText("等待就绪")).toBeVisible();
  expect(
    screen.getByText("完整告警记录与来源规则").closest("details")?.open,
  ).toBe(false);
});

import { QueryState } from "../src/components/QueryState";
import { ApiError } from "../src/api/client";
it("does not misdiagnose graph service failures as collection lag", () => {
  render(
    <QueryState
      loading={false}
      error={
        new ApiError(
          503,
          "GRAPH_NOT_READY",
          "worker unavailable",
          "request-test",
        )
      }
    />,
  );
  expect(screen.getByText("资源查询暂不可用")).toBeVisible();
  expect(screen.getByText(/关系查询可能尚未就绪或服务异常/)).toBeVisible();
  expect(
    screen.queryByText(/采集与关系图同步尚未完成/),
  ).not.toBeInTheDocument();
});

import { CapabilityStatus } from "../src/components/CapabilityStatus";
import type { PlatformCapabilitiesV1 } from "../src/api/generated/models";
it("explains actual investigation failures and preserves their source code", () => {
  const disabled = {
    status: "disabled",
    reason: "ADR0008-development-deferred",
  } as const;
  const value: PlatformCapabilitiesV1 = {
    schemaVersion: "platform-capabilities/v1",
    apiReady: true,
    graphReady: null,
    sourceDegraded: null,
    investigation: {
      enabled: true,
      readOnly: true,
      status: "degraded",
      reason: "BUDGET_EXHAUSTED",
    },
    commandExecution: {
      status: "unverified",
      reason: "SP07-configured; execution-authorization-required",
    },
    kubevirt: disabled,
    cdi: disabled,
    pyrca: { status: "disabled", reason: "ADR0020-not-admitted" },
  };
  render(<CapabilityStatus value={value} />);
  expect(
    screen.getByText("最近一次调查已用尽额度；请核对已有结果与调查预算"),
  ).toBeVisible();
  expect(
    screen.getByText("最近一次调查已用尽额度；请核对已有结果与调查预算"),
  ).toHaveAttribute("title", "BUDGET_EXHAUSTED");
});

import { investigationFailure } from "../src/components/investigationFailure";
it("explains actual model outage without fabricating a diagnosis or hiding unknown codes", () => {
  expect(investigationFailure("MODEL_UNAVAILABLE")).toContain(
    "仍可查看故障、证据和规则诊断",
  );
  expect(investigationFailure("MODEL_TIMEOUT")).toContain("超时");
  expect(investigationFailure("UNRECOGNIZED_NEW_CODE")).toBeUndefined();
});

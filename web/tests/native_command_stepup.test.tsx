import { it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CommandComposer } from "../src/features/action/CommandComposer";
import { setNativeCredentials } from "../src/api/client";
import { incident } from "./fixtures";

const api = vi.hoisted(() => ({
  risk: vi.fn(),
  confirm: vi.fn(),
  submit: vi.fn(),
  profiles: vi.fn(),
  challenge: vi.fn(),
  verify: vi.fn(),
}));

vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    authMode: "native",
    session: { roles: ["operator"], acr: "urn:ops:loa:1" },
    nativeSession: {
      csrf: { token: "csrf-token" },
      revocation: { sessionVersion: 1 },
    },
  }),
}));
vi.mock("../src/features/action/CommandEditor", () => ({
  CommandEditor: ({
    value,
    onChange,
  }: {
    value: string;
    onChange: (s: string) => void;
  }) => (
    <textarea
      aria-label="实际 Bash 命令"
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
}));
vi.mock("../src/api/generated/platform", async (original) => ({
  ...(await original<object>()),
  getExecutionProfiles: api.profiles,
  assessCommandRisk: api.risk,
  acknowledgeCommandRisk: api.confirm,
  createCommandExecution: api.submit,
  createNativeStepUpChallenge: api.challenge,
  verifyNativeStepUpChallenge: api.verify,
}));

const envelope = (data: unknown, status = 200) => ({
  status,
  headers: new Headers(),
  data: { data },
});

const commandDigest =
  "sha256:" +
  Array.from(
    new Uint8Array(
      await crypto.subtle.digest(
        "SHA-256",
        new TextEncoder().encode("kubectl get pods"),
      ),
    ),
    (b) => b.toString(16).padStart(2, "0"),
  ).join("");

const assessment = {
  assessmentId: "assessment-native",
  binding: {
    actualCommandDigest: commandDigest,
    targetCanonicalId: incident.resourceCanonicalId,
    targetUid: "uid-1",
    clusterUid: "cluster-1",
    namespace: null,
    targetRevision: 3,
    shell: "bash",
    incidentId: incident.incidentId,
    actionPlanId: null,
    executionProfileId: "p",
    executionProfileVersion: 2,
    riskAssessmentVersion: "risk-assessment/v1",
    executionRequestDigest: "sha256:request",
    policyVersion: "11111111-1111-1111-1111-111111111111",
    executionOptions: {
      timeoutSeconds: 60,
      maxOutputBytes: 1048576,
      workingDirectory: "",
      environment: {},
    },
  },
  executionRequestDigest: "sha256:request",
  expiresAt: "2099-01-01T00:00:00Z",
  risk: { operationRisk: "high", dataRisk: "low", requiresStepUp: true },
};

beforeEach(() => {
  api.risk.mockReset();
  api.confirm.mockReset();
  api.submit.mockReset();
  api.profiles.mockReset();
  api.challenge.mockReset();
  api.verify.mockReset();
  api.profiles.mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: {
      data: [
        {
          executionProfileId: "p",
          version: 2,
          name: "Profile",
          type: "k8s_cluster",
        },
      ],
      meta: { nextCursor: null },
    },
  });
  api.risk.mockResolvedValue(envelope(assessment));
  api.confirm.mockResolvedValue(
    envelope({ acknowledgementId: "ack-native", expiresAt: "2099-01-01T00:00:00Z" }),
  );
  api.challenge.mockResolvedValue(
    envelope(
      {
        challengeId: "challenge-native",
        revision: 1,
        requiredMethods: ["password", "totp"],
        completedMethods: [],
        state: "pending",
      },
      201,
    ),
  );
  api.verify.mockResolvedValue(
    envelope({
      revision: 2,
      completedMethods: ["password", "totp"],
      verified: true,
      challengeState: "verified",
      stepUpSession: {
        idleExpiresAt: "2099-01-01T00:00:00Z",
        absoluteExpiresAt: "2099-01-01T00:00:00Z",
      },
    }),
  );
  setNativeCredentials("tenant-1");
});

async function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <CommandComposer incident={incident} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "实际 Bash 命令" }), {
    target: { value: "kubectl get pods" },
  });
  fireEvent.mouseDown(
    screen.getByRole("combobox", { name: "执行配置与精确版本" }),
  );
  await screen.findByText("Profile · 版本 2 · Kubernetes 集群");
  fireEvent.click(screen.getByText("Profile · 版本 2 · Kubernetes 集群"));
  return client;
}

it("sends the verified step-up challenge with the risk acknowledgement", async () => {
  const client = await setup();
  fireEvent.click(screen.getByRole("button", { name: "2 · 评估实际命令风险" }));
  await screen.findByText("实际命令风险", { exact: true });
  fireEvent.click(screen.getByRole("checkbox"));
  await screen.findByLabelText("验证密码");
  fireEvent.change(screen.getByLabelText("验证密码"), {
    target: { value: "fixture-password" },
  });
  fireEvent.change(screen.getByLabelText("验证动态码"), {
    target: { value: "123456" },
  });
  fireEvent.click(screen.getByRole("button", { name: "验证本次操作" }));
  await waitFor(() => expect(api.verify).toHaveBeenCalled());
  const confirmButton = screen.getByRole("button", { name: "4 · 本人确认风险" });
  await waitFor(() => expect(confirmButton).not.toBeDisabled());
  fireEvent.click(confirmButton);
  await waitFor(() => expect(api.confirm).toHaveBeenCalled());
  const options = api.confirm.mock.calls[0]?.[1] as
    | { headers?: Record<string, string> }
    | undefined;
  expect(options?.headers?.["X-Ops-Step-Up-Challenge"]).toBe("challenge-native");
  client.clear();
});

it("refuses the risk acknowledgement until the step-up is verified", async () => {
  const client = await setup();
  fireEvent.click(screen.getByRole("button", { name: "2 · 评估实际命令风险" }));
  await screen.findByText("实际命令风险", { exact: true });
  fireEvent.click(screen.getByRole("checkbox"));
  const confirmButton = screen.getByRole("button", { name: "4 · 本人确认风险" });
  await waitFor(() => expect(confirmButton).toBeDisabled());
  fireEvent.click(confirmButton);
  expect(api.confirm).not.toHaveBeenCalled();
  client.clear();
});

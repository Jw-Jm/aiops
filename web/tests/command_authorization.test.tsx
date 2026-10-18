import { it, expect, vi, beforeEach } from "vitest";
import {
  render,
  screen,
  fireEvent,
  waitFor,
  act,
} from "@testing-library/react";
import { MemoryRouter, useSearchParams } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CommandComposer } from "../src/features/action/CommandComposer";
import { authEvents, setCredentials } from "../src/api/client";
import { incident } from "./fixtures";
const api = vi.hoisted(() => ({
  risk: vi.fn(),
  confirm: vi.fn(),
  profiles: vi.fn(),
}));
vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({ session: { roles: ["operator"], acr: "urn:ops:loa:1" } }),
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
}));
const response = (data: unknown) => ({
  status: 200,
  headers: new Headers(),
  data: { data },
});
const assessment = {
  assessmentId: "assessment",
  binding: {
    actualCommandDigest:
      "sha256:" +
      Array.from(
        new Uint8Array(
          await crypto.subtle.digest(
            "SHA-256",
            new TextEncoder().encode("kubectl get pods"),
          ),
        ),
        (b) => b.toString(16).padStart(2, "0"),
      ).join(""),
    targetCanonicalId: incident.resourceCanonicalId,
    shell: "bash",
    incidentId: incident.incidentId,
    actionPlanId: null,
    executionProfileId: "p",
    executionProfileVersion: 2,
    executionOptions: {
      timeoutSeconds: 60,
      maxOutputBytes: 1048576,
      workingDirectory: "",
      environment: {},
    },
  },
  executionRequestDigest: "sha256:test",
  expiresAt: "2099-01-01T00:00:00Z",
  risk: { operationRisk: "low", dataRisk: "low", requiresStepUp: false },
};
beforeEach(() => {
  api.risk.mockReset();
  api.confirm.mockReset();
  api.profiles.mockReset();
  api.profiles.mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: {
      meta: { nextCursor: "fixture-page-2" },
      data: [
        {
          executionProfileId: "p",
          version: 1,
          name: "Profile",
          type: "k8s_namespace",
        },
        {
          executionProfileId: "p",
          version: 2,
          name: "Profile",
          type: "k8s_namespace",
        },
      ],
    },
  });
  setCredentials("fixture-token", "fixture-tenant");
});
function ChangeExecutionRelation() {
  const [, setUrl] = useSearchParams();
  return (
    <button
      onClick={() =>
        setUrl({
          relatedExecutionId: "previous",
          executionRelation: "follow_up",
        })
      }
    >
      关联已有执行
    </button>
  );
}
async function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <CommandComposer incident={incident} />
        <ChangeExecutionRelation />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "实际 Bash 命令" }), {
    target: { value: "kubectl get pods" },
  });
  fireEvent.mouseDown(
    screen.getByRole("combobox", { name: "执行配置与精确版本" }),
  );
  await screen.findByText("Profile · 版本 2 · Kubernetes 命名空间");
  fireEvent.click(screen.getByText("Profile · 版本 2 · Kubernetes 命名空间"));
  return client;
}
it("clears actual command and requires a fresh assessment after selecting an execution relation", async () => {
  const client = await setup();
  fireEvent.click(screen.getByRole("button", { name: "关联已有执行" }));
  const command = screen.getByRole("textbox", { name: "实际 Bash 命令" });
  await waitFor(() => expect(command).toHaveValue(""));
  expect(api.risk).not.toHaveBeenCalled();
  expect(api.confirm).not.toHaveBeenCalled();
  api.risk.mockResolvedValue(
    response({
      ...assessment,
      binding: {
        ...assessment.binding,
        relatedExecutionId: "previous",
        executionRelation: "follow_up",
      },
    }),
  );
  fireEvent.change(command, { target: { value: "kubectl get pods" } });
  fireEvent.click(screen.getByRole("button", { name: "2 · 评估实际命令风险" }));
  await screen.findByText("继续处置关联的原执行");
  expect(api.risk.mock.calls[0][0]).toMatchObject({
    relatedExecutionId: "previous",
    executionRelation: "follow_up",
    actualCommand: "kubectl get pods",
  });
  client.clear();
});
it("uses selected immutable profile version and rejects risk response after permission change", async () => {
  let resolve!: (v: unknown) => void;
  api.risk.mockImplementation(
    () =>
      new Promise((r) => {
        resolve = r;
      }),
  );
  await setup();
  fireEvent.click(screen.getByRole("button", { name: "2 · 评估实际命令风险" }));
  await waitFor(() => expect(api.risk).toHaveBeenCalled());
  expect(api.risk.mock.calls[0]?.[0]).toMatchObject({
    executionProfileId: "p",
    executionProfileVersion: 2,
  });
  act(() => authEvents.dispatchEvent(new Event("authorizationChanged")));
  await act(async () => resolve(response(assessment)));
  expect(
    screen.queryByText("实际命令风险", { exact: true }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "5 · 提交本人命令" }),
  ).not.toBeInTheDocument();
});
it("does not restore acknowledgement from a delayed response after revocation", async () => {
  api.risk.mockResolvedValue(response(assessment));
  let resolve!: (v: unknown) => void;
  api.confirm.mockImplementation(
    () =>
      new Promise((r) => {
        resolve = r;
      }),
  );
  await setup();
  fireEvent.click(screen.getByRole("button", { name: "2 · 评估实际命令风险" }));
  await screen.findByText("实际命令风险", { exact: true });
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.click(screen.getByRole("button", { name: "4 · 本人确认风险" }));
  await waitFor(() => expect(api.confirm).toHaveBeenCalled());
  act(() => authEvents.dispatchEvent(new Event("authorizationChanged")));
  await act(async () =>
    resolve(
      response({ acknowledgementId: "ack", expiresAt: "2099-01-01T00:00:00Z" }),
    ),
  );
  expect(
    screen.queryByRole("button", { name: "5 · 提交本人命令" }),
  ).not.toBeInTheDocument();
});

it("loads later profile pages and retains the selected immutable version when returning to the first page", async () => {
  api.risk.mockResolvedValue(
    response({
      ...assessment,
      binding: {
        ...assessment.binding,
        executionProfileId: "later",
        executionProfileVersion: 7,
      },
    }),
  );
  await setup();
  api.profiles.mockImplementation(async (q) =>
    q?.cursor
      ? {
          ...response([
            {
              executionProfileId: "later",
              version: 7,
              name: "Later Profile",
              type: "k8s_namespace",
            },
          ]),
          data: {
            data: [
              {
                executionProfileId: "later",
                version: 7,
                name: "Later Profile",
                type: "k8s_namespace",
              },
            ],
            meta: { nextCursor: null },
          },
        }
      : response([]),
  );
  fireEvent.click(screen.getByRole("button", { name: "更多配置版本" }));
  await waitFor(() =>
    expect(api.profiles).toHaveBeenCalledWith(
      { limit: 50, cursor: "fixture-page-2" },
      expect.anything(),
    ),
  );
  fireEvent.mouseDown(
    screen.getByRole("combobox", { name: "执行配置与精确版本" }),
  );
  fireEvent.click(
    await screen.findByText("Later Profile · 版本 7 · Kubernetes 命名空间"),
  );
  fireEvent.click(screen.getByRole("button", { name: "最新配置" }));
  fireEvent.click(screen.getByRole("button", { name: "2 · 评估实际命令风险" }));
  await waitFor(() => expect(api.risk).toHaveBeenCalled());
  expect(api.risk.mock.calls[0][0]).toMatchObject({
    executionProfileId: "later",
    executionProfileVersion: 7,
  });
});

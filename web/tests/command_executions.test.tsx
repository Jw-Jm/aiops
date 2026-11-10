import { it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { setNativeCredentials, clearCredentials } from "../src/api/client";
import { CommandExecutionsPage } from "../src/pages/CommandExecutionsPage";

const api = vi.hoisted(() => ({
  list: vi.fn(),
  detail: vi.fn(),
  cancel: vi.fn(),
  postcheck: vi.fn(),
  incident: vi.fn(),
}));
vi.mock("../src/api/generated/platform", () => ({
  listCommandExecutions: api.list,
  getCommandExecution: api.detail,
  cancelCommandExecution: api.cancel,
  getCommandPostCheck: api.postcheck,
  getIncident: api.incident,
}));
afterEach(() => clearCredentials());

const execution = {
  schemaVersion: "command-execution/v1",
  executionId: "019a0000-0000-7000-8000-0000000000e1",
  subject: "user:operator",
  incidentId: "019a0000-0000-7000-8000-000000000001",
  state: "running",
  exitCode: null,
  outputTruncated: false,
  extensions: {
    createdAt: "2026-11-04T10:00:00Z",
    binding: {
      clusterUid: "cluster-a",
      targetCanonicalId: "k8s+v1://tenant/cluster-a/core/Node/node-a",
    },
  },
};

function renderPage(entry = "/jobs/commands") {
  api.list.mockReset();
  api.cancel.mockReset();
  api.detail.mockReset();
  api.postcheck.mockResolvedValue({
    status: 200,
    data: {
      data: {
        status: "completed",
        verdict: "inconclusive",
        executionState: "succeeded",
        applicability: { targetKind: "Service" },
        relatedExecutions: [
          {
            executionId: "child",
            direction: "child",
            relation: "follow_up",
            state: "succeeded",
          },
        ],
      },
    },
  });
  setNativeCredentials("tenant", "csrf");
  const page = (data: unknown[], nextCursor: string | null) =>
    Promise.resolve({
      status: 200,
      data: { data, meta: { nextCursor }, requestId: "test" },
    });
  api.list.mockImplementation(() => page([execution], null));
  api.detail.mockImplementation(() =>
    Promise.resolve({
      status: 200,
      data: {
        data: {
          ...execution,
          extensions: {
            ...execution.extensions,
            outputPreviews: {
              stdout: {
                encoding: "base64",
                first: btoa("first bytes"),
                last: btoa("last bytes"),
              },
            },
          },
        },
        requestId: "test",
      },
    }),
  );
  api.cancel.mockImplementation(() =>
    Promise.resolve({ status: 200, data: { data: {}, requestId: "test" } }),
  );
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={[entry]}>
        <CommandExecutionsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

it("lists executions with status and target link", async () => {
  renderPage();
  expect(await screen.findByText("user:operator")).toBeInTheDocument();
  const target = screen.getByTitle(
    "k8s+v1://tenant/cluster-a/core/Node/node-a",
  );
  expect(target.closest("a")?.getAttribute("href")).toContain(
    "/resources/detail?canonicalId=",
  );
  expect(api.list).toHaveBeenCalledWith(
    expect.objectContaining({ limit: 50 }),
    expect.anything(),
  );
});

it("expands a running execution and cancels it", async () => {
  renderPage();
  const row = await screen.findByText("user:operator");
  const expand = row
    .closest("tr")!
    .querySelector(".ant-table-row-expand-icon")!;
  fireEvent.click(expand);
  const cancel = await screen.findByRole("button", { name: "取消执行" });
  expect(cancel).not.toBeDisabled();
  fireEvent.click(cancel);
  expect(api.cancel).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "继续等待" }));
  expect(api.cancel).not.toHaveBeenCalled();
  fireEvent.click(cancel);
  fireEvent.click(screen.getByRole("button", { name: "确认取消执行" }));
  await waitFor(() =>
    expect(api.cancel).toHaveBeenCalledWith(
      execution.executionId,
      expect.anything(),
    ),
  );
});

it("sends literal search to the SDK and clears search, state and cursor together", async () => {
  renderPage("/jobs/commands?state=running&cursor=old");
  await screen.findByText("user:operator");
  fireEvent.change(screen.getByLabelText("搜索执行"), {
    target: { value: "node-a" },
  });
  fireEvent.keyDown(screen.getByLabelText("搜索执行"), {
    key: "Enter",
    code: "Enter",
  });
  await waitFor(() =>
    expect(api.list).toHaveBeenLastCalledWith(
      expect.objectContaining({
        q: "node-a",
        state: "running",
        cursor: undefined,
      }),
      expect.anything(),
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "清空筛选" }));
  await waitFor(() =>
    expect(api.list).toHaveBeenLastCalledWith(
      { limit: 50, cursor: undefined },
      expect.anything(),
    ),
  );
});
it("opens frozen post-check and routes global follow-up to the actual incident without writes", async () => {
  renderPage("/jobs/commands?executionId=" + execution.executionId);
  await screen.findByText("后检查与后续建议");
  expect(api.postcheck).toHaveBeenCalledWith(
    execution.executionId,
    expect.anything(),
  );
  const child = await screen.findByRole("link", { name: /后续执行.*child/ });
  const target = new URL(child.getAttribute("href")!, "https://ops.invalid");
  expect(target.pathname).toBe("/events/incidents/" + execution.incidentId);
  expect(target.searchParams.get("clusterUid")).toBe("cluster-a");
  expect(target.searchParams.get("executionId")).toBe("child");
  fireEvent.click(
    screen.getByRole("button", { name: "关联本次执行，准备实际探测" }),
  );
  expect(api.cancel).not.toHaveBeenCalled();
});
it("requires a valid authorized incident before opening manual execution", async () => {
  renderPage();
  await screen.findByText("user:operator");
  fireEvent.click(screen.getByRole("button", { name: "新建人工执行" }));
  fireEvent.change(screen.getByLabelText("关联故障 ID"), {
    target: { value: "bad" },
  });
  expect(screen.getByRole("button", { name: "核对故障" })).toBeDisabled();
  expect(api.incident).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("关联故障 ID"), {
    target: { value: execution.incidentId },
  });
  api.incident.mockResolvedValueOnce({
    status: 403,
    data: { error: { code: "FORBIDDEN" } },
  });
  fireEvent.click(screen.getByRole("button", { name: "核对故障" }));
  expect(await screen.findByText("没有此范围的访问权限")).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "进入风险评估与人工执行" }),
  ).not.toBeInTheDocument();
});

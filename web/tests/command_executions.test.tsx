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
}));
vi.mock("../src/api/generated/platform", () => ({
  listCommandExecutions: api.list,
  getCommandExecution: api.detail,
  cancelCommandExecution: api.cancel,
}));
afterEach(() => clearCredentials());

const execution = {
  schemaVersion: "command-execution/v1",
  executionId: "019a0000-0000-7000-8000-0000000000e1",
  subject: "user:operator",
  state: "running",
  exitCode: null,
  outputTruncated: false,
  extensions: {
    createdAt: "2026-11-04T10:00:00Z",
    binding: {
      targetCanonicalId:
        "k8s+v1://tenant/cluster-a/core/Node/node-a",
    },
  },
};

function renderPage() {
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
      <MemoryRouter initialEntries={["/jobs/commands"]}>
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
  await waitFor(() =>
    expect(api.cancel).toHaveBeenCalledWith(
      execution.executionId,
      expect.anything(),
    ),
  );
});

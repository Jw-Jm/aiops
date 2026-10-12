// Explicit configuration API fixture; separate from real deployment validation.
import { expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RegistryEditor } from "../src/pages/admin/RegistryEditor";
import { setCredentials } from "../src/api/client";
const api = vi.hoisted(() => ({ read: vi.fn(), update: vi.fn() }));
vi.mock("../src/api/generated/platform", async (original) => ({
  ...(await original<object>()),
  getRegistryDraft: api.read,
  updateRegistryDraft: api.update,
}));
it("rereads a conflicting draft without overwriting local edits and binds an explicitly merged update to the loaded revision", async () => {
  setCredentials("fixture", "fixture-tenant");
  const response = (status: number, data: unknown) => ({
    status,
    data,
    headers: new Headers(),
  });
  api.update
    .mockResolvedValueOnce(
      response(409, {
        code: "REVISION_CONFLICT",
        message: "changed",
        requestId: "fixture-request",
      }),
    )
    .mockResolvedValueOnce(
      response(200, { data: { draftId: "fixture-draft", revision: 3 } }),
    );
  api.read.mockResolvedValue(
    response(200, {
      data: {
        draftId: "fixture-draft",
        logicalName: "fixture-policy",
        revision: 2,
        content: { serverChange: true },
      },
    }),
  );
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <RegistryEditor ready onChanged={() => {}} />
    </QueryClientProvider>,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "更新策略 draftId" }), {
    target: { value: "fixture-draft" },
  });
  const input = screen.getByRole("textbox", { name: "策略草稿内容 JSON" });
  fireEvent.change(input, { target: { value: '{"localChange":true}' } });
  fireEvent.click(screen.getByRole("button", { name: "按修订号更新草稿" }));
  await screen.findByText("版本冲突，请刷新后重新检查变更");
  fireEvent.click(
    screen.getByRole("button", { name: "重新读取草稿（保留本地修改）" }),
  );
  await screen.findByText("服务端草稿 · 修订号 2");
  expect(input).toHaveValue('{"localChange":true}');
  fireEvent.change(input, {
    target: { value: '{"localChange":true,"serverChange":true}' },
  });
  fireEvent.click(
    screen.getByRole("checkbox", { name: "已比较并在上方完成手动合并" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "保留合并内容，采用已读取修订号" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "按修订号更新草稿" }));
  await waitFor(() => expect(api.update).toHaveBeenCalledTimes(2));
  expect(api.update.mock.calls[1][1]).toEqual({
    expectedRevision: 2,
    content: { localChange: true, serverChange: true },
  });
});

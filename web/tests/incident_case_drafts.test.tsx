import { beforeEach, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { IncidentCaseDrafts } from "../src/features/investigation/IncidentCaseDrafts";
import { setCredentials } from "../src/api/client";
import { ConfigProvider } from "antd";

const mock = vi.hoisted(() => ({
  create: vi.fn(),
  list: vi.fn(),
  read: vi.fn(),
  session: "session-a",
}));
vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: { subject: "operator", roles: ["operator"] },
    nativeSession: { sessionRef: mock.session },
  }),
}));
vi.mock("../src/app/ClusterContext", () => ({
  useOperationalCluster: () => ({ scopeKey: "scope" }),
  useClusterDraft: vi.fn(),
}));
vi.mock("../src/api/generated/platform", () => ({
  createIncidentCaseDraft: mock.create,
  listIncidentCaseDrafts: mock.list,
  getIncidentCaseDraftVersion: mock.read,
}));
const draft = {
  documentId: "draft-id",
  kind: "case",
  version: 1,
  state: "draft",
  incidentId: "incident-id",
  incidentRevision: 6,
  incidentState: "resolved",
  title: "Persisted case",
  summary: "Verified summary",
  content: "Facts and unknowns",
  creatorSubject: "operator",
  createdAt: "2026-11-11T02:00:00Z",
};
const ok = (data: unknown, status = 200) => ({
  status,
  headers: new Headers(),
  data,
});
const props = {
  incidentId: "incident-id",
  revision: 6,
  state: "resolved",
  canonicalId: "pod-uid",
};
function page() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <ConfigProvider theme={{ token: { motion: false } }}>
      <QueryClientProvider client={client}>
        <IncidentCaseDrafts {...props} />
      </QueryClientProvider>
    </ConfigProvider>,
  );
}
beforeEach(() => {
  vi.clearAllMocks();
  setCredentials("case-draft-test");
  mock.session = "session-a";
  mock.list.mockResolvedValue(ok({ data: [], meta: { nextCursor: "" } }));
  mock.create.mockResolvedValue(ok({ data: draft }, 201));
  mock.read.mockResolvedValue(ok({ data: draft }));
});
it("dismisses the case form without sending a mutation", async () => {
  page();
  fireEvent.click(screen.getByText("整理为案例"));
  expect(
    (screen.getByLabelText("案例正文与适用边界") as HTMLTextAreaElement).value,
  ).toContain("记录版本：6");
  fireEvent.click(screen.getByRole("button", { name: /取\s*消/ }));
  await waitFor(() =>
    expect(
      screen.queryByRole("dialog", { name: "新建知识草稿" }),
    ).not.toBeInTheDocument(),
  );
  expect(mock.create).not.toHaveBeenCalled();
});
it("saves the origin revision and rereads the persisted immutable version", async () => {
  page();
  fireEvent.click(screen.getByText("整理为案例"));
  fireEvent.click(
    screen.getByRole("button", { name: "保存草稿" }),
  );
  await screen.findByText("Facts and unknowns");
  expect(mock.create.mock.calls[0][0]).toMatchObject({
    kind: "case",
    incidentId: "incident-id",
    expectedRevision: 6,
  });
  expect(mock.read.mock.calls[0].slice(0, 2)).toEqual(["draft-id", 1]);
  expect(mock.list).toHaveBeenCalled();
});
it("keeps input and reuses the transport key after an unavailable response", async () => {
  mock.create.mockResolvedValueOnce(
    ok({ code: "SOURCE_DEGRADED", message: "Temporarily unavailable" }, 503),
  );
  page();
  fireEvent.click(screen.getByText("整理为案例"));
  fireEvent.change(screen.getByLabelText("案例标题"), {
    target: { value: "Reviewed case" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "保存草稿" }),
  );
  await screen.findByText("数据来源暂不可用");
  expect(screen.getByLabelText("案例标题")).toHaveValue("Reviewed case");
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "保存草稿" }),
    ).toBeEnabled(),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "保存草稿" }),
  );
  await screen.findByText("Facts and unknowns");
  expect(mock.create).toHaveBeenCalledTimes(2);
  const headers = mock.create.mock.calls.map(
    (c) => c[1].headers["Idempotency-Key"],
  );
  expect(headers[0]).toBeTruthy();
  expect(headers[1]).toBe(headers[0]);
});
it("aborts a pending save and drops its late response when the login session changes", async () => {
  let resolve!: (value: ReturnType<typeof ok>) => void;
  mock.create.mockReturnValueOnce(
    new Promise((done) => {
      resolve = done;
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const node = () => (
    <ConfigProvider theme={{ token: { motion: false } }}>
      <QueryClientProvider client={client}>
        <IncidentCaseDrafts {...props} />
      </QueryClientProvider>
    </ConfigProvider>
  );
  const view = render(node());
  fireEvent.click(screen.getByText("整理为案例"));
  const button = screen.getByRole("button", { name: "保存草稿" });
  fireEvent.click(button);
  fireEvent.click(button);
  expect(mock.create).toHaveBeenCalledTimes(1);
  const signal = mock.create.mock.calls[0][1].signal as AbortSignal;
  mock.session = "session-b";
  view.rerender(node());
  expect(signal.aborted).toBe(true);
  resolve(ok({ data: draft }, 201));
  await waitFor(() =>
    expect(screen.queryByText("Facts and unknowns")).not.toBeInTheDocument(),
  );
  expect(mock.read).not.toHaveBeenCalled();
});

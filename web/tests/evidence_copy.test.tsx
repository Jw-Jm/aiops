import { beforeEach, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { EvidenceCopyButton } from "../src/features/evidence/EvidenceCopyButton";
import { setCredentials } from "../src/api/client";
const api = vi.hoisted(() => ({ read: vi.fn(), write: vi.fn() }));
vi.mock("../src/api/generated/platform", () => ({ getEvidence: api.read }));
vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: { subject: "operator" },
    nativeSession: { sessionRef: "session" },
  }),
}));
vi.mock("../src/app/ClusterContext", () => ({
  useOperationalCluster: () => ({ scopeKey: "scope" }),
}));
const response = (status: number, data: unknown) => ({
  status,
  headers: new Headers(),
  data,
});
beforeEach(() => {
  vi.clearAllMocks();
  setCredentials("evidence-copy-fixture");
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: api.write },
  });
  api.write.mockResolvedValue(undefined);
});
it("copies only the freshly authorized observed facts and refuses a later lost grant", async () => {
  const facts = {
    reason: "OOMKilled",
    exitCode: 137,
    container: "gateway-api",
  };
  api.read.mockResolvedValueOnce(
    response(200, { data: { evidenceId: "e1", factSlice: facts } }),
  );
  render(<EvidenceCopyButton evidenceId="e1" />);
  fireEvent.click(screen.getByRole("button", { name: "复制证据内容" }));
  await waitFor(() =>
    expect(api.write).toHaveBeenCalledWith(JSON.stringify(facts, null, 2)),
  );
  expect(api.read).toHaveBeenCalledTimes(1);
  api.read.mockResolvedValueOnce(
    response(403, { code: "FORBIDDEN", message: "lost grant" }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: /已复制证据内容/ }),
  );
  await screen.findByText("没有此范围的访问权限");
  expect(api.read).toHaveBeenCalledTimes(2);
  expect(api.write).toHaveBeenCalledTimes(1);
});
it("drops the old evidence after switching identity even when its read finishes late", async () => {
  let finish!: (value: ReturnType<typeof response>) => void;
  api.read.mockReturnValueOnce(
    new Promise((resolve) => {
      finish = resolve;
    }),
  );
  const view = render(<EvidenceCopyButton evidenceId="old" />);
  fireEvent.click(screen.getByRole("button", { name: "复制证据内容" }));
  const signal = api.read.mock.calls[0][1].signal as AbortSignal;
  view.rerender(<EvidenceCopyButton evidenceId="new" />);
  expect(signal.aborted).toBe(true);
  await act(async () => {
    finish(
      response(200, {
        data: { evidenceId: "old", factSlice: "old private facts" },
      }),
    );
  });
  expect(api.write).not.toHaveBeenCalled();
});
it("refuses a different evidence identity returned by the server", async () => {
  api.read.mockResolvedValueOnce(
    response(200, { data: { evidenceId: "other", factSlice: "facts" } }),
  );
  render(<EvidenceCopyButton evidenceId="requested" />);
  fireEvent.click(screen.getByRole("button", { name: "复制证据内容" }));
  await screen.findByText(/证据身份或观测内容不完整/);
  expect(api.write).not.toHaveBeenCalled();
});

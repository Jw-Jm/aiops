import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { vi, it, expect } from "vitest";
import { IncidentStateActions } from "../src/components/IncidentStateActions";
import { transitionIncident } from "../src/api/generated/platform";
vi.mock("../src/api/generated/platform", () => ({
  transitionIncident: vi.fn(),
}));
vi.mock("../src/api/client", () => ({
  checked: async (p: Promise<unknown>) => p,
  requestOptions: (signal: AbortSignal) => ({ signal }),
}));
function view(state = "open", revision = 7) {
  const onChanged = vi.fn();
  const node = render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { mutations: { retry: false } } })
      }
    >
      <IncidentStateActions
        incidentId="incident"
        state={state}
        revision={revision}
        onChanged={onChanged}
      />
    </QueryClientProvider>,
  );
  return { ...node, onChanged };
}
it("requires a reason and sends the read revision before refreshing", async () => {
  vi.mocked(transitionIncident).mockResolvedValue({
    status: 200,
    data: {},
  } as never);
  const { onChanged } = view();
  expect(screen.getByRole("button", { name: "确认受理" })).toBeDisabled();
  fireEvent.change(screen.getByLabelText("处理说明"), {
    target: { value: "  确认真实 PVC 异常  " },
  });
  fireEvent.click(screen.getByRole("button", { name: "确认受理" }));
  await waitFor(() => expect(onChanged).toHaveBeenCalledOnce());
  expect(transitionIncident).toHaveBeenLastCalledWith(
    "incident",
    {
      expectedRevision: 7,
      targetState: "acknowledged",
      reason: "确认真实 PVC 异常",
    },
    expect.objectContaining({ signal: expect.any(AbortSignal) }),
  );
});
it("only offers closure after recovery", () => {
  view("resolved", 9);
  expect(
    screen.queryByRole("button", { name: "确认受理" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "开始处置" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "关闭故障" })).toBeDisabled();
});
it("aborts pending work on leaving the incident", async () => {
  vi.mocked(transitionIncident).mockImplementation(() => new Promise(() => {}));
  const { unmount } = view();
  fireEvent.change(screen.getByLabelText("处理说明"), {
    target: { value: "开始调查" },
  });
  fireEvent.click(screen.getByRole("button", { name: "开始处置" }));
  await waitFor(() =>
    expect(vi.mocked(transitionIncident).mock.calls.at(-1)?.[1]).toMatchObject({
      targetState: "mitigating",
    }),
  );
  const signal = vi.mocked(transitionIncident).mock.calls.at(-1)?.[2]?.signal;
  unmount();
  expect(signal?.aborted).toBe(true);
});
it("keeps actions blocked until the new revision has been read", async () => {
  vi.mocked(transitionIncident).mockResolvedValue({
    status: 200,
    data: {},
  } as never);
  let release = () => {};
  const refreshed = new Promise<void>((resolve) => {
    release = resolve;
  });
  const onChanged = vi.fn(() => refreshed);
  render(
    <QueryClientProvider client={new QueryClient()}>
      <IncidentStateActions
        incidentId="next"
        state="open"
        revision={4}
        onChanged={onChanged}
      />
    </QueryClientProvider>,
  );
  fireEvent.change(screen.getByLabelText("处理说明"), {
    target: { value: "确认受理" },
  });
  fireEvent.click(screen.getByRole("button", { name: "确认受理" }));
  await waitFor(() => expect(onChanged).toHaveBeenCalledOnce());
  fireEvent.change(screen.getByLabelText("处理说明"), {
    target: { value: "立即开始处置" },
  });
  expect(screen.getByRole("button", { name: "开始处置" })).toBeDisabled();
  release();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "开始处置" })).toBeEnabled(),
  );
});

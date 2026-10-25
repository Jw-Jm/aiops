// The fault-chain children answer for the scope they were mounted for. A late
// answer of an older scope must never reach the page: the request is cancelled
// on unmount, the controller of the running request is captured before the call,
// and the page re-checks the epoch of the submission, the live scope and the
// live conversation before the answer may become the current turn.
import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useRef, useState } from "react";
import { FaultChainGapCheck } from "../src/features/assistant/FaultChainGapCheck";
import { FaultChainCollector } from "../src/features/assistant/FaultChainCollector";
import { setCredentials } from "../src/api/client";
import type {
  AssistantConversationV1,
  AssistantFaultChainContextV1,
} from "../src/api/generated/models";

vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: { tenantId: "tenant", subject: "owner", roles: ["operator"] },
  }),
}));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const window = {
  from: "2026-10-14T00:00:00Z",
  to: "2026-10-14T00:01:00Z",
  timezone: "UTC",
};
const digest = "sha256:" + "1".repeat(64);
const context: AssistantFaultChainContextV1 = {
  schemaVersion: "fault-chain-context/v1",
  ruleVersion: "fault-chain-rules/v1",
  asOf: window.to,
  status: "partial",
  request: {
    schemaVersion: "fault-chain-request/v1",
    targetCanonicalId: "target",
    fault: window,
    baseline: { ...window, from: "2026-10-13T23:58:00Z", to: "2026-10-13T23:59:00Z" },
    maxEdges: 4,
  },
  edges: [
    {
      relation: {
        from: "target",
        to: "peer",
        kind: "communicates_with",
        observedAt: window.from,
        validFrom: window.from,
        ttl: 120,
        confidence: 1,
        provenance: {
          sourceRegistrationId: "fixture",
          ruleVersion: "fixture/v1",
          sourceValues: null,
          observedAt: window.from,
        },
      },
      relationDigest: digest,
      callDirection: "incoming",
      temporalAssessment: "unreliable",
      comparisonStatus: "baseline_missing",
      queryResultRefs: [],
      observations: [],
      missingEvidence: ["network_window_statistics"],
      nextChecks: [],
    },
  ],
  queryResultRefs: [],
  evidenceRefs: [],
  missingEvidence: [],
};
const conversation = {
  conversationId: "conversation-a",
  revision: 7,
  binding: { executionScope: { namespaces: ["apps"] } },
} as unknown as AssistantConversationV1;

function response(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

it("drops a readonly check that answers after the conversation was replaced", async () => {
  setCredentials("FIXTURE", "tenant");
  let finish!: (r: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    finish = resolve;
  });
  const fetcher = vi.fn(() => pending);
  vi.stubGlobal("fetch", fetcher);
  const submitted = vi.fn();
  function Host() {
    const [cid, setCid] = useState("conversation-a");
    const [turn, setTurn] = useState("");
    // The page epoch advances with the scope, exactly like the Assistant page.
    const [epoch, setEpoch] = useState(1);
    return (
      <>
        <button
          onClick={() => {
            setCid("conversation-b");
            setTurn("");
            setEpoch((value) => value + 1);
          }}
        >
          next-conversation
        </button>
        <span data-testid="active-conversation">{cid}</span>
        <span data-testid="active-turn">{turn}</span>
        {cid === "conversation-a" && (
          <FaultChainGapCheck
            context={context}
            edge={context.edges[0]}
            gap="network_window_statistics"
            turnId="old-turn"
            controls={{
              conversation: { ...conversation, conversationId: cid },
              disabled: false,
              epoch,
              onSubmitted: (next, submittedEpoch) => {
                submitted(next, submittedEpoch);
                if (submittedEpoch === epoch) setTurn(next.turnId);
              },
            }}
          />
        )}
      </>
    );
  }
  render(<Host />);
  fireEvent.click(
    screen.getByRole("button", { name: "执行冻结窗口统计检查" }),
  );
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "next-conversation" }));
  await act(async () => {
    finish(
      response(
        { data: { turnId: "turn-from-a", conversationId: "conversation-a", state: "queued" } },
        202,
      ),
    );
  });
  expect(screen.getByTestId("active-conversation")).toHaveTextContent(
    "conversation-b",
  );
  expect(screen.getByTestId("active-turn")).not.toHaveTextContent("turn-from-a");
  expect(submitted).not.toHaveBeenCalled();
});

it("drops a readonly check of an older epoch when the same conversation is shown again", async () => {
  setCredentials("FIXTURE", "tenant");
  let finish!: (r: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    finish = resolve;
  });
  const fetcher = vi.fn(() => pending);
  vi.stubGlobal("fetch", fetcher);
  const submitted = vi.fn();
  function Host() {
    // A -> B -> A: the same conversation id comes back, but the epoch moved on.
    // The page keeps the epoch in a ref, so the comparison uses the live value.
    const [phase, setPhase] = useState<"a" | "b" | "a2">("a");
    const [turn, setTurn] = useState("");
    const epoch = useRef(1);
    epoch.current = phase === "a" ? 1 : phase === "b" ? 2 : 3;
    return (
      <>
        <button onClick={() => setPhase("b")}>to-b</button>
        <button onClick={() => setPhase("a2")}>to-a-again</button>
        <span data-testid="phase">{phase}</span>
        <span data-testid="active-turn">{turn}</span>
        <FaultChainGapCheck
          context={context}
          edge={context.edges[0]}
          gap="network_window_statistics"
          turnId="old-turn"
          controls={{
            conversation: { ...conversation, conversationId: "conversation-a" },
            disabled: false,
            epoch: epoch.current,
            onSubmitted: (next, submittedEpoch) => {
              submitted(next, submittedEpoch);
              if (submittedEpoch === epoch.current) setTurn(next.turnId);
            },
          }}
        />
      </>
    );
  }
  render(<Host />);
  fireEvent.click(
    screen.getByRole("button", { name: "执行冻结窗口统计检查" }),
  );
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "to-b" }));
  fireEvent.click(screen.getByRole("button", { name: "to-a-again" }));
  await act(async () => {
    finish(
      response(
        { data: { turnId: "turn-from-a", conversationId: "conversation-a", state: "queued" } },
        202,
      ),
    );
  });
  expect(screen.getByTestId("phase")).toHaveTextContent("a2");
  expect(screen.getByTestId("active-turn")).not.toHaveTextContent("turn-from-a");
  expect(submitted).not.toHaveBeenCalled();
});

it("does not show the failure of a request whose scope has moved on", async () => {
  setCredentials("FIXTURE", "tenant");
  let failFirst!: (reason: unknown) => void;
  const first = new Promise<Response>((_resolve, reject) => {
    failFirst = reject;
  });
  const fetcher = vi.fn().mockReturnValueOnce(first);
  vi.stubGlobal("fetch", fetcher);
  const submitted = vi.fn();
  const view = render(
    <FaultChainGapCheck
      context={context}
      edge={context.edges[0]}
      gap="network_window_statistics"
      turnId="old-turn"
      controls={{
        conversation,
        disabled: false,
        epoch: 1,
        onSubmitted: submitted,
      }}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "执行冻结窗口统计检查" }),
  );
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  // The page scope moves on while the request is still running. The failure of
  // the old request must not appear on the current state.
  view.rerender(
    <FaultChainGapCheck
      context={context}
      edge={context.edges[0]}
      gap="network_window_statistics"
      turnId="old-turn"
      controls={{
        conversation,
        disabled: false,
        epoch: 2,
        onSubmitted: submitted,
      }}
    />,
  );
  await act(async () => {
    failFirst(new Error("旧请求失败"));
    await Promise.resolve();
  });
  expect(screen.queryByText("旧请求失败")).toBeNull();
  expect(submitted).not.toHaveBeenCalled();
});

it("drops a fault-chain collection that answers after the collector was unmounted", async () => {
  setCredentials("FIXTURE", "tenant");
  let finish!: (r: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    finish = resolve;
  });
  const fetcher = vi.fn((url: string) =>
    url.includes("resolve")
      ? Promise.resolve(
          response({
            data: {
              candidates: [
                {
                  canonicalId: "k8s+v1://tenant/cluster/core/Pod/pod-1",
                  namespace: "apps",
                  name: "pod-1",
                },
              ],
            },
          }),
        )
      : pending,
  );
  vi.stubGlobal("fetch", fetcher);
  const submitted = vi.fn();
  function Host() {
    const [mounted, setMounted] = useState(true);
    return (
      <>
        <button onClick={() => setMounted(false)}>unmount</button>
        {mounted && (
          <FaultChainCollector
            conversation={conversation}
            disabled={false}
            epoch={1}
            onSubmitted={submitted}
          />
        )}
      </>
    );
  }
  render(<Host />);
  fireEvent.change(screen.getByLabelText("调查资源名称"), {
    target: { value: "pod-1" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "查找当前授权范围的资源" }),
  );
  fireEvent.mouseDown(screen.getByRole("combobox", { name: "调查目标资源" }));
  fireEvent.click(await screen.findByText("apps/pod-1"));
  fireEvent.click(screen.getByText("收集故障链证据"));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some((call) =>
        String(call[0]).includes("fault-chain-investigations"),
      ),
    ).toBe(true),
  );
  fireEvent.click(screen.getByRole("button", { name: "unmount" }));
  await act(async () => {
    finish(
      response(
        { data: { turnId: "turn-from-a", conversationId: "conversation-a", state: "queued" } },
        202,
      ),
    );
  });
  expect(submitted).not.toHaveBeenCalled();
});

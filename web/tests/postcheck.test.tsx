import { it, expect, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { PostCheckPanel } from "../src/features/action/PostCheckPanel";
import { setCredentials } from "../src/api/client";
it("distinguishes pending checks from final verdicts and stops polling final results", async () => {
  setCredentials("TEST-FIXTURE");
  for (const [status, verdict, label] of [
    ["pending", "inconclusive", "等待检查"],
    ["completed", "not_resolved", "未确认修复"],
    ["completed", "resolved", "已恢复"],
    ["completed", "inconclusive", "尚无定论"],
  ]) {
    const fetcher = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            data: {
              executionId: "test",
              status,
              verdict,
              checkedAt: status === "completed" ? "2026-10-11T00:00:00Z" : null,
            },
            requestId: "fixture-request",
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        ),
    );
    vi.stubGlobal("fetch", fetcher);
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <PostCheckPanel executionId="test" />
      </QueryClientProvider>,
    );
    await waitFor(() =>
      expect(screen.getAllByText(label).length).toBeGreaterThan(0),
    );
    expect(fetcher).toHaveBeenCalledTimes(1);
    cleanup();
    client.clear();
  }
  vi.unstubAllGlobals();
});

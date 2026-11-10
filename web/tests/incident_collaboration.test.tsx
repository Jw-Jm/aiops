import { it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { IncidentCollaboration } from "../src/features/collab/IncidentCollaboration";
import {
  setCredentials,
  clearCredentials,
  checked,
  ApiError,
} from "../src/api/client";
import { listIncidentFollowUps } from "../src/api/collab";

afterEach(() => {
  clearCredentials();
  vi.unstubAllGlobals();
});
it("renders persisted comment and impact, then completes the actual listed follow-up and reloads it", async () => {
  setCredentials("TEST-FIXTURE");
  let done = false;
  const requests: { path: string; method: string; body: unknown }[] = [];
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "https://ops.invalid").pathname;
      requests.push({
        path,
        method: init?.method || "GET",
        body: init?.body ? JSON.parse(String(init.body)) : null,
      });
      if (path.endsWith(":complete")) {
        done = true;
        return new Response(JSON.stringify({ data: { state: "done" } }), {
          status: 200,
        });
      }
      return new Response(
        JSON.stringify({
          data: path.endsWith("/timeline")
            ? [
                {
                  timelineId: "timeline",
                  actor: "operator",
                  createdAt: "2026-11-10T00:00:00Z",
                  payload: {
                    text: "Persisted handling note",
                    impact: "Customer impact reviewed",
                  },
                },
              ]
            : [
                {
                  followUpId: "task",
                  title: "Persisted follow-up",
                  state: done ? "done" : "open",
                  ownerSubject: "operator",
                  creatorSubject: "operator",
                },
              ],
        }),
        { status: 200 },
      );
    },
  );
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <IncidentCollaboration
        incidentId="incident"
        state="resolved"
        revision={6}
        assigneeSubject="operator"
        currentSubject="operator"
        isAdmin={false}
        onChanged={() => {}}
      />
    </QueryClientProvider>,
  );
  expect(
    await screen.findByText("Persisted handling note"),
  ).toBeInTheDocument();
  expect(screen.getByText(/Customer impact reviewed/)).toBeInTheDocument();
  expect(await screen.findByText("Persisted follow-up")).toBeInTheDocument();
  fireEvent.change(screen.getByRole("textbox", { name: "完成说明" }), {
    target: { value: "Verified and archived" },
  });
  fireEvent.click(screen.getByRole("button", { name: /^完\s*成$/ }));
  await screen.findByRole("button", { name: "重新打开" });
  expect(requests.filter((x) => x.method === "POST")).toEqual([
    {
      path: "/api/v1/incidents/incident/follow-ups/task:complete",
      method: "POST",
      body: { note: "Verified and archived" },
    },
  ]);
  client.clear();
});
it("preserves status and API error category for collaboration permission failures", async () => {
  setCredentials("TEST-FIXTURE");
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({ code: "FORBIDDEN", message: "scope denied" }),
          { status: 403 },
        ),
    ),
  );
  await expect(
    checked(listIncidentFollowUps("incident"), false),
  ).rejects.toMatchObject({ status: 403, code: "FORBIDDEN" });
});

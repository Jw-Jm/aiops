import { expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { MemoryRouter, useLocation, useNavigate } from "react-router-dom";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  LayerProvider,
  V12Drawer,
  V12Modal,
} from "../../src/components/v12/Layer";
import {
  ClusterScopeProvider,
  useClusterDraft,
  useClusterScope,
} from "../../src/app/ClusterContext";
import { NAV, NAV_ITEMS, navGroup, navItem, scopedHref } from "../../src/app/navigation";
import { legacyRedirectTarget } from "../../src/app/router";
import type { AuthorizedClusterV1 } from "../../src/api/generated/models";

const clusters: AuthorizedClusterV1[] = [
  { clusterId: "id-a", clusterUid: "uid-a", displayName: "集群 A" },
  { clusterId: "id-b", clusterUid: "uid-b", displayName: "集群 B" },
];

it("matches the frozen source NAV hierarchy without flattening modules", () => {
  const source = readFileSync(
    resolve(
      process.cwd(),
      "../../ui-handoff/ops-platform-v1.2-20261020/source/prototype.html",
    ),
    "utf8",
  );
  const block = source.slice(
    source.indexOf("const NAV = ["),
    source.indexOf("function navActive"),
  );
  const sourcePaths = new Set(
    [...block.matchAll(/"(\/[^"]+)"/g)].map((match) => match[1]),
  );
  expect(new Set(NAV_ITEMS.map((item) => item.path))).toEqual(sourcePaths);
  expect(NAV.map((group) => group.label)).toEqual([
    "工作空间",
    "运行与处置",
    "经验沉淀",
    "平台配置",
  ]);
  expect(NAV[1].items.map((item) => item.name)).toEqual([
    "资源管理",
    "监控与观测",
    "事件响应",
    "运维作业",
  ]);
  expect(NAV[2].items[0].children?.map((item) => item.path)).toEqual([
    "/knowledge/cases",
    "/knowledge/runbooks",
  ]);
  expect(NAV.at(-1)?.role).toBe("platform_admin");
  expect(NAV.filter((group) => group.role === "operator")).toHaveLength(3);
  expect(navItem("/events/incidents/INC-1")?.path).toBe("/events/incidents");
  expect(navItem("/resources/topology")?.path).toBe("/resources/topology");
  expect(navGroup("/settings/audit")?.label).toBe("平台配置");
  expect(scopedHref("/events/alerts", "uid-b")).toBe(
    "/events/alerts?clusterUid=uid-b",
  );
});

it("preserves old object parameters when redirecting to canonical routes", () => {
  expect(legacyRedirectTarget("/findings", "?clusterUid=uid-a&state=firing")).toEqual({
    pathname: "/events/alerts",
    search: "?clusterUid=uid-a&state=firing",
  });
  expect(
    legacyRedirectTarget("/incidents/INC-2096", "?clusterUid=uid-b", "INC-2096"),
  ).toEqual({
    pathname: "/events/incidents/INC-2096",
    search: "?clusterUid=uid-b",
  });
  expect(legacyRedirectTarget("/admin/audit", "?cursor=1")).toEqual({
    pathname: "/settings/audit",
    search: "?cursor=1",
  });
  expect(legacyRedirectTarget("/capabilities", "?clusterUid=uid-a")).toEqual({
    pathname: "/capabilities",
    search: "?clusterUid=uid-a",
  });
});

function Layers({
  onBottom,
  onTop,
}: {
  onBottom: () => void;
  onTop: () => void;
}) {
  return (
    <>
      <V12Modal open title="底层抽屉" onClose={onBottom}>
        底层内容
      </V12Modal>
      <V12Modal open title="顶层确认" onClose={onTop}>
        顶层内容
      </V12Modal>
    </>
  );
}

it("closes only the top nested layer on Escape", async () => {
  const onBottom = vi.fn();
  const onTop = vi.fn();
  render(
    <LayerProvider>
      <Layers onBottom={onBottom} onTop={onTop} />
    </LayerProvider>,
  );
  expect(await screen.findByText("顶层内容")).toBeInTheDocument();
  fireEvent.keyDown(window, { key: "Escape" });
  expect(onTop).toHaveBeenCalledTimes(1);
  expect(onBottom).not.toHaveBeenCalled();
});

function ScopeConsumer() {
  const scope = useClusterScope();
  const [dirty, setDirty] = useState(false);
  useClusterDraft(dirty);
  const [result, setResult] = useState("");
  return (
    <div>
      <span data-testid="cluster">{scope.clusterUid || "none"}</span>
      <span data-testid="invalid">{String(scope.invalid)}</span>
      <span data-testid="pending">{scope.pendingSwitch || ""}</span>
      <span data-testid="result">{result}</span>
      <button onClick={() => setDirty(true)}>dirty</button>
      <button
        onClick={() => setResult(scope.requestCluster("uid-b"))}
      >
        switch-b
      </button>
      <button
        onClick={() => setResult(scope.requestCluster("uid-missing"))}
      >
        switch-missing
      </button>
      <button onClick={scope.cancelPending}>cancel</button>
    </div>
  );
}

it("keeps the URL-selected cluster instead of resetting to the first authorized cluster", () => {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={["/overview?clusterUid=uid-b"]}>
        <ClusterScopeProvider clusters={clusters}>
          <ScopeConsumer />
        </ClusterScopeProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(screen.getByTestId("cluster")).toHaveTextContent("uid-b");
});

function ConfigConsumer() {
  const scope = useClusterScope();
  return (
    <div>
      <span data-testid="operational">{scope.clusterUid}</span>
      <span data-testid="config">{scope.configClusterUid || "none"}</span>
      <span data-testid="config-invalid">{String(scope.configInvalid)}</span>
      <button onClick={() => scope.requestConfigCluster("uid-c")}>
        config-c
      </button>
    </div>
  );
}

it("keeps administrator config candidates separate and does not fall back to operator clusters", () => {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={["/access/sources?configClusterUid=uid-x"]}>
        <ClusterScopeProvider
          clusters={clusters}
          configCandidates={[
            { clusterId: "id-c", clusterUid: "uid-c", displayName: "注册 C" },
          ]}
        >
          <ConfigConsumer />
        </ClusterScopeProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(screen.getByTestId("config")).toHaveTextContent("none");
  expect(screen.getByTestId("config-invalid")).toHaveTextContent("true");
  expect(screen.getByTestId("operational")).toHaveTextContent("uid-a");
  fireEvent.click(screen.getByText("config-c"));
  expect(screen.getByTestId("config")).toHaveTextContent("uid-c");
});

function FocusLayer() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        onClick={(event) => {
          event.currentTarget.focus();
          setOpen(true);
        }}
      >
        open layer
      </button>
      <V12Modal open={open} title="焦点层" onClose={() => setOpen(false)}>
        <button onClick={() => setOpen(false)}>close layer</button>
      </V12Modal>
    </>
  );
}

it("blocks a dirty selector, stays blocked without provider navigation, and never falls back on unknown uid", async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  client.setQueryData(["old-scope"], { private: true });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/overview?clusterUid=uid-a"]}>
        <ClusterScopeProvider clusters={clusters}>
          <ScopeConsumer />
        </ClusterScopeProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(screen.getByTestId("cluster")).toHaveTextContent("uid-a");
  fireEvent.click(screen.getByText("dirty"));
  fireEvent.click(screen.getByText("switch-b"));
  expect(screen.getByTestId("result")).toHaveTextContent("pending");
  expect(screen.getByTestId("cluster")).toHaveTextContent("uid-a");
  fireEvent.click(screen.getByText("cancel"));
  expect(screen.getByTestId("cluster")).toHaveTextContent("uid-a");
  expect(screen.getByTestId("pending")).toHaveTextContent("");
  // Confirmation is exercised by the real AppShell router-blocker test.
  fireEvent.click(screen.getByText("switch-missing"));
  expect(screen.getByTestId("result")).toHaveTextContent("invalid");
  expect(screen.getByTestId("invalid")).toHaveTextContent("true");
  expect(screen.getByTestId("cluster")).toHaveTextContent("uid-a");
});

function HistoryProbe() {
  const scope = useClusterScope();
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <div>
      <span data-testid="cluster">{scope.clusterUid}</span>
      <span data-testid="search">{location.search}</span>
      <button
        onClick={() =>
          navigate("/overview?clusterUid=uid-b&canonicalId=old-object&cursor=old")
        }
      >
        go-b
      </button>
    </div>
  );
}

it("applies URL/history cluster changes while preserving the explicitly bound object", async () => {
  const client = new QueryClient();
  client.setQueryData(["old"], { old: true });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/overview?clusterUid=uid-a&canonicalId=old"]}>
        <ClusterScopeProvider clusters={clusters}>
          <HistoryProbe />
        </ClusterScopeProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByText("go-b"));
  await waitFor(() =>
    expect(screen.getByTestId("cluster")).toHaveTextContent("uid-b"),
  );
  await waitFor(() =>
    expect(screen.getByTestId("search")).toHaveTextContent(
      "clusterUid=uid-b",
    ),
  );
  expect(screen.getByTestId("search")).toHaveTextContent("canonicalId=old-object");
  expect(screen.getByTestId("search")).toHaveTextContent("cursor=old");
  expect(client.getQueryData(["old"])).toBeUndefined();
});

it("clears the active scope when authorization for that cluster is revoked", async () => {
  const client = new QueryClient();
  client.setQueryData(["private"], { old: true });
  const { rerender } = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/overview?clusterUid=uid-a"]}>
        <ClusterScopeProvider clusters={clusters}>
          <ScopeConsumer />
        </ClusterScopeProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(screen.getByTestId("cluster")).toHaveTextContent("uid-a");
  rerender(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/overview?clusterUid=uid-a"]}>
        <ClusterScopeProvider clusters={[clusters[1]]}>
          <ScopeConsumer />
        </ClusterScopeProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await waitFor(() =>
    expect(screen.getByTestId("cluster")).toHaveTextContent("none"),
  );
  expect(screen.getByTestId("invalid")).toHaveTextContent("true");
  expect(client.getQueryData(["private"])).toBeUndefined();
});

it("keeps an initially unknown uid unavailable without generation-zero recovery", async () => {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter
        initialEntries={["/overview?clusterUid=unknown&canonicalId=object"]}
      >
        <ClusterScopeProvider clusters={clusters}>
          <ScopeConsumer />
        </ClusterScopeProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await waitFor(() =>
    expect(screen.getByTestId("invalid")).toHaveTextContent("true"),
  );
  expect(screen.getByTestId("cluster")).toHaveTextContent("none");
  expect(screen.getByTestId("pending")).toHaveTextContent("");
});

it("stays unavailable after revoking active A even when B remains authorized", async () => {
  const client = new QueryClient();
  client.setQueryData(["private"], { old: true });
  const { rerender } = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/overview?clusterUid=uid-a"]}>
        <ClusterScopeProvider clusters={clusters}>
          <ScopeConsumer />
        </ClusterScopeProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(screen.getByTestId("cluster")).toHaveTextContent("uid-a");
  rerender(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/overview?clusterUid=uid-a"]}>
        <ClusterScopeProvider clusters={[clusters[1]]}>
          <ScopeConsumer />
        </ClusterScopeProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await waitFor(() =>
    expect(screen.getByTestId("invalid")).toHaveTextContent("true"),
  );
  expect(screen.getByTestId("cluster")).toHaveTextContent("none");
  expect(client.getQueryData(["private"])).toBeUndefined();
});

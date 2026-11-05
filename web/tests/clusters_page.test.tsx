// UI mechanism fixture; no database, browser visual or live authorization claim.
import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ClustersPage } from "../src/pages/admin/ClustersPage";
import { getClusters, createCluster } from "../src/api/generated/platform";
import { setNativeCredentials } from "../src/api/client";

vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({
    authMode: "native",
    nativeSession: {
      sessionRef: "session:fixture",
      revocation: { sessionVersion: 1 },
      csrf: { token: "fixture-csrf-key-0001" },
    },
  }),
}));
vi.mock("../src/auth/NativeStepUpControl", () => ({
  NativeStepUpControl: ({
    onReady,
  }: {
    onReady: (expires: string, id: string) => void;
  }) => (
    <button onClick={() => onReady("2099-01-01T00:00:00Z", "fixture-proof")}>
      fixture verification
    </button>
  ),
}));
vi.mock("../src/api/generated/platform", async (original) => ({
  ...(await original<object>()),
  getClusters: vi.fn(),
  createCluster: vi.fn(),
}));

const cluster = {
  clusterId: "019a0000-0000-7000-8000-0000000000c1",
  clusterUid: "fixture-cluster",
  displayName: "生产集群",
};

beforeEach(() => {
  vi.clearAllMocks();
  setNativeCredentials("fixture-tenant", "fixture-csrf-key-0001");
  vi.mocked(getClusters).mockResolvedValue({
    status: 200,
    headers: new Headers(),
    data: { data: [cluster], meta: { nextCursor: null }, requestId: "unit" },
  } as never);
  vi.mocked(createCluster).mockResolvedValue({
    status: 201,
    headers: new Headers(),
    data: { data: {}, requestId: "unit" },
  } as never);
});

function renderPage() {
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <ClustersPage />
    </QueryClientProvider>,
  );
}

it("lists registered clusters", async () => {
  renderPage();
  expect(await screen.findByText("生产集群")).toBeInTheDocument();
  expect(screen.getByText("fixture-cluster")).toBeInTheDocument();
});

it("registers a cluster after step-up verification", async () => {
  renderPage();
  fireEvent.change(await screen.findByLabelText("集群 UID"), {
    target: { value: "edge-01" },
  });
  fireEvent.change(screen.getByLabelText("显示名称"), {
    target: { value: "边缘集群" },
  });
  fireEvent.change(screen.getByLabelText("发行版"), {
    target: { value: "kubeadm" },
  });
  fireEvent.change(screen.getByLabelText("API 凭据引用"), {
    target: { value: "openbao://clusters/edge-01/api" },
  });
  fireEvent.change(screen.getByLabelText("Kubernetes 版本"), {
    target: { value: "v1.31.2" },
  });
  const submit = screen.getByRole("button", { name: "登记集群" });
  expect(submit).toBeDisabled();
  fireEvent.click(screen.getByText("fixture verification"));
  await waitFor(() => expect(submit).not.toBeDisabled());
  fireEvent.click(submit);
  await waitFor(() =>
    expect(createCluster).toHaveBeenCalledWith(
      {
        clusterUid: "edge-01",
        displayName: "边缘集群",
        distribution: "kubeadm",
        apiEndpointRef: "openbao://clusters/edge-01/api",
        actualVersions: { kubernetes: "v1.31.2" },
        capabilities: { kubernetes: true },
      },
      expect.anything(),
    ),
  );
});

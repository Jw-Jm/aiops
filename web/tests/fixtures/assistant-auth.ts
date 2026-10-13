// Transport/browser fixture only; production never imports this module.
export const useAuth = () => ({
  session: {
    tenantId: "fixture-tenant",
    subject: "fixture-owner",
    roles:
      new URLSearchParams(location.search).get("qaRole") === "admin"
        ? ["platform_admin"]
        : ["operator"],
    clusterScopes: ["fixture-cluster-id"],
    namespaceScopes: [{ clusterId: "fixture-cluster-id", namespace: "apps" }],
    authorizedClusters: [
      {
        clusterId: "fixture-cluster-id",
        clusterUid: "fixture-cluster",
        name: "Fixture cluster",
      },
    ],
  },
  user: {},
  loading: false,
  error: null,
});

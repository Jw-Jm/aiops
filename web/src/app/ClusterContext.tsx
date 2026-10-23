import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import type { AuthorizedClusterV1 } from "../api/generated/models";
import { useAuth } from "../auth/AuthProvider";

export type ClusterDraftGuard = () => boolean;
export type ScopeTarget = {
  pathname: string;
  search: string;
  hash: string;
};
type SwitchResult = "applied" | "invalid" | "pending";

export function resolveInitialCluster(
  clusters: AuthorizedClusterV1[],
  requested: string | null,
): { clusterUid: string; invalid: boolean } {
  if (requested) {
    return clusters.some((c) => c.clusterUid === requested)
      ? { clusterUid: requested, invalid: false }
      : { clusterUid: "", invalid: true };
  }
  return { clusterUid: clusters[0]?.clusterUid ?? "", invalid: false };
}

type ClusterScope = {
  clusterUid: string;
  candidates: AuthorizedClusterV1[];
  invalid: boolean;
  loading: boolean;
  generation: number;
  scopeKey: string;
  pendingSwitch: string | null;
  pendingTarget: ScopeTarget | null;
  urlTransition: boolean;
  urlUnauthorizedTransition: boolean;
  requestCluster: (clusterUid: string) => SwitchResult;
  requestUrlNavigation: (target: ScopeTarget) => boolean;
  cancelPending: () => void;
  registerDraft: (guard: ClusterDraftGuard) => () => void;
  configClusterUid: string;
  configCandidates: AuthorizedClusterV1[];
  configInvalid: boolean;
  configLoading: boolean;
  configError: Error | null;
  configGeneration: number;
  pendingConfig: string | null;
  requestConfigCluster: (clusterUid: string) => SwitchResult;
  confirmConfigPending: () => void;
  cancelConfigPending: () => void;
  registerConfigDraft: (guard: ClusterDraftGuard) => () => void;
};

const Context = createContext<ClusterScope | null>(null);

function scopeUrl(
  params: URLSearchParams,
  key: "clusterUid" | "configClusterUid",
  value: string,
) {
  // Object-bound parameters (canonicalId, conversation, incident, namespace,
  // cursor, etc.) belong to the previous scope and must not survive a switch.
  const next = new URLSearchParams();
  const other = key === "clusterUid" ? "configClusterUid" : "clusterUid";
  const otherValue = params.get(other);
  if (otherValue) next.set(other, otherValue);
  next.set(key, value);
  return next;
}

export function ClusterScopeProvider({
  clusters,
  configCandidates = [],
  configLoading = false,
  configError = null,
  loading = false,
  children,
}: {
  clusters: AuthorizedClusterV1[];
  configCandidates?: AuthorizedClusterV1[];
  configLoading?: boolean;
  configError?: Error | null;
  loading?: boolean;
  children: ReactNode;
}) {
  const [params] = useSearchParams();
  const location = useLocation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const requested = params.get("clusterUid");
  const initial = useMemo(
    () => resolveInitialCluster(clusters, requested),
    [clusters, requested],
  );
  const [clusterUid, setClusterUid] = useState(initial.clusterUid);
  const [invalid, setInvalid] = useState(initial.invalid);
  const [pendingSwitch, setPendingSwitch] = useState<string | null>(null);
  const [generation, setGeneration] = useState(0);
  const drafts = useRef(new Set<ClusterDraftGuard>());
  const [pendingTarget, setPendingTarget] = useState<ScopeTarget | null>(null);
  const appliedUidRef = useRef<string | null>(null);

  const requestedConfig = params.get("configClusterUid");
  const configInitial = useMemo(
    () => resolveInitialCluster(configCandidates, requestedConfig),
    [configCandidates, requestedConfig],
  );
  const [configClusterUid, setConfigClusterUid] = useState(
    configInitial.clusterUid,
  );
  const [configInvalid, setConfigInvalid] = useState(configInitial.invalid);
  const [pendingConfig, setPendingConfig] = useState<string | null>(null);
  const [configGeneration, setConfigGeneration] = useState(0);
  const configAutoSelected = useRef(false);
  const configDrafts = useRef(new Set<ClusterDraftGuard>());

  const registerDraft = useCallback((guard: ClusterDraftGuard) => {
    drafts.current.add(guard);
    return () => drafts.current.delete(guard);
  }, []);
  const registerConfigDraft = useCallback((guard: ClusterDraftGuard) => {
    configDrafts.current.add(guard);
    return () => configDrafts.current.delete(guard);
  }, []);

  const clearData = useCallback(() => {
    void queryClient.cancelQueries();
    queryClient.removeQueries();
  }, [queryClient]);

  const applyLocation = useCallback(
    (next: string, nextParams: URLSearchParams) => {
      if (!clusters.some((c) => c.clusterUid === next)) {
        setInvalid(true);
        return false;
      }
      clearData();
      setGeneration((value) => value + 1);
      setClusterUid(next);
      setInvalid(false);
      appliedUidRef.current = next;
      navigate(
        {
          pathname: location.pathname,
          search: nextParams.toString(),
          hash: location.hash,
        },
        { replace: true },
      );
      return true;
    },
    [clearData, clusters, location, navigate],
  );

  // Selector changes deliberately discard object-bound parameters. A validated
  // URL/history navigation keeps the newly committed full location.
  const apply = useCallback(
    (next: string) =>
      applyLocation(next, scopeUrl(params, "clusterUid", next)),
    [applyLocation, params],
  );

  const requestUrlNavigation = useCallback(
    (target: ScopeTarget) => {
      const requestedUid = new URLSearchParams(target.search).get("clusterUid");
      if (requestedUid === clusterUid) return false;
      if (
        requestedUid &&
        clusters.some((cluster) => cluster.clusterUid === requestedUid) &&
        [...drafts.current].some((guard) => guard())
      ) {
        setPendingSwitch(requestedUid);
        setPendingTarget(target);
        return true;
      }
      // Valid or explicitly unauthorized deep links continue. The provider and
      // shell gate children so an old scope cannot read the new object.
      return false;
    },
    [clusterUid, clusters, drafts],
  );

  const requestCluster = useCallback(
    (next: string) => {
      if (!clusters.some((c) => c.clusterUid === next)) {
        setInvalid(true);
        return "invalid" as const;
      }
      if (next === clusterUid) return "applied" as const;
      if ([...drafts.current].some((guard) => guard())) {
        setPendingSwitch(next);
        setPendingTarget({
          pathname: location.pathname,
          search: scopeUrl(params, "clusterUid", next).toString()
            ? `?${scopeUrl(params, "clusterUid", next).toString()}`
            : "",
          hash: location.hash,
        });
        return "pending" as const;
      }
      apply(next);
      return "applied" as const;
    },
    [apply, clusterUid, clusters],
  );

  const cancelPending = useCallback(() => {
    setPendingSwitch(null);
    setPendingTarget(null);
  }, []);

  const applyConfig = useCallback(
    (next: string) => {
      if (!configCandidates.some((c) => c.clusterUid === next)) {
        setConfigInvalid(true);
        return false;
      }
      clearData();
      setConfigGeneration((value) => value + 1);
      setConfigClusterUid(next);
      setConfigInvalid(false);
      navigate(
        {
          pathname: location.pathname,
          search: scopeUrl(params, "configClusterUid", next).toString(),
          hash: location.hash,
        },
        { replace: true },
      );
      return true;
    },
    [clearData, configCandidates, location, navigate, params],
  );
  const requestConfigCluster = useCallback(
    (next: string) => {
      if (!configCandidates.some((c) => c.clusterUid === next)) {
        setConfigInvalid(true);
        return "invalid" as const;
      }
      if (next === configClusterUid) return "applied" as const;
      if ([...configDrafts.current].some((guard) => guard())) {
        setPendingConfig(next);
        return "pending" as const;
      }
      applyConfig(next);
      return "applied" as const;
    },
    [applyConfig, configClusterUid, configCandidates],
  );
  const confirmConfigPending = useCallback(() => {
    if (pendingConfig && applyConfig(pendingConfig)) setPendingConfig(null);
  }, [applyConfig, pendingConfig]);
  const cancelConfigPending = useCallback(() => setPendingConfig(null), []);

  // First authorized load, and no saved URL: select the first real scope.
  useEffect(() => {
    if (loading || requested || clusterUid || invalid || !clusters.length || generation !== 0) return;
    setClusterUid(clusters[0].clusterUid);
    setInvalid(false);
  }, [clusters, clusterUid, generation, invalid, loading, requested]);

  // A confirmed navigation has reached its requested scope.
  useLayoutEffect(() => {
    if (pendingSwitch && requested === pendingSwitch) {
      setPendingSwitch(null);
      setPendingTarget(null);
    }
  }, [pendingSwitch, requested]);

  // Reconcile before passive child effects. Validated URL navigation keeps the
  // new object location; an unauthorized object transitions to no scope before
  // passive child effects can read it.
  useLayoutEffect(() => {
    if (loading) return;
    if (!requested) return;
    if (requested === clusterUid) {
      appliedUidRef.current = null;
      return;
    }
    if (appliedUidRef.current === clusterUid) return;
    if (!clusters.some((c) => c.clusterUid === requested)) {
      clearData();
      setClusterUid("");
      setInvalid(true);
      return;
    }
    applyLocation(requested, params);
  }, [
    applyLocation,
    clearData,
    clusterUid,
    clusters,
    loading,
    params,
    requested,
  ]);

  useEffect(() => {
    if (loading || configLoading || !requestedConfig) return;
    if (requestedConfig === configClusterUid) return;
    if (!configCandidates.some((c) => c.clusterUid === requestedConfig)) {
      clearData();
      setConfigClusterUid("");
      setConfigInvalid(true);
      return;
    }
    if ([...configDrafts.current].some((guard) => guard())) {
        setPendingConfig(requestedConfig);
      return;
    }
    applyConfig(requestedConfig);
  }, [
    applyConfig,
    clearData,
    configClusterUid,
    configCandidates,
    configDrafts,
    configLoading,
    loading,
    params,
    requestedConfig,
    navigate,
  ]);

  // Revocation is also a layout-stage reconciliation; it wins over pending
  // cancellation and removes cached data before passive effects can read it.
  useLayoutEffect(() => {
    if (!clusterUid) return;
    if (clusters.some((c) => c.clusterUid === clusterUid)) return;
    clearData();
    setPendingSwitch(null);
    setPendingTarget(null);
    setClusterUid("");
    setInvalid(true);
    navigate({ pathname: location.pathname, search: "", hash: "" }, {
      replace: true,
    });
  }, [clearData, clusterUid, clusters, invalid, location, navigate]);
  useEffect(() => {
    if (configLoading || requestedConfig || configClusterUid || !configCandidates.length || configGeneration !== 0 || configAutoSelected.current) return;
    configAutoSelected.current = true;
    setConfigClusterUid(configCandidates[0].clusterUid);
    setConfigInvalid(false);
  }, [configCandidates, configClusterUid, configGeneration, configLoading, requestedConfig]);
  useEffect(() => {
    if (!configClusterUid && !configInvalid) return;
    if (configCandidates.some((c) => c.clusterUid === configClusterUid)) return;
    setPendingConfig(null);
    clearData();
    setConfigClusterUid("");
    setConfigInvalid(true);
  }, [clearData, configClusterUid, configCandidates, configInvalid]);

  const urlTransition = Boolean(
    !loading &&
      requested &&
      requested !== clusterUid &&
      clusters.some((cluster) => cluster.clusterUid === requested),
  );
  const urlUnauthorizedTransition = Boolean(
    !loading &&
      requested &&
      requested !== clusterUid &&
      !clusters.some((cluster) => cluster.clusterUid === requested),
  );

  const value = useMemo<ClusterScope>(
    () => ({
      clusterUid,
      candidates: clusters,
      invalid,
      loading,
      generation,
      scopeKey: `${clusterUid}:${generation}`,
      pendingSwitch,
      pendingTarget,
      urlTransition,
      urlUnauthorizedTransition,
      requestCluster,
      requestUrlNavigation,
      cancelPending,
      registerDraft,
      configClusterUid,
      configCandidates,
      configInvalid,
      configLoading,
      configError,
      configGeneration,
      pendingConfig,
      requestConfigCluster,
      confirmConfigPending,
      cancelConfigPending,
      registerConfigDraft,
    }),
    [
      clusterUid,
      clusters,
      invalid,
      loading,
      generation,
      pendingSwitch,
      pendingTarget,
      urlTransition,
      urlUnauthorizedTransition,
      requestCluster,
      requestUrlNavigation,
      cancelPending,
      registerDraft,
      configClusterUid,
      configCandidates,
      configInvalid,
      configLoading,
      configError,
      configGeneration,
      pendingConfig,
      requestConfigCluster,
      confirmConfigPending,
      cancelConfigPending,
      registerConfigDraft,
    ],
  );

  return <Context.Provider value={value}>{children}</Context.Provider>;
}

function fallbackScope(
  clusters: AuthorizedClusterV1[],
): ClusterScope {
  const uid = clusters[0]?.clusterUid ?? "";
  return {
    clusterUid: uid,
    candidates: clusters,
    invalid: false,
    loading: false,
    generation: 0,
    scopeKey: uid,
    pendingSwitch: null,
    pendingTarget: null,
    urlTransition: false,
    urlUnauthorizedTransition: false,
    requestCluster: () => "invalid" as const,
    requestUrlNavigation: () => false,
    cancelPending: () => {},
    registerDraft: () => () => {},
    configClusterUid: "",
    configCandidates: [],
    configInvalid: false,
    configLoading: false,
    configError: null,
    configGeneration: 0,
    pendingConfig: null,
    requestConfigCluster: () => "invalid" as const,
    confirmConfigPending: () => {},
    cancelConfigPending: () => {},
    registerConfigDraft: () => () => {},
  };
}

export function useOperationalCluster() {
  const value = useContext(Context);
  const { session } = useAuth();
  const fallback = useMemo(
    () => fallbackScope(session?.authorizedClusters ?? []),
    [session],
  );
  const scope = value ?? fallback;
  return {
    clusterUid: scope.clusterUid,
    invalid: scope.invalid,
    loading: scope.loading,
    scopeKey: scope.scopeKey,
    generation: scope.generation,
    candidates: scope.candidates,
    requestCluster: scope.requestCluster,
  };
}

export function useClusterScope() {
  const value = useContext(Context);
  if (!value) throw new Error("ClusterScopeProvider is missing");
  return value;
}

export function useClusterDraft(dirty: boolean) {
  const value = useContext(Context);
  useEffect(() => {
    if (!value || !dirty) return;
    return value.registerDraft(() => true);
  }, [value, dirty]);
}

export function useConfigDraft(dirty: boolean) {
  const value = useContext(Context);
  useEffect(() => {
    if (!value || !dirty) return;
    return value.registerConfigDraft(() => true);
  }, [value, dirty]);
}

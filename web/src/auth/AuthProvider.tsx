import {
  createContext,
  useContext,
  useEffect,
  useState,
  useRef,
  type ReactNode,
} from "react";
import type { User } from "oidc-client-ts";
import { record } from "../components/data";
import type {
  AuthSessionV1,
  NativeLoginSuccessV1,
  NativeSessionViewV1,
  AuthorizedClusterV1,
} from "../api/generated/models";
import {
  getAuthSession,
  nativeLogin as nativeLoginRequest,
  nativeLogout,
  changeNativePassword as changeNativePasswordRequest,
} from "../api/generated/platform";
import {
  ApiError,
  authEvents,
  checked,
  clearCredentials,
  nativeRevocationRequestOptions,
  nativeSessionRequestOptions,
  requestOptions,
  setCredentials,
  setNativeCredentials,
} from "../api/client";
import { getManager, restore, safeReturnTo } from "./oidc";
import {
  clearPendingRevocation,
  readPendingRevocation,
  writePendingRevocation,
  type PendingRevocation,
} from "./revocation";

type AuthMode = "oidc" | "native";
// A logout that could not revoke the server session is reported, never treated
// as a successful sign-out: the cookie may still be valid.
export type LogoutOutcome =
  | { status: "confirmed" }
  | { status: "unconfirmed"; pending: PendingRevocation }
  | { status: "superseded"; pending: PendingRevocation };
interface AuthContext {
  user: User | null;
  loggedOut: boolean;
  session: AuthSessionV1 | null;
  nativeSession: NativeSessionViewV1 | null;
  authMode: AuthMode;
  configFailed: boolean;
  loading: boolean;
  error: Error | null;
  accept: (user: User) => Promise<void>;
  refresh: () => Promise<void>;
  nativeLogin: (
    loginName: string,
    password: string,
    returnTo?: string,
    signal?: AbortSignal,
  ) => Promise<string>;
  changeNativePassword: (
    currentPassword: string,
    newPassword: string,
  ) => Promise<void>;
  logout: () => Promise<LogoutOutcome>;
  pendingRevocation: PendingRevocation | null;
  retryRevocation: () => Promise<LogoutOutcome>;
}
const Context = createContext<AuthContext | null>(null);
async function loadAuthMode(): Promise<AuthMode> {
  const response = await fetch("/runtime-config.json", {
    cache: "no-store",
    signal: AbortSignal.timeout(1500),
  });
  if (!response.ok) throw new Error("认证运行配置不可用");
  const config = (await response.json()) as {
    auth?: { mode?: unknown };
    oidc?: unknown;
  };
  if (config.auth !== undefined) {
    const mode = config.auth?.mode;
    if (mode !== "oidc" && mode !== "native") {
      throw new Error("认证运行配置无效");
    }
    return mode;
  }
  // Existing SP08 runtime-config predates auth.mode and contains only the
  // OIDC client/release fields. This is the only legacy shape accepted.
  if (config.oidc !== undefined) return "oidc";
  throw new Error("认证运行配置无效");
}

function nativeAuthSession(
  view: NativeSessionViewV1,
  authorizedClusters: AuthorizedClusterV1[],
): AuthSessionV1 {
  if (view.mode === "first_password_only") {
    return {
      authorizedClusters: [],
      subject: view.subject,
      tenantId: view.tenantId,
      tenantIds: [view.tenantId],
      roles: [],
      clusterScopes: [],
      namespaceScopes: [],
      tokenExpiresAt: view.absoluteExpiresAt,
      acr: view.acr,
    };
  }
  const roles = [
    ...new Set(
      view.authorization.grants
        .map((grant) => grant.role)
        .filter(
          (role): role is "operator" | "platform_admin" =>
            role === "operator" || role === "platform_admin",
        ),
    ),
  ];
  const clusterIdByUid = new Map(
    authorizedClusters.map((cluster) => [
      cluster.clusterUid,
      cluster.clusterId,
    ]),
  );
  const operatorGrants = view.authorization.grants.filter(
    (grant) => grant.role === "operator",
  );
  const clusterScopes = [
    ...new Set(
      operatorGrants
        .filter((grant) => (grant.scope.namespaces ?? []).length === 0)
        .flatMap((grant) => grant.scope.clusterUids ?? [])
        .map((uid) => clusterIdByUid.get(uid))
        .filter((id): id is string => Boolean(id)),
    ),
  ];
  const namespaceScopes = operatorGrants.flatMap((grant) => {
    const uids = grant.scope.clusterUids ?? [];
    if (uids.length !== 1) return [];
    const clusterId = clusterIdByUid.get(uids[0]);
    return clusterId
      ? (grant.scope.namespaces ?? []).map((namespace) => ({
          clusterId,
          namespace,
        }))
      : [];
  });
  return {
    authorizedClusters,
    subject: view.subject,
    tenantId: view.tenantId,
    tenantIds: [view.tenantId],
    roles,
    clusterScopes,
    namespaceScopes,
    tokenExpiresAt: view.absoluteExpiresAt,
    acr: view.acr,
  };
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [session, setSession] = useState<AuthSessionV1 | null>(null);
  const [nativeSession, setNativeSession] =
    useState<NativeSessionViewV1 | null>(null);
  const [authMode, setAuthMode] = useState<AuthMode>("oidc");
  const [modeReady, setModeReady] = useState(false);
  const [configFailed, setConfigFailed] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  const [loggedOut, setLoggedOut] = useState(false);
  const [pendingRevocation, setPendingRevocationState] =
    useState<PendingRevocation | null>(() => readPendingRevocation());
  const setPendingRevocation = (value: PendingRevocation | null) => {
    setPendingRevocationState(value);
  };
  const grantDigest = useRef("");
  const refreshing = useRef(false);
  const refreshPending = useRef(false);
  const nativeRefreshing = useRef(false);
  const nativeRefreshPending = useRef(false);
  const generation = useRef(0);
  const userRef = useRef<User | null>(null);
  const modeRef = useRef<AuthMode>("oidc");

  async function acceptNativeSession(
    view: NativeSessionViewV1,
    acceptedGeneration: number,
    explicitLogin = false,
  ): Promise<AuthSessionV1> {
    const authorizedClusters =
      view.mode === "first_password_only"
        ? []
        : (view.authorizedClusters ?? []);
    const projected = nativeAuthSession(view, authorizedClusters);
    if (acceptedGeneration !== generation.current) {
      throw new DOMException("身份会话已变化", "AbortError");
    }
    const pending = readPendingRevocation();
    if (!explicitLogin && pending?.sessionRef === view.sessionRef) {
      expireIdentity();
      setPendingRevocation(pending);
      throw new DOMException("服务器会话撤销尚未确认", "AbortError");
    }
    const digest = JSON.stringify([
      projected.subject,
      projected.tenantId,
      projected.roles,
      projected.clusterScopes,
      projected.namespaceScopes,
      projected.authorizedClusters,
    ]);
    const changed =
      grantDigest.current !== "" && grantDigest.current !== digest;
    grantDigest.current = digest;
    setLoggedOut(false);
    setNativeCredentials(projected.tenantId, view.csrf.token);
    setNativeSession(view);
    setSession(projected);
    setError(null);
    if (changed) authEvents.dispatchEvent(new Event("authorizationChanged"));
    return projected;
  }

  async function accept(u: User) {
    setLoggedOut(false);
    const acceptedGeneration = ++generation.current;
    setCredentials(u.access_token);
    let response;
    try {
      response = await checked(
        getAuthSession(requestOptions()),
        false,
        () => acceptedGeneration === generation.current,
      );
    } catch (e) {
      if (acceptedGeneration !== generation.current) return;
      if (e instanceof ApiError && e.status === 403) {
        grantDigest.current = "";
        userRef.current = u;
        setUser(u);
        setSession(null);
        setError(e);
        authEvents.dispatchEvent(new Event("authorizationChanged"));
        return;
      }
      throw e;
    }
    if (acceptedGeneration !== generation.current) return;
    if (!("data" in response) || !("roles" in response.data))
      throw new Error("身份 Contract 无效");
    const digest = JSON.stringify([
      response.data.subject,
      response.data.tenantId,
      response.data.roles,
      response.data.clusterScopes,
      response.data.namespaceScopes,
      response.data.authorizedClusters,
    ]);
    if (grantDigest.current && grantDigest.current !== digest)
      authEvents.dispatchEvent(new Event("authorizationChanged"));
    grantDigest.current = digest;
    setCredentials(u.access_token, response.data.tenantId);
    userRef.current = u;
    setUser(u);
    setSession(response.data);
    setError(null);
  }

  async function restoreCurrent() {
    const acceptedGeneration = ++generation.current;
    if (modeRef.current === "native") {
      setNativeCredentials();
      try {
        const response = await checked(
          getAuthSession(requestOptions()),
          false,
          () => acceptedGeneration === generation.current,
        );
        const payload = response as unknown as NativeLoginSuccessV1;
        await acceptNativeSession(payload.data, acceptedGeneration);
      } catch (e) {
        if (acceptedGeneration !== generation.current) return;
        clearCredentials(false);
        setNativeSession(null);
        setSession(null);
        setError(e instanceof Error ? e : new Error("身份会话不可用"));
      }
      return;
    }
    try {
      const identity = await restore();
      if (identity) await accept(identity);
      else setLoading(false);
    } catch (e) {
      if (acceptedGeneration !== generation.current) return;
      setError(e instanceof Error ? e : new Error("身份会话不可用"));
      setLoading(false);
    }
  }

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const mode = await loadAuthMode();
        if (!active) return;
        modeRef.current = mode;
        setAuthMode(mode);
        setModeReady(true);
        await restoreCurrent();
      } catch (cause) {
        if (!active) return;
        modeRef.current = "oidc";
        setConfigFailed(true);
        setError(
          cause instanceof Error ? cause : new Error("认证运行配置不可用"),
        );
        setSession(null);
        setNativeSession(null);
        setLoggedOut(true);
      } finally {
        if (active) setLoading(false);
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  const expiringRef = useRef(false);
  function expireIdentity() {
    if (expiringRef.current) return;
    expiringRef.current = true;
    try {
      generation.current += 1;
      nativeRefreshPending.current = false;
      refreshPending.current = false;
      grantDigest.current = "";
      userRef.current = null;
      setLoggedOut(true);
      setUser(null);
      setSession(null);
      setNativeSession(null);
      setError(null);
      clearCredentials();
    } finally {
      expiringRef.current = false;
    }
  }
  const expiredRef = useRef<() => void>(() => {});
  const deniedRef = useRef<() => void>(() => {});
  const loadedRef = useRef<(identity: User) => void>(() => {});
  useEffect(() => {
    if (!modeReady || configFailed || authMode !== "oidc") return;
    let active = true;
    let cleanup: (() => void) | undefined;
    expiredRef.current = () => expireIdentity();
    deniedRef.current = () => void refresh();
    loadedRef.current = (identity) => {
      void accept(identity);
    };
    void getManager()
      .then((manager) => {
        if (!active) return;
        manager.events.addUserLoaded(loadedRef.current);
        manager.events.addAccessTokenExpired(expiredRef.current);
        cleanup = () => {
          manager.events.removeUserLoaded(loadedRef.current);
          manager.events.removeAccessTokenExpired(expiredRef.current);
        };
        authEvents.addEventListener("expired", expiredRef.current);
        authEvents.addEventListener("forbidden", deniedRef.current);
        return undefined;
      })
      .catch((cause) => {
        if (active)
          setError(
            cause instanceof Error ? cause : new Error("运行配置不可用"),
          );
      });
    return () => {
      active = false;
      cleanup?.();
      authEvents.removeEventListener("expired", expiredRef.current);
      authEvents.removeEventListener("forbidden", deniedRef.current);
    };
  }, [authMode, configFailed, modeReady]);

  useEffect(() => {
    if (!modeReady || configFailed || authMode !== "native") return;
    const expired = () => expireIdentity();
    const denied = () => void refresh();
    authEvents.addEventListener("expired", expired);
    authEvents.addEventListener("forbidden", denied);
    return () => {
      authEvents.removeEventListener("expired", expired);
      authEvents.removeEventListener("forbidden", denied);
    };
  }, [authMode, configFailed, modeReady]);

  useEffect(() => {
    if (modeRef.current === "native" && session) {
      const id = setInterval(() => {
        void refresh();
      }, 30000);
      return () => clearInterval(id);
    }
  }, [authMode, session]);

  async function refresh() {
    if (modeRef.current === "native") {
      if (nativeRefreshing.current) {
        nativeRefreshPending.current = true;
        return;
      }
      nativeRefreshing.current = true;
      const acceptedGeneration = generation.current;
      try {
        const response = await checked(
          getAuthSession(requestOptions()),
          false,
          () => acceptedGeneration === generation.current,
        );
        const payload = response as unknown as NativeLoginSuccessV1;
        await acceptNativeSession(payload.data, acceptedGeneration);
      } catch (e) {
        if (acceptedGeneration !== generation.current) return;
        expireIdentity();
        setError(e instanceof Error ? e : new Error("身份会话不可用"));
      } finally {
        nativeRefreshing.current = false;
        if (nativeRefreshPending.current) {
          nativeRefreshPending.current = false;
          void refresh();
        }
      }
      return;
    }
    const identity = userRef.current;
    if (!identity) return;
    if (refreshing.current) {
      refreshPending.current = true;
      return;
    }
    refreshing.current = true;
    try {
      await accept(identity);
    } catch (e) {
      if (userRef.current !== identity) return;
      setSession(null);
      setError(e as Error);
      authEvents.dispatchEvent(new Event("authorizationChanged"));
    } finally {
      refreshing.current = false;
      if (refreshPending.current) {
        refreshPending.current = false;
        void refresh();
      }
    }
  }

  async function nativeLogin(
    loginName: string,
    password: string,
    returnTo = "",
    signal?: AbortSignal,
  ) {
    const acceptedGeneration = ++generation.current;
    clearCredentials(false);
    setNativeCredentials();
    try {
      const response = await checked(
        nativeLoginRequest(
          {
            loginName,
            password,
          },
          requestOptions(signal),
        ),
        false,
        () => acceptedGeneration === generation.current,
      );
      const payload = response as unknown as NativeLoginSuccessV1;
      // A response can still arrive after the caller cancelled (browser abort
      // races). Never adopt an identity the caller no longer waits for.
      if (signal?.aborted) {
        throw new DOMException("登录已取消", "AbortError");
      }
      const view = payload.data;
      await acceptNativeSession(view, acceptedGeneration, true);
      if (view.mode === "first_password_only") return "/auth/first-password";
      if (view.mustReenrollMfa) return "/account/security";
      const home = view.authorization.grants.some(
        (grant) => grant.role === "operator",
      )
        ? "/overview"
        : "/access/sources";
      return returnTo ? safeReturnTo(returnTo, home) : home;
    } catch (e) {
      if (acceptedGeneration === generation.current && !signal?.aborted) {
        clearCredentials(false);
        setNativeSession(null);
        setSession(null);
        setError(e instanceof Error ? e : new Error("登录失败"));
      }
      throw e;
    }
  }

  async function changeNativePassword(
    currentPassword: string,
    newPassword: string,
  ) {
    const view = nativeSession;
    if (!view) throw new ApiError(401, "UNAUTHENTICATED", "请登录");
    const acceptedGeneration = ++generation.current;
    try {
      const response = await checked(
        changeNativePasswordRequest(
          {
            currentPassword,
            newPassword,
            expectedCredentialVersion: view.revocation.credentialVersion,
          },
          requestOptions(undefined, true),
        ).then((response) => {
          const failure = record(response.data);
          if (
            response.status === 401 &&
            failure.code === "CURRENT_PASSWORD_INVALID" &&
            acceptedGeneration === generation.current
          ) {
            throw new ApiError(
              401,
              "CURRENT_PASSWORD_INVALID",
              "当前密码不正确。",
              typeof failure.requestId === "string"
                ? failure.requestId
                : undefined,
            );
          }
          return response;
        }),
        false,
        () => acceptedGeneration === generation.current,
      );
      const payload = response as unknown as NativeLoginSuccessV1;
      await acceptNativeSession(payload.data, acceptedGeneration);
    } catch (e) {
      if (acceptedGeneration === generation.current) {
        setError(e instanceof Error ? e : new Error("修改密码失败"));
      }
      throw e;
    }
  }

  async function logout(): Promise<LogoutOutcome> {
    if (modeRef.current === "native") {
      const view = nativeSession;
      // The revocation request must survive aborting the old identity's work.
      const options = view
        ? {
            ...requestOptions(undefined, true),
            signal: AbortSignal.timeout(15_000),
          }
        : undefined;
      expireIdentity();
      const logoutGeneration = generation.current;
      if (!view) {
        const pending = readPendingRevocation();
        return pending
          ? { status: "unconfirmed", pending }
          : { status: "confirmed" };
      }
      // Persist before sending: a reload while the request is in flight must
      // not silently restore the session whose revocation is still pending.
      const pending = writePendingRevocation({
        sessionRef: view.sessionRef,
        expectedSessionVersion: view.revocation.sessionVersion,
        reason: "user_requested_logout",
        failedAt: "",
        attempts: 0,
        csrfToken: view.csrf.token,
      });
      setPendingRevocation(pending);
      try {
        await checked(
          nativeLogout(
            {
              expectedSessionVersion: view.revocation.sessionVersion,
              reason: "user_requested_logout",
            },
            options,
          ),
          false,
          () => logoutGeneration === generation.current,
        );
        clearPendingRevocation();
        setPendingRevocation(null);
        return { status: "confirmed" };
      } catch {
        // The local identity stays cleared, but the browser must not claim the
        // server revoked anything. Record the exact session and offer a retry.
        return { status: "unconfirmed", pending };
      }
    }
    await getManager().then((manager) => manager.signoutRedirect());
    return { status: "confirmed" };
  }

  // Controlled retry. It never touches the current identity: it only retries the
  // server revocation of the exact session recorded at failure time, and refuses
  // to act when this browser now carries a different session (a late retry must
  // not revoke the account someone just signed into).
  async function retryRevocation(): Promise<LogoutOutcome> {
    const pending = readPendingRevocation();
    if (!pending) {
      setPendingRevocation(null);
      return { status: "confirmed" };
    }
    let current: NativeLoginSuccessV1 | null = null;
    try {
      current = (await checked(
        getAuthSession(nativeSessionRequestOptions()),
        false,
      )) as unknown as NativeLoginSuccessV1;
    } catch {
      current = null;
    }
    if (!current) {
      // No cookie left: the old session cannot be used from this browser any
      // more. It is still not a confirmed server revocation, so the notice stays
      // until an administrator revokes it.
      return { status: "unconfirmed", pending };
    }
    if (current.data.sessionRef !== pending.sessionRef) {
      setPendingRevocation(pending);
      return { status: "superseded", pending };
    }
    try {
      await checked(
        nativeLogout(
          {
            expectedSessionVersion: pending.expectedSessionVersion,
            reason: pending.reason,
          },
          nativeRevocationRequestOptions(pending.csrfToken),
        ),
        false,
      );
      clearPendingRevocation();
      setPendingRevocation(null);
      return { status: "confirmed" };
    } catch (cause) {
      // A stale version or an already-revoked session means the server refused
      // this revocation attempt for a version reason: report it as unconfirmed
      // rather than silently clearing the notice.
      const retried = writePendingRevocation({
        sessionRef: pending.sessionRef,
        expectedSessionVersion: pending.expectedSessionVersion,
        reason: pending.reason,
        failedAt: pending.failedAt,
        attempts: pending.attempts,
        csrfToken: pending.csrfToken,
      });
      setPendingRevocation(retried);
      void cause;
      return { status: "unconfirmed", pending: retried };
    }
  }

  return (
    <Context.Provider
      value={{
        user,
        loggedOut,
        session,
        nativeSession,
        authMode,
        configFailed,
        loading,
        error,
        accept,
        refresh,
        nativeLogin,
        changeNativePassword,
        logout,
        pendingRevocation,
        retryRevocation,
      }}
    >
      {children}
    </Context.Provider>
  );
}
export function useAuth() {
  const c = useContext(Context);
  if (!c) throw new Error("AuthProvider required");
  return c;
}

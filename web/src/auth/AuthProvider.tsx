import {
  createContext,
  useContext,
  useEffect,
  useState,
  useRef,
  type ReactNode,
} from "react";
import type { User } from "oidc-client-ts";
import type { AuthSessionV1 } from "../api/generated/models";
import { getAuthSession } from "../api/generated/platform";
import {
  ApiError,
  authEvents,
  checked,
  clearCredentials,
  requestOptions,
  setCredentials,
} from "../api/client";
import { getManager, restore } from "./oidc";
interface AuthContext {
  user: User | null;
  session: AuthSessionV1 | null;
  loading: boolean;
  error: Error | null;
  accept: (user: User) => Promise<void>;
  refresh: () => Promise<void>;
}
const Context = createContext<AuthContext | null>(null);
export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [session, setSession] = useState<AuthSessionV1 | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  const grantDigest = useRef("");
  const refreshing = useRef(false);
  const refreshPending = useRef(false);
  const generation = useRef(0);
  const userRef = useRef<User | null>(null);
  async function accept(u: User) {
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
  async function refresh() {
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
        void refreshRef.current();
      }
    }
  }
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;
  useEffect(() => {
    let active = true;
    const restoreGeneration = generation.current;
    const expired = () => {
      generation.current++;
      userRef.current = null;
      refreshPending.current = false;
      grantDigest.current = "";
      setError(null);
      setUser(null);
      setSession(null);
    };
    const denied = () => {
      generation.current++;
      void refreshRef.current();
    };
    authEvents.addEventListener("expired", expired);
    authEvents.addEventListener("forbidden", denied);
    void (
      location.pathname.startsWith("/auth/") ? Promise.resolve(null) : restore()
    )
      .then(async (u) => {
        if (active && u && restoreGeneration === generation.current)
          await accept(u);
      })
      .catch((e) => {
        if (active) setError(e);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    let cleanup = () => {};
    void getManager().then((m) => {
      m.events.addUserLoaded(accept);
      m.events.addAccessTokenExpired(clearCredentials);
      cleanup = () => {
        m.events.removeUserLoaded(accept);
        m.events.removeAccessTokenExpired(clearCredentials);
      };
    });
    return () => {
      active = false;
      cleanup();
      authEvents.removeEventListener("expired", expired);
      authEvents.removeEventListener("forbidden", denied);
    };
  }, []);
  useEffect(() => {
    if (!user) return;
    const id = setInterval(() => {
      void refresh();
    }, 30000);
    return () => clearInterval(id);
  }, [user]);
  return (
    <Context.Provider
      value={{ user, session, loading, error, accept, refresh }}
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

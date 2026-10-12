import {
  InMemoryWebStorage,
  UserManager,
  WebStorageStateStore,
  type User,
} from "oidc-client-ts";
export interface RuntimeConfig {
  oidc: { authority: string; clientId: string; proxyPath?: string };
  release?: { version: string; digest: string; signature: string };
}
let manager: UserManager | undefined;
let managerPromise: Promise<UserManager> | undefined;
export async function getManager(): Promise<UserManager> {
  if (!managerPromise)
    managerPromise = createManager().catch((e) => {
      managerPromise = undefined;
      throw e;
    });
  return managerPromise;
}
async function createManager(): Promise<UserManager> {
  if (manager) return manager;
  const response = await fetch("/runtime-config.json", { cache: "no-store" });
  if (!response.ok) throw new Error("运行配置不可用");
  const config = (await response.json()) as RuntimeConfig;
  const authority = new URL(config.oidc.authority);
  if (authority.protocol !== "https:") throw new Error("OIDC 必须使用 HTTPS");
  const discovery = config.oidc.proxyPath
    ? `${config.oidc.proxyPath}/realms/${authority.pathname.split("/").pop()}/.well-known/openid-configuration`
    : `${authority}/.well-known/openid-configuration`;
  const metadata = await (await fetch(discovery, { cache: "no-store" })).json();
  if (config.oidc.proxyPath) {
    for (const key of [
      "authorization_endpoint",
      "token_endpoint",
      "userinfo_endpoint",
      "jwks_uri",
      "end_session_endpoint",
    ])
      if (metadata[key]) {
        const u = new URL(metadata[key]);
        if (u.origin !== authority.origin)
          throw new Error("OIDC endpoint identity mismatch");
        metadata[key] =
          location.origin + config.oidc.proxyPath + u.pathname + u.search;
      }
  }
  manager = new UserManager({
    authority: authority.toString(),
    metadata,
    client_id: config.oidc.clientId,
    redirect_uri: location.origin + "/auth/callback",
    silent_redirect_uri: location.origin + "/auth/silent-callback",
    post_logout_redirect_uri: location.origin + "/",
    response_type: "code",
    scope: "openid profile",
    automaticSilentRenew: true,
    loadUserInfo: false,
    userStore: new WebStorageStateStore({ store: new InMemoryWebStorage() }),
    stateStore: new WebStorageStateStore({ store: sessionStorage }),
  });
  return manager;
}
export async function login(
  stepUp = false,
  returnTo = location.pathname + location.search,
) {
  return (await getManager()).signinRedirect({
    state: { returnTo },
    ...(stepUp
      ? { acr_values: "urn:ops:loa:2", max_age: 0, prompt: "login" }
      : {}),
  });
}
export function safeReturnTo(value: unknown): string {
  return typeof value === "string" &&
    value.startsWith("/") &&
    !value.startsWith("//") &&
    !value.includes("\\")
    ? value
    : "/overview";
}
export async function restore(): Promise<User | null> {
  const m = await getManager();
  const user = await m.getUser();
  if (user && !user.expired) return user;
  try {
    return await m.signinSilent();
  } catch {
    return null;
  }
}

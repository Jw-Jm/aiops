import { record, text } from "../components/data";
let accessToken: string | undefined;
let tenant: string | undefined;
let nativeSession = false;
let csrfToken: string | undefined;
let credentialEpoch = 0;
let credentialController = new AbortController();
export const authEvents = new EventTarget();
function invalidateCredentials() {
  credentialEpoch++;
  credentialController.abort();
  credentialController = new AbortController();
}
authEvents.addEventListener("authorizationChanged", invalidateCredentials);
export function setCredentials(token?: string, tenantId?: string) {
  const nextTenant = tenantId ?? (token === accessToken ? tenant : undefined);
  if (token === accessToken && nextTenant === tenant && !nativeSession) return;
  invalidateCredentials();
  accessToken = token;
  tenant = nextTenant;
  nativeSession = false;
  csrfToken = undefined;
}
export function setNativeCredentials(tenantId?: string, token?: string) {
  const nextTenant = tenantId ?? tenant;
  const nextCSRF = token ?? csrfToken;
  if (nativeSession && nextTenant === tenant && nextCSRF === csrfToken) return;
  invalidateCredentials();
  accessToken = undefined;
  tenant = nextTenant;
  nativeSession = true;
  csrfToken = nextCSRF;
}
export function clearCredentials(notifyExpired = true) {
  invalidateCredentials();
  accessToken = undefined;
  tenant = undefined;
  nativeSession = false;
  csrfToken = undefined;
  if (notifyExpired) authEvents.dispatchEvent(new Event("expired"));
}
// nativeSessionRequestOptions builds a same-origin native request without
// depending on the in-memory credential state. A pending revocation runs after
// the local identity was cleared, but the browser still carries the HttpOnly
// session cookie that must be revoked.
export function nativeSessionRequestOptions(timeoutMs = 15_000): RequestInit {
  return {
    signal: AbortSignal.timeout(timeoutMs),
    cache: "no-store",
    credentials: "same-origin",
  };
}

// nativeRevocationRequestOptions adds the CSRF token of the session that must
// be revoked. The token is bound to that session and is not a credential on
// its own.
export function nativeRevocationRequestOptions(
  csrfToken: string,
  timeoutMs = 15_000,
): RequestInit {
  return {
    ...nativeSessionRequestOptions(timeoutMs),
    headers: {
      "Content-Type": "application/json",
      ...(csrfToken ? { "X-CSRF-Token": csrfToken } : {}),
      "Idempotency-Key": crypto.randomUUID(),
    },
  };
}

export function requestOptions(
  signal?: AbortSignal,
  mutation = false,
  idempotencyKey?: string,
): RequestInit {
  if (nativeSession) {
    return {
      signal: signal
        ? AbortSignal.any([signal, credentialController.signal])
        : credentialController.signal,
      cache: "no-store",
      credentials: "same-origin",
      headers: {
        // Native requests never accept a tenant from the browser. The fixed
        // deployment tenant and roles come from the server session database.
        ...(mutation && csrfToken
          ? { "X-CSRF-Token": csrfToken }
          : {}),
        ...(mutation
          ? { "Idempotency-Key": idempotencyKey ?? crypto.randomUUID() }
          : {}),
      },
    };
  }
  if (!accessToken) throw new ApiError(401, "UNAUTHENTICATED", "请登录");
  return {
    signal: signal
      ? AbortSignal.any([signal, credentialController.signal])
      : credentialController.signal,
    cache: "no-store",
    headers: {
      Authorization: `Bearer ${accessToken}`,
      ...(tenant ? { "X-Tenant-ID": tenant } : {}),
      ...(mutation
        ? { "Idempotency-Key": idempotencyKey ?? crypto.randomUUID() }
        : {}),
    },
  };
}
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public requestId?: string,
  ) {
    super(`${code} · ${message}`);
  }
}
export async function checked<
  T extends { data: unknown; status: number; headers: Headers },
>(
  promise: Promise<T>,
  notifyAuthorization = true,
  isCurrentSession: () => boolean = () => true,
): Promise<Extract<T, { status: 200 | 201 | 202 | 204 }>["data"]> {
  const requestEpoch = credentialEpoch;
  const current = () => requestEpoch === credentialEpoch && isCurrentSession();
  let result: T;
  try {
    result = await promise;
  } catch (e) {
    if (e instanceof SyntaxError)
      throw new ApiError(
        502,
        "INVALID_RESPONSE",
        "接口返回了无法解析的响应，请刷新观测；不能据此判断操作结果",
      );
    if (e instanceof TypeError)
      throw new ApiError(
        503,
        "NETWORK_UNAVAILABLE",
        "接口连接中断，请先核对持久记录",
      );
    throw e;
  }
  if (result.status < 200 || result.status >= 300) {
    const d = record(result.data);
    if (result.status === 401 && current()) clearCredentials();
    if (result.status === 403 && notifyAuthorization && current())
      authEvents.dispatchEvent(new Event("forbidden"));
    throw new ApiError(
      result.status,
      text(d.code),
      text(d.message),
      typeof d.requestId === "string" ? d.requestId : undefined,
    );
  }
  if (!current())
    throw new DOMException("身份或授权已变化，已丢弃旧请求结果", "AbortError");
  return result.data as Extract<T, { status: 200 | 201 | 202 | 204 }>["data"];
}

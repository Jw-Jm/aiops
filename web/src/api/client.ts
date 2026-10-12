import { record, text } from "../components/data";
let accessToken: string | undefined;
let tenant: string | undefined;
let credentialEpoch = 0;
export const authEvents = new EventTarget();
export function setCredentials(token?: string, tenantId?: string) {
  credentialEpoch++;
  accessToken = token;
  tenant = tenantId;
}
export function clearCredentials() {
  credentialEpoch++;
  accessToken = undefined;
  tenant = undefined;
  authEvents.dispatchEvent(new Event("expired"));
}
export function requestOptions(
  signal?: AbortSignal,
  mutation = false,
  idempotencyKey?: string,
): RequestInit {
  if (!accessToken) throw new ApiError(401, "UNAUTHENTICATED", "请登录");
  return {
    signal,
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
  return result.data as Extract<T, { status: 200 | 201 | 202 | 204 }>["data"];
}

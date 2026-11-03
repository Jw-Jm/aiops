import type { ObservationCreateRequestV1 } from "./generated/models";
import { checked, requestOptions } from "./client";
export type ObservationResource = {
  displayName?: string;
  canonicalId: string;
  uid: string;
  clusterUid: string;
  namespaceScope: {
    kind: "namespaced";
    namespace: string;
    namespaceUid: string;
  };
};
export type ObservationSource = {
  registrationId: string;
  registrationRevision: number;
  credentialRevision: number;
  qualificationDigest: string;
  collectorVersion: string;
};
export type ObservationWindow = {
  from: string;
  to: string;
  timezone: string;
  bounds: "[from,to)";
};
export type ObservationView = {
  request?: ObservationCreateRequestV1;
  queryId: string;
  revision: number;
  state:
    | "accepted"
    | "running"
    | "completed"
    | "failed"
    | "cancel_requested"
    | "terminated"
    | "expired"
    | "denied";
  resource: ObservationResource;
  source: ObservationSource;
  window: ObservationWindow;
  createdAt: string;
  expiresAt: string;
  filters: { field: string; operator: string; value: string }[] | null;
  failure: { code: string; message: string; retryable: boolean } | null;
  termination: { sourceStopped: boolean } | null;
};
export type ObservationCapabilities = {
  resources: ObservationResource[];
  sources: {
    name: string;
    source: ObservationSource;
    coverageFrom: string;
    coverageTo: string;
    verifiedUntil: string;
    maxWindowSeconds: number;
  }[];
  budget: {
    maxRows: number;
    maxBytes: number;
    maxDurationSeconds: number;
    maxSourceCalls: number;
  };
};
export type ObservationResult = {
  queryId: string;
  resultId: string;
  resultDigest: string;
  resource: ObservationResource;
  source: ObservationSource;
  window: ObservationWindow;
  status: "complete" | "partial" | "no_data" | "degraded";
  population: { basis: "exact" | "not_measured"; count?: number };
  samples: {
    returned: number;
    items: { timestamp: string; level: string; message: string }[];
  };
  warnings: string[];
  truncation: { truncated: boolean; reasons: string[] };
};
async function observationResponse<T>(
  path: string,
  cluster: string,
  signal?: AbortSignal,
  body?: unknown,
  canonicalId?: string,
  cursor?: string,
): Promise<{ data: T; nextCursor?: string | null }> {
  const params = new URLSearchParams({ clusterUid: cluster });
  if (canonicalId) params.set("canonicalId", canonicalId);
  if (cursor) params.set("cursor", cursor);
  const options = requestOptions(signal, body !== undefined);
  const response = await checked(
    fetch(`/api/v1/observability/${path}?${params}`, {
      ...options,
      method: body === undefined ? "GET" : "POST",
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      headers: {
        ...options.headers,
        "Content-Type": "application/json",
      },
    }).then(async (response) => ({
      status: response.status,
      headers: response.headers,
      data: await response.json(),
    })),
    false,
  );
  return response as { data: T; nextCursor?: string | null };
}

export async function observation<T>(
  path: string,
  cluster: string,
  signal?: AbortSignal,
  body?: unknown,
  canonicalId?: string,
): Promise<T> {
  return (
    await observationResponse<T>(path, cluster, signal, body, canonicalId)
  ).data;
}
export async function observationHistory(
  cluster: string,
  cursor: string,
  signal: AbortSignal,
) {
  return observationResponse<ObservationView[]>(
    "queries",
    cluster,
    signal,
    undefined,
    undefined,
    cursor,
  );
}

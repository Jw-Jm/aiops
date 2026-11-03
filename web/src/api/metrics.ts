import { checked, requestOptions } from "./client";
export type MetricCapability = {
  sourceId: string;
  datasetId: string;
  operation: string;
  collectorVersion: string;
  proofDigest: string;
  coverageFrom: string;
  coverageTo: string;
  verifiedUntil: string;
  maxWindowSeconds: number;
  status: string;
};
export type MetricResult = {
  datasetId: string;
  stepSeconds?: number;
  status: string;
  exact: boolean;
  unit: string;
  rows: { dimension?: string; value: number }[];
  warnings: string[];
  timeRange: { from: string; to: string; timezone: string };
  sourceManifest: {
    sourceId: string;
    revision: number;
    qualificationDigest: string;
    queryHash: string;
  };
  snapshotReadAt: string;
};
export async function metrics<T>(
  path: string,
  cluster: string,
  canonical: string,
  signal?: AbortSignal,
  body?: unknown,
): Promise<T> {
  const params = new URLSearchParams({ clusterUid: cluster });
  if (canonical) params.set("canonicalId", canonical);
  const options = requestOptions(signal, body !== undefined);
  const result = await checked(
    fetch(`/api/v1/metrics/${path}?${params}`, {
      ...options,
      method: body === undefined ? "GET" : "POST",
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      headers: { ...options.headers, "Content-Type": "application/json" },
    }).then(async (r) => ({
      status: r.status,
      headers: r.headers,
      data: await r.json(),
    })),
    false,
  );
  return (result as { data: T }).data;
}

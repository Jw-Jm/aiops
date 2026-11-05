// Incident collaboration endpoints (P4-04). These wrap the same JSON envelope
// and error contract as the generated client; they stay in a manual module so
// the generated artifacts remain reproducible without a network codegen step.
import { record } from "../components/data";

export type IncidentCollabSuccess<T> = {
  data: T;
  status: number;
  headers: Headers;
};

function url(path: string): string {
  const { origin } = new URL(window.location.href);
  return `${origin}${path}`;
}

async function post<T>(
  path: string,
  body: unknown,
  options?: RequestInit,
): Promise<IncidentCollabSuccess<T>> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(options?.headers as Record<string, string> | undefined),
  };
  const res = await fetch(url(path), { ...options, method: "POST", headers, body: JSON.stringify(body) });
  if (!res.ok) {
    const payload = await res.json().catch(() => null);
    const envelope = record(record(payload));
    throw Object.assign(new Error(textOf(envelope.message) || "协作请求被拒绝"), {
      status: res.status,
      code: textOf(envelope.code),
    });
  }
  const data = (await res.json()) as T;
  return { data, status: res.status, headers: res.headers };
}

async function get<T>(path: string, options?: RequestInit): Promise<IncidentCollabSuccess<T>> {
  const res = await fetch(url(path), { ...options, method: "GET" });
  if (!res.ok) {
    const payload = await res.json().catch(() => null);
    const envelope = record(record(payload));
    throw Object.assign(new Error(textOf(envelope.message) || "协作记录读取失败"), {
      status: res.status,
      code: textOf(envelope.code),
    });
  }
  const data = (await res.json()) as T;
  return { data, status: res.status, headers: res.headers };
}

function textOf(value: unknown): string {
  return typeof value === "string" ? value : "";
}

export function claimIncident(
  incidentId: string,
  body: { expectedRevision: number; note?: string },
  options?: RequestInit,
): Promise<IncidentCollabSuccess<unknown>> {
  return post(`/api/v1/incidents/${encodeURIComponent(incidentId)}:claim`, body, options);
}

export function transferIncident(
  incidentId: string,
  body: { expectedRevision: number; toSubject: string; reason: string },
  options?: RequestInit,
): Promise<IncidentCollabSuccess<unknown>> {
  return post(`/api/v1/incidents/${encodeURIComponent(incidentId)}:transfer`, body, options);
}

export function createIncidentComment(
  incidentId: string,
  body: { body: string; impact?: string },
  options?: RequestInit,
): Promise<IncidentCollabSuccess<unknown>> {
  return post(`/api/v1/incidents/${encodeURIComponent(incidentId)}/comments`, body, options);
}

export function listIncidentFollowUps(
  incidentId: string,
  query: { state?: string; limit?: number } = {},
  options?: RequestInit,
): Promise<IncidentCollabSuccess<unknown>> {
  const params = new URLSearchParams();
  if (query.state) params.set("state", query.state);
  if (query.limit) params.set("limit", String(query.limit));
  const suffix = params.size > 0 ? `?${params.toString()}` : "";
  return get(`/api/v1/incidents/${encodeURIComponent(incidentId)}/follow-ups${suffix}`, options);
}

export function createIncidentFollowUp(
  incidentId: string,
  body: { title: string; detail?: string; ownerSubject?: string },
  options?: RequestInit,
): Promise<IncidentCollabSuccess<unknown>> {
  return post(`/api/v1/incidents/${encodeURIComponent(incidentId)}/follow-ups`, body, options);
}

export function completeIncidentFollowUp(
  incidentId: string,
  followUpId: string,
  body: { note: string },
  options?: RequestInit,
): Promise<IncidentCollabSuccess<unknown>> {
  return post(
    `/api/v1/incidents/${encodeURIComponent(incidentId)}/follow-ups/${encodeURIComponent(followUpId)}:complete`,
    body,
    options,
  );
}

export function reopenIncidentFollowUp(
  incidentId: string,
  followUpId: string,
  options?: RequestInit,
): Promise<IncidentCollabSuccess<unknown>> {
  return post(
    `/api/v1/incidents/${encodeURIComponent(incidentId)}/follow-ups/${encodeURIComponent(followUpId)}:reopen`,
    {},
    options,
  );
}

export function linkIncidentAlerts(
  incidentId: string,
  body: { expectedRevision: number; findingIds: string[]; action: "link" | "unlink"; reason: string },
  options?: RequestInit,
): Promise<IncidentCollabSuccess<unknown>> {
  return post(`/api/v1/incidents/${encodeURIComponent(incidentId)}/alert-links`, body, options);
}

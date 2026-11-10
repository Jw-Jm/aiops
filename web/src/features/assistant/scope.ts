import { useAuth } from "../../auth/AuthProvider";
import { useOperationalCluster } from "../../app/ClusterContext";

// The assistant cache key of the current authorization context. It contains
// the tenant, the subject, the roles and the granted scopes of the session and
// the scope generation of the operational cluster.
//
// The scope generation belongs to the key on purpose: the parent scope decides
// which resources are visible, so a cached result of an older generation is
// not a result of the current authorization context. A query that was built
// without the generation would still show the numbers of a revoked scope
// after the scope changed.
export function useAssistantScopeKey() {
  const { session } = useAuth();
  const { scopeKey: scopeGeneration, generation } = useOperationalCluster();
  return JSON.stringify([
    session?.tenantId,
    session?.subject,
    session?.roles,
    session?.clusterScopes,
    session?.namespaceScopes,
    scopeGeneration,
    generation,
  ]);
}

// The deep link of the assistant carries the scope, the real resource
// identity and the observed time window. The assistant reads them back into
// the new session form, so the scope of the question is the scope the
// operator was looking at.
export interface AssistantEntry {
  clusterUid?: string;
  canonicalId?: string;
  incidentId?: string;
  logQuery?: string;
  mode?: "data_query" | "troubleshoot";
  from?: string;
  to?: string;
}

export function assistantEntryHref(entry: AssistantEntry) {
  const params = new URLSearchParams();
  if (entry.clusterUid) params.set("clusterUid", entry.clusterUid);
  if (entry.canonicalId) params.set("canonicalId", entry.canonicalId);
  if (entry.incidentId) params.set("incidentId", entry.incidentId);
  if (entry.logQuery) params.set("logQuery", entry.logQuery);
  if (entry.mode) params.set("mode", entry.mode);
  if (entry.from) params.set("from", entry.from);
  if (entry.to) params.set("to", entry.to);
  return `/assistant?${params.toString()}`;
}

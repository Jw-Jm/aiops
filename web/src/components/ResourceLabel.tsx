import { useQuery } from "@tanstack/react-query";
import { getResources } from "../api/generated/platform";
import { checked, requestOptions } from "../api/client";
import { list, record, text } from "./data";
import { resourceIdentifier } from "./FaultSummary";
export function useResourceNames(clusterUid: string, ids: string[]) {
  const canonicalIds = [...new Set(ids)].sort().slice(0, 50);
  const query = useQuery({
    queryKey: ["resource-name-index", clusterUid, canonicalIds],
    enabled: !!clusterUid && canonicalIds.length > 0,
    queryFn: ({ signal }) =>
      checked(
        getResources(
          { clusterUid, limit: 50, canonicalIds },
          requestOptions(signal),
        ),
      ),
  });
  return new Map(
    list(record(query.data).data).map((value) => {
      const row = record(value);
      return [text(row.canonicalId), text(row.name)];
    }),
  );
}
export function resourceLabel(id: unknown, names: Map<string, string>) {
  return names.get(text(id)) ?? resourceIdentifier(id);
}

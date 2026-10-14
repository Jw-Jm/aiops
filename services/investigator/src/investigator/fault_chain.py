"""Bounded evidence-only collection via the existing admitted MCP ledger.

No model, direct source client, mutable relative clock or private tool loop.
"""
from datetime import datetime, timedelta, timezone

def collect(job, job_api, bridge):
    committed = []
    failures = []
    remaining = dict(job["budget"])
    last_error = ""

    def call(name, args):
        nonlocal remaining, last_error
        allocation = job_api.allocate(name, args)
        if allocation.get("committed"):
            result = job_api.request("/steps/" + allocation["stepId"] + "/result?argsDigest=" + allocation["argsDigest"])
        else:
            result = bridge.call(name, args, allocation)
        if result.get("budget", {}).get("remaining"):
            remaining = result["budget"]["remaining"]
        last_error = result.get("errorCode", "") or result.get("data", {}).get("sourceErrorCode", "")
        if result.get("state") != "succeeded":
            failures.append(name + "_not_completed")
            return {}, None
        committed.append(allocation["stepId"])
        return result.get("data", {}), allocation["stepId"]

    context, _ = call("get_assistant_context", {})
    request = context.get("faultChainRequest")
    if not isinstance(request, dict) or request.get("schemaVersion") != "fault-chain-request/v1":
        raise RuntimeError("FAULT_CHAIN_REQUEST_REQUIRED")
    target = request["targetCanonicalId"]
    fault = request["fault"]
    # An independently admitted evidence read refreshes only the observed
    # communication overlay. Its original event timestamps are retained.
    if remaining.get("rawQueries", 0) > 0:
        anchor=datetime.fromisoformat(context["requestedAt"].replace("Z","+00:00")).astimezone(timezone.utc).replace(microsecond=0)
        relation_window={"from":(anchor-timedelta(seconds=120)).isoformat().replace("+00:00","Z"),"to":anchor.isoformat().replace("+00:00","Z")}
        l7_live=any(c.get("l7")=="live" or c.get("l7")=="fixture_only" for c in context.get("networkCapabilities",[]))
        relation_args={"resourceCanonicalId": target, "operation": "get l7 context" if l7_live else "get network health", "timeRange": relation_window}
        call("query_deepflow", relation_args)
        if last_error in ("SOURCE_SCOPE_UNVERIFIED","SOURCE_CAPABILITY_UNAVAILABLE") and remaining.get("rawQueries",0)>0 and remaining.get("toolCalls",0)>3:
            call("query_deepflow", relation_args)
    dependencies, _ = call("get_dependencies", {"canonicalId": target, "depth": 1})
    # Capability records use datasetId; choose only current operational sources.
    available = {(d.get("datasetId"), d.get("operation")) for d in context.get("sourceCapabilities", []) if d.get("status") == "qualification_configured"}
    relations = sorted(dependencies.get("relationReferences", []), key=lambda r: r["relationDigest"])
    queries = []
    peers = []
    directions = {}
    node_kinds={n.get("canonicalId"):n.get("kind") for n in dependencies.get("nodes",[])}
    for relation in relations:
        if target not in (relation.get("from"), relation.get("to")):
            continue
        peer = relation["to"] if relation["from"] == target else relation["from"]
        if node_kinds.get(peer) != "Pod":
            continue
        if relation.get("kind")=="communicates_with":directions[peer]="outgoing" if relation["from"]==target else "incoming"
        if peer not in peers:
            peers.append(peer)
    for peer in peers[:request["maxEdges"]]:
        for dataset, metric in (("network.http-5xx-rate", "http_5xx_percent"), ("network.http-5xx", "http_5xx_count"), ("network.http-response-time", "mean_http_response_ms"), ("network.connection-failure-observations", "connection_failure_observation_count"), ("network.tcp-retransmissions", "retransmission_count")):
            for label, window in (("fault", fault), ("recovery", request.get("recovery"))):
                if window is None:
                    continue
                compare = request.get("baseline") is not None
                operation = "compare" if compare else "aggregate"
                if (dataset, operation) not in available:
                    failures.append(dataset + "_" + operation + "_capability_unavailable")
                    continue
                cost = 2 if compare else 1
                if remaining.get("rawQueries", 0) < cost or remaining.get("toolCalls", 0) <= 2:
                    failures.append(label + "_remaining_checks_not_completed")
                    continue
                intent = {"schemaVersion": "query-intent/v1", "datasetId": dataset, "operation": "compare" if compare else "aggregate", "metrics": [metric], "dimensions": ["comparison"] if compare else [], "filters": [], "sort": [], "limit": 3 if compare else 1, "timeRange": window, "networkSelection": {"targetCanonicalId": target, "peerCanonicalId": peer, "direction": directions.get(peer,"either")}}
                if compare:
                    intent["comparisonTimeRange"] = fault if label == "recovery" else request["baseline"]
                result, _ = call("execute_analysis_query", {"intent": intent})
                if last_error == "SOURCE_DEGRADED" and remaining.get("rawQueries",0)>=cost and remaining.get("toolCalls",0)>2:
                    result, _ = call("execute_analysis_query", {"intent":intent})
                if result.get("storedQueryResult"):
                    queries.append(result["storedQueryResult"])
    evaluation, step = call("evaluate_fault_chain", {"stepIds": list(dict.fromkeys(committed))})
    chain = evaluation.get("context")
    if not chain or not step:
        raise RuntimeError("FAULT_CHAIN_EVALUATION_UNAVAILABLE")
    supports, conflicts, links = [], [], []
    entities = {target}
    for edge in chain["edges"]:
        relation = edge["relation"]
        entities.update((relation["from"], relation["to"]))
        yes = sorted({e for o in edge["observations"] for e in o["supports"]})
        no = sorted({e for o in edge["observations"] for e in o["conflicts"]})
        yes = [e for e in yes if e not in no]
        supports.extend(yes)
        conflicts.extend(no)
        links.append({"from": relation["from"], "to": relation["to"], "kind": relation["kind"], "relationDigest": edge["relationDigest"], "assessment": "hypothesis", "supports": yes, "conflicts": no, "missingEvidence": edge["missingEvidence"], "nextChecks": edge["nextChecks"]})
    # Digest is provided by the platform evaluator, never computed from a
    # different JSON serialization in Python.
    digest = evaluation["contentDigest"]
    refs = [{"queryResultId": q["queryResultId"], "factId": f["factId"]} for q in queries if q["queryResultId"] in chain["queryResultRefs"] for f in q["facts"]]
    return {"schemaVersion": "assistant-answer/v1", "answerKind": "diagnosis", "summary": "已完成本轮范围内的故障链证据调查。网络异常与依赖关系属于调查线索，具体因果仍待验证。", "factRefs": refs[:100], "queryResultRefs": chain["queryResultRefs"], "evidenceRefs": chain["evidenceRefs"], "entityRefs": sorted(entities), "missingEvidence": sorted(set(chain["missingEvidence"] + failures)), "scopeSummary": "operational", "asOf": chain["asOf"], "partial": True, "degradedSources": [], "warnings": ["unreliable-event-time"], "nextChecks": ["查看链路比较和证据缺口后发起下一轮只读检查"], "hypotheses": [], "faultChain": links, "faultChainContextRef": {"stepId": step, "contentDigest": digest}}

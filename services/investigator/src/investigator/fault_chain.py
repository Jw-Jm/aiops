"""Bounded evidence-only collection via the existing admitted MCP ledger.

No model, direct source client, mutable relative clock or private tool loop.
"""
from datetime import datetime, timedelta, timezone

def primary_resource_requests(resource):
    return [(name,{"canonicalId":resource,"windowRole":"fault"}) for name in ("resource_history","resource_findings","resource_changes")]

def collect(job, job_api, bridge):
    committed = []
    failures = []
    remaining = dict(job_api.request("/budget"))
    last_error = ""

    def call(name, args):
        nonlocal remaining, last_error
        allocation = job_api.allocate(name, args)
        if allocation.get("committed"):
            result = job_api.request("/steps/" + allocation["stepId"] + "/result?argsDigest=" + allocation["argsDigest"])
            remaining = dict(job_api.request("/budget"))
        else:
            result = bridge.call(name, args, allocation)
        if not allocation.get("committed") and result.get("budget", {}).get("remaining"):
            remaining = result["budget"]["remaining"]
        # The admitted wrapper reports SOURCE_DEGRADED while preserving the
        # specific source failure in data. Prefer that code for bounded
        # fallback selection; the generic wrapper must not hide it.
        last_error = result.get("data", {}).get("sourceErrorCode", "") or result.get("errorCode", "")
        if result.get("state") != "succeeded":
            failures.append(name + "_not_completed")
            return {}, None
        committed.append(allocation["stepId"])
        return result.get("data", {}), allocation["stepId"]

    context, _ = call("get_assistant_context", {})
    request = context.get("faultChainRequest")
    if not isinstance(request, dict) or request.get("schemaVersion") not in ("fault-chain-request/v1", "fault-chain-request/v2"):
        raise RuntimeError("FAULT_CHAIN_REQUEST_REQUIRED")
    target = request["targetCanonicalId"]
    fault = request["fault"]
    check = request.get("readOnlyCheck")
    operational = request.get("schemaVersion") == "fault-chain-request/v2"
    target_kind = next((r.get("kind") for r in context.get("binding", {}).get("resourceContext", {}).get("resources", []) if r.get("canonicalId") == target), None)
    if not target_kind and "/" in target:
        from urllib.parse import unquote
        target_kind = unquote(target.split("/")[-2])
    target_kind = target_kind or "Pod"
    available = {(d.get("datasetId"), d.get("operation")) for d in context.get("sourceCapabilities", []) if d.get("status") == "qualification_configured"}
    queries = []

    def resource_reads(resource, kind, optional=False):
        # Reserve the evaluator and primary counterevidence before optional reads.
        for tool,args in (primary_resource_requests(resource)[:1] if optional else primary_resource_requests(resource)):
            if remaining.get("toolCalls", 0) <= 3:
                failures.append(tool + "_budget_not_collected")
                continue
            call(tool, args)
        if optional and request.get("recovery") and remaining.get("toolCalls", 0) > 5:
            call("resource_history", {"canonicalId": resource, "windowRole": "recovery"})
        metrics = {"Pod": [("metrics.pod-restarts", "restart_increase"), ("metrics.pod-phase", "phase_count"), ("metrics.pod-memory", "memory_bytes"), ("metrics.pod-cpu", "cpu_cores")], "Node": [("metrics.node-ready", "ready_count")], "PersistentVolumeClaim": []}.get(kind, [])
        metrics = [] if optional else metrics[:1]

        for dataset, metric in metrics:
            operation = "compare" if request.get("baseline") else "aggregate"
            # Structured error level only; samples are never interpreted as totals.
            filters = [{"field": "level", "operator": "eq", "value": "error"}] if dataset == "logs.records" else []
            if (dataset, operation) not in available:
                failures.append(dataset + "_capability_unavailable")
                continue
            cost = 2 if operation == "compare" else 1
            if remaining.get("rawQueries", 0) < cost or remaining.get("toolCalls", 0) <= 3:
                failures.append(dataset + "_budget_not_collected")
                continue
            intent = {"schemaVersion": "query-intent/v1", "datasetId": dataset, "operation": operation, "metrics": [metric], "dimensions": ["comparison"] if operation == "compare" else [], "filters": filters, "sort": [], "limit": 3 if operation == "compare" else 1, "timeRange": fault}
            if operation == "compare": intent["comparisonTimeRange"] = request["baseline"]
            data, _ = call("execute_analysis_query", {"canonicalId": resource, "windowRole": "fault", "intent": intent})
            if data.get("storedQueryResult"): queries.append(data["storedQueryResult"])
        if kind == "PersistentVolumeClaim":
            failures.append("pvc_metrics_logs_not_applicable_use_native_and_csi_relations")

    if operational and not check:
        resource_reads(target, target_kind)
    if check and check["tool"] != "execute_analysis_query":
        args = {"canonicalId": check["canonicalId"]}
        if check["tool"] == "get_dependencies":
            args["depth"] = 1
        call(check["tool"], args)
        queries = []
    else:
        # An independently admitted evidence read refreshes only the observed
        # communication overlay. Its original event timestamps are retained.
        if not check and target_kind == "Pod" and remaining.get("rawQueries", 0) > 0:
            anchor=datetime.fromisoformat(context["requestedAt"].replace("Z","+00:00")).astimezone(timezone.utc).replace(microsecond=0)
            relation_window={"from":(anchor-timedelta(seconds=120)).isoformat().replace("+00:00","Z"),"to":anchor.isoformat().replace("+00:00","Z")}
            l7_live=any(c.get("l7")=="live" or c.get("l7")=="fixture_only" for c in context.get("networkCapabilities",[]))
            relation_args={"resourceCanonicalId": target, "operation": "get l7 context" if l7_live else "get network health", "timeRange": relation_window}
            relation_result, _ = call("query_deepflow", relation_args)
            if l7_live and (last_error in ("SOURCE_SCOPE_UNVERIFIED","SOURCE_CAPABILITY_UNAVAILABLE") or not relation_result.get("evidence")) and remaining.get("rawQueries",0)>0 and remaining.get("toolCalls",0)>3:
                # Degraded legacy reads are committed too. Use the separately
                # admitted L4 operation; identical args would replay the same gap.
                call("query_deepflow", {**relation_args,"operation":"get network health"})
        dependencies, _ = call("get_dependencies", {"canonicalId": target, "depth": 1})
        # Capability records use datasetId; choose only current operational sources.
        available = {(d.get("datasetId"), d.get("operation")) for d in context.get("sourceCapabilities", []) if d.get("status") == "qualification_configured"}
        relations = sorted(dependencies.get("relationReferences", []), key=lambda r: r["relationDigest"])
        peers = []
        directions = set()
        node_kinds={n.get("canonicalId"):n.get("kind") for n in dependencies.get("nodes",[])}
        for relation in relations:
            if target not in (relation.get("from"), relation.get("to")):
                continue
            peer = relation["to"] if relation["from"] == target else relation["from"]
            if target_kind != "Pod" or node_kinds.get(peer) != "Pod":
                continue
            if relation.get("kind") == "communicates_with":
                directions.add((peer, "outgoing" if relation["from"] == target else "incoming"))
            if peer not in peers:
                peers.append(peer)
        selections = sorted(directions | {(peer, "either") for peer in peers if not any(p == peer for p, _ in directions)}, key=lambda pair: (pair[0], {"outgoing": 0, "incoming": 1, "either": 2}[pair[1]]))
        selected = selections[:request["maxEdges"]]
        selected_peers = list(dict.fromkeys(peer for peer, _ in selected))
        if check:
            selected_peers = [check["canonicalId"]]
            selected = [(check["canonicalId"], check["direction"])]
        if not check and len(selections) > len(selected):
            failures.append("remaining_relations_not_checked")
        # Shared Node paths are each caller's own admitted one-hop graph read.
        # Do not traverse two hops or silently expand the frozen execution scope.
        if not check and any(n.get("kind") == "Node" for n in dependencies.get("nodes", [])):
            for peer in selected_peers:
                if remaining.get("toolCalls", 0) > 3:
                    call("get_dependencies", {"canonicalId": peer, "depth": 1})
                else:
                    failures.append("shared_resource_relations_not_completed")
        # Stable stages give every selected edge its primary fault observation
        # before spending budget on transport and recovery. Every read still goes
        # through allocate/call and uses the frozen window, never a private loop.
        for label, window in (("fault", fault), ("recovery", request.get("recovery"))):
            if target_kind != "Pod":
                failures.append("network_not_applicable_for_" + target_kind)
                break
            if window is None:
                continue
            compare = label == "recovery" or request.get("baseline") is not None
            operation = "compare" if compare else "aggregate"
            datasets = []
            if ("network.http-summary", operation) in available:
                datasets.append(("network.http-summary", "http_5xx_percent"))
            else:
                if ("network.http-5xx-rate", operation) in available:
                    datasets.append(("network.http-5xx-rate", "http_5xx_percent"))
                elif ("network.http-5xx", operation) in available:
                    datasets.append(("network.http-5xx", "http_5xx_count"))
                else:
                    failures.append(label + "_http_error_capability_unavailable")
                if ("network.http-response-time", operation) in available:
                    datasets.append(("network.http-response-time", "mean_http_response_ms"))
            if ("network.tcp-summary", operation) in available:
                datasets.append(("network.tcp-summary", "connection_failure_observation_count"))
            else:
                tcp = [(d, m) for d, m in (("network.connection-failure-observations", "connection_failure_observation_count"), ("network.tcp-retransmissions", "retransmission_count")) if (d, operation) in available]
                datasets.extend(tcp)
                if not tcp:
                    failures.append(label + "_tcp_capability_unavailable")
            for dataset, metric in datasets:
                for peer, direction in selected:
                    cost = 2 if compare else 1
                    if remaining.get("rawQueries", 0) < cost or remaining.get("toolCalls", 0) <= 2:
                        failures.append(label + "_remaining_checks_not_completed")
                        continue
                    intent = {"schemaVersion": "query-intent/v1", "datasetId": dataset, "operation": operation, "metrics": [metric], "dimensions": ["comparison"] if compare else [], "filters": [], "sort": [], "limit": 3 if compare else 1, "timeRange": window, "networkSelection": {"targetCanonicalId": target, "peerCanonicalId": peer, "direction": direction}}
                    if compare:
                        intent["comparisonTimeRange"] = fault if label == "recovery" else request["baseline"]
                    result, _ = call("execute_analysis_query", {"intent": intent})
                    if last_error == "SOURCE_DEGRADED" and remaining.get("rawQueries",0)>=cost and remaining.get("toolCalls",0)>2:
                        result, _ = call("execute_analysis_query", {"intent":intent})
                    if result.get("storedQueryResult"):
                        queries.append(result["storedQueryResult"])
            if label == "fault" and not check:
                # Only already disclosed Nodes may enter a cluster-scoped read.
                health_targets=sorted({n.get("canonicalId") for n in dependencies.get("nodes",[]) if n.get("kind")=="Node" and n.get("canonicalId")})
                health_targets.append(target)
                health_targets.extend(p for p in selected_peers if p not in health_targets)
                for resource in health_targets[:request["maxEdges"] + 2]:
                    if remaining.get("toolCalls",0)>3:
                        call("get_resource_health", {"canonicalId":resource})
                    else:
                        failures.append("shared_resource_health_not_completed")
        if operational and not check:
            related = sorted({n.get("canonicalId"): n.get("kind") for n in dependencies.get("nodes", []) if n.get("canonicalId") and n.get("canonicalId") != target and n.get("kind") in ("Pod", "Node", "PersistentVolumeClaim")}.items())
            for resource, kind in related[:request["maxEdges"]]:
                if remaining.get("toolCalls", 0) > 6:
                    resource_reads(resource, kind, optional=True)
                else:
                    failures.append("related_resource_budget_not_collected")
            for role in ("baseline", "recovery"):
                if request.get(role) and remaining.get("toolCalls", 0) > 4:
                    call("resource_history", {"canonicalId": target, "windowRole": role})
            if request.get("baseline") and remaining.get("toolCalls", 0) > 4:
                call("resource_changes", {"canonicalId": target, "windowRole": "baseline"})
    if operational and not check and target_kind == "Pod":
        for dataset, metric in (("logs.records", "log_count"), ("metrics.pod-memory", "memory_bytes"), ("metrics.pod-cpu", "cpu_cores")):
            compare = request.get("baseline") is not None and remaining.get("rawQueries", 0) >= 2
            operation = "compare" if compare else "aggregate"
            if (dataset, operation) not in available or remaining.get("rawQueries", 0) < (2 if compare else 1) or remaining.get("toolCalls", 0) <= 2:
                failures.append(dataset + "_optional_not_collected")
                continue
            intent = {"schemaVersion": "query-intent/v1", "datasetId": dataset, "operation": operation, "metrics": [metric], "dimensions": ["comparison"] if compare else [], "filters": [{"field": "level", "operator": "eq", "value": "error"}] if dataset == "logs.records" else [], "sort": [], "limit": 3 if compare else 1, "timeRange": fault}
            if compare: intent["comparisonTimeRange"] = request["baseline"]
            data, _ = call("execute_analysis_query", {"canonicalId": target, "windowRole": "fault", "intent": intent})
            if data.get("storedQueryResult"): queries.append(data["storedQueryResult"])
    evaluation, step = call("evaluate_fault_chain", {"stepIds": list(dict.fromkeys(committed))})
    chain = evaluation.get("context")
    if not chain or not step:
        raise RuntimeError("FAULT_CHAIN_EVALUATION_UNAVAILABLE")
    supports, conflicts, links = [], [], []
    entities = {target}
    entities.update(r["canonicalId"] for r in chain.get("sourceSteps", []))
    entities.update(h["canonicalId"] for h in chain.get("resourceHealthChecks", []))
    for edge in chain["edges"]:
        relation = edge["relation"]
        entities.update((relation["from"], relation["to"]))
        for observation in edge["observations"]:
            health=observation.get("resourceHealth")
            if health:entities.add(health["canonicalId"])
            for path in observation.get("relatedRelations",[]):entities.update((path["from"],path["to"]))
        yes = sorted({e for o in edge["observations"] for e in o["supports"]})
        no = sorted({e for o in edge["observations"] for e in o["conflicts"]})
        yes = [e for e in yes if e not in no]
        supports.extend(yes)
        conflicts.extend(no)
        links.append({"from": relation["from"], "to": relation["to"], "kind": relation["kind"], "relationDigest": edge["relationDigest"], "assessment": "hypothesis", "supports": yes, "conflicts": no, "missingEvidence": edge["missingEvidence"], "nextChecks": edge["nextChecks"]})
    # Digest is provided by the platform evaluator, never computed from a
    # different JSON serialization in Python.
    digest = evaluation["contentDigest"]
    summary = "已提交本轮预算内的运行数据关联调查。已收集内容与证据缺口见关联记录；共现和时间接近不确认因果。"
    if check:
        summary = f"已执行关联原轮次缺口 {check['gapCode']} 的只读检查 {check['tool']}。结果以已提交数据为准；当前快照不替代历史窗口证明，原缺口保留。"
    refs = [{"queryResultId": q["queryResultId"], "factId": f["factId"]} for q in queries if q["queryResultId"] in chain["queryResultRefs"] for f in q["facts"]]
    return {"schemaVersion": "assistant-answer/v1", "answerKind": "diagnosis", "summary": summary, "factRefs": refs[:100], "queryResultRefs": chain["queryResultRefs"], "evidenceRefs": chain["evidenceRefs"], "entityRefs": sorted(entities), "missingEvidence": sorted(set(chain["missingEvidence"] + failures)), "scopeSummary": "operational", "asOf": chain["asOf"], "partial": True, "degradedSources": [], "warnings": ["unreliable-event-time"], "nextChecks": ["查看链路比较和证据缺口后发起下一轮只读检查"], "hypotheses": [], "faultChain": links, "faultChainContextRef": {"stepId": step, "contentDigest": digest}}

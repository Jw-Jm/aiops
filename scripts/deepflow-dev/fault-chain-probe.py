#!/usr/bin/env python3
"""Independent HTTP counting in the isolated owned canaries; no credentials.

Run only on 192.168.0.63. Reports summarize D1 metadata, never HTTP content.

The oracle is the traffic record produced inside the owned client Pod. The
source proof only passes when one observation point of the frozen window
accounts for exactly that many completed HTTP responses and 5xx responses.
Ingest is progressive: a first empty poll is not proof that the collector
missed the traffic, so the probe polls a bounded settle window, re-reads a
match once, and records how it classified a failure. It never lowers the
counts to make a partial window pass.
"""
import argparse
import json
import math
import re
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

NAMESPACE = "ops-assistant-deepflow-20261013"
RELEASE = "fault-chain-20261014"
OBSERVATION_POINTS = ("s-p", "c-p", "s", "c", "s-app", "c-app", "app")
HTTP_COMPLETED = "single_point_completed_http_session_observations"
MIN_RECONCILED_RESPONSES = 200
POLL_SECONDS = 5
CONFIRM_SECONDS = 10
GROUP_LIMIT = 100


def kubectl(*args):
    return subprocess.check_output(["kubectl", "-n", NAMESPACE, *args], text=True)


def query(sql, timeout=20):
    request = urllib.request.Request(
        "http://127.0.0.1:21416/v1/query/",
        urllib.parse.urlencode({"db": "flow_log", "sql": sql}).encode(),
        headers={"X-Org-Id": "1"},
    )
    with urllib.request.urlopen(request, timeout=timeout) as response:
        body = json.load(response)
    if body.get("OPT_STATUS") != "SUCCESS":
        raise RuntimeError("QUERIER_CONTRACT_REJECTED")
    return [dict(zip(body["result"]["columns"], row)) for row in (body["result"]["values"] or [])]


def point_totals(rows):
    """Completed HTTP session observations per observation point.

    Only session-end rows of the HTTP protocol with a final response code are
    counted; anything else is not a reconcilable HTTP response.
    """
    totals = {}
    for row in rows:
        point = row.get("tap_side")
        if point not in OBSERVATION_POINTS or row.get("l7_protocol") != 20 or row.get("type") != 2:
            continue
        code = row.get("response_code")
        if code is None or not 200 <= int(code) < 600:
            continue
        entry = totals.setdefault(point, {"observations": 0, "errors": 0})
        observations = int(row.get("observations") or 0)
        entry["observations"] += observations
        if int(code) >= 500:
            entry["errors"] += observations
    return totals


def reconciling_points(totals, responses, errors):
    """Every point whose completed responses equal the independent record."""
    return sorted(
        point
        for point, entry in totals.items()
        if entry["observations"] == responses and entry["errors"] == errors
    )


def classify_failure(samples, totals, namespace_observations, agent_restarted):
    """Explain why a window has no reconcilable point.

    collector_blackout: no DeepFlow rows at all, not even for other Pods of the
    same namespace, so the gap is not a pair/identity/filter problem.
    namespace_without_pair: other Pods of the namespace were observed while this
    pair was not, which points at the pair identity or the query filter.
    count_unstable_late_rows: rows existed but the re-read disagreed, so the
    population of the frozen window was still changing.
    count_mismatch: stable rows that simply do not reconcile.
    """
    if not samples:
        if totals:
            return "count_unstable_late_rows"
        if namespace_observations is None:
            return "no_rows_for_pair_with_unverified_namespace"
        if namespace_observations == 0:
            return "collector_blackout_downstream_of_agent_or_ingest"
        return "namespace_without_pair"
    if not totals:
        return "rows_without_completed_http_sessions"
    if agent_restarted:
        return "count_mismatch_after_agent_restart"
    return "count_mismatch"


def agent_restarts():
    try:
        raw = kubectl("get", "pods", "-l", "component=deepflow-agent", "-o", "json")
    except subprocess.CalledProcessError:
        return None
    total = 0
    for pod in json.loads(raw)["items"]:
        for status in pod.get("status", {}).get("containerStatuses", []):
            total += int(status.get("restartCount") or 0)
    return total


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--report", required=True)
    parser.add_argument("--count", type=int, default=1000)
    parser.add_argument("--wait-seconds", type=int, default=240, help="bounded settle window for ingest")
    args = parser.parse_args()
    if not MIN_RECONCILED_RESPONSES <= args.count <= 2000:
        raise ValueError("bounded traffic required")
    if not 30 <= args.wait_seconds <= 600:
        raise ValueError("bounded settle window required")
    pods = json.loads(kubectl("get", "pods", "-l", "ops.platform.io/test-release=" + RELEASE, "-o", "json"))["items"]
    pods = [p for p in pods if p["metadata"]["labels"].get("app") in ("fault-chain-client", "fault-chain-server")]
    if len(pods) != 2:
        raise RuntimeError("OWNED_PAIR_REQUIRED")
    client = next(p for p in pods if p["metadata"]["labels"]["app"] == "fault-chain-client")
    server = next(p for p in pods if p["metadata"]["labels"]["app"] == "fault-chain-server")
    for pod in pods:
        if pod["status"].get("phase") != "Running" or pod["metadata"]["labels"]["ops.platform.io/test-release"] != RELEASE:
            raise RuntimeError("OWNED_READY_CANARIES_REQUIRED")
    for pod in pods:
        if not re.fullmatch(r"[a-z0-9.-]+", pod["metadata"]["name"]):
            raise RuntimeError("NATIVE_NAME_INVALID")
    restarts_before = agent_restarts()
    program = """import json,time,urllib.request,urllib.error
count=%d;start=time.time();ok=bad=failed=0
for n in range(count):
 try:
  urllib.request.urlopen('http://fault-chain-server:8080/'+('fail' if n%%20==0 else 'health'),timeout=3).read();ok+=1
 except urllib.error.HTTPError as e:
  if e.code==503:bad+=1
  else:failed+=1
 except Exception:failed+=1
print(json.dumps({'startedAt':start,'endedAt':time.time(),'responses':ok+bad,'errors':bad,'transportFailures':failed}))
""" % args.count
    golden = json.loads(kubectl("exec", client["metadata"]["name"], "--", "python3", "-c", program))
    start, end = math.floor(golden["startedAt"]), math.ceil(golden["endedAt"]) + 1
    report = {
        "schemaVersion": "fault-chain-http-proof/v1",
        "namespace": NAMESPACE,
        "release": RELEASE,
        "golden": golden,
        "window": {"fromUnix": start, "toUnix": end},
        "nativePods": [
            {
                "name": p["metadata"]["name"],
                "uid": p["metadata"]["uid"],
                "containerIds": [c["containerID"] for c in p["status"]["containerStatuses"]],
            }
            for p in pods
        ],
        "metadataCounts": [],
        "status": "FAIL",
        "timeReliable": False,
        "scope": HTTP_COMPLETED,
        "pollSeconds": 0,
        "confirmSeconds": 0,
        "ingestLagSeconds": None,
        "matchingPoints": [],
        "failureClass": "",
    }
    # Tag IDs are independent observations for the oracle, never a product
    # mapper input. Runtime tests must still use LiveMapper and native UIDs.
    left = right = None
    namespace_observations = None
    pair_observations = None
    totals = {}
    deadline = time.time() + args.wait_seconds
    started_polling = time.time()

    def bounded(cap):
        return max(1, min(cap, deadline - time.time()))

    while time.time() < deadline:
        time.sleep(bounded(POLL_SECONDS))
        report["pollSeconds"] = round(time.time() - started_polling)
        try:
            if left is None or right is None:
                client_ids = query("SHOW TAG pod VALUES FROM l7_flow_log WHERE pod='%s'" % client["metadata"]["name"], bounded(20))
                server_ids = query("SHOW TAG pod VALUES FROM l7_flow_log WHERE pod='%s'" % server["metadata"]["name"], bounded(20))
                if len(client_ids) != 1 or len(server_ids) != 1:
                    continue
                left, right = int(client_ids[0]["value"]), int(server_ids[0]["value"])
            rows = query(
                "SELECT pod_id_0,pod_id_1,pod_cluster_id_0,pod_cluster_id_1,pod_ns_id_0,pod_ns_id_1,tap_side,l7_protocol,type,response_code,Count(row) AS observations "
                "FROM l7_flow_log WHERE time >= %d AND time < %d AND pod_id_0 = %d AND pod_id_1 = %d "
                "GROUP BY pod_id_0,pod_id_1,pod_cluster_id_0,pod_cluster_id_1,pod_ns_id_0,pod_ns_id_1,tap_side,l7_protocol,type,response_code LIMIT %d"
                % (start, end, left, right, GROUP_LIMIT),
                bounded(20),
            )
            report["metadataCounts"] = rows
            totals = point_totals(rows)
            matching = reconciling_points(totals, golden["responses"], golden["errors"])
            if not matching:
                continue
            # Re-read once: a first matching poll could still be followed by
            # late duplicate rows that change the population of the same window.
            matched_at = time.time()
            report["ingestLagSeconds"] = round(matched_at - started_polling)
            time.sleep(bounded(CONFIRM_SECONDS))
            report["confirmSeconds"] = round(time.time() - matched_at)
            confirm = point_totals(
                query(
                    "SELECT tap_side,l7_protocol,type,response_code,Count(row) AS observations "
                    "FROM l7_flow_log WHERE time >= %d AND time < %d AND pod_id_0 = %d AND pod_id_1 = %d "
                    "GROUP BY tap_side,l7_protocol,type,response_code LIMIT %d" % (start, end, left, right, GROUP_LIMIT),
                    bounded(20),
                )
            )
            report["confirmMatches"] = confirm == totals
            if confirm != totals:
                totals = confirm
                continue
            report["matchingPoints"] = matching
            break
        except (RuntimeError, urllib.error.URLError, TimeoutError):
            continue
    if left is not None:
        try:
            namespace_ids = query("SHOW TAG pod_ns VALUES FROM l7_flow_log WHERE pod_ns='%s'" % NAMESPACE, bounded(20))
            if len(namespace_ids) == 1:
                namespace_id = int(namespace_ids[0]["value"])
                pair_observations = int(
                    query(
                        "SELECT Count(row) AS observations FROM l7_flow_log WHERE time >= %d AND time < %d AND pod_ns_id_0 = %d AND pod_id_0 = %d AND pod_id_1 = %d"
                        % (start, end, namespace_id, left, right),
                        bounded(20),
                    )[0]["observations"]
                )
                namespace_observations = int(
                    query(
                        "SELECT Count(row) AS observations FROM l7_flow_log WHERE time >= %d AND time < %d AND pod_ns_id_0 = %d AND NOT (pod_id_0 = %d AND pod_id_1 = %d)"
                        % (start, end, namespace_id, left, right),
                        bounded(20),
                    )[0]["observations"]
                )
        except (RuntimeError, urllib.error.URLError, TimeoutError):
            namespace_observations = None
    report["pairObservations"] = pair_observations
    report["namespaceOtherObservations"] = namespace_observations
    report["agentRestarts"] = {"before": restarts_before, "after": agent_restarts()}
    restarted = restarts_before is not None and report["agentRestarts"]["after"] is not None and report["agentRestarts"]["after"] != restarts_before
    if not report["matchingPoints"]:
        if len(report["metadataCounts"]) >= GROUP_LIMIT > 0:
            report["failureClass"] = "group_budget_exceeded"
        else:
            report["failureClass"] = classify_failure(report["metadataCounts"], totals, namespace_observations, restarted)
    else:
        point = report["matchingPoints"][0]
        row = next(r for r in report["metadataCounts"] if r["tap_side"] == point)
        report["observationPoint"] = point
        sql = (
            "SELECT response_code,Count(row) AS observations,Sum(response_duration) AS duration_us FROM l7_flow_log "
            "WHERE time >= %d AND time < %d AND pod_cluster_id_0 = %d AND pod_cluster_id_1 = %d AND pod_ns_id_0 = %d AND pod_ns_id_1 = %d "
            "AND pod_id_0 = %d AND pod_id_1 = %d AND tap_side = '%s' AND l7_protocol = 20 AND type = 2 AND response_code >= 200 AND response_code < 600 "
            "GROUP BY response_code LIMIT 401"
            % (
                start,
                end,
                row["pod_cluster_id_0"],
                row["pod_cluster_id_1"],
                row["pod_ns_id_0"],
                row["pod_ns_id_1"],
                row["pod_id_0"],
                row["pod_id_1"],
                point,
            )
        )
        try:
            report["aggregate"] = query(sql, 20)
            if sum(r["observations"] for r in report["aggregate"]) != golden["responses"] or sum(
                r["observations"] for r in report["aggregate"] if r["response_code"] >= 500
            ) != golden["errors"]:
                report["failureClass"] = "aggregate_mismatch"
            else:
                report["status"] = "PASS" if golden["transportFailures"] == 0 else "FAIL"
                if golden["transportFailures"] != 0:
                    report["failureClass"] = "transport_failures"
        except RuntimeError:
            report["failureClass"] = "fixed_aggregate_not_supported"
    Path(args.report).write_text(json.dumps(report, indent=2) + "\n")
    print(
        json.dumps(
            {
                "status": report["status"],
                "failureClass": report["failureClass"],
                "responses": golden["responses"],
                "errors": golden["errors"],
                "observationPoint": report.get("observationPoint"),
                "ingestLagSeconds": report.get("ingestLagSeconds"),
                "pairObservations": pair_observations,
                "namespaceOtherObservations": namespace_observations,
                "report": args.report,
            }
        )
    )
    return 0 if report["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())

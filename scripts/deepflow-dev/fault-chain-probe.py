#!/usr/bin/env python3
"""Independent HTTP counting in the isolated owned canaries; no credentials.

Run only on 192.168.0.63. Reports summarize D1 metadata, never HTTP content.
"""
import argparse
import json
import math
import re
import subprocess
import time
import urllib.parse
import urllib.request
from pathlib import Path

NAMESPACE = "ops-assistant-deepflow-20261013"
RELEASE = "fault-chain-20261014"


def kubectl(*args):
    return subprocess.check_output(["kubectl", "-n", NAMESPACE, *args], text=True)


def query(sql):
    request = urllib.request.Request("http://127.0.0.1:21416/v1/query/", urllib.parse.urlencode({"db": "flow_log", "sql": sql}).encode(), headers={"X-Org-Id": "1"})
    with urllib.request.urlopen(request, timeout=10) as response:
        body = json.load(response)
    if body.get("OPT_STATUS") != "SUCCESS":
        raise RuntimeError("QUERIER_CONTRACT_REJECTED")
    return [dict(zip(body["result"]["columns"], row)) for row in (body["result"]["values"] or [])]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--report", required=True)
    parser.add_argument("--count", type=int, default=1000)
    args = parser.parse_args()
    if not 1 <= args.count <= 2000:
        raise ValueError("bounded traffic required")
    pods = json.loads(kubectl("get", "pods", "-l", "ops.platform.io/test-release=" + RELEASE, "-o", "json"))["items"]
    if len(pods) != 2:
        raise RuntimeError("OWNED_PAIR_REQUIRED")
    client = next(p for p in pods if p["metadata"]["labels"]["app"] == "fault-chain-client")
    server = next(p for p in pods if p["metadata"]["labels"]["app"] == "fault-chain-server")
    for pod in pods:
        if pod["status"].get("phase") != "Running" or pod["metadata"]["labels"]["ops.platform.io/test-release"] != RELEASE:
            raise RuntimeError("OWNED_READY_CANARIES_REQUIRED")
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
    # Tag IDs are independent observations for the oracle, never a product
    # mapper input. Runtime tests must still use LiveMapper and native UIDs.
    for pod in pods:
        name = pod["metadata"]["name"]
        if not re.fullmatch(r"[a-z0-9.-]+", name):
            raise RuntimeError("NATIVE_NAME_INVALID")
    samples = []
    matched = []
    for _ in range(30):
        time.sleep(2)
        try:
            client_ids = query("SHOW TAG pod VALUES FROM l7_flow_log WHERE pod='%s'" % client["metadata"]["name"])
            server_ids = query("SHOW TAG pod VALUES FROM l7_flow_log WHERE pod='%s'" % server["metadata"]["name"])
            if len(client_ids) != 1 or len(server_ids) != 1:
                continue
            left, right = int(client_ids[0]["value"]), int(server_ids[0]["value"])
            rows = query("SELECT pod_id_0,pod_id_1,pod_cluster_id_0,pod_cluster_id_1,pod_ns_id_0,pod_ns_id_1,tap_side,l7_protocol,type,response_code,Count(row) AS observations FROM l7_flow_log WHERE time >= %d AND time < %d AND pod_id_0 = %d AND pod_id_1 = %d GROUP BY pod_id_0,pod_id_1,pod_cluster_id_0,pod_cluster_id_1,pod_ns_id_0,pod_ns_id_1,tap_side,l7_protocol,type,response_code LIMIT 100" % (start, end, left, right))
            samples = rows
            for point in ("s-p", "c-p", "s", "c", "s-app", "c-app", "app"):
                observed = [r for r in rows if r["tap_side"] == point and r["l7_protocol"] == 20 and r["type"] == 2 and r.get("response_code") is not None and 200 <= r["response_code"] < 600]
                count = sum(r["observations"] for r in observed)
                errors = sum(r["observations"] for r in observed if r["response_code"] >= 500)
                if count == golden["responses"] and errors == golden["errors"] and count > 200:
                    matched = [point, observed[0]]
                    break
            if matched:
                break
        except (RuntimeError, urllib.error.URLError, TimeoutError):
            continue
    report = {"schemaVersion": "fault-chain-http-proof/v1", "namespace": NAMESPACE, "release": RELEASE, "golden": golden, "window": {"fromUnix": start, "toUnix": end}, "nativePods": [{"name": p["metadata"]["name"], "uid": p["metadata"]["uid"], "containerIds": [c["containerID"] for c in p["status"]["containerStatuses"]]} for p in pods], "metadataCounts": samples, "status": "PASS" if matched and golden["transportFailures"] == 0 else "FAIL", "timeReliable": False, "scope": "single_point_completed_http_session_observations"}
    if matched:
        point, row = matched
        report["observationPoint"] = point
        sql = "SELECT response_code,Count(row) AS observations,Sum(response_duration) AS duration_us FROM l7_flow_log WHERE time >= %d AND time < %d AND pod_cluster_id_0 = %d AND pod_cluster_id_1 = %d AND pod_ns_id_0 = %d AND pod_ns_id_1 = %d AND pod_id_0 = %d AND pod_id_1 = %d AND tap_side = '%s' AND l7_protocol = 20 AND type = 2 AND response_code >= 200 AND response_code < 600 GROUP BY response_code LIMIT 401" % (start, end, row["pod_cluster_id_0"], row["pod_cluster_id_1"], row["pod_ns_id_0"], row["pod_ns_id_1"], row["pod_id_0"], row["pod_id_1"], point)
        try:
            report["aggregate"] = query(sql)
            if sum(r["observations"] for r in report["aggregate"]) != golden["responses"] or sum(r["observations"] for r in report["aggregate"] if r["response_code"]>=500) != golden["errors"]:
                report["status"] = "FAIL"
        except RuntimeError:
            report["status"] = "FAIL"
            report["aggregateError"] = "FIXED_AGGREGATE_NOT_SUPPORTED"
    Path(args.report).write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({"status": report["status"], "responses": golden["responses"], "errors": golden["errors"], "observationPoint": report.get("observationPoint"), "report": args.report}))
    return 0 if report["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())

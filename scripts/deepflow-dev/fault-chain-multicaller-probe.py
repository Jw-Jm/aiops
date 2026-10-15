#!/usr/bin/env python3
"""Owned two-caller oracle; execute only on 192.168.0.63, D1 metadata only."""
import argparse
import concurrent.futures
import copy
import importlib.util
import json
import subprocess
import time
from pathlib import Path

spec = importlib.util.spec_from_file_location("probe", Path(__file__).with_name("fault-chain-probe.py"))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--report", required=True)
    args = parser.parse_args()
    manifest = json.loads(Path(__file__).with_name("fault-chain-canaries.json").read_text())
    client = copy.deepcopy(next(x for x in manifest["items"] if x["metadata"]["name"] == "fault-chain-client"))
    client["metadata"]["name"] = "fault-chain-client-b"
    for labels in (client["metadata"]["labels"], client["spec"]["selector"]["matchLabels"], client["spec"]["template"]["metadata"]["labels"]):
        labels["app"] = "fault-chain-client-b"
    subprocess.run(["kubectl", "apply", "-f", "-"], input=json.dumps(client), text=True, check=True)
    probe.kubectl("rollout", "status", "deployment/fault-chain-client-b", "--timeout=120s")
    pods = json.loads(probe.kubectl("get", "pods", "-l", "ops.platform.io/test-release=" + probe.RELEASE, "-o", "json"))["items"]
    if len(pods) != 3 or any(p["status"].get("phase") != "Running" for p in pods):
        raise RuntimeError("OWNED_RUNNING_TRIPLE_REQUIRED")
    server = next(p for p in pods if p["metadata"]["labels"]["app"] == "fault-chain-server")
    callers = sorted((p for p in pods if p != server), key=lambda p: p["metadata"]["name"])
    start = int(time.time()) + 3
    end = start + 60
    def stimulate(pair):
        index, pod = pair
        program = '''import time,json,urllib.request,urllib.error
start=%r;time.sleep(max(0,start-time.time()));codes={};failed=0
for n in range(1000):
 path="fail" if %r==0 and n%%20==0 else "health"
 try:
  with urllib.request.urlopen("http://%s:8080/"+path,timeout=3) as r:r.read();codes[r.status]=codes.get(r.status,0)+1
 except urllib.error.HTTPError as r:r.read();codes[r.code]=codes.get(r.code,0)+1;r.close()
 except Exception:failed+=1
print(json.dumps({"responses":sum(codes.values()),"errors":codes.get(503,0),"transportFailures":failed,"finishedAt":time.time()}))
''' % (start, index, server["status"]["podIP"])
        return json.loads(probe.kubectl("exec", pod["metadata"]["name"], "--", "python3", "-c", program))
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
        golden = list(executor.map(stimulate, enumerate(callers)))
    Path(args.report).write_text(json.dumps({"schemaVersion": "fault-chain-multicaller-proof/v1", "status": "FAIL", "stage": "source-not-yet-verified", "oracle": golden, "window": {"fromUnix": start, "toUnix": end}, "nativePods": [{"name": p["metadata"]["name"], "uid": p["metadata"]["uid"]} for p in pods]}, indent=2) + "\n")
    time.sleep(max(0, end - time.time()))
    server_id = probe.query("SHOW TAG pod VALUES FROM l7_flow_log WHERE pod='%s'" % server["metadata"]["name"])
    if len(server_id) != 1:
        raise RuntimeError("UNIQUE_SERVER_TAG_REQUIRED")
    measurements = []
    for attempt in range(30):
        measurements = []
        for pod in callers:
            ids = probe.query("SHOW TAG pod VALUES FROM l7_flow_log WHERE pod='%s'" % pod["metadata"]["name"])
            if len(ids) != 1:
                raise RuntimeError("UNIQUE_CALLER_TAG_REQUIRED")
            rows = probe.query("SELECT response_code,Count(row) AS observations,Sum(response_duration) AS duration_us FROM l7_flow_log WHERE time >= %d AND time < %d AND pod_id_0 = %d AND pod_id_1 = %d AND tap_side = 's-p' AND l7_protocol = 20 AND type = 2 AND response_code >= 200 AND response_code < 600 GROUP BY response_code LIMIT 401" % (start, end, ids[0]["value"], server_id[0]["value"]))
            measurements.append({"name": pod["metadata"]["name"], "uid": pod["metadata"]["uid"], "node": pod["spec"]["nodeName"], "responses": sum(r["observations"] for r in rows), "errors": sum(r["observations"] for r in rows if r["response_code"] >= 500), "rows": rows})
        if all(m["responses"] == g["responses"] == 1000 and m["errors"] == g["errors"] == (50 if i == 0 else 0) and g["transportFailures"] == 0 and g["finishedAt"] < end for i, (m, g) in enumerate(zip(measurements, golden))):
            break
        time.sleep(2)
    passed = len(measurements) == 2 and all(m["responses"] == 1000 and m["errors"] == (50 if i == 0 else 0) for i, m in enumerate(measurements)) and all(g["transportFailures"] == 0 and g["finishedAt"] < end for g in golden)
    report = {"schemaVersion": "fault-chain-multicaller-proof/v1", "status": "PASS" if passed else "FAIL", "golden": golden[0], "callers": measurements, "oracle": golden, "window": {"fromUnix": start, "toUnix": end}, "observationPoint": "s-p", "timeReliable": False, "sharedNode": len({p["spec"]["nodeName"] for p in pods}) == 1, "nativePods": [{"name": p["metadata"]["name"], "uid": p["metadata"]["uid"]} for p in pods], "serverUid": server["metadata"]["uid"], "namespace": probe.NAMESPACE, "release": probe.RELEASE}
    Path(args.report).write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({"status": report["status"], "responses": [m["responses"] for m in measurements], "errors": [m["errors"] for m in measurements], "sharedNode": report["sharedNode"]}))
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())

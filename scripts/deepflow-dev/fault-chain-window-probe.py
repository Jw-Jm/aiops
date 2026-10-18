#!/usr/bin/env python3
"""Three frozen owned windows with independent HTTP/TCP stimuli, D1 only.

Ingest is progressive, so the three windows are polled inside one bounded
settle budget, a complete poll is re-read once, and every window must
reconcile exactly. A poll that is still short is never treated as a pass; the
number of polls, the settle time and the last two measurements are recorded so
a failure can be attributed, and a transient source error still leaves a
report behind.
"""
import argparse
import json
import re
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

NS = "ops-assistant-deepflow-20261013"
RELEASE = "fault-chain-20261014"
POLL_SECONDS = 5
CONFIRM_SECONDS = 10
GROUP_LIMIT = 401


def kubectl(*args):
    return subprocess.check_output(["kubectl", "-n", NS, *args], text=True)


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
    data = body["result"]
    return [dict(zip(data["columns"], row)) for row in (data["values"] or [])]


def window_matches(measurement, golden):
    """One frozen window accounts for its own independent record."""
    return (
        measurement["responses"] == 1000
        and measurement["errors"] == int(golden.get("codes", {}).get("503", 0))
        and measurement["failureObservations"] == golden["refused"]
        and measurement["tcpRecords"] == 1000 + golden["refused"]
        and golden["transportFailures"] == 0
        and golden.get("uniqueClientPorts") == 1000 + golden["refused"]
    )


def reconcile(measurements, golden):
    """Every frozen window must reconcile; unequal lengths never pass."""
    return len(measurements) == len(golden) and all(
        window_matches(m, g) for m, g in zip(measurements, golden)
    )


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--report", required=True)
    parser.add_argument("--wait-seconds", type=int, default=240)
    args = parser.parse_args()
    if not 30 <= args.wait_seconds <= 600:
        raise ValueError("bounded settle window required")
    pods = json.loads(kubectl("get", "pods", "-l", "ops.platform.io/test-release=" + RELEASE, "-o", "json"))["items"]
    pods = [p for p in pods if p["metadata"]["labels"].get("app") in ("fault-chain-client", "fault-chain-server")]
    if len(pods) != 2 or any(p["status"].get("phase") != "Running" for p in pods):
        raise RuntimeError("OWNED_RUNNING_PAIR_REQUIRED")
    client = next(p for p in pods if p["metadata"]["labels"]["app"] == "fault-chain-client")
    server = next(p for p in pods if p["metadata"]["labels"]["app"] == "fault-chain-server")
    ip = server["status"]["podIP"]
    for pod in pods:
        if not re.fullmatch(r"[a-z0-9.-]+", pod["metadata"]["name"]):
            raise RuntimeError("NATIVE_NAME_INVALID")
    program = '''import socket,time,json,http.client,errno
used=set()
def fresh_socket():
 for _ in range(65536):
  s=socket.socket();s.settimeout(3);s.bind(("0.0.0.0",0));port=s.getsockname()[1]
  if port in used:s.close();continue
  used.add(port);return s
 raise RuntimeError("UNIQUE_CLIENT_PORTS_EXHAUSTED")
class Connection(http.client.HTTPConnection):
 def connect(self):
  self.sock=fresh_socket();self.sock.connect((self.host,self.port))
ip=%r;start=int(time.time())+2;out=[]
for phase in range(3):
 begin=start+phase*60;end=begin+60
 time.sleep(max(0,begin-time.time()));codes={};failures=0;refused=0;ports_before=len(used)
 for n in range(1000):
  path="fail" if (phase==0 and n%%100==0) or (phase==1 and n%%20==0) else "slow" if phase==1 and n%%10==1 else "health"
  connection=Connection(ip,8080,timeout=3)
  try:
   connection.request("GET","/"+path);r=connection.getresponse();codes[r.status]=codes.get(r.status,0)+1;r.read()
  except Exception:failures+=1
  finally:connection.close()
  # Pace this owned stimulus; all 1000 requests must still reconcile exactly.
  time.sleep(0.005)
 if phase==1:
  for n in range(100):
   s=fresh_socket();s.settimeout(1)
   try:s.connect((ip,8081))
   except OSError as e:
    if e.errno==errno.ECONNREFUSED:refused+=1
    else:failures+=1
   finally:s.close()
 finished=time.time();ports=len(used)-ports_before
 if ports!=1000+refused:raise RuntimeError("STIMULUS_CLIENT_PORTS_NOT_UNIQUE")
 out.append({"fromUnix":begin,"toUnix":end,"codes":codes,"transportFailures":failures,"refused":refused,"uniqueClientPorts":ports,"finishedAt":finished})
 if finished>=end:raise RuntimeError("CONTROLLED_BURST_EXCEEDED_WINDOW")
 time.sleep(max(0,end-time.time()))
print(json.dumps(out))
''' % ip
    golden = json.loads(kubectl("exec", client["metadata"]["name"], "--", "python3", "-c", program))
    # Preserve the independent stimulus even if the first backend read fails.
    Path(args.report).with_suffix(".golden.json").write_text(json.dumps({"golden": golden, "nativePods": [
        {"name": p["metadata"]["name"], "uid": p["metadata"]["uid"], "containerIds": [s["containerID"] for s in p["status"]["containerStatuses"]]}
        for p in pods]}, indent=2) + "\n")
    left = right = None

    def measure(g, timeout):
        where = "time >= %d AND time < %d AND pod_id_0 = %d AND pod_id_1 = %d" % (g["fromUnix"], g["toUnix"], left, right)
        http = query(
            "SELECT tap_side,response_code,Count(row) AS observations,Sum(response_duration) AS duration_us FROM l7_flow_log WHERE "
            + where
            + " AND l7_protocol = 20 AND type = 2 AND response_code >= 200 AND response_code < 600 GROUP BY tap_side,response_code LIMIT %d" % GROUP_LIMIT,
            timeout,
        )
        tcp = query(
            "SELECT tap_side,status,close_type,Count(row) AS observations,Sum(retrans_tx) AS retrans_tx,Sum(retrans_rx) AS retrans_rx FROM l4_flow_log WHERE "
            + where
            + " AND protocol = 6 GROUP BY tap_side,status,close_type LIMIT 257",
            timeout,
        )
        h = [r for r in http if r["tap_side"] == "s-p"]
        t = [r for r in tcp if r["tap_side"] == "s"]
        count = sum(r["observations"] for r in h)
        return {
            "http": http,
            "tcp": tcp,
            "responses": count,
            "errors": sum(r["observations"] for r in h if r["response_code"] >= 500),
            "meanMs": sum(r["duration_us"] for r in h) / count / 1000 if count else None,
            "failureObservations": sum(r["observations"] for r in t if r["status"] in (3, 4)),
            "tcpRecords": sum(r["observations"] for r in t),
            "retransmissions": sum(r["retrans_tx"] + r["retrans_rx"] for r in t),
        }

    measurements = []
    history = []
    passed = False
    stable = False
    polls = 0
    source_errors = []
    started = time.time()
    deadline = started + args.wait_seconds

    def bounded(cap):
        return max(1, min(cap, deadline - time.time()))

    while time.time() < deadline:
        polls += 1
        try:
            if left is None or right is None:
                # Backend IDs are oracle observations, never product input.
                l = query("SHOW TAG pod VALUES FROM l7_flow_log WHERE pod='%s'" % client["metadata"]["name"], bounded(20))
                r = query("SHOW TAG pod VALUES FROM l7_flow_log WHERE pod='%s'" % server["metadata"]["name"], bounded(20))
                if len(l) != 1 or len(r) != 1:
                    raise RuntimeError("UNIQUE_NATIVE_TAGS_REQUIRED")
                left, right = int(l[0]["value"]), int(r[0]["value"])
            measurements = [measure(g, bounded(20)) for g in golden]
        except (RuntimeError, urllib.error.URLError, TimeoutError) as error:
            source_errors.append({"poll": polls, "type": type(error).__name__})
            time.sleep(bounded(POLL_SECONDS))
            continue
        history.append(measurements)
        if reconcile(measurements, golden):
            # Re-read once: late duplicate rows on the same frozen window would
            # make a one-shot PASS no longer describe the stored population.
            time.sleep(bounded(CONFIRM_SECONDS))
            try:
                confirm = [measure(g, bounded(20)) for g in golden]
            except (RuntimeError, urllib.error.URLError, TimeoutError) as error:
                source_errors.append({"poll": polls, "type": type(error).__name__, "stage": "confirm"})
                continue
            if confirm == measurements:
                passed = True
                stable = True
                break
            history.append(confirm)
            measurements = confirm
            continue
        time.sleep(bounded(POLL_SECONDS))
    report = {
        "schemaVersion": "fault-chain-window-proof/v1",
        "status": "PASS" if passed else "FAIL",
        "namespace": NS,
        "release": RELEASE,
        "nativePods": [
            {"name": p["metadata"]["name"], "uid": p["metadata"]["uid"], "containerIds": [s["containerID"] for s in p["status"]["containerStatuses"]]}
            for p in pods
        ],
        "golden": golden,
        "measurements": measurements,
        "previousMeasurements": history[-2] if len(history) >= 2 else [],
        "observationPoints": {"http": "s-p", "tcp": "s"},
        "timeReliable": False,
        "polls": polls,
        "settleSeconds": round(time.time() - started),
        "stable": stable,
        "sourceErrors": source_errors,
        "tcpFailureDefinition": "status 3/4 records; not unique failed connections or failed user requests",
        "retransmissionDefinition": "sum of per-report-period tx/rx counts, one observation point; not packet-loss rate",
    }
    Path(args.report).write_text(json.dumps(report, indent=2) + "\n")
    print(
        json.dumps(
            {
                "status": report["status"],
                "responses": [m["responses"] for m in measurements],
                "errors": [m["errors"] for m in measurements],
                "tcpFailures": [m["failureObservations"] for m in measurements],
                "polls": polls,
                "settleSeconds": report["settleSeconds"],
                "stable": stable,
                "sourceErrors": len(source_errors),
            }
        )
    )
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())

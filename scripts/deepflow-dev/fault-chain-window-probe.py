#!/usr/bin/env python3
"""Three frozen owned windows with independent HTTP/TCP stimuli, D1 only."""
import argparse, json, math, re, subprocess, time, urllib.parse, urllib.request
from pathlib import Path
NS="ops-assistant-deepflow-20261013"
RELEASE="fault-chain-20261014"
def kubectl(*args):return subprocess.check_output(["kubectl","-n",NS,*args],text=True)
def query(sql):
 r=urllib.request.Request("http://127.0.0.1:21416/v1/query/",urllib.parse.urlencode({"db":"flow_log","sql":sql}).encode(),headers={"X-Org-Id":"1"})
 with urllib.request.urlopen(r,timeout=10) as response:v=json.load(response)
 if v.get("OPT_STATUS")!="SUCCESS":raise RuntimeError("QUERIER_CONTRACT_REJECTED")
 d=v["result"];return [dict(zip(d["columns"],row)) for row in (d["values"] or [])]
def main():
 p=argparse.ArgumentParser();p.add_argument("--report",required=True);a=p.parse_args()
 pods=json.loads(kubectl("get","pods","-l","ops.platform.io/test-release="+RELEASE,"-o","json"))["items"]
 if len(pods)!=2 or any(p["status"].get("phase")!="Running" for p in pods):raise RuntimeError("OWNED_RUNNING_PAIR_REQUIRED")
 client=next(p for p in pods if p["metadata"]["labels"]["app"]=="fault-chain-client")
 server=next(p for p in pods if p["metadata"]["labels"]["app"]=="fault-chain-server")
 ip=server["status"]["podIP"]
 for pod in pods:
  if not re.fullmatch(r"[a-z0-9.-]+",pod["metadata"]["name"]):raise RuntimeError("NATIVE_NAME_INVALID")
 program='''import socket,time,json,urllib.request,urllib.error,errno
ip=%r;start=int(time.time())+2;out=[]
for phase in range(3):
 begin=start+phase*60;end=begin+60
 time.sleep(max(0,begin-time.time()));codes={};failures=0;refused=0
 for n in range(1000):
  path="fail" if (phase==0 and n%%100==0) or (phase==1 and n%%20==0) else "slow" if phase==1 and n%%10==1 else "health"
  try:
   with urllib.request.urlopen("http://"+ip+":8080/"+path,timeout=3) as r:codes[r.status]=codes.get(r.status,0)+1;r.read()
  except urllib.error.HTTPError as r:codes[r.code]=codes.get(r.code,0)+1;r.read();r.close()
  except Exception:failures+=1
 if phase==1:
  for n in range(100):
   try:socket.create_connection((ip,8081),timeout=1).close()
   except OSError as e:
    if e.errno==errno.ECONNREFUSED:refused+=1
    else:failures+=1
 finished=time.time();out.append({"fromUnix":begin,"toUnix":end,"codes":codes,"transportFailures":failures,"refused":refused,"finishedAt":finished})
 if finished>=end:raise RuntimeError("CONTROLLED_BURST_EXCEEDED_WINDOW")
 time.sleep(max(0,end-time.time()))
print(json.dumps(out))
'''%ip
 golden=json.loads(kubectl("exec",client["metadata"]["name"],"--","python3","-c",program))
 # Numeric IDs are independent oracle observations, never supplied to product.
 left=query("SHOW TAG pod VALUES FROM l7_flow_log WHERE pod='%s'"%client["metadata"]["name"])
 right=query("SHOW TAG pod VALUES FROM l7_flow_log WHERE pod='%s'"%server["metadata"]["name"])
 if len(left)!=1 or len(right)!=1:raise RuntimeError("UNIQUE_NATIVE_TAGS_REQUIRED")
 left,right=int(left[0]["value"]),int(right[0]["value"])
 measurements=[];passed=False
 for attempt in range(30):
  measurements=[]
  for g in golden:
   where="time >= %d AND time < %d AND pod_id_0 = %d AND pod_id_1 = %d"%(g["fromUnix"],g["toUnix"],left,right)
   http=query("SELECT tap_side,response_code,Count(row) AS observations,Sum(response_duration) AS duration_us FROM l7_flow_log WHERE "+where+" AND l7_protocol = 20 AND type = 2 AND response_code >= 200 AND response_code < 600 GROUP BY tap_side,response_code LIMIT 401")
   tcp=query("SELECT tap_side,status,close_type,Count(row) AS observations,Sum(retrans_tx) AS retrans_tx,Sum(retrans_rx) AS retrans_rx FROM l4_flow_log WHERE "+where+" AND protocol = 6 GROUP BY tap_side,status,close_type LIMIT 257")
   h=[r for r in http if r["tap_side"]=="s-p"];t=[r for r in tcp if r["tap_side"]=="s"]
   count=sum(r["observations"] for r in h);errors=sum(r["observations"] for r in h if r["response_code"]>=500)
   failures=sum(r["observations"] for r in t if r["status"] in (3,4));records=sum(r["observations"] for r in t)
   measurements.append({"http":http,"tcp":tcp,"responses":count,"errors":errors,"meanMs":sum(r["duration_us"] for r in h)/count/1000 if count else None,"failureObservations":failures,"tcpRecords":records,"retransmissions":sum(r["retrans_tx"]+r["retrans_rx"] for r in t)})
  passed=all(m["responses"]==1000 and m["errors"]==int(g["codes"].get("503",0)) and m["failureObservations"]==g["refused"] and m["tcpRecords"]==1000+g["refused"] and g["transportFailures"]==0 for m,g in zip(measurements,golden))
  if passed:break
  time.sleep(2)
 report={"schemaVersion":"fault-chain-window-proof/v1","status":"PASS" if passed else "FAIL","namespace":NS,"release":RELEASE,"nativePods":[{"name":p["metadata"]["name"],"uid":p["metadata"]["uid"],"containerIds":[s["containerID"] for s in p["status"]["containerStatuses"]]} for p in pods],"golden":golden,"measurements":measurements,"observationPoints":{"http":"s-p","tcp":"s"},"timeReliable":False,"tcpFailureDefinition":"status 3/4 records; not unique failed connections or failed user requests","retransmissionDefinition":"sum of per-report-period tx/rx counts, one observation point; not packet-loss rate"}
 Path(a.report).write_text(json.dumps(report,indent=2)+"\n");print(json.dumps({"status":report["status"],"responses":[m["responses"] for m in measurements],"errors":[m["errors"] for m in measurements],"tcpFailures":[m["failureObservations"] for m in measurements]}));return 0 if passed else 1
if __name__=="__main__":raise SystemExit(main())

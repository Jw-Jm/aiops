"""Compare native fixed expressions with the exact UI time windows; no data writes."""
from pathlib import Path
import datetime,json,math,urllib.parse,urllib.request
w=Path('/data/ops-v12-native-visual-r24-20261026/r51')
exec((w/'qualify-source.py').read_text().split('meta=json.loads')[0])
browser=json.loads((w/'browser-metrics.json').read_text());assert browser['status'].startswith('passed')
checks=[]
for item in browser['queries']:
 intent=item['intent'];uid=item['canonicalId'].split('/')[-1]
 labels={'tenant':s['tenantId'],'cluster':s['clusterUid'],'collector':'victoriametrics-kubernetes/v1','namespace':s['namespace'],'uid':uid}
 selector='{'+','.join(k+'='+json.dumps(v) for k,v in sorted(labels.items()))+'}'
 containers=selector[:-1]+',container!="",container!="POD"}'
 expressions={'metrics.pod-cpu':'sum(rate(container_cpu_usage_seconds_total'+containers+'[5m]))','metrics.pod-memory':'sum(last_over_time(container_memory_working_set_bytes'+containers+'[5m]))','metrics.pod-restarts':'sum(increase(kube_pod_container_status_restarts_total'+selector+'[60s]))','metrics.pod-phase':'sum(last_over_time(kube_pod_status_phase'+selector+'[5m]) == bool 1)'}
 q=expressions[intent['datasetId']]
 params={'query':q,'start':intent['timeRange']['from'],'end':intent['timeRange']['to'],'step':'60','nocache':'1'}
 req=urllib.request.Request(s['endpoint']+'/api/v1/query_range?'+urllib.parse.urlencode(params),headers=headers)
 with urllib.request.urlopen(req,context=ctx,timeout=5)as response:d=json.load(response)
 assert d['status']=='success' and len(d['data']['result'])==1
 actual=d['data']['result'][0]['values'];expected=item['result']['rows'];assert len(actual)==len(expected)==3
 for (timestamp,value),row in zip(actual,expected):
  assert timestamp==datetime.datetime.fromisoformat(row['dimension'].replace('Z','+00:00')).timestamp()
  assert math.isclose(float(value),row['value'],rel_tol=1e-9,abs_tol=1e-9),(intent['datasetId'],value,row)
 checks.append({'datasetId':intent['datasetId'],'nativeExpression':q,'timeRange':intent['timeRange'],'matchedPoints':len(actual)})
out={'checkedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'passed','checks':checks,'nativeDataWrites':0}
(w/'oracle-check.json').write_text(json.dumps(out,indent=2));print(json.dumps(out))

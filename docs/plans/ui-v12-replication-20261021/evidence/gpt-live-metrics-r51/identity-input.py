from pathlib import Path
import datetime,json
w=Path('/data/ops-v12-native-visual-r24-20261026/r51')
exec((w/'qualify-source.py').read_text().split('meta=json.loads')[0])
name='ops-r51-ksm';pod=json.loads(kube('get','pod',name,'-n',ns,'-o','json'));current=pod['metadata']['uid']
rows=query('max_over_time(container_memory_working_set_bytes{collector="victoriametrics-kubernetes/v1",namespace="'+ns+'",pod="'+name+'",container!="",container!="POD"}[1h])')
uids=sorted({r['metric']['uid']for r in rows});assert current in uids and len(uids)==2,uids
old=next(u for u in uids if u!=current);fault=json.loads((w/'recovery-check.json').read_text());end=datetime.datetime.fromisoformat(fault['startedAt']).replace(microsecond=0)-datetime.timedelta(seconds=60)
out={'name':name,'currentUID':current,'previousUID':old,'nativeCurrentCreation':pod['metadata']['creationTimestamp'],'historicalWindow':{'from':(end-datetime.timedelta(seconds=120)).isoformat(),'to':end.isoformat(),'timezone':'Asia/Shanghai'}}
(w/'identity-input.json').write_text(json.dumps(out,indent=2));print(json.dumps(out))

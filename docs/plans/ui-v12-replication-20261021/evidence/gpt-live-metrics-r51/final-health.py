"""Native read-only health, projected-token rotation and least-privilege assertions."""
from pathlib import Path
import base64,datetime,json,time
w=Path('/data/ops-v12-native-visual-r24-20261026/r51')
exec((w/'qualify-source.py').read_text().split('meta=json.loads')[0])
receipts=[]
for name in ['ops-r51-ksm','ops-r51-metrics']:
 pod=json.loads(kube('get','pod',name,'-n',ns,'-o','json'));uid=pod['metadata']['uid'];assert pod['status']['phase']=='Running' and all(c['ready'] and c['restartCount']==0 for c in pod['status']['containerStatuses'])
 token=Path('/data/kubelet/pods')/uid/'volumes/kubernetes.io~projected/api-access/token'
 raw=token.resolve(strict=True).read_text().split('.')[1];payload=json.loads(base64.urlsafe_b64decode(raw+'='*(-len(raw)%4)))
 assert payload['kubernetes.io']['pod']['uid']==uid and payload['iat']<time.time()<payload['exp']
 created=datetime.datetime.fromisoformat(pod['metadata']['creationTimestamp'].replace('Z','+00:00')).timestamp()
 assert payload['iat']>created+60,(name,'token has not rotated yet')
 receipts.append({'name':name,'uid':uid,'phase':pod['status']['phase'],'containerRestarts':0,'createdAt':pod['metadata']['creationTimestamp'],'tokenIssuedAt':payload['iat'],'tokenExpiresAt':payload['exp'],'rotationAfterPodCreationVerified':True,'boundPodUIDMatched':True})
rbac=[]
for sa,verb,resource,extra,expected in [('ops-r51-metrics','get','nodes/mssc',['--subresource=metrics'],True),('ops-r51-metrics','get','nodes/mssc',['--subresource=proxy'],False),('ops-r51-metrics','list','secrets',[],False),('ops-r51-ksm','list','pods',[],True),('ops-r51-ksm','watch','pods',[],True),('ops-r51-ksm','get','pods',['--subresource=log'],False),('ops-r51-ksm','list','secrets',[],False),('ops-r51-ksm','delete','pods',[],False)]:
 p=subprocess.run(['kubectl','auth','can-i',verb,resource,'--as=system:serviceaccount:'+ns+':'+sa,'-n',ns,*extra],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 allowed=p.stdout.decode().strip()=='yes';assert allowed==expected,(sa,resource,p.stdout.decode());rbac.append({'sa':sa,'verb':verb,'resource':resource,'extra':extra,'allowed':allowed})
rows=query('topk by (__name__) (1, timestamp({collector="victoriametrics-kubernetes/v1",uid!=""}) keep_metric_names)');assert len(rows)==4 and all(0<=time.time()-float(r['value'][1])<45 for r in rows)
containers=subprocess.check_output(['docker','ps','--format','{{.Names}}']).decode().splitlines()
assert 'ops-v12-native-r51b-web-30471'in containers
out={'checkedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'passed','pods':receipts,'rbac':rbac,'allFourFamiliesFreshAfterTokenRotation':True,'runningContainers':containers}
(w/'final-health.json').write_text(json.dumps(out,indent=2));print(json.dumps({'status':'passed','pods':receipts,'rbacChecks':len(rbac),'allFourFamiliesFreshAfterTokenRotation':True}))

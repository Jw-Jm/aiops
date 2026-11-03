"""Bind only existing read-only resource rules in the new task namespace."""
import json,subprocess
from pathlib import Path
ns='ops-native-v12-r24-20261026';second='ops-native-v12-r35-scope';outside='ops-native-v12-r35-outside';actor='system:serviceaccount:'+ns+':ops-worker'
root=Path('/data/ops-v12-native-visual-r24-20261026/r35')
def review(namespace,verb,resource):
 p=subprocess.run(['kubectl','auth','can-i',verb,resource,'-n',namespace,'--as='+actor],capture_output=True,text=True);return p.stdout.strip()
proof={'before':{'secondPods':review(second,'list','pods'),'outsidePods':review(outside,'list','pods')}}
role=json.loads(subprocess.check_output(['kubectl','get','role','r32-sp04-collect','-n',ns,'-o','json']))
for rule in role['rules']:
 assert set(rule['verbs'])<=set(['get','list','watch']) and 'secrets' not in rule['resources']
role={'apiVersion':'rbac.authorization.k8s.io/v1','kind':'Role','metadata':{'name':'r35-sp04-collect','namespace':second},'rules':role['rules']}
binding={'apiVersion':'rbac.authorization.k8s.io/v1','kind':'RoleBinding','metadata':role['metadata'],'roleRef':{'apiGroup':'rbac.authorization.k8s.io','kind':'Role','name':'r35-sp04-collect'},'subjects':[{'kind':'ServiceAccount','name':'ops-worker','namespace':ns}]}
for obj in [role,binding]:subprocess.run(['kubectl','create','-f','-'],input=json.dumps(obj).encode(),check=True,stdout=subprocess.DEVNULL)
proof['role']=role;proof['binding']=binding;proof['after']={k:review(n,v,r) for k,n,v,r in [('secondPods',second,'list','pods'),('outsidePods',outside,'list','pods'),('secrets',second,'get','secrets'),('podWrite',second,'delete','pods')]}
assert proof['after']=={'secondPods':'yes','outsidePods':'no','secrets':'no','podWrite':'no'}
(root/'namespace-rbac.json').write_text(json.dumps(proof,indent=2));print(json.dumps({'before':proof['before'],'after':proof['after']}))

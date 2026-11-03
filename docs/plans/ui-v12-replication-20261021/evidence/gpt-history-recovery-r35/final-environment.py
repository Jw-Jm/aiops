from pathlib import Path
import hashlib,json,os,socket,subprocess,urllib.request,urllib.error
assert socket.gethostname()=='mssc'
root=Path('/data/ops-v12-native-visual-r24-20261026');work=root/'r35'
env=os.environ|{'PGPASSWORD':(root/'private/pg-password').read_text().strip()}
query="SELECT json_build_object('database',current_database(),'postgres',current_setting('server_version'),'schema',(SELECT max(version_id) FROM goose_db_version WHERE is_applied))"
database=json.loads(subprocess.check_output(['docker','exec','-e','PGPASSWORD','ops-v12-native-r24-pg','psql','-U','postgres','-d','ui_v12_r24_native','-tA','-c',query],env=env))
assert database['schema']==70
receipt=json.loads((work/'runtime-final.json').read_text());receipt['databaseVerified']=database;receipt['processes']=[]
for ns in ['ops-native-v12-r24-20261026','ops-native-v12-r34-20261027']:
 for name in ['ops-api','ops-worker']:
  pod=json.loads(subprocess.check_output(['kubectl','get','pod',name,'-n',ns,'-o','json']))
  c=pod['spec']['containers'][0];binary=Path(c['command'][0])
  receipt['processes'].append({'namespace':ns,'name':name,'uid':pod['metadata']['uid'],'image':c['image'],'command':str(binary),'sha256':hashlib.sha256(binary.read_bytes()).hexdigest()})
for port in [19093,19097]:
 with urllib.request.urlopen('http://127.0.0.1:'+str(port)+'/readyz',timeout=5) as response:assert response.status==200
receipt['workerReadiness']={'r24':200,'r34':200};receipt['scope']='source canary mechanisms; credentials private; no immutable release or whole product acceptance'
(work/'runtime-final.json').write_text(json.dumps(receipt,indent=2));print(json.dumps({'database':database,'workerReadiness':receipt['workerReadiness']}))

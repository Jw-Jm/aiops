"""Deploy tested source canaries to task previews; preserve previous artifacts."""
from pathlib import Path
import datetime,hashlib,json,socket,subprocess,tarfile
assert socket.gethostname()=='mssc'
work=Path('/data/ops-v12-native-visual-r24-20261026/r35');receipts=[]
for ns in ['ops-native-v12-r24-20261026','ops-native-v12-r34-20261027']:
 p=json.loads(subprocess.check_output(['kubectl','get','pod','ops-api','-n',ns,'-o','json']))
 manifest={'apiVersion':'v1','kind':'Pod','metadata':{'name':'ops-api','namespace':ns,'labels':p['metadata'].get('labels',{})},'spec':p['spec']}
 (work/('api-before-'+ns+'.json')).write_text(json.dumps(manifest,indent=2))
 c=manifest['spec']['containers'][0];old=c['command'][0]
 mount=next(m for m in c['volumeMounts'] if m['mountPath']==old)
 volume=next(v for v in manifest['spec']['volumes'] if v['name']==mount['name'])
 volume['hostPath']['path']=str(work/'ops-v12-r35-api');mount['mountPath']=str(work/'ops-v12-r35-api');c['command']=[str(work/'ops-v12-r35-api')]
 path=work/('api-r35-'+ns+'.json');path.write_text(json.dumps(manifest,indent=2))
 subprocess.run(['kubectl','delete','pod','ops-api','-n',ns,'--wait=true'],check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','create','-f',str(path)],check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['kubectl','wait','--for=condition=Ready','pod/ops-api','-n',ns,'--timeout=120s'],check=True,stdout=subprocess.DEVNULL)
 receipts.append({'component':'API','namespace':ns,'binarySHA256':hashlib.sha256((work/'ops-v12-r35-api').read_bytes()).hexdigest()})
for root,previous,name,port in [
 (Path('/data/ops-v12-native-visual-r24-20261026'),'ops-v12-native-r30-web-r14','ops-v12-native-r35-web-30471',30471),
 (Path('/data/ops-v12-native-cold-r34-20261027'),'ops-v12-native-r34-web-r4','ops-v12-native-r35-web-30472',30472)]:
 old=json.loads(subprocess.check_output(['docker','inspect',previous]))[0]
 assert old['State']['Running'] and old['HostConfig']['NetworkMode']=='host' and old['Config']['User']=='101:101' and old['HostConfig']['ReadonlyRootfs']
 web=root/'web-r35';web.mkdir(mode=0o755)
 with tarfile.open(work/'ops-v12-r35-web.tgz') as archive:archive.extractall(web,filter='data')
 oldweb=next(m['Source'] for m in old['Mounts'] if m['Destination']=='/preview/web')
 (web/'runtime-config.json').write_bytes((Path(oldweb)/'runtime-config.json').read_bytes())
 args=['docker','run','-d','--name',name,'--network','host','--read-only','--user','101:101','--restart','unless-stopped','--tmpfs','/tmp:rw,noexec,nosuid,size=64m,mode=1777']
 for opt in old['HostConfig'].get('SecurityOpt') or []:args+=['--security-opt',opt]
 for cap in old['HostConfig'].get('CapDrop') or []:args+=['--cap-drop',cap]
 for m in old['Mounts']:
  assert m['Type']=='bind' and not m['RW']
  source=str(web) if m['Destination']=='/preview/web' else m['Source']
  args+=['--mount',f'type=bind,source={source},target={m["Destination"]},readonly']
 args+=['--entrypoint','/usr/sbin/nginx',old['Config']['Image'],'-e','/dev/stderr','-c','/etc/nginx/nginx.conf','-g','daemon off;']
 subprocess.run(['docker','stop',previous],check=True,stdout=subprocess.DEVNULL)
 try:
  subprocess.run(args,check=True,stdout=subprocess.DEVNULL)
  subprocess.run(['curl','-skf','--retry','5','--retry-connrefused','--retry-delay','1','--output','/dev/null',f'https://192.168.0.63:{port}/login'],check=True)
 except Exception:
  subprocess.run(['docker','stop',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
  subprocess.run(['docker','start',previous],check=True,stdout=subprocess.DEVNULL)
  raise
 receipts.append({'component':'Web','container':name,'previousStoppedPreserved':previous,'preview':f'https://192.168.0.63:{port}/login','indexSHA256':hashlib.sha256((web/'index.html').read_bytes()).hexdigest()})
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'artifacts':receipts,'workerSHA256':hashlib.sha256((work/'ops-v12-r35-worker').read_bytes()).hexdigest(),'schema':70,'immutableRelease':False}
(work/'runtime-final.json').write_text(json.dumps(receipt,indent=2));print(json.dumps(receipt))

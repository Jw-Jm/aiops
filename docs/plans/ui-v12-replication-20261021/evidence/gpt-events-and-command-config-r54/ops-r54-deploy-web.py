from pathlib import Path
import datetime,hashlib,json,socket,subprocess,tarfile
assert socket.gethostname()=="mssc"
work=Path("/data/ops-v12-native-visual-r24-20261026/r54");receipts=[]
for root,previous,name,port in [
 (Path('/data/ops-v12-native-visual-r24-20261026'),'ops-v12-native-r51b-web-30471','ops-v12-native-r54-web-30471',30471)]:
 old=json.loads(subprocess.check_output(['docker','inspect',previous]))[0]
 assert old['State']['Running'] and old['HostConfig']['NetworkMode']=='host' and old['Config']['User']=='101:101' and old['HostConfig']['ReadonlyRootfs']
 web=root/'web-r54';web.mkdir(mode=0o755,exist_ok=True)
 with tarfile.open(work/'ops-r54-web.tgz') as archive:archive.extractall(web,filter='data')
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

(work/"web-runtime-r54.json").write_text(json.dumps({"at":datetime.datetime.now(datetime.timezone.utc).isoformat(),"artifacts":receipts},indent=2))
print(json.dumps(receipts))

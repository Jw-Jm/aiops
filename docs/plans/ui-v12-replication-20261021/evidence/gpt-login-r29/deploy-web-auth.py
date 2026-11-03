from pathlib import Path
import datetime,hashlib,json,socket,subprocess,tarfile
root=Path('/data/ops-v12-native-visual-r24-20261026')
run=root/'r29-login'
assert socket.gethostname()=='mssc' and root.stat().st_uid==0
old=json.loads(subprocess.check_output(['docker','inspect','ops-v12-native-r29-web-r8']))[0]
assert old['State']['Running'] and old['HostConfig']['NetworkMode']=='host' and old['HostConfig']['ReadonlyRootfs']
assert old['Config']['User']=='101:101'
effective=old['Config']['Entrypoint']+(old['Config']['Cmd'] or [])
assert effective==['/usr/sbin/nginx','-e','/dev/stderr','-g','daemon off;']
web=root/'web-r9';web.mkdir(mode=0o755)
with tarfile.open(run/'ops-r29-web-auth.tgz') as archive:archive.extractall(web,filter='data')
(web/'runtime-config.json').write_bytes((root/'web-r8/runtime-config.json').read_bytes())
args=['docker','run','-d','--name','ops-v12-native-r29-web-r9','--network','host','--read-only','--user','101:101','--restart','unless-stopped','--tmpfs','/tmp:rw,noexec,nosuid,size=64m']
assert {m['Destination'] for m in old['Mounts']}=={'/preview/tls','/preview/web','/etc/nginx/nginx.conf'}
for mount in old['Mounts']:
 assert mount['Type']=='bind' and not mount['RW']
 source=str(web) if mount['Destination']=='/preview/web' else mount['Source']
 args+=['--mount',f'type=bind,source={source},target={mount["Destination"]},readonly']
args+=['--entrypoint',old['Config']['Entrypoint'][0],old['Config']['Image'],*effective[1:]]
subprocess.run(['docker','stop','ops-v12-native-r29-web-r8'],check=True,stdout=subprocess.DEVNULL)
try:
 subprocess.run(args,check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['curl','--silent','--show-error','--fail','--insecure','--retry','5','--retry-connrefused','--retry-delay','1','--output','/dev/null','https://192.168.0.63:30471/login'],check=True)
except Exception:
 subprocess.run(['docker','stop','ops-v12-native-r29-web-r9'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 subprocess.run(['docker','start','ops-v12-native-r29-web-r8'],check=True,stdout=subprocess.DEVNULL)
 raise
receipt={'date':datetime.datetime.now(datetime.timezone.utc).isoformat(),'container':'ops-v12-native-r29-web-r9','web':str(web),'origin':'https://192.168.0.63:30471','indexSHA256':hashlib.sha256((web/'index.html').read_bytes()).hexdigest(),'archiveSHA256':hashlib.sha256((run/'ops-r29-web-auth.tgz').read_bytes()).hexdigest(),'image':old['Config']['Image'],'previousContainer':'ops-v12-native-r29-web-r8 stopped and preserved','APIWorkerDatabase':'r28 API-query/Worker + real investigator source-4; isolated PG15454/schema70'}
(run/'web-runtime-r9.json').write_text(json.dumps(receipt,indent=2)+'\n')
print(json.dumps(receipt))

"""Rebuild both migration targets with no network and initially empty caches on mssc."""
from pathlib import Path
import datetime,hashlib,json,os,socket,subprocess,tarfile,zipfile
assert socket.gethostname()=='mssc'
w=Path('/data/ops-migration-renew-r50-20261103');material=w/'material';toolchain=w/'toolchain'
image='ops.local/amd64-poc/postgresql-bookworm:17.11'
with zipfile.ZipFile(material/'v0.0.1-go1.27.1.linux-amd64.zip')as z:
 z.extractall(toolchain)
 for item in z.infolist():
  if not item.is_dir():os.chmod(toolchain/item.filename,(item.external_attr>>16)&0o777 or 0o644)
goroot=toolchain/'golang.org/toolchain@v0.0.1-go1.27.1.linux-amd64'
for arch in ['amd64','arm64']:
 work=w/arch;work.mkdir(exist_ok=False)
 archive=material/('source-'+arch+'.tar')
 with tarfile.open(archive)as t:t.extractall(work,filter='data')
 for directory in ['module-cache','build-cache']:(work/directory).mkdir()
 assert not list((work/'module-cache').iterdir()) and not list((work/'build-cache').iterdir())
 name='ops-r50-migration-build-'+arch
 command=['docker','run','--name',name,'--pull=never','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user',str(os.getuid())+':'+str(os.getgid()),'--memory','2g','--cpus','2','--tmpfs','/tmp:rw,nosuid,nodev,size=1g,mode=1777','--mount',f'type=bind,source={goroot},target=/toolchain,readonly','--mount',f'type=bind,source={work},target=/src','-w','/src']
 for key,value in {'GOROOT':'/toolchain','GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off','GOMODCACHE':'/src/module-cache','GOCACHE':'/src/build-cache','GOOS':'linux','GOARCH':arch,'CGO_ENABLED':'0'}.items():command+=['-e',key+'='+value]
 command+=['--entrypoint','/toolchain/bin/go',image,'build','-mod=mod','-trimpath','-buildvcs=false','-o','/src/db-migrate','./cmd/db-migrate']
 p=subprocess.run(command,stdout=subprocess.PIPE,stderr=subprocess.STDOUT);(w/('offline-rebuild-'+arch+'.log')).write_bytes(p.stdout)
 state=json.loads(subprocess.check_output(['docker','inspect',name]))[0]
 binary=work/'db-migrate';digest='sha256:'+hashlib.sha256(binary.read_bytes()).hexdigest()if binary.exists()else None
 receipt={'architecture':'linux/'+arch,'observedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'goVersion':'go1.27.1','command':command,'exitCode':p.returncode,'network':state['HostConfig']['NetworkMode'],'initialModuleCacheEmpty':True,'initialBuildCacheEmpty':True,'sourceBundleSHA256':'sha256:'+hashlib.sha256(archive.read_bytes()).hexdigest(),'binarySHA256':digest,'migrationsIncluded':'00001-00072','nativeSQLRun':False}
 (w/('offline-rebuild-'+arch+'.json')).write_text(json.dumps(receipt,indent=2));print(json.dumps(receipt),flush=True)
 assert p.returncode==0 and receipt['network']=='none'
 subprocess.run(['docker','rm',name],check=True,stdout=subprocess.DEVNULL)

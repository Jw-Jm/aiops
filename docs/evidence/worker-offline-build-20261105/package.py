import pathlib,hashlib,json,tarfile,shutil,subprocess,datetime
root=pathlib.Path('/data/ops-worker-build-20261105');repo=root/'source';ctx=root/'image';ctx.mkdir(exist_ok=True)
def sha(p):
 h=hashlib.sha256()
 with p.open('rb') as f:
  for b in iter(lambda:f.read(1024*1024),b''):h.update(b)
 return 'sha256:'+h.hexdigest()
lock_path=repo/'third_party/admission/sp05-k8sgpt-runtime-source-amd64.lock.json';lock=json.loads(lock_path.read_text())
analyzer=pathlib.Path('/data/ops-k8sgpt-material-20261105/amd64/k8sgpt')
source=pathlib.Path('/opt/ops-amd64-validation-20261009/bundle-inputs/1e79bee49bab0acf5ba40b0ad732bccb7d9a310105c57aa571d414e001956948-k8sgpt-runtime-source-amd64.tar')
assert sha(analyzer)==lock['binarySHA256'];assert sha(source)==lock['correspondingSourceBundleSHA256']
shutil.copy2(root/'ops-worker',ctx/'ops-worker');shutil.copy2(analyzer,ctx/'k8sgpt')
for name in ['ops-worker','k8sgpt']:(ctx/name).chmod(0o555)
shutil.copytree(pathlib.Path('/data/ops-k8sgpt-material-20261105/amd64/legal'),ctx/'legal',dirs_exist_ok=True)
legal=ctx/'legal/k8sgpt';shutil.rmtree(legal);legal.mkdir()
with tarfile.open(source) as t:
 for m in t.getmembers():
  if not m.isfile() or not(m.name.startswith('notices/') or m.name=='toolchain/go1.27.1/LICENSE'):continue
  p=legal/m.name
  if not p.resolve().is_relative_to(legal.resolve()):raise ValueError('unsafe notice path')
  p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(t.extractfile(m).read())
shutil.copy2(lock_path,legal/'k8sgpt-runtime-source-amd64.lock.json')
(legal/'SOURCE-DISTRIBUTION.json').write_text(json.dumps({'sourceMaterial':str(source),'sourceSHA256':sha(source),'binarySHA256':sha(analyzer),'closureLock':'k8sgpt-runtime-source-amd64.lock.json','state':'must-accompany-distributed-image','pyrca':'disabled-excluded'},indent=2)+'\n')
(ctx/'Dockerfile').write_text('FROM scratch\nCOPY --chown=65532:65532 ops-worker /ops-worker\nCOPY --chown=65532:65532 k8sgpt /opt/ops/bin/k8sgpt\nCOPY legal /usr/share/ops/legal\nUSER 65532:65532\nENTRYPOINT ["/ops-worker"]\n')
tag='ops.local/worker-build-check:20261105-amd64'
r=subprocess.run(['docker','build','--network=none','--pull=false','--platform=linux/amd64','-t',tag,str(ctx)],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True);(root/'image-build.log').write_text(r.stdout);assert r.returncode==0,r.stdout[-2000:]
info=json.loads(subprocess.check_output(['docker','image','inspect',tag]))[0];assert info['Architecture']=='amd64' and info['Config']['User']=='65532:65532'
receipt={'verifiedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'sourceCommit':'60a9824c3c41d103ed347c8a1da70b69c7f63f8f','architecture':'linux/amd64','workerSHA256':sha(root/'ops-worker'),'analyzerSHA256':sha(analyzer),'sourceBundleSHA256':sha(source),'sourceBundle':str(source),'image':tag,'imageID':info['Id'],'compile':'Go 1.27.1; unshare --net; GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local; CGO_ENABLED=0; -mod=readonly -trimpath -buildvcs=false -ldflags=-s -w','package':'--network=none --pull=false','scope':'complete production Worker executable and image, retained cached modules, no deployment; not empty-cache or full bundle distribution acceptance'}
(root/'build.json').write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps(receipt,indent=2))

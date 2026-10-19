from pathlib import Path
import copy,hashlib,io,json,os,shutil,subprocess,tarfile,time
os.umask(0o077)
base=Path('/opt/ops-verification-20261019-1a7c7da');out=base/'distribution';repo=base/'repo';candidate=base/'candidate';revision='1a7c7da4af7d74d80df0df0aec72868cbe2785cc'
def sha(p):return 'sha256:'+hashlib.file_digest(Path(p).open('rb'),'sha256').hexdigest()
def run(args,name,timeout=180):
 with (out/(name+'.log')).open('w') as log:
  subprocess.run(args,stdout=log,stderr=subprocess.STDOUT,timeout=timeout,check=True)
def export(name,tag):
 raw=out/(name+'.raw.oci.tar');dest=out/(name+'.oci.tar')
 run(['skopeo','copy','--format','oci','docker-daemon:'+tag,'oci-archive:'+str(raw)+':candidate'],name+'-export')
 with tarfile.open(raw) as source,tarfile.open(dest,'w') as target:
  index=json.load(source.extractfile('index.json'));assert len(index['manifests'])==1
  descriptor=index['manifests'][0];digest=descriptor['digest'];reference='ops.local/verification-20261019/'+name+'@'+digest
  descriptor['annotations']={'org.opencontainers.image.ref.name':reference}
  for member in source:
   if member.name=='index.json':continue
   assert member.name in ['oci-layout','blobs','blobs/sha256'] or member.name.startswith('blobs/sha256/')
   target.addfile(copy.copy(member),source.extractfile(member)if member.isfile()else None)
  data=json.dumps(index,separators=(',',':')).encode();m=tarfile.TarInfo('index.json');m.mode=0o644;m.size=len(data);target.addfile(m,io.BytesIO(data))
 return {'name':name,'imageDigest':digest,'reference':reference,'archiveSHA256':sha(dest),'archiveBytes':dest.stat().st_size,'sourceCommit':revision,'buildNetwork':'none','runtimeQualification':'pending'}
with tarfile.open(out/'go-distribution-inputs.tar.gz')as package:package.extractall(out/'go-inputs',filter='data')
spec=json.loads(Path('/data/ops-amd64-final-20261010/build-spec-r12.json').read_text())
receipts=[]
for name in ['platform-api','platform-worker','command-runner','holmesgpt']:
 try:
  ctx=out/(name+'-context');ctx.mkdir(exist_ok=False)
  if name in ['platform-api','platform-worker']:
   shutil.copy2(candidate/'binaries'/name,ctx/'ops-process');(ctx/'ops-process').chmod(0o555)
   shutil.copytree(out/'go-inputs/notices',ctx/'notices')
   extra=''
   if name=='platform-worker':
    material=next(x for x in spec['materials']if x['name']==name);item=next(x for x in spec['files']if x['path']==material['payloadRef']);archive=Path(item['source']);assert sha(archive)==item['digest']
    with tarfile.open(archive)as image:
     index=json.load(image.extractfile('index.json'));root=index['manifests'][0];blob=lambda d:image.extractfile('blobs/sha256/'+d.split(':')[1]);manifest=json.load(blob(root['digest']))
     if 'manifests'in manifest:manifest=json.load(blob(next(x['digest']for x in manifest['manifests']if x.get('platform',{}).get('architecture')=='amd64')))
     for layer in manifest['layers']:
      with tarfile.open(fileobj=blob(layer['digest']),mode='r|*')as package:
       for m in package:
        path=m.name.removeprefix('./')
        if path=='opt/ops/bin/k8sgpt' or path.startswith('usr/share/ops/legal/'):
         assert '..'not in Path(path).parts and not Path(path).is_absolute()
         if m.isfile():
          target=ctx/'worker-runtime'/path;target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(package.extractfile(m).read());target.chmod(m.mode & 0o755)
    assert sha(ctx/'worker-runtime/opt/ops/bin/k8sgpt')=='sha256:6f9152ff31d2692a14e880ae73c2fe35dbc2938560d2c55227f4b0c67409af53'
    extra='COPY worker-runtime/ /\n'
   dockerfile='FROM scratch\nCOPY --chown=65532:65532 ops-process /ops-process\nCOPY notices /usr/share/ops/legal/go\n'+extra+'USER 65532:65532\nENTRYPOINT ["/ops-process"]\n'
   (ctx/'Dockerfile').write_text(dockerfile)
  elif name=='command-runner':
   image=json.loads(subprocess.check_output(['docker','image','inspect','ops.local/amd64-poc/command-runner:20261009-r2']))[0];parent=image['Id']
   shutil.copy2(candidate/'binaries/command-runner',ctx/'command-runner');(ctx/'command-runner').chmod(0o555)
   shutil.copytree(out/'go-inputs/notices',ctx/'notices')
   (ctx/'Dockerfile').write_text('FROM '+parent+'\nCOPY --chown=65532:65532 command-runner /ops-command-runner\nCOPY notices /usr/share/ops/legal/go\nUSER 65532:65532\nENTRYPOINT ["/ops-command-runner"]\n')
   m=next(x for x in spec['materials']if x['name']=='command-runner-source');f=next(x for x in spec['files']if x['path']==m['payloadRef']);original=Path(f['source']);assert sha(original)==f['digest']
   with tarfile.open(original)as src,tarfile.open(out/'command-runner-source.tar','w')as dst:
    for member in src:dst.addfile(copy.copy(member),src.extractfile(member)if member.isfile()else None)
    dst.add(out/'go-inputs/runtime-source/runtime-go-source.tar',arcname='platform-update/runtime-go-source.tar');dst.add(out/'go-inputs/source.tar.gz',arcname='platform-update/platform-source.tar.gz');dst.add(ctx/'Dockerfile',arcname='platform-update/Dockerfile')
  else:
   old=Path('/data/ops-amd64-final-20261010/investigator-r12')
   run(['cp','--reflink=auto','-a',str(old)+'/.',str(ctx)],name+'-context-copy')
   lock=json.loads((ctx/'runtime.lock.json').read_text());assert lock['uvLockSHA256']==sha(repo/'services/investigator/uv.lock')
   shutil.rmtree(ctx/'wrapper');shutil.copytree(repo/'services/investigator',ctx/'wrapper')
   lock['wrapperSourceFiles']={str(p.relative_to(ctx/'wrapper')):sha(p)for p in sorted((ctx/'wrapper').rglob('*'))if p.is_file()};(ctx/'runtime.lock.json').write_text(json.dumps(lock,indent=2)+'\n')
   with tarfile.open(old/'python-runtime-source.tar')as src,tarfile.open(out/'holmesgpt-source.tar','w')as dst:
    for member in src:
     if member.name=='platform-wrapper' or member.name.startswith('platform-wrapper/')or member.name=='runtime.lock.json':continue
     dst.addfile(copy.copy(member),src.extractfile(member)if member.isfile()else None)
    dst.add(ctx/'wrapper',arcname='platform-wrapper');dst.add(ctx/'runtime.lock.json',arcname='runtime.lock.json')
   shutil.copy2(out/'holmesgpt-source.tar',ctx/'python-runtime-source.tar');shutil.copy2(ctx/'Dockerfile.amd64',ctx/'Dockerfile')
  tag='ops.local/verification-20261019/'+name+':1a7c7da'
  run(['docker','build','--network=none','--pull=false','--platform=linux/amd64','-t',tag,str(ctx)],name+'-build',240)
  receipt=export(name,tag)
  if name in ['holmesgpt','command-runner']:receipt['sourceArchiveSHA256']=sha(out/(name+'-source.tar'))
  receipts.append(receipt);print(name,'BUILT',receipt['imageDigest'],flush=True)
 except Exception as error:
  receipts.append({'name':name,'status':'FAILED','errorType':type(error).__name__,'detail':str(error),'retried':False});print(name,'FAILED',str(error),flush=True)
 (out/'native-build-receipts.json').write_text(json.dumps(receipts,indent=2)+'\n')

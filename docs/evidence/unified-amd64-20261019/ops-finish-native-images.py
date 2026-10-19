from pathlib import Path
import ast,copy,hashlib,io,json,os,shutil,subprocess,tarfile,time
os.umask(0o077)
base=Path('/opt/ops-verification-20261019-1a7c7da');out=base/'distribution';repo=base/'repo';revision='1a7c7da4af7d74d80df0df0aec72868cbe2785cc'
tree=ast.parse((out/'ops-build-native-candidate.py').read_text());functions=ast.Module(body=[n for n in tree.body if isinstance(n,ast.FunctionDef)],type_ignores=[]);exec(compile(functions,'reviewed-native-helpers','exec'))
receipts=json.loads((out/'native-build-receipts.json').read_text())
for name in ['platform-worker','platform-web']:
 r=export(name,'ops.local/verification-20261019/'+name+':1a7c7da')
 if name=='platform-web':r['sourceArchiveSHA256']=sha(out/'platform-web-source.tar')
 receipts.append(r);(out/'native-build-receipts.json').write_text(json.dumps(receipts,indent=2)+'\n');print(name,'EXPORTED',flush=True)
# Native Runner tests use loopback services/temp files, not real tenant resources.
import gzip
binary=out/'action-native.test';binary.write_bytes(gzip.decompress((out/'ops-action-native-20261019.test.gz').read_bytes()));binary.chmod(0o555)
run(['docker','run','--rm','--pull=never','--network=none','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--tmpfs=/tmp:rw,uid=65532,gid=65532,mode=0700,size=64m','--mount=type=bind,src='+str(binary)+',dst=/native-action.test,readonly','--entrypoint','/native-action.test','ops.local/verification-20261019/command-runner:1a7c7da','-test.run','^Test(Verification|NetworkVerification|ReadWriteVerification|TypedRunner)','-test.v','-test.count=1'], 'command-runner-native-tests',60)
print('Runner native protocol tests PASS',flush=True)
# Reuse the exact existing dependency image instead of copying its wheel/build caches.
old=Path('/data/ops-amd64-final-20261010/investigator-r12');ctx=out/'holmesgpt-overlay-context';ctx.mkdir(exist_ok=False)
parent=json.loads(subprocess.check_output(['docker','image','inspect','ops.local/amd64-poc/investigator:20261010-r12']))[0]['Id']
with tarfile.open('/data/ops-amd64-final-20261010/materials/investigator-r12.oci.tar')as package:
 index=json.load(package.extractfile('index.json'));manifest=json.load(package.extractfile('blobs/sha256/'+index['manifests'][0]['digest'].split(':')[1]));assert manifest['config']['digest']==parent
assert sha(old/'python-runtime-source.tar')=='sha256:12420aadd423092bc2f8e2a098849e2e25b5df703879e1a8f0ba621fd6f1ee91'
shutil.copytree(repo/'services/investigator',ctx/'wrapper');lock=json.loads((old/'runtime.lock.json').read_text());assert lock['uvLockSHA256']==sha(ctx/'wrapper/uv.lock')
lock['wrapperSourceFiles']={str(p.relative_to(ctx/'wrapper')):sha(p)for p in sorted((ctx/'wrapper').rglob('*'))if p.is_file()};(ctx/'runtime.lock.json').write_text(json.dumps(lock,indent=2)+'\n')
source=out/'holmesgpt-source.tar';deadline=time.monotonic()+180
with tarfile.open(old/'python-runtime-source.tar')as src,tarfile.open(source,'w')as dst:
 count=0
 for member in src:
  if time.monotonic()>deadline:raise TimeoutError('bounded investigator source preparation exceeded; preserve partial; no retry')
  if member.name=='platform-wrapper' or member.name.startswith('platform-wrapper/')or member.name=='runtime.lock.json':continue
  dst.addfile(copy.copy(member),src.extractfile(member)if member.isfile()else None);count+=member.isfile()
 dst.add(ctx/'wrapper',arcname='platform-wrapper');dst.add(ctx/'runtime.lock.json',arcname='runtime.lock.json')
record={'sourceCommit':revision,'parentImageConfig':parent,'correspondingSourceSHA256':sha(source),'unchangedSourceMembers':count,'uvLockAndDependenciesUnchanged':True}
(ctx/'SOURCE-DISTRIBUTION.json').write_text(json.dumps(record,indent=2)+'\n')
(ctx/'Dockerfile').write_text('FROM '+parent+'\nCOPY wrapper/src /app/src\nCOPY runtime.lock.json SOURCE-DISTRIBUTION.json /usr/share/ops/legal/\nUSER 65532:65532\nENTRYPOINT ["python", "-m", "investigator.worker"]\n')
tag='ops.local/verification-20261019/holmesgpt:1a7c7da';run(['docker','build','--network=none','--pull=false','--platform=linux/amd64','-t',tag,str(ctx)],'holmesgpt-overlay-build',120)
tests=ctx/'wrapper/tests';tests.chmod(0o755)
for p in tests.rglob('*'):p.chmod(0o755 if p.is_dir()else 0o644)
run(['docker','run','--rm','--pull=never','--network=none','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--tmpfs=/tmp:rw,uid=65532,gid=65532,mode=0700,size=128m','--mount=type=bind,src='+str(tests)+',dst=/native-tests,readonly','--entrypoint','python',tag,'-m','unittest','discover','-s','/native-tests','-p','*unittest.py','-v'],'holmesgpt-native-tests',90)
r=export('holmesgpt',tag);r.update(record);receipts.append(r);(out/'native-build-receipts.json').write_text(json.dumps(receipts,indent=2)+'\n');print('Investigator native build/tests/export PASS',flush=True)

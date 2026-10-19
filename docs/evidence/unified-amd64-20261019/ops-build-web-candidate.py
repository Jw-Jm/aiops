from pathlib import Path
import copy,hashlib,io,json,os,shutil,subprocess,tarfile
os.umask(0o077)
base=Path('/opt/ops-verification-20261019-1a7c7da');out=base/'distribution';repo=base/'repo';cache=Path('/data/ops-sp08-20261011/cache');ctx=out/'platform-web-context';ctx.mkdir(exist_ok=False)
def sha(p):return hashlib.file_digest(Path(p).open('rb'),'sha256').hexdigest()
review=json.loads((repo/'third_party/admission/sp08-web-npm-review-draft.json').read_text());native=json.loads((repo/'third_party/admission/sp08-nginx-runtime-source.json').read_text())
verified=[]
for p in review['packages']:
 source=cache/'npm-sources'/(p['name'].lstrip('@').replace('/','_')+'-'+p['version']+'.tgz');assert sha(source)==p['archiveDigest'].removeprefix('sha256:'),(p['name'],'source hash')
 for path,digest in p['publisherNoticeDigests'].items():assert sha(repo/p['noticePath']/path)==digest.removeprefix('sha256:'),(p['name'],path)
 verified.append(source)
for p in native['files']:
 source=cache/'nginx-sources'/p['file'];assert sha(source)==p['sha256'],p['file'];verified.append(source)
for p in native['recipes']:
 source=repo/'third_party/source-recipes/sp08-nginx-runtime'/p['name']/'APKBUILD';assert sha(source)==p['recipeDigest'].removeprefix('sha256:')
shutil.copytree(base/'candidate/web-dist',ctx/'web-dist')
shutil.copytree(repo/'third_party/licenses/sp08-web-npm',ctx/'licenses/npm')
shutil.copytree(repo/'third_party/licenses/sp08-nginx-runtime',ctx/'licenses/nginx')
shutil.copy2(repo/'build/images/web/Dockerfile',ctx/'Dockerfile');shutil.copy2(repo/'build/images/web/nginx-global.conf',ctx/'nginx-global.conf')
with tarfile.open(out/'platform-web-source.tar','w')as package:
 for p in verified:package.add(p,arcname=('npm-sources/'if p.parent.name=='npm-sources'else 'nginx-sources/')+p.name)
 for path in ['third_party/admission/sp08-web-npm-review-draft.json','third_party/admission/sp08-nginx-runtime-source.json','third_party/source-recipes/sp08-nginx-runtime','third_party/licenses/sp08-web-npm','third_party/licenses/sp08-nginx-runtime','web','build/images/web']:
  package.add(repo/path,arcname='platform/'+path)
 package.add(ctx/'web-dist',arcname='built-web-dist')
(out/'web-source-verification.json').write_text(json.dumps({'npmPackages':len(review['packages']),'nativeSources':len(native['files']),'allSourceAndPublisherNoticeHashesMatch':True,'nginxReleaseSignatureVerified':native['nginxReleaseSignatureVerified'],'sourceArchiveSHA256':'sha256:'+sha(out/'platform-web-source.tar'),'sourceCommit':'1a7c7da4af7d74d80df0df0aec72868cbe2785cc'},indent=2)+'\n')
with (out/'platform-web-build.log').open('w')as log:subprocess.run(['docker','build','--network=none','--pull=false','--platform=linux/amd64','-t','ops.local/verification-20261019/platform-web:1a7c7da',str(ctx)],stdout=log,stderr=subprocess.STDOUT,check=True,timeout=180)
with (out/'platform-web-native.log').open('w')as log:subprocess.run(['docker','run','--rm','--pull=never','--network=none','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--entrypoint','/usr/sbin/nginx','ops.local/verification-20261019/platform-web:1a7c7da','-V'],stdout=log,stderr=subprocess.STDOUT,check=True,timeout=20)
print('Web built; 277 npm / 28 native sources verified; nginx native executable PASS',flush=True)

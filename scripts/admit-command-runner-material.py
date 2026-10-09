#!/usr/bin/env python3
"""Bind the exact SP07 Runner distribution to original publisher sources/notices.

Run only after preparing the complete closure and a native tool-version PoC.
This does not qualify Kubernetes/SSH business behavior or AMD64 deployment.
"""
import argparse,hashlib,json,pathlib,tarfile,shutil,re,sys
import yaml
from importlib.util import spec_from_file_location,module_from_spec
p=argparse.ArgumentParser();p.add_argument('--material-root',type=pathlib.Path,required=True);p.add_argument('--runner-commit',required=True);p.add_argument('--poc-report',default='docs/poc/sp07-command-runner-material.md');a=p.parse_args()
if not re.fullmatch('[0-9a-f]{40}',a.runner_commit):raise ValueError('full upstream Runner source commit required')
repo=pathlib.Path.cwd();m=a.material_root.resolve();sha=lambda p:'sha256:'+hashlib.sha256(pathlib.Path(p).read_bytes()).hexdigest()
spec=spec_from_file_location('oci_verifier',repo/'scripts/admit-investigator-material.py')
# Reuse the immutable verifier function without invoking the other component's CLI.
namespace={};text=(repo/'scripts/admit-investigator-material.py').read_text();exec(text[:text.index('parser=argparse.ArgumentParser')],namespace)
with tarfile.open(m/'command-runner.oci.tar') as t:image=json.load(t.extractfile('index.json'))['manifests'][0]['digest']
namespace['verify_image_oci'](m/'command-runner.oci.tar',image)
bundle=sha(m/'runner-corresponding-source.tar');closure=json.loads((m/'source-closure.json').read_text());assert closure['digest']==bundle
adr='docs/adr/0033-sp07-runner-material-distribution.md';adrsha=sha(adr)
notices=[];deps=[];native=[]
def exact(name,version,url,digest,notice):
 """Finite identity for mixed publisher notices; no guessed aggregate license."""
 if not re.fullmatch('sha256:[0-9a-f]{64}',digest):raise ValueError('source digest required')
 target=repo/'third_party/licenses/command-runner'/re.sub('[^a-zA-Z0-9_.-]','_',name+'-'+version)/'publisher-notices.txt';target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(notice)
 nh=sha(target);identifier='LicenseRef-SP07-Runner-'+hashlib.sha256((name+'@'+version+image+nh).encode()).hexdigest();path=str(target.relative_to(repo))
 notices.append(dict(path=path,license=identifier,digest=nh));deps.append(dict(name=name,version=version,source=url,digest=digest,license=identifier,sourceType='archive',sourceArchiveSHA256=digest))
 native.append(dict(id=identifier,component='command-runner',componentVersion='2.4.3',imageDigest=image,dependencyName=name,version=version,source=url,sourceArchiveSHA256=digest,correspondingSourceBundleSHA256=bundle,noticePath=path,noticeDigest=nh,adr=adr,adrDigest=adrsha))
# Original metadata and all publisher license files are preserved, including mixed source licenses.
lock=json.loads((m/'python.lock.json').read_text())
for x in lock['packages']:
 assert sha(m/x['sourcePath'])==x['sourceDigest'] and sha(m/x['wheel'])==x['wheelDigest']
 note=('Publisher wheel/source identity and license declaration\n'+json.dumps(x,indent=2)+'\n').encode()
 if not x['notices']:raise ValueError('original Python notices missing')
 for n in x['notices']:note+=b'\nOriginal member: '+n.encode()+b'\n'+(m/n).read_bytes()
 exact('python/'+x['name'],x['version'],x['sourceURL'],x['sourceDigest'],note)
# Debian signatures were checked by apt; retained indexes bind each exact source.
paragraphs={}
for block in (m/'debian/Sources.txt').read_text().split('\n\n'):
 fields={};key=None
 for line in block.splitlines():
  if line.startswith(' ') and key:fields[key]+='\n'+line
  elif ':' in line:key,v=line.split(':',1);fields[key]=v.strip()
 if 'Package' in fields and 'Version' in fields:paragraphs[(fields['Package'],fields['Version'])]=fields
base=json.loads((m/'debian/lock.json').read_text())
for x in base['files']:assert sha(m/'debian'/x['path'])==x['digest']
for x in base['packages']:
 f=paragraphs[(x['source'],x['sourceVersion'])];files=[v.split() for v in f['Checksums-Sha256'].splitlines() if v.strip()]
 for digest,size,name in files:assert sha(m/'debian/sources'/name)=='sha256:'+digest
 dsc=next(v for v in files if v[2].endswith('.dsc'));url='https://deb.debian.org/'+('debian-security' if f['Directory'].startswith('pool/updates/') else 'debian')+'/'+f['Directory']+'/'+dsc[2]
 note=(m/'debian/notices'/x['name'].split(':')[0]/'copyright').read_bytes()
 exact('debian/'+x['name'],x['version'],url,'sha256:'+dsc[0],note)
# Reused native wheel bytes have the same exact publisher source closure as SP06.
rust=json.loads((m/'rust/source.lock.json').read_text())
for x in rust['packages']:
 assert sha(m/x['path'])==x['sourceSHA256']
 with tarfile.open(m/x['path']) as t:note=b'Original publisher Cargo.toml\n'+t.extractfile(x['name']+'-'+x['version']+'/Cargo.toml').read()
 for n in x['notices']:
  assert sha(m/n['path'])==n['sha256'];note+=b'\n'+n['path'].encode()+b'\n'+(m/n['path']).read_bytes()
 exact('cargo/'+x['name'],x['version'],x['sourceURL'],x['sourceSHA256'],note)
for x in json.loads((m/'native/source.lock.json').read_text())['materials']:
 assert sha(m/x['path'])==x['sourceSHA256'];note=b'Original embedded publisher notices\n'
 for n in x['notices']:
  assert sha(m/n['path'])==n['sha256'];note+=b'\n'+n['path'].encode()+b'\n'+(m/n['path']).read_bytes()
 exact('embedded/'+x['name'],x['version'],x['sourceURL'],x['sourceSHA256'],note)
def archive_notices(path):
 with tarfile.open(path) as t:
  ns=[x for x in t.getmembers() if x.isfile() and re.search(r'(^|/)(LICENSE[^/]*|COPYING[^/]*|NOTICE[^/]*|COPYRIGHT[^/]*)$',x.name,re.I)]
  if not ns:raise ValueError('publisher archive notices absent')
  return b''.join(x.name.encode()+b'\n'+t.extractfile(x).read()+b'\n' for x in ns)
for x in json.loads((m/'base/python-source.lock.json').read_text())['sources']:
 if x.get('buildOnly'):continue
 path=m/'base'/x['path'];assert sha(path)==x['sha256'];exact(x['name'],x['version'],x['url'],x['sha256'],archive_notices(path))
x=next(x for x in json.loads((m/'rust/toolchain/toolchain.lock.json').read_text())['components'] if x['name']=='rust-src');path=m/x['path'];assert sha(path)==x['sha256'];exact('rust/standard-library',x['version'],x['url'],x['sha256'],archive_notices(path))
k=json.loads((m/'kubectl.lock.json').read_text());x=next(x for x in k['files'] if x['name']=='kubernetes-source.tar.gz');assert sha(m/x['name'])==x['digest'];exact('kubernetes/kubectl-vendor-source',k['version'],x['url'],x['digest'],archive_notices(m/x['name']))
x=json.loads((m/'kubectl-go.lock.json').read_text());assert sha(m/x['path'])==x['digest'];exact('kubectl/go-standard-library',x['version'],x['url'],x['digest'],archive_notices(m/x['path']))
# Platform Go modules retain their existing separate source/license admission.
assert sha(m/'runtime-go-source.tar')==closure['runtimeSource']
assert sha(m/'platform-runner-source.tar')==closure['platformSource']
registry=repo/'internal/supplychain/licenses/sp07-runner-reviewed.json';registry.write_text(json.dumps(dict(schemaVersion=1,licenses=native),indent=2)+'\n')
review=repo/'third_party/admission/sp07-runner-license-review.json';review.write_text(json.dumps(dict(schemaVersion=1,imageDigest=image,correspondingSourceBundleSHA256=bundle,pythonPackages=lock['packages'],debianPackages=base['packages'],nativeBuildSourceSuperset=True,sourceClosure=closure,licenseScopes=native,businessAcceptance='separate; no execution/RCA claim'),indent=2)+'\n')
source=next(x for x in lock['packages'] if x['name']=='ansible-runner')
fixtures=['test/fixtures/sp07/command-transaction.go.fixture','test/fixtures/sp07/execution-profile.go.fixture']
for origin,dest in zip(['test/integration/sp07_command_test.go','test/security/sp07_execution_profile_test.go'],fixtures):
 dest=repo/dest;dest.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(repo/origin,dest)
section=dict(name='command-runner',state='qualified',version='2.4.3',source='https://github.com/ansible/ansible-runner',commit=a.runner_commit,digest=image,license='Apache-2.0',specialLicenseADR=adr,fileLicenses=notices,sourceSnapshot=True,sourceArchiveSHA256=source['sourceDigest'],correspondingSourceBundleSHA256=bundle,architectures=['linux/arm64'],usage='SP07 one-shot locked Bash/Kubernetes/Ansible transport; no investigation or LLM runtime',reuseMode='process-isolated',linkageMode='external-cli',importedPaths=['ansible_runner'],dependencyClosure=deps,dependencyClosureVerified=True,patches=[],forkPolicy='not-applicable',owner='SP07 command execution',pocReport=a.poc_report,conformanceFixtures=fixtures,exitPlan='Disable SP07; preserve execution_unknown, evidence and audit; never redispatch uncertain commands',requiredFor1_0=False,officialSupportSources=['https://github.com/ansible/ansible-runner/tree/'+a.runner_commit,'https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/'])
catalog=repo/'bundle/component-catalog.yaml';text=catalog.read_text()
if '  - name: command-runner\n' in text:
 begin=re.search(r'^  - name: command-runner\n',text,re.M).start(); match=re.search(r'^(?:  - name: |firstPartyKernels:)',text[begin+1:],re.M);end=begin+1+match.start() if match else len(text);text=text[:begin]+text[end:]
entry=''.join('  '+line+'\n' for line in yaml.safe_dump([section],sort_keys=False,width=150).splitlines()); boundary=text.index('firstPartyKernels:');text=text[:boundary]+entry+text[boundary:];catalog.write_text(text)
for name in [adr,a.poc_report,*fixtures,str(review.relative_to(repo)),*[x['path'] for x in notices]]:
 target=repo/'bundle/evidence'/name;target.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(repo/name,target)
print(json.dumps(dict(imageDigest=image,sourceDigest=bundle,dependencyCount=len(deps),noticeCount=len(notices),businessAcceptance='NOT_RUN'),indent=2))

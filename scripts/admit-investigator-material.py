#!/usr/bin/env python3
import json,hashlib,shutil,re,tarfile,argparse
from pathlib import Path


def verify_image_oci(path, expected):
    """Bind admission to the exact arm64 manifest, not a Docker config ID."""
    with tarfile.open(path) as archive:
        members = {}
        for member in archive:
            if member.name in members:
                raise ValueError('duplicate OCI member')
            if member.isfile():
                members[member.name] = member

        def read_json(name):
            member = members[name]
            if member.size > 1 << 20:
                raise ValueError('oversized OCI metadata')
            return json.loads(archive.extractfile(member).read())

        def verify_blob(descriptor):
            digest = descriptor['digest']
            if not re.fullmatch(r'sha256:[0-9a-f]{64}', digest):
                raise ValueError('invalid OCI digest')
            member = members['blobs/sha256/' + digest.split(':')[1]]
            if member.size != descriptor['size']:
                raise ValueError('OCI size mismatch')
            actual = hashlib.sha256()
            with archive.extractfile(member) as source:
                for block in iter(lambda: source.read(1 << 20), b''):
                    actual.update(block)
            if 'sha256:' + actual.hexdigest() != digest:
                raise ValueError('OCI blob digest mismatch')
            return member.name

        index = read_json('index.json')
        if read_json('oci-layout') != {'imageLayoutVersion': '1.0.0'} or len(index['manifests']) != 1:
            raise ValueError('one selected OCI manifest required')
        descriptor = index['manifests'][0]
        if descriptor['digest'] != expected:
            raise ValueError('admission digest must be the selected OCI manifest')
        manifest = read_json(verify_blob(descriptor))
        if manifest.get('schemaVersion') != 2 or 'manifests' in manifest:
            raise ValueError('selected native image manifest required')
        config = read_json(verify_blob(manifest['config']))
        if config.get('os') != 'linux' or config.get('architecture') != 'arm64':
            raise ValueError('exact linux/arm64 image required')
        if not manifest['layers']:
            raise ValueError('image layers required')
        for layer in manifest['layers']:
            verify_blob(layer)


parser=argparse.ArgumentParser(description="Bind the exact reviewed investigator source/image/license inventory")
parser.add_argument("--image-digest",required=True)
parser.add_argument("--image-oci",type=Path,required=True,help="One selected arm64 OCI manifest and its exact local closure")
parser.add_argument("--verify-image-only",action="store_true",help="Read-only identity verification before material admission")
parser.add_argument("--debian-source-index",type=Path,required=True)
parser.add_argument("--material-root",type=Path,default=Path('artifacts/sp06-investigator'),help="Prepared exact source/image inputs; use a separate directory to preserve prior signed material")
args=parser.parse_args()
verify_image_oci(args.image_oci, args.image_digest)
if args.verify_image_only:
    print('Exact arm64 OCI manifest/config/layer identity verified; no admission files changed')
    raise SystemExit(0)
import yaml
root=Path.cwd(); out=args.material_root.resolve();image=args.image_digest
sha=lambda p:'sha256:'+hashlib.sha256(Path(p).read_bytes()).hexdigest()
lock=json.loads((out/'runtime.lock.json').read_text());base=json.loads((out/'base/source.lock.json').read_text());bundle=sha(out/'python-runtime-source.tar')
adr='docs/adr/0027-sp06-investigator-license-distribution.md';adrsha=sha(adr)
rows={x['name']:x for x in lock['sources']}; notices=[]; deps=[]; reviews=[]; native=[]
known={'events':'BSD-3-Clause','fastuuid':'BSD-3-Clause','jinja2':'BSD-3-Clause','jsonpatch':'BSD-3-Clause','jsonpointer':'BSD-3-Clause','prometrix':'MIT','holmesgpt':'Apache-2.0','prompt-toolkit':'BSD-3-Clause','pyasn1-modules':'BSD-2-Clause','python-dateutil':'Apache-2.0 AND BSD-3-Clause','text-unidecode':'Artistic-1.0 OR GPL-2.0-or-later','bashlex':'GPL-3.0-or-later','scramp':'MIT-0'}
for p in lock['wheels']:
 lic=known.get(p['name'],p['licenseExpression'])
 if not lic:
  text=p['licenseMetadata'] or ''; cls=' '.join(p['licenseClassifiers'])
  if text in ('BSD-2-Clause','BSD-2-Clause License'):lic='BSD-2-Clause'
  elif 'BSD-3-Clause' in text or text in ('BSD 3-Clause License','3-Clause BSD License'):lic='BSD-3-Clause'
  elif text in ('MIT-0','MIT No Attribution'):lic='MIT-0'
  elif text=='PSF-2.0':lic='PSF-2.0'
  elif text in ('ISC','ISC License'):lic='ISC'
  elif text=='MPL-2.0':lic='MPL-2.0'
  elif text=='MPL-2.0 AND MIT':lic=text
  elif text=='Apache-2.0 AND MIT':lic=text
  elif text=='MIT OR Apache-2.0':lic=text
  elif 'Apache' in text or 'Apache Software License' in cls:lic='Apache-2.0'
  elif 'MIT' in text or 'MIT License' in cls:lic='MIT'
  else:raise ValueError((p['name'],text,cls))
 original=rows[p['name']]
 deps.append(dict(name=p['name'],version=p['version'],source=original['url'],digest=p['sha256'],license=lic,sourceType='archive',sourceArchiveSHA256=original['sha256']))
 for n in p['notices']:
  target=root/'third_party/licenses/investigator'/n['path'];target.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(out/n['path'],target)
  assert sha(target)==n['sha256'];notices.append(dict(path=str(target.relative_to(root)),license=lic,digest=n['sha256']))
 reviews.append(dict(name=p['name'],version=p['version'],license=lic,publisherLicenseExpression=p['licenseExpression'],publisherLicenseMetadata=p['licenseMetadata'],originalNotices=p['notices'],source=original))
# Exact Debian source paragraph identity: authenticated, retained signed indexes.
paragraphs={}
for block in args.debian_source_index.read_text().split('\n\n'):
 fields={};k=None
 for l in block.splitlines():
  if l.startswith(' ') and k:fields[k]+='\n'+l
  elif ':' in l:k,v=l.split(':',1);fields[k]=v.strip()
 if 'Package' in fields and 'Version' in fields:paragraphs[(fields['Package'],fields['Version'])]=fields
for p in base['packages']:
 f=paragraphs[(p['source'],p['sourceVersion'])]
 sources=[l.split() for l in f['Checksums-Sha256'].splitlines() if l.strip()]
 artifact=next(x for x in sources if x[2].endswith('.dsc'))
 source='https://deb.debian.org/'+('debian-security' if f.get('Directory','').startswith('pool/updates/') else 'debian')+'/'+f['Directory']+'/'+artifact[2]
 sourcehash='sha256:'+artifact[0]
 assert sha(out/'base/sources'/artifact[2])==sourcehash
 path=out/'base/notices'/p['binary'].split(':')[0]/'copyright'
 if not path.exists():
  # Some binary packages share source package documentation via symlinks.
  raise ValueError('missing binary copyright '+p['binary'])
 target=root/'third_party/licenses/investigator/base'/p['binary'].replace(':','_')/'copyright';target.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(path,target)
 noticehash=sha(target);identifier='LicenseRef-SP06-Native-'+hashlib.sha256((p['binary']+'@'+p['version']+image+noticehash).encode()).hexdigest()
 notices.append(dict(path=str(target.relative_to(root)),license=identifier,digest=noticehash))
 deps.append(dict(name='debian/'+p['binary'],version=p['version'],source=source,digest=sourcehash,license=identifier,sourceType='archive',sourceArchiveSHA256=sourcehash))
 native.append(dict(id=identifier,component='holmesgpt',componentVersion='0.42.0',imageDigest=image,dependencyName='debian/'+p['binary'],version=p['version'],source=source,sourceArchiveSHA256=sourcehash,correspondingSourceBundleSHA256=bundle,noticePath=str(target.relative_to(root)),noticeDigest=noticehash,adr=adr,adrDigest=adrsha))
# Exact declared Cargo lock superset, with unaltered publisher license metadata.
rust=json.loads((out/'rust/source.lock.json').read_text())
for p in rust['packages']:
 assert sha(out/p['path'])==p['sourceSHA256']
 with tarfile.open(out/p['path']) as archive:
  original=archive.extractfile(p['name']+'-'+p['version']+'/Cargo.toml').read()
 aggregate=b'Original publisher Cargo.toml (license declaration)\n'+original
 for n in p['notices']:
  assert sha(out/n['path'])==n['sha256']
  aggregate+=b'\n\nOriginal publisher member: '+n['path'].encode()+b'\n'+(out/n['path']).read_bytes()
 target=root/'third_party/licenses/investigator/rust'/(p['name']+'-'+p['version'])/'publisher-notices.txt';target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(aggregate)
 noticehash=sha(target);identifier='LicenseRef-SP06-Native-'+hashlib.sha256((p['name']+'@'+p['version']+image+noticehash).encode()).hexdigest()
 notices.append(dict(path=str(target.relative_to(root)),license=identifier,digest=noticehash))
 deps.append(dict(name='cargo/'+p['name'],version=p['version'],source=p['sourceURL'],digest=p['sourceSHA256'],license=identifier,sourceType='archive',sourceArchiveSHA256=p['sourceSHA256']))
 native.append(dict(id=identifier,component='holmesgpt',componentVersion='0.42.0',imageDigest=image,dependencyName='cargo/'+p['name'],version=p['version'],source=p['sourceURL'],sourceArchiveSHA256=p['sourceSHA256'],correspondingSourceBundleSHA256=bundle,noticePath=str(target.relative_to(root)),noticeDigest=noticehash,adr=adr,adrDigest=adrsha))
for p in json.loads((out/'native/source.lock.json').read_text())['materials']:
 assert sha(out/p['path'])==p['sourceSHA256']
 aggregate=b'Complete native publisher source notices; each original notice governs its own files.\n'
 for n in p['notices']:
  assert sha(out/n['path'])==n['sha256']
  aggregate+=b'\nOriginal publisher member: '+n['path'].encode()+b'\n'+(out/n['path']).read_bytes()
 target=root/'third_party/licenses/investigator/native'/(p['name']+'-'+p['version'])/'publisher-notices.txt';target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(aggregate)
 noticehash=sha(target);identifier='LicenseRef-SP06-Native-'+hashlib.sha256(('embedded/'+p['name']+'@'+p['version']+image+noticehash).encode()).hexdigest()
 notices.append(dict(path=str(target.relative_to(root)),license=identifier,digest=noticehash))
 deps.append(dict(name='embedded/'+p['name'],version=p['version'],source=p['sourceURL'],digest=p['sourceSHA256'],license=identifier,sourceType='archive',sourceArchiveSHA256=p['sourceSHA256']))
 native.append(dict(id=identifier,component='holmesgpt',componentVersion='0.42.0',imageDigest=image,dependencyName='embedded/'+p['name'],version=p['version'],source=p['sourceURL'],sourceArchiveSHA256=p['sourceSHA256'],correspondingSourceBundleSHA256=bundle,noticePath=str(target.relative_to(root)),noticeDigest=noticehash,adr=adr,adrDigest=adrsha))
# Full original compiler/library publisher notices accompany the build inputs.
for p in sorted((out/'rust/toolchain/notices').rglob('*')):
 if p.is_file():
  target=root/'third_party/licenses/investigator'/p.relative_to(out);target.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(p,target)
# The standard-library source used by the platform native rebuild is admitted
# under its actual publisher COPYRIGHT-library notice, not a guessed expression.
p=next(p for p in json.loads((out/'rust/toolchain/toolchain.lock.json').read_text())['components'] if p['name']=='rust-src')
assert sha(out/p['path'])==p['sha256']
with tarfile.open(out/p['path']) as archive:
 names=[m for m in archive.getmembers() if m.isfile() and (m.name.endswith('/COPYRIGHT-library.html') or m.name.endswith('/LICENSE-APACHE') or m.name.endswith('/LICENSE-MIT'))]
 if not names: raise ValueError('Rust library publisher notice absent')
 aggregate=b''.join(m.name.encode()+b'\n'+archive.extractfile(m).read()+b'\n' for m in names)
target=root/'third_party/licenses/investigator/rust/standard-library-publisher-notices.txt';target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(aggregate)
noticehash=sha(target);identifier='LicenseRef-SP06-Native-'+hashlib.sha256(('rust-std@'+p['version']+image+noticehash).encode()).hexdigest()
notices.append(dict(path=str(target.relative_to(root)),license=identifier,digest=noticehash))
deps.append(dict(name='rust/standard-library',version=p['version'],source=p['url'],digest=p['sha256'],license=identifier,sourceType='archive',sourceArchiveSHA256=p['sha256']))
native.append(dict(id=identifier,component='holmesgpt',componentVersion='0.42.0',imageDigest=image,dependencyName='rust/standard-library',version=p['version'],source=p['url'],sourceArchiveSHA256=p['sha256'],correspondingSourceBundleSHA256=bundle,noticePath=str(target.relative_to(root)),noticeDigest=noticehash,adr=adr,adrDigest=adrsha))

# CPython/pip original sources and wrapper license are distributed as separate packages.
for p in json.loads((out/'base/python-source.lock.json').read_text())['sources']:
 if p.get('buildOnly'):continue
 lic='PSF-2.0' if p['name']=='cpython' else 'MIT'
 deps.append(dict(name=p['name'],version=p['version'],source=p['url'],digest=p['sha256'],license=lic,sourceType='archive',sourceArchiveSHA256=p['sha256']))
for p,lic in [('services/investigator/LICENSE','GPL-3.0-or-later'),('services/investigator/NOTICE','GPL-3.0-or-later')]:notices.append(dict(path=p,license=lic,digest=sha(p)))
Path('internal/supplychain/licenses/sp06-investigator-reviewed.json').write_text(json.dumps(dict(schemaVersion=1,licenses=native),indent=2)+'\n')
Path('third_party/admission/sp06-investigator-license-review.json').write_text(json.dumps(dict(schemaVersion=1,imageDigest=image,correspondingSourceBundleSHA256=bundle,pythonPackages=reviews,debianPackages=base['packages'],cargoDeclaredSourceSuperset=rust,nativePublisherNotices=native,wrapperLicense='GPL-3.0-or-later',pyrca='excluded',sourceClosure='complete publisher artifacts and original notices; exact signed Debian source indexes'),indent=2)+'\n')
source=rows['holmesgpt']
conformance='test/fixtures/sp06/real-chain.go.fixture'
shutil.copyfile(root/'test/integration/sp06_real_chain_test.go', root/conformance)
old_embedded=root/'bundle/evidence/test/integration/sp06_real_chain_test.go'
if old_embedded.exists():old_embedded.unlink()
section=dict(name='holmesgpt',state='qualified',version='0.42.0',source='https://github.com/HolmesGPT/holmesgpt',commit=lock['holmesCommit'],digest=image,license='Apache-2.0',specialLicenseADR=adr,fileLicenses=notices,sourceSnapshot=True,sourceArchiveSHA256=source['sha256'],correspondingSourceBundleSHA256=bundle,architectures=['linux/arm64'],usage='Exact unmodified Holmes runtime plus GPL platform wrapper; platform MCP only; suggestions only',reuseMode='process-isolated',linkageMode='python-official-extension-points',importedPaths=['holmes/core/llm.py','holmes/core/tool_calling_llm.py','holmes/core/tools.py','holmes/core/tools_utils/tool_executor.py'],dependencyClosure=deps,dependencyClosureVerified=True,patches=[],forkPolicy='not-applicable',owner='SP06 investigator',pocReport='docs/poc/sp06-holmesgpt-runtime.md',conformanceFixtures=[conformance,'services/investigator/tests/test_security.py'],exitPlan='Disable SP06 dispatch; preserve immutable Jobs/Steps/audit; replace adapter through versioned contracts',requiredFor1_0=True,officialSupportSources=['https://github.com/HolmesGPT/holmesgpt/tree/'+lock['holmesCommit'],'https://modelcontextprotocol.io/specification/2025-06-18/basic/transports'])
# Preserve all existing catalog entries/text; only replace the Holmes candidate section.
catalog=Path('bundle/component-catalog.yaml');text=catalog.read_text();begin=text.index('  - name: holmesgpt\n');end=text.index('  - name: k8sgpt\n',begin)
new=yaml.safe_dump([section],sort_keys=False,width=150)
new=''.join('  '+line+'\n' for line in new.splitlines());catalog.write_text(text[:begin]+new+text[end:])
for p in [adr,'docs/poc/sp06-holmesgpt-runtime.md']+[x['path'] for x in notices]+section['conformanceFixtures']:
 origin=root/p
 if not origin.exists():print('pending embedded evidence',p);continue
 target=root/'bundle/evidence'/p;target.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(origin,target)
print('exact admission inventory',len(deps),'dependencies',len(notices),'notices',len(native),'native bindings')
source_manifest=Path('third_party/admission/sp06-holmesgpt-source.json')
record=json.loads(source_manifest.read_text());record.update(state='qualified',runtimeAdmission='exact reuse/distribution admission; full SP06 final gates and independent review separate',imageDigest=image,correspondingSourceBundleSHA256=bundle,dependencyClosureLock=str((out/'runtime.lock.json').relative_to(root)) if out.is_relative_to(root) else str(out/'runtime.lock.json'),licenseReview='third_party/admission/sp06-investigator-license-review.json')
source_manifest.write_text(json.dumps(record,indent=2)+'\n')

# Keep the public runtime reuse lock bound to this exact admitted material.
reuse_path=Path('docs/poc/holmes-investigator-reuse-lock.yaml')
reuse=yaml.safe_load(reuse_path.read_text())
reuse.update(imageDigest=image, correspondingSourceBundleSHA256=bundle)
reuse_path.write_text(yaml.safe_dump(reuse,sort_keys=False,width=100))

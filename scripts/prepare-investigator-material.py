#!/usr/bin/env python3
"""Authenticate exact locked Python materials, original notices and source closure.

This is a build-time preparation step, never a runtime dependency or service.
Runtime/offline checks use the emitted local wheel hashes without a network.
"""
import argparse, concurrent.futures, email, hashlib, json, pathlib, tarfile, tomllib, urllib.request, zipfile, shutil


def sha(data): return 'sha256:'+hashlib.sha256(data).hexdigest()
def canonical(name): return name.lower().replace('_','-').replace('.','-')

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--out',type=pathlib.Path,required=True)
    a=p.parse_args();root=pathlib.Path(__file__).resolve().parents[1]
    out=a.out.resolve();out.mkdir(parents=True,exist_ok=True)
    source=out/'sources';source.mkdir(exist_ok=True)
    notices=out/'notices';notices.mkdir(exist_ok=True)
    lock=tomllib.loads((root/'services/investigator/uv.lock').read_text())
    packages={canonical(x['name']):x for x in lock['package'] if x.get('source',{}).get('registry')}
    if any('pyrca' in x for x in packages):raise ValueError('PyRCA is disabled and cannot be distributed')
    wheels=[]
    for file in sorted((out/'wheels').glob('*.whl')):
        with zipfile.ZipFile(file) as archive:
            metadata_path=next(x for x in archive.namelist() if x.endswith('.dist-info/METADATA'))
            m=email.message_from_bytes(archive.read(metadata_path));name=canonical(m['Name'])
            pkg=packages[name]
            if pkg['version']!=m['Version']:raise ValueError('wheel version does not match uv.lock')
            digest=sha(file.read_bytes())
            original=next((x for x in pkg.get('wheels',[]) if x['hash']==digest),None)
            rebuilt=None
            if original is None:
                review_name={'tiktoken':'sp06-tiktoken-rebuilt-wheel.json','google-crc32c':'sp06-crc32c-rebuilt-wheel.json','clickhouse-sqlalchemy':'sp06-python-rebuilt-wheel.json'}.get(name)
                if review_name is None:raise ValueError('unrecognized rebuilt package: '+name)
                review_path=root/'third_party/admission'/review_name
                review=json.loads(review_path.read_text())
                if name!=review['package'] or digest!=review['wheelSHA256'] or pkg['sdist']['hash']!=review['sourceSHA256'] or not review['allMembersCompared']:raise ValueError('unrecognized or unauthenticated rebuilt wheel: '+name)
                if name=='tiktoken':
                    if review['comparison']!='offline-rebuild-payload' or sha((out/review['cargoLockPath']).read_bytes())!=review['cargoLockSHA256'] or sha((out/'rust/toolchain/toolchain.lock.json').read_bytes())!=review['toolchainLockSHA256']:raise ValueError('native rebuild requires exact Cargo/toolchain locks')
                if name=='google-crc32c':
                    native_lock=json.loads((out/'native/source.lock.json').read_text())
                    crc=next(x for x in native_lock['materials'] if x['name']=='crc32c')
                    if review['comparison']!='offline-rebuild-payload' or sha((out/'native/source.lock.json').read_bytes())!=review['nativeSourceLockSHA256'] or crc['version']!=review['nativeCommit'] or crc['sourceSHA256']!=review['nativeSourceSHA256'] or sha((root/'scripts/build-investigator-crc32c.sh').read_bytes())!=review['scriptSHA256']:raise ValueError('CRC rebuild requires exact native source/build locks')
                rebuilt={'source':pkg['sdist'],'buildBackend':review.get('buildBackend','setuptools==84.0.0'),'python':'3.12.14','pip':'25.0.1','unmodifiedPublisherSource':True,'reviewSHA256':sha(review_path.read_bytes())}
            entries=[]
            for member in archive.namelist():
                leaf=pathlib.PurePosixPath(member).name.upper()
                if member.endswith('/') or not any(x in leaf for x in ('LICENSE','LICENCE','COPYING','NOTICE','AUTHORS')):continue
                data=archive.read(member);target=notices/name/member
                if not target.resolve().is_relative_to(notices.resolve()):raise ValueError('unsafe notice path')
                target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(data)
                entries.append({'path':str(target.relative_to(out)),'sha256':sha(data)})
            classifiers=[x for x in m.get_all('Classifier',[]) if x.startswith('License ::')]
            wheels.append({'name':name,'version':m['Version'],'wheel':str(file.relative_to(out)),'sha256':digest,'sourceURL':original['url'] if original else pkg['sdist']['url'],'licenseExpression':m.get('License-Expression'),'licenseMetadata':m.get('License'),'licenseClassifiers':classifiers,'notices':entries,'rebuilt':rebuilt})
    # The exported runtime requirements are the full unmodified Holmes closure,
    # excluding pytest/dev tools. Check every runtime requirement is materialized.
    from packaging.requirements import Requirement
    from packaging.markers import default_environment
    target=default_environment();target.update(sys_platform="linux",platform_system="Linux",platform_machine="aarch64",python_version="3.12",python_full_version="3.12.14")
    requirements=[]
    for line in (out/'requirements.txt').read_text().splitlines():
        if line and not line.startswith((' ','#','-')):
            requirement=Requirement(line.rstrip(' \\'))
            if requirement.marker is None or requirement.marker.evaluate(target):requirements.append(canonical(requirement.name))
    if set(requirements)!={x['name'] for x in wheels}:raise ValueError('runtime wheel closure is incomplete')
    def download(pkg):
        s=pkg.get('sdist')
        if not s:
            wheel=next(x for x in wheels if x['name']==canonical(pkg['name']))
            original=out/wheel['wheel']
            with zipfile.ZipFile(original) as archive:
                compiled=any(x.endswith(('.so','.dylib','.dll','.pyd')) for x in archive.namelist())
            if compiled:
                with urllib.request.urlopen('https://pypi.org/pypi/'+pkg['name']+'/'+pkg['version']+'/json',timeout=60) as response:published=json.load(response)
                candidates=[x for x in published['urls'] if x['packagetype']=='sdist']
                if len(candidates)!=1:raise ValueError('compiled package has no unique original source: '+pkg['name'])
                x=candidates[0];s={'url':x['url'],'hash':'sha256:'+x['digests']['sha256']}
                pkg['sdist']=s
                return download(pkg)
            target=source/original.name;target.write_bytes(original.read_bytes())
            return {'name':pkg['name'],'version':pkg['version'],'url':wheel['sourceURL'],'path':str(target.relative_to(out)),'sha256':wheel['sha256'],'sourceForm':'original-pure-python-wheel'}
        file=source/(pkg['name']+'-'+pkg['version']+'-'+pathlib.PurePosixPath(s['url']).name)
        if not file.exists():
            with urllib.request.urlopen(s['url'],timeout=60) as response:data=response.read()
            if sha(data)!=s['hash']:raise ValueError('source hash mismatch')
            file.write_bytes(data)
        if sha(file.read_bytes())!=s['hash']:raise ValueError('cached source hash mismatch')
        return {'name':pkg['name'],'version':pkg['version'],'url':s['url'],'path':str(file.relative_to(out)),'sha256':s['hash']}
    selected=[packages[x['name']] for x in wheels]
    with concurrent.futures.ThreadPoolExecutor(max_workers=6) as executor:sources=list(executor.map(download,selected))
    # Preserve original source notices omitted from the published wheels.
    # Supabase's exact release monorepo owns all five sub-distributions.
    supplemental={name:('https://raw.githubusercontent.com/supabase/supabase-py/8584419cbfac152a16e601e9cd724af04da6c24f/LICENSE','sha256:334dd6820e2eaeab2064e7c59001b810566728a28a41a7c1dbf69bbee17d0936') for name in ('postgrest','realtime','storage3','supabase-auth','supabase-functions')}
    for wheel in wheels:
        if wheel['notices']:continue
        original=next(x for x in sources if canonical(x['name'])==wheel['name'])
        file=out/original['path']
        if tarfile.is_tarfile(file):
            with tarfile.open(file) as archive:
                for member in archive.getmembers():
                    if not member.isfile() or not any(x in pathlib.PurePosixPath(member.name).name.upper() for x in ('LICENSE','LICENCE','COPYING','NOTICE')):continue
                    data=archive.extractfile(member).read();target=notices/wheel['name']/'source'/pathlib.PurePosixPath(member.name).name
                    target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(data)
                    wheel['notices'].append({'path':str(target.relative_to(out)),'sha256':sha(data),'sourceArchiveSHA256':original['sha256']})
        if wheel['name'] in supplemental:
            url,digest=supplemental[wheel['name']]
            target=notices/wheel['name']/'upstream'/'LICENSE'
            if target.exists():data=target.read_bytes()
            else:
                with urllib.request.urlopen(url,timeout=60) as response:data=response.read()
            if sha(data)!=digest:raise ValueError('exact upstream notice changed')
            target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(data)
            wheel['notices'].append({'path':str(target.relative_to(out)),'sha256':digest,'sourceURL':url})
        if not wheel['notices']:
            if wheel['name']!='bs4':raise ValueError('original license notice missing: '+wheel['name'])
            with zipfile.ZipFile(out/wheel['wheel']) as archive:
                if any(x.endswith(('.py','.so','.pyd')) for x in archive.namelist()):raise ValueError('bs4 metapackage unexpectedly contains code')
            wheel['noticeDecision']='empty compatibility metapackage; preserve original MIT metadata and complete original archive; actual BeautifulSoup code is the separate beautifulsoup4 distribution'
    base=out/'base'
    base_lock=json.loads((base/'source.lock.json').read_text())
    for material in base_lock['files']:
        if sha((base/material['path']).read_bytes())!=material['sha256']:raise ValueError('base source material changed')
    python_lock=json.loads((base/'python-source.lock.json').read_text())
    for material in python_lock['sources']:
        if sha((base/material['path']).read_bytes())!=material['sha256']:raise ValueError('base Python source material changed')
    # Native extensions need corresponding source beyond their Python sdists.
    rust_lock=json.loads((out/'rust/source.lock.json').read_text())
    for origin in rust_lock['origins']:
        if sha((out/origin['origin']['path']).read_bytes())!=origin['sha256']:raise ValueError('Cargo origin lock changed')
    for package in rust_lock['packages']:
        if sha((out/package['path']).read_bytes())!=package['sourceSHA256']:raise ValueError('Cargo source changed')
        for notice in package['notices']:
            if sha((out/notice['path']).read_bytes())!=notice['sha256']:raise ValueError('Cargo notice changed')
    toolchain=json.loads((out/'rust/toolchain/toolchain.lock.json').read_text())
    for material in toolchain['components']:
        if sha((out/material['path']).read_bytes())!=material['sha256']:raise ValueError('Rust toolchain source changed')
    for material in json.loads((out/'rust/tiktoken-vendor.lock.json').read_text())['files']:
        if sha((out/material['path']).read_bytes())!=material['sha256']:raise ValueError('vendored offline Rust source changed')
    native=json.loads((out/'native/source.lock.json').read_text())
    for material in native['materials']:
        if sha((out/material['path']).read_bytes())!=material['sourceSHA256']:raise ValueError('embedded native source changed')
        for notice in material['notices']:
            if sha((out/notice['path']).read_bytes())!=notice['sha256']:raise ValueError('embedded native notice changed')
    for material in json.loads((base/'build-tools.lock.json').read_text())['materials']:
        if sha((out/material['path']).read_bytes())!=material['sha256']:raise ValueError('build tool source changed')
    manifest={'schemaVersion':'investigator-runtime-lock/v1','state':'prepared-awaiting-offline-and-runtime-gates','architecture':'linux/arm64','python':'3.12.14','holmesgpt':'0.42.0','holmesCommit':'bfd33247f154484bc2f734a0da709da133c6a434','mcpPython':'1.28.1','mcpGo':'v1.1.0','mcpProtocol':'2025-06-18','uvLockSHA256':sha((root/'services/investigator/uv.lock').read_bytes()),'pyrca':'disabled-excluded','virtualization':'disabled-unverified-deferred','wheels':wheels,'sources':sources,'baseImage':base_lock['baseImage'],'baseSourceLockSHA256':sha((base/'source.lock.json').read_bytes()),'basePythonSourceLockSHA256':sha((base/'python-source.lock.json').read_bytes()),'wrapperLicense':'GPL-3.0-or-later'}
    manifest.update(cargoSourceLockSHA256=sha((out/'rust/source.lock.json').read_bytes()),cargoDeclaredSourceCount=len(rust_lock['packages']),nativeSourceLockSHA256=sha((out/'native/source.lock.json').read_bytes()),rustToolchainLockSHA256=sha((out/'rust/toolchain/toolchain.lock.json').read_bytes()))
    # The image and corresponding source must use the same current wrapper,
    # including when an operator prepares a second build after fixing code.
    wrapper=out/'wrapper'
    if wrapper.exists():shutil.rmtree(wrapper)
    shutil.copytree(root/'services/investigator',wrapper,ignore=shutil.ignore_patterns('.venv','__pycache__','.pytest_cache'))
    manifest['wrapperSourceFiles']={str(file.relative_to(wrapper)):sha(file.read_bytes()) for file in sorted(wrapper.rglob('*')) if file.is_file()}
    (out/'runtime.lock.json').write_text(json.dumps(manifest,indent=2)+'\n')
    # Install requirements authenticate prepared rebuilds as well as original wheels.
    (out/'offline-requirements.txt').write_text(''.join(x['name']+'=='+x['version']+' --hash='+x['sha256']+'\n' for x in wheels))
    sbom={'bomFormat':'CycloneDX','specVersion':'1.5','version':1,'components':[{'type':'library','name':x['name'],'version':x['version'],'purl':'pkg:pypi/'+x['name']+'@'+x['version'],'hashes':[{'alg':'SHA-256','content':x['sha256'].split(':')[1]}]} for x in wheels]}
    sbom['components'] += [{'type':'library','name':x['binary'],'version':x['version'],'purl':'pkg:deb/debian/'+x['binary']+'@'+x['version']+'?arch=arm64&distro=debian-13'} for x in base_lock['packages']]
    sbom['components'] += [{'type':'library','name':x['name'],'version':x['version'],'hashes':[{'alg':'SHA-256','content':x['sha256'].split(':')[1]}]} for x in python_lock['sources'] if not x.get('buildOnly')]
    sbom['components'] += [{'type':'application','name':'ops-investigator','version':'0.1.0','licenses':[{'license':{'id':'GPL-3.0-or-later'}}]}]
    # Explicitly distinguish declared source superset from measured linkage.
    sbom['components'] += [{'type':'library','name':x['name'],'version':x['version'],'purl':'pkg:cargo/'+x['name']+'@'+x['version'],'hashes':[{'alg':'SHA-256','content':x['sourceSHA256'].split(':')[1]}],'properties':[{'name':'ops:inventory-scope','value':'publisher-Cargo-lock-superset; conditional build/test/target dependencies included; runtime linkage unverified'}]} for x in rust_lock['packages']]
    sbom['components'] += [{'type':'library','name':x['name'],'version':x['version'],'hashes':[{'alg':'SHA-256','content':x['sourceSHA256'].split(':')[1]}]} for x in native['materials']]
    (out/'sbom.cdx.json').write_text(json.dumps(sbom,indent=2)+'\n')
    with tarfile.open(out/'python-runtime-source.tar','w') as archive:
        for directory in (source,notices,base,out/'rust',out/'native'):
            for file in sorted(directory.rglob('*')):
                if file.is_file() and file.name!='prepare.py':archive.add(file,arcname=str(file.relative_to(out)))
        archive.add(root/'scripts/prepare-investigator-base-source.py',arcname='build/prepare-investigator-base-source.py')
        archive.add(root/'scripts/prepare-investigator-material.py',arcname='build/prepare-investigator-material.py')
        archive.add(root/'scripts/prepare-investigator-rust-source.py',arcname='build/prepare-investigator-rust-source.py')
        archive.add(root/'scripts/build-investigator-tiktoken.sh',arcname='build/build-investigator-tiktoken.sh')
        archive.add(root/'scripts/build-investigator-crc32c.sh',arcname='build/build-investigator-crc32c.sh')
        archive.add(root/'scripts/prepare-investigator-native-source.py',arcname='build/prepare-investigator-native-source.py')
        for name in ('sp06-python-rebuilt-wheel.json','sp06-tiktoken-rebuilt-wheel.json','sp06-crc32c-rebuilt-wheel.json'):
            archive.add(root/'third_party/admission'/name,arcname='build/admission/'+name)
        archive.add(root/'build/images/investigator/Dockerfile',arcname='build/Dockerfile')
        archive.add(root/'services/investigator',arcname='platform-wrapper',filter=lambda m:None if any(x in m.name.split('/') for x in ('.venv','__pycache__','.pytest_cache')) else m)
        archive.add(out/'runtime.lock.json',arcname='runtime.lock.json')
        archive.add(out/'sbom.cdx.json',arcname='sbom.cdx.json')
    print(json.dumps({'packages':len(wheels),'sources':len(sources),'sourceBundleSHA256':sha((out/'python-runtime-source.tar').read_bytes()),'pyrca':'disabled-excluded'}))
if __name__=='__main__':main()

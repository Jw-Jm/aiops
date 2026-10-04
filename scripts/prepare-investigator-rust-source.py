#!/usr/bin/env python3
"""Authenticate the complete declared Cargo lock closure of Python native extensions.

Preparation only: every archive checksum comes from an authenticated publisher
sdist's Cargo.lock. Runtime/offline distribution uses the resulting local lock.
"""
import argparse, concurrent.futures, hashlib, json, pathlib, tarfile, tomllib, urllib.request

def sha(data): return 'sha256:'+hashlib.sha256(data).hexdigest()

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--material',type=pathlib.Path,required=True)
    parser.add_argument('--additional-lock',type=pathlib.Path)
    args=parser.parse_args();root=args.material.resolve();dest=root/'rust';dest.mkdir(exist_ok=True)
    manifest=json.loads((root/'runtime.lock.json').read_text());packages={};origins=[]
    def admit(raw, origin):
        data=tomllib.loads(raw.decode());origins.append({'origin':origin,'sha256':sha(raw)})
        for package in data.get('package',[]):
            if 'source' not in package:continue
            if package['source']!='registry+https://github.com/rust-lang/crates.io-index' or len(package.get('checksum',''))!=64:raise ValueError('unadmitted Cargo source '+str(package))
            key=(package['name'],package['version']);old=packages.get(key)
            if old and old['checksum']!=package['checksum']:raise ValueError('conflicting publisher Cargo checksum')
            packages[key]=package
    for source in manifest['sources']:
        path=root/source['path']
        if sha(path.read_bytes())!=source['sha256']:raise ValueError('Python source changed')
        if not tarfile.is_tarfile(path):continue
        with tarfile.open(path) as archive:
            for member in archive.getmembers():
                if member.isfile() and member.name.endswith('/Cargo.lock'):
                    raw=archive.extractfile(member).read();lockpath=dest/'publisher-locks'/source['name']/member.name
                    lockpath.parent.mkdir(parents=True,exist_ok=True);lockpath.write_bytes(raw)
                    admit(raw,{'package':source['name'],'sourceSHA256':source['sha256'],'member':member.name,'path':str(lockpath.relative_to(root))})
    if args.additional_lock:
        raw=args.additional_lock.read_bytes();path=dest/'publisher-locks/tiktoken/platform-locked-Cargo.lock';path.parent.mkdir(parents=True,exist_ok=True);path.write_bytes(raw)
        admit(raw,{'package':'tiktoken','decision':'unmodified publisher source rebuilt with this explicit platform Cargo lock; publisher wheel is not retained','path':str(path.relative_to(root))})
    def fetch(package):
        name,version=package['name'],package['version'];url=f'https://static.crates.io/crates/{name}/{name}-{version}.crate'
        path=dest/'crates'/f'{name}-{version}.crate';path.parent.mkdir(exist_ok=True)
        raw=path.read_bytes() if path.exists() else urllib.request.urlopen(url,timeout=60).read()
        if sha(raw)!='sha256:'+package['checksum']:raise ValueError('Cargo artifact hash mismatch '+name)
        path.write_bytes(raw)
        with tarfile.open(path) as archive:
            metadata=tomllib.loads(archive.extractfile(f'{name}-{version}/Cargo.toml').read().decode());license=metadata['package'].get('license');notices=[]
            if not license:raise ValueError('missing exact Cargo package license '+name)
            for member in archive.getmembers():
                if member.isfile() and any(word in pathlib.PurePosixPath(member.name).name.upper() for word in ('LICENSE','LICENCE','COPYING','NOTICE','COPYRIGHT')):
                    data=archive.extractfile(member).read();target=dest/'notices'/member.name
                    if not target.resolve().is_relative_to(dest.resolve()):raise ValueError('unsafe Cargo notice path')
                    target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(data);notices.append({'path':str(target.relative_to(root)),'sha256':sha(data)})
        return {'name':name,'version':version,'sourceURL':url,'sourceSHA256':sha(raw),'path':str(path.relative_to(root)),'license':license,'notices':notices}
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:records=list(executor.map(fetch,[packages[k] for k in sorted(packages)]))
    lock={'schemaVersion':'investigator-cargo-source/v1','scope':'complete publisher lock superset, including platform/target/build/test conditional dependencies; no claim all are linked at runtime','origins':origins,'packages':records}
    (dest/'source.lock.json').write_text(json.dumps(lock,indent=2)+'\n')
    print(json.dumps({'authenticatedCargoSources':len(records),'originalNotices':sum(len(p['notices']) for p in records)}))

if __name__=='__main__':main()

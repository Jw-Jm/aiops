#!/usr/bin/env python3
"""Package the reviewed unchanged K8sGPT CLI source, exact module artifacts and notices.

No network, dependency downloads or release substitutions are allowed. The
existing SP02 license decisions remain the inputs; this packaging step does not
qualify runtime behavior. Module source zips include the complete MPL sources.
"""
import argparse
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tarfile
import zipfile


def sha(raw):
    return 'sha256:'+hashlib.sha256(raw).hexdigest()


def prepare(args):
    root=Path(__file__).resolve().parents[1]
    lock_path=root/'docs/poc/inspection-reuse-lock.yaml'
    closure_path=root/'test/fixtures/upstream-inspection/dependency-closures.json'
    cap=next(x for x in json.loads(lock_path.read_text())['capabilities'] if x['id']=='k8sgpt-analyzer')
    record=next(x for x in json.loads(closure_path.read_text())['generatedFrom']['records'] if x['id']=='k8sgpt-analyzer')
    if not record['inventoryComplete'] or not record['licenseReviewComplete'] or record['unknownLicenses'] or len(record['modules'])!=233:
        raise ValueError('exact reviewed CLI dependency closure is required')
    source=args.source_archive.read_bytes() if args.source_archive else subprocess.check_output(['git','-C',str(args.source),'archive','--format=tar',cap['commit']])
    if sha(source)!='sha256:'+cap['sourceArchiveSHA256']:raise ValueError('upstream source archive drift')
    go_root=Path(subprocess.check_output(['go','env','GOROOT'],text=True).strip())
    if subprocess.check_output(['go','env','GOVERSION'],text=True).strip()!='go1.27.1':raise ValueError('locked compiler required')
    files={'toolchain/go1.27.1/LICENSE':(go_root/'LICENSE').read_bytes(),'upstream/k8sgpt-source.tar':source,'admission/inspection-reuse-lock.json':lock_path.read_bytes(),'admission/dependency-closures.json':closure_path.read_bytes()}
    modules=[]
    for m in record['modules']:
        name,version=m['resolvedPath'],m['resolvedVersion']
        escaped=''.join('!'+c.lower() if c.isupper() else c for c in name)
        escaped_version=''.join('!'+c.lower() if c.isupper() else c for c in version)
        path=args.module_cache/'cache/download'/escaped/'@v'/(escaped_version+'.zip')
        data=path.read_bytes()
        if sha(data)!='sha256:'+m['artifactSHA256']:raise ValueError('exact publisher module zip drift: '+name)
        files['modules/'+escaped+'/@v/'+escaped_version+'.zip']=data
        with zipfile.ZipFile(io.BytesIO(data)) as z:
            prefix=name+'@'+version+'/'
            for item in m['licenseFiles']+m.get('noticeFiles',[]):
                evidence=(root/item['path']).read_bytes()
                if sha(evidence)!='sha256:'+item['sha256']:raise ValueError('reviewed notice drift')
                if item.get('sourcePath') and z.read(prefix+item['sourcePath'])!=evidence:raise ValueError('notice differs from original source')
                files['notices/'+item['path']]=evidence
        modules.append({'path':name,'version':version,'artifactSHA256':sha(data),'license':m['license'],'importedPackages':m.get('importedPackages',[])})
    # Buf schema and generator provenance are redistributable locked inputs too.
    provenance=root/'docs/poc/k8sgpt-buf-license-provenance.json'
    if sha(provenance.read_bytes())!='sha256:'+cap['licenseProvenance']['sha256']:raise ValueError('generated SDK provenance drift')
    files['admission/k8sgpt-buf-license-provenance.json']=provenance.read_bytes()
    for directory in ['third_party/licenses/k8sgpt','third_party/licenses/k8sgpt-buf','third_party/licenses/k8sgpt-cli-v0.3.41']:
        for p in sorted((root/directory).rglob('*')):
            if p.is_file():files['notices/'+str(p.relative_to(root))]=p.read_bytes()
    decision={'schemaVersion':1,'source':cap['source'],'version':cap['version'],'commit':cap['commit'],'upstreamSourceArchiveSHA256':sha(source),'architecture':'linux/arm64','goVersion':'go1.27.1','patches':[],'modules':modules,'licenseClosureSHA256':sha(closure_path.read_bytes()),'distribution':'Complete unchanged publisher module zips, including MPL sources; original notices and generated SDK schema provenance accompany the CLI. Providers compiled upstream remain unused; fixed no-explain argv and read broker are the only runtime consumer.'}
    raw=(json.dumps(decision,indent=2)+'\n').encode()
    files['admission/k8sgpt-runtime-source.lock.json']=raw
    args.out.mkdir(parents=True,exist_ok=True)
    (args.out/'k8sgpt-runtime-source.lock.json').write_bytes(raw)
    with tarfile.open(args.out/'k8sgpt-runtime-source.tar','w') as out:
        for name,data in sorted(files.items()):
            header=tarfile.TarInfo(name);header.size=len(data);header.mode=0o644;header.mtime=0
            out.addfile(header,io.BytesIO(data))
    print(json.dumps({'modules':len(modules),'sourceBundleSHA256':sha((args.out/'k8sgpt-runtime-source.tar').read_bytes()),'sourceBytes':(args.out/'k8sgpt-runtime-source.tar').stat().st_size,'lockSHA256':sha(raw)}))


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    source_group=parser.add_mutually_exclusive_group(required=True)
    source_group.add_argument('--source',type=Path)
    source_group.add_argument('--source-archive',type=Path)
    parser.add_argument('--module-cache',type=Path,required=True)
    parser.add_argument('--out',type=Path,required=True)
    prepare(parser.parse_args())

#!/usr/bin/env python3
"""Build the Worker material offline with the exact licensed no-LLM CLI.

Inputs are authenticated locked source artifacts and locally cached modules.
Output remains outside Git; Bundle builders must include both runtime source
materials, original notices and the image's SBOM. No registry pulls occur.
"""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import tarfile

GO_IMAGE='golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414'
EXPECTED='6f9152ff31d2692a14e880ae73c2fe35dbc2938560d2c55227f4b0c67409af53'


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--analyzer-source',type=Path,required=True)
    p.add_argument('--module-cache',type=Path,required=True)
    p.add_argument('--out',type=Path,required=True)
    p.add_argument('--image',required=True)
    a=p.parse_args()
    root=Path(__file__).resolve().parents[1]
    a.out=a.out.resolve();a.out.mkdir(parents=True,exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='ops-sp05-worker-build-') as d:
        task=Path(d);cache=task/'cache';cache.mkdir()
        # Read-only source and publisher module cache, no network or downloads.
        argv=['docker','run','--rm','--network=none','--pull=never','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--tmpfs','/tmp:rw,exec,mode=1777',
          '--mount',f'type=bind,src={a.analyzer_source.resolve()},dst=/work,readonly',
          '--mount',f'type=bind,src={a.module_cache.resolve()},dst=/gomodcache,readonly',
          '--mount',f'type=bind,src={cache},dst=/buildcache',
          '--mount',f'type=bind,src={a.out},dst=/output',
          '-e','GOPROXY=off','-e','GOSUMDB=off','-e','GOMODCACHE=/gomodcache','-e','GOCACHE=/buildcache','-e','CGO_ENABLED=0','-w','/work',GO_IMAGE]
        subprocess.run(argv+['go','build','-trimpath','-mod=readonly','-p','1','-o','/output/k8sgpt','.'],check=True)
        binary=(a.out/'k8sgpt').read_bytes()
        if hashlib.sha256(binary).hexdigest()!=EXPECTED:raise ValueError('rebuilt CLI differs from the admitted binary')
        # The source preparation authenticates original source + all exact zips.
        subprocess.run(['python3',str(root/'scripts/prepare-k8sgpt-runtime.py'),'--source-archive',str(a.analyzer_source.resolve().parent/'source.tar'),'--module-cache',str(a.module_cache.resolve()),'--out',str(a.out)],check=True)
        notices=a.out/'runtime-notices';notices.mkdir(exist_ok=True)
        with tarfile.open(a.out/'k8sgpt-runtime-source.tar','r:') as package:
            for member in package.getmembers():
                if not member.isfile() or not (member.name.startswith('notices/') or member.name=='toolchain/go1.27.1/LICENSE'):continue
                target=notices/'k8sgpt'/member.name
                if not target.resolve().is_relative_to(notices.resolve()):raise ValueError('unsafe original notice path')
                target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(package.extractfile(member).read())
        distribution={'sourceMaterial':'k8sgpt-runtime-source.tar','sourceSHA256':'sha256:'+hashlib.sha256((a.out/'k8sgpt-runtime-source.tar').read_bytes()).hexdigest(),'binarySHA256':'sha256:'+EXPECTED,'closureLock':'k8sgpt-runtime-source.lock.json','state':'must-accompany-distributed-image','pyrca':'disabled-excluded'}
        (notices/'k8sgpt'/'SOURCE-DISTRIBUTION.json').write_text(json.dumps(distribution,indent=2)+'\n')
        shutil.copy2(a.out/'k8sgpt-runtime-source.lock.json',notices/'k8sgpt'/'k8sgpt-runtime-source.lock.json')
        build_env={'GOOS':'linux','GOARCH':'arm64','CGO_ENABLED':'0','GOPROXY':'off','GOSUMDB':'off','GOTOOLCHAIN':'local'}
        import os
        subprocess.run(['go','build','-mod=readonly','-trimpath','-buildvcs=false','-ldflags=-s -w','-o',str(a.out/'ops-worker'),'./cmd/platform-worker'],cwd=root,env={**os.environ,**build_env},check=True)
        (task/'Dockerfile').write_text('FROM scratch\nCOPY --chown=65532:65532 --chmod=0555 ops-worker /ops-worker\nCOPY --chown=65532:65532 --chmod=0555 k8sgpt /opt/ops/bin/k8sgpt\nCOPY runtime-notices /usr/share/ops/legal\nUSER 65532:65532\nENTRYPOINT ["/ops-worker"]\n')
        shutil.copy2(a.out/'ops-worker',task/'ops-worker');shutil.copy2(a.out/'k8sgpt',task/'k8sgpt')
        shutil.copytree(notices,task/'runtime-notices')
        subprocess.run(['docker','build','--network=none','--pull=false','-t',a.image,str(task)],check=True)
        image=subprocess.check_output(['docker','image','inspect',a.image,'--format','{{.Id}}'],text=True).strip()
        print(json.dumps({'image':image,'analyzerSHA256':EXPECTED,'sourceClosure':str(a.out/'k8sgpt-runtime-source.tar'),'state':'built-awaiting-runtime-and-distribution-gates'}))


if __name__=='__main__':main()

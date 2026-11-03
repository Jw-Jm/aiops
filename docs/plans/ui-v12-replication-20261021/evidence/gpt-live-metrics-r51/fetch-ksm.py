"""Fetch an exact official amd64 OCI image as data; no local container runtime."""
from pathlib import Path
import concurrent.futures,hashlib,json,tarfile,urllib.request
root=Path('/tmp/ops-r51-material');oci=root/'ksm-oci';(oci/'blobs/sha256').mkdir(parents=True,exist_ok=False)
base='https://us-central1-docker.pkg.dev/v2/k8s-artifacts-prod/images/kube-state-metrics/kube-state-metrics/'
headers={'Accept':'application/vnd.docker.distribution.manifest.v2+json'}
index=json.loads((root/'ksm-index.json').read_text());descriptor=next(x for x in index['manifests']if x['platform']=={'architecture':'amd64','os':'linux'})
assert descriptor['digest']=='sha256:12d340f106edf923db88bd637666c864cb9d02b53b66a3976b535750cd2ef74d'
def fetch(descriptor,kind):
 digest=descriptor['digest'];path=oci/'blobs/sha256'/digest.split(':')[1]
 with urllib.request.urlopen(urllib.request.Request(base+kind+'/'+digest,headers=headers),timeout=60)as r:path.write_bytes(r.read())
 assert 'sha256:'+hashlib.sha256(path.read_bytes()).hexdigest()==digest
 assert path.stat().st_size==descriptor['size'];return path
manifest=fetch(descriptor,'manifests');m=json.loads(manifest.read_text())
with concurrent.futures.ThreadPoolExecutor(max_workers=4)as pool:list(pool.map(lambda d:fetch(d,'blobs'),[m['config'],*m['layers']]))
descriptor['annotations']={'org.opencontainers.image.ref.name':'v2.17.0'}
(oci/'index.json').write_text(json.dumps({'schemaVersion':2,'manifests':[descriptor]}));(oci/'oci-layout').write_text('{"imageLayoutVersion":"1.0.0"}')
with tarfile.open(root/'ksm-oci.tar','w')as t:
 for p in oci.rglob('*'):
  if p.is_file():t.add(p,arcname=str(p.relative_to(oci)))
print(json.dumps({'image':'registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.17.0','digest':descriptor['digest'],'allBlobDigestsVerified':True,'bytes':(root/'ksm-oci.tar').stat().st_size}))

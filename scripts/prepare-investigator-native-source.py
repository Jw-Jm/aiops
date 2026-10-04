#!/usr/bin/env python3
"""Authenticate exact native source closure without inferring wheel linkage.

Versions/hashes below are bound to the measured extension and immutable
publisher build locks; CRC32C is an explicit platform source rebuild. Source
supersets include build/test files, whose own notices govern those files.
"""
import argparse, hashlib, json, pathlib, tarfile, urllib.request

MATERIALS = (
 ('openssl','4.0.3','https://www.openssl.org/source/openssl-4.0.3.tar.gz','325b5c806167c13b40b1ffeadfe0248197c00eccc4cf123ec1e28d2d2fd216d9'),
 ('librdkafka','2.15.1','https://codeload.github.com/confluentinc/librdkafka/tar.gz/c58bbed850ebe1cc493259d11e4de6ca930d5120','7264b8af3d288a6428b2c3822675fcf266eaca2e8a3fbce21fb84239752e5944'),
 ('openssl','3.5.7','https://www.openssl.org/source/openssl-3.5.7.tar.gz','a8c0d28a529ca480f9f36cf5792e2cd21984552a3c8e4aa11a24aa31aeac98e8'),
 ('zlib','1.3.2','https://zlib.net/fossils/zlib-1.3.2.tar.gz','bb329a0a2cd0274d05519d61c667c062e06990d72e125ee2dfa8de64f0119d16'),
 ('zstd','1.5.7','https://github.com/facebook/zstd/releases/download/v1.5.7/zstd-1.5.7.tar.gz','eb33e51f49a15e023950cd7825ca74a4a2b43db8354825ac24fc1b7ee09e6fa3'),
 ('curl','8.21.0','https://curl.se/download/curl-8.21.0.tar.gz','d9b327997999045a24cda50f3983e69e51c516bd8be6ef9842fc7f99135e33bb'),
 ('crc32c','02e65f4fd3065d27b2e29324800ca6d04df16126','https://codeload.github.com/google/crc32c/tar.gz/02e65f4fd3065d27b2e29324800ca6d04df16126','50ac512ad20ebca0be21bcf7c3c6d15aa3fcc27550b09825fab061bffe512bdf'),
 ('libjq','1.8.2','https://github.com/jqlang/jq/releases/download/jq-1.8.2/jq-1.8.2.tar.gz','71b8d6e8f5fe81f6c6d0d110e3892251f6ce76ed095abd315e26e6e1193af3af'),
)
def sha(data): return 'sha256:'+hashlib.sha256(data).hexdigest()
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--out',type=pathlib.Path,required=True);a=p.parse_args();out=a.out.resolve();dest=out/'native';dest.mkdir(exist_ok=True)
 previous=json.loads((dest/'source.lock.json').read_text()) if (dest/'source.lock.json').exists() else {'materials':[]}
 existing={(x['name'],x['version']):x for x in previous['materials']};records=[]
 for name,version,url,digest in MATERIALS:
  record=existing.get((name,version),{});path=out/record.get('path','native/'+name+'-'+version+'.tar.gz')
  if not path.exists():
   with urllib.request.urlopen(url,timeout=90) as response:data=response.read()
   if sha(data)!='sha256:'+digest:raise ValueError('publisher source hash mismatch: '+name)
   path.write_bytes(data)
  if sha(path.read_bytes())!='sha256:'+digest:raise ValueError('cached native source changed: '+name)
  record.update(name=name,version=version,sourceURL=url,sourceSHA256='sha256:'+digest,path=str(path.relative_to(out)),notices=[])
  with tarfile.open(path) as archive:
   for member in archive:
    if member.isfile() and any(w in pathlib.PurePosixPath(member.name).name.upper() for w in ('LICENSE','COPYRIGHT','NOTICE','COPYING')):
     data=archive.extractfile(member).read();target=dest/'notices'/(name+'-'+version)/member.name
     if not target.resolve().is_relative_to(dest.resolve()):raise ValueError('unsafe original notice path')
     target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(data);record['notices'].append({'path':str(target.relative_to(out)),'sha256':sha(data)})
  if not record['notices']:raise ValueError('native publisher notices absent: '+name)
  records.append(record)
 kafka=next(x for x in records if x['name']=='librdkafka')
 with tarfile.open(out/kafka['path']) as archive:
  for name,module in (('openssl','libssl'),('zlib','zlib'),('zstd','libzstd'),('curl','libcurl')):
   material=next(x for x in records if x['name']==name and x['version']!='4.0.3')
   member=next(m for m in archive if m.name.endswith('/mklove/modules/configure.'+module))
   text=archive.extractfile(member).read().decode()
   if material['version'] not in text or material['sourceSHA256'].split(':')[1] not in text:raise ValueError('immutable publisher build dependency differs: '+name)
   material['publisherBuildLockSHA256']=material['sourceSHA256']
 jq=next(x for x in records if x['name']=='libjq');runtime=json.loads((out/'runtime.lock.json').read_text());parent=next(x for x in runtime['sources'] if x['name']=='jq')
 if sha((out/parent['path']).read_bytes())!=parent['sha256']:raise ValueError('Python jq publisher archive changed')
 with tarfile.open(out/parent['path']) as archive:
  member=next(m for m in archive if m.name.endswith('/deps/jq-1.8.2.tar.gz'))
  if sha(archive.extractfile(member).read())!=jq['sourceSHA256']:raise ValueError('nested native jq publisher source differs')
  jq.update(publisherParentSourceSHA256=parent['sha256'],publisherMember=member.name)
 crc=next(x for x in records if x['name']=='crc32c');crc['admissionBasis']='Exact platform-native build source for unmodified google-crc32c 1.9.0 publisher sdist; old publisher wheel source linkage not claimed'
 (dest/'source.lock.json').write_text(json.dumps({'schemaVersion':1,'materials':records,'scope':'Measured extensions plus exact publisher native build/source supersets; original notices govern every file; CRC32C is platform-rebuilt','publisherBuildDependenciesVerified':True},indent=2)+'\n')
 print('authenticated native source closure:',len(records))
if __name__=='__main__':main()

import pathlib,json,hashlib,yaml,tarfile,re,shutil
import argparse
a=argparse.ArgumentParser();a.add_argument('--material-root',type=pathlib.Path,required=True);args=a.parse_args();root=pathlib.Path(__file__).resolve().parents[1];p=args.material_root.resolve();src=p/'postgresql-bookworm-source';report=json.loads((p/'postgresql-bookworm-source-closure-result.json').read_text());receipt=json.loads((p/'materials/postgresql-bookworm-candidate-receipt.json').read_text());image=receipt['reference'].split('@')[1]
assert image=='sha256:3645570cccdfa447589da9f57dd740faa29b30938e861289a5574b6ca6b03826'
def sha(f):
 with pathlib.Path(f).open('rb')as r:return 'sha256:'+hashlib.file_digest(r,'sha256').hexdigest()
archive=p/'materials/postgresql-bookworm-candidate.oci.tar';assert sha(archive)==receipt['archiveSHA256']
with tarfile.open(archive)as t:
 def blob(d):
  m=t.getmember('blobs/sha256/'+d['digest'].split(':')[1]);assert m.size==d['size'];data=t.extractfile(m).read();assert 'sha256:'+hashlib.sha256(data).hexdigest()==d['digest'];return data
 index=json.load(t.extractfile('index.json'));d=index['manifests'][0];assert d['digest']==image and len(index['manifests'])==1;up=json.loads(blob(d));native=[x for x in up['manifests']if x.get('platform',{}).get('architecture')=='amd64'and x.get('platform',{}).get('os')=='linux'];assert len(native)==1 and native[0]['digest']==receipt['nativeDigest'];manifest=json.loads(blob(native[0]));cfg=json.loads(blob(manifest['config']));assert cfg['os']=='linux'and cfg['architecture']=='amd64'
 for l in manifest['layers']:blob(l)
source_tar=p/'postgresql-bookworm-corresponding-source.tar';assert sha(source_tar)==report['sourceBundleSHA256'];poc=json.loads((root/'docs/evidence/amd64-deployment-20261009/postgresql-bookworm-native-poc-result.json').read_text());assert poc['sqlPassed']and poc['reference']==receipt['reference']
rows={x['name']:x for x in json.loads((src/'native-packages.json').read_text())};paragraphs={}
for block in (src/'Sources.txt').read_text().split('\n\n'):
 fields={};k=None
 for l in block.splitlines():
  if l.startswith(' ')and k:fields[k]+='\n'+l
  elif ':'in l:k,v=l.split(':',1);fields[k]=v.strip()
 if 'Package'in fields and 'Version'in fields:paragraphs[(fields['Package'],fields['Version'])]=fields
catalog=root/'bundle/component-catalog-amd64.yaml';text=catalog.read_text();c=next(x for x in yaml.safe_load((root/'bundle/component-catalog.yaml').read_text())['components']if x['name']=='postgresql');old=json.loads((root/'internal/supplychain/licenses/native-core-reviewed.json').read_text());mapping={};scopes=[]
for record in old['licenses']:
 if record['component']!='postgresql':continue
 q=dict(record);n=q['dependencyName'].removeprefix('native:');row=rows[n];q['imageDigest']=image;q['correspondingSourceBundleSHA256']=report['sourceBundleSHA256'];q['version']=row['version'];q['sourcePackage']=row['source'];q['sourceVersion']=row['sourceVersion'];changed=row['source']in['pcre2','perl','openssl','tzdata']
 if changed:
  para=paragraphs[(row['source'],row['sourceVersion'])];dsc=next(l.split()for l in para['Checksums-Sha256'].splitlines()if l.strip()and l.split()[2].endswith('.dsc'));q['sourceArchiveSHA256']='sha256:'+dsc[0];q['source']='https://deb.debian.org/'+('debian-security'if para['Directory'].startswith('pool/updates/')else'debian')+'/'+para['Directory']+'/'+dsc[2];assert sha(src/'sources'/dsc[2])==q['sourceArchiveSHA256'];data=('\n===== Original publisher notice: /usr/share/doc/'+n+'/copyright =====\n').encode()+(src/'notices'/n/'copyright').read_bytes();q['noticeDigest']='sha256:'+hashlib.sha256(data).hexdigest()
 else:assert sha(root/q['noticePath'])==q['noticeDigest']and row['version']==record['version']and row['sourceVersion']==record['sourceVersion']
 q['id']='LicenseRef-Task27-AMD64-Native-'+hashlib.sha256(json.dumps(q,sort_keys=True).encode()).hexdigest()
 if changed:
  q['noticePath']='third_party/licenses/native-amd64/'+q['id']+'.txt';f=root/q['noticePath'];f.parent.mkdir(parents=True,exist_ok=True);f.write_bytes(data)
 mapping[record['id']]=q;scopes.append(q)
for d in c['dependencyClosure']:
 if d['license']in mapping:
  q=mapping[d['license']];d.update(license=q['id'],version=q['version'],source=q['source'],digest=q['sourceArchiveSHA256'],sourceArchiveSHA256=q['sourceArchiveSHA256'])
for f in c['fileLicenses']:
 if f['license']in mapping:
  q=mapping[f['license']];f.update(license=q['id'],path=q['noticePath'],digest=q['noticeDigest'])
c.update(digest=image,architectures=['linux/amd64'],correspondingSourceBundleSHA256=report['sourceBundleSHA256'],pocReport='docs/poc/core-amd64-postgresql-admission.md');ev=['docs/evidence/amd64-deployment-20261009/postgresql-bookworm-source-closure-result.json','docs/evidence/amd64-deployment-20261009/postgresql-bookworm-native-poc-result.json','docs/evidence/amd64-deployment-20261009/postgresql-bookworm-candidate-dependency-parity.json'];c['conformanceFixtures']+=ev
begin=re.search(r'^  - name: postgresql\n',text,re.M).start();end=re.search(r'^  - name: ',text[begin+1:],re.M).start()+begin+1;entry=''.join('  '+l+'\n'for l in yaml.safe_dump([c],sort_keys=False,width=150).splitlines());catalog.write_text(text[:begin]+entry+text[end:]);reg=root/'internal/supplychain/licenses/native-core-amd64-reviewed.json';data=json.loads(reg.read_text());data['licenses']=[x for x in data['licenses']if x['component']!='postgresql']+scopes;reg.write_text(json.dumps(data,indent=2)+'\n')
review=root/'third_party/admission/core-amd64/postgresql.json';review.write_text(json.dumps({'state':'qualified','architecture':'linux/amd64','imageDigest':image,'nativeDigest':receipt['nativeDigest'],'sourceClosure':report,'nativeLicenseScopes':scopes,'changedSourcePackages':['pcre2','perl','openssl','tzdata'],'fullDeploymentAcceptance':'NOT_RUN'},indent=2)+'\n')
for path in [c['pocReport'],*ev,str(review.relative_to(root)),*[x['noticePath']for x in scopes]]:
 target=root/'bundle/evidence'/path;target.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(root/path,target)
print('PostgreSQL native source variant admitted; finite scopes:',len(scopes))

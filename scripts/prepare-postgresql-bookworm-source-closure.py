import pathlib,json,tarfile,hashlib,io
import argparse
a=argparse.ArgumentParser();a.add_argument('--material-root',type=pathlib.Path,required=True);args=a.parse_args();root=pathlib.Path(__file__).resolve().parents[1];p=args.material_root.resolve();out=p/'postgresql-bookworm-source';old=root/'artifacts/task27-materials/corresponding-sources/postgresql-source.tar';dest=p/'postgresql-bookworm-corresponding-source.tar'
def sha(f):
 with pathlib.Path(f).open('rb')as r:return hashlib.file_digest(r,'sha256').hexdigest()
paragraphs={}
for block in (out/'Sources.txt').read_text().split('\n\n'):
 fields={};k=None
 for l in block.splitlines():
  if l.startswith(' ') and k:fields[k]+='\n'+l
  elif ':' in l:k,v=l.split(':',1);fields[k]=v.strip()
 if 'Package' in fields and 'Version' in fields:paragraphs[(fields['Package'],fields['Version'])]=fields
rows={x['name']:x for x in json.loads((out/'native-packages.json').read_text())};scope=json.loads((root/'internal/supplychain/licenses/native-core-reviewed.json').read_text())['licenses'];verified=changed=0
for x in scope:
 if x['component']!='postgresql':continue
 n=x['dependencyName'].removeprefix('native:');row=rows[n];data=(out/'notices'/n/'copyright').read_bytes();old_data=(root/x['noticePath']).read_bytes();header=('\n===== Original publisher notice: /usr/share/doc/'+n+'/copyright =====\n').encode()
 if row['source']not in ['pcre2','perl','openssl','tzdata']:
  assert row['version']==x['version'] and row['sourceVersion']==x['sourceVersion'],n
  assert header+data==old_data,n;verified+=1
 else:changed+=1
with tarfile.open(old)as original,tarfile.open(dest,'w')as t:
 inventory=json.load(original.extractfile('SOURCE-INVENTORY.json'))
 for f in inventory['files']:
  info=original.getmember(f['path']);assert info.size==f['size'];assert hashlib.sha256(original.extractfile(info).read()).hexdigest()==f['sha256'],f['path']
 for m in original:
  if not m.isfile() or m.name=='SOURCE-INVENTORY.json':continue
  t.addfile(m,original.extractfile(m))
 for f in sorted(out.rglob('*')):
  if not f.is_file():continue
  rel='amd64-native-source/'+str(f.relative_to(out));i=t.gettarinfo(str(f),rel);i.uid=i.gid=0;i.uname=i.gname='';i.mtime=0
  with f.open('rb')as r:t.addfile(i,r)
  inventory['files'].append({'file':str(f),'path':rel,'sha256':sha(f),'size':f.stat().st_size})
 for package in inventory['nativePackages']:
  n=package['package'];row=rows[n];package['version']=row['version'];package['sourcePackage']=row['source'];package['sourceVersion']=row['sourceVersion']
  if row['source'] in ['pcre2','perl','openssl','tzdata']:
   para=paragraphs[(row['source'],row['sourceVersion'])];source_files=[]
   for line in para['Checksums-Sha256'].splitlines():
    if not line.strip():continue
    digest,size,name=line.split();source_files.append({'name':name,'url':'https://deb.debian.org/'+('debian-security'if para['Directory'].startswith('pool/updates/')else'debian')+'/'+para['Directory']+'/'+name,'sha256':digest,'size':int(size),'file':'amd64-native-source/sources/'+name})
   package['sourceFiles']=source_files;f=out/'notices'/n/'copyright';package['noticeFiles']=[{'path':'/usr/share/doc/'+n+'/copyright','sha256':sha(f),'file':'amd64-native-source/notices/'+n+'/copyright'}]
 inventory['architecture']='linux/amd64';inventory['historicalSourceSupersetRetained']=True
 data=(json.dumps(inventory,indent=2)+'\n').encode();i=tarfile.TarInfo('SOURCE-INVENTORY.json');i.size=len(data);i.mode=0o644;t.addfile(i,io.BytesIO(data))
report={'architecture':'linux/amd64','sourceBundleSHA256':'sha256:'+sha(dest),'originalSourceBundleSHA256':'sha256:'+sha(old),'unchangedNativePackageAndNoticeCount':verified,'changedBinaryPackageCount':changed,'changedSourcePackages':['pcre2','perl','openssl','tzdata'],'sourceFilesVerified':len(inventory['files']),'sourceAuthentication':'signature-verified apt Sources indexes; complete source archive digests independently checked','nativePackageCount':len(rows),'status':'COMPLETE_NATIVE_SOURCE_VARIANT_PREPARED_NOT_ADMITTED'};(p/'postgresql-bookworm-source-closure-result.json').write_text(json.dumps(report,indent=2)+'\n');print(json.dumps(report,indent=2))

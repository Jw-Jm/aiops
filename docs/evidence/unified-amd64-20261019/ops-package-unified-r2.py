from pathlib import Path
import copy,hashlib,io,json,os,subprocess,tarfile,datetime
os.umask(0o077)
b=Path('/opt/ops-verification-20261019-1a7c7da');o=b/'distribution';work=o/'installer-source-r2';work.mkdir(exist_ok=False)
with tarfile.open(o/'ops-unified-source-r2.tar.gz')as t:t.extractall(work,filter='data')
source=o/'current-platform-source-r2.tar'
with tarfile.open(source,'w')as dst,tarfile.open(o/'ops-unified-source-r2.tar.gz')as src:
 for m in src:dst.addfile(copy.copy(m),src.extractfile(m)if m.isfile()else None)
def sha(p):return 'sha256:'+hashlib.file_digest(Path(p).open('rb'),'sha256').hexdigest()
receipts={x['name']:x for x in json.loads((o/'native-build-receipts.json').read_text())if 'imageDigest'in x};assert len(receipts)==5
spec=json.loads(Path('/data/ops-amd64-final-20261010/build-spec-r12.json').read_text());spec['bundleId']='unified-amd64-20261019-r2';spec['platformVersion']='1.0.0'
materials=spec['materials'];files=spec['files']
def update(path,f):
 item=next((x for x in files if x['path']==path),None)
 if item is None:item={'path':path,'kind':'source'if path.startswith('sources/')else 'oci'if path.startswith('oci/')else 'sbom'if path.startswith('sbom/')else 'license'};files.append(item)
 item.update(source=str(f),digest=sha(f),size=f.stat().st_size)
 for m in materials:
  if m['payloadRef']==path:m['digest']=item['digest']
def sbom(name,archive,license_ref,details):
 p=o/(name+'.spdx.json');dg=sha(archive);doc={'spdxVersion':'SPDX-2.3','dataLicense':'CC0-1.0','SPDXID':'SPDXRef-DOCUMENT','name':name,'documentNamespace':'https://ops.local/unified-amd64-20261019/'+name+'/'+dg.split(':')[1],'creationInfo':{'creators':['Tool: ops-current-material-inventory'],'created':'2026-10-19T00:00:00Z'},'documentDescribes':['SPDXRef-Material'],'packages':[{'SPDXID':'SPDXRef-Material','name':name,'downloadLocation':'NOASSERTION','filesAnalyzed':False,'licenseConcluded':'NOASSERTION','licenseDeclared':'NOASSERTION','copyrightText':'See unchanged publisher originals and corresponding source','checksums':[{'algorithm':'SHA256','checksumValue':dg.split(':')[1]}],'sourceInfo':json.dumps(details,sort_keys=True)}],'files':[{'SPDXID':'SPDXRef-File','fileName':archive.name,'checksums':[{'algorithm':'SHA256','checksumValue':dg.split(':')[1]}],'licenseConcluded':'NOASSERTION','licenseInfoInFiles':['NOASSERTION'],'copyrightText':'Original rights preserved; see '+license_ref}],'relationships':[{'spdxElementId':'SPDXRef-DOCUMENT','relationshipType':'DESCRIBES','relatedSpdxElement':'SPDXRef-Material'},{'spdxElementId':'SPDXRef-Material','relationshipType':'CONTAINS','relatedSpdxElement':'SPDXRef-File'}]};
 dependencies=json.loads((o/'ops-current-sbom-dependencies.json').read_text()).get(name,[])
 for i,item in enumerate(dependencies):
  ident='SPDXRef-Dependency-'+str(i);doc['packages'].append({'SPDXID':ident,'name':item['name'],'versionInfo':item['version'],'downloadLocation':item.get('source','NOASSERTION'),'filesAnalyzed':False,'licenseConcluded':'NOASSERTION','licenseDeclared':item.get('license','NOASSERTION'),'copyrightText':'Retained original publisher notice governs','sourceInfo':'Complete measured/reviewed distribution source and build superset; conditional/dev entries included. '+json.dumps(item,sort_keys=True)});doc['relationships'].append({'spdxElementId':'SPDXRef-Material','relationshipType':'DEPENDS_ON','relatedSpdxElement':ident})
 p.write_text(json.dumps(doc,indent=2)+'\n');return p
for name in ['platform-api','platform-worker','command-runner','holmesgpt','platform-web']:
 archive=o/(name+'.oci.tar');r=receipts[name];assert sha(archive)==r['archiveSHA256']
 material=next((x for x in materials if x['name']==name),None)
 if material is None:
  material={'name':name,'kind':'container-image','version':'1.0.0','architecture':'linux/amd64','payloadRef':'oci/'+name+'.tar','sbomRef':'sbom/'+name+'.json','licenseRef':'licenses/'+name+'.txt','installAfter':[]};materials.append(material)
 update(material['payloadRef'],archive)
 if name in ['platform-api','platform-worker']:material['version']='1.0.0'
 # Preserve original detailed publisher SBOM as a sidecar, bind the new image/source in the current envelope.
 if name!='platform-web':
  original=Path(next(x['source']for x in files if x['path']==material['sbomRef']));assert original.is_file()
 details={'runtimeSourceCommit':'1a7c7da4af7d74d80df0df0aec72868cbe2785cc','imageDigest':r['imageDigest'],'dependencySBOM':'current SPDX package inventory; retained build/source superset explicitly marked','correspondingSource':'sources/current-platform-source.tar','licenseOriginals':'all retained Go/Python/native/npm originals; unmodified third-party identities and notices'}
 update(material['sbomRef'],sbom(name,archive,material['licenseRef'],details))
 if name=='platform-web':
  notice=o/'platform-web-notices.txt';parts=[]
  for directory in ['sp08-web-npm','sp08-nginx-runtime']:
   for p in sorted((work/'platform/third_party/licenses'/directory).rglob('*')):
    if p.is_file():parts.append('\n===== '+str(p.relative_to(work/'platform'))+' =====\n'+p.read_text(errors='replace'))
  notice.write_text('Original publisher notices; no aggregate relicensing. Complete source is supplied alongside this material.\n'+''.join(parts));update(material['licenseRef'],notice)
for name,f in [('holmesgpt-source',o/'holmesgpt-source.tar'),('command-runner-source',o/'command-runner-source.tar'),('opa-sdk-source',o/'go-inputs/runtime-source/runtime-go-source.tar'),('db-migrate-source',o/'runtime-go-source.tar'),('opsctl-source',source),('opsctl',o/'opsctl-unified-amd64-20261019-r2'),('db-migrate',o/'ops-db-migrate-qualified-amd64')]:
 m=next(x for x in materials if x['name']==name);update(m['payloadRef'],f);update(m['sbomRef'],sbom(name,f,m['licenseRef'],{'installerAdmissionCommit':'9ade735','runtimeSourceCommit':'1a7c7da','correspondingSourceArchiveSHA256':sha(f)}))
 if name in ['opsctl','opsctl-source']:m['version']='1.0.0'
for name in ['ops-platform','ops-dependencies']:
 m=next(x for x in materials if x['name']==name+'-chart');update(m['payloadRef'],b/'candidate/charts'/(name+'-0.1.0.tgz'));update(m['sbomRef'],sbom(name,b/'candidate/charts'/(name+'-0.1.0.tgz'),m['licenseRef'],{'runtimeSourceCommit':'1a7c7da'}))
materials.append({'name':'platform-web-source','kind':'source','version':'1.0.0','architecture':'linux/amd64','payloadRef':'sources/sp08-web-source.tar','sbomRef':'sbom/platform-web-source.json','licenseRef':'licenses/platform-web.txt','installAfter':[]})
update('sources/sp08-web-source.tar',o/'platform-web-source.tar')
update('sbom/platform-web-source.json',sbom('platform-web-source',o/'platform-web-source.tar','licenses/platform-web.txt',{'sourceCommit':'1a7c7da','sourcesAndLicensesVerified':True}))
(o/'build-spec-unified.json').write_text(json.dumps(spec,indent=2)+'\n')
# A separate tools bundle can be verified by the previously trusted installer.
selected={'opsctl','opsctl-source','opa-sdk-source'};tool_materials=[x for x in materials if x['name']in selected];paths={m[k]for m in tool_materials for k in ['payloadRef','sbomRef','licenseRef']}
tools={'schemaVersion':1,'bundleId':'unified-amd64-installer-20261019-r2','platformVersion':'1.0.0','architecture':'linux/amd64','materials':tool_materials,'files':[x for x in files if x['path']in paths]}
(o/'build-spec-tools.json').write_text(json.dumps(tools,indent=2)+'\n')
for label,config,output in [('tools','build-spec-tools.json','installer-bundle-r2'),('core','build-spec-unified.json','core-bundle-r2')]:
 with(o/('bundle-r2-'+label+'-build.json')).open('w')as result,(o/('bundle-r2-'+label+'-build.log')).open('w')as log:
  subprocess.run([str(o/'opsctl-unified-amd64-20261019-r2'),'bundle','build','--spec',str(o/config),'--output',str(o/output),'--signing-key','/opt/ops-amd64-validation-20261009/private/bundle-signing.pem','--architecture','linux/amd64'],cwd=work/'platform',env={**os.environ,'TMPDIR':str(o)},stdout=result,stderr=log,check=True,timeout=600)
 print(label,'BUNDLE_BUILT',flush=True)

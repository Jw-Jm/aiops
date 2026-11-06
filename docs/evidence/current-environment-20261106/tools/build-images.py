from pathlib import Path
import subprocess,json,shutil,hashlib
r=Path('/data/ops-current');rel=r/'releases/9372674a87201a4ccaab6737a1019808ea41ffd5';images={}
for name in ['api','worker']:
 context=rel/('image-'+name);context.mkdir(exist_ok=True);shutil.copy2(rel/('bin/platform-'+name),context/('ops-'+name))
 if name=='worker':
  material=Path('/data/ops-worker-build-20261105/image');shutil.copy2(material/'k8sgpt',context/'k8sgpt');shutil.copytree(material/'legal',context/'legal',dirs_exist_ok=True)
  assert hashlib.sha256((context/'k8sgpt').read_bytes()).hexdigest()=='e6ce7073f35889bc678443034169d4a64e94106442a5775fe3f59ca4ae6b66a4'
  docker='FROM scratch\nCOPY --chown=65532:65532 ops-worker /ops-worker\nCOPY --chown=65532:65532 k8sgpt /opt/ops/bin/k8sgpt\nCOPY legal /usr/share/ops/legal\nUSER 65532:65532\nENTRYPOINT ["/ops-worker"]\n'
 else:
  docker='FROM sha256:9b092b51ca55d1bc889326afc127f4845b4f3dc4683f502568643481eb3b10c6\nCOPY --chown=65532:65532 ops-api /ops-api\nUSER 65532:65532\nENTRYPOINT ["/ops-api"]\n'
  # Resolve the already cached immutable base ID, never a registry tag.
  info=json.loads(subprocess.check_output(['docker','image','inspect','ops.local/verification-20261019/platform-api:53bae79']))[0];docker=docker.replace(docker.splitlines()[0],'FROM '+info['Id'])
 (context/'Dockerfile').write_text(docker);tag='ops.local/current/platform-'+name+':9372674'
 p=subprocess.run(['docker','build','--network=none','--pull=false','-t',tag,str(context)],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True);(r/('evidence/build-image-'+name+'.log')).write_text(p.stdout);assert p.returncode==0,p.stdout[-1800:]
 archive=rel/('platform-'+name+'.tar');subprocess.run(['docker','save','-o',str(archive),tag],check=True)
 subprocess.run(['ctr','--namespace','k8s.io','images','import','--digests',str(archive)],check=True,stdout=subprocess.DEVNULL)
 rows=subprocess.check_output(['ctr','--namespace','k8s.io','images','ls'],text=True).splitlines();line=next(l for l in rows if l.split()[0]==tag);digest=line.split()[2]
 images[name]={'tag':tag,'image':'ops.local/current/platform-'+name+'@'+digest,'manifestDigest':digest,'imageID':json.loads(subprocess.check_output(['docker','image','inspect',tag]))[0]['Id'],'binarySHA256':hashlib.sha256((context/('ops-'+name)).read_bytes()).hexdigest()}
(rel/'images.json').write_text(json.dumps(images,indent=2)+'\n');print(json.dumps(images))

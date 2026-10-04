#!/usr/bin/env python3
"""Run only inside the pinned ephemeral Python image with /material mounted.
Authenticate the exact Debian base source closure using signed apt indexes.
"""
import json,pathlib,subprocess,hashlib,shutil,tarfile
out=pathlib.Path('/material/base');m=json.loads((out/'packages.json').read_text());p=out/'notices';p.mkdir(exist_ok=True)
for source in pathlib.Path('/usr/share/doc').glob('*/copyright'):
 target=p/source.parent.name/'copyright';target.parent.mkdir(exist_ok=True);shutil.copy2(source,target)
# Publisher-verified apt Sources indexes authenticate each exact source version.
pathlib.Path('/etc/apt/sources.list.d/sp06-sources.sources').write_text('Types: deb-src\nURIs: http://deb.debian.org/debian\nSuites: trixie trixie-updates\nComponents: main\nSigned-By: /usr/share/keyrings/debian-archive-keyring.pgp\n\nTypes: deb-src\nURIs: http://deb.debian.org/debian-security\nSuites: trixie-security\nComponents: main\nSigned-By: /usr/share/keyrings/debian-archive-keyring.pgp\n')
subprocess.run(['apt-get','update'],check=True)
p=out/'sources';p.mkdir(exist_ok=True)
for name,version in sorted({(x['source'],x['sourceVersion']) for x in m['packages']}):
 subprocess.run(['apt-get','source','--download-only',name+'='+version],cwd=p,check=True)
proof=out/'publisher-indexes';proof.mkdir(exist_ok=True)
for f in pathlib.Path('/var/lib/apt/lists').glob('*'):
 if f.is_file() and any(x in f.name for x in ('InRelease','Sources','Release')):shutil.copy2(f,proof/f.name)
files=[{'path':str(f.relative_to(out)),'sha256':'sha256:'+hashlib.sha256(f.read_bytes()).hexdigest()} for f in sorted(out.rglob('*')) if f.is_file() and f.name not in ('source.lock.json','prepare.py')]
(out/'source.lock.json').write_text(json.dumps({'baseImage':m['baseImage'],'packages':m['packages'],'authentication':'Debian archive signed Sources indexes and apt package hash verification','files':files},indent=2)+'\n')
print('BASE_SOURCE_CLOSURE',len(m['packages']),len(files))

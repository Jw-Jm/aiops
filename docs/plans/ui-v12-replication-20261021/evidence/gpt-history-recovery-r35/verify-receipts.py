"""Assert the recorded live outcomes, rather than treating HTTP200 as acceptance."""
import json
from pathlib import Path
root=Path(__file__).resolve().parent
names=['credential-recovery','namespace-persistence','namespace-rbac','archive-version-proof','namespace-before-grant-final','namespace-both-stable','namespace-outside-qualified','namespace-revoked-old-session','namespace-revoked-relogin','namespace-narrow-old-session','namespace-narrow-second','namespace-narrow-first','archive-read-authorized']
r={name:json.loads((root/(name+'.json')).read_text()) for name in names}
a='ops-native-v12-r24-20261026';b='ops-native-v12-r35-scope'
assert r['credential-recovery']['status']=='passed' and r['credential-recovery']['originalProjectionRestored']
assert {x['namespace'] for x in r['namespace-persistence']['rows']}=={a,b}
assert r['namespace-rbac']['after']=={'secondPods':'yes','outsidePods':'no','secrets':'no','podWrite':'no'}
assert r['archive-version-proof']['sameCanonicalAndContentDifferentEvidence']
assert len(r['namespace-both-stable']['requests'])==12
for read in r['namespace-both-stable']['requests']:
 assert read['status']==200 and len(read['rows'])==2 and {x['namespace'] for x in read['rows']}=={a,b} and len({x['canonicalId'] for x in read['rows']})==2
for key in ['namespace-before-grant-final','namespace-outside-qualified','namespace-narrow-second']:
 read=r[key]['requests'][0];assert read['status']==200 and read['rows']==[],key
for key in ['namespace-revoked-old-session','namespace-narrow-old-session']:assert r[key]['requests'][0]['status']==401,key
assert r['namespace-revoked-relogin']['requests'][0]['status']==403
read=r['namespace-narrow-first']['requests'][0];assert read['status']==200 and len(read['rows'])==1 and read['rows'][0]['namespace']==a
read=r['archive-read-authorized']['requests'][0]
assert read['status']==200 and read['meta']['partial'] is False and read['evidence']['state']=='archived_verified' and read['evidence']['sourceRevision']==3
assert read['evidence']['uid'] in read['evidence']['canonicalId'] and read['evidence']['namespace']==a and read['evidence']['rv']
print('live receipts passed: credential recovery, scope/source/RBAC, same-name UID separation, revocation, immutable archive body')

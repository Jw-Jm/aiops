"""Show that a new binding archives unchanged facts separately, without replacing old bytes."""
from pathlib import Path
import json,os,subprocess,socket
assert socket.gethostname()=='mssc'
root=Path('/data/ops-v12-native-visual-r24-20261026');work=root/'r35'
env=os.environ|{'PGPASSWORD':(root/'private/pg-password').read_text().strip()}
query="""SELECT COALESCE(json_agg(t),'[]'::json) FROM (SELECT old.evidence_id AS old_id,new.evidence_id AS new_id,old.canonical_id,old.content_digest FROM platform.evidence_metadata old JOIN platform.evidence_metadata new ON old.tenant_id=new.tenant_id AND old.canonical_id=new.canonical_id AND old.content_digest=new.content_digest WHERE old.tenant_id='01a177f1-372f-75b7-be90-e8121d1445f5' AND old.metadata->>'sourceRevision'='2' AND new.metadata->>'sourceRevision'='3' AND old.metadata->>'queryTemplateVersion'='kubernetes-projection/v2' AND new.metadata->>'queryTemplateVersion'='kubernetes-projection/v3' AND old.evidence_id<>new.evidence_id LIMIT 5)t"""
rows=json.loads(subprocess.check_output(['docker','exec','-e','PGPASSWORD','ops-v12-native-r24-pg','psql','-U','postgres','-d','ui_v12_r24_native','-tA','-c',query],env=env))
assert rows,'no unchanged old/new fact pair'
proof={'status':'passed','sameCanonicalAndContentDifferentEvidence':rows,'scope':'original evidence rows preserved; source revision contributes to new immutable identity'}
(work/'archive-version-proof.json').write_text(json.dumps(proof,indent=2));print(json.dumps({'status':'passed','matchedUnchangedFacts':len(rows)}))

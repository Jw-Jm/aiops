"""Verify retained combination receipts; does not operate the remote environment."""
import json
from pathlib import Path
p=Path(__file__).resolve().parent
read=lambda n:json.loads((p/n).read_text())
session=read('final-legacy-gate-and-scope.json')['session']
assert not session['administratorState']['isAdministrator']
grant=session['authorization']['grants']
assert len(grant)==1 and grant[0]['role']=='operator'
assert grant[0]['scope']=={'clusterUids':['79014400-9453-4cb4-9678-0fae47981d79'],'namespaces':['ops-current']}
assert grant[0]['bindingId']=='233b35d6-0ef4-46d1-a1e8-1ba3185656c0' and grant[0]['authorizationVersion']==2
conversation=read('final-history-create.json')['body']['data']
assert conversation['ownerSubject']==session['subject']
assert conversation['binding']['executionScope']['namespaces']==['ops-current']
assert conversation['binding']['executionScope']['clusterScoped'] is False
before=read('history-frozen-before.json');after=read('history-frozen-after.json')
assert before==after and after['sourceRevision']==2 and after['currentRcaRevision']==22
steps=[json.loads(line) for line in (p/'final-history-steps.jsonl').read_text().splitlines()]
assert [x['tool'] for x in steps]==['get_assistant_context','model','get_formal_references','model']
assert all(x['state']=='succeeded' for x in steps)
formal=steps[2]['result']['data'];ref=formal['formalReferences'][0]
assert formal['historical'] and ref['revision']==1 and ref['incidentId']==before['incidentId']
assert ref['contentDigest']=='sha256:d131fc8917122a7c9fd0ed2c661d8d47d28a0cf667ab8edb74b163f4f82f1a35'
assert not formal['omittedEvidence']
archives=formal['archivedEvidence'];ids={e['evidenceId'] for e in archives}
assert ids=={'3dbe6b3d-67b1-560e-9712-a5093ce69b3b','9f58d2ab-8723-5df5-931c-85b89676206f'}
assert all(e['sourceRevision']==1 and e['replayState']=='archived_verified' and e['facts']['reason']=='FailedMount' for e in archives)
assert {e['facts']['resourceVersion'] for e in archives}=={'2528117','2528128'}
turn=read('final-history-result.json')['body']['data']
assert turn['conversationId']==conversation['conversationId'] and turn['state'] in ('completed','partial')
answer=json.dumps(turn['answer'],ensure_ascii=False)
assert all(s in answer for s in ids|{'2528117','2528128',ref['contentDigest']})
assert all(x['status']==200 for x in read('final-citation-reads.json'))
assert all(x['status']==403 for x in read('final-namespace-denials.json'))
assert all(x['status']==401 for x in read('final-anonymous-denials.json'))
assert read('final-other-owner-denied.json')['status']==404
legacy=read('final-legacy-gate-and-scope.json')
assert legacy['status']==503 and legacy['body']['code']=='CAPABILITY_DISABLED'
assert read('final-legacy-job-count.json')['v2JobCount']==2
executions=[json.loads(line) for line in (p/'final-execution-identities.jsonl').read_text().splitlines()]
original=json.loads((p.parent/'incident-remediation-20261109/execution-identities.json').read_text())
assert {x['executionId'] for x in executions}==set(original['ids']) and sum(x['attempts'] for x in executions)==7
assert all(x['status']==200 for x in read('final-reused-evidence-reads.json'))
assert all(x['status']==200 and x['body']['data']['replayState']=='archived_verified' for x in read('final-reused-rollback-archives.json'))
print(json.dumps({'status':'passed','namespaceOnly':True,'historicalRca':1,'currentRca':22,'archivedSourceRevision':1,'currentSourceRevision':2,'newCommandExecutions':0,'retainedExecutions':9,'retainedAttempts':7,'modelAnswerState':turn['state'],'freshQuerySteps':0},ensure_ascii=False))

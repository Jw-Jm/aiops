import datetime,hashlib,json,socket,subprocess
from pathlib import Path
assert socket.gethostname()=='mssc'
root=Path('/data/ops-v12-native-visual-r24-20261026');work=root/'r43';ns='ops-native-v12-r24-20261026'
def run(args,**kw):return subprocess.check_output(args,text=True,**kw)
namespace=json.loads(run(['kubectl','get','ns',ns,'-o','json']))
assert namespace['metadata']['uid']=='731f0e88-1893-4836-844b-a29374c1ca11'
cm=json.loads(run(['kubectl','-n',ns,'get','cm','r28-runtime','-o','json']))['data']
sp06=json.loads(cm['sp06.json']);profile=sp06['assistant']['profile'];python=json.loads(cm['investigator.json'])
assert python['assistantModelProfile']==profile
admission=(root/'r28-worker/model-runtime-authorization.json').read_bytes()
assert profile['admissionEvidenceDigest']=='sha256:'+hashlib.sha256(admission).hexdigest()
protocol=(root/'evidence/deepseek-protocol-r27.json').read_bytes()
assert profile['evaluationDigest']=='sha256:'+hashlib.sha256(protocol).hexdigest()
sql="""BEGIN READ ONLY;
select json_build_object('version',version_id,'digest',digest,'name',logical_name,'content',content) from platform.registry_versions where logical_name='r28-read-only';
select json_build_object('canonicalId',canonical_id,'kind',kind,'namespace',namespace,'name',name,'metadata',metadata,'observedAt',observed_at,'deletedAt',deleted_at) from platform.resource_entities where tenant_id='01a177f1-372f-75b7-be90-e8121d1445f5' and namespace in ('ops-native-v12-r24-20261026','ops-native-v12-r35-scope');
select json_build_object('queryId',query_id,'resultDigest',result->>'resultDigest','state',state,'qualificationUntil',request#>>'{source,qualificationExpiresAt}') from investigation.observation_queries where query_id='e630a865-c1c8-49f4-b3f5-5685ed117df4';
COMMIT;"""
out=run(['docker','exec','-i','ops-v12-native-r24-pg','psql','-U','postgres','-d','ui_v12_r24_native','-At','-v','ON_ERROR_STOP=1'],input=sql)
rows=[json.loads(line) for line in out.splitlines() if line.startswith('{')]
policies=[row for row in rows if 'content' in row]
resources=[row for row in rows if 'canonicalId' in row]
(work/'readonly-resources.json').write_text(json.dumps({'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'transaction':'READ ONLY','resources':resources,'savedLogResult':[row for row in rows if 'queryId' in row]},indent=2))
receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'namespace':ns,'namespaceUID':namespace['metadata']['uid'],'modelProfile':profile,'apiPythonProfileEqual':True,'profileReference':sp06['assistant']['modelProfileVersion'],'policyName':sp06['policyName'],'policies':policies,'admission':json.loads(admission),'protocol':json.loads(protocol),'resourceTroubleshootingEnabled':False,'missing':'No resource_troubleshooting-specific evaluation/admission evidence exists in r28 material. Need actual operational tool/structured-result evaluation and policy decision verification before replacing the frozen API/Worker/Python profile; the existing signed r28 policy already allows scope-bound read-only operator/agent tools; existing audit-only runtime evidence does not establish that capability. No material or profile changed.'}
(work/'model-gate-audit.json').write_text(json.dumps(receipt,ensure_ascii=False,indent=2))
print(json.dumps({'profileTasks':profile['taskKinds'],'policyVersions':len(policies),'persistedResources':len(resources),'namespaceVerified':True}))

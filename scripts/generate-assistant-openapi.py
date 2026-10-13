#!/usr/bin/env python3
"""Keep the public Assistant contracts sourced from the platform OpenAPI."""
import json
from pathlib import Path
root=Path(__file__).resolve().parents[1]
path=root/'api/openapi/platform-v1.yaml'
s=path.read_text()
start='# assistant-generated-components-start\n';end='# assistant-generated-components-end\n'
if start in s:s=s[:s.index(start)]+s[s.index(end)+len(end):]
startpaths='# assistant-generated-paths-start\n';endpaths='# assistant-generated-paths-end\n'
if startpaths in s:s=s[:s.index(startpaths)]+s[s.index(endpaths)+len(endpaths):]
def ref(name):return {'$ref':'#/components/schemas/'+name}
def arr(schema):return {'type':'array','items':schema}
def obj(properties,required=None):return {'type':'object','additionalProperties':False,'properties':properties,'required':list(properties) if required is None else required}
text={'type':'string'};uid={'type':'string','format':'uuid'};timestamp={'type':'string','format':'date-time'}
components={}
for filename,name in [('assistant-answer-v1','AssistantAnswerV1'),('query-result-v1','AssistantQueryResultV1'),('query-fact-v1','AssistantQueryFactV1')]:
 v=json.loads((root/'api/schemas'/f'{filename}.schema.json').read_text());v.pop('$id',None);v.pop('$schema',None)
 def normalize(v):
  if isinstance(v,dict):
   if '$ref' in v and v['$ref'].startswith('https://ops.local/schemas/common/v1#/$defs/'):
    key=v['$ref'].split('/')[-1];return timestamp if key=='timestamp' else {'type':'string','pattern':'^sha256:[a-f0-9]{64}$'} if key=='sha256' else text
   return {k:normalize(x) for k,x in v.items()}
  if isinstance(v,list):return [normalize(x) for x in v]
  return v
 components[name]=normalize(v)
components['AssistantCreateConversationV1']=obj({'title':{'type':'string','minLength':1,'maxLength':160},'mode':{'type':'string','enum':['data_query','troubleshoot']},'authorizationDomain':{'type':'string','enum':['operational','audit']},'clusterUid':text,'namespaceSelection':arr(text),'includeClusterScoped':{'type':'boolean'},'incidentId':uid,'resourceCanonicalIds':arr(text),'symptom':text,'timezone':text,'timeRange':obj({'from':timestamp,'to':timestamp,'timezone':text})},['title','mode','authorizationDomain','timezone'])
components['AssistantResourceSelectorV1']=obj({'canonicalId':text,'name':text,'kind':text,'namespace':text},[])
components['AssistantInterpretV1']=obj({'symptom':{'type':'string','minLength':1,'maxLength':4000}})
components['AssistantIntentEnvelopeV1']=obj({'requestId':text,'data':obj({'taskKind':text,'selector':ref('AssistantResourceSelectorV1'),'status':text,'missingFields':arr(text),'requiresConfirmation':{'type':'boolean'}})})
components['AssistantResolveResourceV1']=obj({'clusterUid':text,'namespaceSelection':arr(text),'includeClusterScoped':{'type':'boolean'},'selector':ref('AssistantResourceSelectorV1')},['clusterUid','selector'])
components['AssistantResourceCandidateV1']=obj({'canonicalId':text,'kind':text,'name':text,'namespace':text,'observedAt':timestamp})
components['AssistantResolutionEnvelopeV1']=obj({'requestId':text,'data':obj({'status':{'type':'string','enum':['resolved','clarification']},'candidates':arr(ref('AssistantResourceCandidateV1')),'missingFields':arr(text),'truncated':{'type':'boolean'},'observationRevision':obj({'ownerEpoch':{'type':'integer'},'graphGeneration':{'type':'integer'}}),'snapshotReadAt':timestamp,'freshness':text})})
components['AssistantConversationV1']=obj({'tenantId':uid,'conversationId':uid,'ownerSubject':text,'title':text,'mode':text,'authorizationDomain':text,'clusterUid':text,'binding':{'type':'object','additionalProperties':True},'activeTurn':ref('AssistantTurnV1'),'revision':{'type':'integer','format':'int64'},'archivedAt':timestamp,'createdAt':timestamp,'updatedAt':timestamp},['tenantId','conversationId','ownerSubject','title','mode','authorizationDomain','binding','revision','createdAt','updatedAt'])
components['AssistantMessageV1']=obj({'messageId':uid,'turnId':uid,'sequence':{'type':'integer','format':'int64'},'role':{'type':'string','enum':['user','assistant']},'content':{'oneOf':[obj({'text':text}),ref('AssistantAnswerV1')]},'createdAt':timestamp})
components['AssistantTurnV1']=obj({'turnId':uid,'conversationId':uid,'jobId':uid,'parentTurnId':uid,'state':{'type':'string','enum':['queued','running','awaiting_user','completed','partial','failed','cancelled','expired']},'errorCode':text,'answer':ref('AssistantAnswerV1'),'createdAt':timestamp,'conversationRevision':{'type':'integer','format':'int64'}},['turnId','conversationId','jobId','state','createdAt','conversationRevision'])
components['AssistantRebindV1']=obj({'expectedConversationRevision':{'type':'integer','format':'int64','minimum':1},'namespaceSelection':arr(text),'includeClusterScoped':{'type':'boolean'},'resourceCanonicalId':text,'timeRange':obj({'from':timestamp,'to':timestamp,'timezone':text})},['expectedConversationRevision'])
components['AssistantSubmitMessageV1']=obj({'message':{'type':'string','minLength':1,'maxLength':4000},'expectedConversationRevision':{'type':'integer','format':'int64','minimum':1},'parentTurnId':uid,'clarificationId':uid,'referentCanonicalId':text},['message','expectedConversationRevision'])
components['AssistantStoredQueryResultV1']=obj({'queryResultId':uid,'result':ref('AssistantQueryResultV1'),'facts':arr(ref('AssistantQueryFactV1')),'contentDigest':text})
components['AssistantEventV1']=obj({'eventId':{'type':'integer','format':'int64'},'turnId':uid,'type':text,'payload':{'type':'object','additionalProperties':True},'occurredAt':timestamp})
for name,data in [('AssistantConversationEnvelopeV1',ref('AssistantConversationV1')),('AssistantConversationListV1',arr(ref('AssistantConversationV1'))),('AssistantMessagesEnvelopeV1',arr(ref('AssistantMessageV1'))),('AssistantTurnEnvelopeV1',ref('AssistantTurnV1')),('AssistantQueryEnvelopeV1',ref('AssistantStoredQueryResultV1'))]:
 components[name]=obj({'data':data,'requestId':text,'meta':obj({'nextCursor':text})},['data','requestId'])
components['AssistantCapabilitiesEnvelopeV1']=obj({'data':obj({'enabled':{'type':'boolean'},'catalog':{'type':'object','additionalProperties':True},'virtualization':text,'runtimeAcceptance':text,'taskKinds':arr(text),'sourceCapabilityStatus':text,'sourceCapabilities':arr({'type':'object','additionalProperties':True})}),'requestId':text})
components['AssistantMutationEnvelopeV1']=obj({'data':{'type':'object','additionalProperties':{'type':'boolean'}},'requestId':text})
components['AssistantHoldV1']=obj({'turnId':uid,'held':{'type':'boolean'},'reason':{'type':'string','minLength':1,'maxLength':1024}})
components['AssistantFormalEnvelopeV1']=obj({'data':obj({'reference':{'type':'object','additionalProperties':True},'metadata':{'type':'object','additionalProperties':True}}),'requestId':text})
components['AssistantEmptyBodyV1']=obj({})
blob=start+''.join('    '+name+': '+json.dumps(schema,ensure_ascii=False)+'\n' for name,schema in components.items())+end
s=s.replace('  schemas:\n','  schemas:\n'+blob,1)
paths={}
def add(path,method,op,envelope,body=None,status='200',params=None,idempotent=True):
 item={'operationId':op,'tags':['Assistant'],'summary':op,'responses':{status:{'description':'Authorized owner response; never cached.','content':{'application/json':{'schema':ref(envelope)}}},'default':{'description':'Unified error','content':{'application/json':{'schema':ref('ErrorEnvelope')}}}}}
 parameters=list(params or [])
 for arg in ['conversationId','turnId','queryResultId']:
  if '{'+arg+'}' in path:parameters.append({'in':'path','name':arg,'required':True,'schema':uid})
 if body:
  item['requestBody']={'required':True,'content':{'application/json':{'schema':ref(body)}}}
  if method=='post' and idempotent:parameters.append({'in':'header','name':'Idempotency-Key','required':True,'schema':{'type':'string','minLength':1,'maxLength':128}});item['x-idempotency']='required'
 if body and method=='post' and not idempotent:item['x-idempotency']='non-idempotent'
 if parameters:item['parameters']=parameters
 paths.setdefault(path,{})[method]=item
base='/api/v1/assistant'
add(base+'/legal-holds','post','setAssistantLegalHold','AssistantMutationEnvelopeV1','AssistantHoldV1',idempotent=False)
add(base+'/resources:interpret','post','interpretAssistantSymptom','AssistantIntentEnvelopeV1','AssistantInterpretV1',idempotent=False)
add(base+'/resources:resolve','post','resolveAssistantResource','AssistantResolutionEnvelopeV1','AssistantResolveResourceV1',idempotent=False)
add(base+'/capabilities','get','getAssistantCapabilities','AssistantCapabilitiesEnvelopeV1',params=[{'in':'query','name':'clusterUid','schema':text}])
add(base+'/conversations','post','createAssistantConversation','AssistantConversationEnvelopeV1','AssistantCreateConversationV1','201')
add(base+'/conversations','get','listAssistantConversations','AssistantConversationListV1',params=[{'in':'query','name':'limit','schema':{'type':'integer','minimum':1,'maximum':50}},{'in':'query','name':'cursor','schema':text}])
add(base+'/conversations/{conversationId}','get','getAssistantConversation','AssistantConversationEnvelopeV1')
add(base+'/conversations/{conversationId}/binding','post','rebindAssistantConversation','AssistantConversationEnvelopeV1','AssistantRebindV1','200')
add(base+'/conversations/{conversationId}/messages','get','listAssistantMessages','AssistantMessagesEnvelopeV1',params=[{'in':'query','name':'cursor','schema':text},{'in':'query','name':'limit','schema':{'type':'integer','minimum':1,'maximum':100}}])
add(base+'/conversations/{conversationId}/messages','post','submitAssistantMessage','AssistantTurnEnvelopeV1','AssistantSubmitMessageV1','202')
add(base+'/turns/{turnId}/formal-references','get','getAssistantFormalReference','AssistantFormalEnvelopeV1',params=[{'in':'query','name':'index','required':True,'schema':{'type':'integer','minimum':0,'maximum':10}}])
add(base+'/turns/{turnId}','get','getAssistantTurn','AssistantTurnEnvelopeV1')
add(base+'/turns/{turnId}:cancel','post','cancelAssistantTurn','AssistantMutationEnvelopeV1','AssistantEmptyBodyV1')
add(base+'/conversations/{conversationId}:archive','post','archiveAssistantConversation','AssistantMutationEnvelopeV1','AssistantEmptyBodyV1')
add(base+'/query-results/{queryResultId}','get','getAssistantQueryResult','AssistantQueryEnvelopeV1')
add(base+'/conversations/{conversationId}/events','get','getAssistantConversationEvents','AssistantMessagesEnvelopeV1',params=[{'in':'header','name':'Last-Event-ID','schema':text}])
paths[base+'/conversations/{conversationId}/events']['get']['responses']['200']['content']={'text/event-stream':{'schema':ref('AssistantEventV1')}}
paths[base+'/conversations/{conversationId}/events']['get']['x-sse-event-envelope']='#/components/schemas/AssistantEventV1'
blob=startpaths+''.join('  '+name+': '+json.dumps(item,ensure_ascii=False)+'\n' for name,item in paths.items())+endpaths
s=s.replace('paths:\n','paths:\n'+blob,1);path.write_text(s)

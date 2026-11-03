"""Real provider protocol checks using synthetic diagnostic data, not business tools."""
from pathlib import Path
import datetime,json,ssl,urllib.request,urllib.error
root=Path('/data/ops-v12-native-visual-r24-20261026')
key=(root/'private/deepseek-api-key').read_text().strip()
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs):return None
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect(),urllib.request.HTTPSHandler(context=ssl.create_default_context()))
result={'date':datetime.datetime.now(datetime.timezone.utc).isoformat(),'model':'deepseek-flash','endpoint':'https://api.deepseek.com/v1/chat/completions','scope':'synthetic diagnostic tool/JSON protocol; not a business tool, admission or complete Worker acceptance','calls':[]}
def request(body):
 req=urllib.request.Request(result['endpoint'],data=json.dumps(dict(model=result['model'],max_tokens=1024,thinking={'type':'disabled'},stream=False,**body)).encode(),headers={'Authorization':'Bearer '+key,'Content-Type':'application/json'})
 with opener.open(req,timeout=60) as resp:data=json.loads(resp.read(1024*1024))
 result['calls'].append({'responseId':data.get('id'),'model':data.get('model'),'finishReason':data['choices'][0].get('finish_reason'),'usage':{k:v for k,v in data.get('usage',{}).items() if isinstance(v,(int,float))}})
 return data['choices'][0]['message']
try:
 messages=[{'role':'user','content':'Call diagnostic_probe with value="probe", then return JSON {"ok": true} if the tool reports success.'}]
 message=request({'messages':messages,'tools':[{'type':'function','function':{'name':'diagnostic_probe','description':'Synthetic protocol diagnostic only','parameters':{'type':'object','properties':{'value':{'type':'string'}},'required':['value'],'additionalProperties':False}}}],'tool_choice':{'type':'function','function':{'name':'diagnostic_probe'}}})
 calls=message.get('tool_calls',[]);assert len(calls)==1
 tool=calls[0];assert tool['function']['name']=='diagnostic_probe' and json.loads(tool['function']['arguments'])=={'value':'probe'}
 result['toolCallValidated']=True
 messages+=[message,{'role':'tool','tool_call_id':tool['id'],'content':json.dumps({'ok':True,'scope':'synthetic_protocol_probe'})}]
 final=request({'messages':messages,'response_format':{'type':'json_object'}})
 assert json.loads(final.get('content',''))=={'ok':True}
 result.update(status='passed',toolResultRoundTrip=True,structuredJSON=True)
except urllib.error.HTTPError as error:
 try:data=json.loads(error.read(65536))
 except Exception:data={}
 result.update(status='failed',httpStatus=error.code,errorType=data.get('error',{}).get('type'),errorCode=data.get('error',{}).get('code'))
except Exception as error:result.update(status='failed',errorType=type(error).__name__)
with (root/'evidence/deepseek-protocol-r27.json').open('x') as out:json.dump(result,out,indent=2)
print(json.dumps(result))

"""Remote-only official DeepSeek OpenAI probe, no business content or secrets exported."""
from pathlib import Path
import datetime,json,ssl,urllib.request,urllib.error
root=Path('/data/ops-v12-native-visual-r24-20261026')
p=root/'private/deepseek-api-key';assert p.stat().st_mode & 0o777==0o600
key=p.read_text().strip()
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs):return None
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect(),urllib.request.HTTPSHandler(context=ssl.create_default_context()))
receipt={'date':datetime.datetime.now(datetime.timezone.utc).isoformat(),'baseURL':'https://api.deepseek.com/v1','scope':'models list and minimal chat only; no business data or model admission'}
def call(path,body=None):
 req=urllib.request.Request(receipt['baseURL']+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+key,'Content-Type':'application/json'})
 with opener.open(req,timeout=60) as resp:return json.loads(resp.read(1024*1024)),resp.headers.get('x-request-id')
try:
 models,_=call('/models');ids=[m['id'] for m in models.get('data',[])];receipt['availableModels']=ids
 selected=next((m for m in ['deepseek-flash','deepseek-v4-flash','deepseek-chat'] if m in ids),None)
 if not selected:raise ValueError('no documented flash/chat model in authorized inventory')
 receipt['requestedModel']=selected
 data,request_id=call('/chat/completions',{'model':selected,'messages':[{'role':'user','content':'Reply exactly OK.'}],'max_tokens':1024,'thinking':{'type':'disabled'},'stream':False})
 choice=data.get('choices',[{}])[0];text=choice.get('message',{}).get('content') or ''
 receipt.update(httpStatus=200,requestId=request_id,responseId=data.get('id'),returnedModel=data.get('model'),finishReason=choice.get('finish_reason'),usage={k:v for k,v in data.get('usage',{}).items() if isinstance(v,(int,float))},textPresent=bool(text.strip()),replyMatchesOK=text.strip()=='OK',status='passed' if text.strip()=='OK' else 'response_requires_review')
except urllib.error.HTTPError as error:
 try:payload=json.loads(error.read(65536))
 except Exception:payload={}
 receipt.update(status='failed',httpStatus=error.code,errorType=payload.get('error',{}).get('type'),errorCode=payload.get('error',{}).get('code'),requestId=error.headers.get('x-request-id'))
except Exception as error:receipt.update(status='failed',errorType=type(error).__name__)
with (root/'evidence/deepseek-connectivity-r27.json').open('x') as out:json.dump(receipt,out,indent=2)
print(json.dumps(receipt))

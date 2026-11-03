"""Minimal remote connectivity probe; never records credentials or thinking."""
from pathlib import Path
import datetime,json,ssl,urllib.request,urllib.error
root=Path('/data/ops-v12-native-visual-r24-20261026')
private=root/'private/minimax-m31-api-key'
assert private.stat().st_mode & 0o777 == 0o600
key=private.read_text().strip()
endpoint='https://api.minimax.cn/anthropic/v1/messages'
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,*args,**kwargs):return None
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect(),urllib.request.HTTPSHandler(context=ssl.create_default_context()))
payload={'model':'MiniMax-M3.1-Flash','max_tokens':1024,'output_config':{'effort':'low'},'messages':[{'role':'user','content':'Reply exactly OK.'}]}
receipt={'date':datetime.datetime.now(datetime.timezone.utc).isoformat(),'endpoint':endpoint,'model':payload['model'],'scope':'minimal direct connectivity only; no business data; not model admission or Worker acceptance'}
try:
    request=urllib.request.Request(endpoint,data=json.dumps(payload).encode(),headers={'Authorization':'Bearer '+key,'Content-Type':'application/json','anthropic-version':'2023-06-01'})
    with opener.open(request,timeout=60) as response:
        raw=response.read(1024*1024);data=json.loads(raw)
        text=''.join(b.get('text','') for b in data.get('content',[]) if b.get('type')=='text')
        receipt.update(httpStatus=response.status,requestId=response.headers.get('x-request-id') or data.get('request_id'),returnedModel=data.get('model'),stopReason=data.get('stop_reason'),usage={k:v for k,v in data.get('usage',{}).items() if isinstance(v,(int,float))},textPresent=bool(text.strip()),replyMatchesOK=text.strip()=='OK',status='passed' if text.strip()=='OK' else 'response_requires_review')
except urllib.error.HTTPError as error:
    raw=error.read(65536)
    try:data=json.loads(raw)
    except Exception:data={}
    receipt.update(status='failed',httpStatus=error.code,errorType=data.get('error',{}).get('type'),requestId=data.get('request_id') or error.headers.get('x-request-id'))
except Exception as error:
    receipt.update(status='failed',errorType=type(error).__name__)
output=root/'evidence/minimax-connectivity-r26.json'
with output.open('x') as stream:json.dump(receipt,stream,indent=2)
print(json.dumps(receipt))

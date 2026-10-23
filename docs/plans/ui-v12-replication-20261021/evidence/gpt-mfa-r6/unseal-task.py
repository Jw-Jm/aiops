# Dedicated development server only; reads its own protected initialization file.
from pathlib import Path
import json,ssl,urllib.request,time
root=Path('/data/ops-ui-v12-mfa-20261022-r1/gpt-r6-openbao')
context=ssl.create_default_context(cafile=str(root/'tls/ca.crt'))
def request(path,data=None):
 req=urllib.request.Request('https://127.0.0.1:18206/v1/'+path,data=json.dumps(data).encode() if data else None,headers={'Content-Type':'application/json'})
 with urllib.request.urlopen(req,context=context,timeout=10) as response:return json.loads(response.read())
for attempt in range(10):
 try:seal=request('sys/seal-status');break
 except OSError:
  if attempt==9:raise RuntimeError('task server did not start')
  time.sleep(1)
if seal['sealed']:
 init=json.loads((root/'private/initialization.json').read_text());request('sys/unseal',{'key':init['keys'][0]})
for attempt in range(10):
 if request('sys/leader').get('is_self'):break
 if attempt==9:raise RuntimeError('task Raft leader unavailable')
 time.sleep(1)
print('task server unsealed and leader active')

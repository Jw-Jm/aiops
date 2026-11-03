"""Reversible outage of only this task's private log source; always resume it."""
from pathlib import Path
import os,subprocess,socket,sys,json,datetime
assert socket.gethostname()=='mssc'
mode=sys.argv[1];assert mode in ['failure','cancel']
work=Path('/data/ops-v12-native-visual-r24-20261026/r37');name='ops-v12-r37-victorialogs'
state=json.loads(subprocess.check_output(['docker','inspect',name]))[0]
assert state['State']['Running'] and not state['State']['Paused']
subprocess.run(['docker','pause',name],check=True,stdout=subprocess.DEVNULL)
try:
 result=subprocess.run(['/opt/ops-assistant-browser-20261014/node',str(work/'live-query.mjs')],env=os.environ|{'MODE':mode},timeout=50,check=False)
finally:
 subprocess.run(['docker','unpause',name],check=True,stdout=subprocess.DEVNULL)
 (work/('source-'+mode+'-resumed.json')).write_text(json.dumps({'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'source':name,'resumed':True}))
sys.exit(result.returncode)

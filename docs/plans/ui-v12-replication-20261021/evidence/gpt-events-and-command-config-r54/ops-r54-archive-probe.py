from pathlib import Path
import base64,datetime,hashlib,hmac,json,socket,ssl,subprocess,urllib.parse,urllib.request,urllib.error,xml.etree.ElementTree as ET
r=Path('/data/ops-v12-native-visual-r24-20261026');w=r/'r54';ns='ops-native-v12-r24-20261026';tenant='01a177f1-372f-75b7-be90-e8121d1445f5';prefix='tenants/'+hashlib.sha256(('ops-archive/v1|'+tenant).encode()).hexdigest()+'/'
env=json.loads(subprocess.check_output(['kubectl','get','secret','ops-worker-env','-n',ns,'-o','json']))['data'];env={k:base64.b64decode(v).decode()for k,v in env.items()};endpoint=env['S3_ENDPOINT'];bucket=env['S3_BUCKET'];host=urllib.parse.urlsplit(endpoint).hostname
ip=subprocess.check_output(['kubectl','get','svc',host.split('.')[0],'-n',ns,'-o','jsonpath={.spec.clusterIP}']).decode();lookup=socket.getaddrinfo;socket.getaddrinfo=lambda n,*a,**kw:lookup(ip if n==host else n,*a,**kw)
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT);ctx.load_verify_locations(cafile=str(r/'private/archive-ca.pem'));assert ctx.verify_mode==ssl.CERT_REQUIRED and ctx.check_hostname;secret=json.loads(subprocess.check_output(['kubectl','get','secret','r24-worker-archive','-n',ns,'-o','json']))['data'];credential=json.loads(base64.b64decode(secret['credentials.json']));roles=credential['tenants'][0]['roles'];out=[]
def request(role,path,params):
 c=roles[role];url=urllib.parse.urlsplit(endpoint);now=datetime.datetime.now(datetime.timezone.utc);at=now.strftime('%Y%m%dT%H%M%SZ');date=now.strftime('%Y%m%d');scope=date+'/us-east-1/s3/aws4_request';digest=hashlib.sha256(b'').hexdigest();query=urllib.parse.urlencode(sorted(params.items()),quote_via=urllib.parse.quote,safe='')
 signed='host;x-amz-content-sha256;x-amz-date';headers='host:'+url.netloc+'\nx-amz-content-sha256:'+digest+'\nx-amz-date:'+at+'\n';canonical='GET\n'+path+'\n'+query+'\n'+headers+'\n'+signed+'\n'+digest;string='AWS4-HMAC-SHA256\n'+at+'\n'+scope+'\n'+hashlib.sha256(canonical.encode()).hexdigest();key=('AWS4'+c['secretKey']).encode()
 for part in [date,'us-east-1','s3','aws4_request']:key=hmac.new(key,part.encode(),hashlib.sha256).digest()
 signature=hmac.new(key,string.encode(),hashlib.sha256).hexdigest();req=urllib.request.Request(endpoint+path+('?' +query if query else ''),headers={'X-Amz-Date':at,'X-Amz-Content-Sha256':digest,'Authorization':'AWS4-HMAC-SHA256 Credential='+c['accessKey']+'/'+scope+', SignedHeaders='+signed+', Signature='+signature})
 try:
  with urllib.request.urlopen(req,context=ctx,timeout=5)as reply:return {'status':reply.status}
 except urllib.error.HTTPError as e:
  raw=e.read(8192)
  try:code=ET.fromstring(raw).findtext('Code')
  except Exception:code='nonXML'
  return {'status':e.code,'code':code}
 except Exception as e:return {'error':type(e).__name__,'reason':str(e)}
foreign='tenants/'+hashlib.sha256(('ops-archive/iam-probe|'+prefix).encode()).hexdigest()+'/'
for role in roles:
 checks={}
 for label,p in [('own',prefix),('unscoped',''),('foreign',foreign)]:checks[label]=request(role,'/'+bucket,{'list-type':'2','max-keys':'1','prefix':p})
 checks['foreignGet']=request(role,'/'+bucket+'/'+foreign+'iam-scope-probe',{})
 out.append({'role':role,'checks':checks})
(w/'archive-probe.json').write_text(json.dumps(out,indent=2));print(json.dumps(out))

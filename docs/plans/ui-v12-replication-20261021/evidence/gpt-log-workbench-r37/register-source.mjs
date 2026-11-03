// Real native administrative writes, with a proof bound to the exact method/path/body.
import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {createHmac} from 'node:crypto';
import {safeErrorMessage} from './safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r37',origin='https://192.168.0.63:30471';
const admin=JSON.parse(readFileSync(root+'/private/browser-account.json','utf8')),factor=JSON.parse(readFileSync(root+'/private/browser-mfa.json','utf8'));
const target=JSON.parse(readFileSync(root+'/private/r31-operator-account.json','utf8'));
const mode='source'; const snapshot=JSON.parse(readFileSync(work+'/source-snapshot.json','utf8'));
let bits='';for(const c of factor.secret.replace(/=+$/,''))bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5,'0');const bytes=[];for(let i=0;i+8<=bits.length;i+=8)bytes.push(parseInt(bits.slice(i,i+8),2));
const result={mode,origin,requests:[]};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true});const page=await context.newPage();
 page.on('response',r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/'))result.requests.push({path,method:r.request().method(),status:r.status(),requestId:r.headers()['x-request-id']})});
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(admin.loginName);await page.getByLabel('密码',{exact:true}).fill(admin.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 // One fresh factor step per administrative operation avoids replaying a TOTP.
 await new Promise(r=>setTimeout(r,30000-Date.now()%30000+150));
 const counter=Buffer.alloc(8);counter.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));const mac=createHmac('sha1',Buffer.from(bytes)).update(counter).digest();const code=String((mac.readUInt32BE(mac[mac.length-1]&15)&0x7fffffff)%1000000).padStart(6,'0');
 result.outcome=await page.evaluate(async({mode,targetId,password,code,prior,snapshot})=>{
  const canonical=value=>Array.isArray(value)?value.map(canonical):value&&typeof value==='object'?Object.fromEntries(Object.keys(value).sort().map(k=>[k,canonical(value[k])])):value;
  const request=async(method,path,body,headers={})=>{const r=await fetch(path,{method,credentials:'same-origin',headers:{...(body?{'Content-Type':'application/json'}:{}),...headers},body:body?JSON.stringify(body):undefined});return {status:r.status,requestId:r.headers.get('x-request-id'),body:await r.json()}};
  const session=await request('GET','/api/v1/auth/session');if(session.status!==200)throw Error('admin session unavailable');const csrf={'X-CSRF-Token':session.body.data.csrf.token,'Idempotency-Key':crypto.randomUUID()};
  let method='POST',path='/api/v1/admin/role-bindings',operation='user_administration',body;
  if(mode==='source'){
   path='/api/v1/admin/source-registrations';method='POST';operation='source_administration';
   body={sourceType:'victorialogs',instanceKey:'r37-native-pod-log-snapshot',clusterId:snapshot.cluster.clusterId,authRef:snapshot.authRef,allowedSchemas:['finding-envelope/v2'],backendLogicalId:'r37-native-pod-log-snapshot',dataScopeMapping:{scopes:{cluster:[snapshot.cluster.clusterUid],namespace:[snapshot.namespace]},requiredLabels:{tenant:snapshot.tenantId}}};
  }else{
   const users=await request('GET','/api/v1/admin/users?limit=100');if(users.status!==200)throw Error('users unavailable');const user=users.body.data.find(u=>u.userId===targetId);if(!user)throw Error('task operator missing');const version=user.revocation.authorizationVersion;
   body={userId:targetId,role:'operator',scope:{clusterUids:['79014400-9453-4cb4-9678-0fae47981d79'],namespaces:mode==='narrow'?['ops-native-v12-r24-20261026']:['ops-native-v12-r24-20261026','ops-native-v12-r35-scope']},expectedAuthorizationVersion:version,reason:'r35 task-only second namespace grant'};
   if(mode==='revoke'){method='PATCH';path+='/'+prior.bindingId+'/status';body={userId:targetId,expectedRevision:version,status:'disabled',reason:'r35 task-only second namespace revocation'}}
  }
  const sum=await crypto.subtle.digest('SHA-256',new TextEncoder().encode(JSON.stringify(canonical(body))));const bodyDigest='sha256:'+Array.from(new Uint8Array(sum),x=>x.toString(16).padStart(2,'0')).join('');
  const challenge=await request('POST','/api/v1/auth/step-up-challenges',{operation,target:{kind:'administrative_write',write:{method,path,bodyDigest}},expectedSessionVersion:session.body.data.revocation.sessionVersion},csrf);
  if(challenge.status!==201)throw Error('challenge rejected '+challenge.status);let revision=challenge.body.data.revision;const challengeId=challenge.body.data.challengeId;
  for(const [method,credential] of [['password',password],['totp',code]]){const verified=await request('POST','/api/v1/auth/step-up-challenges/'+challengeId+':verify',{method,credential,expectedChallengeRevision:revision},csrf);if(verified.status!==200)throw Error('factor rejected '+verified.status);revision=verified.body.data.revision;if(method==='totp'&&verified.body.data.verified!==true)throw Error('factor not verified')}
  const changed=await request(method,path,body,{...csrf,'X-Ops-Step-Up-Challenge':challengeId});if(changed.status!==201)throw Error('write rejected '+changed.status+' '+changed.body.code);
  const data=changed.body.data;return {requestId:changed.requestId,status:changed.status,sourceId:data.sourceId,revision:data.revision,dataScopeMapping:data.dataScopeMapping,bindingId:data.bindingId,userId:data.userId};
 },{mode,targetId:target.userId,password:admin.password,code,prior:null,snapshot});result.status='passed';
}catch(error){result.status='failed';result.failure=safeErrorMessage(error,[admin.password,factor.secret,...factor.recovery]);process.exitCode=1}
finally{writeFileSync(work+'/source-registration.json',JSON.stringify(result,null,2));await browser.close();console.log(JSON.stringify({status:result.status,mode,outcome:result.outcome,failure:result.failure}))}

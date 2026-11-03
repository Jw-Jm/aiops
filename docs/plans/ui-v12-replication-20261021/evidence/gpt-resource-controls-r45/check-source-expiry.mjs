import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {safeErrorMessage} from '../r37/safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r45',origin='https://192.168.0.63:30471';
const account=JSON.parse(readFileSync(root+'/private/r31-operator-account.json','utf8')),node=JSON.parse(readFileSync(root+'/r43/browser-topology.json','utf8')).root;
const expiry='2026-10-29T10:45:00.720238+08:00',source='01a1805f-2214-7373-82a4-c20276c68d43';
const receipt={at:new Date().toISOString(),snapshotSource:source,originalVerifiedUntil:expiry,sourceQueries:0,qualificationMutations:0,scope:'qualification wall-clock expiry only; not natural saved-result TTL expiry'};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 if(Date.now()<=Date.parse(expiry))throw Error('source qualification has not expired yet');
 const context=await browser.newContext({ignoreHTTPSErrors:true}),page=await context.newPage();
 page.on('request',r=>{if(r.method()==='POST'&&new URL(r.url()).pathname==='/api/v1/observability/queries')receipt.sourceQueries++});
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 const response=await context.request.get(origin+'/api/v1/observability/capabilities?'+new URLSearchParams({clusterUid:node.scope,canonicalId:node.canonicalId}));const body=await response.json();receipt.httpStatus=response.status();receipt.requestId=body.requestId;receipt.sourceCount=body.data?.sources?.length;receipt.availableSources=body.data?.sources?.map(item=>({registrationId:item.source.registrationId,verifiedUntil:item.verifiedUntil}));
 if(response.status()!==200)throw Error('capability projection failed '+response.status());
 receipt.expiredSnapshotOffered=body.data.sources.some(item=>item.source.registrationId===source);
 if(receipt.expiredSnapshotOffered)throw Error('expired snapshot source still offered');receipt.status='passed_expired_snapshot_not_offered';
}catch(error){receipt.status='failed';receipt.failure=safeErrorMessage(error,[account.password]);process.exitCode=1}
finally{await browser.close();writeFileSync(work+'/source-expiry.json',JSON.stringify(receipt,null,2));console.log(JSON.stringify(receipt))}

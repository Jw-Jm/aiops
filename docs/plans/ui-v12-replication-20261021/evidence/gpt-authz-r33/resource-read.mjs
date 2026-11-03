// Run only on mssc-direct. Credentials stay in its private directory.
import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync} from 'node:fs';
const root='/data/ops-v12-native-visual-r24-20261026';
const account=JSON.parse(readFileSync(root+'/private/r31-operator-account.json','utf8'));
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try {
 const context=await browser.newContext({ignoreHTTPSErrors:true}); const page=await context.newPage();
 const origin='https://192.168.0.63:30471';
 await page.goto(origin+'/login'); await page.getByLabel('用户名',{exact:true}).fill(account.loginName); await page.getByLabel('密码',{exact:true}).fill(account.password); await page.getByRole('button',{name:/^登\s*录/}).click(); await page.waitForURL(u=>u.pathname!='/login');
 const response=await context.request.get(origin+'/api/v1/resources?clusterUid=79014400-9453-4cb4-9678-0fae47981d79&name=r33-rotation-proof&kind=ConfigMap');
 const body=await response.json(); const rows=Array.isArray(body.data)?body.data:[];
 console.log(JSON.stringify({at:new Date().toISOString(),status:response.status(),requestId:response.headers()['x-request-id'],rows:rows.map(r=>({canonicalId:r.canonicalId,name:r.name,namespace:r.namespace,labels:r.labels,updatedAt:r.updatedAt,sourceRefs:r.sourceRefs})),code:body.code}));
 if(response.status()!==200 || rows.length!==1 || rows[0].name!=='r33-rotation-proof' || rows[0].labels?.['ops.platform.io/revision']!==process.env.EXPECTED_REVISION)process.exitCode=1;
} finally {await browser.close();}

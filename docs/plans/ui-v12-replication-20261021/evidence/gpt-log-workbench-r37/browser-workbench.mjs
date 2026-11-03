// Remote-only real UI actions; no intercepted business responses or secret captures.
import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {safeShot,inspectPage} from '../r36/safe-capture.mjs';
import {safeErrorMessage} from './safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r37',origin='https://192.168.0.63:30471';
const account=JSON.parse(readFileSync(root+'/private/r31-operator-account.json','utf8'));
const mode=process.env.UI_FAULT||'normal';if(!['normal','failure','cancel'].includes(mode))throw Error('unsupported mode');
const receipt={mode,at:new Date().toISOString(),origin,viewport:{width:1440,height:960},checks:[],captures:[],requests:[]};
mkdirSync(work+'/shots',{recursive:true});
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:receipt.viewport,locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();
 page.on('response',r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/'))receipt.requests.push({path,status:r.status(),method:r.request().method(),requestId:r.headers()['x-request-id']})});
 async function shot(name){const probes=await inspectPage(page);if(probes.length){receipt.captures.push({name,captured:false,probes});return}receipt.captures.push({name,captured:true,...await safeShot(page,work+'/shots/'+name+'.png')})}
 async function select(label,text){await page.getByRole('combobox',{name:label,exact:true}).click();await page.locator('.ant-select-item-option-content').filter({hasText:text}).click()}
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 await page.goto(origin+'/observe/logs');await page.getByRole('heading',{name:'日志查询',exact:true}).waitFor();await page.getByText('请选择资源与来源后查询',{exact:true}).waitFor();await shot('logs-idle');
 await select('资源对象','ops-seaweedfs-0');await select('数据来源','r37-native-pod-log-snapshot');
 if(mode!=='normal'){
  await page.getByRole('button',{name:'查询',exact:true}).click();await page.getByRole('heading',{name:'正在读取选定范围',exact:true}).waitFor();receipt.checks.push('real pending state visible');await shot('logs-'+mode+'-loading');
  if(mode==='cancel'){
   await page.getByRole('button',{name:'取消查询',exact:true}).click();await page.getByRole('heading',{name:'本次查询已终止',exact:true}).waitFor({timeout:20000});await page.getByText('平台已停止接收结果；来源端停止尚未确认。',{exact:true}).waitFor();receipt.checks.push('running cancel waits for persisted termination without claiming source stopped');
  }else{
   await page.getByRole('heading',{name:'查询未成功',exact:true}).waitFor({timeout:20000});await page.getByRole('button',{name:'重新查询',exact:true}).waitFor();receipt.checks.push('real source outage displays failure and retry');
  }
  await shot('logs-'+mode+'-terminal');
 }else{
 receipt.windowInputs=await page.locator('input[type=\"datetime-local\"]').evaluateAll(xs=>xs.map(x=>x.value));const creation=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/observability/queries'&&r.request().method()==='POST');await page.getByRole('button',{name:'查询',exact:true}).click();const admitted=await creation;if(admitted.status()!==202){const e=await admitted.json();receipt.admissionError=e;throw Error('admission '+admitted.status())}await page.getByText(/来源总体 186 条 · 本次脱敏样例 20 条/).waitFor({timeout:20000});receipt.checks.push('real population 186 / bounded samples 20');await shot('logs-success');
 const creations=()=>receipt.requests.filter(r=>r.method==='POST'&&r.path==='/api/v1/observability/queries').length;
 const before=creations();await page.getByRole('textbox',{name:'关键词（脱敏样例内检索）'}).fill('r37-no-matching-sample');await page.getByRole('textbox',{name:'关键词（脱敏样例内检索）'}).press('Enter');await page.waitForTimeout(300);
 if(creations()!==before)throw Error('local keyword Enter submitted new source query');await page.getByText(/来源总体 186 条 · 本次脱敏样例 20 条/).waitFor();receipt.checks.push('local filter preserves population and Enter does not resubmit');
 await page.getByRole('textbox',{name:'关键词（脱敏样例内检索）'}).fill('');await page.getByRole('button',{name:'原文',exact:true}).first().click();await page.getByRole('dialog').waitFor();receipt.checks.push('sanitized sample modal opens');await shot('logs-sample');await page.getByRole('dialog').getByRole('button',{name:'关闭',exact:true}).click();
 await page.getByRole('button',{name:'查看来源状态',exact:true}).click();await page.getByRole('dialog').getByText(/采集完整覆盖未证实/).waitFor();receipt.checks.push('source revision qualification and incomplete coverage shown');await shot('logs-source');await page.getByRole('dialog').getByRole('button',{name:'关闭',exact:true}).click();
 const listing=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/observability/queries'&&r.request().method()==='GET');await page.getByRole('button',{name:/查询历史/}).click();const listed=await (await listing).json();receipt.historyLength=Array.isArray(listed.data)?listed.data.length:null;await page.getByRole('dialog').getByRole('button',{name:/查\s*看/}).first().waitFor();await page.getByRole('dialog').getByRole('button',{name:/查\s*看/}).first().click();await page.getByText(/来源总体 186 条 · 本次脱敏样例 20 条/).waitFor();receipt.checks.push('persisted personal history restores result');
 await select('日志级别','DEBUG');await page.getByRole('button',{name:'查询',exact:true}).click();await page.getByText(/所选范围没有日志/).waitFor({timeout:20000});receipt.checks.push('measured zero renders empty state');await shot('logs-empty');
 }
 receipt.status='passed';
}catch(error){receipt.status='failed';receipt.failure=safeErrorMessage(error,[account.password]);process.exitCode=1}
finally{await browser.close();writeFileSync(work+'/browser-workbench'+(mode==='normal'?'':'-'+mode)+'.json',JSON.stringify(receipt,null,2));console.log(JSON.stringify({status:receipt.status,checks:receipt.checks,captures:receipt.captures.map(({name,captured,probes})=>({name,captured,probes})),failure:receipt.failure}))}

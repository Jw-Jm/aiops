import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {safeErrorMessage} from '../r37/safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r42',origin='https://192.168.0.63:30471';
const account=JSON.parse(readFileSync(root+'/private/r31-operator-account.json','utf8'));
const reference=JSON.parse(readFileSync(root+'/r41/browser-export.json','utf8')).result;
const receipt={at:new Date().toISOString(),queryId:reference.queryId,sourceQueries:0,messageSubmits:0,logReads:0};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try {
 const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();
 page.on('request',r=>{const p=new URL(r.url()).pathname;if(p==='/api/v1/observability/queries'&&r.method()==='POST')receipt.sourceQueries++;if(p.startsWith('/api/v1/assistant/')&&p.endsWith('/messages')&&r.method()==='POST')receipt.messageSubmits++;if(p==='/api/v1/observability/queries/'+reference.queryId+'/results')receipt.logReads++});
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(u=>u.pathname!='/login');
 const navigation=await page.goto(origin+'/observe/logs');receipt.indexSHA256=createHash('sha256').update(await navigation.body()).digest('hex');
 await page.getByRole('button',{name:/查询历史/}).click();await page.locator('tr[data-row-key="'+reference.queryId+'"]').getByRole('button',{name:/查\s*看/}).click();
 const link=page.getByRole('link',{name:'按此资源与时间询问 AI',exact:true});await link.waitFor();receipt.link=await link.getAttribute('href');await link.click();
 await page.getByText('按日志资源与时间创建排查会话',{exact:true}).waitFor();
 for(const label of ['会话模式','授权域','授权集群','命名空间子范围'])if(!await page.getByRole(label==='命名空间子范围'?'textbox':'combobox',{name:label,exact:true}).isDisabled())throw Error('linked scope editable: '+label);
 await page.getByText(reference.window.from+' – '+reference.window.to+' · '+reference.window.timezone,{exact:true}).waitFor();
 receipt.readsBeforeCreate=receipt.logReads;
 await page.getByPlaceholder('描述现象',{exact:true}).fill('r42 核对日志所选资源与绝对时间范围，不提交模型任务。');
 const createButton=page.getByRole('button',{name:'创建会话',exact:true});
 receipt.createEnabled=await createButton.isEnabled();
 if(!receipt.createEnabled){
  await page.getByText('当前模型 Profile 未开放此会话模式，请选择已开放模式。',{exact:true}).waitFor();
  const cap=await (await context.request.get(origin+'/api/v1/assistant/capabilities?clusterUid='+reference.resource.clusterUid)).json();receipt.capability={enabled:cap.data?.enabled,taskKinds:cap.data?.taskKinds};
  receipt.creation='blocked_by_current_model_profile';
 } else {
 const [response]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/assistant/conversations'&&r.request().method()==='POST'),createButton.click({timeout:8000})]);receipt.createStatus=response.status();receipt.createRequest=JSON.parse(response.request().postData());const out=await response.json();receipt.requestId=out.requestId;
 if(response.status()!==201)throw Error('create failed '+response.status()+' '+out.error?.code);
 receipt.conversationId=out.data.conversationId;receipt.binding=out.data.binding;
 await page.waitForURL(u=>u.searchParams.get('conversation')===receipt.conversationId&&!u.searchParams.has('logQuery'));
 const q=receipt.createRequest;
 if(JSON.stringify(q.timeRange)!==JSON.stringify({from:reference.window.from,to:reference.window.to,timezone:reference.window.timezone}))throw Error('exact time tuple changed');
 if(q.resourceCanonicalIds[0]!==reference.resource.canonicalId||q.namespaceSelection[0]!==reference.resource.namespaceScope.namespace||q.includeClusterScoped!==false||q.clusterUid!==reference.resource.clusterUid)throw Error('resource scope changed');
 if(receipt.logReads!==receipt.readsBeforeCreate+1||receipt.sourceQueries||receipt.messageSubmits)throw Error('unexpected runtime request count');
 }
 receipt.negativeReferences=[];
 for(const [queryId,status,title] of [['7bc32caf-31b3-43d1-a901-fffc1af9df27',410,'查询结果已过期'],['00000000-0000-4000-8000-000000000042',404,'引用已失效或资源已删除']]){
  const read=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/observability/queries/'+queryId+'/results');
  await page.goto(origin+'/assistant?'+new URLSearchParams({clusterUid:reference.resource.clusterUid,logQuery:queryId}));const r=await read;
  if(r.status()!==status)throw Error('negative reference expected '+status+' received '+r.status());
  await page.getByText(title,{exact:true}).waitFor();
  if(await page.getByRole('button',{name:'创建会话',exact:true}).isEnabled())throw Error('rejected reference enabled create');
  if(await page.getByText('按日志资源与时间创建排查会话',{exact:true}).count())throw Error('rejected reference exposed cached scope');
  receipt.negativeReferences.push({queryId,status,requestId:(await r.json()).requestId});
 }
 receipt.status='passed_scoped_handoff_and_rejection';
}catch(error){receipt.status='failed';receipt.failure=safeErrorMessage(error,[account.password]);process.exitCode=1}
finally{await browser.close();writeFileSync(work+'/browser-handoff.json',JSON.stringify(receipt,null,2));console.log(JSON.stringify({status:receipt.status,createStatus:receipt.createStatus,conversationId:receipt.conversationId,logReads:receipt.logReads,sourceQueries:receipt.sourceQueries,messageSubmits:receipt.messageSubmits,failure:receipt.failure}))}

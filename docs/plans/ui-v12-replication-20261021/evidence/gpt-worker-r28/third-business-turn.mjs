import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync,existsSync} from 'node:fs';
import {randomUUID} from 'node:crypto';
import assert from 'node:assert/strict';
const root='/data/ops-v12-native-visual-r24-20261026/r28-worker',origin='https://192.168.0.63:30471';
const session=JSON.parse(readFileSync(root+'/private/actor-session.json','utf8'));
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const context=await browser.newContext({ignoreHTTPSErrors:true,storageState:root+'/private/actor-storage.json'});
const result={origin,scope:'actual native administrator -> independent production Worker -> OpenBao signing/mTLS -> Holmes -> DeepSeek -> actual audit tools -> persisted answer',requests:[],states:[]};
async function api(method,path,body){const response=await context.request.fetch(origin+path,{method,data:body,headers:{Origin:origin,'X-Tenant-ID':session.tenantId,'X-CSRF-Token':session.csrf.token,'Idempotency-Key':randomUUID()}});const out=await response.json();result.requests.push({method,path,status:response.status(),requestId:out.requestId});assert(response.ok(),`${method} ${path} HTTP${response.status()} ${out.error?.code??''}`);return out.data;}
try{assert(!existsSync(root+'/third-business-submitted.json'),'already submitted; observe existing task instead');
 const c=await api('POST','/api/v1/assistant/conversations',{title:'r28真实审计Worker验收',mode:'data_query',authorizationDomain:'audit',timezone:'Asia/Shanghai'});
 const turn=await api('POST',`/api/v1/assistant/conversations/${c.conversationId}/messages`,{message:'查询最近一小时审计事件总数，按事件类型统计。必须使用audit.events的实际查询结果，不要估算。',expectedConversationRevision:c.revision});
 result.conversationId=c.conversationId;result.turnId=turn.turnId;result.jobId=turn.jobId;writeFileSync(root+'/third-business-submitted.json',JSON.stringify({conversationId:c.conversationId,turnId:turn.turnId,jobId:turn.jobId},null,2));
 const deadline=Date.now()+240000;let current=turn;
 while(Date.now()<deadline){current=await api('GET',`/api/v1/assistant/turns/${turn.turnId}`);if(result.states.at(-1)!==current.state)result.states.push(current.state);if(!['queued','running'].includes(current.state))break;await new Promise(r=>setTimeout(r,1500));}
 result.finalState=current.state;result.errorCode=current.errorCode;result.answer=current.answer?{answerKind:current.answer.answerKind,summary:current.answer.summary,queryResultRefs:current.answer.queryResultRefs,factRefs:current.answer.factRefs,warnings:current.answer.warnings}:null;
 writeFileSync(root+'/third-business-readback.json',JSON.stringify(current,null,2));
 assert(current.state==='completed','business turn did not complete');assert(current.answer,'no answer published');assert(current.answer.queryResultRefs?.length>0,'no actual query result reference');
 for(const id of current.answer.queryResultRefs){const q=await api('GET',`/api/v1/assistant/query-results/${id}`);result.queryResults??=[];result.queryResults.push({queryResultId:id,...q});}
 result.status='passed';
}catch(error){result.status='failed';result.failure=error.message;process.exitCode=1;}
finally{await browser.close();writeFileSync(root+'/third-business-result.json',JSON.stringify(result,null,2));console.log(JSON.stringify(result));}

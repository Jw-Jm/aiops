import {chromium} from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import {readFileSync,writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {safeErrorMessage} from '../r37/safe-error.mjs';
const root='/data/ops-v12-native-visual-r24-20261026',work=root+'/r45',origin='https://192.168.0.63:30471',cluster='79014400-9453-4cb4-9678-0fae47981d79',namespace='ops-native-v12-r24-20261026';
const account=JSON.parse(readFileSync(root+'/private/r31-operator-account.json','utf8'));
const receipt={at:new Date().toISOString(),resourceReads:[],writes:0,errors:[],screenshots:false};
const browser=await chromium.launch({executablePath:'/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true,acceptDownloads:true,viewport:{width:1440,height:960},locale:'zh-CN',timezoneId:'Asia/Shanghai'}),page=await context.newPage();page.setDefaultTimeout(15000);
 page.on('pageerror',error=>receipt.errors.push(safeErrorMessage(error,[account.password])));
 page.on('response',response=>{const url=new URL(response.url());if(url.pathname==='/api/v1/resources')receipt.resourceReads.push({query:url.searchParams.toString(),status:response.status()})});
 page.on('request',request=>{const path=new URL(request.url()).pathname;if(request.method()!=='GET'&&path.startsWith('/api/v1/')&&!path.includes('/auth/'))receipt.writes++});
 await page.goto(origin+'/login');await page.getByLabel('用户名',{exact:true}).fill(account.loginName);await page.getByLabel('密码',{exact:true}).fill(account.password);await page.getByRole('button',{name:/^登\s*录/}).click();await page.waitForURL(url=>url.pathname!='/login');
 const url='/resources?'+new URLSearchParams({clusterUid:cluster,namespace,kind:'Pod',order:'asc',limit:'5'});
 const listResponse=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/v1/resources');const nav=await page.goto(origin+url);receipt.indexSHA256=createHash('sha256').update(await nav.body()).digest('hex');const initialResponse=await listResponse;receipt.initial=await initialResponse.json();
 if(initialResponse.status()!==200||receipt.initial.data.length<2)throw Error('not enough real authorized resources for comparison');
 const selected=receipt.initial.data.slice(0,2);await page.getByRole('link',{name:selected[0].name,exact:true}).waitFor();
 for(const node of selected)await page.locator('tr[data-row-key="'+node.canonicalId+'"]').getByRole('checkbox').check();
 const compareRead=page.waitForResponse(response=>{const url=new URL(response.url());return url.pathname==='/api/v1/resources'&&url.searchParams.getAll('canonicalIds').length===2});
 await page.getByRole('button',{name:'对比资源',exact:true}).click();const compared=await compareRead;receipt.comparison=await compared.json();if(compared.status()!==200||receipt.comparison.data.length!==2)throw Error('comparison fresh read failed');
 const modal=page.getByRole('dialog',{name:'资源对比',exact:true});await modal.getByText('只对比同次授权读取的静态投影字段，不将不同资源的指标或证据混合计算。',{exact:true}).waitFor();const body=await modal.innerText();for(const node of selected)if(!body.includes(node.stableId)||!receipt.comparison.data.some(current=>current.canonicalId===node.canonicalId))throw Error('comparison UID mismatch');receipt.comparisonSelectedIds=selected.map(node=>node.canonicalId);
 await modal.locator('.ant-modal-close').click();await page.getByRole('button',{name:'取消选择',exact:true}).click();if(await page.getByRole('button',{name:'对比资源',exact:true}).count())throw Error('selection not cleared');
 const visibleIds=receipt.initial.data.map(node=>node.canonicalId);const exportRead=page.waitForResponse(response=>{const url=new URL(response.url());return url.pathname==='/api/v1/resources'&&url.searchParams.getAll('canonicalIds').length===visibleIds.length});
 const [download,response]=await Promise.all([page.waitForEvent('download'),exportRead,page.getByRole('button',{name:'导出当前结果',exact:true}).click()]);receipt.export=await response.json();receipt.exportRequest=new URL(response.url()).searchParams.toString();
 if(response.status()!==200||receipt.export.data.length!==visibleIds.length||receipt.export.data.some(node=>!visibleIds.includes(node.canonicalId)))throw Error('export identity set differs');
 receipt.downloadName=download.suggestedFilename();await download.saveAs(work+'/resources-page.csv');receipt.csvSHA256=createHash('sha256').update(readFileSync(work+'/resources-page.csv')).digest('hex');receipt.exportRows=receipt.export.data.length;receipt.clickedMatchedTotal=receipt.initial.meta.summary.total;
 if(receipt.writes||receipt.errors.length)throw Error('unexpected business writes or page errors');receipt.status='passed_scoped_comparison_and_page_export';
}catch(error){receipt.status='failed';receipt.failure=safeErrorMessage(error,[account.password]);process.exitCode=1}
finally{await browser.close();writeFileSync(work+'/browser-controls.json',JSON.stringify(receipt,null,2));console.log(JSON.stringify({status:receipt.status,comparison:receipt.comparison?.data?.length,exportRows:receipt.exportRows,matchedTotal:receipt.clickedMatchedTotal,writes:receipt.writes,errors:receipt.errors,failure:receipt.failure}))}

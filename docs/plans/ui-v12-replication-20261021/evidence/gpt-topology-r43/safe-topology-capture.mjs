// Only UUIDs explicitly read from authorized graph metadata are excluded
// from the recovery-code probe. Factor pages and other secret probes stay blocked.
import {rm} from 'node:fs/promises';
const uuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export function inspectTopologyDom(dom,publicIds){
 const found=[];
 if(dom.path!=='/resources/topology')found.push('unexpected-page');
 if(/otpauth:\/\/totp\//i.test(dom.text))found.push('otpauth-uri');
 let recoveryText=dom.text;
 for(const id of publicIds)if(uuid.test(id))recoveryText=recoveryText.replaceAll(id,'[public UUID]');
 if(/\b[A-Z0-9]{4}-[A-Z0-9]{4}\b/i.test(recoveryText))found.push('recovery-code');
 for(const field of dom.fields){
  if(field.type==='password')continue;
  if(/^\d{6}$/.test(field.value.trim()))found.push('otp-input');
  if(/^[A-Z2-7]{16,}$/i.test(field.value.trim()))found.push('base32-input');
 }
 return found;
}
const readDom=page=>page.evaluate(()=>({path:location.pathname,text:document.body.innerText,fields:[...document.querySelectorAll('input, textarea')].map(e=>({type:e.getAttribute('type')||'text',value:e.value||''}))}));
export async function safeTopologyShot(page,file,publicIds,options={}){
 await page.mouse.move(1438,4);
 await page.evaluate(async()=>{await Promise.allSettled(document.getAnimations().filter(a=>a.playState==='running'&&Number.isFinite(a.effect.getComputedTiming().endTime)).map(a=>a.finished))});
 const before=inspectTopologyDom(await readDom(page),publicIds);
 if(before.length)throw Error('topology capture refused: '+before.join(','));
 await page.screenshot({path:file,...options});
 const after=inspectTopologyDom(await readDom(page),publicIds);
 if(after.length){await rm(file,{force:true});throw Error('topology capture discarded: '+after.join(','))}
 return{file,at:new Date().toISOString(),probes:[],postProbes:[],publicUuidCount:publicIds.length};
}

import assert from 'node:assert/strict';
import {mkdtemp,writeFile,access,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {inspectTopologyDom,safeTopologyShot} from './safe-topology-capture.mjs';
const publicId='12345678-abcd-4000-8000-123456789012';
const dom={path:'/resources/topology',text:publicId,fields:[]};
assert.deepEqual(inspectTopologyDom(dom,[publicId]),[]);
assert.deepEqual(inspectTopologyDom(dom,[]),['recovery-code']);
assert.deepEqual(inspectTopologyDom({...dom,text:publicId+' ABCD-1234'},[publicId]),['recovery-code']);
assert.deepEqual(inspectTopologyDom({...dom,path:'/account/security'},[publicId]),['unexpected-page']);
assert.deepEqual(inspectTopologyDom({...dom,text:'otpauth://totp/test'},[]),['otpauth-uri']);
assert.deepEqual(inspectTopologyDom({...dom,text:'',fields:[{type:'text',value:'123456'},{type:'text',value:'JBSWY3DPEHPK3PXP'}]},[]),['otp-input','base32-input']);
const dir=await mkdtemp(join(tmpdir(),'ops-r43-capture-'));const file=join(dir,'late.png');
try{
 let reads=0;
 const page={mouse:{move:async()=>{}},evaluate:async fn=>fn.toString().includes('document.getAnimations')?null:(++reads===1?{...dom,text:''}:{...dom,text:'ABCD-1234'}),screenshot:async({path})=>writeFile(path,'fixture')};
 await assert.rejects(safeTopologyShot(page,file,[]),/discarded/);
 await assert.rejects(access(file));
 console.log('7 topology capture checks passed (public metadata only; unknown codes and late secrets refused)');
}finally{await rm(dir,{recursive:true,force:true})}

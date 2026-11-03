import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,rm} from 'node:fs/promises';
import {existsSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {inspectLogDom,safeLogShot} from './safe-log-capture.mjs';
const id='79014400-9453-4cb4-9678-0fae47981d79';
const dom=(text,fields=[])=>({path:'/observe/logs',text,fields});
test('known public UUID is the false positive; unknown UUID is still blocked',()=>{
 assert.deepEqual(inspectLogDom(dom(id),[id]),[]);
 assert.deepEqual(inspectLogDom(dom(id),[]),['recovery-code']);
});
test('isolated recovery code remains blocked beside an approved UUID',()=>{
 assert.deepEqual(inspectLogDom(dom(id+' ABCD-EFGH'),[id]),['recovery-code']);
 assert.deepEqual(inspectLogDom(dom('ABCD-EFGH'),['ABCD-EFGH']),['recovery-code']);
});
test('factor URI and inputs remain blocked even with public IDs',()=>{
 assert.deepEqual(inspectLogDom(dom('otpauth://totp/'+id,[{type:'text',value:'123456'},{type:'text',value:'JBSWY3DPEHPK3PXP'}]),[id]),['otpauth-uri','otp-input','base32-input']);
});
test('factor route cannot use the log allowlist',()=>assert.deepEqual(inspectLogDom({...dom(id),path:'/account/security'},[id]),['unexpected-page']));
test('late secret is discarded after capture',async()=>{
 const dir=await mkdtemp(join(tmpdir(),'log-capture-')),file=join(dir,'late.png');
 const queue=[undefined,dom(id),dom(id+' ABCD-EFGH')];
 const page={mouse:{move:async()=>{}},waitForFunction:async()=>{},evaluate:async()=>queue.shift(),screenshot:async()=>writeFile(file,'synthetic frame')};
 await assert.rejects(()=>safeLogShot(page,file,[id]),/discarded/);assert.equal(existsSync(file),false);await rm(dir,{recursive:true,force:true});
});

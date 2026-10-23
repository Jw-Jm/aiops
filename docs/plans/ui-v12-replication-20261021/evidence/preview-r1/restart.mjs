import { chromium } from './playwright-core/index.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import assert from 'node:assert/strict';
const base = '/data/ops-ui-v12-http-20261022-r1/preview-r1';
const origin = 'https://192.168.0.63:30462';
const browser = await chromium.launch({executablePath: '/opt/ops-assistant-browser-20261014/chrome-linux64/chrome', args:['--no-sandbox'], headless:true});
const result = {status:'FAIL'};
try {
  const context = await browser.newContext({ignoreHTTPSErrors:true});
  const page = await context.newPage();
  await page.goto(origin+'/login');
  await page.getByLabel('用户名', {exact:true}).fill('v12.admin');
  await page.getByLabel('密码', {exact:true}).fill(readFileSync(base+'/private/administrator-current-password','utf8'));
  await page.getByRole('button',{name:/^登\s*录$/}).click();
  await page.waitForURL('**/access/sources');
  const session = () => page.evaluate(async () => (await fetch('/api/v1/auth/session')).json());
  const before = await session();
  assert.equal(before.data.mode,'full');
  execFileSync('systemctl',['restart','ops-ui-v12-preview-r1-api']);
  // A readiness response proves the new serving loop and DB pool initialized.
  let ready = false;
  for (let attempt=0; attempt<20; attempt++) {
    try { ready=(await context.request.get(origin+'/api/v1/auth/session')).status()===200; } catch {}
    if (ready) break;
    await new Promise(resolve=>setTimeout(resolve,250));
  }
  assert(ready);
  await page.reload();
  await page.getByRole('button',{name:/退\s*出/}).waitFor();
  const after = await session();
  assert.equal(after.data.sessionRef,before.data.sessionRef);
  assert.equal(after.data.subject,before.data.subject);
  assert.deepEqual(after.data.revocation,before.data.revocation);
  const done=page.waitForResponse(r=>r.url().endsWith('/api/v1/auth/logout') && r.request().method()==='POST');
  await page.getByRole('button',{name:/退\s*出/}).click();
  assert.equal((await done).status(),200);
  result.status='PASS';
  result.subject=after.data.subject;
  result.check='session survives API process restart and browser reload; logout completes';
} catch(e) { result.failure=e.message; process.exitCode=1; }
finally {
  writeFileSync(base+'/logs/restart-result.json',JSON.stringify(result,null,2));
  await browser.close(); console.log(JSON.stringify(result));
}

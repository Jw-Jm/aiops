import { chromium } from '/opt/ops-current-tools/playwright-core/index.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { createHmac, randomBytes } from 'node:crypto';

const root = '/data/ops-current';
const origin = 'https://192.168.0.63:30473';
const identity = JSON.parse(readFileSync('/data/ops-current/private/browser-account.json', 'utf8'));
const sensitive = [identity.password];
const result = { origin, database: 'ops_current', scope: 'real native MFA UI with isolated native tenant and projected Kubernetes API identity', checks: [], requests: [] };
function code(secret) {
  let bits = '';
  for (const c of secret.replace(/=+$/, '')) bits += 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5, '0');
  const bytes = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2));
  const counter = Buffer.alloc(8); counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const digest = createHmac('sha1', Buffer.from(bytes)).update(counter).digest();
  return String((digest.readUInt32BE(digest[digest.length - 1] & 15) & 0x7fffffff) % 1000000).padStart(6, '0');
}
const browser = await chromium.launch({ executablePath: '/opt/ops-assistant-browser-20261014/chrome-linux64/chrome', headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 960 }, locale: 'zh-CN', timezoneId: 'Asia/Shanghai' });
const page = await context.newPage();
page.on('response', r => { if (new URL(r.url()).pathname.startsWith('/api/')) result.requests.push({ path: new URL(r.url()).pathname, status: r.status(), requestId: r.headers()['x-request-id'] }); });
const assert = (condition, message) => { if (!condition) throw new Error(message); };
async function login() {
  await page.goto(origin + '/login');
  await page.getByLabel('用户名', { exact: true }).fill(identity.loginName);
  await page.getByLabel('密码', { exact: true }).fill(identity.password);
  const response = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/auth/login');
  await page.getByRole('button', { name: /^登\s*录/ }).click();
  assert((await response).status() === 201, 'copied fixture login rejected');
  await page.waitForURL(url => url.pathname !== '/login');
  if (new URL(page.url()).pathname === '/auth/first-password') {
    const next = randomBytes(30).toString('base64url'); sensitive.push(next);
    writeFileSync(root+'/private/password-rotation-pending.json',JSON.stringify({password:next}),{mode:0o600});
    const dialog=page.getByRole('dialog');
    await dialog.getByLabel('当前密码',{exact:true}).fill(identity.password);
    await dialog.getByLabel('密码',{exact:true}).fill(next);
    await dialog.getByLabel('确认密码',{exact:true}).fill(next);
    await dialog.getByRole('button',{name:'保存密码',exact:true}).click();
    await page.waitForURL(u=>u.pathname!='/auth/first-password' && u.pathname!='/login');
    identity.password=next;writeFileSync(root+'/private/browser-account.json',JSON.stringify(identity),{mode:0o600});
  }

}
async function enroll() {
  await page.goto(origin + '/account/security');
  await page.getByRole('button', { name: '启用双因素', exact: true }).click();
  await page.getByLabel('当前密码', { exact: true }).waitFor();
  const uri = await page.locator('code').filter({ hasText: 'otpauth://' }).textContent();
  const secret = new URL(uri.trim()).searchParams.get('secret');
  assert(secret, 'enrollment has no secret');
  sensitive.push(secret, uri.trim());
  await page.getByLabel('当前密码', { exact: true }).fill(identity.password);
  await page.getByRole('button', { name: '验证密码', exact: true }).click();
  await page.getByText('密码已验证，请提交身份验证器中的 6 位动态码。', { exact: true }).waitFor();
  await page.getByLabel('动态码', { exact: true }).fill(code(secret));
  await page.getByRole('button', { name: '验证动态码', exact: true }).click();
  await page.getByLabel('新绑定动态码', { exact: true }).waitFor();
  await page.getByLabel('新绑定动态码', { exact: true }).fill(code(secret));
  await page.getByRole('button', { name: '确认新绑定', exact: true }).click();
  await page.getByText('双因素已启用。恢复码只显示这一次，请离线保存。', { exact: true }).waitFor();
  const recovery = (await page.locator('.ant-tag').allTextContents()).filter(x => /^[A-Z0-9]{4}-[A-Z0-9]{4}$/.test(x));
  assert(recovery.length === 10, 'expected ten one-time recovery codes');
  sensitive.push(...recovery);
  writeFileSync(root + '/private/browser-mfa.json', JSON.stringify({ ...identity, secret, recovery }), { mode: 0o600 });
  return recovery;
}
try {
 await login(); await enroll();
 await page.reload(); await page.getByText('已启用', {exact:true}).waitFor();
 result.checks.push('fresh administrator native login and Transit-backed MFA enrollment persisted');
 writeFileSync(root+'/evidence/admin-enrollment.json',JSON.stringify(result,null,2));
 console.log('Current administrator login and MFA enrollment passed');
} catch(e) {
 let message=e.message;for(const value of sensitive)message=message.replaceAll(value,'[REDACTED]');
 writeFileSync(root+'/evidence/admin-enrollment-failure.json',JSON.stringify({failure:message,requests:result.requests},null,2));process.exitCode=1;
} finally {await browser.close();}

import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { createHmac } from 'node:crypto';

const root = '/data/ops-v12-native-visual-r24-20261026';
const origin = 'https://192.168.0.63:30471';
const identity = JSON.parse(readFileSync('/data/ops-v12-native-visual-r24-20261026/private/member-account.json', 'utf8'));
const sensitive = [identity.password];
const result = { origin, database: 'ui_v12_r24_native', scope: 'real native MFA UI with isolated native tenant and projected Kubernetes API identity', checks: [], requests: [] };
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
}
async function enroll() {
  await page.goto(origin + '/account/security');
  const created=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/mfa/enrollments'&&r.request().method()==='POST');
  await page.getByRole('button', { name: '启用双因素', exact: true }).click();
  result.enrollmentId=(await (await created).json()).data.enrollmentId;
  const pending=await page.evaluate(async id=>{const r=await fetch('/api/v1/auth/mfa/enrollments/'+id);const x=await r.json();return {status:r.status,hasSecret:['otpauthUri','secret','seed','secretCiphertext','recoveryCodes'].some(k=>k in (x.data??{}))};},result.enrollmentId);
  assert(pending.status===200&&!pending.hasSecret,'pending GET redisclosed secret or rejected owner');
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
  writeFileSync(root + '/private/member-mfa.json', JSON.stringify({ ...identity, secret, recovery }), { mode: 0o600 });
  return recovery;
}
try {
  await login();await page.waitForURL('**/account/security');
  await enroll();await page.reload();await page.getByText('已启用',{exact:true}).waitFor();
  const confirmed=await page.evaluate(async id=>{const r=await fetch('/api/v1/auth/mfa/enrollments/'+id);const x=await r.json();return {status:r.status,hasSecret:['otpauthUri','secret','seed','secretCiphertext','recoveryCodes'].some(k=>k in (x.data??{}))};},result.enrollmentId);
  assert(confirmed.status===200&&!confirmed.hasSecret,'confirmed GET redisclosed secret');assert(await page.locator('code').filter({hasText:'otpauth://'}).count()===0,'refresh redisclosed URI');
  const adminIdentity=JSON.parse(readFileSync(root+'/private/browser-account.json','utf8'));sensitive.push(adminIdentity.password);const admin=await browser.newContext({ignoreHTTPSErrors:true});const ap=await admin.newPage();await ap.goto(origin+'/login');await ap.getByLabel('用户名',{exact:true}).fill(adminIdentity.loginName);await ap.getByLabel('密码',{exact:true}).fill(adminIdentity.password);await ap.getByRole('button',{name:/^登\s*录/}).click();await ap.waitForURL(u=>u.pathname!=='/login');const other=await admin.request.get(origin+'/api/v1/auth/mfa/enrollments/'+result.enrollmentId);assert(other.status()===404,'other administrator read another user enrollment');await admin.close();
  const view=await page.evaluate(async()=> (await (await fetch('/api/v1/auth/session')).json()).data);assert(!view.mustReenrollMfa&&view.authorization.grants.length===0,'re-enrollment changed independent roles');result.checks.push('admin-reset target re-enrolls through real Transit and clears forced enrollment','owner pending/confirmed GET200 never rediscloses URI/seed/recovery; cross-user admin GET404','refresh retains MFA and zero role grants');await page.screenshot({path:root+'/evidence/member-reenrolled-after-admin-reset.png'});result.status='passed';
} catch (error) {
  result.status = 'failed'; result.failure = sensitive.reduce((message, value) => message.replaceAll(value, '[REDACTED]'), error.message).replace(/\b\d{6}\b/g, '[CODE]'); process.exitCode = 1;
} finally {
  writeFileSync(root + '/evidence/reenroll-after-admin-reset.json', JSON.stringify(result, null, 2));
  await browser.close();
  console.log(JSON.stringify({ status: result.status, checks: result.checks, failure: result.failure }));
}

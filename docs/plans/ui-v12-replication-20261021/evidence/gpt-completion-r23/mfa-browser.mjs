import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { createHmac } from 'node:crypto';

const root = '/data/ops-v12-completion-20261026-r1';
const origin = 'https://192.168.0.63:30470';
const identity = JSON.parse(readFileSync('/data/ops-ui-v12-mfa-20261022-r1/gpt-dialog-r14/preview/private/browser-created-account.json', 'utf8'));
const sensitive = [identity.password];
const result = { origin, database: 'ui_v12_r23_preview', scope: 'real native MFA UI with existing task OpenBao Transit; database copy only', checks: [], requests: [] };
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
  await login();
  const recovery = await enroll();
  result.checks.push('actual Transit-backed enrollment/password/TOTP/confirmation and ten recovery codes');
  await page.screenshot({ path: root + '/evidence/mfa-confirmed.png', mask: [page.locator('.ant-tag').filter({ hasText: /^[A-Z0-9]{4}-[A-Z0-9]{4}$/ })] });
  await page.reload();
  await page.getByText('已启用', { exact: true }).waitFor();
  assert(await page.locator('code').filter({ hasText: 'otpauth://' }).count() === 0, 'secret appeared after reload');
  result.checks.push('confirmed status persists after reload; secret is not redisclosed');
  await page.getByRole('button', { name: '创建恢复挑战', exact: true }).click();
  await page.getByLabel('恢复码', { exact: true }).fill(recovery[0]);
  await page.getByRole('button', { name: '提交恢复码', exact: true }).click();
  await page.waitForURL('**/login');
  await login();
  await page.waitForURL('**/account/security');
  const denied = await page.evaluate(async () => (await fetch('/api/v1/admin/clusters?limit=100')).status);
  assert(denied === 403, 'recovery session bypassed the re-enrollment gate');
  result.checks.push('recovery revokes sessions; relogin requires enrollment; business API403');
  await enroll();
  const restored = await page.evaluate(async () => (await fetch('/api/v1/admin/clusters?limit=100')).status);
  assert(restored === 200, 're-enrollment did not restore existing independent admin grant');
  result.checks.push('new factor confirmation restores persisted admin access200');
  result.status = 'passed';
} catch (error) {
  result.status = 'failed'; result.failure = sensitive.reduce((message, value) => message.replaceAll(value, '[REDACTED]'), error.message).replace(/\b\d{6}\b/g, '[CODE]'); process.exitCode = 1;
} finally {
  writeFileSync(root + '/evidence/mfa-browser.json', JSON.stringify(result, null, 2));
  await browser.close();
  console.log(JSON.stringify({ status: result.status, checks: result.checks, failure: result.failure }));
}

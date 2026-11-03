import {safeErrorMessage} from './safe-error.mjs';
// r34: create one isolated, clearly marked native account that will be used as
// the target of an explicit historical-subject mapping. It holds no role, so any
// historical read it later receives cannot be confused with operator authority.
import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { createHmac, randomBytes } from 'node:crypto';

const root = '/data/ops-v12-native-cold-r34-20261027';
const run = root + '/evidence';
const origin = 'https://192.168.0.63:30472';
const admin = JSON.parse(readFileSync(root + '/private/browser-account.json', 'utf8'));
const factor = JSON.parse(readFileSync(root + '/private/browser-mfa.json', 'utf8'));
const stamp = Date.now();
const target = { loginName: 'r34.legacy.owner', displayName: 'r34 历史映射测试账号', password: randomBytes(24).toString('base64url') };
writeFileSync(root+'/private/r34-hist-account.json',JSON.stringify(target),{mode:0o600});
const sensitive = [admin.password, factor.secret, ...factor.recovery, target.password];
const result = { origin, requests: [], checks: [], failure: null };

function totp() {
  let bits = '';
  for (const c of factor.secret.replace(/=+$/, '')) bits += 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5, '0');
  const bytes = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2));
  const b = Buffer.alloc(8);
  b.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const d = createHmac('sha1', Buffer.from(bytes)).update(b).digest();
  return String((d.readUInt32BE(d[d.length - 1] & 15) & 0x7fffffff) % 1000000).padStart(6, '0');
}

const browser = await chromium.launch({ executablePath: '/opt/ops-assistant-browser-20261014/chrome-linux64/chrome', headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 960 }, locale: 'zh-CN', timezoneId: 'Asia/Shanghai' });
const page = await context.newPage();
page.setDefaultTimeout(30000);
page.on('response', (r) => {
  const path = new URL(r.url()).pathname;
  if (path.startsWith('/api/')) result.requests.push({ method: r.request().method(), path, status: r.status(), requestId: r.headers()['x-request-id'] });
});

try {
  await page.goto(origin + '/login');
  await page.getByLabel('用户名', { exact: true }).fill(admin.loginName);
  await page.getByLabel('密码', { exact: true }).fill(admin.password);
  await page.getByRole('button', { name: /^登\s*录/ }).click();
  await page.waitForURL((u) => u.pathname !== '/login');
  await page.goto(origin + '/settings/permissions');
  await page.getByText(admin.loginName, { exact: true }).waitFor();
  await page.getByRole('button', { name: /创建用户/ }).click();
  const dialog = page.getByRole('dialog', { name: '创建内置账号' });
  await dialog.waitFor();
  await dialog.getByLabel('用户名', { exact: true }).fill(target.loginName);
  await dialog.getByLabel('姓名', { exact: true }).fill(target.displayName);
  await dialog.getByLabel('密码', { exact: true }).fill(target.password);
  await dialog.getByLabel('确认密码', { exact: true }).fill(target.password);
  await dialog.getByLabel('验证密码', { exact: true }).fill(admin.password);
  await dialog.getByLabel('验证动态码', { exact: true }).fill(totp());
  await dialog.getByRole('button', { name: '验证本次操作', exact: true }).click();
  await page.waitForFunction(() => [...document.querySelectorAll('.ant-modal button')].some((b) => b.textContent.trim() === '创建账号' && !b.disabled));
  const created = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/v1/admin/users' && r.request().method() === 'POST');
  await dialog.getByRole('button', { name: '创建账号', exact: true }).click();
  const reply = await created;
  if (reply.status() !== 201) throw new Error('user creation rejected: ' + reply.status());
  const body = await reply.json();
  target.userId = body.data.userId;
  target.requestId = reply.headers()['x-request-id'];
  await dialog.waitFor({ state: 'hidden' });
  const row = page.getByRole('row').filter({ hasText: target.loginName });
  await row.getByText('尚未授权', { exact: true }).waitFor();
  writeFileSync(root + '/private/r34-hist-account.json', JSON.stringify(target), { mode: 0o600 });
  result.account = { loginName: target.loginName, userId: target.userId };
  result.checks.push('mapping-target account created through the real UI with zero roles (row shows 尚未授权)');
  result.status = 'passed';
  writeFileSync(run + '/create-hist-target.json', JSON.stringify(result, null, 2));
} catch (error) {
  result.status = 'failed';
  result.failure = safeErrorMessage(error,sensitive).replace(/\b\d{6}\b/g, '[CODE]');
  writeFileSync(run + '/create-hist-target.json', JSON.stringify(result, null, 2));
  process.exitCode = 1;
} finally {
  await browser.close();
  console.log(JSON.stringify({ status: result.status, account: result.account, checks: result.checks, failure: result.failure }));
}

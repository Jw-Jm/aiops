// r30: create one independent acceptance identity through the real native
// administrator UI (password + TOTP operation proof). Existing sign-in
// accounts (v12.r24.admin, r24.member...) are never created, reset or edited.
import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { randomBytes, createHmac } from 'node:crypto';

const root = '/data/ops-v12-native-visual-r24-20261026';
const run = root + '/r37';
const origin = 'https://192.168.0.63:30471';
const admin = JSON.parse(readFileSync(root + '/private/browser-account.json', 'utf8'));
const factor = JSON.parse(readFileSync(root + '/private/browser-mfa.json', 'utf8'));
const stamp = Date.now();
const target = {
  loginName: 'v12.fullview',
  displayName: 'V1.2 全页面查看',
  password: randomBytes(24).toString('base64url'),
};
const sensitive = [admin.password, factor.secret, ...factor.recovery, target.password];
const result = { origin, checks: [], requests: [], screenshots: [], failure: null };
const assert = (ok, message) => {
  if (!ok) throw new Error(message);
};
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
const browser = await chromium.launch({
  executablePath: '/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',
  headless: true,
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
});
const context = await browser.newContext({
  ignoreHTTPSErrors: true,
  viewport: { width: 1440, height: 960 },
  locale: 'zh-CN',
  timezoneId: 'Asia/Shanghai',
});
const page = await context.newPage();
page.setDefaultTimeout(20000);
page.on('response', (r) => {
  const path = new URL(r.url()).pathname;
  if (path.startsWith('/api/')) result.requests.push({ method: r.request().method(), path, status: r.status(), requestId: r.headers()['x-request-id'] });
});
let lastStep = -1;
async function proof(dialog) {
  const now = Math.floor(Date.now() / 30000);
  if (now === lastStep) await new Promise((r) => setTimeout(r, 30000 - (Date.now() % 30000) + 200));
  lastStep = Math.floor(Date.now() / 30000);
  await dialog.getByLabel('验证密码', { exact: true }).fill(admin.password);
  await dialog.getByLabel('验证动态码', { exact: true }).fill(totp());
  await dialog.getByRole('button', { name: '验证本次操作', exact: true }).click();
  await page.waitForFunction(() => [...document.querySelectorAll('.ant-modal button')].some((b) => b.textContent.trim() === '创建账号' && !b.disabled));
}
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
  assert(await dialog.getByRole('button', { name: '创建账号', exact: true }).isDisabled(), 'create bypassed the operation proof');
  await proof(dialog);
  const created = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/v1/admin/users' && r.request().method() === 'POST');
  await dialog.getByRole('button', { name: '创建账号', exact: true }).click();
  const reply = await created;
  assert(reply.status() === 201, 'user creation rejected: ' + reply.status());
  const body = await reply.json();
  target.userId = body.data.userId;
  target.requestId = reply.headers()['x-request-id'];
  await dialog.waitFor({ state: 'hidden' });
  const row = page.getByRole('row').filter({ hasText: target.loginName });
  await row.getByText('尚未授权', { exact: true }).waitFor();
  result.checks.push('real create 201 with password+TOTP operation proof; zero implicit role; mustChange forced server-side');
  writeFileSync(root + '/private/fullview-account.json', JSON.stringify(target), { mode: 0o600 });
  result.account = { loginName: target.loginName, userId: target.userId };
  result.status = 'passed';
} catch (error) {
  result.status = 'failed';
  result.failure = sensitive.reduce((s, v) => s.replaceAll(v, '[REDACTED]'), error.message).replace(/\b\d{6}\b/g, '[CODE]');
  process.exitCode = 1;
} finally {
  writeFileSync(run + '/create-fullview.json', JSON.stringify(result, null, 2));
  await browser.close();
  console.log(JSON.stringify({ status: result.status, account: result.account, checks: result.checks, failure: result.failure }));
}

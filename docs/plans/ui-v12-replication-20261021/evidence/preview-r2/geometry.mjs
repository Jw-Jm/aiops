// SL-001 geometry probe. Runs on the authorized remote host with its bundled node.
// It only reports computed geometry/styles; it does not perform product acceptance.
import { chromium } from '/data/ops-ui-v12-http-20261022-r1/preview-r1/playwright-core/index.mjs';
import { writeFileSync, readFileSync, existsSync } from 'node:fs';

const design = '/data/ops-ui-v12-20261021-r1/ops-platform-v1.2-20261020';
const origin = process.env.PRODUCT_ORIGIN || 'https://192.168.0.63:30462';
const out = process.env.OUT_DIR || '/data/ops-ui-v12-http-20261022-r1/preview-r2';
const override = process.env.OVERRIDE_CSS || '';

const PROBE = (selectors) => {
  const pick = (el) => {
    if (!el) return null;
    const r = el.getBoundingClientRect();
    const s = getComputedStyle(el);
    return {
      x: +r.x.toFixed(2), y: +r.y.toFixed(2), w: +r.width.toFixed(2), h: +r.height.toFixed(2),
      font: s.fontSize + '/' + s.lineHeight + ' ' + s.fontWeight,
      color: s.color, bg: s.backgroundColor, border: s.borderColor,
      radius: s.borderRadius, padding: s.padding, margin: s.margin,
    };
  };
  const result = {};
  for (const [name, selector] of Object.entries(selectors)) {
    result[name] = pick(document.querySelector(selector));
  }
  result.__doc = {
    height: document.documentElement.scrollHeight,
    bodyFont: getComputedStyle(document.body).fontFamily,
  };
  return result;
};

const designSelectors = {
  card: '.login-card',
  tag: '.login-card .tag',
  heading: '.login-card h2',
  hint: '.login-card p',
  form: '#login-form',
  label1: 'label[for="f-username"]',
  input1: '#f-username',
  label2: 'label[for="f-password"]',
  input2: '#f-password',
  button: '#login-form button[type="submit"]',
  footer: '.login-card .small',
};
const productSelectors = {
  card: '.v12-login .login-card',
  tag: '.v12-login-tag',
  heading: '#native-login-title',
  hint: '.login-hint',
  form: '.v12-login form.ant-form',
  item1: '.v12-login form .ant-form-item',
  labelWrap1: '.v12-login form .ant-form-item-label',
  control1: '.v12-login form .ant-form-item-control',
  label1: 'label[for="loginName"]',
  input1: '#loginName',
  label2: 'label[for="password"]',
  input2: '.v12-login .ant-input-affix-wrapper',
  inner2: '#password',
  button: '.v12-login button[type="submit"]',
  footer: '.v12-login-footer',
};

const report = { design: {}, product: {}, delta: {}, override: override || null };
const browser = await chromium.launch({
  executablePath: '/opt/ops-assistant-browser-20261014/chrome-linux64/chrome',
  headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'],
});
try {
  const context = await browser.newContext({
    ignoreHTTPSErrors: true, viewport: { width: 1440, height: 960 }, deviceScaleFactor: 1,
    locale: 'zh-CN', timezoneId: 'Asia/Shanghai',
  });

  const designPage = await context.newPage();
  await designPage.goto('file://' + design + '/slices/SL-001/index.html');
  await designPage.evaluate(() => document.fonts.ready);
  report.design = await designPage.evaluate(PROBE, designSelectors);
  await designPage.screenshot({ path: out + '/prototype-login.png' });
  await designPage.close();

  const page = await context.newPage();
  await page.goto(origin + '/login');
  await page.waitForSelector('#native-login-title');
  await page.evaluate(() => document.fonts.ready);
  // antd inputs animate `transition: all`; computed values are only final once
  // transitions are disabled, so the probe never reports a mid-transition size.
  await page.addStyleTag({
    content: '*,*::before,*::after{transition:none !important;animation:none !important}',
  });
  if (override && existsSync(override)) {
    await page.addStyleTag({ content: readFileSync(override, 'utf8') });
  }
  report.product = await page.evaluate(PROBE, productSelectors);
  await page.screenshot({ path: out + '/product-login-empty.png' });

  // Same-fill diagnostic render: value differences are not design differences.
  await page.fill('#loginName', 'lin.wei');
  await page.fill('#password', 'DemoOps#2026');
  // Remove focus so the comparison never measures the focus ring.
  await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
  report.productFilled = await page.evaluate(PROBE, productSelectors);
  await page.screenshot({ path: out + '/product-login-filled.png' });

  for (const key of Object.keys(productSelectors)) {
    const a = report.design[key]; const b = report.productFilled[key];
    if (!a || !b) { report.delta[key] = 'missing'; continue; }
    report.delta[key] = {
      x: +(b.x - a.x).toFixed(2), y: +(b.y - a.y).toFixed(2),
      w: +(b.w - a.w).toFixed(2), h: +(b.h - a.h).toFixed(2),
    };
  }
} finally {
  writeFileSync(out + '/geometry.json', JSON.stringify(report, null, 2));
  await browser.close();
}
console.log(JSON.stringify({ delta: report.delta, design: report.design, product: report.productFilled }, null, 2));

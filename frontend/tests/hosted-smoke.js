import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { chromium, firefox } from 'playwright';
import { verifyDatasets, verifyWorkbench } from './hosted-datasets.js';

// Only the launcher sees the operator credentials. Never log its single-use URL.
const helper = fileURLToPath(new URL('../../deploy/sandbox/hosted_console.py', import.meta.url));
const origin = new URL(process.env.HOSTED_CONSOLE_URL).origin;
if (!/^https:\/\/[a-z0-9-]+(?:\.[a-z0-9-]+)*\.run\.app$/.test(origin)) {
  throw new Error('Set HOSTED_CONSOLE_URL to the deployed Cloud Run console.');
}
const browserName = process.env.MLC_SMOKE_BROWSER || 'chromium';
const engine = new Map([['chromium', chromium], ['firefox', firefox]]).get(browserName);
if (!engine) throw new Error('MLC_SMOKE_BROWSER must be chromium or firefox.');
const output = execFileSync('python3', [helper, '--print-url'], { encoding: 'utf8' }).trim();
const entry = new URL(output.replace(/^Open /, ''));
if (entry.origin !== origin || !entry.hash) throw new Error('Unexpected login destination');
const browser = await engine.launch();
async function checkWrite(response, status) {
  const request = response.request();
  assert.equal(await request.headerValue('origin'), origin, 'Console write must identify its origin');
  const referrer = await request.headerValue('referer');
  assert.ok(referrer === null || referrer === origin + '/', 'Console write exposed more than the origin root');
  assert.equal(response.status(), status);
}
try {
  const page = await browser.newPage();
  page.setDefaultTimeout(20000);
  const api = origin + '/api/v1/admin/connections';
  const denied = await page.request.get(api, { maxRedirects: 0 });
  assert.ok([401, 403].includes(denied.status()), 'Anonymous directory access should be denied');
  const redeem = page.waitForResponse(r => new URL(r.url()).pathname === '/session/redeem');
  try { await page.goto(entry.href); } catch { throw new Error('Hosted sign-in failed'); }
  await checkWrite(await redeem, 204);
  await page.getByRole('heading', { name: 'Pipeline workspace' }).waitFor();
  assert.ok(!new URL(page.url()).hash, 'Redeemed ticket remains in URL');
  const session = (await page.context().cookies(origin)).find(c => c.secure && c.httpOnly && c.sameSite === 'Strict');
  assert.ok(session, 'Missing protected session cookie');
  assert.equal((await page.request.get(api)).status(), 200);
  const crossSite = await page.request.post(origin + '/api/v1/admin/preview', {
    headers: { Origin: 'https://example.invalid', 'X-MLC-Request': 'console' }, data: {}
  });
  assert.equal(crossSite.status(), 403, 'A cross-origin mutation reached the gateway');
  await page.getByRole('region', { name: 'Preview results' }).waitFor();
  for (const [label, value] of [['Connection', 'demo'], ['Database', 'mlc-sandbox'], ['Collection', 'orders']]) {
    await page.getByRole('combobox', { name: label, exact: true }).selectOption(value);
  }
  await page.getByLabel('Aggregation pipeline').fill(JSON.stringify([
    { $sort: { amount: -1 } }, { $limit: 1 }, { $project: { _id: 1, amount: 1 } }
  ]));
  const preview = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/admin/workspace/preview');
  await page.getByRole('button', { name: 'Run preview' }).click();
  const response = await preview;
  await checkWrite(response, 200);
  assert.deepEqual(await response.json(), { fields: [{ name: '_id', type: 'TEXT' }, { name: 'amount', type: 'NUMBER' }], rows: [['order-001', 42]] });
  await page.getByText('order-001', { exact: true }).waitFor();
  if (process.env.MLC_SMOKE_DATASETS === '1') {
    await verifyDatasets(page, origin);
    await verifyWorkbench(page, origin);
  }
  const replay = await browser.newPage();
  try { await replay.goto(entry.href); } catch { throw new Error('Could not verify consumed login ticket'); }
  await replay.getByText(/link expired or has already been used/i).waitFor();
  assert.ok([401, 403].includes((await replay.request.get(api, { maxRedirects: 0 })).status()), 'A consumed ticket started a second session');
  await page.goto(origin);
  const logout = page.waitForResponse(r => new URL(r.url()).pathname === '/session/logout' && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Sign out' }).click();
  await checkWrite(await logout, 303);
  await page.getByRole('heading', { name: 'Sign in to your workspace' }).waitFor();
  assert.ok([401, 403].includes((await page.request.get(api, { maxRedirects: 0 })).status()), 'Signed-out session still has access');
  const revoked = await page.request.get(api, { headers: { Cookie: session.name + '=' + session.value }, maxRedirects: 0 });
  assert.equal(revoked.status(), 401, 'The gateway still accepts the signed-out session');
  console.log('Verified hosted sign-in and sign-out, anonymous denial, cross-origin rejection, single-use ticket and order-001 for 42.');
} finally {
  await browser.close();
}

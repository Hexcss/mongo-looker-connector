import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

// Only the launcher sees the operator credentials. Never log its single-use URL.
const helper = fileURLToPath(new URL('../../deploy/sandbox/hosted_console.py', import.meta.url));
const origin = new URL(process.env.HOSTED_CONSOLE_URL).origin;
if (!/^https:\/\/[a-z0-9-]+(?:\.[a-z0-9-]+)*\.run\.app$/.test(origin)) {
  throw new Error('Set HOSTED_CONSOLE_URL to the deployed Cloud Run console.');
}
const output = execFileSync('python3', [helper, '--print-url'], { encoding: 'utf8' }).trim();
const entry = new URL(output.replace(/^Open /, ''));
if (entry.origin !== origin || !entry.hash) throw new Error('Unexpected login destination');
const browser = await chromium.launch();
try {
  const page = await browser.newPage();
  page.setDefaultTimeout(20000);
  const api = origin + '/api/v1/admin/connections';
  const denied = await page.request.get(api, { maxRedirects: 0 });
  assert.ok([401, 403].includes(denied.status()), 'Anonymous directory access should be denied');
  try { await page.goto(entry.href); } catch { throw new Error('Hosted sign-in failed'); }
  await page.getByRole('heading', { name: 'Connection directory' }).waitFor();
  assert.ok(!new URL(page.url()).hash, 'Redeemed ticket remains in URL');
  const session = (await page.context().cookies(origin)).find(c => c.secure && c.httpOnly && c.sameSite === 'Strict');
  assert.ok(session, 'Missing protected session cookie');
  assert.equal((await page.request.get(api)).status(), 200);
  const crossSite = await page.request.post(origin + '/api/v1/admin/preview', {
    headers: { Origin: 'https://example.invalid', 'X-MLC-Request': 'console' }, data: {}
  });
  assert.equal(crossSite.status(), 403, 'A cross-origin mutation reached the gateway');
  await page.getByRole('link', { name: 'Query preview', exact: true }).click();
  for (const [label, value] of [['Connection', 'demo'], ['Database', 'mlc-sandbox'], ['Collection', 'orders']]) {
    await page.getByRole('combobox', { name: label, exact: true }).selectOption(value);
  }
  await page.getByLabel('Aggregation pipeline').fill(JSON.stringify([
    { $sort: { amount: -1 } }, { $limit: 1 }, { $project: { _id: 1, amount: 1 } }
  ]));
  const preview = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/admin/preview');
  await page.getByRole('button', { name: 'Run preview' }).click();
  const response = await preview;
  assert.equal(response.status(), 200);
  assert.deepEqual(await response.json(), { fields: ['_id', 'amount'], rows: [['order-001', 42]] });
  await page.getByText('order-001', { exact: true }).waitFor();
  const replay = await browser.newPage();
  try { await replay.goto(entry.href); } catch { throw new Error('Could not verify consumed login ticket'); }
  await replay.getByText(/link expired or has already been used/i).waitFor();
  assert.ok([401, 403].includes((await replay.request.get(api, { maxRedirects: 0 })).status()), 'A consumed ticket started a second session');
  await page.goto(origin);
  const logout = page.waitForResponse(r => new URL(r.url()).pathname === '/session/logout' && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Sign out' }).click();
  assert.equal((await logout).status(), 303, 'The server declined sign-out');
  await page.getByRole('heading', { name: 'Sign in with your operator account' }).waitFor();
  assert.ok([401, 403].includes((await page.request.get(api, { maxRedirects: 0 })).status()), 'Signed-out session still has access');
  const revoked = await page.request.get(api, { headers: { Cookie: session.name + '=' + session.value }, maxRedirects: 0 });
  assert.equal(revoked.status(), 401, 'The gateway still accepts the signed-out session');
  console.log('Verified hosted sign-in and sign-out, anonymous denial, cross-origin rejection, single-use ticket and order-001 for 42.');
} finally {
  await browser.close();
}

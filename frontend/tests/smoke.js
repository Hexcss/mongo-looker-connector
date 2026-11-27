import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';

let launcher, browser;
try {
  let address = process.env.MLC_CONSOLE_URL;
  if (!address) {
    if (!process.env.PROJECT_ID || !process.env.OPERATOR_ACCOUNT) throw new Error('Set PROJECT_ID and OPERATOR_ACCOUNT for the private console launcher.');
    launcher = spawn('python3', [fileURLToPath(new URL('../../deploy/sandbox/console.py', import.meta.url))], {
      stdio: ['ignore', 'pipe', 'inherit']
    });
    const lines = createInterface({ input: launcher.stdout });
    for await (const line of lines) {
      if (line.startsWith('Open ')) { address = line.slice(5); break; }
    }
    if (!address) throw new Error('The private console launcher did not start.');
  }
  let url;
  try { url = new URL(address); } catch { throw new Error('Invalid console launcher URL.'); }
  const entry = /^\/open\/[A-Za-z0-9_-]+$/.test(url.pathname);
  const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname) || /^[a-z0-9-]+\.localhost$/.test(url.hostname);
  if (url.protocol !== 'http:' || !loopback || url.username || url.password || (!entry && url.pathname !== '/admin/') || url.search || url.hash) {
    throw new Error('Provide a loopback console launcher URL.');
  }
  browser = await chromium.launch();
  const page = await browser.newPage();
  page.setDefaultTimeout(20000);
  const errors = [];
  page.on('pageerror', () => errors.push('Browser script error'));
  // Exercise only list and preview routes, even if a future UI automatically calls another endpoint.
  await page.route('**/*', route => {
    const request = route.request();
    const target = new URL(request.url());
    const allowed = target.origin === url.origin && (
      request.method() === 'GET' && (entry && target.pathname === url.pathname || target.pathname.startsWith('/admin/') || ['/api/v1/admin/connections', '/api/v1/admin/tokens'].includes(target.pathname)) ||
      request.method() === 'POST' && target.pathname === '/api/v1/admin/preview'
    );
    if (!allowed) errors.push('Unexpected request');
    return allowed ? route.continue() : route.abort();
  });
  try { await page.goto(url.href); } catch { throw new Error('Could not open the loopback console.'); }
  if (entry) {
    const session = await page.context().cookies(url.origin);
    assert.ok(session.some(cookie => cookie.domain === url.hostname && cookie.httpOnly && cookie.sameSite === 'Strict'), 'Missing host-only protected console cookie');
    if (url.hostname.endsWith('.localhost')) {
      const otherHost = await page.context().cookies('http://127.0.0.1:' + url.port + '/');
      assert.equal(otherHost.length, 0, 'The independent loopback host received a cookie');
    }
  }
  await page.getByRole('heading', { name: 'Connection directory' }).waitFor();
  assert.equal(await page.getByRole('alert').count(), 0);
  await page.getByRole('link', { name: 'Access tokens', exact: true }).click();
  await page.getByRole('region', { name: 'Reporting tokens' }).or(page.getByRole('heading', { name: 'No access tokens yet' })).waitFor();
  await page.getByRole('link', { name: 'Query preview', exact: true }).click();
  const { MLC_SMOKE_CONNECTION, MLC_SMOKE_DATABASE, MLC_SMOKE_COLLECTION } = process.env;
  if (MLC_SMOKE_CONNECTION) await page.getByLabel('Connection', { exact: true }).selectOption(MLC_SMOKE_CONNECTION);
  if (MLC_SMOKE_DATABASE) await page.getByLabel('Database', { exact: true }).selectOption(MLC_SMOKE_DATABASE);
  if (MLC_SMOKE_COLLECTION) await page.getByLabel('Collection', { exact: true }).selectOption(MLC_SMOKE_COLLECTION);
  await page.getByLabel('Aggregation pipeline').fill('[{"$limit":1}]');
  const response = page.waitForResponse(res => new URL(res.url()).pathname === '/api/v1/admin/preview');
  await page.getByRole('button', { name: 'Run preview' }).click();
  assert.equal((await response).status(), 200, 'Read-only preview failed');
  await page.getByRole('region', { name: 'Preview results' }).or(page.getByRole('heading', { name: 'No rows returned' })).waitFor();
  assert.deepEqual(errors, []);
  console.log('Browser verified connections, token inventory and a bounded read-only preview.');
} finally {
  launcher?.kill();
  await browser?.close();
}

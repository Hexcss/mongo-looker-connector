import assert from 'node:assert/strict';
import { after, before, test } from 'node:test';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { chromium } from 'playwright';

let server, browser, origin;
before(async () => {
  server = createServer(async (request, response) => {
    const name = { '/admin/': 'index.html', '/admin/console.js': 'console.js',
      '/admin/console.css': 'console.css', '/admin/icons.svg': 'icons.svg' }[request.url];
    try {
      const asset = await readFile(new URL('../../internal/console/static/' + name, import.meta.url));
      const type = { html: 'text/html', js: 'text/javascript', css: 'text/css', svg: 'image/svg+xml' }[name.split('.').pop()];
      response.writeHead(200, { 'Content-Type': type, 'Content-Security-Policy': "default-src 'self'; script-src 'self'; style-src 'self'" }).end(asset);
    } catch { response.writeHead(404).end(); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  origin = 'http://127.0.0.1:' + server.address().port;
  browser = await chromium.launch();
});
after(async () => {
  await browser?.close();
  if (server?.listening) await new Promise(resolve => server.close(resolve));
});

async function fixture(t, viewport) {
  const page = await browser.newPage({ viewport });
  t.after(() => page.close());
  page.setDefaultTimeout(4500);
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  t.after(() => assert.deepEqual(errors, []));
  const connections = [{ id: 'warehouse', managed: false, revision: '', databases: { reporting: ['orders', 'regions'] } }];
  const tokens = [];
  const requests = [];
  await page.route('**/api/v1/admin/**', async route => {
    const request = route.request();
    const [resource, id] = new URL(request.url()).pathname.split('/').slice(4);
    const method = request.method();
    const body = request.postDataJSON();
    const result = data => route.fulfill({ json: data });
    if (method !== 'GET') {
      assert.equal(request.headers()['content-type'], 'application/json');
      assert.equal(request.headers()['x-mlc-request'], 'console');
      requests.push({ resource, id, method, body, raw: request.postData() });
    }
    if (resource === 'connections') {
      if (method === 'GET') return result({ connections });
      if (method === 'POST') connections.push({ id: body.id, databases: body.databases, managed: true, revision: 'r1' });
      if (method === 'PUT') Object.assign(connections.find(c => c.id === id), { databases: body.databases, revision: 'r2' });
      if (method === 'DELETE') connections.splice(connections.findIndex(c => c.id === id), 1);
      return result({});
    }
    if (resource === 'preview') return result({ fields: ['item', 'amount'], rows: [['Lathe', 42]] });
    if (resource === 'tokens') {
      if (method === 'GET') return result({ tokens });
      if (method === 'POST') {
        const token = { id: 'fixture-token', label: body.label, grants: body.grants, createdAt: '2026-05-02T10:00:00Z', revoked: false };
        tokens.push(token);
        return result({ token, secret: 'mlc_browser_test_value' });
      }
      tokens.find(token => token.id === id).revoked = true;
      return result({});
    }
    return route.abort();
  });
  await page.goto(origin + '/admin/');
  await page.getByRole('button', { name: 'warehouse', exact: true }).waitFor();
  return { page, requests };
}

test('create, rotate, update and delete a multi-database connection with revision checks', async t => {
  const { page, requests } = await fixture(t);
  assert.equal(await page.getByRole('button', { name: 'Edit connection' }).count(), 0);
  await page.getByRole('button', { name: 'Add connection' }).click();
  let dialog = page.getByRole('dialog');
  await dialog.getByLabel('Connection ID').fill('sales');
  await dialog.getByLabel('Connection URI').fill('mongodb+srv://fixture.invalid');
  await dialog.getByLabel('Database name').fill('crm');
  await dialog.getByLabel('Collections').fill('accounts\nleads');
  await dialog.getByRole('button', { name: 'Add database' }).click();
  await dialog.getByLabel('Database name').nth(1).fill('reports');
  await dialog.getByLabel('Collections').nth(1).fill('daily');
  await dialog.getByRole('button', { name: 'Save connection' }).click();
  await page.getByRole('button', { name: 'sales', exact: true }).click();
  assert.deepEqual(requests.at(-1).body, { id: 'sales', uri: 'mongodb+srv://fixture.invalid', databases: { crm: ['accounts', 'leads'], reports: ['daily'] } });
  await page.getByRole('button', { name: 'Edit connection' }).click();
  assert.equal(await dialog.getByLabel('Connection URI').inputValue(), '');
  await dialog.getByLabel('Collections').first().fill('accounts');
  await dialog.getByRole('button', { name: 'Save connection' }).click();
  await dialog.waitFor({ state: 'hidden' });
  assert.deepEqual(requests.at(-1).body, { revision: 'r1', databases: { crm: ['accounts'], reports: ['daily'] } });
  await page.getByRole('button', { name: 'Edit connection' }).click();
  await dialog.getByLabel('Connection URI').fill('mongodb+srv://rotated.invalid');
  await dialog.getByRole('button', { name: 'Save connection' }).click();
  await dialog.waitFor({ state: 'hidden' });
  assert.equal(requests.at(-1).body.uri, 'mongodb+srv://rotated.invalid');
  await page.getByRole('button', { name: 'Delete connection' }).click();
  await dialog.getByRole('button', { name: 'Delete connection' }).click();
  await page.getByRole('button', { name: 'sales', exact: true }).waitFor({ state: 'hidden' });
  assert.deepEqual(requests.at(-1).body, { revision: 'r2' });
});

test('preview preserves exact input and field order and handles invalid JSON', async t => {
  const { page, requests } = await fixture(t);
  await page.getByRole('link', { name: 'Query preview', exact: true }).click();
  await page.getByLabel('Aggregation pipeline').fill('[{');
  await page.getByRole('button', { name: 'Run preview' }).click();
  await page.getByRole('alert').waitFor();
  assert.equal(requests.length, 0);
  await page.getByLabel('Aggregation pipeline').fill('[{"$match":{"id":9007199254740993}}]');
  await page.getByLabel('Output fields').fill('item\namount');
  await page.getByRole('button', { name: 'Run preview' }).click();
  await page.getByRole('cell', { name: 'Lathe' }).waitFor();
  assert.deepEqual(requests[0].body.fields, ['item', 'amount']);
  assert.equal(requests[0].body.database, 'reporting');
  assert.ok(requests[0].raw.includes('9007199254740993'));
  await page.getByLabel('Output fields').fill('');
  await page.getByRole('button', { name: 'Run preview' }).click();
  await page.getByRole('cell', { name: 'Lathe' }).waitFor();
  assert.ok(!('fields' in requests[1].body));
});

test('issue and revoke a narrowly scoped token at mobile width', async t => {
  const { page, requests } = await fixture(t, { width: 390, height: 844 });
  await page.getByRole('link', { name: 'Access tokens' }).click();
  await page.getByRole('button', { name: 'Issue token' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Token label').fill('Finance');
  await dialog.getByLabel('orders', { exact: true }).check();
  await dialog.getByLabel('regions', { exact: true }).check();
  await dialog.getByRole('button', { name: 'Issue token' }).click();
  await dialog.getByLabel('Token secret').waitFor();
  assert.equal(await dialog.getByLabel('Token secret').inputValue(), 'mlc_browser_test_value');
  assert.deepEqual(requests[0].body.grants, [
    { connectionId: 'warehouse', database: 'reporting', collection: 'orders' },
    { connectionId: 'warehouse', database: 'reporting', collection: 'regions' }
  ]);
  await dialog.getByRole('button', { name: 'Done' }).click();
  assert.equal(await page.getByLabel('Token secret').count(), 0);
  await page.getByRole('button', { name: 'Revoke Finance' }).click();
  await dialog.getByRole('button', { name: 'Revoke token' }).click();
  await page.getByText('Revoked', { exact: true }).waitFor();
  assert.equal(requests.at(-1).method, 'DELETE');
  assert.equal(requests.at(-1).id, 'fixture-token');
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
});

test('a failed catalog fetch can be retried and a new empty installation stays usable', async t => {
  const { page } = await fixture(t);
  let fails = true;
  await page.route('**/api/v1/admin/connections', route => fails
    ? route.fulfill({ status: 503, json: { error: { message: 'Catalog unavailable.' } } })
    : route.fulfill({ json: { connections: [] } }));
  await page.getByRole('button', { name: 'Refresh' }).click();
  await page.getByRole('alert').waitFor();
  fails = false;
  await page.getByRole('button', { name: 'Refresh' }).click();
  await page.getByRole('heading', { name: 'No connections yet' }).waitFor();
  await page.getByRole('button', { name: 'Add connection' }).click();
  await page.keyboard.press('Escape');
  await page.getByRole('dialog').waitFor({ state: 'hidden' });
  await page.getByRole('link', { name: 'Query preview', exact: true }).click();
  await page.getByRole('link', { name: 'Open Connections' }).waitFor();
  await page.getByRole('link', { name: 'Access tokens' }).click();
  assert.ok(await page.getByRole('button', { name: 'Issue token' }).isDisabled());
});

test('an open editor retains the revision it actually displayed during a background refresh', async t => {
  const { page, requests } = await fixture(t);
  const record = { id: 'warehouse', managed: true, revision: 'old', databases: { reporting: ['orders'] } };
  let reply;
  await page.route('**/api/v1/admin/connections', async route => {
    if (record.revision === 'new' && !reply) await new Promise(resolve => { reply = resolve; });
    return route.fulfill({ json: { connections: [record] } });
  });
  await page.getByRole('button', { name: 'Refresh' }).click();
  await page.getByRole('button', { name: 'Edit connection' }).waitFor();
  record.revision = 'new';
  const response = page.waitForResponse(res => new URL(res.url()).pathname === '/api/v1/admin/connections');
  await page.getByRole('button', { name: 'Refresh' }).click();
  await page.getByRole('button', { name: 'Edit connection' }).click();
  await page.getByLabel('Connection ID').waitFor();
  reply();
  await response;
  await page.getByRole('button', { name: 'Save connection' }).click();
  await page.getByRole('dialog').waitFor({ state: 'hidden' });
  assert.equal(requests.at(-1).body.revision, 'old');
});

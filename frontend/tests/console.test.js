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

async function fixture(t, viewport, { role = 'owner', rotate = false, start = 'connections', users = [] } = {}) {
  const page = await browser.newPage({ viewport });
  t.after(() => page.close());
  page.setDefaultTimeout(4500);
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  t.after(() => assert.deepEqual(errors, []));
  const connections = [{ id: 'warehouse', managed: false, revision: '', databases: { reporting: ['orders', 'regions'] } }];
  const tokens = [];
  const datasets = [];
  const requests = [];
  const reads = [];
  const fields = [{ name: 'order_id', type: 'TEXT' }, { name: 'region', type: 'TEXT' }, { name: 'amount', type: 'NUMBER' }];
  const sample = { fields, rows: [['SO-1048', 'West', 127], ['SO-1052', 'South', 86], ['SO-1045', 'North', 42]] };
  const launch = id => ({ datasetId: id, url: 'https://lookerstudio.google.com/datasources/create?connectorId=example&connectorConfig=' + encodeURIComponent(JSON.stringify({ collection: id })) });
  await page.route('**/api/v1/admin/**', async route => {
    const request = route.request();
    const [resource, id, action] = new URL(request.url()).pathname.split('/').slice(4);
    const method = request.method();
    const body = request.postDataJSON();
    const result = data => route.fulfill({ json: data });
    if (method !== 'GET') {
      assert.equal(request.headers()['content-type'], 'application/json');
      assert.equal(request.headers()['x-mlc-request'], 'console');
      requests.push({ resource, id, method, body, raw: request.postData() });
    }
    if (method === 'GET') reads.push({ resource, id, action });
    if (resource === 'me') return result({ id: 'self', email: 'analyst@example.invalid', role, operator: role === 'owner' && !rotate, mustChangePassword: rotate });
    if (resource === 'workspace') {
      if (id === 'preview') return result(sample);
      if (id === 'looker') return result(launch(action));
      if (datasets.some(d => d.id === body.id)) return route.fulfill({ status: 409, json: { error: { message: 'Dataset already exists.' } } });
      datasets.push({ ...body, fields, pipelineText: JSON.stringify(body.pipeline), revision: 'w1' });
      return result(launch(body.id));
    }
    if (resource === 'password') return route.fulfill({ status: 204 });
    if (resource === 'users' || resource === 'bootstrap-owner') {
      if (method === 'GET') return result({ users });
      const user = id ? users.find(u => u.id === id) : { id: String(users.length), email: body.email, role: body.role || 'owner', primary: resource === 'bootstrap-owner', mustChangePassword: true };
      if (!id) users.push(user);
      if (method === 'PUT') { Object.assign(user, body); return result({ user }); }
      return result({ user, temporaryPassword: 'fixture-temporary-password' });
    }
    if (resource === 'connections') {
      if (method === 'GET') return result({ connections });
      if (method === 'POST') connections.push({ id: body.id, databases: body.databases, managed: true, revision: 'r1' });
      if (method === 'PUT') Object.assign(connections.find(c => c.id === id), { databases: body.databases, revision: 'r2' });
      if (method === 'DELETE') connections.splice(connections.findIndex(c => c.id === id), 1);
      return result({});
    }
    if (resource === 'datasets') {
      if (method === 'GET') return result({ datasets });
      if (method === 'POST') datasets.push({ ...body, pipelineText: body.pipeline.length ? '[{"$match":{"serial":9007199254740993}}]' : '[]', revision: 'ds1' });
      if (method === 'PUT') Object.assign(datasets.find(d => d.id === id), body, { revision: 'ds2' });
      if (method === 'DELETE') datasets.splice(datasets.findIndex(d => d.id === id), 1);
      return result({});
    }
    if (resource === 'preview') return body.connectionId === '@datasets'
      ? result({ fields: ['_id', 'amount'], rows: [['order-001', 42]] })
      : result({ fields: ['item', 'amount'], rows: [['Lathe', 42]] });
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
  await page.goto(origin + '/admin/#' + start);
  await page.getByRole('main').getByRole('heading', { level: 1 }).waitFor();
  return { page, requests, reads };
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
  await page.goto(origin + '/admin/#preview');
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

test('an access denial outside the JSON API tells the operator how to recover', async t => {
  const { page } = await fixture(t);
  await page.route('**/api/v1/admin/connections', route => route.fulfill({
    status: 403, contentType: 'text/html', body: '<h1>Forbidden</h1>'
  }));
  await page.getByRole('button', { name: 'Refresh' }).click();
  await page.getByText(/ask a workspace owner to check your role/i).waitFor();
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
  await page.goto(origin + '/admin/#preview');
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

test('publish a stable schema, issue a field-scoped grant, republish and delete a dataset', async t => {
  const { page, requests } = await fixture(t, { width: 390, height: 844 });
  await page.getByRole('link', { name: 'Datasets', exact: true }).click();
  await page.getByRole('button', { name: 'Publish dataset' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Dataset ID').fill('sales');
  const pipeline = '[{"$match":{"serial":9007199254740993}}]';
  await dialog.getByLabel('Saved pipeline').fill(pipeline);
  await dialog.getByLabel('Field name').fill('_id');
  await dialog.getByRole('button', { name: 'Add field' }).click();
  await dialog.getByLabel('Field name').nth(1).fill('amount');
  await dialog.getByLabel('Field type').nth(1).selectOption('NUMBER');
  await dialog.getByRole('button', { name: 'Publish dataset' }).click();
  await page.getByRole('button', { name: 'Edit sales' }).waitFor();
  const publish = requests.at(-1);
  assert.equal(publish.body.collection, 'orders');
  assert.deepEqual(publish.body.fields, [{ name: '_id', type: 'TEXT' }, { name: 'amount', type: 'NUMBER' }]);
  assert.ok(publish.raw.includes(pipeline));
  await page.getByText('Preview rows', { exact: true }).click();
  await page.getByLabel('Equality filters').fill('{"amount":42}');
  await page.getByRole('button', { name: 'Run dataset preview' }).click();
  await page.getByRole('region', { name: 'Dataset preview' }).getByRole('cell', { name: 'order-001' }).waitFor();
  assert.deepEqual(requests.at(-1).body, {
    connectionId: '@datasets', database: 'published', collection: 'sales', fields: ['_id', 'amount'], filters: { amount: 42 }
  });
  await page.getByRole('link', { name: 'Access tokens' }).click();
  await page.getByRole('button', { name: 'Issue token' }).click();
  await dialog.getByLabel('Token label').fill('Amounts only');
  await dialog.getByRole('group', { name: 'sales', exact: true }).getByLabel('amount', { exact: true }).check();
  await dialog.getByRole('button', { name: 'Issue token' }).click();
  await dialog.getByRole('button', { name: 'Done' }).click();
  assert.deepEqual(requests.at(-1).body.grants, [{ datasetId: 'sales', fields: ['amount'] }]);
  await page.getByRole('link', { name: 'Datasets', exact: true }).click();
  await page.getByRole('button', { name: 'Edit sales' }).click();
  assert.equal(await dialog.getByLabel('Saved pipeline').inputValue(), pipeline);
  await dialog.getByRole('button', { name: 'Republish dataset' }).click();
  await dialog.waitFor({ state: 'hidden' });
  assert.equal(requests.at(-1).body.revision, 'ds1');
  assert.ok(requests.at(-1).raw.includes(pipeline));
  await page.getByRole('button', { name: 'Delete sales' }).click();
  await dialog.getByRole('button', { name: 'Delete dataset' }).click();
  await page.getByRole('heading', { name: 'No datasets yet' }).waitFor();
  assert.deepEqual(requests.at(-1).body, { revision: 'ds2' });
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
});

test('build and reopen a pipeline without rewriting its JSON or sharing the reporting key', async t => {
  const { page, requests } = await fixture(t, { width: 1400, height: 970 }, { start: 'workspace' });
  await page.context().route('https://lookerstudio.google.com/**', route => route.fulfill({ body: 'Looker Studio fixture' }));
  await page.getByRole('region', { name: 'Preview results' }).waitFor();
  await page.getByLabel('Aggregation pipeline').fill('[\n  { "$match": { "amount": { "$lte": 9007199254740993 } } }\n]');
  assert.ok(await page.getByRole('button', { name: 'Open in Looker Studio' }).isDisabled());
  await page.getByLabel('Operation').selectOption('$sort');
  await page.getByLabel('Field', { exact: true }).fill('amount');
  await page.getByRole('button', { name: 'Add stage' }).click();
  const pipeline = await page.getByLabel('Aggregation pipeline').inputValue();
  assert.ok(pipeline.includes('9007199254740993'));
  assert.deepEqual(JSON.parse(pipeline)[1], { $sort: { amount: -1 } });
  await page.getByLabel('Aggregation pipeline').press('Control+Enter');
  await page.getByRole('region', { name: 'Preview results' }).waitFor();
  assert.ok(requests.at(-1).raw.includes('9007199254740993'));
  await page.getByRole('button', { name: 'Fields', exact: true }).click();
  await page.getByRole('region', { name: 'Preview fields' }).getByRole('cell', { name: 'NUMBER' }).waitFor();
  await page.getByRole('button', { name: 'Rows', exact: true }).click();
  if (process.env.MLC_UI_SCREENSHOT_DIR) {
    const desktop = process.env.MLC_UI_SCREENSHOT_DIR + '/pipeline-workspace-desktop.png';
    await page.screenshot({ path: desktop, fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: process.env.MLC_UI_SCREENSHOT_DIR + '/pipeline-workspace-mobile.png', fullPage: true });
  }
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  await page.getByRole('button', { name: 'Open in Looker Studio' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Dataset ID').fill('regional_orders');
  const popup = page.waitForEvent('popup');
  await dialog.getByRole('button', { name: 'Save and open' }).click();
  const tab = await popup;
  await tab.waitForURL('https://lookerstudio.google.com/**');
  assert.deepEqual(JSON.parse(new URL(tab.url()).searchParams.get('connectorConfig')), { collection: 'regional_orders' });
  await tab.close();
  assert.equal(requests.at(-1).body.id, 'regional_orders');
  assert.ok(requests.at(-1).raw.includes(pipeline));
  await page.getByRole('link', { name: 'Datasets', exact: true }).click();
  await page.getByRole('heading', { name: 'regional_orders' }).waitFor();
  const nextPopup = page.waitForEvent('popup');
  await page.getByRole('button', { name: 'Open in Looker Studio' }).click();
  const nextTab = await nextPopup;
  await nextTab.waitForURL('https://lookerstudio.google.com/**');
  await nextTab.close();
});

test('workspace discards stale previews and lets an empty dataset or rejected edit recover', async t => {
  const { page } = await fixture(t, { width: 390, height: 844 }, { start: 'workspace' });
  await page.getByRole('region', { name: 'Preview results' }).waitFor();
  const input = page.getByLabel('Aggregation pipeline');
  await input.fill('[{');
  await page.getByRole('button', { name: 'Run preview' }).click();
  await page.getByRole('alert').getByText(/Enter a JSON array/).waitFor();
  await page.route('**/workspace/preview', route => route.fulfill({ json: { fields: [], rows: [] } }));
  await input.fill('[{"$limit":1}]');
  await page.getByRole('button', { name: 'Run preview' }).click();
  await page.getByText(/No rows returned/).waitFor();
  assert.ok(await page.getByRole('button', { name: 'Open in Looker Studio' }).isDisabled());
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
});

test('bootstrap a primary owner, manage a member, and dismiss each one-time credential', async t => {
  const { page, requests } = await fixture(t, undefined, { start: 'team' });
  const dialog = page.getByRole('dialog');
  const button = name => dialog.getByRole('button', { name, exact: true });
  await page.getByRole('button', { name: 'Create primary owner' }).click();
  await dialog.getByLabel('Email address').fill('owner@example.invalid');
  await button('Create primary owner').click();
  await dialog.getByLabel('One-time password').waitFor();
  assert.equal(requests.at(-1).resource, 'bootstrap-owner');
  await button('Done').click();
  await page.getByRole('button', { name: 'Add member' }).click();
  await dialog.getByLabel('Email address').fill('editor@example.invalid');
  await dialog.getByRole('combobox', { name: 'Role', exact: true }).selectOption('editor');
  await button('Add member').click();
  await button('Done').click();
  assert.deepEqual(requests.at(-1).body, { email: 'editor@example.invalid', role: 'editor' });
  await page.getByRole('button', { name: 'Reset password for editor@example.invalid' }).click();
  await button('Reset password').click();
  await button('Done').click();
  assert.equal(await page.getByLabel('One-time password').count(), 0);
  await page.getByRole('button', { name: 'Edit editor@example.invalid' }).click();
  await dialog.getByRole('combobox', { name: 'Role', exact: true }).selectOption('viewer');
  await dialog.getByLabel('Status').selectOption('disabled');
  await button('Save changes').click();
  await page.getByText('Disabled', { exact: true }).waitFor();
  assert.deepEqual(requests.at(-1).body, { role: 'viewer', disabled: true });
});

test('viewers browse dataset references without reading raw connections; password rotation restricts navigation', async t => {
  const { page, reads } = await fixture(t, undefined, { role: 'viewer', start: 'team' });
  await page.getByRole('heading', { name: 'Published datasets' }).waitFor();
  assert.ok(!reads.some(read => ['connections', 'users'].includes(read.resource)));
  assert.equal(await page.getByRole('link', { name: 'Access tokens' }).count(), 0);
  const pending = await fixture(t, undefined, { role: 'editor', rotate: true, start: 'workspace' });
  const account = pending.page;
  await account.getByText('Set a new password before opening your workspace.').waitFor();
  await account.getByRole('button', { name: 'Sign out' }).waitFor();
  assert.ok(!pending.reads.some(read => read.resource === 'connections'));
  await account.getByLabel('Current password').fill('fixture-temporary');
  await account.getByLabel('New password', { exact: true }).fill('fixture-rotated');
  await account.getByLabel('Confirm new password').fill('mismatch');
  await account.getByRole('button', { name: 'Change password' }).click();
  await account.getByRole('alert').getByText('The two new passwords must match.').waitFor();
  await account.getByLabel('Confirm new password').fill('fixture-rotated');
  await account.getByRole('button', { name: 'Change password' }).click();
  await account.waitForURL(origin + '/');
  assert.deepEqual(pending.requests.at(-1).body, { currentPassword: 'fixture-temporary', newPassword: 'fixture-rotated' });
});

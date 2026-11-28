import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';

// Mutate only uniquely named records created by this invocation.
export async function verifyDatasets(page, origin) {
  const name = 'browser-' + randomUUID();
  const dialog = page.getByRole('dialog');
  const admin = origin + '/api/v1/admin/';
  async function list(resource) {
    const response = await page.request.get(admin + resource);
    assert.equal(response.status(), 200, 'Could not inspect ' + resource);
    return (await response.json())[resource];
  }
  async function remove(path, data) {
    const response = await page.request.delete(admin + path, {
      headers: { Origin: origin, 'X-MLC-Request': 'console' }, data
    });
    assert.equal(response.status(), 204, 'Could not clean up ' + path);
  }
  try {
    const datasets = page.getByRole('link', { name: 'Datasets', exact: true });
    await datasets.click();
    await page.getByRole('button', { name: 'Publish dataset' }).click();
    await dialog.getByLabel('Dataset ID').fill(name);
    for (const [label, value] of [['Connection', 'demo'], ['Database', 'mlc-sandbox'], ['Collection', 'orders']]) {
      await dialog.getByRole('combobox', { name: label, exact: true }).selectOption(value);
    }
    await dialog.getByLabel('Saved pipeline').fill('[{"$match":{"amount":{"$gte":0}}}]');
    await dialog.getByLabel('Field name').fill('_id');
    await dialog.getByRole('button', { name: 'Add field' }).click();
    await dialog.getByLabel('Field name').nth(1).fill('amount');
    await dialog.getByLabel('Field type').nth(1).selectOption('NUMBER');
    await dialog.getByRole('button', { name: 'Publish dataset' }).click();
    const edit = page.getByRole('button', { name: 'Edit ' + name });
    await edit.waitFor();
    await page.getByText('Preview rows', { exact: true }).click();
    await page.getByLabel('Equality filters').fill('{"amount":42}');
    await page.getByRole('button', { name: 'Run dataset preview' }).click();
    const result = page.getByRole('region', { name: 'Dataset preview' });
    await result.getByRole('cell', { name: 'order-001' }).waitFor();
    assert.deepEqual(await result.getByRole('cell').allTextContents(), ['order-001', '42']);

    await page.getByRole('link', { name: 'Access tokens' }).click();
    await page.getByRole('button', { name: 'Issue token' }).click();
    await dialog.getByLabel('Token label').fill(name);
    await dialog.getByRole('group', { name, exact: true }).getByLabel('amount', { exact: true }).check();
    await dialog.getByRole('button', { name: 'Issue token' }).click();
    await dialog.getByRole('button', { name: 'Done' }).click();
    const token = (await list('tokens')).find(token => token.label === name);
    assert.ok(token && !token.revoked);
    assert.deepEqual(token.grants, [{ datasetId: name, fields: ['amount'] }]);
    await page.getByRole('button', { name: 'Revoke ' + name }).click();
    await dialog.getByRole('button', { name: 'Revoke token' }).click();
    await page.getByText('Revoked access for ' + name + '.', { exact: true }).waitFor();
    assert.equal((await list('tokens')).find(item => item.id === token.id).revoked, true);

    await datasets.click();
    await page.getByRole('button', { name, exact: true }).click();
    await edit.click();
    await dialog.getByLabel('Saved pipeline').fill('[{"$sort":{"amount":-1}}]');
    await dialog.getByRole('button', { name: 'Republish dataset' }).click();
    await dialog.waitFor({ state: 'hidden' });
    const updated = (await list('datasets')).find(dataset => dataset.id === name);
    assert.deepEqual(updated.pipeline, [{ $sort: { amount: -1 } }]);
    await page.getByRole('button', { name: 'Delete ' + name }).click();
    await dialog.getByRole('button', { name: 'Delete dataset' }).click();
    await page.getByText('Deleted ' + name + '.', { exact: true }).waitFor();
    assert.ok(!(await list('datasets')).some(dataset => dataset.id === name));
    console.log('Verified dataset publishing, filtered preview, field-scoped token issuance and revocation, republishing and deletion in the hosted browser.');
  } finally {
    try {
      for (const token of await list('tokens')) {
        if (token.label === name && !token.revoked) await remove('tokens/' + encodeURIComponent(token.id), {});
      }
    } finally {
      const remaining = (await list('datasets')).find(dataset => dataset.id === name);
      if (remaining) await remove('datasets/' + name, { revision: remaining.revision });
    }
  }
}

// Open only the sandbox fixture dataset. Intercept the final Google URL before navigating away.
export async function verifyWorkbench(page, origin) {
  const id = 'workbench-' + randomUUID();
  const admin = origin + '/api/v1/admin/';
  const pattern = 'https://lookerstudio.google.com/datasources/create?**';
  const intercept = route => route.fulfill({ contentType: 'text/plain', body: 'Verified the connector setup URL.' });
  await page.context().route(pattern, intercept);
  try {
    await page.getByRole('link', { name: 'Pipeline workspace', exact: true }).click();
    await page.getByRole('combobox', { name: 'Connection', exact: true }).selectOption('demo');
    await page.getByRole('combobox', { name: 'Database', exact: true }).selectOption('mlc-sandbox');
    await page.getByRole('combobox', { name: 'Collection', exact: true }).selectOption('orders');
    const pipeline = '[{"$sort":{"amount":-1}},{"$limit":1},{"$project":{"_id":1,"amount":1}}]';
    await page.getByLabel('Aggregation pipeline').fill(pipeline);
    await page.getByRole('button', { name: 'Run preview' }).click();
    await page.getByRole('region', { name: 'Preview results' }).getByRole('cell', { name: '42' }).waitFor();
    await page.getByRole('button', { name: 'Fields', exact: true }).click();
    await page.getByRole('region', { name: 'Preview fields' }).getByRole('cell', { name: 'NUMBER' }).waitFor();
    await page.getByRole('button', { name: 'Open in Looker Studio' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('Dataset ID').fill(id);
    async function handoff(click) {
      const [tab] = await Promise.all([page.waitForEvent('popup'), click()]);
      try {
        await tab.waitForURL(pattern);
        const url = new URL(tab.url());
        const config = JSON.parse(url.searchParams.get('connectorConfig'));
        assert.equal(config.collection, id);
        assert.equal(decodeURIComponent(config.connectionId), '@datasets');
        assert.ok(url.searchParams.get('connectorId'));
        assert.ok(!Object.hasOwn(config, 'pipeline'));
        return url.href;
      } finally { await tab.close(); }
    }
    const created = await handoff(() => dialog.getByRole('button', { name: 'Save and open' }).click());
    await page.getByRole('link', { name: 'Datasets', exact: true }).click();
    await page.getByRole('button', { name: id, exact: true }).click();
    const reopened = await handoff(() => page.getByRole('button', { name: 'Open in Looker Studio' }).click());
    assert.equal(created, reopened);
    console.log('Verified typed output, publishing from the workbench, and reopening the managed dataset through the Looker Studio handoff.');
  } finally {
    await page.context().unroute(pattern, intercept);
    const catalog = await page.request.get(admin + 'datasets');
    assert.equal(catalog.status(), 200);
    const item = (await catalog.json()).datasets.find(dataset => dataset.id === id);
    if (item) {
      const removed = await page.request.delete(admin + 'datasets/' + id, { headers: { Origin: origin, 'X-MLC-Request': 'console' }, data: { revision: item.revision } });
      assert.equal(removed.status(), 204, 'Could not clean up this workbench fixture');
    }
  }
}

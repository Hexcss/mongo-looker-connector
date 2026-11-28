import assert from 'node:assert/strict';

// Read only the two release-fixture collections configured on the sandbox.
export async function verifyJoin(page) {
  const input = page.getByLabel('Aggregation pipeline');
  await input.fill('[]');
  await page.getByLabel('Operation').selectOption('$lookup');
  await page.getByLabel('Local field').fill('_id');
  await page.getByRole('combobox', { name: 'Join collection', exact: true }).selectOption('shipping_zones');
  await page.getByLabel('Foreign field').fill('_id');
  await page.getByLabel('Output field').fill('shipping');
  await page.getByLabel('One row per match').check();
  await page.getByRole('button', { name: 'Add stage' }).click();
  const stages = JSON.parse(await input.inputValue());
  assert.deepEqual(stages, [
    { $lookup: { from: 'shipping_zones', localField: '_id', foreignField: '_id', as: 'shipping' } },
    { $unwind: '$shipping' }
  ]);
  stages.push({ $project: { _id: 1, region: '$shipping.region' } }, { $sort: { _id: 1 } });
  await input.fill(JSON.stringify(stages, null, 2));
  const result = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/admin/workspace/preview');
  await page.getByRole('button', { name: 'Run preview' }).click();
  const preview = await result;
  assert.equal(preview.status(), 200);
  assert.deepEqual((await preview.json()).rows, [['order-001', 'EU-South'], ['order-002', 'EU-West']]);
  const table = page.getByRole('region', { name: 'Preview results' });
  await table.getByRole('cell', { name: 'EU-South' }).waitFor();
  stages.unshift({ $match: { amount: 42 } });
  await input.fill(JSON.stringify(stages, null, 2));
  await page.getByRole('button', { name: 'Run preview' }).click();
  await table.getByRole('cell', { name: 'EU-South' }).waitFor();
  assert.equal(await table.getByRole('row').count(), 2, 'Expected the header and one matching order');
  const response = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/admin/workspace/explain');
  await page.getByRole('button', { name: 'Explain', exact: true }).click();
  const raw = await response;
  assert.equal(raw.status(), 200);
  const stats = await raw.json();
  assert.ok(Number.isSafeInteger(stats.documentsExamined) && stats.documentsExamined > 0);
  assert.ok(Number.isSafeInteger(stats.keysExamined) && stats.keysExamined > 0);
  assert.ok(stats.indexesUsed?.some(name => name.includes('amount')), 'Expected the sandbox orders.amount index');
  const plan = page.getByRole('region', { name: 'Execution plan' });
  const number = x => x === null ? 'Unavailable' : Number(x).toLocaleString('en-US');
  assert.equal(await plan.locator('dt', { hasText: 'Documents scanned' }).locator('..').locator('dd').innerText(), number(stats.documentsExamined));
  assert.equal(await plan.locator('dt', { hasText: 'Index entries scanned' }).locator('..').locator('dd').innerText(), number(stats.keysExamined));
  const indexes = plan.locator('dt', { hasText: 'Indexes used' }).locator('..').locator('dd');
  if (stats.indexesUsed?.length) assert.deepEqual(await indexes.locator('li').allTextContents(), stats.indexesUsed);
  else assert.equal(await indexes.innerText(), stats.indexesUsed === null ? 'Unavailable' : 'None');
  if (process.env.MLC_UI_SCREENSHOT_DIR) {
    await page.screenshot({ path: process.env.MLC_UI_SCREENSHOT_DIR + '/sandbox-join-explain.png', fullPage: true });
  }
  console.log('Verified hosted equality join, joined rows and database-reported execution metrics.');
}

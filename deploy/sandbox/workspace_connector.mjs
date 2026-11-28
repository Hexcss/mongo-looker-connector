// Run the connector's synchronous Apps Script entry points against the live HTTPS gateway.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { createContext, runInContext } from 'node:vm';
import { Config } from '../../connector/config-fixture.mjs';

const input = JSON.parse(readFileSync(0, 'utf8'));
let phase = 'HTTPS transport';
try {
  if (process.argv[2] === '--fetch') {
    const { url, options } = input;
    const response = await fetch(url, { method: options.method ?? 'GET', headers: options.headers,
      body: options.payload, signal: AbortSignal.timeout(65000), redirect: 'error' });
    process.stdout.write(JSON.stringify({ code: response.status, content: await response.text() }));
  } else {
    const { origin, key, connectorConfig } = input;
    const params = JSON.parse(connectorConfig);
    const script = process.env.VERIFY_CONNECTOR_SOURCE || new URL('../../connector/Code.gs', import.meta.url);
    const source = readFileSync(script, 'utf8');
    const properties = new Map();
    const cc = {
      getConfig: () => new Config(),
      AuthType: { PATH_KEY: 'PATH_KEY', KEY: 'KEY' },
      newAuthTypeResponse: () => ({
        setAuthType(type) { this.type = type; return this; },
        setHelpUrl() { return this; }, build() { return this; }
      }),
      newUserError: () => ({ setText: () => ({ throwException() { throw new Error('Connector rejected the request'); } }) })
    };
    function fetchGateway(url, options) {
      const target = new URL(url);
      assert.ok(target.protocol === 'https:' && target.origin === origin && !target.username && !target.password);
      assert.ok(target.pathname.startsWith('/api/v1/') && !target.pathname.startsWith('/api/v1/admin/'));
      assert.equal(options.followRedirects, false);
      assert.ok(options.headers['X-MLC-Token'] === key);
      const result = spawnSync(process.execPath, [fileURLToPath(import.meta.url), '--fetch'], {
        input: JSON.stringify({ url, options: { ...options, headers: { ...options.headers,
          ...(options.contentType ? { 'Content-Type': options.contentType } : {}) } } }),
        encoding: 'utf8', timeout: 70000, stdio: ['pipe', 'pipe', 'ignore']
      });
      assert.equal(result.status, 0);
      const { code, content } = JSON.parse(result.stdout);
      return { getResponseCode: () => code, getContentText: () => content };
    }
    const api = createContext({ DataStudioApp: { createCommunityConnector: () => cc },
      PropertiesService: { getUserProperties: () => ({
        getProperty: name => properties.get(name), setProperty: (name, value) => properties.set(name, value)
      }) }, UrlFetchApp: { fetch: fetchGateway }, console: { warn() {} }, MLC_GATEWAY_ORIGINS: [origin] });
    phase = 'PATH_KEY authorization';
    runInContext(source, api);
    assert.equal(api.getAuthType().type, 'PATH_KEY');
    assert.equal(api.setCredentials({ pathKey: { path: origin, key } }).errorCode, 'NONE');
    assert.ok(api.isAuthValid());

    phase = 'preconfigured setup';
    const request = { configParams: params, languageCode: 'en' };
    const config = api.getConfig(request);
    assert.equal(config.stepped, false);
    for (const id of ['gatewayOrigin', 'connectionId', 'collection']) {
      assert.ok(config.questions.find(q => q.id === id)?.choices.includes(params[id]));
    }
    assert.ok(!config.questions.some(q => ['queryMode', 'pipeline', 'database'].includes(q.id)));

    phase = 'field permissions';
    const schema = api.getSchema(request).schema;
    assert.deepEqual(Array.from(schema, f => [f.name, f.dataType]), [['total', 'NUMBER']]);
    assert.throws(() => api.getData({ ...request, fields: [{ name: 'order' }] }));

    phase = 'report rows';
    const data = api.getData({ ...request, fields: [{ name: 'total' }] });
    assert.deepEqual(Array.from(data.rows, row => Array.from(row.values)), [[42]]);
    console.log('Connector accepted PATH_KEY and the unchanged console handoff; returned only total (NUMBER), value 42.');
  }
} catch {
  // Connector exceptions and HTTP errors can contain request details. Keep the ephemeral key out of logs.
  console.error('Workspace connector verification failed during ' + phase + '.');
  process.exitCode = 1;
}

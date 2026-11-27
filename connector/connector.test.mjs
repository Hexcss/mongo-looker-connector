import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { createContext, runInContext } from 'node:vm';
import test from 'node:test';

const source = readFileSync(new URL('./Code.gs', import.meta.url), 'utf8');
const key = 'mlc_' + '1'.repeat(32) + '.' + 'x'.repeat(43);
const origin = 'https://demo.a.run.app';
const invoker = 'reports@example-project.iam.gserviceaccount.com';
const params = {connectionId: 'demo', database: 'sales', collection: 'daily%20orders', dateField: 'order.date'};
const range = {startDate: '2026-01-01', endDate: '2026-01-02'};
const fields = [{name: '_id', type: 'TEXT'}, {name: 'amount', type: 'NUMBER'},
  {name: 'order.date', type: 'YEAR_MONTH_DAY_SECOND'}];

class Config {
  questions = [];
  newSelectSingle() {
    const q = {choices: []};
    this.questions.push(q);
    return {
      setId(id) { q.id = id; return this; },
      setName() { return this; },
      setIsDynamic(value) { q.dynamic = value; return this; },
      addOption(option) { q.choices.push(option.value); return this; }
    };
  }
  newOptionBuilder() { return {setLabel() { return this; }, setValue(value) { this.value = value; return this; }}; }
  setIsSteppedConfig(value) { this.stepped = value; }
  setDateRangeRequired(value) { this.dateRequired = value; }
  build() { return this; }
}

function connector() {
  const properties = new Map();
  const calls = [];
  const warnings = [];
  const settings = {fields, status: 200, iamStatus: 200};
  const getUserProperties = () => ({
    setProperty: (name, value) => properties.set(name, value),
    deleteProperty: name => properties.delete(name),
    getProperty: name => properties.get(name)
  });
  const cc = {
    AuthType: {KEY: 'KEY'},
    newAuthTypeResponse: () => ({setAuthType: auth => ({build: () => ({type: auth})})}),
    newUserError: () => ({setText: text => ({throwException: () => { throw new Error(text); }})}),
    getConfig: () => new Config()
  };
  const context = createContext({
    console: {warn: event => warnings.push(event)},
    DataStudioApp: {createCommunityConnector: () => cc},
    PropertiesService: {getUserProperties},
    ScriptApp: {getOAuthToken: () => 'test-user-access'},
    Utilities: {DigestAlgorithm: {}, Charset: {}, computeDigest: (_, name) => [...createHash('sha256').update(name).digest()]},
    MLC_GATEWAY_URL: origin, MLC_INVOKER: invoker,
    UrlFetchApp: {fetch(url, opts) {
      const data = opts.payload && JSON.parse(opts.payload);
      calls.push({url, data});
      assert.equal(opts.followRedirects, false);
      let body, status = settings.status;
      if (url.startsWith('https://iamcredentials.googleapis.com/')) {
        assert.equal(url, 'https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/' + invoker + ':generateIdToken');
        assert.deepEqual({...opts.headers}, {Authorization: 'Bearer test-user-access', 'x-goog-user-project': 'example-project'});
        assert.deepEqual(data, {audience: origin, includeEmail: true});
        if (settings.iamThrows) throw new Error('upstream-private-detail');
        body = settings.iamError || {token: 'test-invoker'};
        status = settings.iamStatus;
      } else {
        assert.deepEqual({...opts.headers}, {Authorization: 'Bearer ' + key, 'X-Serverless-Authorization': 'Bearer test-invoker'});
        const routes = {
          connections: {connections: ['demo']},
          'connections/demo/databases': {databases: ['sales']},
          'collections?connectionId=demo&database=sales': {collections: ['daily orders']},
          schema: {fields: settings.fields}, query: {rows: [[42, 'order-001']]}
        };
        assert.ok(url.startsWith(origin + '/api/v1/'));
        body = routes[url.slice((origin + '/api/v1/').length)];
        assert.ok(body);
      }
      return {getResponseCode: () => status, getContentText: () => JSON.stringify(body)};
    }}
  });
  runInContext(source, context);
  return {api: context, calls, settings, properties, warnings};
}

test('per-user KEY auth validates, stores, rechecks revocation and resets', () => {
  const {api, calls, settings, properties} = connector();
  assert.equal(api.getAuthType().type, 'KEY');
  assert.equal(api.setCredentials({key: 'bad'}).errorCode, 'INVALID_CREDENTIALS');
  assert.equal(calls.length, 0);
  assert.equal(api.setCredentials({key}).errorCode, 'NONE');
  assert.equal(api.isAuthValid(), true);
  settings.status = 401;
  assert.equal(api.isAuthValid(), false);
  settings.status = 503;
  assert.throws(() => api.isAuthValid(), /HTTP 503/);
  api.resetAuth();
  assert.equal(properties.size, 0);
});

test('reports missing IAM grant separately from malformed reporting token', () => {
  const {api, settings, properties} = connector();
  settings.iamStatus = 403;
  assert.throws(() => api.setCredentials({key}), /reporting allowlist/);
  assert.equal(properties.size, 0);
});

test('stepped discovery preserves real names and only selects BSON dates', () => {
  const {api, settings} = connector();
  api.setCredentials({key});
  let config = api.getConfig({});
  assert.equal(config.stepped, true);
  assert.deepEqual(config.questions.map(q => q.id), ['connectionId']);
  config = api.getConfig({configParams: params});
  assert.equal(config.stepped, false);
  assert.equal(config.dateRequired, true);
  assert.deepEqual(config.questions.map(q => q.choices), [['demo'], ['sales'], ['daily%20orders'], ['order.date']]);
  settings.fields = fields.slice(0, 2);
  config = api.getConfig({configParams: params});
  assert.equal(config.dateRequired, false);
  assert.equal(config.questions.length, 3);
  assert.throws(() => api.getConfig({configParams: {connectionId: 'other'}}), /unavailable/);
});

test('typed schema, field subsets and date intervals compose with Looker filtering', () => {
  const {api, calls} = connector();
  api.setCredentials({key});
  const {schema} = api.getSchema({configParams: params});
  assert.equal(schema[1].semantics.conceptType, 'METRIC');
  assert.match(schema[2].name, /^mlc_hash_[0-9a-f]{64}$/);
  assert.equal(schema[2].label, 'order.date');
  assert.equal(schema[2].semantics.semanticType, 'YEAR_MONTH_DAY_SECOND');
  const data = api.getData({configParams: params, dateRange: range,
    fields: [{name: 'amount'}, {name: '_id', forFilterOnly: true}]});
  assert.deepEqual(JSON.parse(JSON.stringify(data.rows)), [{values: [42, 'order-001']}]);
  assert.deepEqual(Array.from(data.schema, f => f.name), ['amount', '_id']);
  assert.equal(data.filtersApplied, false);
  assert.deepEqual(calls.at(-1).data, {connectionId: 'demo', database: 'sales', collection: 'daily orders',
    pipeline: [{$sort: {_id: 1}}], fields: ['amount', '_id'], dateRange: {field: 'order.date', ...range}});
  assert.throws(() => api.getData({configParams: params, fields: [{name: 'amount'}]}), /date range/);
});

test('date-free collections connect directly and one eligible date defaults without selection', () => {
  const {api, settings, calls} = connector();
  api.setCredentials({key});
  const {dateField, ...namespace} = params;
  const request = {configParams: namespace, dateRange: range, fields: [{name: 'amount'}, {name: '_id'}]};
  assert.equal(api.getConfig({configParams: namespace}).stepped, false);
  api.getData(request);
  assert.equal(calls.at(-1).data.dateRange.field, 'order.date');
  settings.fields = fields.concat({name: 'shipped', type: 'YEAR_MONTH_DAY_SECOND'});
  assert.equal(api.getConfig({configParams: namespace}).stepped, true);
  assert.throws(() => api.getData(request), /Choose a report date field/);
  settings.fields = fields.slice(0, 2);
  assert.equal(api.getConfig({configParams: namespace}).dateRequired, false);
  api.getData({...request, dateRange: undefined});
  assert.equal(calls.at(-1).data.dateRange, undefined);
});

test('IAM diagnostics allowlist reason codes and never echo response or transport details', () => {
  const {api, settings, warnings} = connector();
  settings.iamStatus = 403;
  const info = {'@type': 'type.googleapis.com/google.rpc.ErrorInfo', reason: 'USER_PROJECT_DENIED'};
  settings.iamError = {error: {message: 'upstream-private-detail', details: [info]}};
  assert.throws(() => api.setCredentials({key}), /HTTP 403, USER_PROJECT_DENIED/);
  assert.equal(warnings.pop(), 'reporting_auth stage=iam status=403 reason=USER_PROJECT_DENIED');
  info.reason = 'upstream-private-detail';
  assert.throws(() => api.setCredentials({key}), /HTTP 403, HTTP_ERROR/);
  assert.equal(warnings.pop(), 'reporting_auth stage=iam status=403 reason=HTTP_ERROR');
  settings.iamThrows = true;
  assert.throws(() => api.setCredentials({key}), /could not contact iam/);
  assert.equal(warnings.pop(), 'reporting_auth stage=iam status=0 reason=FETCH_FAILED');
});

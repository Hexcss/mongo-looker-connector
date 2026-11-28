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
  newSelectSingle() { return this.entry('select'); }
  newTextArea() { return this.entry('textarea'); }
  newInfo() {
    const q = {type: 'info'};
    this.questions.push(q);
    return {
      setId(id) { q.id = id; return this; },
      setText(text) { q.text = text; return this; }
    };
  }
  entry(type) {
    const q = {type, choices: [], labels: []};
    this.questions.push(q);
    return {
      setId(id) { q.id = id; return this; },
      setName(name) { q.name = name; return this; },
      setHelpText(help) { q.help = help; return this; },
      setPlaceholder(example) { q.example = example; return this; },
      setAllowOverride(value) { q.override = value; return this; },
      setIsDynamic(value) { q.dynamic = value; return this; },
      addOption(option) { q.choices.push(option.value); q.labels.push(option.label); return this; }
    };
  }
  newOptionBuilder() { return {setLabel(label) { this.label = label; return this; }, setValue(value) { this.value = value; return this; }}; }
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
    newAuthTypeResponse: () => ({
      setAuthType(type) { this.type = type; return this; },
      setHelpUrl(helpUrl) { this.helpUrl = helpUrl; return this; },
      build() { return {type: this.type, helpUrl: this.helpUrl}; }
    }),
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
      calls.push({url, data, payload: opts.payload});
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
          connections: {connections: settings.connections || ['demo']},
          'connections/%40datasets/databases': {databases: ['published']},
          'collections?connectionId=%40datasets&database=published': {collections: ['weekly']},
          'connections/demo/databases': {databases: ['sales']},
          'collections?connectionId=demo&database=sales': {collections: ['daily orders']},
          schema: {fields: data?.pipeline ? (settings.outputFields ?? settings.fields) : settings.fields},
          query: {rows: settings.rows || [[42, 'order-001']]}
        };
        assert.ok(url.startsWith(origin + '/api/v1/'));
        body = routes[url.slice((origin + '/api/v1/').length)];
        if (url.endsWith('/schema') && !data?.pipeline && settings.sourceSchemaStatus) status = settings.sourceSchemaStatus;
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
  const auth = api.getAuthType();
  assert.equal(auth.type, 'KEY');
  assert.equal(auth.helpUrl, 'https://github.com/Hexcss/mongo-looker-connector/blob/167f44fa033a023a149e401f98d9b25bf2ce1f94/docs/looker-studio.md#sign-in-with-a-reporting-token');
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
  config = api.getConfig({configParams: {...params, queryMode: 'collection'}});
  assert.equal(config.stepped, false);
  assert.equal(config.dateRequired, true);
  assert.deepEqual(config.questions.filter(q => q.type === 'select').map(q => q.choices), [['demo'], ['sales'], ['daily%20orders'], ['collection', 'custom'], ['order.date']]);
  settings.fields = fields.slice(0, 2);
  config = api.getConfig({configParams: {...params, queryMode: 'collection'}});
  assert.equal(config.dateRequired, false);
  assert.equal(config.questions.filter(q => q.type === 'select').length, 4);
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
  assert.equal(api.getConfig({configParams: {...namespace, queryMode: 'collection'}}).stepped, false);
  api.getData(request);
  assert.equal(calls.at(-1).data.dateRange.field, 'order.date');
  settings.fields = fields.concat({name: 'shipped', type: 'YEAR_MONTH_DAY_SECOND'});
  assert.equal(api.getConfig({configParams: {...namespace, queryMode: 'collection'}}).stepped, true);
  assert.throws(() => api.getData(request), /Choose a report date field/);
  settings.fields = fields.slice(0, 2);
  assert.equal(api.getConfig({configParams: {...namespace, queryMode: 'collection'}}).dateRequired, false);
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

const customFields = [{name: 'revenue', type: 'NUMBER'}, {name: 'lastOrder', type: 'YEAR_MONTH_DAY_SECOND'}];
const pipeline = '[\n  {"$match":{"comment":"100% / %2F","serial":9007199254740993}},\n' +
  '  {"$project":{"revenue":"$amount","lastOrder":"$order.date"}},{"$sort":{"2":-1,"1":1}}\n]';
const custom = {...params, queryMode: 'custom', pipeline};

test('stepped raw source setup offers a private pipeline editor and optional source date', () => {
  const {api, calls, settings} = connector();
  settings.outputFields = customFields;
  settings.fields = fields.concat({name: 'none', type: 'YEAR_MONTH_DAY_SECOND'});
  api.setCredentials({key});
  const mode = api.getConfig({configParams: params});
  assert.deepEqual(mode.questions.at(-1).labels, ['Collection documents', 'Custom aggregation']);
  assert.equal(mode.stepped, true);
  const editor = api.getConfig({configParams: {...custom, pipeline: ''}});
  assert.equal(editor.stepped, true);
  const question = editor.questions.at(-1);
  assert.deepEqual([question.id, question.type, question.dynamic, question.override], ['pipeline', 'textarea', true, false]);
  assert.ok(Array.isArray(JSON.parse(question.example)));
  assert.ok(question.example.includes('\n'));
  assert.equal(calls.filter(call => call.url.endsWith('/schema')).length, 0);
  const dates = api.getConfig({configParams: custom}).questions.at(-1);
  assert.equal(dates.id, 'sourceDateField');
  assert.deepEqual(dates.labels, ['No date filter', 'order.date', 'none']);
  for (const [index, required] of [[0, false], [1, true], [2, true]]) {
    const final = api.getConfig({configParams: {...custom, sourceDateField: dates.choices[index]}});
    assert.equal(final.stepped, false);
    assert.equal(final.dateRequired, required);
  }
  settings.fields = [];
  assert.equal(api.getConfig({configParams: custom}).dateRequired, false);
  settings.sourceSchemaStatus = 413;
  assert.equal(api.getConfig({configParams: custom}).dateRequired, false);
  assert.throws(() => api.getConfig({configParams: {...custom, sourceDateField: 'order.date'}}), /gateway limit/i);
  settings.sourceSchemaStatus = 403;
  assert.throws(() => api.getConfig({configParams: custom}), /access denied/i);
  settings.sourceSchemaStatus = 200;
  settings.outputFields = [];
  assert.throws(() => api.getConfig({configParams: custom}), /no .*fields/i);
});

test('uses exact JSON in output schema and data queries and filters only selected source dates', () => {
  const {api, calls, settings} = connector();
  settings.outputFields = customFields;
  settings.rows = [[42]];
  api.setCredentials({key});
  assert.deepEqual(Array.from(api.getSchema({configParams: custom}).schema, f => f.name), ['revenue', 'lastOrder']);
  const report = {configParams: custom, fields: [{name: 'revenue'}], dateRange: range};
  assert.equal(api.getData(report).rows[0].values[0], 42);
  assert.equal(calls.at(-1).data.dateRange, undefined);
  assert.equal(calls.filter(call => call.url.endsWith('/schema') && !call.data.pipeline).length, 0);
  for (const call of calls.filter(call => call.data?.pipeline)) {
    assert.ok(call.payload.includes(pipeline), 'changed numeric literals, key order or percent sequences');
    assert.equal(call.data.pipeline.length, 3);
    assert.equal(call.data.collection, 'daily orders');
  }
  api.getData({...report, configParams: {...custom, sourceDateField: 'order.date'}});
  assert.deepEqual(calls.at(-1).data.dateRange, {field: 'order.date', ...range});
  for (const sourceDateField of ['lastOrder', 'amount']) {
    assert.throws(() => api.getData({...report, configParams: {...custom, sourceDateField}}), /date field/i);
  }
  assert.throws(() => api.getData({...report, configParams: {...custom, sourceDateField: 'order.date'}, dateRange: undefined}), /date range/i);
  assert.throws(() => api.getData({...report, fields: [{name: 'amount'}]}), /unavailable/);
});

test('rejects invalid JSON and stale custom configurations without falling back to a raw collection', () => {
  const {api, calls, settings} = connector();
  api.setCredentials({key});
  assert.throws(() => api.getConfig({configParams: {...custom, pipeline: {}}}), /JSON array/i);
  for (const value of ['', '  ', '[', 'null', '{}', '[],"fields":["other"]']) {
    assert.throws(() => api.getSchema({configParams: {...custom, pipeline: value}}), /JSON array/i);
  }
  assert.throws(() => api.getSchema({configParams: {...params, queryMode: 'typo'}}), /query mode/i);
  assert.equal(calls.filter(call => call.url.endsWith('/schema')).length, 0);
  settings.status = 400;
  assert.throws(() => api.getSchema({configParams: custom}), /aggregation pipeline/i);
});

test('published datasets have no custom mode and keep their configured pipeline and restrictions', () => {
  const {api, calls, settings} = connector();
  settings.connections = ['@datasets'];
  api.setCredentials({key});
  const published = {connectionId: '%40datasets', database: 'published', collection: 'weekly'};
  const config = api.getConfig({configParams: published});
  assert.deepEqual(config.questions.map(q => q.id), ['connectionId', 'collection', 'dateField', 'fieldSummary']);
  assert.equal(config.stepped, false);
  api.getData({configParams: published, fields: [{name: 'amount'}, {name: '_id'}], dateRange: range});
  assert.deepEqual(calls.at(-1).data.pipeline, [{$sort: {_id: 1}}]);
  const before = calls.length;
  const forged = {configParams: {...published, queryMode: 'custom', pipeline: '[]'}};
  assert.throws(() => api.getSchema(forged), /published dataset/i);
  assert.throws(() => api.getData({...forged, fields: [{name: 'amount'}]}), /published dataset/i);
  assert.equal(calls.length, before);
});

test('published source uses its fixed namespace in setup, schema and data for both saved forms', () => {
  const {api, calls, settings} = connector();
  settings.connections = ['@datasets', 'demo'];
  settings.fields = [{name: 'revenue', type: 'NUMBER'}];
  settings.rows = [[42]];
  api.setCredentials({key});
  for (const languageCode of ['en', 'es-ES']) {
    const next = api.getConfig({languageCode, configParams: {connectionId: '%40datasets'}});
    assert.deepEqual(next.questions.map(q => q.name), languageCode === 'en'
      ? ['Data source', 'Published dataset'] : ['Origen de datos', 'Conjunto de datos publicado']);
    assert.deepEqual(next.questions[0].choices, ['%40datasets', 'demo']);
    assert.deepEqual(next.questions[0].labels, [languageCode === 'en' ? 'Published datasets' : 'Conjuntos de datos publicados', 'demo']);
    for (const extra of [{}, {database: 'published'}]) {
      const configParams = {connectionId: '%40datasets', collection: 'weekly', ...extra};
      const done = api.getConfig({configParams, languageCode});
      assert.equal(done.questions.at(-1).text, languageCode === 'en' ? '1 published field · revenue' : '1 campo publicado · revenue');
      assert.equal(api.getSchema({configParams}).schema[0].name, 'revenue');
      assert.equal(api.getData({configParams, fields: [{name:'revenue'}]}).rows[0].values[0], 42);
    }
  }
  assert.ok(calls.filter(c => c.url.endsWith('/schema') || c.url.endsWith('/query'))
    .every(c => c.data.connectionId === '@datasets' && c.data.database === 'published'));
  assert.ok(!calls.some(c => c.url.endsWith('/databases')));
  const forged = {configParams: {connectionId: '%40datasets', database: 'private', collection: 'weekly'}};
  const before = calls.length;
  assert.throws(() => api.getConfig({...forged, languageCode:'es'}), /publicad/i);
  assert.throws(() => api.getSchema(forged), /published/i);
  assert.throws(() => api.getData({...forged, fields: [{name:'revenue'}]}), /published/i);
  assert.equal(calls.length, before);
});

test('sample summary lists up to five permitted output names and never fetches rows or leaks source fields', () => {
  const {api, calls, settings} = connector();
  settings.fields = [{name:'unprojected_source', type:'TEXT'}];
  settings.outputFields = Array.from({length: 7}, (_, i) => ({name:'result' + i, type:'TEXT'}));
  settings.rows = [['private-row-content']];
  api.setCredentials({key});
  for (const languageCode of ['en', 'es-419', 'fr']) {
    const start = calls.length;
    const {questions} = api.getConfig({configParams:custom, languageCode});
    const summary = questions.at(-1);
    assert.equal(summary.type, 'info');
    assert.equal(summary.text, (languageCode === 'es-419' ? '7 campos de la muestra' : '7 sampled fields') +
      ' · result0, result1, result2, result3, result4, …');
    assert.doesNotMatch(summary.text, /private-row|unprojected_source|result[56]/);
    assert.equal(calls.slice(start).filter(c => c.url.endsWith('/schema')).length, 2);
    assert.ok(!calls.slice(start).some(c => c.url.endsWith('/query')));
  }
  const longName = '📈'.repeat(60);
  settings.outputFields = [{name: longName, type: 'NUMBER'}];
  assert.equal(api.getConfig({configParams: custom}).questions.at(-1).text,
    '1 sampled field · ' + '📈'.repeat(47) + '…');
  assert.equal(api.getSchema({configParams: custom}).schema[0].label, longName);
});

test('Spanish setup retains identifiers and localizes help, validation and gateway errors', () => {
  const {api, settings} = connector();
  api.setCredentials({key});
  const config = configParams => api.getConfig({languageCode:'ES-mx', configParams});
  const mode = config(params).questions.at(-1);
  assert.deepEqual(mode.labels, ['Documentos de la colección','Agregación personalizada']);
  assert.deepEqual(mode.choices, ['collection','custom']);
  assert.match(mode.help, /colección/);
  const editor = config({...custom, pipeline:''}).questions.at(-1);
  assert.equal(editor.name, 'Pipeline de agregación de MongoDB (JSON)');
  assert.match(editor.help, /etapas/);
  const dates = config(custom).questions.at(-1);
  assert.equal(dates.name, 'Filtrar documentos de origen por fecha (UTC)');
  assert.match(dates.help, /antes/);
  assert.equal(dates.choices[0], '%24none');
  assert.equal(dates.labels[0], 'Sin filtro de fecha');
  assert.throws(() => config({...custom, pipeline:'{' }), /array JSON/i);
  assert.throws(() => config({connectionId:'missing'}), /disponible/i);
  settings.status = 403;
  assert.throws(() => config(params), /acceso denegado/i);
  assert.throws(() => api.getConfig({languageCode:'fr', configParams:params}), /Access denied/);
});

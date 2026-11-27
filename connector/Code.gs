// Installation.gs pins the company-owned gateway and its reporting-only identity.
const cc = DataStudioApp.createCommunityConnector();
const keyProperty = 'mlc.reportingKey';
let invocationIdToken;

function fail_(message) {
  cc.newUserError().setText(message).throwException();
}

// Only fixed stage labels, numeric HTTP statuses, and reviewed reason codes reach the log.
function authWarning_(stage, status, reason) {
  console.warn('reporting_auth stage=' + stage + ' status=' + status + ' reason=' + reason);
}

function iamReason_(response) {
  const known = ['SERVICE_DISABLED', 'USER_PROJECT_DENIED', 'IAM_PERMISSION_DENIED',
    'ACCESS_TOKEN_SCOPE_INSUFFICIENT', 'BILLING_DISABLED', 'CONSUMER_INVALID', 'RATE_LIMIT_EXCEEDED'];
  try {
    const details = JSON.parse(response.getContentText()).error.details;
    const info = Array.isArray(details) && details.find(detail =>
      detail && detail['@type'] === 'type.googleapis.com/google.rpc.ErrorInfo' && known.includes(detail.reason));
    if (info) return info.reason;
  } catch (_) {
    // The response may be HTML, empty, or a different Google error envelope.
  }
  return 'HTTP_ERROR';
}

function request_(stage, url, headers, data) {
  const options = {headers, escaping: false, followRedirects: false, muteHttpExceptions: true};
  if (data !== undefined) {
    options.method = 'post';
    options.contentType = 'application/json';
    options.payload = JSON.stringify(data);
  }
  try {
    return UrlFetchApp.fetch(url, options);
  } catch (_) {
    authWarning_(stage, 0, 'FETCH_FAILED');
    fail_('Apps Script could not contact ' + stage + '. Check the deployed URL allowlist and HTTPS fetch permission.');
  }
}

function gateway_(path, data, key) {
  if (!invocationIdToken) {
    const url = 'https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/' + MLC_INVOKER + ':generateIdToken';
    // package.py validates and pins the complete service-account email in Installation.gs.
    const project = MLC_INVOKER.split('@')[1].split('.')[0];
    const response = request_('iam', url, {
      Authorization: 'Bearer ' + ScriptApp.getOAuthToken(),
      'x-goog-user-project': project
    }, {audience: MLC_GATEWAY_URL, includeEmail: true});
    if (response.getResponseCode() !== 200) {
      const status = response.getResponseCode();
      const reason = iamReason_(response);
      authWarning_('iam', status, reason);
      fail_('Google could not authorize the reporting identity (HTTP ' + status + ', ' + reason +
        '). Ask the operator to check the reporting allowlist and IAM Credentials API.');
    }
    invocationIdToken = JSON.parse(response.getContentText()).token;
  }
  return request_('gateway', MLC_GATEWAY_URL + '/api/v1/' + path, {
    'X-Serverless-Authorization': 'Bearer ' + invocationIdToken,
    Authorization: 'Bearer ' + key
  }, data);
}

function read_(path, data) {
  const key = PropertiesService.getUserProperties().getProperty(keyProperty);
  if (!key) fail_('Authorize this connector with a reporting token.');
  const response = gateway_(path, data, key);
  const status = response.getResponseCode();
  if (status === 401 || status === 403) fail_('Access denied. Check the reporting token and Cloud Run invoker permissions.');
  if (status === 404) fail_('The selected connection or namespace is unavailable. Reconfigure the data source.');
  if (status === 413) fail_('This report exceeds a gateway limit. Select fewer fields or a shorter date range.');
  if (status !== 200) fail_('The reporting gateway returned HTTP ' + status + '.');
  return JSON.parse(response.getContentText());
}

function getAuthType() {
  return cc.newAuthTypeResponse().setAuthType(cc.AuthType.KEY).build();
}

function validKey_(key) {
  if (typeof key !== 'string' || !/^mlc_[0-9a-f]{32}\.[A-Za-z0-9_-]{43}$/.test(key)) return false;
  const response = gateway_('connections', undefined, key);
  const status = response.getResponseCode();
  if (status !== 200) authWarning_('gateway', status, 'HTTP_ERROR');
  if (status === 401 || status === 403) return false;
  if (status !== 200) fail_('Could not verify access. The reporting gateway returned HTTP ' + status + '.');
  return Array.isArray(JSON.parse(response.getContentText()).connections);
}

function setCredentials(request) {
  if (!validKey_(request.key)) return {errorCode: 'INVALID_CREDENTIALS'};
  PropertiesService.getUserProperties().setProperty(keyProperty, request.key);
  return {errorCode: 'NONE'};
}

function isAuthValid() {
  return validKey_(PropertiesService.getUserProperties().getProperty(keyProperty));
}

function resetAuth() {
  PropertiesService.getUserProperties().deleteProperty(keyProperty);
}

function configValues_(saved) {
  const params = {};
  for (const id of ['connectionId', 'database', 'collection', 'dateField']) {
    if (saved && saved[id]) params[id] = decodeURIComponent(saved[id]);
  }
  return params;
}

function namespace_(params) {
  return {connectionId: params.connectionId, database: params.database, collection: params.collection};
}

function selectableDate_(field) {
  return field.type === 'YEAR_MONTH_DAY_SECOND' && !field.name.includes('\\') &&
    field.name.split('.').every(part => part && !part.startsWith('$'));
}

function getConfig(request) {
  const params = configValues_(request.configParams);
  const config = cc.getConfig();
  config.setIsSteppedConfig(true);
  function choose(id, label, values, dynamic) {
    if (!values.length) fail_('No available options for ' + label + '. Check the token grants and source data.');
    const select = config.newSelectSingle().setId(id).setName(label).setIsDynamic(dynamic);
    values.forEach(value => select.addOption(config.newOptionBuilder().setLabel(value).setValue(encodeURIComponent(value))));
    if (params[id] && !values.includes(params[id])) fail_('The saved ' + label + ' is unavailable. Reconfigure the data source.');
    return Boolean(params[id]);
  }
  const connections = read_('connections').connections;
  if (!choose('connectionId', 'Connection', connections, true)) return config.build();
  const databases = read_('connections/' + encodeURIComponent(params.connectionId) + '/databases').databases;
  if (!choose('database', 'Database', databases, true)) return config.build();
  const collections = read_('collections?connectionId=' + encodeURIComponent(params.connectionId) +
    '&database=' + encodeURIComponent(params.database)).collections;
  if (!choose('collection', 'Collection', collections, true)) return config.build();
  const fields = read_('schema', namespace_(params)).fields;
  if (!fields.length) fail_('The collection has no sampled fields. Choose a collection containing documents.');
  const dates = fields.filter(selectableDate_).map(field => field.name);
  if (dates.length) {
    const selected = choose('dateField', 'Report date field (UTC)', dates, dates.length > 1);
    if (dates.length > 1 && !selected) return config.build();
  }
  config.setDateRangeRequired(dates.length > 0);
  config.setIsSteppedConfig(false);
  return config.build();
}

// Looker field IDs permit ASCII letters, digits and underscores. Keep original labels.
function schemaField_(field) {
  let name = field.name;
  if (!/^[A-Za-z0-9_]+$/.test(name) || name.startsWith('mlc_hash_')) {
    const digest = Utilities.computeDigest(Utilities.DigestAlgorithm.SHA_256, name, Utilities.Charset.UTF_8);
    name = 'mlc_hash_' + digest.map(b => ('0' + (b & 255).toString(16)).slice(-2)).join('');
  }
  const dataType = {TEXT: 'STRING', YEAR_MONTH_DAY_SECOND: 'STRING', NUMBER: 'NUMBER', BOOLEAN: 'BOOLEAN'}[field.type];
  if (!dataType) fail_('Unsupported field type. Refresh the data source schema.');
  return {name, label: field.name, dataType, semantics: {
    conceptType: field.type === 'NUMBER' ? 'METRIC' : 'DIMENSION', semanticType: field.type
  }};
}

function getSchema(request) {
  const fields = read_('schema', namespace_(configValues_(request.configParams))).fields;
  return {schema: fields.map(schemaField_)};
}

function getData(request) {
  const params = configValues_(request.configParams);
  const body = namespace_(params);
  const fields = read_('schema', body).fields;
  const schemaById = new Map(fields.map(field => {
    const schema = schemaField_(field);
    return [schema.name, schema];
  }));
  const schema = request.fields.map(selected => {
    const field = schemaById.get(selected.name);
    if (!field) fail_('A requested field is unavailable. Refresh the data source schema.');
    return field;
  });
  // Looker can split wide charts into batches of fields. Keep row order stable.
  body.pipeline = [{$sort: {_id: 1}}];
  body.fields = schema.map(field => field.label);
  const dateFields = fields.filter(selectableDate_);
  const dateField = params.dateField || (request.dateRange && dateFields.length === 1 && dateFields[0].name);
  if (dateField) {
    if (!dateFields.some(f => f.name === dateField) || !request.dateRange) {
      fail_('Refresh the data source and select an available date field and report date range.');
    }
    body.dateRange = {field: dateField, startDate: request.dateRange.startDate, endDate: request.dateRange.endDate};
  } else if (request.dateRange && dateFields.length) {
    fail_('Choose a report date field in the data source configuration.');
  }
  const result = read_('query', body);
  const rows = result.rows.map(row => ({values: row.map((value, i) => {
    if (value === null) return null;
    const type = schema[i].dataType;
    if (type === 'STRING') return String(value);
    if (typeof value !== type.toLowerCase()) fail_('A field changed type. Refresh the data source schema.');
    return value;
  })}));
  return {schema, rows, filtersApplied: false};
}

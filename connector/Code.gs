// Installation.gs describes the HTTPS gateways registered for this deployment.
const cc = DataStudioApp.createCommunityConnector();
const keyProperty = 'mlc.reportingKey';
const noSourceDate = '$none'; // MongoDB date paths cannot start with '$'.
let invocationIdToken;
// Apps Script can evaluate Code.gs before Installation.gs.
function usesGatewayURL_() {
  return typeof MLC_GATEWAY_ORIGINS !== 'undefined';
}
const activeGateway = 'mlc.activeGateway';
let selectedGateway;

function registeredOrigin_(path) {
  if (typeof path !== 'string') return undefined;
  const origin = path.trim().replace(/\/$/, '').toLowerCase();
  return MLC_GATEWAY_ORIGINS.includes(origin) ? origin : undefined;
}

function originKey_(origin) {
  return 'mlc.reportingKey:' + origin;
}

const english_ = (en, es) => en;
const spanish_ = (en, es) => es;

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

function request_(stage, url, headers, data, text) {
  const options = {headers, escaping: false, followRedirects: false, muteHttpExceptions: true};
  if (data !== undefined) {
    options.method = 'post';
    options.contentType = 'application/json';
    options.payload = typeof data === 'string' ? data : JSON.stringify(data);
  }
  try {
    return UrlFetchApp.fetch(url, options);
  } catch (_) {
    authWarning_(stage, 0, 'FETCH_FAILED');
    fail_(text('Apps Script could not contact ' + stage + '. Check the deployed URL allowlist and HTTPS fetch permission.',
      'Apps Script no pudo contactar con ' + stage + '. Comprueba las URL permitidas y el permiso para hacer solicitudes HTTPS.'));
  }
}

function gateway_(path, data, key, text = english_, origin = selectedGateway) {
  if (usesGatewayURL_()) {
    if (!origin || !MLC_GATEWAY_ORIGINS.includes(origin)) fail_(text('Select a registered gateway.', 'Selecciona una pasarela registrada.'));
    return request_('gateway', origin + '/api/v1/' + path, {'X-MLC-Token': key}, data, text);
  }
  if (!invocationIdToken) {
    const url = 'https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/' + MLC_INVOKER + ':generateIdToken';
    // package.py validates and pins the complete service-account email in Installation.gs.
    const project = MLC_INVOKER.split('@')[1].split('.')[0];
    const response = request_('iam', url, {
      Authorization: 'Bearer ' + ScriptApp.getOAuthToken(),
      'x-goog-user-project': project
    }, {audience: MLC_GATEWAY_URL, includeEmail: true}, text);
    if (response.getResponseCode() !== 200) {
      const status = response.getResponseCode();
      const reason = iamReason_(response);
      authWarning_('iam', status, reason);
      const details = ' (HTTP ' + status + ', ' + reason + '). ';
      fail_(text('Google could not authorize the reporting identity' + details + 'Ask the operator to check the reporting allowlist and IAM Credentials API.',
        'Google no pudo autorizar la identidad de los informes' + details + 'Pide al administrador que revise los permisos de acceso y la API IAM Credentials.'));
    }
    invocationIdToken = JSON.parse(response.getContentText()).token;
  }
  return request_('gateway', MLC_GATEWAY_URL + '/api/v1/' + path, {
    'X-Serverless-Authorization': 'Bearer ' + invocationIdToken,
    Authorization: 'Bearer ' + key
  }, data, text);
}

function read_(path, data, pipeline, {optionalSourceSchema = false, text = english_} = {}) {
  const key = PropertiesService.getUserProperties().getProperty(usesGatewayURL_() ? originKey_(selectedGateway) : keyProperty);
  if (!key) fail_(text('Authorize this connector with a reporting token.',
    'Autoriza este conector con un token de acceso a los informes.'));
  // pipeline is a validated JSON array. Preserve BSON int64 literals and compound sort key order.
  if (pipeline !== undefined) data = JSON.stringify(data).slice(0, -1) + ',"pipeline":' + pipeline + '}';
  const response = gateway_(path, data, key, text);
  const status = response.getResponseCode();
  if (usesGatewayURL_() && (status === 401 || status === 403)) fail_(text('Access denied. Check the reporting token for this gateway.',
    'Acceso denegado. Comprueba el token de acceso para esta pasarela.'));
  if (status === 401 || status === 403) fail_(text('Access denied. Check the reporting token and Cloud Run invoker permissions.',
    'Acceso denegado. Comprueba el token de acceso y los permisos para invocar el servicio de Cloud Run.'));
  if (status === 404) fail_(text('The selected connection or namespace is unavailable. Reconfigure the data source.',
    'El origen seleccionado ya no está disponible. Vuelve a configurar la fuente de datos.'));
  // A narrow custom projection can fit even when the unprojected sample exceeds schema limits.
  if (status === 413 && optionalSourceSchema) return {fields: []};
  if (pipeline !== undefined && status === 400) fail_(text('The gateway rejected the aggregation pipeline. Check the read-only stages, operators and field names.',
    'La pasarela rechazó el pipeline de agregación. Revisa las etapas de solo lectura, los operadores y los nombres de los campos.'));
  if (pipeline !== undefined && status === 413) fail_(text('The aggregation exceeds a gateway limit. Shorten the pipeline or reduce its output.',
    'La agregación supera un límite de la pasarela. Acorta el pipeline o reduce el resultado.'));
  if (pipeline !== undefined && status === 502) fail_(text('MongoDB could not run the aggregation. Check the stage arguments and source field types.',
    'MongoDB no pudo ejecutar la agregación. Revisa los argumentos de las etapas y los tipos de los campos de origen.'));
  if (status === 413) fail_(text('This report exceeds a gateway limit. Select fewer fields or a shorter date range.',
    'El informe supera un límite de la pasarela. Selecciona menos campos o un intervalo de fechas más corto.'));
  if (status !== 200) fail_(text('The reporting gateway returned HTTP ', 'La pasarela de informes devolvió HTTP ') + status + '.');
  return JSON.parse(response.getContentText());
}

function getAuthType() {
  if (usesGatewayURL_()) return cc.newAuthTypeResponse().setAuthType(cc.AuthType.PATH_KEY).build();
  return cc.newAuthTypeResponse().setAuthType(cc.AuthType.KEY)
    .setHelpUrl('https://github.com/Hexcss/mongo-looker-connector/blob/167f44fa033a023a149e401f98d9b25bf2ce1f94/docs/looker-studio.md#sign-in-with-a-reporting-token')
    .build();
}

function validKey_(key, origin) {
  if (typeof key !== 'string' || !/^mlc_[0-9a-f]{32}\.[A-Za-z0-9_-]{43}$/.test(key)) return false;
  const response = gateway_('connections', undefined, key, english_, origin);
  const status = response.getResponseCode();
  if (status !== 200) authWarning_('gateway', status, 'HTTP_ERROR');
  if (status === 401 || status === 403) return false;
  if (status !== 200) fail_('Could not verify access. The reporting gateway returned HTTP ' + status + '.');
  return Array.isArray(JSON.parse(response.getContentText()).connections);
}

function setCredentials(request) {
  if (usesGatewayURL_()) {
    const creds = request.pathKey || {};
    const origin = registeredOrigin_(creds.path);
    if (!origin || !validKey_(creds.key, origin)) return {errorCode: 'INVALID_CREDENTIALS'};
    const properties = PropertiesService.getUserProperties();
    properties.setProperty(originKey_(origin), creds.key);
    properties.setProperty(activeGateway, origin);
    return {errorCode: 'NONE'};
  }
  if (!validKey_(request.key)) return {errorCode: 'INVALID_CREDENTIALS'};
  PropertiesService.getUserProperties().setProperty(keyProperty, request.key);
  return {errorCode: 'NONE'};
}

function isAuthValid() {
  if (usesGatewayURL_()) {
    const props = PropertiesService.getUserProperties();
    const recent = registeredOrigin_(props.getProperty(activeGateway));
    // Google supplies no data source configuration here. One expired credential
    // must not lock out data sources bound to another usable gateway.
    const candidates = [recent, ...MLC_GATEWAY_ORIGINS.filter(origin => origin !== recent)];
    let unavailable;
    for (const origin of candidates) {
      if (!origin) continue;
      const key = props.getProperty(originKey_(origin));
      if (!key) continue;
      try {
        if (validKey_(key, origin)) return true;
      } catch (err) {
        unavailable = err;
      }
    }
    if (unavailable) throw unavailable;
    return false;
  }
  return validKey_(PropertiesService.getUserProperties().getProperty(keyProperty));
}

function resetAuth() {
  if (usesGatewayURL_()) {
    const props = PropertiesService.getUserProperties();
    for (const name of props.getKeys()) if (name.startsWith('mlc.reportingKey:')) props.deleteProperty(name);
    props.deleteProperty(activeGateway);
    return;
  }
  PropertiesService.getUserProperties().deleteProperty(keyProperty);
}

function configValues_(saved, text = english_) {
  const params = {};
  for (const id of ['gatewayOrigin', 'connectionId', 'database', 'collection', 'dateField', 'sourceDateField', 'queryMode']) {
    if (saved && saved[id]) {
      try {
        params[id] = decodeURIComponent(saved[id]);
      } catch (_) {
        fail_(text('A saved selection is invalid. Reconfigure the data source.', 'Una opción guardada no es válida. Vuelve a configurar la fuente de datos.'));
      }
    }
  }
  if (params.connectionId === '@datasets') {
    if (params.database && params.database !== 'published') {
      fail_(text('The selected database is unavailable for published datasets.', 'La base de datos seleccionada no está disponible para conjuntos de datos publicados.'));
    }
    params.database = 'published';
  }
  if (params.queryMode && !['collection', 'custom'].includes(params.queryMode)) fail_(text('Choose an available query mode.',
    'Selecciona un modo de consulta disponible.'));
  if (params.queryMode === 'custom') {
    if (params.connectionId === '@datasets') fail_(text('Published datasets use the pipeline configured by their administrator.',
      'Los conjuntos de datos publicados utilizan el pipeline configurado por su administrador.'));
    params.pipeline = saved.pipeline;
  }
  return params;
}

function pipeline_(params, text = english_) {
  if (params.queryMode !== 'custom') return undefined;
  let valid = false;
  try {
    valid = typeof params.pipeline === 'string' && Array.isArray(JSON.parse(params.pipeline));
  } catch (_) {
    // Keep query text out of error messages.
  }
  if (!valid) fail_(text('Enter the MongoDB aggregation pipeline as a JSON array of stages.',
    'Introduce el pipeline de agregación de MongoDB como un array JSON de etapas.'));
  return params.pipeline;
}

function namespace_(params) {
  return {connectionId: params.connectionId, database: params.database, collection: params.collection};
}

function selectableDate_(field) {
  return field.type === 'YEAR_MONTH_DAY_SECOND' && !field.name.includes('\\') &&
    field.name.split('.').every(part => part && !part.startsWith('$'));
}

function sourceDates_(params, optional = false, text = english_) {
  return read_('schema', namespace_(params), undefined, {optionalSourceSchema: optional, text}).fields.filter(selectableDate_).map(field => field.name);
}

function getConfig(request) {
  const text = /^es(?:[-_]|$)/i.test(request.languageCode || '') ? spanish_ : english_;
  const params = configValues_(request.configParams, text);
  const published = params.connectionId === '@datasets';
  const config = cc.getConfig();
  config.setIsSteppedConfig(true);

  function read(path, data, pipeline) {
    return read_(path, data, pipeline, {text});
  }
  function choose(id, label, values, dynamic, help, display = value => value) {
    if (!values.length) fail_(text('No options available for ' + label + '. Check the token grants and source data.',
      'No hay opciones disponibles para «' + label + '». Comprueba los permisos del token y los datos de origen.'));
    const select = config.newSelectSingle().setId(id).setName(label).setIsDynamic(dynamic);
    if (help) select.setHelpText(help);
    values.forEach(value => select.addOption(config.newOptionBuilder().setLabel(display(value)).setValue(encodeURIComponent(value))));
    if (params[id] && !values.includes(params[id])) fail_(text('The saved selection for ' + label + ' is unavailable. Reconfigure the data source.',
      'La opción guardada para «' + label + '» ya no está disponible. Vuelve a configurar la fuente de datos.'));
    return Boolean(params[id]);
  }
  function finish(fields, dateRequired) {
    const count = fields.length;
    const kind = published
      ? text(count === 1 ? 'published field' : 'published fields', count === 1 ? 'campo publicado' : 'campos publicados')
      : text(count === 1 ? 'sampled field' : 'sampled fields', count === 1 ? 'campo de la muestra' : 'campos de la muestra');
    const names = fields.slice(0, 5).map(field => {
      const chars = Array.from(field.name.replace(/\s+/g, ' '));
      return chars.length <= 48 ? chars.join('') : chars.slice(0, 47).join('') + '…';
    });
    if (count > names.length) names.push('…');
    config.newInfo().setId('fieldSummary').setText(count + ' ' + kind + ' · ' + names.join(', '));
    config.setDateRangeRequired(dateRequired);
    config.setIsSteppedConfig(false);
    return config.build();
  }

  if (usesGatewayURL_()) {
    const props = PropertiesService.getUserProperties();
    const origins = MLC_GATEWAY_ORIGINS.filter(origin => props.getProperty(originKey_(origin)));
    if (!choose('gatewayOrigin', text('Reporting gateway', 'Pasarela de informes'), origins, true)) return config.build();
    selectedGateway = params.gatewayOrigin;
  }
  const connections = read('connections').connections;
  if (!choose('connectionId', text('Data source', 'Origen de datos'), connections, true,
    text('Your reporting token determines which connections and published datasets you can use.',
      'Tu token de acceso determina qué conexiones y conjuntos de datos publicados puedes utilizar.'),
    id => id === '@datasets' ? text('Published datasets', 'Conjuntos de datos publicados') : id)) return config.build();
  if (!published) {
    const databases = read('connections/' + encodeURIComponent(params.connectionId) + '/databases').databases;
    if (!choose('database', text('Database', 'Base de datos'), databases, true)) return config.build();
  }
  const collections = read('collections?connectionId=' + encodeURIComponent(params.connectionId) +
    '&database=' + encodeURIComponent(params.database)).collections;
  const collectionLabel = published ? text('Published dataset', 'Conjunto de datos publicado') : text('Collection', 'Colección');
  if (!choose('collection', collectionLabel, collections, true, published && text(
    'Your administrator maintains the pipeline and grants access to its fields.',
    'El administrador mantiene el pipeline y concede acceso a sus campos.'))) return config.build();
  if (!published && !choose('queryMode', text('Query mode', 'Modo de consulta'), ['collection', 'custom'], true,
    text('Use the fields in your collection documents, or shape the results with a read-only pipeline.',
      'Utiliza los campos de los documentos de la colección o define el resultado con un pipeline de solo lectura.'),
    mode => mode === 'collection' ? text('Collection documents', 'Documentos de la colección') :
      text('Custom aggregation', 'Agregación personalizada'))) return config.build();
  if (params.queryMode === 'custom') {
    config.newTextArea().setId('pipeline')
      .setName(text('MongoDB aggregation pipeline (JSON)', 'Pipeline de agregación de MongoDB (JSON)'))
      .setIsDynamic(true).setAllowOverride(false)
      .setHelpText(text('Enter a JSON array of stages. The example returns the ten highest amounts and uses _id to order ties.',
        'Introduce un array JSON de etapas. El ejemplo devuelve los diez importes más altos y usa _id para ordenar los empates.'))
      .setPlaceholder('[\n  {"$sort": {"amount": -1, "_id": 1}},\n  {"$limit": 10}\n]');
    if (params.pipeline === undefined || (typeof params.pipeline === 'string' && !params.pipeline.trim())) return config.build();
    const output = read('schema', namespace_(params), pipeline_(params, text)).fields;
    if (!output.length) fail_(text('The pipeline returned no sampled fields. Edit it to return at least one row with fields.',
      'La muestra del pipeline no contiene campos. Modifica las etapas para que devuelvan al menos un documento con campos.'));
    const filterByDate = Boolean(params.sourceDateField && params.sourceDateField !== noSourceDate);
    const dates = sourceDates_(params, !filterByDate, text);
    if (dates.length || filterByDate) {
      if (!choose('sourceDateField', text('Filter source rows by date (UTC)', 'Filtrar documentos de origen por fecha (UTC)'), [noSourceDate, ...dates], true,
        text('The report date range filters this source field before the pipeline runs.',
          'El intervalo de fechas del informe se aplica a este campo de origen antes de ejecutar el pipeline.'),
        name => name === noSourceDate ? text('No date filter', 'Sin filtro de fecha') : name)) return config.build();
    }
    return finish(output, filterByDate);
  }
  const fields = read('schema', namespace_(params)).fields;
  if (!fields.length) {
    if (published) fail_(text('No fields are available for this dataset. Ask the administrator to check your field grants.',
      'Este conjunto de datos no tiene campos disponibles. Pide al administrador que revise tus permisos de acceso a los campos.'));
    fail_(text('The collection has no sampled fields. Choose a collection containing documents.',
      'La muestra de la colección no contiene campos. Selecciona una colección que contenga documentos.'));
  }
  const dates = fields.filter(selectableDate_).map(field => field.name);
  if (published && usesGatewayURL_()) {
    if (dates.length) {
      choose('dateField', text('Report date field (UTC)', 'Campo de fecha del informe (UTC)'), [noSourceDate, ...dates], true,
        text('Optionally filter the saved results by a report date range.', 'Puedes filtrar los resultados guardados por un intervalo de fechas.'),
        name => name === noSourceDate ? text('No date filter', 'Sin filtro de fecha') : name);
    }
    return finish(fields, Boolean(params.dateField && params.dateField !== noSourceDate && dates.includes(params.dateField)));
  }
  if (dates.length) {
    const help = published
      ? text('The report date range filters the published results after the saved pipeline runs.',
        'El intervalo de fechas del informe filtra los resultados publicados después de ejecutar el pipeline guardado.')
      : text('The report date range filters documents by this BSON date field.',
        'El intervalo de fechas del informe filtra los documentos según este campo de fecha BSON.');
    const selected = choose('dateField', text('Report date field (UTC)', 'Campo de fecha del informe (UTC)'), dates, dates.length > 1, help);
    if (dates.length > 1 && !selected) return config.build();
  }
  return finish(fields, dates.length > 0);
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
  const params = configValues_(request.configParams);
  if (usesGatewayURL_()) selectedGateway = registeredOrigin_(params.gatewayOrigin);
  const fields = read_('schema', namespace_(params), pipeline_(params)).fields;
  return {schema: fields.map(schemaField_)};
}

function getData(request) {
  const params = configValues_(request.configParams);
  if (usesGatewayURL_()) selectedGateway = registeredOrigin_(params.gatewayOrigin);
  const body = namespace_(params);
  const pipeline = pipeline_(params);
  const fields = read_('schema', body, pipeline).fields;
  const schemaById = new Map(fields.map(field => {
    const schema = schemaField_(field);
    return [schema.name, schema];
  }));
  const schema = request.fields.map(selected => {
    const field = schemaById.get(selected.name);
    if (!field) fail_('A requested field is unavailable. Refresh the data source schema.');
    return field;
  });
  // Looker may batch fields. The author controls ordering in custom aggregations.
  const workspaceDataset = usesGatewayURL_() && params.connectionId === '@datasets';
  if (pipeline === undefined && !workspaceDataset) body.pipeline = [{$sort: {_id: 1}}];
  body.fields = schema.map(field => field.label);
  let dateField;
  if (pipeline !== undefined) {
    if (params.sourceDateField && params.sourceDateField !== noSourceDate) {
      dateField = params.sourceDateField;
      if (!request.dateRange || !sourceDates_(params).includes(dateField)) {
        fail_('Refresh the data source and select an available source date field and report date range.');
      }
    }
  } else if (!workspaceDataset || (params.dateField && params.dateField !== noSourceDate)) {
    const dates = fields.filter(selectableDate_).map(field => field.name);
    dateField = params.dateField || (request.dateRange && dates.length === 1 && dates[0]);
    if (dateField && (!dates.includes(dateField) || !request.dateRange)) {
      fail_('Refresh the data source and select an available date field and report date range.');
    }
    if (!dateField && request.dateRange && dates.length) fail_('Choose a report date field in the data source configuration.');
  }
  if (dateField) {
    body.dateRange = {field: dateField, startDate: request.dateRange.startDate, endDate: request.dateRange.endDate};
  }
  const result = read_('query', body, pipeline);
  const rows = result.rows.map(row => ({values: row.map((value, i) => {
    if (value === null) return null;
    const type = schema[i].dataType;
    if (type === 'STRING') return String(value);
    if (typeof value !== type.toLowerCase()) fail_('A field changed type. Refresh the data source schema.');
    return value;
  })}));
  return {schema, rows, filtersApplied: false};
}

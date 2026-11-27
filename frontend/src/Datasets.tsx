import { useRef, useState } from 'react';
import { api, message, useList } from './api';
import type { Connection, Dataset, DatasetField, Result } from './api';
import { ActionForm, Alert, Empty, Icon, PageHeading } from './ui';

const types = ['TEXT', 'NUMBER', 'BOOLEAN', 'YEAR_MONTH_DAY_SECOND'];

function DatasetEditor({ items, original, close, saved }: {
  items: Connection[]; original?: Dataset; close: () => void; saved: (id: string) => void
}) {
  const [id, setID] = useState(original?.id ?? '');
  const [sourceID, setSource] = useState(original?.connectionId ?? items[0]?.id ?? '');
  const source = items.find(item => item.id === sourceID);
  const databases = Object.keys(source?.databases ?? {});
  const [database, setDatabase] = useState(original?.database ?? databases[0] ?? '');
  const collections = source?.databases[database] ?? [];
  const [collection, setCollection] = useState(original?.collection ?? collections[0] ?? '');
  const [pipeline, setPipeline] = useState(original?.pipelineText ?? '[]');
  const [rows, setRows] = useState((original?.fields ?? [{name: '', type: 'TEXT'}]).map((field, key) => ({...field, key})));
  const nextKey = useRef(rows.length);
  function changeField(key: number, update: Partial<DatasetField>) {
    setRows(current => current.map(row => row.key === key ? {...row, ...update} : row));
  }
  return <ActionForm title={original ? 'Edit dataset' : 'Publish dataset'} close={close} confirm={original ? 'Republish dataset' : 'Publish dataset'} submit={async () => {
    if (!collections.includes(collection)) throw new Error('Choose an available source collection.');
    try { if (!Array.isArray(JSON.parse(pipeline))) throw new Error(); }
    catch { throw new Error('Enter the saved pipeline as a JSON array.'); }
    const fields = rows.map(({ name, type }) => ({ name, type }));
    if (new Set(fields.map(field => field.name)).size !== fields.length) throw new Error('Use a different name for each published field.');
    const params = { ...(original ? {revision: original.revision} : {id}), connectionId: sourceID, database, collection, fields };
    // Forward the original pipeline so every numeric literal and compound sort key stays intact.
    await api(original ? 'datasets/' + encodeURIComponent(original.id) : 'datasets', original ? 'PUT' : 'POST',
      JSON.stringify(params).slice(0, -1) + ',"pipeline":' + pipeline + '}');
    saved(id);
  }}>
    <label>Dataset ID<input autoFocus required pattern="[a-zA-Z0-9_\-]{1,64}" maxLength={64} disabled={!!original} value={id} onChange={e => setID(e.target.value)} placeholder="e.g. daily_sales" /></label>
    <div className="selectors">
      <label>Connection<select required value={sourceID} onChange={e => {
        const selected = items.find(item => item.id === e.target.value)!;
        const db = Object.keys(selected.databases)[0] ?? '';
        setSource(selected.id); setDatabase(db); setCollection(selected.databases[db]?.[0] ?? '');
      }}>{!source && <option disabled value={sourceID}>{sourceID || 'Choose a connection'}</option>}{items.map(item => <option key={item.id}>{item.id}</option>)}</select></label>
      <label>Database<select required value={database} onChange={e => {
        const db = e.target.value; setDatabase(db); setCollection(source?.databases[db]?.[0] ?? '');
      }}>{!databases.includes(database) && <option disabled value={database}>{database || 'Choose a database'}</option>}{databases.map(db => <option key={db}>{db}</option>)}</select></label>
      <label>Collection<select required value={collection} onChange={e => setCollection(e.target.value)}>{!collections.includes(collection) && <option disabled value={collection}>{collection || 'Choose a collection'}</option>}{collections.map(coll => <option key={coll}>{coll}</option>)}</select></label>
    </div>
    <label>Saved pipeline<textarea required rows={5} className="code-editor" value={pipeline} spellCheck={false} onChange={e => setPipeline(e.target.value)} /></label>
    <div className="field-heading"><h3>Published schema</h3><button className="text-button" type="button" onClick={() => setRows([...rows, {key: nextKey.current++, name: '', type: 'TEXT'}])}><Icon name="plus" />Add field</button></div>
    <p className="field-note">Declare the output fields that reporting tokens can access.</p>
    {rows.map(row => <div className="schema-row" key={row.key}>
      <label>Field name<input required value={row.name} spellCheck={false} onChange={e => changeField(row.key, {name: e.target.value})} /></label>
      <label>Field type<select value={row.type} onChange={e => changeField(row.key, {type: e.target.value})}>{types.map(type => <option key={type}>{type}</option>)}</select></label>
      <button type="button" className="icon-button" aria-label={'Remove field ' + (row.name || 'row')} disabled={rows.length < 2} onClick={() => setRows(rows.filter(field => field.key !== row.key))}><Icon name="close" /></button>
    </div>)}
    {original && <p className="hint"><Icon name="lock" />Republishing invalidates existing grants for this dataset. Issue new grants for the updated definition.</p>}
  </ActionForm>;
}

function DatasetPreview({ source }: {source: Dataset}) {
  const [filters, setFilters] = useState('');
  const [result, setResult] = useState<Result | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  async function run() {
    setBusy(true); setError(''); setResult(null);
    try {
      const raw = filters.trim() || '{}';
      const value = JSON.parse(raw);
      if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Enter equality filters as a JSON object.');
      const params = { connectionId: '@datasets', database: 'published', collection: source.id, fields: source.fields.map(field => field.name) };
      const body = JSON.stringify(params).slice(0, -1) + ',"filters":' + raw + '}';
      setResult(await (await api('preview', 'POST', body)).json());
    } catch (err) { setError(message(err)); }
    finally { setBusy(false); }
  }
  return <details className="dataset-preview"><summary>Preview rows</summary><div className="dataset-preview-controls">
    <label>Equality filters<textarea rows={2} spellCheck={false} disabled={busy} placeholder={'{}'} value={filters} onChange={e => { setFilters(e.target.value); setResult(null); setError(''); }} /></label>
    <button type="button" className="secondary" disabled={busy} onClick={run}><Icon name="play" />{busy ? 'Running…' : 'Run dataset preview'}</button>
    {error && <Alert>{error}</Alert>}
    {result && <><p role="status" className="mode">{result.rows.length} preview {result.rows.length === 1 ? 'row' : 'rows'}</p>
      <div className="table-scroll" tabIndex={0} role="region" aria-label="Dataset preview"><table><thead><tr>{result.fields.map(field => <th scope="col" key={field}>{field}</th>)}</tr></thead>
        <tbody>{result.rows.map((row, index) => <tr key={index}>{row.map((value, i) => <td key={i}>{value === null ? <em>null</em> : String(value)}</td>)}</tr>)}</tbody></table></div></>}
  </div></details>;
}

export function Datasets({ items, refreshKey }: { items: Connection[]; refreshKey: number }) {
  const [changes, setChanges] = useState(0);
  const { list, error } = useList<Dataset>('datasets', refreshKey + changes);
  const [selected, setSelected] = useState('');
  const [editing, setEditing] = useState<Dataset | 'new' | null>(null);
  const [deleting, setDeleting] = useState<Dataset | null>(null);
  const [notice, setNotice] = useState('');
  const current = list?.find(item => item.id === selected) ?? list?.[0];
  return <>
    <PageHeading title="Managed datasets" description="Publish a saved query and choose the fields available to reports."
      action={<button className="primary" disabled={!items.length} onClick={() => setEditing('new')}><Icon name="plus" />Publish dataset</button>} />
    {error && <Alert>{error} Use Refresh to retry.</Alert>}
    {notice && <p className="notice" role="status">{notice}</p>}
    {!list ? !error && <p className="loading" role="status">Loading datasets…</p> : !current ? <section className="panel"><Empty icon="layers" title="No datasets yet">
      {items.length ? 'Publish a pipeline and its output schema to make it available for reporting.' : <><a href="#connections">Add a connection</a> to choose a source collection.</>}</Empty></section> :
      <div className="inventory"><section className="panel"><div className="panel-header"><h2>Published datasets</h2><span className="counter">{list.length}</span></div>
        <ul className="directory">{list.map(item => <li key={item.id}><button aria-label={item.id} aria-pressed={item.id === current.id} onClick={() => setSelected(item.id)}><span className="icon-box"><Icon name="layers" /></span>
          <span className="directory-name"><strong>{item.id}</strong><small>{item.fields.length} published {item.fields.length === 1 ? 'field' : 'fields'}</small></span><Icon name="right" /></button></li>)}</ul></section>
        <section className="panel detail"><div className="panel-header"><h2>{current.id}</h2></div><div className="detail-content dataset-detail">
          <div><h3>Source collection</h3><code>{current.connectionId} / {current.database} / {current.collection}</code></div>
          <div><h3>Published schema</h3><div className="table-scroll"><table><thead><tr><th scope="col">Field</th><th scope="col">Type</th></tr></thead>
            <tbody>{current.fields.map(field => <tr key={field.name}><td>{field.name}</td><td>{field.type}</td></tr>)}</tbody></table></div></div>
          <details key={current.id}><summary>Saved pipeline</summary><pre className="pipeline-source">{current.pipelineText}</pre></details>
          <DatasetPreview key={current.id + ':' + current.revision} source={current} />
        </div><footer className="detail-actions">
          <button className="secondary" aria-label={'Edit ' + current.id} onClick={() => setEditing(current)}><Icon name="edit" />Edit dataset</button>
          <button className="danger-link" aria-label={'Delete ' + current.id} onClick={() => setDeleting(current)}>Delete dataset</button>
        </footer></section></div>}
    {editing && <DatasetEditor items={items} original={editing === 'new' ? undefined : editing} close={() => setEditing(null)} saved={id => {
      setSelected(id); setEditing(null); setNotice('Published ' + id + '.'); setChanges(n => n + 1);
    }} />}
    {deleting && <ActionForm title="Delete dataset" confirm="Delete dataset" danger close={() => setDeleting(null)} submit={async () => {
      await api('datasets/' + encodeURIComponent(deleting.id), 'DELETE', JSON.stringify({revision: deleting.revision}));
      setNotice('Deleted ' + deleting.id + '.'); setDeleting(null); setChanges(n => n + 1);
    }}><p>Delete <strong>{deleting.id}</strong>? Reports using this dataset will lose access.</p></ActionForm>}
  </>;
}

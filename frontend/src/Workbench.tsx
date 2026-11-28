import { useEffect, useMemo, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api, message } from './api';
import type { Connection, DatasetField, Result } from './api';
import { ActionForm, Alert, Empty, Icon } from './ui';

type Sample = { fields: DatasetField[]; rows: Result['rows'] };
type Source = { connectionId: string; database: string; collection: string };
type Launch = { datasetId: string; url: string };

function pipelineBody(source: Source, pipeline: string, id?: string) {
  let stages: unknown;
  try { stages = JSON.parse(pipeline); } catch { /* Report the input format below. */ }
  if (!Array.isArray(stages)) throw new Error('Enter a JSON array of stages. Start with [] for all documents.');
  // Leave numeric literals, compound key order and duplicate keys untouched for gateway validation.
  return JSON.stringify({ ...source, id }).slice(0, -1) + ',"pipeline":' + pipeline + '}';
}

async function openLooker(request: () => Promise<Response>, saved: (result: Launch) => void) {
  const tab = window.open('about:blank', '_blank');
  if (tab) tab.opener = null;
  try {
    const launch: Launch = await (await request()).json();
    const url = new URL(launch.url);
    if (!['https://lookerstudio.google.com', 'https://datastudio.google.com'].includes(url.origin) || url.username || url.password) {
      throw new Error('The gateway returned an invalid Looker Studio link.');
    }
    saved(launch);
    if (tab && !tab.closed) tab.location.replace(url.href);
  } catch (reason) { tab?.close(); throw reason; }
}

export function SavedLaunch({ id }: { id: string }) {
  const [launch, setLaunch] = useState<Launch | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  return <>{launch ? <a className="secondary" href={launch.url} target="_blank" rel="noopener noreferrer">Open in Looker Studio <Icon name="arrow" /></a> :
    <button className="secondary" disabled={busy} onClick={async () => {
      setBusy(true); setError('');
      try { await openLooker(() => api('workspace/looker/' + encodeURIComponent(id)), setLaunch); }
      catch (err) { setError(message(err)); }
      finally { setBusy(false); }
    }}>Open in Looker Studio <Icon name="arrow" /></button>}
    {error && <Alert>{error}</Alert>}</>;
}

const operations = [
  { op: '$match', title: 'Filter documents', hint: 'Keep documents where a field equals a value.' },
  { op: '$sort', title: 'Sort documents', hint: 'Order documents by a field.' },
  { op: '$project', title: 'Select a field', hint: 'Keep a field in the output, along with the document ID.' },
  { op: '$group', title: 'Group and count', hint: 'Count the documents for each distinct field value.' },
  { op: '$limit', title: 'Limit rows', hint: 'Keep the first rows at this point in the pipeline.' }
];
function StageBuilder({ sample, append }: { sample: Sample | null; append: (stage: string) => void }) {
  const [operation, choose] = useState('$match');
  const [field, setField] = useState<string | null>(null);
  const [value, setValue] = useState('');
  const [direction, setDirection] = useState('-1');
  const [error, setError] = useState('');
  const names = sample?.fields.map(item => item.name) ?? [];
  const name = field ?? names.find(item => item !== '_id') ?? names[0] ?? '';
  const index = names.indexOf(name);
  const example = sample?.rows.find(row => row[index] != null)?.[index];
  const exampleText = example == null ? '' : String(example);
  const kind = sample?.fields[index]?.type;
  const active = operations.find(item => item.op === operation)!;
  function insert(event: FormEvent) {
    event.preventDefault(); setError('');
    try {
      const key = JSON.stringify(name);
      let operand: string;
      if (operation === '$limit') operand = value || '10';
      else if (operation === '$sort') operand = '{ ' + key + ': ' + direction + ' }';
      else if (operation === '$project') operand = '{ ' + key + ': 1 }';
      else if (operation === '$group') operand = '{ "_id": ' + JSON.stringify('$' + name) + ', "count": { "$sum": 1 } }';
      else {
        let literal = value || exampleText;
        if (kind === 'NUMBER') {
          if (!/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(literal)) throw new Error('Enter a JSON number for this numeric field.');
        } else if (kind === 'BOOLEAN') {
          if (!['true', 'false'].includes(literal)) throw new Error('Enter true or false for this field.');
        } else literal = JSON.stringify(literal);
        operand = '{ ' + key + ': ' + literal + ' }';
      }
      append('{ ' + JSON.stringify(operation) + ': ' + operand + ' }');
    } catch (err) { setError(message(err)); }
  }
  return <form className="stage-builder" onSubmit={insert}>
    <h3><Icon name="layers" /> Stage builder</h3>
    <label>Operation<select value={operation} onChange={e => { choose(e.target.value); setValue(''); setError(''); }}>{operations.map(item =>
      <option key={item.op} value={item.op}>{item.title} · {item.op}</option>)}</select></label>
    <p className="stage-hint">{active.hint}</p>
    {operation !== '$limit' && <label>Field<input list="workspace-fields" required value={name} placeholder="Field name" onChange={e => { setField(e.target.value); setValue(''); }} />
      <datalist id="workspace-fields">{names.map(item => <option key={item} value={item} />)}</datalist></label>}
    {operation === '$match' && <label>Equals<input value={value} placeholder={exampleText || 'Value'} required={example == null} onChange={e => setValue(e.target.value)} /></label>}
    {operation === '$sort' && <label>Order<select value={direction} onChange={e => setDirection(e.target.value)}>
      <option value="-1">Descending</option><option value="1">Ascending</option></select></label>}
    {operation === '$limit' && <label>Rows<input type="number" min="1" step="1" value={value} placeholder="10" onChange={e => setValue(e.target.value)} /></label>}
    <button className="secondary add-stage"><Icon name="plus" />Add stage</button>
    {error && <Alert>{error}</Alert>}
  </form>;
}

export function Workbench({ items, initialID, owner }: { items: Connection[]; initialID: string | null; owner: boolean }) {
  const [id, setID] = useState(initialID ?? '');
  const [db, setDB] = useState('');
  const [coll, setColl] = useState('');
  const connection = items.find(item => item.id === id) ?? items[0];
  const databases = Object.keys(connection?.databases ?? {});
  const database = databases.includes(db) ? db : databases[0];
  const collections = connection?.databases[database] ?? [];
  const collection = collections.includes(coll) ? coll : collections[0];
  const [pipeline, setPipeline] = useState('[\n]');
  return <>
    <div className="work-heading"><h1 id="page-title" tabIndex={-1}>Pipeline workspace</h1><p>Build an aggregation and explore its output before connecting a report.</p></div>
    {!connection ? <section className="panel"><Empty icon="database" title="No connections yet">{owner ? <><a href="#connections">Open Connections</a> to add a source.</> : 'Ask a workspace owner to add a source.'}</Empty></section> :
    <><section className="source-bar" aria-label="Data source"><span className="source-mark"><Icon name="database" /></span>
      <label>Connection<select value={connection.id} onChange={e => { setID(e.target.value); setDB(''); setColl(''); }}>{items.map(item => <option key={item.id}>{item.id}</option>)}</select></label>
      <Icon name="right" /><label>Database<select value={database ?? ''} onChange={e => { setDB(e.target.value); setColl(''); }}>{databases.map(name => <option key={name}>{name}</option>)}</select></label>
      <Icon name="right" /><label>Collection<select value={collection ?? ''} onChange={e => setColl(e.target.value)}>{collections.map(name => <option key={name}>{name}</option>)}</select></label>
    </section>{collection ? <Pipeline key={JSON.stringify([connection.id, database, collection])} source={{ connectionId: connection.id, database, collection }} pipeline={pipeline} edit={setPipeline} owner={owner} /> :
      <section className="panel"><Empty icon="folder" title="No collection selected">Choose a connection with an available collection.</Empty></section>}</>}
  </>;
}

function Pipeline({ source, pipeline, edit, owner }: { source: Source; pipeline: string; edit: (value: string) => void; owner: boolean }) {
  const [sample, setSample] = useState<Sample | null>(null);
  const [preview, setPreview] = useState<{ text: string; result: Sample } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [view, setView] = useState<'rows' | 'fields'>('rows');
  const [publish, setPublish] = useState(false);
  const [id, setID] = useState('');
  const [launch, setLaunch] = useState<Launch | null>(null);
  const [sampling, setSampling] = useState(true);
  const [sampleError, setSampleError] = useState('');
  const request = useRef<AbortController | null>(null);
  const editor = useRef<HTMLTextAreaElement>(null);
  const syntax = useRef<HTMLPreElement>(null);
  const highlighted = useMemo(() => pipeline.split(/("(?:\\.|[^"\\])*"|-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b|\b(?:true|false|null)\b)/g).map((token, i) => {
    if (i % 2 === 0) return token;
    const kind = token.startsWith('"$') ? 'operator' : token.startsWith('"') ? 'string' : 'literal';
    return <span className={'syntax-' + kind} key={i}>{token}</span>;
  }), [pipeline]);
  // Parent remounts this editor whenever its namespace changes.
  useEffect(() => {
    const controller = new AbortController();
    api('workspace/preview', 'POST', pipelineBody(source, '[]'), controller.signal).then(r => r.json()).then((data: Sample) => {
      if (!controller.signal.aborted) setSample(data);
    }).catch(err => { if (!controller.signal.aborted) setSampleError(message(err)); })
      .finally(() => { if (!controller.signal.aborted) setSampling(false); });
    return () => { controller.abort(); request.current?.abort(); };
  }, [source.connectionId, source.database, source.collection]);
  const empty = /^\s*\[\s*\]\s*$/.test(pipeline);
  const result = error ? null : preview?.text === pipeline ? preview.result : empty ? sample : null;
  const stageCount = (() => { try { const stages = JSON.parse(pipeline); return Array.isArray(stages) ? stages.length : null; } catch { return null; } })();
  function change(text: string) {
    request.current?.abort(); setBusy(false); setError(''); setLaunch(null); edit(text);
  }
  async function run() {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller; setError(''); setBusy(true);
    try {
      const response = await api('workspace/preview', 'POST', pipelineBody(source, pipeline), controller.signal);
      const data: Sample = await response.json();
      if (!controller.signal.aborted) { setPreview({ text: pipeline, result: data }); if (empty) { setSample(data); setSampleError(''); } }
    } catch (err) { if (!controller.signal.aborted) setError(message(err)); }
    finally { if (!controller.signal.aborted) setBusy(false); }
  }
  function add(stage: string) {
    pipelineBody(source, pipeline);
    const prefix = pipeline.trim().slice(0, -1).trimEnd();
    change(prefix + (stageCount ? ',' : '') + '\n  ' + stage + '\n]');
    editor.current?.focus();
  }
  return <>
    <section className="work-panel"><header className="work-panel-title"><h2><Icon name="terminal" />Aggregation pipeline</h2>
      <span className="mode"><Icon name="lock" />Read only</span></header>
      <div className="editor-split"><div className="editor-column">
        <div className="editor-frame"><div className="editor-caption"><span>pipeline.json</span><span>JSON</span></div>
          <div className="code-surface"><pre className="syntax-view" aria-hidden="true" ref={syntax}>{highlighted}{'\n'}</pre>
            <textarea ref={editor} aria-label="Aggregation pipeline" className="pipeline-input" value={pipeline} spellCheck={false} autoCapitalize="off" autoComplete="off" onChange={e => change(e.target.value)}
              onScroll={e => { if (syntax.current) { syntax.current.scrollTop = e.currentTarget.scrollTop; syntax.current.scrollLeft = e.currentTarget.scrollLeft; } }}
              onKeyDown={e => { if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); void run(); } }} /></div>
          <div className="editor-status">{stageCount === null ? 'Editing JSON' : stageCount + (stageCount === 1 ? ' stage' : ' stages')}</div></div>
        <div className="run-toolbar"><button className="primary" disabled={busy} onClick={run}><Icon name="play" />{busy ? 'Running…' : 'Run preview'}</button><kbd>Ctrl / ⌘ + Enter</kbd></div>
        {error && <Alert>{error}</Alert>}
      </div><StageBuilder sample={result ?? sample} append={add} /></div>
    </section>
    <section className="work-panel output" aria-busy={busy || sampling}><header className="work-panel-title"><h2><Icon name="grid" />Output preview</h2>
      {result && <span className="sample-count" role="status">{result.rows.length} {result.rows.length === 1 ? 'row' : 'rows'} · {result.fields.length} {result.fields.length === 1 ? 'field' : 'fields'}</span>}</header>
      <div className="output-toolbar"><div className="view-switch" role="group" aria-label="Preview view">{(['rows', 'fields'] as const).map(tab =>
        <button key={tab} aria-pressed={tab === view} onClick={() => setView(tab)}>{tab === 'rows' ? 'Rows' : 'Fields'}</button>)}</div>
        {launch ? <a className="secondary" target="_blank" rel="noopener noreferrer" href={launch.url}>Open in Looker Studio <Icon name="arrow" /></a> :
          <button className="secondary launch-button" disabled={!result?.fields.length || busy} onClick={() => setPublish(true)}>Open in Looker Studio <Icon name="arrow" /></button>}</div>
      {launch && <div role="status" className="saved-notice"><p>Saved as <strong>{launch.datasetId}</strong>.</p><p>{owner ? <>Issue a reporting token for this dataset’s fields in <a href="#tokens">Access tokens</a>.</> : 'Ask a workspace owner for a reporting token scoped to this dataset’s fields.'} If Google asks you to authorize the connector, reopen the Looker Studio link after authorization.</p></div>}
      {!result ? <div className="result-note" role="status">{busy || sampling && empty ? 'Loading preview…' : empty && sampleError ? sampleError + ' Use Run preview to retry.' : 'Run preview to see the rows and fields for this pipeline.'}</div> :
        view === 'fields' ? <div className="table-scroll" role="region" aria-label="Preview fields" tabIndex={0}><table><thead><tr><th>Field</th><th>Looker Studio type</th></tr></thead>
          <tbody>{result.fields.map(field => <tr key={field.name}><td>{field.name}</td><td>{field.type}</td></tr>)}</tbody></table>{!result.fields.length && <p className="result-note">No fields returned.</p>}</div> :
        result.rows.length ? <div className="table-scroll" role="region" aria-label="Preview results" tabIndex={0}><table><thead><tr>{result.fields.map(field =>
          <th key={field.name}>{field.name}<span className="col-type">{field.type}</span></th>)}</tr></thead><tbody>{result.rows.map((row, i) => <tr key={i}>
            {row.map((cell, j) => <td key={j}>{cell === null ? <em>null</em> : String(cell)}</td>)}</tr>)}</tbody></table></div> : <p className="result-note">No rows returned. Try adjusting the filter.</p>}
    </section>
    {publish && <ActionForm title="Open in Looker Studio" confirm="Save and open" close={() => setPublish(false)} submit={async () => {
      await openLooker(() => api('workspace/open', 'POST', pipelineBody(source, pipeline, id)), published => { setLaunch(published); setPublish(false); });
    }}><p>Save this pipeline and its output fields as a reusable dataset.</p>
      <label>Dataset ID<input autoFocus required pattern="[a-zA-Z0-9_\-]{1,64}" maxLength={64} value={id} placeholder="e.g. orders_by_region" onChange={e => setID(e.target.value)} /></label>
      <p className="hint"><Icon name="key" />Looker Studio needs a reporting token with a grant for this dataset. {owner ? 'Create one in Access tokens after saving.' : 'Ask a workspace owner for access after saving.'}</p>
    </ActionForm>}
  </>;
}

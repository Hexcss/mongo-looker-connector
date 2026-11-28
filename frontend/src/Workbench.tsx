import { useEffect, useMemo, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api, message } from './api';
import type { Connection, DatasetField, Result } from './api';
import { ActionForm, Alert, Empty, Icon } from './ui';

type Sample = { fields: DatasetField[]; rows: Result['rows'] };
type Source = { connectionId: string; database: string; collection: string };
type Launch = { datasetId: string; url: string };
type Plan = { documentsExamined: number | null; keysExamined: number | null; indexesUsed: string[] | null; rowLimit: number };

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
  { op: '$limit', title: 'Limit rows', hint: 'Keep the first rows at this point in the pipeline.' },
  { op: '$lookup', title: 'Join collections', hint: 'Match equal field values from a collection in this database.' }
];
function JoinFields({ source, collections }: { source: Source; collections: string[] }) {
  const [selection, setSelection] = useState('');
  const target = collections.includes(selection) ? selection : collections.find(c => c !== source.collection) ?? source.collection;
  const [suggestions, setSuggestions] = useState<DatasetField[]>([]);
  const [error, setError] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    setSuggestions([]); setError('');
    api('workspace/preview', 'POST', pipelineBody({ ...source, collection: target }, '[{"$limit":1}]'), controller.signal)
      .then(r => r.json()).then((result: Sample) => { if (!controller.signal.aborted) setSuggestions(result.fields); })
      .catch(reason => { if (!controller.signal.aborted) setError(message(reason)); });
    return () => controller.abort();
  }, [source.connectionId, source.database, target]);
  return <>
    <label>Join collection<select name="from" value={target} onChange={e => setSelection(e.target.value)}>
      {collections.map(c => <option key={c}>{c}</option>)}
    </select></label>
    <label>Foreign field<input name="foreignField" key={target} required list="join-fields" defaultValue="_id" />
      <datalist id="join-fields">{suggestions.map(f => <option key={f.name} value={f.name} />)}</datalist></label>
    {error && <p className="stage-hint" role="status">Field suggestions unavailable. {error} You can enter a field name.</p>}
    <label>Output field<input name="as" key={target} required defaultValue={target.replace(/[^a-zA-Z0-9_]/g, '_') + '_match'} /></label>
    <label className="join-unwind"><input name="unwind" type="checkbox" defaultChecked />One row per match</label>
    <p className="stage-hint">Adds an <code>$unwind</code> stage and skips unmatched rows. Clear to keep matches in an array.</p>
  </>;
}

function ExecutionPlan({ value }: { value: Plan }) {
  return <section className="execution-plan" aria-label="Execution plan">
    <h3>Execution plan</h3>
    <p>Measured with a {value.rowLimit}-row output limit.</p>
    <dl>
      <div><dt>Documents scanned</dt><dd>{value.documentsExamined?.toLocaleString() ?? 'Unavailable'}</dd></div>
      <div><dt>Index entries scanned</dt><dd>{value.keysExamined?.toLocaleString() ?? 'Unavailable'}</dd></div>
      <div><dt>Indexes used</dt><dd>{value.indexesUsed === null ? 'Unavailable' : value.indexesUsed.length === 0 ? 'None' :
        <ul>{value.indexesUsed.map(index => <li key={index}><code>{index}</code></li>)}</ul>}</dd></div>
    </dl>
  </section>;
}

function StageBuilder({ source, collections, sample, append }: {
  source: Source; collections: string[]; sample: Sample | null; append: (...stages: string[]) => void;
}) {
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
  function insert(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setError('');
    try {
      if (operation === '$lookup') {
        const form = new FormData(event.currentTarget);
        const from = String(form.get('from'));
        const foreignField = String(form.get('foreignField')).trim();
        const alias = String(form.get('as')).trim();
        const paths = [name, foreignField, alias];
        if (!collections.includes(from) || paths.some(path => !path || /[\\\0]/.test(path) ||
          path.split('.').some(part => !part || part.startsWith('$') || part.trim() !== part))) {
          throw new Error('Choose an available collection and use dotted field names without empty or dollar-prefixed segments.');
        }
        const lookup = JSON.stringify({ $lookup: { from, localField: name, foreignField, as: alias } }, null, 2);
        const stages = [lookup];
        if (form.has('unwind')) stages.push(JSON.stringify({ $unwind: '$' + alias }));
        append(...stages);
        return;
      }
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
    {operation !== '$limit' && <label>{operation === '$lookup' ? 'Local field' : 'Field'}<input list="workspace-fields" required value={name} placeholder="Field name" onChange={e => { setField(e.target.value); setValue(''); }} />
      <datalist id="workspace-fields">{names.map(item => <option key={item} value={item} />)}</datalist></label>}
    {operation === '$lookup' && <JoinFields source={source} collections={collections} />}
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
    </section>{collection ? <Pipeline key={JSON.stringify([connection.id, connection.revision, database, collection, collections])} source={{ connectionId: connection.id, database, collection }} collections={collections} pipeline={pipeline} edit={setPipeline} owner={owner} /> :
      <section className="panel"><Empty icon="folder" title="No collection selected">Choose a connection with an available collection.</Empty></section>}</>}
  </>;
}

function Pipeline({ source, collections, pipeline, edit, owner }: { source: Source; collections: string[]; pipeline: string; edit: (value: string) => void; owner: boolean }) {
  const [sample, setSample] = useState<Sample | null>(null);
  const [preview, setPreview] = useState<{ text: string; result: Sample } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [view, setView] = useState<'rows' | 'fields'>('rows');
  const [publish, setPublish] = useState(false);
  const [id, setID] = useState('');
  const [launch, setLaunch] = useState<Launch | null>(null);
  const [plan, setPlan] = useState<Plan | null>(null);
  const [planError, setPlanError] = useState('');
  const [explaining, setExplaining] = useState(false);
  const explainRequest = useRef<AbortController | null>(null);
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
    return () => { controller.abort(); request.current?.abort(); explainRequest.current?.abort(); };
  }, [source.connectionId, source.database, source.collection]);
  const empty = /^\s*\[\s*\]\s*$/.test(pipeline);
  const result = error ? null : preview?.text === pipeline ? preview.result : empty ? sample : null;
  const stageCount = (() => { try { const stages = JSON.parse(pipeline); return Array.isArray(stages) ? stages.length : null; } catch { return null; } })();
  function change(text: string) {
    request.current?.abort(); setBusy(false); setError(''); setPreview(null); setLaunch(null);
    explainRequest.current?.abort(); setExplaining(false); setPlan(null); setPlanError(''); edit(text);
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
  async function explain() {
    explainRequest.current?.abort();
    const controller = new AbortController();
    explainRequest.current = controller; setExplaining(true); setPlanError(''); setPlan(null);
    try {
      const response = await api('workspace/explain', 'POST', pipelineBody(source, pipeline), controller.signal);
      const data: Plan = await response.json();
      if (!controller.signal.aborted) setPlan(data);
    } catch (err) { if (!controller.signal.aborted) setPlanError(message(err)); }
    finally { if (!controller.signal.aborted) setExplaining(false); }
  }
  function add(...stages: string[]) {
    pipelineBody(source, pipeline);
    const prefix = pipeline.trim().slice(0, -1).trimEnd();
    change(prefix + (stageCount ? ',' : '') + '\n  ' + stages.join(',\n').replaceAll('\n', '\n  ') + '\n]');
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
        <div className="run-toolbar"><button className="primary" disabled={busy} onClick={run}><Icon name="play" />{busy ? 'Running…' : 'Run preview'}</button>
          <button className="secondary" disabled={explaining} onClick={explain}><Icon name="search" />{explaining ? 'Explaining…' : 'Explain'}</button>
          <kbd>Ctrl / ⌘ + Enter to preview</kbd></div>
        <span role="status" className="sr-only">{explaining ? 'Running Explain.' : plan ? 'Execution plan ready.' : ''}</span>
        {planError && <Alert>{planError}</Alert>}
        {plan && <ExecutionPlan value={plan} />}
        {error && <Alert>{error}</Alert>}
      </div><StageBuilder source={source} collections={collections} sample={result ?? sample} append={add} /></div>
    </section>
    <section className="work-panel output" aria-busy={busy || sampling}><header className="work-panel-title"><h2><Icon name="grid" />Output preview</h2>
      {result && <span className="sample-count" role="status">{result.rows.length} {result.rows.length === 1 ? 'row' : 'rows'} · {result.fields.length} {result.fields.length === 1 ? 'field' : 'fields'}</span>}</header>
      <div className="output-toolbar"><div className="view-switch" role="group" aria-label="Preview view">{(['rows', 'fields'] as const).map(tab =>
        <button key={tab} aria-pressed={tab === view} onClick={() => setView(tab)}>{tab === 'rows' ? 'Rows' : 'Fields'}</button>)}</div>
        {launch ? <a className="secondary" target="_blank" rel="noopener noreferrer" href={launch.url}>Open in Looker Studio <Icon name="arrow" /></a> :
          <button className="secondary launch-button" disabled={!result?.fields.length || busy} onClick={() => setPublish(true)}>Open in Looker Studio <Icon name="arrow" /></button>}</div>
      {launch && <div role="status" className="saved-notice"><p>Saved as <strong>{launch.datasetId}</strong>.</p><p>{owner ? <>In <a href="#tokens">Access tokens</a>, issue a key that covers this dataset’s fields and any others you report on through this gateway.</> : 'Ask an owner for a key that covers this dataset’s fields and your other datasets on this gateway.'} Reauthorizing replaces the gateway’s saved key. Reopen the Looker Studio link afterward.</p></div>}
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

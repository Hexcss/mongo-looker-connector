import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api, lines, message } from './api';
import type { Connection, Result } from './api';
import { Alert, Empty, Icon, PageHeading } from './ui';

export function Preview({ items, initialID }: { items: Connection[]; initialID: string | null }) {
  const [id, setID] = useState(initialID ?? '');
  const [db, setDB] = useState('');
  const [coll, setColl] = useState('');
  const source = items.find(item => item.id === id) ?? items[0];
  const databases = Object.keys(source?.databases ?? {});
  const database = databases.includes(db) ? db : databases[0];
  const collections = source?.databases[database] ?? [];
  const collection = collections.includes(coll) ? coll : collections[0];
  const [pipeline, setPipeline] = useState('[]');
  const [fields, setFields] = useState('');
  const [result, setResult] = useState<Result | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const current = useRef<AbortController | null>(null);
  useEffect(() => {
    current.current?.abort(); setResult(null); setError(''); setBusy(false);
    return () => current.current?.abort();
  }, [source?.id, database, collection, fields, pipeline]);

  async function run(event: FormEvent) {
    event.preventDefault();
    if (busy || !source || !database || !collection) return;
    setError(''); setResult(null);
    try {
      if (!Array.isArray(JSON.parse(pipeline))) throw new Error();
    } catch { setError('Enter the aggregation pipeline as a valid JSON array. Use [] to read documents directly.'); return; }
    const requested = lines(fields);
    const params = { connectionId: source.id, database, collection, ...(requested.length && { fields: requested }) };
    // Forward the original JSON so JavaScript cannot round int64 literals or discard duplicate keys.
    const body = JSON.stringify(params).slice(0, -1) + ',"pipeline":' + pipeline + '}';
    const controller = new AbortController();
    current.current = controller; setBusy(true);
    try {
      const response: Result = await (await api('preview', 'POST', body, controller.signal)).json();
      if (!controller.signal.aborted) setResult(response);
    } catch (reason) { if (!controller.signal.aborted) setError(message(reason)); }
    finally { if (!controller.signal.aborted) setBusy(false); }
  }
  return <>
    <PageHeading title="Query preview" description="Run a read-only aggregation and inspect up to 50 rows." />
    {source ? <><form className="panel query-form" onSubmit={run} onKeyDown={event => {
      if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') { event.preventDefault(); event.currentTarget.requestSubmit(); }
    }}>
      <div className="panel-header"><h2>Query editor</h2><span className="mode"><Icon name="lock" />Read only</span></div>
      <div className="editor-fields">
        <div className="selectors"><label>Connection<select value={source.id} onChange={event => { setID(event.target.value); setDB(''); setColl(''); }}>{items.map(item => <option key={item.id}>{item.id}</option>)}</select></label>
          <label>Database<select value={database} onChange={event => { setDB(event.target.value); setColl(''); }}>{databases.map(name => <option key={name}>{name}</option>)}</select></label>
          <label>Collection<select value={collection} onChange={event => setColl(event.target.value)}>{collections.map(name => <option key={name}>{name}</option>)}</select></label></div>
        <label>Aggregation pipeline<textarea className="code-editor" value={pipeline} rows={5} spellCheck={false} onChange={event => setPipeline(event.target.value)} /></label>
        <label>Output fields<textarea rows={2} value={fields} placeholder="One field per line, in display order" spellCheck={false} onChange={event => setFields(event.target.value)} /></label>
        <p className="field-note">Leave blank to infer fields from the results.</p>
        {error && <Alert>{error}</Alert>}
      </div>
      <footer className="editor-actions"><button disabled={busy || !collection} className="primary"><Icon name="play" />{busy ? 'Running…' : 'Run preview'}</button><small>Ctrl / ⌘ + Enter</small></footer>
    </form>
    <section className="panel results" aria-busy={busy}><div className="panel-header"><h2>Results</h2>{result && <span className="mode" role="status">{result.rows.length} {result.rows.length === 1 ? 'row' : 'rows'} · {result.fields.length} {result.fields.length === 1 ? 'column' : 'columns'}</span>}</div>
      {result?.rows.length ? <div className="table-scroll" tabIndex={0} role="region" aria-label="Preview results"><table><thead><tr>{result.fields.map(field => <th key={field} scope="col">{field}</th>)}</tr></thead><tbody>{result.rows.map((row, i) => <tr key={i}>{row.map((value, j) => <td key={j}>{value === null ? <em>null</em> : String(value)}</td>)}</tr>)}</tbody></table></div> :
        <Empty icon="grid" title={busy ? 'Running preview…' : result ? 'No rows returned' : 'No preview yet'}>{result ? 'Adjust the pipeline or choose another collection.' : 'Rows from the selected collection will appear here.'}</Empty>}
    </section></> : <section className="panel"><Empty icon="database" title="Add a connection first"><a href="#connections">Open Connections</a> to configure the first collection.</Empty></section>}
  </>;
}

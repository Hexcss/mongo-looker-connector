import { useRef, useState } from 'react';
import { api, lines } from './api';
import type { Connection } from './api';
import { ActionForm, Badge, Empty, Icon, PageHeading } from './ui';

function ConnectionForm({ source, close, saved }: { source?: Connection; close: () => void; saved: (id: string) => void }) {
  const [id, setID] = useState(source?.id ?? '');
  const [uri, setURI] = useState('');
  const [rows, setRows] = useState(() => Object.entries(source?.databases ?? { '': [] }).map(([database, collections], key) => ({ key, database, collections: collections.join('\n') })));
  const nextKey = useRef(rows.length);
  function change(key: number, field: 'database' | 'collections', value: string) {
    setRows(rows.map(row => row.key === key ? { ...row, [field]: value } : row));
  }
  async function save() {
    const databases = Object.create(null) as Record<string, string[]>;
    for (const row of rows) {
      const name = row.database.trim();
      const collections = lines(row.collections);
      if (!name || !collections.length) throw new Error('Enter a database name and at least one collection for each database.');
      if (Object.hasOwn(databases, name)) throw new Error('Each database name must appear only once.');
      if (new Set(collections).size !== collections.length) throw new Error('Each collection must appear only once within its database.');
      databases[name] = collections;
    }
    const body = source ? { revision: source.revision, databases, ...(uri && { uri }) } : { id, uri, databases };
    const path = source ? 'connections/' + encodeURIComponent(source.id) : 'connections';
    await api(path, source ? 'PUT' : 'POST', JSON.stringify(body));
    setURI('');
    saved(id);
  }
  return <ActionForm title={source ? 'Edit connection' : 'Add connection'} confirm="Save connection" close={close} submit={save}>
    <label>Connection ID<input autoFocus value={id} onChange={event => setID(event.target.value)} disabled={!!source} required maxLength={64} pattern="[A-Za-z0-9_\-]{1,64}" placeholder="e.g. reporting" spellCheck={false} /></label>
    <label>Connection URI<input type="password" autoComplete="new-password" value={uri} onChange={event => setURI(event.target.value)} required={!source} placeholder={source ? 'Enter a new URI to rotate credentials' : 'mongodb://… or mongodb+srv://…'} spellCheck={false} /></label>
    <p className="field-note">{source ? 'Leave blank to keep the stored credential. Replacing it invalidates existing reporting grants for this connection.' : 'Use a read-only identity. SRV URIs use TLS by default. For mongodb:// URIs, include tls=true.'}</p>
    <div className="field-heading"><h3>Allowed namespaces</h3><button type="button" className="text-button" onClick={() => setRows([...rows, { key: nextKey.current++, database: '', collections: '' }])}><Icon name="plus" />Add database</button></div>
    {rows.map(row => <div className="namespace-fields" key={row.key}>
      <label>Database name<input required value={row.database} onChange={event => change(row.key, 'database', event.target.value)} placeholder="e.g. analytics" spellCheck={false} /></label>
      <label>Collections<textarea required rows={2} value={row.collections} onChange={event => change(row.key, 'collections', event.target.value)} placeholder="One collection per line" spellCheck={false} /></label>
      {rows.length > 1 && <button className="text-button" type="button" onClick={() => setRows(rows.filter(item => item.key !== row.key))}>Remove database</button>}
    </div>)}
  </ActionForm>;
}

function ScopeStats({ items }: { items: Connection[] }) {
  const dbs = items.flatMap(item => Object.values(item.databases));
  const stats = [
    { icon: 'database', label: 'Connections', value: items.length },
    { icon: 'layers', label: 'Allowed databases', value: dbs.length },
    { icon: 'grid', label: 'Allowed collections', value: dbs.reduce((count, list) => count + list.length, 0) }
  ];
  return <dl className="stats">{stats.map(stat => <div key={stat.label}><dt><span className="stat-icon"><Icon name={stat.icon} /></span>{stat.label}</dt><dd>{stat.value}</dd></div>)}</dl>;
}

export function Connections({ items, refresh }: { items: Connection[]; refresh: () => void }) {
  const [selected, setSelected] = useState('');
  const [search, setSearch] = useState('');
  const [modal, setModal] = useState<{ action: 'add' } | { action: 'edit' | 'delete'; source: Connection } | null>(null);
  const [notice, setNotice] = useState('');
  const visible = items.filter(item => item.id.toLowerCase().includes(search.trim().toLowerCase()));
  const active = visible.find(item => item.id === selected) ?? visible[0];
  function saved(id: string) {
    setNotice('Saved connection ' + id + '.'); setSelected(id); setSearch(''); setModal(null); refresh();
  }
  return <>
    <PageHeading title="Connections" description="Manage database connections and the collections available for reporting." action={<button className="primary" onClick={() => setModal({ action: 'add' })}><Icon name="plus" />Add connection</button>} />
    <ScopeStats items={items} />
    {notice && <p role="status" className="notice">{notice}</p>}
    <div className="inventory">
      <section className="panel"><div className="panel-header"><h2>Connection directory</h2><span className="counter">{items.length}</span></div>
        <div className="search"><Icon name="search" /><input aria-label="Find a connection" type="search" placeholder="Find a connection…" value={search} onChange={event => setSearch(event.target.value)} /></div>
        {visible.length ? <ul className="directory">{visible.map(item => <li key={item.id}><button aria-label={item.id} aria-pressed={item.id === active?.id} onClick={() => setSelected(item.id)}>
          <span className="icon-box"><Icon name="database" /></span><span className="directory-name"><strong>{item.id}</strong><small>{item.managed ? 'Console managed' : 'Deployment managed'}</small></span><Icon name="right" />
        </button></li>)}</ul> : <Empty icon="database" title={search ? 'No matching connections' : 'No connections yet'}>{search ? 'Try another connection ID.' : 'Add a connection to make a collection available for reporting.'}</Empty>}
      </section>
      <section className="panel detail"><div className="panel-header"><h2>Connection details</h2></div>{active ? <>
        <div className="detail-summary"><div className="detail-id"><span className="icon-box big"><Icon name="database" /></span><div><small className="meta-label">Connection ID</small><h3>{active.id}</h3></div></div>
          <Badge accent={active.managed}>{active.managed ? 'Console managed' : 'Deployment managed'}</Badge></div>
        <div className="detail-content"><h3>Allowed namespaces</h3>
          <ul className="dbs">{Object.entries(active.databases).map(([db, collections]) => <li key={db}><div className="db-name"><Icon name="folder" /><strong>{db}</strong></div>
            <div className="chips">{collections.map(name => <code key={name}>{name}</code>)}</div></li>)}</ul>
          {!active.managed && <p className="hint"><Icon name="lock" />Edit this connection in the gateway deployment configuration.</p>}
        </div>
        <footer className="detail-actions"><a className="secondary" href={'#preview?connection=' + encodeURIComponent(active.id)}><Icon name="terminal" />Open query preview</a>
          {active.managed && <><button className="secondary" onClick={() => setModal({ action: 'edit', source: active })}><Icon name="edit" />Edit connection</button>
            <button className="danger-link" onClick={() => setModal({ action: 'delete', source: active })}>Delete connection</button></>}</footer>
      </> : <Empty icon="layers" title="No connection selected">Choose a connection from the directory to inspect its namespaces.</Empty>}</section>
    </div>
    {(modal?.action === 'add' || modal?.action === 'edit') && <ConnectionForm source={modal.action === 'edit' ? modal.source : undefined} close={() => setModal(null)} saved={saved} />}
    {modal?.action === 'delete' && <ActionForm title="Delete connection" confirm="Delete connection" danger close={() => setModal(null)} submit={async () => {
      await api('connections/' + encodeURIComponent(modal.source.id), 'DELETE', JSON.stringify({ revision: modal.source.revision }));
      setNotice('Deleted connection ' + modal.source.id + '.'); setModal(null); refresh();
    }}><p>Delete <strong>{modal.source.id}</strong>? Reports using this connection will lose access to its collections.</p></ActionForm>}
  </>;
}

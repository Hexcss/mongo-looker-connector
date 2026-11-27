import { useState } from 'react';
import { api, useList } from './api';
import type { CollectionGrant, Connection, Dataset, DatasetGrant, Token } from './api';
import { ActionForm, Alert, Badge, Empty, Icon, Modal, PageHeading } from './ui';

function IssueToken({ items, close, issued }: { items: Connection[]; close: () => void; issued: (secret: string) => void }) {
  const [label, setLabel] = useState('');
  const [selected, setSelected] = useState<CollectionGrant[]>([]);
  const [datasetGrants, setDatasetGrants] = useState<DatasetGrant[]>([]);
  const datasets = useList<Dataset>('datasets', 0);
  function toggleField(datasetId: string, field: string, checked: boolean) {
    setDatasetGrants(current => {
      const existing = current.find(item => item.datasetId === datasetId);
      if (!existing) return checked ? [...current, {datasetId, fields: [field]}] : current;
      const fields = checked ? [...existing.fields, field] : existing.fields.filter(name => name !== field);
      return fields.length ? current.map(item => item === existing ? { datasetId, fields } : item) : current.filter(item => item !== existing);
    });
  }
  function toggle(grant: CollectionGrant, checked: boolean) {
    setSelected(current => checked ? [...current, grant] : current.filter(item =>
      item.connectionId !== grant.connectionId || item.database !== grant.database || item.collection !== grant.collection));
  }
  return <ActionForm title="Issue access token" close={close} confirm="Issue token" submit={async () => {
    if (!label.trim()) throw new Error('Enter a descriptive token label.');
    const grants = [...selected, ...datasetGrants];
    if (!grants.length) throw new Error('Select a dataset field or a source collection.');
    const response = await api('tokens', 'POST', JSON.stringify({ label: label.trim(), grants }));
    const { secret }: { secret: string } = await response.json();
    issued(secret);
  }}>
    <label>Token label<input autoFocus required value={label} onChange={event => setLabel(event.target.value)} placeholder="e.g. Finance reporting" /></label>
    <h3>Dataset field access</h3>
    {datasets.error ? <Alert>{datasets.error}</Alert> : !datasets.list ? <p className="field-note" role="status">Loading datasets…</p> :
      datasets.list.length === 0 ? <p className="field-note">Published datasets will appear here.</p> : <div className="scope-picker">{datasets.list.map(dataset =>
        <fieldset key={dataset.id} className="scope-db"><legend>{dataset.id}</legend>{dataset.fields.map(field =>
          <label key={field.name}><input type="checkbox" onChange={event => toggleField(dataset.id, field.name, event.target.checked)} />{field.name}</label>
        )}</fieldset>)}</div>}
    <h3>Source collection access</h3><p className="field-note">Selecting a source grants direct access to all its fields, including fields excluded from datasets.</p>
    <div className="scope-picker">{items.map(source => <div key={source.id}><div className="scope-source"><Icon name="database" />{source.id}</div>
      {Object.entries(source.databases).map(([db, names]) => <fieldset key={db} className="scope-db"><legend>{db}</legend>
        {names.map(collection => <label key={collection}><input type="checkbox" onChange={event => toggle({ connectionId: source.id, database: db, collection }, event.target.checked)} />{collection}</label>)}</fieldset>)}</div>)}</div>
  </ActionForm>;
}

function Secret({ secret, close }: { secret: string; close: () => void }) {
  const [status, setStatus] = useState('');
  return <Modal title="Token issued" close={close}><div className="form-fields"><p>Copy this token now and store it with your reporting client. Its value is shown once.</p>
    <label>Token secret<textarea autoFocus readOnly autoComplete="off" spellCheck={false} rows={3} className="secret" value={secret} onFocus={event => event.target.select()} /></label>
    {status && <p role="status">{status}</p>}</div>
    <footer className="form-actions"><button className="secondary" onClick={async () => {
      try { await navigator.clipboard.writeText(secret); setStatus('Copied to clipboard.'); }
      catch { setStatus('Select and copy the value in the token field.'); }
    }}><Icon name="copy" />Copy token</button><button className="primary" onClick={close}>Done</button></footer></Modal>;
}

export function Tokens({ items, refreshKey }: { items: Connection[]; refreshKey: number }) {
  const [updates, setUpdates] = useState(0);
  const refresh = () => setUpdates(count => count + 1);
  const { list, error } = useList<Token>('tokens', refreshKey + updates);
  const [issuing, setIssuing] = useState(false);
  const [revoking, setRevoking] = useState<Token | null>(null);
  const [secret, setSecret] = useState('');
  const [notice, setNotice] = useState('');
  return <>
    <PageHeading title="Access tokens" description="Grant reporting clients access to dataset fields or source collections." action={<button className="primary" disabled={!items.length} onClick={() => setIssuing(true)}><Icon name="plus" />Issue token</button>} />
    {notice && <p role="status" className="notice">{notice}</p>}
    {error && <Alert>{error}</Alert>}
    <section className="panel"><div className="panel-header"><h2>Reporting tokens</h2>{list && <span className="counter">{list.length}</span>}</div>
      {list?.length ? <div className="table-scroll" tabIndex={0} role="region" aria-label="Reporting tokens"><table className="token-table"><thead><tr><th scope="col">Token</th><th scope="col">Reporting access</th><th scope="col">Created</th><th scope="col">Status</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead>
        <tbody>{list.map(token => <tr key={token.id}><td><strong>{token.label}</strong><small>{token.id}</small></td>
          <td><details><summary>{token.grants.length} {token.grants.length === 1 ? 'grant' : 'grants'}</summary><ul className="grant-list">{token.grants.map(grant => <li key={JSON.stringify(grant)}><code>{'datasetId' in grant ? grant.datasetId + ' · ' + grant.fields.join(', ') : [grant.connectionId, grant.database, grant.collection].join(' / ')}</code></li>)}</ul></details></td>
          <td><time dateTime={token.createdAt}>{new Date(token.createdAt).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })}</time></td>
          <td><Badge accent={!token.revoked}>{token.revoked ? 'Revoked' : 'Active'}</Badge></td><td>{!token.revoked && <button className="danger-link" aria-label={'Revoke ' + token.label} onClick={() => setRevoking(token)}>Revoke</button>}</td></tr>)}</tbody></table></div>
        : list ? <Empty icon="key" title="No access tokens yet">{items.length ? 'Issue a token to connect a reporting client to dataset fields or collections.' : 'Add a connection before issuing a token.'}</Empty> : !error && <p className="loading" role="status">Loading tokens…</p>}
    </section>
    {issuing && <IssueToken items={items} close={() => setIssuing(false)} issued={value => { setSecret(value); setIssuing(false); setNotice(''); refresh(); }} />}
    {secret && <Secret secret={secret} close={() => setSecret('')} />}
    {revoking && <ActionForm title="Revoke token" confirm="Revoke token" danger close={() => setRevoking(null)} submit={async () => {
      await api('tokens/' + encodeURIComponent(revoking.id), 'DELETE', '{}');
      setNotice('Revoked access for ' + revoking.label + '.'); setRevoking(null); refresh();
    }}><p>Revoke <strong>{revoking.label}</strong>? Clients using this token will lose their granted access.</p></ActionForm>}
  </>;
}

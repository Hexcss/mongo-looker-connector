import { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { api, message, useList } from './api';
import type { Connection } from './api';
import { Datasets } from './Datasets';
import { Connections } from './Connections';
import { Preview } from './Preview';
import { Tokens } from './Tokens';
import { Alert, Icon } from './ui';
import { Workbench } from './Workbench';
import { Profile, Team } from './Accounts';
import type { Account } from './Accounts';
import './style.css';

const destinations = [
  { href: 'workspace', label: 'Pipeline workspace', icon: 'terminal', group: 'Data workspace' },
  { href: 'datasets', label: 'Datasets', icon: 'layers', group: 'Data workspace' },
  { href: 'connections', label: 'Connections', icon: 'database', group: 'Administration' },
  { href: 'tokens', label: 'Access tokens', icon: 'key', group: 'Administration' },
  { href: 'team', label: 'Team members', icon: 'users', group: 'Administration' },
  { href: 'account', label: 'My account', icon: 'user', group: 'Account' }
];

function CatalogPage({ route, params, me, refreshKey, refresh }: { route: string; params?: string; me: Account; refreshKey: number; refresh: () => void }) {
  const { list, error } = useList<Connection>('connections', refreshKey);
  if (error) return <Alert>{error} Use Refresh to retry.</Alert>;
  if (!list) return <p className="loading" role="status">Loading connections…</p>;
  switch (route) {
    case 'connections': return <Connections items={list} refresh={refresh} />;
    case 'datasets': return <Datasets items={list} refreshKey={refreshKey} role={me.role} />;
    case 'tokens': return <Tokens items={list} refreshKey={refreshKey} />;
    case 'preview': return <Preview items={list} initialID={new URLSearchParams(params).get('connection')} />;
    default: return <Workbench items={list} initialID={new URLSearchParams(params).get('connection')} owner={me.role === 'owner'} />;
  }
}

function App() {
  const [hash, setHash] = useState(location.hash.slice(1));
  const [refreshKey, setRefresh] = useState(0);
  const [me, setMe] = useState<Account | null>(null);
  const [error, setError] = useState('');
  const refresh = () => setRefresh(key => key + 1);
  useEffect(() => {
    const controller = new AbortController();
    api('me', 'GET', undefined, controller.signal).then(r => r.json()).then((account: Account) => {
      if (!controller.signal.aborted) { setMe(account); setError(''); }
    }).catch(err => { if (!controller.signal.aborted) { setMe(null); setError(message(err)); } });
    return () => controller.abort();
  }, [refreshKey]);
  const [route, params] = hash.split('?');
  const allowed = destinations.filter(item => {
    if (item.href === 'account') return true;
    if (!me || me.mustChangePassword) return false;
    if (item.href === 'datasets') return true;
    if (item.href === 'workspace') return me.role !== 'viewer';
    return me.role === 'owner';
  });
  const page = allowed.find(item => item.href === route) ?? allowed[0];
  const legacy = route === 'preview' && me && me.role !== 'viewer' && !me.mustChangePassword;
  const title = legacy ? 'Query preview' : page.label;
  useEffect(() => {
    const navigate = () => setHash(location.hash.slice(1));
    window.addEventListener('hashchange', navigate);
    return () => window.removeEventListener('hashchange', navigate);
  }, []);
  useEffect(() => {
    document.title = title + ' · Mongo Looker Connector';
    if (hash) document.getElementById('page-title')?.focus({ preventScroll: true });
    window.scrollTo(0, 0);
  }, [hash, title]);
  return <><a className="skip" href="#main" onClick={event => { event.preventDefault(); document.getElementById('main')?.focus(); }}>Skip to content</a>
    <header className="mast"><a className="product" href="#"><Icon name="layers" /><strong>MongoDB Reporting</strong></a><span className="product-context">Gateway console</span>
      {me && <a className="identity" href="#account"><span className="avatar" aria-hidden="true">{(me.email || 'Operator').slice(0, 1).toUpperCase()}</span>{me.email || 'Operator'}</a>}</header>
    <div className="layout"><aside className="sidebar"><nav aria-label="Administration">{['Data workspace', 'Administration', 'Account'].map(group => {
      const links = allowed.filter(item => item.group === group);
      return links.length > 0 && <div className="nav-section" key={group}><div className="nav-group">{group}</div>{links.map(item =>
        <a key={item.href} href={'#' + item.href} aria-current={page.href === item.href ? 'page' : undefined}><Icon name={item.icon} />{item.label}</a>)}</div>;
    })}</nav></aside>
    <div className="workspace"><header className="topbar"><div className="location"><span>{page.group}</span><Icon name="right" /><strong>{title}</strong></div>
      <button className="refresh" onClick={refresh}><Icon name="refresh" />Refresh</button></header>
      <main id="main" tabIndex={-1}>{error ? <Alert>{error} <a href="/">Sign in again</a> or use Refresh to retry.</Alert> : !me ? <p className="loading" role="status">Opening workspace…</p> :
        page.href === 'account' ? <Profile account={me} /> : page.href === 'team' ? <Team me={me} refresh={refreshKey} /> :
        me.role === 'viewer' ? <Datasets items={[]} refreshKey={refreshKey} role={me.role} /> :
          <CatalogPage key={page.href + ':' + (params ?? '') + (legacy ? ':legacy' : '')} route={legacy ? 'preview' : page.href} params={params} me={me} refreshKey={refreshKey} refresh={refresh} />}
      </main>
    </div></div></>;
}

const container = document.getElementById('root')!;
let root = createRoot(container);
root.render(<App />);
window.addEventListener('pagehide', () => root.unmount());
window.addEventListener('pageshow', event => {
  if (event.persisted) { root = createRoot(container); root.render(<App />); }
});

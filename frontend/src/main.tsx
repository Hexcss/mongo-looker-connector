import { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { useList } from './api';
import type { Connection } from './api';
import { Connections } from './Connections';
import { Preview } from './Preview';
import { Tokens } from './Tokens';
import { Alert, Icon } from './ui';
import './style.css';

const destinations = [
  { href: 'connections', label: 'Connections', icon: 'database' },
  { href: 'preview', label: 'Query preview', icon: 'terminal' },
  { href: 'tokens', label: 'Access tokens', icon: 'key' }
];

function App() {
  const [hash, setHash] = useState(location.hash.slice(1));
  const [refreshKey, setRefresh] = useState(0);
  const refresh = () => setRefresh(key => key + 1);
  const { list, error } = useList<Connection>('connections', refreshKey);
  const [route, params] = hash.split('?');
  const page = destinations.find(item => item.href === route) ?? destinations[0];
  useEffect(() => {
    const navigate = () => setHash(location.hash.slice(1));
    window.addEventListener('hashchange', navigate);
    return () => window.removeEventListener('hashchange', navigate);
  }, []);
  useEffect(() => {
    document.title = page.label + ' · Mongo Looker Connector';
    if (hash) document.getElementById('page-title')?.focus({ preventScroll: true });
    window.scrollTo(0, 0);
  }, [hash, page]);
  return <div className="layout">
    <a className="skip" href="#main" onClick={event => { event.preventDefault(); document.getElementById('main')?.focus(); }}>Skip to content</a>
    <aside className="sidebar">
      <div className="brand"><span className="brand-mark" aria-hidden="true"><i/><i/><i/></span><div><strong>Mongo Looker</strong><span>Connector</span></div></div>
      <nav aria-label="Administration"><div className="nav-group">Workspace</div>{destinations.map(item => <a key={item.href} href={'#' + item.href} aria-current={page.href === item.href ? 'page' : undefined}><Icon name={item.icon} /><span>{item.label}</span></a>)}</nav>
      <div className="sidebar-foot"><Icon name="lock" /><span>Private gateway</span></div>
    </aside>
    <div className="workspace"><header className="topbar"><div className="location"><Icon name="layers" /><span>Administration</span><Icon name="right" /><strong>{page.label}</strong></div>
      <button className="refresh" onClick={refresh}><Icon name="refresh" />Refresh</button></header>
      <main id="main" tabIndex={-1}>
        {error ? <><h1 id="page-title" tabIndex={-1}>{page.label}</h1><Alert>{error} Use Refresh to retry.</Alert></> : !list ? <p className="loading" role="status">Loading connections…</p> :
          page.href === 'connections' ? <Connections items={list} refresh={refresh} /> :
          page.href === 'preview' ? <Preview key={hash} items={list} initialID={new URLSearchParams(params).get('connection')} /> :
          <Tokens items={list} refreshKey={refreshKey} />}
      </main>
    </div>
  </div>;
}

const container = document.getElementById('root')!;
let root = createRoot(container);
root.render(<App />);
// Drop credential forms and one-time secrets when leaving, including back/forward cache entries.
window.addEventListener('pagehide', () => root.unmount());
window.addEventListener('pageshow', event => {
  if (event.persisted) { root = createRoot(container); root.render(<App />); }
});

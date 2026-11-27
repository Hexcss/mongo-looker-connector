import { useEffect, useId, useRef, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { message } from './api';

export function Icon({ name }: { name: string }) {
  return <svg className="icon" aria-hidden="true"><use href={'./icons.svg#' + name} /></svg>;
}

export function PageHeading({ title, description, action }: { title: string; description: string; action?: ReactNode }) {
  return <header className="page-heading"><div><h1 id="page-title" tabIndex={-1}>{title}</h1><p>{description}</p></div>{action}</header>;
}

export function Alert({ children }: { children: ReactNode }) {
  return <div className="alert" role="alert">{children}</div>;
}

export function Empty({ icon, title, children }: { icon: string; title: string; children: ReactNode }) {
  return <div className="empty"><span className="icon-box"><Icon name={icon} /></span><h3>{title}</h3><p>{children}</p></div>;
}

export function Modal({ title, children, close, busy = false }: { title: string; children: ReactNode; close: () => void; busy?: boolean }) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleID = useId();
  useEffect(() => {
    const dialog = ref.current!;
    dialog.showModal();
    return () => dialog.close();
  }, []);
  return <dialog ref={ref} aria-labelledby={titleID} onCancel={event => { event.preventDefault(); if (!busy) close(); }}>
    <header className="modal-title"><h2 id={titleID}>{title}</h2><button type="button" aria-label="Close dialog" className="icon-button" disabled={busy} onClick={close}><Icon name="close" /></button></header>
    {children}
  </dialog>;
}

export function ActionForm({ title, children, submit, close, confirm, danger = false }: {
  title: string; children: ReactNode; submit: () => Promise<void>; close: () => void; confirm: string; danger?: boolean
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  async function send(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true); setError('');
    try { await submit(); }
    catch (err) { setError(message(err)); }
    finally { setBusy(false); }
  }
  return <Modal title={title} close={close} busy={busy}>
    <form onSubmit={send} autoComplete="off">
      <fieldset disabled={busy} className="form-fields">{children}{error && <Alert>{error}</Alert>}</fieldset>
      <footer className="form-actions"><button type="button" className="secondary" disabled={busy} onClick={close}>Cancel</button>
        <button className={danger ? 'danger' : 'primary'} disabled={busy}>{busy ? 'Working…' : confirm}</button></footer>
    </form>
  </Modal>;
}

export function Badge({ children, accent = false }: { children: ReactNode; accent?: boolean }) {
  return <span className={'badge' + (accent ? ' accent' : '')}>{children}</span>;
}

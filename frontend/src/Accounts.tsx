import { useState } from 'react';
import type { FormEvent } from 'react';
import { api, message, useList } from './api';
import { ActionForm, Alert, Badge, Icon, Modal, PageHeading } from './ui';

export type Role = 'owner' | 'editor' | 'viewer';
export type Account = { id: string; email: string; role: Role; operator?: boolean; mustChangePassword?: boolean; primary?: boolean; disabled?: boolean };
const roleDescriptions = {
  owner: 'Manage accounts, connections, datasets, and reporting tokens.',
  editor: 'Build pipelines, preview collections, and publish datasets.',
  viewer: 'Browse published datasets and open them in Looker Studio.'
};
type MemberAction = { mode: 'add' | 'bootstrap' | 'edit' | 'reset'; member?: Account };

export function Profile({ account }: { account: Account }) {
  const [current, setCurrent] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  async function change(event: FormEvent) {
    event.preventDefault(); setError('');
    const bytes = new TextEncoder().encode(password).length;
    if (bytes < 12 || bytes > 72) { setError('Choose a password between 12 and 72 bytes.'); return; }
    if (password !== confirm) { setError('The two new passwords must match.'); return; }
    setBusy(true);
    try {
      await api('password', 'POST', JSON.stringify({ currentPassword: current, newPassword: password }));
      setCurrent(''); setPassword(''); setConfirm(''); location.replace('/');
    } catch (err) { setError(message(err)); setBusy(false); }
  }
  return <><PageHeading title="My account" description={account.email || 'Google Cloud operator'} />
    <section className="panel"><div className="panel-header"><h2>Workspace access</h2><Badge>{account.role}</Badge></div>
      <p className="detail-content">{roleDescriptions[account.role]}</p></section>
    {account.operator ? <p className="hint"><Icon name="lock" />Your Google Cloud account manages this sign-in. Create a password account in <a href="#team">Team members</a>.</p> :
      <form className="panel password-panel" onSubmit={change}><div className="panel-header"><h2>Change password</h2></div>
        <fieldset className="form-fields" disabled={busy}>
          {account.mustChangePassword && <p className="notice">Set a new password before opening your workspace.</p>}
          <label>Current password<input autoComplete="current-password" type="password" required value={current} onChange={e => setCurrent(e.target.value)} /></label>
          <label>New password<input autoComplete="new-password" type="password" required value={password} onChange={e => setPassword(e.target.value)} /></label>
          <p className="field-note">Use 12–72 bytes. You’ll sign in again after saving.</p>
          <label>Confirm new password<input autoComplete="new-password" type="password" required value={confirm} onChange={e => setConfirm(e.target.value)} /></label>
          {error && <Alert>{error}</Alert>}
        </fieldset><footer className="editor-actions"><button className="primary" disabled={busy}>{busy ? 'Saving…' : 'Change password'}</button></footer>
      </form>}
    {(!account.operator || location.protocol === 'https:') && <SignOut />}
  </>;
}

function SignOut() {
  const [error, setError] = useState('');
  return <div className="session-actions"><button className="secondary" onClick={async () => {
    setError('');
    try {
      const response = await fetch('/session/logout', { method: 'POST', mode: 'same-origin', credentials: 'same-origin', referrer: location.origin + '/', referrerPolicy: 'same-origin' });
      if (!response.ok) throw new Error('Could not sign out. Try again.');
      location.replace('/');
    } catch (err) { setError(message(err)); }
  }}>Sign out</button>{error && <Alert>{error}</Alert>}</div>;
}

export function Team({ me, refresh }: { me: Account; refresh: number }) {
  const [changes, setChanges] = useState(0);
  const { list, error } = useList<Account>('users', refresh + changes);
  const [action, setAction] = useState<MemberAction | null>(null);
  const [secret, setSecret] = useState<{ email: string; password: string } | null>(null);
  const [notice, setNotice] = useState('');
  const bootstrap = me.operator && list && !list.some(member => member.primary);
  return <><PageHeading title="Team members" description="Control who can administer the gateway and build reporting datasets."
    action={list && <button className="primary" onClick={() => setAction({ mode: bootstrap ? 'bootstrap' : 'add' })}><Icon name="plus" />{bootstrap ? 'Create primary owner' : 'Add member'}</button>} />
    {error && <Alert>{error}</Alert>}{notice && <p role="status" className="notice">{notice}</p>}
    <section className="panel"><header className="panel-header"><h2>Members</h2>{list && <span className="counter">{list.length}</span>}</header>
      {!list ? <p className="loading">Loading team members…</p> : !list.length ? <p className="result-note">Create the first owner to enable password sign-in for your team.</p> :
        <div className="table-scroll"><table className="members"><thead><tr><th>Email</th><th>Role</th><th>Status</th><th><span className="sr-only">Actions</span></th></tr></thead>
          <tbody>{list.map(member => <tr key={member.id}><td>{member.email}{member.primary && <small>Primary owner</small>}</td><td>{member.role}</td><td><Badge accent={!member.disabled}>{member.disabled ? 'Disabled' : member.mustChangePassword ? 'Temporary password' : 'Active'}</Badge></td>
            <td><div className="member-actions">{!member.primary && member.id !== me.id && <button className="text-button" aria-label={'Edit ' + member.email} onClick={() => setAction({ mode: 'edit', member })}>Edit</button>}
              {!member.disabled && member.id !== me.id && (!member.primary || me.operator) && <button className="text-button" aria-label={'Reset password for ' + member.email} onClick={() => setAction({ mode: 'reset', member })}>Reset password</button>}</div></td>
          </tr>)}</tbody></table></div>}</section>
    {action && <MemberDialog action={action} close={() => setAction(null)} done={async response => {
      const result = await response.json();
      setAction(null); setChanges(n => n + 1);
      if (result.temporaryPassword) setSecret({ email: result.user.email, password: result.temporaryPassword });
      else setNotice('Updated ' + result.user.email + '.');
    }} />}
    {secret && <Modal title="Temporary password" close={() => setSecret(null)}><div className="form-fields"><p>Share this password privately with <strong>{secret.email}</strong>. They’ll choose a new one when they sign in.</p>
      <label>One-time password<input autoFocus readOnly spellCheck={false} autoComplete="off" value={secret.password} onFocus={e => e.target.select()} /></label></div>
      <footer className="form-actions"><button className="primary" onClick={() => setSecret(null)}>Done</button></footer></Modal>}
  </>;
}

function MemberDialog({ action, close, done }: { action: MemberAction; close: () => void; done: (r: Response) => Promise<void> }) {
  const { mode, member } = action;
  const [email, setEmail] = useState(member?.email || '');
  const [role, setRole] = useState<Role>(member?.role || 'viewer');
  const [disabled, setDisabled] = useState(member?.disabled || false);
  const title = { reset: 'Reset password', edit: 'Edit member', bootstrap: 'Create primary owner', add: 'Add member' }[mode];
  return <ActionForm close={close} title={title} confirm={mode === 'edit' ? 'Save changes' : title} danger={mode === 'reset'} submit={async () => {
    const path = member ? 'users/' + encodeURIComponent(member.id) : mode === 'bootstrap' ? 'bootstrap-owner' : 'users';
    const body = mode === 'bootstrap' ? { email } : mode === 'reset' ? {} : mode === 'edit' ? { role, disabled } : { email, role };
    await done(await api(mode === 'reset' ? path + '/reset-password' : path, mode === 'edit' ? 'PUT' : 'POST', JSON.stringify(body)));
  }}>
    {mode === 'reset' ? <p>Generate a temporary password for <strong>{email}</strong>? Their current password and sessions will expire.</p> : <>
      <label>Email address<input type="email" autoComplete="off" autoFocus required disabled={!!member} value={email} onChange={e => setEmail(e.target.value)} /></label>
      {mode === 'bootstrap' ? <p>The primary owner can manage all team members. You’ll receive a generated password to use for the first sign-in.</p> :
        <><label>Role<select value={role} onChange={e => setRole(e.target.value as Role)}>{Object.keys(roleDescriptions).map(r => <option key={r} value={r}>{r}</option>)}</select></label><p className="field-note">{roleDescriptions[role]}</p></>}
      {mode === 'edit' && <label>Status<select value={disabled ? 'disabled' : 'enabled'} onChange={e => setDisabled(e.target.value === 'disabled')}><option value="enabled">Enabled</option><option value="disabled">Disabled</option></select></label>}
    </>}
  </ActionForm>;
}

const statusMessage = document.getElementById('status');
document.getElementById('logout').addEventListener('click', async event => {
  const button = event.currentTarget;
  button.disabled = true;
  try {
    // Browsers suppress Origin for a native form with our no-referrer policy.
    const result = await fetch('/session/logout', {method:'POST', mode:'same-origin', credentials:'same-origin'});
    if (!result.ok) throw new Error('Sign out failed');
    location.replace('/');
  } catch {
    statusMessage.textContent = 'Could not sign out. Please try again.';
    button.disabled = false;
  }
});

(async () => {
  const ticket = location.hash.slice(1);
  if (!ticket) return;
  history.replaceState(null, '', '/');
  if (!/^[A-Za-z0-9_-]{43}$/.test(ticket)) { statusMessage.textContent = 'This sign-in link is invalid.'; return; }
  statusMessage.textContent = 'Opening your console…';
  try {
    const result = await fetch('/session/redeem', {method:'POST', body:ticket, credentials:'same-origin', mode:'same-origin', redirect:'error'});
    if (result.status === 204) { location.replace('/admin/'); return; }
    statusMessage.textContent = result.status === 401 ? 'This link expired or has already been used. Request a new link.' : 'Sign-in failed. Request a new link and try again.';
  } catch { statusMessage.textContent = 'The gateway is unavailable. Try again with a new link.'; }
})();

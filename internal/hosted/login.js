(async () => {
  const ticket = location.hash.slice(1);
  if (!ticket) return;
  history.replaceState(null, '', '/');
  const status = document.getElementById('status');
  if (!/^[A-Za-z0-9_-]{43}$/.test(ticket)) { status.textContent = 'This sign-in link is invalid.'; return; }
  status.textContent = 'Opening your console…';
  try {
    const result = await fetch('/session/redeem', {method:'POST', body:ticket, credentials:'same-origin', mode:'same-origin', redirect:'error'});
    if (result.status === 204) { location.replace('/admin/'); return; }
    status.textContent = result.status === 401 ? 'This link expired or has already been used. Request a new link.' : 'Sign-in failed. Request a new link and try again.';
  } catch { status.textContent = 'The gateway is unavailable. Try again with a new link.'; }
})();

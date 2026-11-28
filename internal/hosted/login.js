const statusMessage = document.getElementById('status');
// Firefox derives POST Origin from the referrer. Only disclose the public origin root.
const sessionPost = {method:'POST', mode:'same-origin', credentials:'same-origin', referrerPolicy:'same-origin', referrer:location.origin+'/'};
document.getElementById('logout').addEventListener('click', async event => {
  const button = event.currentTarget;
  button.disabled = true;
  try {
    const result = await fetch('/session/logout', sessionPost);
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
  if (!/^[A-Za-z0-9_-]{43}$/.test(ticket)) return;
  statusMessage.textContent = 'Opening your console…';
  try {
    const result = await fetch('/session/redeem', {...sessionPost, body:ticket, redirect:'error'});
    if (result.status === 204) { location.replace('/admin/'); return; }
    statusMessage.textContent = result.status === 401 ? 'This link expired or has already been used. Sign in below or request a new operator link.' : 'Sign in below or request a new operator link.';
  } catch { statusMessage.textContent = 'The gateway is unavailable. Try again with a new link.'; }
})();

document.getElementById('login').addEventListener('submit', async event => {
  event.preventDefault();
  const form = event.currentTarget;
  const button = form.querySelector('button');
  button.disabled = true;
  statusMessage.textContent = '';
  try {
    const response = await fetch('/session/login', {
      ...sessionPost, redirect: 'error',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({email: form.email.value.trim(), password: form.password.value})
    });
    if (response.status === 204) { form.password.value = ''; location.replace('/admin/'); return; }
    if (response.status === 401) statusMessage.textContent = 'The email or password is incorrect.';
    else if (response.status === 429) statusMessage.textContent = 'Too many sign-in attempts. Please wait before trying again.';
    else statusMessage.textContent = 'The gateway could not sign you in. Try again.';
  } catch { statusMessage.textContent = 'The gateway is unavailable. Try again.'; }
  button.disabled = false;
});

// S9 management UI — vanilla JS, no build. Escape-by-default: all rendering via
// textContent/DOM APIs, never innerHTML with API data.
const BASE = '/api/v1/plugins/custom-fields';

const token = () => localStorage.getItem('token') || '';
const $ = (id) => document.getElementById(id);

function show(stateId) {
  for (const s of ['state-loading', 'state-not-authorized', 'state-expired', 'state-app']) {
    $(s).classList.toggle('hidden', s !== stateId);
  }
}

// api() talks to the plugin API with the SPA's localStorage JWT. 401 → the
// expired view (we deliberately do NOT replicate the SPA's cookie refresh —
// S9 spec). Errors normalize to Error{status, message}.
async function api(path, opts = {}) {
  const res = await fetch(BASE + path, {
    ...opts,
    headers: {
      Authorization: 'Bearer ' + token(),
      ...(opts.body ? { 'Content-Type': 'application/json' } : {}),
      ...(opts.headers || {}),
    },
  });
  if (res.status === 401) {
    show('state-expired');
    const e = new Error('unauthorized');
    e.status = 401;
    throw e;
  }
  if (res.status === 204) return null;
  const text = await res.text();
  let body = null;
  if (text) {
    try { body = JSON.parse(text); } catch { body = { message: text }; }
  }
  if (!res.ok) {
    const e = new Error((body && body.message) || ('HTTP ' + res.status));
    e.status = res.status;
    throw e;
  }
  return body;
}

// fetchJSONv1 talks to the MAIN Vikunja API (project picker) — same token.
async function fetchJSONv1(path) {
  const res = await fetch('/api/v1' + path, {
    headers: { Authorization: 'Bearer ' + token() },
  });
  if (res.status === 401) {
    show('state-expired');
    const e = new Error('unauthorized');
    e.status = 401;
    throw e;
  }
  if (!res.ok) {
    const e = new Error('HTTP ' + res.status);
    e.status = res.status;
    throw e;
  }
  return res.json();
}

// List rendering arrives with the list-view task; the app state is the
// deliverable here.
async function boot() {
  if (!token()) {
    show('state-expired');
    return;
  }
  try {
    const { is_manager } = await api('/management-access');
    if (!is_manager) {
      show('state-not-authorized');
      return;
    }
    show('state-app');
  } catch (e) {
    if (e.status === 401) return; // expired view already shown by api()
    console.error(e);
    const box = document.createElement('wa-callout');
    box.setAttribute('variant', 'danger');
    box.textContent = 'Failed to load: ' + e.message;
    $('state-loading').replaceChildren(box);
  }
}

boot();

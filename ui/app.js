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

// The list endpoint returns definition fields ONLY (no relations —
// definitionFieldsMap). Cards need options + project_ids, so each definition
// is followed by a ReadOne (N+1 over a handful of definitions — S9 spec).
async function loadList() {
  const defs = await api('/definitions');
  const items = await Promise.all(defs.map(async (d) => {
    try {
      const full = await api('/definitions/' + d.id);
      return { ...d, options: full.options || [], project_ids: full.project_ids || [] };
    } catch (e) {
      return { ...d, options: [], project_ids: [], relFailed: true };
    }
  }));
  items.sort((a, b) => (a.display_order || 0) - (b.display_order || 0));
  $('list').replaceChildren(...items.map(cardFor));
}

function metaLine(label, value) {
  const p = document.createElement('p');
  p.className = 'meta';
  const b = document.createElement('strong');
  b.textContent = label + ': ';
  p.appendChild(b);
  p.appendChild(document.createTextNode(value));
  return p;
}

function cardFor(item) {
  const card = document.createElement('wa-card');
  const header = document.createElement('div');
  header.className = 'wa-split';
  header.style.alignItems = 'center';
  const h = document.createElement('h3');
  h.textContent = item.name;
  const badge = document.createElement('wa-badge');
  badge.setAttribute('variant', 'neutral');
  badge.textContent = item.type;
  header.append(h, badge);

  const body = document.createElement('div');
  const fc = item.field_config || {};
  body.append(metaLine('Required', fc.required ? 'yes' : 'no'));
  if (fc.min != null || fc.max != null) {
    body.append(metaLine('Range', (fc.min ?? '−∞') + ' … ' + (fc.max ?? '∞')));
  }
  if (item.type === 'select' || item.type === 'multiselect') {
    body.append(metaLine('Options', String((item.options || []).length)));
  }
  body.append(metaLine('Assigned to', (item.project_ids || []).length === 0 ? 'All projects' : item.project_ids.length + ' project(s)'));
  if (fc.is_api_only) body.append(metaLine('API-only', 'yes'));
  if (item.description) body.append(metaLine('Description', item.description));
  if (item.relFailed) body.append(metaLine('Warning', 'details unavailable (read failed)'));

  const footer = document.createElement('div');
  footer.className = 'wa-split';
  footer.style.marginTop = '0.5rem';
  // Action buttons are added by later tasks (Edit in the form task, Delete in
  // the delete task) — the footer is intentionally empty here.

  card.append(header, body, footer);
  return card;
}

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
    try {
      await loadList();
    } catch (e) {
      // state-app is already visible, so the error callout must render where
      // the manager is looking — the list area — leaving actions usable.
      if (e.status === 401) return; // expired view already shown by api()
      console.error(e);
      const box = document.createElement('wa-callout');
      box.setAttribute('variant', 'danger');
      box.textContent = 'Failed to load: ' + e.message;
      $('list-error').replaceChildren(box);
      $('list-error').classList.remove('hidden');
    }
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

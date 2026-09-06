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
  const edit = document.createElement('wa-button');
  edit.setAttribute('appearance', 'plain');
  edit.textContent = 'Edit';
  edit.addEventListener('click', () => openForm(item.id));
  footer.append(edit);
  const del = document.createElement('wa-button');
  del.setAttribute('variant', 'danger');
  del.setAttribute('appearance', 'plain');
  del.textContent = 'Delete';
  del.addEventListener('click', () => confirmDelete(item.id));
  footer.append(del);

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

// ── Form (create + edit). formId === null → create.
let formId = null;
let formDef = null;

function draftKey() {
  return formId === null ? 'cf-mgmt-draft-new' : 'cf-mgmt-draft-' + formId;
}
function saveDraft(candidate) {
  try { localStorage.setItem(draftKey(), JSON.stringify(candidate)); } catch {}
}
function restoreDraft() {
  try {
    const raw = localStorage.getItem(draftKey());
    if (raw) return JSON.parse(raw);
  } catch {}
  return null;
}
function clearDraft() {
  try { localStorage.removeItem(draftKey()); } catch {}
}

async function openForm(id) {
  clearErrors();
  formId = id;
  if (id === null) {
    formDef = { name: '', type: 'text', description: '', display_order: 0, field_config: {}, options: [], project_ids: [] };
    $('form-title').textContent = 'New field';
  } else {
    formDef = await api('/definitions/' + id);
    $('form-title').textContent = 'Edit field';
  }
  await fillForm();
  $('list').classList.add('hidden');
  $('btn-new').classList.add('hidden');
  $('form-view').classList.remove('hidden');
}

function closeForm() {
  $('form-view').classList.add('hidden');
  $('list').classList.remove('hidden');
  $('btn-new').classList.remove('hidden');
}

function parseNum(v) {
  const s = String(v ?? '').trim();
  if (s === '') return null;
  const n = Number(s);
  return Number.isFinite(n) ? n : null;
}

async function fillForm() {
  const draft = restoreDraft();
  const c = draft || {
    name: formDef.name || '',
    type: formDef.type || 'text',
    description: formDef.description || '',
    display_order: formDef.display_order || 0,
    field_config: formDef.field_config || {},
    options: (formDef.options || []).map((o) => ({ value: o.value, label: o.label })),
    project_ids: formDef.project_ids || [],
  };
  $('f-name').value = c.name;
  $('f-description').value = c.description;
  $('f-order').value = c.display_order;
  $('f-type').value = c.type;
  $('f-required').checked = !!(c.field_config && c.field_config.required);
  $('f-api-only').checked = !!(c.field_config && c.field_config.is_api_only);
  $('f-default').value = (c.field_config && c.field_config.default) || '';
  $('f-min').value = (c.field_config && c.field_config.min) ?? '';
  $('f-max').value = (c.field_config && c.field_config.max) ?? '';
  const allProjects = (c.project_ids || []).length === 0;
  $('f-all-projects').checked = allProjects;
  $('block-assignment').classList.toggle('hidden', allProjects);
  const rows = $('options-rows');
  rows.replaceChildren();
  for (const o of c.options || []) addOptionRow(o);
  await fillProjects(c.project_ids || []);
  updateBlocks();
}

async function fillProjects(selectedIds) {
  const sel = $('f-projects');
  sel.replaceChildren();
  let projects = [];
  try {
    projects = await fetchJSONv1('/projects'); // first page only (paginated endpoint) — fine at proving-ground scale
  } catch (e) {
    projects = []; // picker stays empty; add-by-ID still works (existence-only validation)
  }
  for (const p of projects) {
    const o = document.createElement('wa-option');
    o.value = String(p.id);
    o.textContent = p.title;
    if (selectedIds.includes(p.id)) o.setAttribute('selected', '');
    sel.appendChild(o);
  }
  for (const pid of selectedIds) {
    if (!projects.some((p) => p.id === pid)) {
      const o = document.createElement('wa-option');
      o.value = String(pid);
      o.textContent = 'Project ' + pid;
      o.setAttribute('selected', '');
      sel.appendChild(o);
    }
  }
}

function updateBlocks() {
  const t = $('f-type').value;
  $('block-range').classList.toggle('hidden', !(t === 'integer' || t === 'decimal'));
  $('block-options').classList.toggle('hidden', !(t === 'select' || t === 'multiselect'));
}

function addOptionRow(opt) {
  const row = document.createElement('div');
  row.className = 'opt-row';
  const value = document.createElement('wa-input');
  value.className = 'opt-value';
  value.setAttribute('label', 'Value');
  value.setAttribute('placeholder', 'stored value');
  value.value = (opt && opt.value) || '';
  const label = document.createElement('wa-input');
  label.className = 'opt-label';
  label.setAttribute('label', 'Label');
  label.setAttribute('placeholder', 'shown label (optional)');
  label.value = (opt && opt.label) || '';
  const up = document.createElement('wa-button');
  up.setAttribute('appearance', 'plain');
  up.textContent = '↑';
  up.addEventListener('click', () => {
    if (row.previousElementSibling) row.previousElementSibling.before(row);
  });
  const down = document.createElement('wa-button');
  down.setAttribute('appearance', 'plain');
  down.textContent = '↓';
  down.addEventListener('click', () => {
    if (row.nextElementSibling) row.nextElementSibling.after(row);
  });
  const remove = document.createElement('wa-button');
  remove.setAttribute('variant', 'danger');
  remove.setAttribute('appearance', 'plain');
  remove.textContent = '✕';
  remove.addEventListener('click', () => row.remove());
  row.append(value, label, up, down, remove);
  $('options-rows').appendChild(row);
}

function collectOptions() {
  return [...document.querySelectorAll('#options-rows .opt-row')].map((row, i) => ({
    value: row.querySelector('.opt-value').value.trim(),
    label: row.querySelector('.opt-label').value.trim(),
    display_order: i,
  }));
}

function collectProjectIds() {
  const v = $('f-projects').value || [];
  const arr = Array.isArray(v) ? v : [v];
  return arr.map(Number).filter((n) => Number.isFinite(n));
}

function collectForm() {
  const type = $('f-type').value;
  const fc = {};
  if ($('f-required').checked) fc.required = true;
  const dflt = $('f-default').value.trim();
  if (dflt !== '') fc.default = dflt;
  const min = parseNum($('f-min').value);
  if (min !== null) fc.min = min;
  const max = parseNum($('f-max').value);
  if (max !== null) fc.max = max;
  if ($('f-api-only').checked) fc.is_api_only = true;
  return {
    name: $('f-name').value.trim(),
    type,
    description: $('f-description').value.trim(),
    field_config: fc,
    display_order: parseNum($('f-order').value) || 0,
    options: (type === 'select' || type === 'multiselect') ? collectOptions() : [],
    project_ids: $('f-all-projects').checked ? [] : collectProjectIds(),
  };
}

function clearErrors() {
  for (const id of ['err-name', 'err-type', 'err-options', 'err-assignment']) {
    const el = $(id);
    el.textContent = '';
    el.classList.add('hidden');
  }
  $('form-banner').classList.add('hidden');
}

function setFieldError(id, msg) {
  const el = $(id);
  el.textContent = msg;
  el.classList.remove('hidden');
}

function showFormError(e) {
  const msg = (e && e.message) || String(e);
  const m = msg.toLowerCase();
  if (m.includes('option')) setFieldError('err-options', msg);
  else if (m.includes('project')) setFieldError('err-assignment', msg);
  else if (m.includes('type')) setFieldError('err-type', msg);
  else if (m.includes('name')) setFieldError('err-name', msg);
  else {
    $('form-banner').textContent = msg;
    $('form-banner').classList.remove('hidden');
  }
}

// Promise-based confirm dialog. The body is built by buildBody(container) via
// DOM APIs — no innerHTML with data.
function askConfirm(label, buildBody, okLabel, okVariant) {
  return new Promise((resolve) => {
    const dlg = $('dialog-confirm');
    dlg.setAttribute('label', label);
    const body = $('dialog-body');
    body.replaceChildren();
    buildBody(body);
    const ok = $('dialog-ok');
    const cancel = $('dialog-cancel');
    ok.textContent = okLabel;
    ok.setAttribute('variant', okVariant);
    const done = (val) => {
      ok.removeEventListener('click', onOk);
      cancel.removeEventListener('click', onCancel);
      // wa-dialog has no hide() method — the open property is the documented
      // toggle (dialog.md: "Toggle this attribute to show and hide").
      dlg.open = false;
      resolve(val);
    };
    const onOk = () => done(true);
    const onCancel = () => done(false);
    ok.addEventListener('click', onOk);
    cancel.addEventListener('click', onCancel);
    dlg.open = true;
  });
}

async function saveForm() {
  clearErrors();
  const candidate = collectForm();
  saveDraft(candidate);
  try {
    if (formId !== null) {
      const imp = await api('/definitions/' + formId + '/impact', {
        method: 'POST',
        body: JSON.stringify(candidate),
      });
      if (imp.affected_values > 0) {
        const n = imp.affected_values;
        const ok = await askConfirm('Invalidating edit', (body) => {
          body.textContent = 'This edit invalidates ' + n + ' stored value(s). Save anyway?';
        }, 'Save anyway', 'brand');
        if (!ok) return;
      }
      await api('/definitions/' + formId, { method: 'PUT', body: JSON.stringify(candidate) });
      clearDraft();
    } else {
      await api('/definitions', { method: 'POST', body: JSON.stringify(candidate) });
      clearDraft();
    }
    closeForm();
    await loadList();
  } catch (e) {
    if (e.status === 401) return; // expired view already shown by api()
    showFormError(e);
  }
}

// wa-select/wa-checkbox emit the STANDARD change event — there is no wa-change.
$('f-type').addEventListener('change', updateBlocks);
$('f-all-projects').addEventListener('change', () => {
  $('block-assignment').classList.toggle('hidden', $('f-all-projects').checked);
});
$('btn-add-option').addEventListener('click', () => addOptionRow({}));
$('btn-add-project').addEventListener('click', () => {
  const pid = parseNum($('f-project-id').value);
  if (pid === null) return;
  const sel = $('f-projects');
  if (![...sel.children].some((o) => o.value === String(pid))) {
    const o = document.createElement('wa-option');
    o.value = String(pid);
    o.textContent = 'Project ' + pid;
    sel.appendChild(o);
  }
  // A wa-option's `selected` attribute only seeds the initial selection at
  // option-registration time; setting it on an already-connected option does
  // not update the select's value (the saved payload came out with
  // project_ids: []). The documented programmatic path is assigning the
  // select's value property — an array when multiple.
  const current = Array.isArray(sel.value) ? sel.value : (sel.value ? [sel.value] : []);
  sel.value = [...new Set([...current, String(pid)])];
  $('f-project-id').value = '';
});
$('btn-new').addEventListener('click', () => openForm(null));
$('btn-save').addEventListener('click', saveForm);
$('btn-cancel').addEventListener('click', closeForm);
// Draft persistence: every keystroke/change while the form is open.
for (const evt of ['input', 'change']) {
  $('form-view').addEventListener(evt, () => {
    try { saveDraft(collectForm()); } catch {}
  });
}

async function confirmDelete(id) {
  let n = 0;
  try {
    const imp = await api('/definitions/' + id + '/impact');
    n = imp.affected_values;
  } catch (e) {
    if (e.status === 401) return;
    // Count is a nicety; the cascade still warns below if the preview fails.
  }
  const ok = await askConfirm('Delete field', (body) => {
    body.textContent = 'This permanently deletes the field and its ' + n +
      ' stored value(s). This cannot be undone.';
  }, 'Delete field', 'danger');
  if (!ok) return;
  try {
    await api('/definitions/' + id, { method: 'DELETE' });
  } catch (e) {
    if (e.status === 401) return;
    const box = $('list-error');
    box.textContent = 'Delete failed: ' + e.message;
    box.classList.remove('hidden');
    return;
  }
  await loadList();
}

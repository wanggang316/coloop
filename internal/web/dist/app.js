// coloop admin SPA — vanilla JS, no framework, no build.
// Consumes the JSON API served by internal/api and renders three views:
//   #/                          task list
//   #/task/<name>               task detail + recent runs
//   #/task/<name>/run/<id>      live/replay log view
//
// Data contract (frozen):
//   GET /api/tasks            -> [{ task:Task, last_run?:Run, next_run?:RFC3339, running:bool }]
//   GET /api/tasks/{name}     -> { task:Task, runs:[Run], next_run?:RFC3339, running:bool }
//   Run = { id, task, trigger, status, exit_code, started_at, finished_at?, log_path? }

'use strict';

// ---------- tiny DOM + format helpers ----------

const $ = (sel, root = document) => root.querySelector(sel);

// el(tag, attrs, ...children) — minimal hyperscript with safe text handling.
function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null || v === false) continue;
      if (k === 'class') node.className = v;
      else if (k === 'html') node.innerHTML = v;
      else if (k.startsWith('on') && typeof v === 'function') node.addEventListener(k.slice(2), v);
      else if (k === 'dataset') Object.assign(node.dataset, v);
      else node.setAttribute(k, v === true ? '' : v);
    }
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    node.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return node;
}

function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }

// ---------- icons (inline Lucide SVG, no dependency) ----------

// Path data lifted from lucide.dev. Each value is the inner markup of a
// 24x24 stroke icon; icon() wraps it in a consistently-styled <svg>.
const ICONS = {
  repeat: '<path d="m17 2 4 4-4 4"/><path d="M3 11v-1a4 4 0 0 1 4-4h14"/><path d="m7 22-4-4 4-4"/><path d="M21 13v1a4 4 0 0 1-4 4H3"/>',
  settings: '<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>',
  'refresh-cw': '<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/>',
  'rotate-ccw': '<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/><path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/><path d="M2 12h2"/><path d="M20 12h2"/><path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/>',
  moon: '<path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/>',
  play: '<polygon points="6 3 20 12 6 21 6 3"/>',
  pencil: '<path d="M12 20h9"/><path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4Z"/>',
  trash: '<path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><line x1="10" x2="10" y1="11" y2="17"/><line x1="14" x2="14" y1="11" y2="17"/>',
  plus: '<path d="M5 12h14"/><path d="M12 5v14"/>',
  // pretty-view activity icons
  'book-open': '<path d="M12 7v14"/><path d="M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z"/>',
  search: '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
  terminal: '<polyline points="4 17 10 11 4 5"/><line x1="12" x2="20" y1="19" y2="19"/>',
  globe: '<circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/><path d="M2 12h20"/>',
  sparkles: '<path d="M9.937 15.5A2 2 0 0 0 8.5 14.063l-6.135-1.582a.5.5 0 0 1 0-.962L8.5 9.936A2 2 0 0 0 9.937 8.5l1.582-6.135a.5.5 0 0 1 .963 0L14.063 8.5A2 2 0 0 0 15.5 9.937l6.135 1.581a.5.5 0 0 1 0 .964L15.5 14.063a2 2 0 0 0-1.437 1.437l-1.582 6.135a.5.5 0 0 1-.963 0z"/><path d="M20 3v4"/><path d="M22 5h-4"/>',
  wrench: '<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>',
  lightbulb: '<path d="M15 14c.2-1 .7-1.7 1.5-2.5 1-.9 1.5-2.2 1.5-3.5A6 6 0 0 0 6 8c0 1 .2 2.2 1.5 3.5.7.7 1.3 1.5 1.5 2.5"/><path d="M9 18h6"/><path d="M10 22h4"/>',
  'chevron-right': '<path d="m9 18 6-6-6-6"/>',
  zap: '<path d="M4 14a1 1 0 0 1-.78-1.63l9.9-10.2a.5.5 0 0 1 .86.46l-1.92 6.02A1 1 0 0 0 13 10h7a1 1 0 0 1 .78 1.63l-9.9 10.2a.5.5 0 0 1-.86-.46l1.92-6.02A1 1 0 0 0 11 14z"/>',
};

// icon builds a 24x24 stroke SVG that inherits the current text color and size.
function icon(name, cls) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '2');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  svg.setAttribute('class', 'icon' + (cls ? ' ' + cls : ''));
  svg.setAttribute('aria-hidden', 'true');
  svg.innerHTML = ICONS[name] || '';
  return svg;
}

// ---------- theme (light / dark) ----------

function currentTheme() { return document.documentElement.dataset.theme === 'light' ? 'light' : 'dark'; }
function applyTheme(t) {
  document.documentElement.dataset.theme = t;
  try { localStorage.setItem('theme', t); } catch (e) { /* private mode */ }
  updateThemeIcon();
}
function toggleTheme() { applyTheme(currentTheme() === 'light' ? 'dark' : 'light'); }
// Show the icon of the mode you'll switch TO: sun in dark, moon in light.
function updateThemeIcon() {
  const b = $('#themeBtn');
  if (b) b.replaceChildren(icon(currentTheme() === 'dark' ? 'sun' : 'moon'));
}

const STATUSES = ['success', 'failed', 'timeout', 'running', 'pending'];

function statusBadge(status) {
  const s = STATUSES.includes(status) ? status : 'none';
  return el('span', { class: 'badge ' + s }, status || 'never run');
}

// Render an RFC3339/empty timestamp as a readable local string.
function fmtTime(ts) {
  if (!ts) return '—';
  const d = new Date(ts);
  if (isNaN(d)) return '—';
  // Detect Go's zero time ("0001-01-01...") which JSON-encodes far in the past.
  if (d.getUTCFullYear() <= 1) return '—';
  return d.toLocaleString(undefined, {
    month: 'short', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  });
}

// Relative "x ago" for compact columns.
function fmtAgo(ts) {
  if (!ts) return '—';
  const d = new Date(ts);
  if (isNaN(d) || d.getUTCFullYear() <= 1) return '—';
  const sec = Math.round((Date.now() - d.getTime()) / 1000);
  if (sec < 0) return fmtTime(ts);
  if (sec < 60) return sec + 's ago';
  if (sec < 3600) return Math.floor(sec / 60) + 'm ago';
  if (sec < 86400) return Math.floor(sec / 3600) + 'h ago';
  return Math.floor(sec / 86400) + 'd ago';
}

// "next run" forward-looking relative time.
function fmtNext(ts) {
  if (!ts) return '—';
  const d = new Date(ts);
  if (isNaN(d) || d.getUTCFullYear() <= 1) return '—';
  const sec = Math.round((d.getTime() - Date.now()) / 1000);
  if (sec <= 0) return 'due';
  if (sec < 60) return 'in ' + sec + 's';
  if (sec < 3600) return 'in ' + Math.floor(sec / 60) + 'm';
  if (sec < 86400) return 'in ' + Math.floor(sec / 3600) + 'h';
  return fmtTime(ts);
}

// Duration of a run, from its timestamps; live for unfinished runs.
function fmtDuration(run) {
  if (!run || !run.started_at) return '—';
  const start = new Date(run.started_at).getTime();
  const end = run.finished_at && new Date(run.finished_at).getUTCFullYear() > 1
    ? new Date(run.finished_at).getTime()
    : (run.status === 'running' ? Date.now() : start);
  let ms = end - start;
  if (ms < 0) ms = 0;
  if (ms < 1000) return ms + 'ms';
  const s = ms / 1000;
  if (s < 60) return s.toFixed(1) + 's';
  const m = Math.floor(s / 60), rem = Math.round(s % 60);
  if (m < 60) return m + 'm ' + rem + 's';
  const h = Math.floor(m / 60);
  return h + 'h ' + (m % 60) + 'm';
}

// ---------- API client ----------

async function api(path, opts) {
  const res = await fetch(path, opts);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body && body.error) msg = body.error;
    } catch (_) { /* non-JSON error body */ }
    const err = new Error(msg);
    err.status = res.status;
    throw err;
  }
  const ct = res.headers.get('content-type') || '';
  return ct.includes('application/json') ? res.json() : res.text();
}

const Tasks = {
  list: () => api('/api/tasks'),
  detail: (name) => api('/api/tasks/' + encodeURIComponent(name)),
  create: (t) => api('/api/tasks', jsonOpts('POST', t)),
  update: (name, t) => api('/api/tasks/' + encodeURIComponent(name), jsonOpts('PUT', t)),
  remove: (name) => api('/api/tasks/' + encodeURIComponent(name), { method: 'DELETE' }),
  run: (name) => api('/api/tasks/' + encodeURIComponent(name) + '/run', { method: 'POST' }),
  runLog: (name, id) => api(`/api/tasks/${encodeURIComponent(name)}/runs/${encodeURIComponent(id)}/log`),
  reload: () => api('/api/reload', { method: 'POST' }),
};

// Runners are the named agent command templates a task selects by name.
const Runners = {
  list: () => api('/api/runners'),
  create: (r) => api('/api/runners', jsonOpts('POST', r)),
  update: (name, r) => api('/api/runners/' + encodeURIComponent(name), jsonOpts('PUT', r)),
  remove: (name) => api('/api/runners/' + encodeURIComponent(name), { method: 'DELETE' }),
};

// Channels are the named notifier channels a task selects to receive alerts.
const Channels = {
  list: () => api('/api/notifiers'),
  create: (c) => api('/api/notifiers', jsonOpts('POST', c)),
  update: (name, c) => api('/api/notifiers/' + encodeURIComponent(name), jsonOpts('PUT', c)),
  remove: (name) => api('/api/notifiers/' + encodeURIComponent(name), { method: 'DELETE' }),
};

// jsonOpts builds fetch options for a JSON request body.
function jsonOpts(method, body) {
  return { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) };
}

// ---------- transient toast ----------

let toastTimer = null;
function toast(msg, kind = 'ok') {
  const t = $('#toast');
  t.textContent = msg;
  t.className = 'toast ' + kind;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, 3200);
}

// ---------- task editor (create / edit / delete) ----------

// modal mounts node in a dismissable overlay (click-outside or Esc) and returns
// a close() handle.
function modal(node) {
  const overlay = el('div', { class: 'modal-overlay' }, node);
  function onKey(ev) { if (ev.key === 'Escape') close(); }
  function close() { document.removeEventListener('keydown', onKey); overlay.remove(); }
  overlay.addEventListener('click', (ev) => { if (ev.target === overlay) close(); });
  document.addEventListener('keydown', onKey);
  document.body.append(overlay);
  return { close };
}

// confirmModal asks a yes/no question. It deliberately avoids window.confirm,
// which blocks the event loop. onYes may be async and runs with the button busy.
function confirmModal(title, body, confirmLabel, onYes) {
  const yes = el('button', { type: 'button', class: 'btn primary danger' }, confirmLabel);
  const m = modal(el('div', { class: 'modal-card sm' },
    el('h2', null, title),
    el('p', { class: 'modal-body' }, body),
    el('div', { class: 'form-actions' },
      el('button', { type: 'button', class: 'btn', onclick: () => m.close() }, 'cancel'),
      yes)));
  yes.addEventListener('click', async () => {
    yes.disabled = true;
    try { await onYes(); m.close(); }
    catch (e) { toast(e.message, 'err'); yes.disabled = false; }
  });
}

// formToTask reads the editor's field nodes into a Task payload for the API.
function formToTask(f) {
  const type = f.type.value;
  const t = {
    name: f.name.value.trim(),
    type,
    cron: f.cron.value.trim(),
    working_dir: f.working_dir.value.trim(),
    alert: f.alert.value,
    enabled: f.enabled.checked,
  };
  if (type === 'agent') {
    t.runner = f.runner.value;
    t.prompt = f.prompt.value;
  } else {
    t.command = f.command.value;
  }
  if (f.notifierBoxes) {
    t.notifiers = f.notifierBoxes.filter((b) => b.input.checked).map((b) => b.name);
  }
  const to = f.timeout.value.trim();
  if (to) t.timeout = to;
  const env = {};
  for (const line of f.env.value.split('\n')) {
    const s = line.trim();
    if (!s) continue;
    const eq = s.indexOf('=');
    if (eq > 0) env[s.slice(0, eq).trim()] = s.slice(eq + 1);
  }
  if (Object.keys(env).length) t.env = env;
  return t;
}

// openTaskForm shows the create form (existing omitted) or an edit form.
// onSaved(newName, oldName) runs after a successful save (oldName is null on create).
// It loads the runner registry first so agent tasks select a runner by name.
async function openTaskForm(existing, onSaved) {
  const editing = !!existing;
  const t = existing || { type: 'shell', enabled: true };
  const f = {};

  let runners = [];
  let channels = [];
  try {
    const [r, c] = await Promise.all([Runners.list(), Channels.list()]);
    runners = r || [];
    channels = c || [];
  } catch (_) { /* offer empty lists */ }

  const field = (label, node, hint) => el('label', { class: 'field' },
    el('span', { class: 'field-label' }, label),
    node,
    hint ? el('span', { class: 'field-hint' }, hint) : null);

  f.name = el('input', { type: 'text', value: t.name || '', placeholder: 'my-task', autocomplete: 'off', spellcheck: 'false' });
  f.type = el('select', null,
    el('option', { value: 'shell', selected: t.type !== 'agent' }, 'shell'),
    el('option', { value: 'agent', selected: t.type === 'agent' }, 'agent'));
  f.cron = el('input', { type: 'text', value: t.cron || '', placeholder: '0 9 * * *  (empty = manual only)', autocomplete: 'off', spellcheck: 'false' });
  f.working_dir = el('input', { type: 'text', value: t.working_dir || '', placeholder: '~/project', autocomplete: 'off', spellcheck: 'false' });
  f.command = el('textarea', { rows: '2', placeholder: './run.sh', spellcheck: 'false' }, t.command || '');
  f.prompt = el('textarea', { rows: '2', placeholder: 'Inspect logs and summarise.' }, t.prompt || '');
  const runnerNames = runners.map((r) => r.name);
  // Keep an unknown/legacy reference visible so editing doesn't silently drop it.
  if (t.runner && !runnerNames.includes(t.runner)) runnerNames.unshift(t.runner);
  f.runner = el('select', null,
    el('option', { value: '', selected: !t.runner },
      runnerNames.length ? '— select a runner —' : '— no runners defined —'),
    ...runnerNames.map((n) => el('option', { value: n, selected: t.runner === n }, n)));
  f.timeout = el('input', { type: 'text', value: t.timeout && t.timeout !== '0s' ? t.timeout : '', placeholder: '1h (default)', autocomplete: 'off', spellcheck: 'false' });
  f.alert = el('select', null,
    ...['on_failure', 'on_success', 'always', 'never'].map(
      (a) => el('option', { value: a, selected: (t.alert || 'on_failure') === a }, a)));
  f.enabled = el('input', { type: 'checkbox', checked: t.enabled !== false });
  f.env = el('textarea', { rows: '2', placeholder: 'ENV=prod', spellcheck: 'false' },
    t.env ? Object.entries(t.env).map(([k, v]) => `${k}=${v}`).join('\n') : '');

  // Notifier channels: a checkbox per channel; no selection means no alerts.
  const selected = new Set(t.notifiers || []);
  f.notifierBoxes = channels.map((c) => ({
    name: c.name,
    input: el('input', { type: 'checkbox', checked: selected.has(c.name) }),
  }));
  const notifiersNode = f.notifierBoxes.length
    ? el('div', { class: 'check-grid' },
        ...f.notifierBoxes.map((b) => el('label', { class: 'field-check' }, b.input,
          el('span', null, b.name))))
    : el('div', { class: 'field-hint' }, 'no channels defined — add one in Settings → Channels');

  const shellRow = field('command', f.command, 'run via the configured shell');
  const promptRow = field('prompt', f.prompt);
  const legacyTmpl = t.cmd_tmpl && !t.runner;
  const tmplRow = field('runner', f.runner,
    legacyTmpl
      ? 'this task uses an inline cmd_tmpl; pick a runner to replace it · manage in Settings → Runners'
      : 'agent command template · manage in Settings → Runners');

  function syncType() {
    const agent = f.type.value === 'agent';
    shellRow.hidden = agent;
    promptRow.hidden = !agent;
    tmplRow.hidden = !agent;
  }
  f.type.addEventListener('change', syncType);

  const err = el('div', { class: 'form-err', hidden: true });
  const save = el('button', { type: 'submit', class: 'btn primary' }, editing ? 'save' : 'create');

  async function onSubmit(ev) {
    ev.preventDefault();
    const task = formToTask(f);
    if (!task.name) { err.textContent = 'name is required'; err.hidden = false; return; }
    save.disabled = true;
    err.hidden = true;
    try {
      const saved = editing ? await Tasks.update(existing.name, task) : await Tasks.create(task);
      m.close();
      toast(editing ? `Saved ${saved.name}` : `Created ${saved.name}`, 'ok');
      if (onSaved) onSaved(saved.name, editing ? existing.name : null);
    } catch (e) {
      err.textContent = e.message;
      err.hidden = false;
      save.disabled = false;
    }
  }

  const m = modal(el('div', { class: 'modal-card' },
    el('h2', null, editing ? 'Edit task' : 'New task'),
    el('form', { class: 'task-form', onsubmit: onSubmit },
      field('name', f.name),
      el('div', { class: 'field-row' }, field('type', f.type), field('schedule (cron)', f.cron)),
      field('working dir', f.working_dir),
      shellRow, promptRow, tmplRow,
      el('div', { class: 'field-row' }, field('timeout', f.timeout), field('alert', f.alert)),
      field('notify channels', notifiersNode, 'alert policy decides whether; these decide where'),
      field('env (KEY=VALUE per line)', f.env),
      el('label', { class: 'field-check' }, f.enabled, el('span', null, 'enabled')),
      err,
      el('div', { class: 'form-actions' },
        el('button', { type: 'button', class: 'btn', onclick: () => m.close() }, 'cancel'),
        save))));
  syncType();
  f.name.focus();
}

// ---------- settings: shared registries (runners; channels added later) ----------

// openSettings shows a tabbed modal for managing the registries that tasks
// reference by name. Each tab CRUDs its registry in place.
function openSettings(initialTab = 'runners') {
  const tabsDef = [
    { id: 'runners', label: 'Runners', render: renderRunnersTab },
    { id: 'channels', label: 'Channels', render: renderChannelsTab },
  ];
  const body = el('div', { class: 'settings-body' });
  const tabBar = el('div', { class: 'tabs' });
  function select(id) {
    for (const b of tabBar.children) b.classList.toggle('active', b.dataset.tab === id);
    clear(body);
    (tabsDef.find((t) => t.id === id) || tabsDef[0]).render(body);
  }
  for (const t of tabsDef) {
    tabBar.append(el('button',
      { type: 'button', class: 'tab', dataset: { tab: t.id }, onclick: () => select(t.id) }, t.label));
  }
  modal(el('div', { class: 'modal-card lg' },
    el('h2', null, 'Settings'),
    tabBar, body));
  select(tabsDef.some((t) => t.id === initialTab) ? initialTab : tabsDef[0].id);
}

// renderRunnersTab lists the agent runners with add / edit / delete.
function renderRunnersTab(container) {
  const slot = el('div', null, loadingState());
  const head = el('div', { class: 'settings-head' },
    el('p', { class: 'settings-hint' }, 'Reusable agent command templates. Agent tasks pick a runner by name.'),
    el('span', { class: 'spacer' }),
    el('button', { class: 'btn primary sm', onclick: () => openRunnerForm(null, reload) }, icon('plus'), 'Add runner'));
  container.append(head, slot);

  async function reload() {
    clear(slot);
    slot.append(loadingState());
    try {
      const runners = (await Runners.list()) || [];
      clear(slot);
      if (!runners.length) {
        slot.append(emptyState('No runners yet', 'Add one to use it in agent tasks.'));
        return;
      }
      const rows = runners.map((r) => el('div', { class: 'reg-row' },
        el('div', { class: 'reg-main' },
          el('div', { class: 'reg-name' }, r.name),
          el('div', { class: 'reg-sub mono' }, r.cmd_tmpl)),
        el('div', { class: 'reg-actions' },
          el('button', { class: 'btn sm', onclick: () => openRunnerForm(r, reload) }, icon('pencil'), 'edit'),
          el('button', { class: 'btn sm danger', onclick: () => askDelete(r) }, icon('trash'), 'delete'))));
      slot.append(el('div', { class: 'reg-list' }, rows));
    } catch (e) {
      clear(slot);
      slot.append(errorState(e.message, reload));
    }
  }

  function askDelete(r) {
    confirmModal('Delete runner', `Delete runner "${r.name}"? Tasks still using it must be updated first.`,
      'delete', async () => { await Runners.remove(r.name); toast(`Deleted ${r.name}`, 'ok'); reload(); });
  }

  reload();
}

// openRunnerForm shows a small create / edit form for a single runner.
function openRunnerForm(existing, onSaved) {
  const editing = !!existing;
  const r = existing || {};

  const field = (label, node, hint) => el('label', { class: 'field' },
    el('span', { class: 'field-label' }, label), node,
    hint ? el('span', { class: 'field-hint' }, hint) : null);

  const name = el('input', { type: 'text', value: r.name || '', placeholder: 'claude-code', autocomplete: 'off', spellcheck: 'false' });
  const cmd = el('textarea', { rows: '3', placeholder: 'claude -p "{{.Prompt}}" --output-format stream-json --verbose --permission-mode acceptEdits', spellcheck: 'false' }, r.cmd_tmpl || '');
  const err = el('div', { class: 'form-err', hidden: true });
  const save = el('button', { type: 'submit', class: 'btn primary' }, editing ? 'save' : 'create');

  async function onSubmit(ev) {
    ev.preventDefault();
    const payload = { name: name.value.trim(), cmd_tmpl: cmd.value.trim() };
    if (!payload.name) { err.textContent = 'name is required'; err.hidden = false; return; }
    if (!payload.cmd_tmpl) { err.textContent = 'command template is required'; err.hidden = false; return; }
    save.disabled = true;
    err.hidden = true;
    try {
      const saved = editing ? await Runners.update(existing.name, payload) : await Runners.create(payload);
      m.close();
      toast(editing ? `Saved ${saved.name}` : `Created ${saved.name}`, 'ok');
      if (onSaved) onSaved();
    } catch (e) {
      err.textContent = e.message;
      err.hidden = false;
      save.disabled = false;
    }
  }

  const m = modal(el('div', { class: 'modal-card' },
    el('h2', null, editing ? 'Edit runner' : 'New runner'),
    el('form', { class: 'task-form', onsubmit: onSubmit },
      field('name', name),
      field('command template', cmd, '{{.Prompt}} {{.TaskName}} {{.WorkingDir}} are rendered'),
      err,
      el('div', { class: 'form-actions' },
        el('button', { type: 'button', class: 'btn', onclick: () => m.close() }, 'cancel'),
        save))));
  name.focus();
}

// channelSummary returns a short, secret-free description of a channel.
function channelSummary(c) {
  if (c.type === 'feishu') return c.feishu && c.feishu.cli_path ? 'feishu · cli' : 'feishu · webhook';
  if (c.type === 'telegram') return c.telegram ? `telegram · chat ${c.telegram.chat_id || '?'}` : 'telegram';
  return c.type || '—';
}

// renderChannelsTab lists the notifier channels with add / edit / delete.
function renderChannelsTab(container) {
  const slot = el('div', null, loadingState());
  const head = el('div', { class: 'settings-head' },
    el('p', { class: 'settings-hint' }, 'Named alert channels. Tasks pick which channels receive their alerts.'),
    el('span', { class: 'spacer' }),
    el('button', { class: 'btn primary sm', onclick: () => openChannelForm(null, reload) }, icon('plus'), 'Add channel'));
  container.append(head, slot);

  async function reload() {
    clear(slot);
    slot.append(loadingState());
    try {
      const channels = (await Channels.list()) || [];
      clear(slot);
      if (!channels.length) {
        slot.append(emptyState('No channels yet', 'Add one, then select it on a task to get alerts.'));
        return;
      }
      const rows = channels.map((c) => el('div', { class: 'reg-row' },
        el('div', { class: 'reg-main' },
          el('div', { class: 'reg-name' }, c.name, ' ', el('span', { class: 'tag' }, c.type)),
          el('div', { class: 'reg-sub' }, channelSummary(c))),
        el('div', { class: 'reg-actions' },
          el('button', { class: 'btn sm', onclick: () => openChannelForm(c, reload) }, icon('pencil'), 'edit'),
          el('button', { class: 'btn sm danger', onclick: () => askDelete(c) }, icon('trash'), 'delete'))));
      slot.append(el('div', { class: 'reg-list' }, rows));
    } catch (e) {
      clear(slot);
      slot.append(errorState(e.message, reload));
    }
  }

  function askDelete(c) {
    confirmModal('Delete channel', `Delete channel "${c.name}"? Tasks still using it must be updated first.`,
      'delete', async () => { await Channels.remove(c.name); toast(`Deleted ${c.name}`, 'ok'); reload(); });
  }

  reload();
}

// openChannelForm shows a create / edit form for a single notifier channel,
// toggling the credential fields to match the selected type.
function openChannelForm(existing, onSaved) {
  const editing = !!existing;
  const c = existing || { type: 'feishu' };
  const fs = c.feishu || {};
  const tg = c.telegram || {};
  const f = {};

  const field = (label, node, hint) => el('label', { class: 'field' },
    el('span', { class: 'field-label' }, label), node,
    hint ? el('span', { class: 'field-hint' }, hint) : null);
  const input = (val, ph) => el('input', { type: 'text', value: val || '', placeholder: ph || '', autocomplete: 'off', spellcheck: 'false' });

  f.name = input(c.name, 'team-feishu');
  f.type = el('select', null,
    el('option', { value: 'feishu', selected: c.type !== 'telegram' }, 'feishu'),
    el('option', { value: 'telegram', selected: c.type === 'telegram' }, 'telegram'));
  f.webhook = input(fs.webhook, 'https://open.feishu.cn/open-apis/bot/v2/hook/…');
  f.secret = input(fs.secret, 'signing secret (optional)');
  f.cli_path = input(fs.cli_path, 'or path to a feishu CLI binary');
  f.bot_token = input(tg.bot_token, '123456:ABC-…');
  f.chat_id = input(tg.chat_id, '-1001234567890');
  f.api_base = input(tg.api_base, 'https://api.telegram.org (override, optional)');

  const feishuRows = [
    field('webhook', f.webhook),
    field('secret', f.secret),
    field('cli_path', f.cli_path, 'provide a webhook or a cli_path'),
  ];
  const telegramRows = [
    field('bot_token', f.bot_token),
    field('chat_id', f.chat_id),
    field('api_base', f.api_base),
  ];

  function syncType() {
    const tgSel = f.type.value === 'telegram';
    for (const r of feishuRows) r.hidden = tgSel;
    for (const r of telegramRows) r.hidden = !tgSel;
  }
  f.type.addEventListener('change', syncType);

  const err = el('div', { class: 'form-err', hidden: true });
  const save = el('button', { type: 'submit', class: 'btn primary' }, editing ? 'save' : 'create');

  async function onSubmit(ev) {
    ev.preventDefault();
    const type = f.type.value;
    const payload = { name: f.name.value.trim(), type };
    if (type === 'feishu') {
      payload.feishu = { webhook: f.webhook.value.trim(), secret: f.secret.value.trim(), cli_path: f.cli_path.value.trim() };
    } else {
      payload.telegram = { bot_token: f.bot_token.value.trim(), chat_id: f.chat_id.value.trim(), api_base: f.api_base.value.trim() };
    }
    if (!payload.name) { err.textContent = 'name is required'; err.hidden = false; return; }
    save.disabled = true;
    err.hidden = true;
    try {
      const saved = editing ? await Channels.update(existing.name, payload) : await Channels.create(payload);
      m.close();
      toast(editing ? `Saved ${saved.name}` : `Created ${saved.name}`, 'ok');
      if (onSaved) onSaved();
    } catch (e) {
      err.textContent = e.message;
      err.hidden = false;
      save.disabled = false;
    }
  }

  const m = modal(el('div', { class: 'modal-card' },
    el('h2', null, editing ? 'Edit channel' : 'New channel'),
    el('form', { class: 'task-form', onsubmit: onSubmit },
      field('name', f.name),
      field('type', f.type),
      ...feishuRows, ...telegramRows,
      err,
      el('div', { class: 'form-actions' },
        el('button', { type: 'button', class: 'btn', onclick: () => m.close() }, 'cancel'),
        save))));
  syncType();
  f.name.focus();
}

// ---------- router ----------

// Active view owns a cleanup() to stop polling / close EventSource on nav away.
let active = { cleanup: () => {} };

function parseRoute() {
  const h = (location.hash || '#/').replace(/^#/, '');
  const parts = h.split('/').filter(Boolean); // ["task","name","run","id"]
  if (parts[0] === 'task' && parts[1]) {
    const name = decodeURIComponent(parts[1]);
    if (parts[2] === 'run' && parts[3]) {
      return { view: 'log', name, id: decodeURIComponent(parts[3]) };
    }
    return { view: 'detail', name };
  }
  return { view: 'list' };
}

function setCrumbs(route) {
  const c = $('#crumbs');
  clear(c);
  c.append(el('a', { href: '#/' }, 'tasks'));
  if (route.view === 'detail' || route.view === 'log') {
    c.append(el('span', { class: 'sep' }, '/'));
    const isHere = route.view === 'detail';
    c.append(isHere
      ? el('span', { class: 'here' }, route.name)
      : el('a', { href: '#/task/' + encodeURIComponent(route.name) }, route.name));
  }
  if (route.view === 'log') {
    c.append(el('span', { class: 'sep' }, '/'));
    c.append(el('span', { class: 'here' }, 'run #' + route.id));
  }
}

function navigate() {
  active.cleanup();
  active = { cleanup: () => {} };
  const route = parseRoute();
  setCrumbs(route);
  const root = $('#view');
  clear(root);
  // The header refresh button is meaningful on the list/detail views only.
  $('#refreshBtn').disabled = route.view === 'log';
  if (route.view === 'list') renderList(root);
  else if (route.view === 'detail') renderDetail(root, route.name);
  else if (route.view === 'log') renderLog(root, route.name, route.id);
}

// ---------- shared render bits ----------

function loadingState(label = 'loading…') {
  return el('div', { class: 'state' }, el('span', { class: 'spinner' }), ' ', label);
}
function errorState(msg, onRetry) {
  return el('div', { class: 'state err' },
    el('div', { class: 'big' }, 'Something went wrong'),
    el('div', { class: 'mono' }, msg),
    onRetry ? el('div', { style: 'margin-top:14px' },
      el('button', { class: 'btn', onclick: onRetry }, 'retry')) : null);
}
function emptyState(big, sub) {
  return el('div', { class: 'state' },
    el('div', { class: 'big' }, big),
    sub ? el('div', null, sub) : null);
}

function tickPoll() {
  const p = $('#poll');
  p.classList.add('tick');
  setTimeout(() => p.classList.remove('tick'), 250);
}

// ========================================================================
// VIEW 1 — task list
// ========================================================================

const POLL_MS = 3000;

function renderList(root) {
  const head = el('div', { class: 'page-head' },
    el('h1', null, 'Tasks'),
    el('span', { class: 'meta', id: 'listMeta' }, ''),
    el('span', { class: 'spacer' }),
    el('button', { class: 'btn primary sm', onclick: () => openTaskForm(null, () => load(true)) }, icon('plus'), 'New task'));
  const slot = el('div', { id: 'listSlot' }, loadingState());
  root.append(head, slot);

  let timer = null;
  let stopped = false;

  async function load(silent) {
    if (stopped) return;
    if (silent) tickPoll();
    try {
      const tasks = await Tasks.list();
      if (stopped) return;
      $('#listMeta').textContent = tasks.length
        ? `${tasks.length} task${tasks.length === 1 ? '' : 's'}`
        : '';
      clear(slot);
      slot.append(tasks.length ? taskTable(tasks) : emptyState(
        'No tasks defined',
        'Add tasks to config.yaml and reload.'));
    } catch (e) {
      if (stopped) return;
      clear(slot);
      slot.append(errorState(e.message, () => load(false)));
    }
  }

  // Run a task without leaving the list; refresh shortly after to show "running".
  async function runTask(name, btn) {
    btn.disabled = true;
    const saved = Array.from(btn.childNodes);
    btn.replaceChildren(el('span', { class: 'spinner' }));
    try {
      const r = await Tasks.run(name);
      toast(`Triggered ${name} (run #${r.run_id})`, 'ok');
      setTimeout(() => load(true), 400);
    } catch (e) {
      toast('Run failed: ' + e.message, 'err');
    } finally {
      btn.disabled = false;
      btn.replaceChildren(...saved);
    }
  }

  function taskTable(tasks) {
    const rows = tasks.map((tv) => {
      const t = tv.task || {};
      const last = tv.last_run;
      const status = tv.running ? 'running' : (last ? last.status : null);
      const tr = el('tr', { class: 'clickable' },
        el('td', { class: 'name' }, t.name),
        el('td', { class: 'hide-sm' }, el('span', { class: 'tag' }, t.type || '—')),
        el('td', { class: 'mono hide-sm' }, t.cron ? t.cron : el('span', { class: 'muted' }, 'manual')),
        el('td', null, t.enabled === false
          ? el('span', { class: 'badge none' }, 'disabled')
          : el('span', { class: 'dim' }, 'enabled')),
        el('td', null, statusBadge(status)),
        el('td', { class: 'dim nowrap hide-sm' }, t.cron ? fmtNext(tv.next_run) : '—'),
        el('td', { class: 'right' },
          el('button', {
            class: 'btn primary sm icon-only',
            title: 'Trigger a manual run',
            onclick: (ev) => { ev.stopPropagation(); runTask(t.name, ev.currentTarget); },
          }, icon('play'))));
      tr.addEventListener('click', () => {
        location.hash = '#/task/' + encodeURIComponent(t.name);
      });
      return tr;
    });

    return el('div', { class: 'table-wrap' },
      el('table', null,
        el('thead', null, el('tr', null,
          el('th', null, 'Name'),
          el('th', { class: 'hide-sm' }, 'Type'),
          el('th', { class: 'hide-sm' }, 'Schedule'),
          el('th', null, 'Enabled'),
          el('th', null, 'Last run'),
          el('th', { class: 'hide-sm' }, 'Next run'),
          el('th', { class: 'right' }, ''))),
        el('tbody', null, rows)));
  }

  load(false);
  timer = setInterval(() => load(true), POLL_MS);

  active.cleanup = () => { stopped = true; clearInterval(timer); };
  active.refresh = () => load(true);
}

// ========================================================================
// VIEW 2 — task detail + recent runs
// ========================================================================

function renderDetail(root, name) {
  const head = el('div', { class: 'page-head' },
    el('h1', null, name),
    el('span', { class: 'meta', id: 'detailMeta' }, ''));
  const slot = el('div', { id: 'detailSlot' }, loadingState());
  root.append(head, slot);

  let timer = null;
  let stopped = false;

  async function load(silent) {
    if (stopped) return;
    if (silent) tickPoll();
    try {
      const d = await Tasks.detail(name);
      if (stopped) return;
      render(d);
    } catch (e) {
      if (stopped) return;
      clear(slot);
      if (e.status === 404) {
        slot.append(emptyState('Task not found', el('a', { href: '#/' }, '← back to tasks')));
      } else {
        slot.append(errorState(e.message, () => load(false)));
      }
    }
  }

  async function runTask(btn) {
    btn.disabled = true;
    try {
      const r = await Tasks.run(name);
      toast(`Triggered ${name} (run #${r.run_id})`, 'ok');
      setTimeout(() => load(true), 400);
    } catch (e) {
      toast('Run failed: ' + e.message, 'err');
    } finally {
      btn.disabled = false;
    }
  }

  // After an edit: follow a rename to the new URL, otherwise just refresh.
  function onEdited(newName, oldName) {
    if (oldName && newName !== oldName) location.hash = '#/task/' + encodeURIComponent(newName);
    else load(true);
  }

  function deleteTask() {
    confirmModal('Delete task',
      `Delete "${name}"? It is removed from config.yaml. Run history on disk is kept.`,
      'delete', async () => {
        await Tasks.remove(name);
        toast(`Deleted ${name}`, 'ok');
        location.hash = '#/';
      });
  }

  function configCard(t) {
    const rows = [];
    const kv = (k, v, mono = true) => {
      rows.push(el('div', { class: 'k' }, k));
      rows.push(el('div', { class: 'v' + (mono ? '' : ' sans') }, v == null || v === '' ? '—' : v));
    };
    kv('type', t.type);
    kv('schedule', t.cron ? t.cron : 'manual only');
    kv('enabled', t.enabled === false ? 'no' : 'yes');
    kv('working dir', t.working_dir);
    if (t.type === 'agent') {
      kv('prompt', t.prompt);
      kv('runner', t.runner);
      if (t.cmd_tmpl && !t.runner) kv('cmd template', t.cmd_tmpl);
    } else {
      kv('command', t.command);
    }
    kv('timeout', t.timeout && t.timeout !== '0s' ? t.timeout : 'none');
    kv('alert', t.alert || 'on_failure');
    kv('notify channels', t.notifiers && t.notifiers.length ? t.notifiers.join(', ') : 'none');
    const env = t.env && Object.keys(t.env).length
      ? Object.entries(t.env).map(([k, v]) => `${k}=${v}`).join('\n') : '';
    kv('env', env);
    return el('div', { class: 'card' },
      el('h2', null, 'Configuration'),
      el('div', { class: 'kv' }, rows));
  }

  function runsCard(runs) {
    if (!runs || !runs.length) {
      return el('div', { class: 'card' },
        el('h2', null, 'Recent runs'),
        el('div', { style: 'padding:8px' }, emptyState('No runs yet', 'Trigger one with the run button.')));
    }
    const rows = runs.map((r) => {
      const tr = el('tr', { class: 'clickable' },
        el('td', { class: 'mono' }, '#' + r.id),
        el('td', null, statusBadge(r.status)),
        el('td', { class: 'hide-sm' }, el('span', { class: 'tag' }, r.trigger || '—')),
        el('td', { class: 'mono right' }, r.status === 'running' ? '—' : r.exit_code),
        el('td', { class: 'dim nowrap hide-sm' }, fmtTime(r.started_at)),
        el('td', { class: 'dim nowrap right' }, fmtDuration(r)));
      tr.addEventListener('click', () => {
        location.hash = `#/task/${encodeURIComponent(name)}/run/${encodeURIComponent(r.id)}`;
      });
      return tr;
    });
    return el('div', { class: 'card' },
      el('h2', null, 'Recent runs'),
      el('div', { class: 'table-wrap', style: 'border:none;border-radius:0' },
        el('table', null,
          el('thead', null, el('tr', null,
            el('th', null, 'Run'),
            el('th', null, 'Status'),
            el('th', { class: 'hide-sm' }, 'Trigger'),
            el('th', { class: 'right' }, 'Exit'),
            el('th', { class: 'hide-sm' }, 'Started'),
            el('th', { class: 'right' }, 'Duration'))),
          el('tbody', null, rows))));
  }

  function render(d) {
    const t = d.task || {};
    $('#detailMeta').textContent = d.running ? 'running now' : '';
    clear(slot);
    const bar = el('div', { class: 'log-head' },
      el('button', { class: 'btn primary sm', onclick: (ev) => runTask(ev.currentTarget) }, icon('play'), 'run now'),
      el('button', { class: 'btn sm', onclick: () => openTaskForm(t, onEdited) }, icon('pencil'), 'edit'),
      el('button', { class: 'btn sm danger', onclick: () => deleteTask() }, icon('trash'), 'delete'),
      el('span', { class: 'spacer' }),
      el('span', { class: 'log-state' },
        el('span', { class: 'dot' + (d.running ? ' live' : '') }),
        d.running ? 'task is running' : 'idle'));
    slot.append(bar, el('div', { class: 'detail-grid' },
      configCard(t),
      runsCard(d.runs)));
  }

  load(false);
  timer = setInterval(() => load(true), POLL_MS);
  active.cleanup = () => { stopped = true; clearInterval(timer); };
  active.refresh = () => load(true);
}

// ========================================================================
// Claude Code stream-json transcript formatting
// ========================================================================
//
// `claude -p --output-format stream-json` emits one JSON object per log line.
// Most are noise for a human (per-token counters, hook lifecycle, rate-limit
// pings); the formatted view keeps only the meaningful timeline — session
// header, assistant prose, tool calls + their results, sub-task lifecycle, and
// the final result — and drops the rest. A line that is not a recognised event
// falls back to a verbatim raw line, so non-Claude logs still render.

const CLAUDE_EVENT_TYPES = new Set(['system', 'assistant', 'user', 'result', 'rate_limit_event']);

// tryParseJSON returns the parsed object for a JSON line, or null for anything
// that is not a JSON object/array (cheap first-char guard before JSON.parse).
function tryParseJSON(s) {
  const t = String(s == null ? '' : s).trim();
  if (!t || (t[0] !== '{' && t[0] !== '[')) return null;
  try { return JSON.parse(t); } catch (_) { return null; }
}

function isClaudeEvent(o) {
  return !!o && typeof o === 'object' && typeof o.type === 'string' && CLAUDE_EVENT_TYPES.has(o.type);
}

// firstLine collapses a multi-line string to its first line with an ellipsis,
// for compact one-line tool summaries.
function firstLine(s) {
  s = String(s == null ? '' : s);
  const nl = s.indexOf('\n');
  return nl < 0 ? s : s.slice(0, nl) + ' …';
}

// cap truncates a string to n characters with an ellipsis marker.
function cap(s, n) {
  s = String(s == null ? '' : s);
  return s.length > n ? s.slice(0, n) + '…' : s;
}

// toolInputDetail renders a tool_use's FULL input for the expandable row detail
// — nothing is dropped. A bare command is shown unescaped because it reads far
// better than JSON-escaped; any remaining fields follow as pretty JSON so the
// complete input is always visible.
function toolInputDetail(input) {
  if (input == null || typeof input !== 'object') return String(input == null ? '' : input);
  if (typeof input.command === 'string') {
    const rest = {};
    for (const k of Object.keys(input)) { if (k !== 'command') rest[k] = input[k]; }
    let out = input.command;
    if (Object.keys(rest).length) {
      try { out += '\n\n' + JSON.stringify(rest, null, 2); } catch (_) { /* keep command only */ }
    }
    return out;
  }
  try { return JSON.stringify(input, null, 2); } catch (_) { return String(input); }
}

// toolResultText normalises a tool_result `content` (string or block array) to text.
function toolResultText(content) {
  if (content == null) return '';
  if (typeof content === 'string') return content;
  if (Array.isArray(content)) {
    return content.map((b) => (b && typeof b === 'object')
      ? (b.type === 'text' ? (b.text || '') : (b.text || JSON.stringify(b)))
      : String(b)).join('\n');
  }
  try { return JSON.stringify(content); } catch (_) { return String(content); }
}

function fmtMs(ms) {
  if (!(ms > 0)) return '0ms';
  if (ms < 1000) return Math.round(ms) + 'ms';
  const s = ms / 1000;
  if (s < 60) return s.toFixed(1) + 's';
  const m = Math.floor(s / 60), rem = Math.round(s % 60);
  return m + 'm ' + rem + 's';
}

// createTranscript decomposes Claude Code events into flat table rows, emitting
// each through sink.row(desc) and run-level stats through sink.stat(patch). A
// desc is { kind, tone:'solid'|'muted', color:'blue'|'green'|'gray', label,
// summary, detail, error?, time? }. It maps tool_use id -> tool name so a later
// tool_result row can be labelled with the tool it answers.
function createTranscript(sink) {
  let toolNames = new Map();
  let taskTypes = new Map(); // task_id -> task_type, for notifications
  function reset() { toolNames = new Map(); taskTypes = new Map(); }

  function shortenPath(p) {
    const parts = String(p || '').split('/').filter(Boolean);
    return parts.length <= 2 ? String(p || '') : '…/' + parts.slice(-2).join('/');
  }

  // callSummary derives a one-line label for any tool from a generic priority
  // over common input fields, so unfamiliar tools (Skill, TodoWrite, MCP tools,
  // …) still read well without per-name special-casing.
  function callSummary(input) {
    const inp = input || {};
    if (inp.query) return firstLine(String(inp.query));
    if (inp.file_path) return shortenPath(String(inp.file_path));
    if (inp.notebook_path) return shortenPath(String(inp.notebook_path));
    if (inp.pattern) return firstLine(String(inp.pattern)); // Grep/Glob: the pattern says more than the search root
    if (inp.path) return shortenPath(String(inp.path));
    if (inp.description) return firstLine(String(inp.description));
    if (inp.command) return cap(firstLine(String(inp.command)), 120);
    if (inp.prompt) return cap(firstLine(String(inp.prompt)), 120);
    if (inp.url) return String(inp.url);
    if (inp.skill) return String(inp.skill);
    for (const v of Object.values(inp)) {
      if (typeof v === 'string' && v.length > 0 && v.length < 120) return v;
    }
    return '';
  }

  // A non-null parent_tool_use_id marks a message emitted by a subagent (the
  // Agent call that spawned it); renderers use it to nest rather than to
  // interleave subagent chatter with the main transcript.
  function asst(o) {
    const parent = o.parent_tool_use_id || null;
    for (const b of (o.message && o.message.content) || []) {
      if (!b || typeof b !== 'object') continue;
      if (b.type === 'text') {
        if ((b.text || '').trim()) sink.row({ kind: 'agent', tone: 'solid', color: 'green', label: parent ? 'Subagent' : 'Agent', summary: firstLine(b.text), detail: b.text, parent });
      } else if (b.type === 'thinking') {
        if ((b.thinking || '').trim()) sink.row({ kind: 'thinking', tone: 'muted', color: 'gray', label: 'Thinking', summary: firstLine(b.thinking), detail: b.thinking, parent });
      } else if (b.type === 'tool_use') {
        const name = b.name || 'tool';
        if (b.id) toolNames.set(b.id, name);
        sink.row({ kind: 'tool', tone: 'solid', color: 'blue', label: name, summary: callSummary(b.input), detail: toolInputDetail(b.input), toolUseId: b.id, parent });
      }
    }
  }

  function usr(o) {
    const parent = o.parent_tool_use_id || null;
    for (const b of (o.message && o.message.content) || []) {
      if (!b || typeof b !== 'object' || b.type !== 'tool_result') continue;
      const text = toolResultText(b.content);
      const detail = text.length > 8000 ? text.slice(0, 8000) + '\n… (truncated)' : text;
      const label = (b.tool_use_id && toolNames.get(b.tool_use_id)) || 'Result';
      sink.row({ kind: 'result', tone: 'muted', color: 'blue', label, summary: firstLine(text) || '(no output)', detail: detail || '(no output)', error: !!b.is_error, time: o.timestamp, toolUseId: b.tool_use_id, parent });
    }
  }

  function sys(o) {
    if (o.subtype === 'init') { sink.stat({ model: o.model, cwd: o.cwd, permissionMode: o.permissionMode }); return; }
    // Background-task lifecycle. Both carry the tool_use_id of the call that
    // went to the background (a Bash with run_in_background, or an Agent), so
    // a renderer can pin the status onto that call instead of a separate row.
    // Only task_started carries task_type; the notification inherits it by task_id.
    if (o.subtype === 'task_started') {
      if (o.task_id) taskTypes.set(o.task_id, o.task_type);
      sink.row({ kind: 'task', tone: 'solid', color: 'green', label: 'Task', summary: o.description || o.task_id || '', detail: o.description, toolUseId: o.tool_use_id, taskType: o.task_type });
      return;
    }
    if (o.subtype === 'task_notification') {
      const taskType = o.task_type || taskTypes.get(o.task_id);
      sink.row({ kind: 'task', tone: 'muted', color: 'green', label: 'Task', summary: (o.status ? o.status + ': ' : '') + (o.summary || ''), detail: o.summary, toolUseId: o.tool_use_id, taskType, status: o.status, error: o.status === 'failed' });
      return;
    }
    if (o.subtype === 'api_retry') { sink.row({ kind: 'note', tone: 'muted', color: 'gray', label: 'Retry', summary: 'API retry ' + (o.attempt || '?') + '/' + (o.max_retries || '?') + (o.error ? ' · ' + o.error : '') + (o.error_status ? ' (' + o.error_status + ')' : '') }); return; }
    if (o.subtype === 'compact_boundary') { sink.row({ kind: 'note', tone: 'muted', color: 'gray', label: 'Compact', summary: 'Context compacted' + (o.compact_metadata && o.compact_metadata.trigger ? ' (' + o.compact_metadata.trigger + ')' : '') }); return; }
    // Hook lifecycle, paired by hook_id: started → (progress)* → response.
    if (o.subtype === 'hook_started') { sink.row({ kind: 'hook', tone: 'solid', color: 'gray', label: 'Hook', summary: o.hook_name || o.hook_event || '', hookId: o.hook_id }); return; }
    if (o.subtype === 'hook_progress') { sink.row({ kind: 'hook', tone: 'muted', color: 'gray', label: 'Hook', summary: (o.hook_name || '') + ' · output', detail: o.output || '', hookId: o.hook_id, progress: true }); return; }
    if (o.subtype === 'hook_response') {
      const failed = o.outcome ? o.outcome !== 'success' : (o.exit_code != null && o.exit_code !== 0);
      const status = o.outcome || (o.exit_code != null ? 'exit ' + o.exit_code : 'done');
      const out = [o.output, o.stdout, o.stderr].filter((x) => x && String(x).trim()).join('\n');
      sink.row({ kind: 'hook', tone: 'muted', color: 'gray', label: 'Hook', summary: (o.hook_name || '') + ' · ' + status, detail: out, error: failed, hookId: o.hook_id, status });
      return;
    }
    // thinking_tokens / plugin_install / … → noise, skip.
  }

  function res(o) {
    const err = o.is_error || o.subtype === 'error';
    sink.stat({ status: err ? 'error' : 'success', durationMs: o.duration_ms, numTurns: o.num_turns, costUsd: o.total_cost_usd });
  }

  function render(o) {
    switch (o.type) {
      case 'system': return sys(o);
      case 'assistant': return asst(o);
      case 'user': return usr(o);
      case 'result': return res(o);
      default: return; // rate_limit_event & unknown → skip
    }
  }

  return { render, reset };
}

// ---------- pretty view helpers: Markdown subset + tool-activity phrasing ----------

// mdInline renders the inline subset — `code` and **bold** — as DOM nodes.
// Everything else stays literal text, so nothing in a log line can inject markup.
function mdInline(s) {
  const out = [];
  const re = /(`[^`\n]+`|\*\*[^*\n]+\*\*)/g;
  let last = 0, m;
  while ((m = re.exec(s)) !== null) {
    if (m.index > last) out.push(s.slice(last, m.index));
    const tok = m[0];
    if (tok[0] === '`') out.push(el('code', { class: 'md-code-inline' }, tok.slice(1, -1)));
    else out.push(el('strong', null, mdInline(tok.slice(2, -2)))); // bold may wrap `code`
    last = m.index + tok.length;
  }
  if (last < s.length) out.push(s.slice(last));
  return out;
}

const MD_LIST_RE = /^\s*([-*+]|\d+[.)])\s+/;
const MD_HEAD_RE = /^\s{0,3}(#{1,6})\s+(.*)$/;
const MD_FENCE_RE = /^\s*```/;
const MD_TABLE_RE = /^\s*\|/;
const MD_QUOTE_RE = /^\s{0,3}>\s?/;

// renderMarkdown converts a small Markdown subset (fenced code, headings,
// bullet / numbered lists, pipe tables, paragraphs, inline code and bold) into
// a DocumentFragment. Paragraph line breaks are preserved because agents tend
// to write deliberately line-broken prose. Built with el(), never innerHTML.
function renderMarkdown(src) {
  const frag = document.createDocumentFragment();
  const lines = String(src == null ? '' : src).replace(/\r\n?/g, '\n').split('\n');
  const isSpecial = (ln) => MD_FENCE_RE.test(ln) || MD_HEAD_RE.test(ln) || MD_LIST_RE.test(ln) || MD_TABLE_RE.test(ln) || MD_QUOTE_RE.test(ln);
  let i = 0;
  while (i < lines.length) {
    const ln = lines[i];
    if (!ln.trim()) { i += 1; continue; }
    if (MD_FENCE_RE.test(ln)) {
      const buf = [];
      i += 1;
      while (i < lines.length && !MD_FENCE_RE.test(lines[i])) { buf.push(lines[i]); i += 1; }
      i += 1; // closing fence (or EOF)
      frag.append(el('pre', { class: 'md-code' }, buf.join('\n')));
      continue;
    }
    const h = ln.match(MD_HEAD_RE);
    if (h) { frag.append(el('div', { class: 'md-h md-h' + h[1].length }, mdInline(h[2]))); i += 1; continue; }
    if (MD_LIST_RE.test(ln)) {
      const list = el(/^\s*\d/.test(ln) ? 'ol' : 'ul', { class: 'md-list' });
      while (i < lines.length && MD_LIST_RE.test(lines[i])) {
        list.append(el('li', null, mdInline(lines[i].replace(MD_LIST_RE, ''))));
        i += 1;
      }
      frag.append(list);
      continue;
    }
    if (MD_TABLE_RE.test(ln)) {
      const buf = [];
      while (i < lines.length && MD_TABLE_RE.test(lines[i])) { buf.push(lines[i]); i += 1; }
      frag.append(el('pre', { class: 'md-table' }, buf.join('\n')));
      continue;
    }
    if (MD_QUOTE_RE.test(ln)) {
      const buf = [];
      while (i < lines.length && MD_QUOTE_RE.test(lines[i])) { buf.push(lines[i].replace(MD_QUOTE_RE, '')); i += 1; }
      frag.append(el('blockquote', { class: 'md-quote' }, mdInline(buf.join('\n'))));
      continue;
    }
    const buf = [];
    while (i < lines.length && lines[i].trim() && !isSpecial(lines[i])) { buf.push(lines[i]); i += 1; }
    frag.append(el('p', { class: 'md-p' }, mdInline(buf.join('\n'))));
  }
  return frag;
}

// Tool names collapse into activity categories so a burst of calls reads as
// one line — "Read files, ran 3 commands, edited a file" — the way a chat UI
// summarises an agent's work between two messages.
const TOOL_CATEGORY = {
  Read: 'read', NotebookRead: 'read',
  Glob: 'search', Grep: 'search', LS: 'search',
  Bash: 'shell', BashOutput: 'shell', KillShell: 'shell',
  Edit: 'edit', Write: 'edit', MultiEdit: 'edit', NotebookEdit: 'edit',
  WebFetch: 'web', WebSearch: 'web',
  Agent: 'agent', Task: 'agent',
};
const CATEGORY_META = {
  read:   { icon: 'book-open', one: 'read a file',       many: 'read files' },
  search: { icon: 'search',    one: 'searched files',    many: 'searched files' },
  shell:  { icon: 'terminal',  one: 'ran a command',     many: 'ran {n} commands' },
  edit:   { icon: 'pencil',    one: 'edited a file',     many: 'edited files' },
  web:    { icon: 'globe',     one: 'searched the web',  many: 'searched the web' },
  agent:  { icon: 'sparkles',  one: 'launched an agent', many: 'launched {n} agents' },
  hook:   { icon: 'zap',       one: 'ran a hook',        many: 'ran {n} hooks' },
};

// toolCategory maps a tool name to a category key; unknown tools (Skill,
// TodoWrite, mcp__server__tool, …) get their own key so they are still named.
function toolCategory(name) {
  return TOOL_CATEGORY[name] || ('tool:' + String(name || 'tool').replace(/^mcp__.*__/, ''));
}

// activityPhrase turns an ordered {category -> count} map into one sentence.
function activityPhrase(counts) {
  const parts = [];
  for (const [cat, n] of counts) {
    const meta = CATEGORY_META[cat];
    if (meta) parts.push((n === 1 ? meta.one : meta.many).replace('{n}', n));
    else parts.push('used ' + cat.slice(5) + (n > 1 ? ' ×' + n : ''));
  }
  if (!parts.length) return 'Thought for a moment';
  const s = parts.join(', ');
  return s[0].toUpperCase() + s.slice(1);
}

// ========================================================================
// VIEW 3 — live log view
// ========================================================================

function renderLog(root, name, id) {
  // Declare state before building the DOM: the auto-scroll checkbox is created
  // into a local const below and referenced directly (no IIFE), which avoids a
  // temporal-dead-zone ReferenceError on these `let` bindings.
  let es = null;
  let stopped = false;
  let gotAny = false;   // any line (incl. system messages) has been rendered
  let gotData = false;  // a real log line/data has been received from the run
  let doneSeen = false; // server sent a terminal `done` event
  let retries = 0;
  let retryTimer = null;

  // View mode: 'pretty' renders Claude Code stream-json as a chat-style
  // transcript (prose + collapsed tool activity); 'formatted' as a dense event
  // table; 'raw' shows every line verbatim. Auto-picked from the first content
  // line, overridable by the segmented toggle. `entries` is the ordered source
  // of truth so switching modes replays the whole run cleanly.
  let mode = null;
  let userPinnedMode = false;
  const entries = []; // { kind:'content'|'meta', text, sys? }

  const autoScrollBox = el('input', { type: 'checkbox', checked: true });
  const prettyBtn = el('button', { type: 'button', class: 'seg-btn', onclick: () => setMode('pretty', true) }, 'Pretty');
  const fmtBtn = el('button', { type: 'button', class: 'seg-btn', onclick: () => setMode('formatted', true) }, 'Formatted');
  const rawBtn = el('button', { type: 'button', class: 'seg-btn', onclick: () => setMode('raw', true) }, 'Raw');
  const seg = el('div', { class: 'seg' }, prettyBtn, fmtBtn, rawBtn);
  const expandBtn = el('button', { type: 'button', class: 'btn sm', onclick: () => toggleExpandAll() }, 'expand all');
  const copyBtn = el('button', { type: 'button', class: 'btn sm', onclick: () => copyAll() }, 'copy');

  // run-summary header (formatted mode only): status pill + chips + minimap.
  const statusEl = el('span', { class: 'rs-status' });
  const chipsEl = el('div', { class: 'rs-chips' });
  const minimapEl = el('div', { class: 'minimap' });
  const runSummary = el('div', { class: 'run-summary', hidden: true },
    el('div', { class: 'rs-line' }, statusEl, chipsEl), minimapEl);

  const head = el('div', { class: 'log-head' },
    el('h1', { style: 'font-size:16px;margin:0' }, `${name} · run #${id}`),
    el('span', { class: 'spacer' }),
    seg,
    expandBtn,
    copyBtn,
    el('label', { class: 'toggle' }, autoScrollBox, 'auto-scroll'),
    el('span', { class: 'log-state', id: 'logState' },
      el('span', { class: 'dot', id: 'logDot' }), el('span', { id: 'logStateText' }, 'connecting…')));

  const box = el('div', { class: 'logbox', id: 'logbox' });
  root.append(el('div', { class: 'page-head', style: 'margin-bottom:8px' }, head), runSummary, box);

  // ---- formatted event table: badge rows + summary header + minimap ----
  let rowIdx = 0;
  let allExpanded = false;
  const STAT0 = { status: null, model: null, cwd: null, permissionMode: null, durationMs: null, numTurns: null, costUsd: null, toolCalls: 0 };
  const stats = Object.assign({}, STAT0);

  // place appends a node to the scroll box, staying pinned to the bottom while
  // auto-scroll is on and the user has not scrolled up.
  function place(node) {
    if (!node) return;
    const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 60;
    box.append(node);
    if (autoScrollBox.checked && (atBottom || !gotAny)) box.scrollTop = box.scrollHeight;
    gotAny = true;
  }

  function shortCwd(p) {
    const parts = String(p || '').split('/').filter(Boolean);
    return parts.length <= 2 ? String(p || '') : parts.slice(-2).join('/');
  }

  // fmtClock renders an ISO timestamp as a local HH:MM:SS (or '' if unparseable).
  function fmtClock(ts) {
    const d = new Date(ts);
    if (isNaN(d) || d.getUTCFullYear() <= 1) return '';
    return d.toLocaleTimeString(undefined, { hour12: false });
  }

  // renderHeader redraws the run-summary status pill and metadata chips.
  function renderHeader() {
    statusEl.className = 'rs-status';
    if (stats.status === 'success') { statusEl.classList.add('ok'); statusEl.textContent = 'completed'; }
    else if (stats.status === 'error') { statusEl.classList.add('err'); statusEl.textContent = 'failed'; }
    else { statusEl.classList.add('run'); statusEl.textContent = doneSeen ? 'finished' : 'running'; }
    const chips = ['Claude Code'];
    if (stats.model) chips.push(stats.model);
    if (stats.durationMs != null) chips.push(fmtMs(stats.durationMs));
    chips.push(stats.toolCalls + ' tool call' + (stats.toolCalls === 1 ? '' : 's'));
    chips.push(rowIdx + ' event' + (rowIdx === 1 ? '' : 's'));
    if (stats.numTurns != null) chips.push(stats.numTurns + ' turns');
    if (stats.costUsd != null) chips.push('$' + Number(stats.costUsd).toFixed(4));
    if (stats.cwd) chips.push(shortCwd(stats.cwd));
    clear(chipsEl);
    for (const c of chips) chipsEl.append(el('span', { class: 'chip' }, c));
  }

  // sinkRow accounts one decomposed event (stats + minimap tick) and hands it
  // to the renderer for the current mode. The tick scrolls to whatever node
  // the renderer returned — a table row, or the chat group the event joined.
  function sinkRow(desc) {
    rowIdx += 1;
    if (desc.kind === 'tool') stats.toolCalls += 1;
    const idx = rowIdx;
    const node = mode === 'pretty' ? prettyRow(desc) : tableRow(desc, idx);
    const tick = el('i', { class: 'tick ' + desc.color + ' ' + desc.tone, title: '#' + idx + ' ' + desc.label });
    if (node) tick.addEventListener('click', () => node.scrollIntoView({ block: 'center' }));
    minimapEl.append(tick);
    renderHeader();
  }

  // ---- pretty chat view: prose messages + collapsed tool-activity groups ----
  //
  // Consecutive tool calls (and their results) fold into one group line such
  // as "Read files, ran a command"; the group expands to per-call input/output.
  // An agent message or task event closes the open group. Results are matched
  // to their call by tool_use_id, so they land inside the right group even
  // when they arrive after it was closed.
  let curGroup = null;            // { node, body, phrase, iconSlot, counts:Map }
  const callsById = new Map();    // tool_use_id -> { detail, head }
  const hooksById = new Map();    // hook_id -> { head, detail, node }

  function closeGroup() { curGroup = null; }

  function ensureGroup() {
    if (curGroup) return curGroup;
    const iconSlot = el('span', { class: 'pg-icon' }, icon('wrench'));
    const phrase = el('span', { class: 'pg-phrase' }, activityPhrase(new Map()));
    const body = el('div', { class: 'pg-body', hidden: !allExpanded });
    const head = el('div', { class: 'pg-head' }, iconSlot, phrase, el('span', { class: 'pg-chev' }, icon('chevron-right')));
    const node = el('div', { class: 'pgroup' + (allExpanded ? ' open' : '') }, head, body);
    head.addEventListener('click', () => {
      body.hidden = !body.hidden;
      node.classList.toggle('open', !body.hidden);
    });
    curGroup = { node, body, phrase, iconSlot, counts: new Map() };
    place(node);
    return curGroup;
  }

  // countCat updates the group's phrase and icon after an activity joins it.
  function countCat(g, cat) {
    g.counts.set(cat, (g.counts.get(cat) || 0) + 1);
    g.phrase.textContent = activityPhrase(g.counts);
    const first = g.counts.keys().next().value;
    const meta = CATEGORY_META[first];
    g.iconSlot.replaceChildren(icon(meta ? meta.icon : 'wrench'));
  }

  // prettyCall renders one tool call inside a group: a one-line head that
  // expands to the full input, with the output pre appended when it arrives.
  function prettyCall(g, desc) {
    const detail = el('div', { class: 'pcall-detail', hidden: true });
    if (desc.detail) detail.append(el('div', { class: 'pcall-label' }, 'input'), el('pre', { class: 'evdetail' }, desc.detail));
    const head = el('div', { class: 'pcall-head' },
      el('span', { class: 'pg-chev' }, icon('chevron-right')),
      el('span', { class: 'evbadge solid blue' }, desc.label),
      el('span', { class: 'pcall-summary' }, desc.summary || ''));
    const node = el('div', { class: 'pcall' }, head, detail);
    head.addEventListener('click', () => {
      detail.hidden = !detail.hidden;
      node.classList.toggle('open', !detail.hidden);
    });
    g.body.append(node);
    if (desc.toolUseId) callsById.set(desc.toolUseId, { detail, head });
    countCat(g, toolCategory(desc.label));
  }

  function prettyResult(desc) {
    const call = desc.toolUseId && callsById.get(desc.toolUseId);
    if (call) {
      call.detail.append(el('div', { class: 'pcall-label' }, desc.error ? 'error' : 'output'),
        el('pre', { class: 'evdetail' + (desc.error ? ' error' : '') }, desc.detail));
      if (desc.error) call.head.classList.add('err');
      return call.head.closest('.pgroup');
    }
    // Orphan result (no matching call seen): show it as its own entry.
    const g = ensureGroup();
    g.body.append(el('div', { class: 'pcall' },
      el('div', { class: 'pcall-head leaf' }, el('span', { class: 'evbadge muted blue' + (desc.error ? ' err' : '') }, desc.label), el('span', { class: 'pcall-summary' }, desc.summary || '')),
      el('div', { class: 'pcall-detail' }, el('pre', { class: 'evdetail' + (desc.error ? ' error' : '') }, desc.detail))));
    return g.node;
  }

  // prettyEntry adds a collapsible non-tool entry (thinking, subagent text) to
  // the open group without affecting its activity phrase.
  function prettyEntry(label, badgeCls, summary, body) {
    const g = ensureGroup();
    const detail = el('div', { class: 'pcall-detail', hidden: true }, body);
    const head = el('div', { class: 'pcall-head' },
      el('span', { class: 'pg-chev' }, icon('chevron-right')),
      el('span', { class: 'evbadge ' + badgeCls }, label),
      el('span', { class: 'pcall-summary muted' }, summary || ''));
    const node = el('div', { class: 'pcall' }, head, detail);
    head.addEventListener('click', () => {
      detail.hidden = !detail.hidden;
      node.classList.toggle('open', !detail.hidden);
    });
    g.body.append(node);
    return g.node;
  }

  // prettyHook shows a hook as a group entry tagged with its lifecycle
  // (running → success / failed); response output, when any, becomes the
  // expandable detail. Paired by hook_id, like background tasks by tool_use_id.
  function prettyHook(desc) {
    let h = desc.hookId && hooksById.get(desc.hookId);
    if (!h) {
      const g = ensureGroup();
      const detail = el('div', { class: 'pcall-detail', hidden: true });
      const head = el('div', { class: 'pcall-head leaf' },
        el('span', { class: 'pg-chev' }, icon('chevron-right')),
        el('span', { class: 'evbadge muted gray' }, 'Hook'),
        el('span', { class: 'pcall-summary muted' }, desc.summary.replace(/ · .*$/, '')),
        el('span', { class: 'ptag' }, 'running'));
      const node = el('div', { class: 'pcall' }, head, detail);
      head.addEventListener('click', () => {
        if (head.classList.contains('leaf')) return;
        detail.hidden = !detail.hidden;
        node.classList.toggle('open', !detail.hidden);
      });
      g.body.append(node);
      h = { head, detail, node };
      if (desc.hookId) hooksById.set(desc.hookId, h);
      countCat(g, 'hook');
    }
    if (desc.detail) {
      h.detail.append(el('div', { class: 'pcall-label' }, desc.error ? 'error' : 'output'), el('pre', { class: 'evdetail' + (desc.error ? ' error' : '') }, desc.detail));
      h.head.classList.remove('leaf');
    }
    if (desc.tone === 'muted' && !desc.progress) {
      const tag = h.head.querySelector('.ptag');
      tag.textContent = desc.status || 'done';
      tag.classList.toggle('ok', !desc.error);
      tag.classList.toggle('err', !!desc.error);
      if (desc.error) h.head.classList.add('err');
    }
    return h.node.closest('.pgroup');
  }

  function prettyThinking(desc) {
    return prettyEntry('Thinking', 'muted gray', desc.summary, el('pre', { class: 'evdetail' }, desc.detail));
  }

  // prettyTask handles background-task lifecycle. A task that belongs to a
  // call already shown in a group (a backgrounded Bash) becomes a status tag on
  // that call — "background" → "completed" / "failed" — rather than a separate
  // line. Subagent tasks, and tasks whose call was never seen, are milestones
  // worth a standalone line.
  function prettyTask(desc) {
    const started = desc.tone === 'solid';
    const call = desc.toolUseId && callsById.get(desc.toolUseId);
    if (call && desc.taskType === 'local_bash') {
      let tag = call.head.querySelector('.ptag');
      if (!tag) { tag = el('span', { class: 'ptag' }); call.head.append(tag); }
      tag.textContent = started ? 'background' : (desc.status || 'done');
      tag.classList.toggle('ok', !started && !desc.error);
      tag.classList.toggle('err', !!desc.error);
      if (desc.error) call.head.classList.add('err');
      return call.head.closest('.pgroup');
    }
    closeGroup();
    const agent = desc.taskType && desc.taskType !== 'local_bash';
    const noun = agent ? 'Agent' : 'Task';
    const text = started
      ? noun + ' started · ' + (desc.summary || '')
      : noun + ' ' + (desc.status || 'updated') + (desc.detail ? ' · ' + desc.detail : '');
    const node = el('div', { class: 'pline' + (desc.error ? ' err' : '') },
      el('span', { class: 'pg-icon' }, icon('sparkles')),
      el('span', { class: 'pline-text' }, text));
    place(node);
    return node;
  }

  function prettyRow(desc) {
    switch (desc.kind) {
      case 'tool': { const g = ensureGroup(); prettyCall(g, desc); return g.node; }
      case 'result': return prettyResult(desc);
      case 'thinking': return prettyThinking(desc);
      case 'agent': {
        if (desc.parent) return prettyEntry('Subagent', 'muted green', desc.summary, el('div', { class: 'pmsg sub' }, renderMarkdown(desc.detail)));
        closeGroup();
        const node = el('div', { class: 'pmsg' }, renderMarkdown(desc.detail));
        place(node);
        return node;
      }
      case 'task': return prettyTask(desc);
      case 'hook': return prettyHook(desc);
      case 'note': {
        closeGroup();
        const node = el('div', { class: 'pnote' }, desc.summary || desc.label);
        place(node);
        return node;
      }
      default: {
        closeGroup();
        const node = el('div', { class: 'pnote mono' }, desc.summary || desc.label);
        place(node);
        return node;
      }
    }
  }

  // ---- formatted event table: one badge row per event ----
  function tableRow(desc, idx) {
    const summary = desc.summary || '';
    const detailText = desc.detail != null ? String(desc.detail) : '';
    const hasDetail = !!detailText && (detailText !== summary || detailText.indexOf('\n') >= 0);
    const detail = hasDetail
      ? el('pre', { class: 'evdetail' + (desc.error ? ' error' : ''), hidden: !allExpanded }, detailText)
      : null;
    const rowHead = el('div', { class: 'evrow-head' + (hasDetail ? '' : ' leaf') },
      el('span', { class: 'evchev' }, '›'),
      el('span', { class: 'evbadge ' + desc.tone + ' ' + desc.color + (desc.error ? ' err' : '') }, desc.label),
      el('span', { class: 'evsummary' }, summary),
      el('span', { class: 'evmeta' }, '#' + idx + (desc.time ? '  ' + fmtClock(desc.time) : '')));
    const row = el('div', { class: 'evrow' + (desc.parent ? ' sub' : ''), dataset: { kind: desc.kind } }, rowHead, detail);
    if (hasDetail) {
      if (allExpanded) row.classList.add('open');
      rowHead.addEventListener('click', () => {
        detail.hidden = !detail.hidden;
        row.classList.toggle('open', !detail.hidden);
      });
    }
    place(row);
    return row;
  }

  function sinkStat(patch) { Object.assign(stats, patch); renderHeader(); }

  const transcript = createTranscript({ row: sinkRow, stat: sinkStat });

  function toggleExpandAll() {
    allExpanded = !allExpanded;
    if (mode === 'pretty') {
      for (const b of box.querySelectorAll('.pg-body')) b.hidden = !allExpanded;
      for (const g of box.querySelectorAll('.pgroup')) g.classList.toggle('open', allExpanded);
      for (const d of box.querySelectorAll('.pcall-detail')) d.hidden = !allExpanded;
      for (const c of box.querySelectorAll('.pcall')) c.classList.toggle('open', allExpanded);
    } else {
      for (const d of box.querySelectorAll('.evdetail')) d.hidden = !allExpanded;
      for (const r of box.querySelectorAll('.evrow')) r.classList.toggle('open', allExpanded && !!r.querySelector('.evdetail'));
    }
    expandBtn.textContent = allExpanded ? 'collapse all' : 'expand all';
  }

  async function copyAll() {
    const text = entries.filter((e) => e.kind === 'content').map((e) => e.text).join('\n');
    try { await navigator.clipboard.writeText(text); toast('Log copied', 'ok'); }
    catch (_) { toast('Copy failed', 'err'); }
  }

  // applyModeChrome syncs the toggle, run-summary visibility and toolbar to mode.
  function applyModeChrome() {
    prettyBtn.classList.toggle('active', mode === 'pretty');
    fmtBtn.classList.toggle('active', mode === 'formatted');
    rawBtn.classList.toggle('active', mode === 'raw');
    const structured = mode === 'pretty' || mode === 'formatted';
    box.classList.toggle('pretty', mode === 'pretty');
    runSummary.hidden = !structured;
    expandBtn.hidden = !structured;
    if (structured) renderHeader();
  }

  function renderMeta(text) {
    if (mode === 'pretty') {
      closeGroup();
      place(el('div', { class: 'pnote' }, text || ' '));
    } else if (mode === 'formatted') {
      place(el('div', { class: 'evrow meta' }, el('span', { class: 'evmeta-note' }, text || ' ')));
    } else {
      place(el('div', { class: 'log-meta sys' }, text === '' ? ' ' : text));
    }
  }

  function renderContentNode(text) {
    if (mode === 'pretty') {
      const o = tryParseJSON(text);
      if (isClaudeEvent(o)) { transcript.render(o); return; }
      closeGroup();
      place(el('div', { class: 'pnote mono' }, text === '' ? ' ' : text));
      return;
    }
    if (mode === 'formatted') {
      const o = tryParseJSON(text);
      if (isClaudeEvent(o)) { transcript.render(o); return; }
      place(el('div', { class: 'evrow meta' }, el('span', { class: 'evmeta-note mono' }, text === '' ? ' ' : text)));
      return;
    }
    place(el('span', { class: 'ln' }, text === '' ? ' ' : text));
  }

  function resetStats() {
    rowIdx = 0;
    Object.assign(stats, STAT0);
    stats.toolCalls = 0;
    curGroup = null;
    callsById.clear();
    hooksById.clear();
  }

  function rebuild() {
    clear(box);
    clear(minimapEl);
    gotAny = false;
    resetStats();
    transcript.reset();
    applyModeChrome();
    for (const e of entries) {
      if (e.kind === 'meta') renderMeta(e.text);
      else renderContentNode(e.text);
    }
    renderHeader();
  }

  function setMode(m, byUser) {
    if (byUser) userPinnedMode = true;
    if (mode === m) return;
    mode = m;
    rebuild();
  }

  // pushContent records and renders one real log line from the run.
  function pushContent(text) {
    entries.push({ kind: 'content', text });
    gotData = true;
    if (mode === null) {
      mode = isClaudeEvent(tryParseJSON(text)) ? 'pretty' : 'raw';
      applyModeChrome();
    } else if (mode === 'raw' && !userPinnedMode && entries.length <= 10 && isClaudeEvent(tryParseJSON(text))) {
      // An early non-JSON banner mis-detected the run as raw; the first real
      // event upgrades it to pretty (replaying what we have).
      setMode('pretty', false);
      return;
    }
    renderContentNode(text);
  }

  // pushMeta records and renders a transient status message (connect, finished,
  // errors) that survives a mode switch alongside the run's own output.
  function pushMeta(text) {
    entries.push({ kind: 'meta', text, sys: true });
    renderMeta(text);
  }

  // setRunStatus reflects the terminal SSE status into the header when there was
  // no explicit `result` event (e.g. a crashed or killed run).
  function setRunStatus(s) {
    if (stats.status) return;
    if (s === 'success') stats.status = 'success';
    else if (s) stats.status = 'error';
    renderHeader();
  }

  // resetTranscript drops all buffered lines and clears the view, preserving mode.
  function resetTranscript() {
    entries.length = 0;
    transcript.reset();
    clear(box);
    clear(minimapEl);
    resetStats();
    gotAny = false;
    renderHeader();
  }

  function setState(text, cls) {
    const dot = $('#logDot'), st = $('#logStateText');
    if (dot) dot.className = 'dot' + (cls ? ' ' + cls : '');
    if (st) st.textContent = text;
  }

  // appendLine keeps the SSE/static call sites unchanged: system/status lines
  // (sys=true) become meta entries; everything else is a run content line.
  function appendLine(text, sys) {
    if (sys) pushMeta(text);
    else pushContent(text);
  }

  // Fallback: fetch the full static log (finished runs, or if SSE unavailable).
  async function loadStaticLog() {
    try {
      const text = await Tasks.runLog(name, id);
      resetTranscript();
      const lines = String(text).replace(/\n$/, '').split('\n');
      if (lines.length === 1 && lines[0] === '') {
        appendLine('(empty log)', true);
      } else {
        for (const ln of lines) { appendLine(ln); gotData = true; }
      }
      setState('log complete', 'done');
    } catch (e) {
      if (e.status === 404) appendLine('(no log for this run)', true);
      else appendLine('error loading log: ' + e.message, true);
      setState('unavailable', '');
    }
  }

  // Live stream via SSE. The server replays the full log on connect, emits one
  // "data:" line per log line, and a final `event: done` carrying the terminal
  // status; we then stop streaming.
  const MAX_RETRIES = 5;

  function openStream(reconnect) {
    setState(reconnect ? 'reconnecting…' : 'live', reconnect ? '' : 'live');
    // The server replays from the start on every connect, so on a reconnect we
    // drop what we have to avoid duplicating already-rendered lines.
    if (reconnect) { resetTranscript(); gotData = false; }
    const url = `/api/tasks/${encodeURIComponent(name)}/runs/${encodeURIComponent(id)}/stream`;
    es = new EventSource(url);

    es.onmessage = (ev) => {
      if (stopped) return;
      appendLine(ev.data);
      gotData = true;
      retries = 0; // a healthy frame resets the backoff budget
      setState('live', 'live');
    };

    // Server marks completion with a named "done" event whose data is the status.
    es.addEventListener('done', (ev) => {
      if (stopped) return;
      doneSeen = true;
      const status = (ev.data || '').trim();
      appendLine(status ? `— run ${status} —` : '— run finished —', true);
      setRunStatus(status);
      setState(status ? 'finished: ' + status : 'finished', 'done');
      es.close();
      es = null;
    });

    es.onerror = () => {
      if (stopped || !es) return;
      // Suppress the browser's built-in reconnect so we control retry/fallback.
      es.close();
      es = null;
      if (doneSeen) return; // already terminal; nothing to do
      if (!gotData) {
        // Never received real output: the run is most likely already finished
        // (or SSE transport is unusable) — fall back to the static log.
        loadStaticLog();
        return;
      }
      // We had live data but the stream dropped before a `done` event: the task
      // may still be running. Attempt a bounded reconnect with backoff instead
      // of permanently abandoning the live stream.
      if (retries < MAX_RETRIES) {
        retries += 1;
        setState('reconnecting…', '');
        const backoff = Math.min(1000 * retries, 5000);
        clearTimeout(retryTimer);
        retryTimer = setTimeout(() => { if (!stopped) openStream(true); }, backoff);
      } else {
        // Give up live streaming after repeated failures; show the final state
        // from the static log so the user still sees the full output.
        setState('disconnected', '');
        appendLine('— stream disconnected, loading saved log —', true);
        loadStaticLog();
      }
    };
  }

  // EventSource is the live path; it gracefully degrades to the static log.
  if (window.EventSource) {
    appendLine('connecting to log stream…', true);
    openStream();
  } else {
    loadStaticLog();
  }

  active.cleanup = () => {
    stopped = true;
    clearTimeout(retryTimer);
    if (es) { es.close(); es = null; }
  };
}

// ========================================================================
// boot
// ========================================================================

// Inject brand + header-action icons (kept out of index.html so the SVG markup
// lives in one place).
$('.brand .logo').replaceChildren(icon('repeat'));
$('#settingsBtn').replaceChildren(icon('settings'));
$('#reloadBtn').replaceChildren(icon('rotate-ccw'));
$('#refreshBtn').replaceChildren(icon('refresh-cw'));
updateThemeIcon();

$('#themeBtn').addEventListener('click', toggleTheme);

$('#settingsBtn').addEventListener('click', () => openSettings());

$('#refreshBtn').addEventListener('click', () => {
  if (typeof active.refresh === 'function') active.refresh();
});

$('#reloadBtn').addEventListener('click', async (ev) => {
  const btn = ev.currentTarget;
  btn.disabled = true;
  try {
    await Tasks.reload();
    toast('Config reloaded', 'ok');
    if (typeof active.refresh === 'function') active.refresh();
  } catch (e) {
    toast('Reload failed: ' + e.message, 'err');
  } finally {
    btn.disabled = false;
  }
});

window.addEventListener('hashchange', navigate);
navigate();

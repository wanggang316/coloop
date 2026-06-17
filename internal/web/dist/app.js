// the-loop admin SPA — vanilla JS, no framework, no build.
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
    el('button', { class: 'btn primary sm', onclick: () => openRunnerForm(null, reload) }, '+ Add runner'));
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
          el('button', { class: 'btn sm', onclick: () => openRunnerForm(r, reload) }, 'edit'),
          el('button', { class: 'btn sm danger', onclick: () => askDelete(r) }, 'delete'))));
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
    el('button', { class: 'btn primary sm', onclick: () => openChannelForm(null, reload) }, '+ Add channel'));
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
          el('button', { class: 'btn sm', onclick: () => openChannelForm(c, reload) }, 'edit'),
          el('button', { class: 'btn sm danger', onclick: () => askDelete(c) }, 'delete'))));
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
    el('button', { class: 'btn primary sm', onclick: () => openTaskForm(null, () => load(true)) }, '+ New task'));
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
    const label = btn.textContent;
    btn.textContent = '…';
    try {
      const r = await Tasks.run(name);
      toast(`Triggered ${name} (run #${r.run_id})`, 'ok');
      setTimeout(() => load(true), 400);
    } catch (e) {
      toast('Run failed: ' + e.message, 'err');
    } finally {
      btn.disabled = false;
      btn.textContent = label;
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
            class: 'btn primary sm',
            title: 'Trigger a manual run',
            onclick: (ev) => { ev.stopPropagation(); runTask(t.name, ev.currentTarget); },
          }, 'run')));
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
      el('button', { class: 'btn primary sm', onclick: (ev) => runTask(ev.currentTarget) }, 'run now'),
      el('button', { class: 'btn sm', onclick: () => openTaskForm(t, onEdited) }, 'edit'),
      el('button', { class: 'btn sm danger', onclick: () => deleteTask() }, 'delete'),
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

  const autoScrollBox = el('input', { type: 'checkbox', checked: true });
  const head = el('div', { class: 'log-head' },
    el('h1', { style: 'font-size:16px;margin:0' }, `${name} · run #${id}`),
    el('span', { class: 'spacer' }),
    el('label', { class: 'toggle' }, autoScrollBox, 'auto-scroll'),
    el('span', { class: 'log-state', id: 'logState' },
      el('span', { class: 'dot', id: 'logDot' }), el('span', { id: 'logStateText' }, 'connecting…')));

  const box = el('div', { class: 'logbox', id: 'logbox' });
  root.append(el('div', { class: 'page-head', style: 'margin-bottom:8px' }, head), box);

  function setState(text, cls) {
    const dot = $('#logDot'), st = $('#logStateText');
    if (dot) dot.className = 'dot' + (cls ? ' ' + cls : '');
    if (st) st.textContent = text;
  }

  function appendLine(text, sys) {
    const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
    box.append(el('span', { class: 'ln' + (sys ? ' sys' : '') }, text === '' ? ' ' : text));
    if (autoScrollBox && autoScrollBox.checked && (atBottom || !gotAny)) {
      box.scrollTop = box.scrollHeight;
    }
    gotAny = true;
  }

  // Fallback: fetch the full static log (finished runs, or if SSE unavailable).
  async function loadStaticLog() {
    try {
      const text = await Tasks.runLog(name, id);
      clear(box);
      gotAny = false;
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
    // clear the box to avoid duplicating already-rendered lines.
    if (reconnect) { clear(box); gotData = false; }
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

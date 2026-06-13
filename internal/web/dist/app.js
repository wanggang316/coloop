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
  run: (name) => api('/api/tasks/' + encodeURIComponent(name) + '/run', { method: 'POST' }),
  runLog: (name, id) => api(`/api/tasks/${encodeURIComponent(name)}/runs/${encodeURIComponent(id)}/log`),
  reload: () => api('/api/reload', { method: 'POST' }),
};

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
    el('span', { class: 'meta', id: 'listMeta' }, ''));
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
      kv('cmd template', t.cmd_tmpl);
    } else {
      kv('command', t.command);
    }
    kv('timeout', t.timeout && t.timeout !== '0s' ? t.timeout : 'none');
    kv('alert', t.alert || 'on_failure');
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

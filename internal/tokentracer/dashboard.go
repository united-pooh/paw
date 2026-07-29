package tokentracer

const dashboardHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Token Tracer</title>
<style>
:root {
  --bg: #0b0f14;
  --surface: #111820;
  --surface-2: #151e28;
  --surface-3: #1a2430;
  --line: #26323f;
  --line-strong: #3b4b5b;
  --text: #e6edf3;
  --muted: #99a7b6;
  --soft: #667486;
  --accent: #56d6be;
  --input: #6ea8fe;
  --cache: #45c7de;
  --create: #d6a85f;
  --output: #ef7f64;
  --live: #56d6be;
  --done: #8ea0b5;
  --failed: #ff6b6b;
}
* { box-sizing: border-box; }
body {
  margin: 0;
  min-width: 1040px;
  background: var(--bg);
  color: var(--text);
  font: 12px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
header {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 18px;
  padding: 16px 22px 12px;
  border-bottom: 1px solid var(--line);
  background: #0d1218;
}
.brand { min-width: 0; display: flex; gap: 10px; align-items: center; }
.mark {
  width: 34px; height: 28px; display: inline-flex; align-items: center; justify-content: center;
  border: 1px solid #347b70; background: #102520; color: var(--accent); font-weight: 900;
}
.title { font-size: 16px; font-weight: 900; }
.pipe { color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.status { display: flex; align-items: center; gap: 8px; justify-content: flex-end; color: var(--accent); }
.status-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--accent); box-shadow: 0 0 0 5px rgba(86,214,190,.12); }
.status.failed { color: var(--failed); }
.status.failed .status-dot { background: var(--failed); box-shadow: 0 0 0 5px rgba(255,107,107,.12); }
.status.completed { color: var(--done); }
.status.completed .status-dot { background: var(--done); box-shadow: 0 0 0 5px rgba(142,160,181,.12); }
main { padding: 16px 22px 26px; }
.summary {
  display: grid;
  grid-template-columns: 1.2fr repeat(5, minmax(110px, .5fr));
  border: 1px solid var(--line);
  background: var(--surface);
}
.stat { min-width: 0; padding: 11px 13px; border-right: 1px solid var(--line); }
.stat:last-child { border-right: 0; }
.k { color: var(--muted); font-size: 10px; letter-spacing: .08em; text-transform: uppercase; }
.v { margin-top: 7px; font-size: 20px; line-height: 1.1; font-weight: 900; overflow-wrap: anywhere; }
.error-strip {
  margin-top: 12px; padding: 9px 12px; border: 1px solid rgba(255,107,107,.34);
  background: rgba(255,107,107,.08); color: #ffb4b4; display: none;
}
.layout { display: grid; grid-template-columns: minmax(0, 1fr) 330px; gap: 18px; margin-top: 18px; }
.section-title { margin: 0 0 9px; color: var(--muted); font-size: 10px; text-transform: uppercase; letter-spacing: .1em; }
.panel { border: 1px solid var(--line); background: var(--surface); min-width: 0; }
.timeline-panel { overflow: hidden; }
.time-axis {
  display: grid; grid-template-columns: 216px minmax(0, 1fr);
  border-bottom: 1px solid var(--line); background: var(--surface-2);
}
.axis-label { padding: 9px 12px; color: var(--muted); border-right: 1px solid var(--line); }
.axis-track { position: relative; min-height: 34px; }
.tick { position: absolute; top: 0; bottom: 0; width: 1px; background: #2a3947; }
.tick span { position: absolute; top: 9px; left: 6px; color: var(--soft); white-space: nowrap; }
.timeline-rows { max-height: 520px; overflow: auto; }
.timeline-row {
  display: grid; grid-template-columns: 216px minmax(0, 1fr);
  min-height: 58px; border-bottom: 1px solid #202a35;
}
.timeline-row:last-child { border-bottom: 0; }
.timeline-row.selected { background: rgba(86,214,190,.055); }
.row-label {
  padding: 9px 10px; border-right: 1px solid var(--line); overflow: hidden;
}
.row-kind { color: var(--soft); font-size: 10px; text-transform: uppercase; letter-spacing: .08em; }
.row-name { margin-top: 4px; font-size: 12px; font-weight: 900; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.row-meta { margin-top: 4px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.row-track {
  position: relative; min-height: 58px; background:
    linear-gradient(90deg, transparent calc(20% - 1px), rgba(255,255,255,.035) calc(20% - 1px), rgba(255,255,255,.035) 20%, transparent 20%),
    linear-gradient(90deg, transparent calc(40% - 1px), rgba(255,255,255,.035) calc(40% - 1px), rgba(255,255,255,.035) 40%, transparent 40%),
    linear-gradient(90deg, transparent calc(60% - 1px), rgba(255,255,255,.035) calc(60% - 1px), rgba(255,255,255,.035) 60%, transparent 60%),
    linear-gradient(90deg, transparent calc(80% - 1px), rgba(255,255,255,.035) calc(80% - 1px), rgba(255,255,255,.035) 80%, transparent 80%);
}
.gantt-bar {
  position: absolute; top: 13px; height: 28px; min-width: 4px;
  border: 1px solid var(--line-strong); background: #1d2935; overflow: hidden; cursor: pointer;
  box-shadow: 0 8px 22px rgba(0,0,0,.2);
}
.gantt-bar.stage { height: 20px; top: 18px; background: #202c38; border-color: #435468; }
.gantt-bar.live { border-color: var(--live); box-shadow: 0 0 0 1px rgba(86,214,190,.18), 0 8px 22px rgba(0,0,0,.2); }
.gantt-bar.failed { border-color: var(--failed); background: #2b1b20; }
.bar-fill { position: absolute; inset: 0; opacity: .72; }
.token-stack { position: absolute; left: 0; right: 0; bottom: 0; height: 4px; display: flex; opacity: .82; }
.token-seg.input { background: var(--input); }
.token-seg.cache { background: var(--cache); }
.token-seg.create { background: var(--create); }
.token-seg.output { background: var(--output); }
.bar-text {
  position: absolute; left: 8px; right: 8px; top: 5px; color: #fff; font-weight: 900;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap; text-shadow: 0 1px 4px #000;
}
.marker {
  position: absolute; top: 8px; width: 2px; height: 42px; background: var(--soft); opacity: .85;
}
.marker.api_call { top: 16px; height: 22px; background: rgba(230,237,243,.45); }
.marker.step { top: 11px; width: 7px; height: 7px; transform: rotate(45deg); background: var(--create); }
.marker.failure { top: 7px; width: 9px; height: 42px; background: var(--failed); opacity: 1; }
.marker.start { background: var(--live); }
.marker.end { background: var(--done); }
.bottom-grid { display: grid; grid-template-columns: minmax(0, 1.2fr) minmax(300px, .8fr); gap: 18px; margin-top: 18px; }
.dist { padding: 12px; }
.dist-row { display: grid; grid-template-columns: 132px minmax(0, 1fr) 64px; gap: 10px; align-items: center; margin: 9px 0; }
.dist-name { color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dist-bar { height: 22px; border: 1px solid #25313d; background: #0d1218; display: flex; overflow: hidden; }
.dist-value { text-align: right; color: var(--text); font-weight: 800; }
.legend { display: flex; gap: 13px; flex-wrap: wrap; padding-top: 10px; color: var(--muted); }
.swatch { display: inline-block; width: 10px; height: 10px; margin-right: 5px; vertical-align: -1px; }
.inspector { padding: 12px; min-height: 247px; }
.inspector h3 { margin: 0; font-size: 14px; }
.kv { display: grid; grid-template-columns: 92px minmax(0, 1fr); gap: 6px 10px; margin-top: 12px; }
.kv div:nth-child(odd) { color: var(--muted); }
.kv div:nth-child(even) { overflow-wrap: anywhere; }
.event-list { margin-top: 12px; border-top: 1px solid var(--line); padding-top: 8px; color: var(--muted); }
.event-item { padding: 4px 0; overflow-wrap: anywhere; }
.event-item.error { color: var(--failed); }
.event-note { padding-top: 6px; color: var(--soft); font-size: 11px; }
.live-pulse { animation: pulse 1.3s ease-in-out infinite; }
@keyframes pulse { 0%,100% { opacity: .82; } 50% { opacity: 1; } }
@media (max-width: 980px) {
  body { min-width: 0; }
  header, .layout, .bottom-grid { grid-template-columns: 1fr; }
  .summary { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .stat { border-bottom: 1px solid var(--line); }
  .time-axis, .timeline-row { grid-template-columns: 170px minmax(600px, 1fr); }
}
</style>
</head>
<body>
<header>
  <div class="brand">
    <span class="mark">TT</span>
    <span class="title">Token Tracer</span>
    <span class="pipe" id="pipeline-name">Paw</span>
  </div>
  <div class="status"><span class="status-dot"></span><span id="status-text">live</span></div>
</header>
<main>
  <section class="summary">
    <div class="stat"><div class="k">Run Window</div><div class="v" id="duration">0s</div></div>
    <div class="stat"><div class="k">Calls</div><div class="v" id="calls">0</div></div>
    <div class="stat"><div class="k">Context</div><div class="v" id="context">0</div></div>
    <div class="stat"><div class="k">Cache Hit</div><div class="v" id="cache-hit">0%</div></div>
    <div class="stat"><div class="k">Output</div><div class="v" id="output">0</div></div>
    <div class="stat"><div class="k">Parallel</div><div class="v" id="parallel">0x</div></div>
  </section>
  <div class="error-strip" id="error-strip"></div>
  <div class="layout">
    <section>
      <h2 class="section-title">Execution Timeline</h2>
      <div class="panel timeline-panel">
        <div class="time-axis">
          <div class="axis-label">stage / agent</div>
          <div class="axis-track" id="axis"></div>
        </div>
        <div class="timeline-rows" id="timeline"></div>
      </div>
    </section>
    <aside>
      <h2 class="section-title">Inspector</h2>
      <div class="panel inspector" id="inspector"></div>
    </aside>
  </div>
  <div class="bottom-grid">
    <section>
      <h2 class="section-title">Token Distribution</h2>
      <div class="panel dist" id="distribution"></div>
    </section>
    <section>
      <h2 class="section-title">Recent Runtime Events</h2>
      <div class="panel inspector" id="events"></div>
    </section>
  </div>
</main>
<script>
let state = null;
let events = [];
let eventKeys = new Set();
let selectedRowID = '';
let pendingRefresh = null;
const nf = new Intl.NumberFormat();
function fmt(n) { return nf.format(Number(n || 0)); }
function pct(n) { return Number(n || 0).toFixed(1) + '%'; }
function esc(s) { return String(s == null ? '' : s).replace(/[&<>"']/g, ch => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[ch])); }
function ms(t) { const n = Date.parse(t || ''); return Number.isFinite(n) ? n : Date.now(); }
function dur(msValue) {
  msValue = Math.max(0, Number(msValue || 0));
  const s = Math.round(msValue / 1000);
  if (s < 60) return s + 's';
  const m = Math.floor(s / 60);
  const rest = s % 60;
  return m + 'm ' + rest + 's';
}
function totalTokens(u) {
  u = u || {};
  return Number(u.input || 0) + Number(u.cache_read || 0) + Number(u.cache_creation || 0) + Number(u.output || 0);
}
function cachePct(usage) {
  usage = usage || {};
  const context = Number(usage.total_context || 0) || (Number(usage.input || 0) + Number(usage.cache_read || 0) + Number(usage.cache_creation || 0));
  return context > 0 ? Number(usage.cache_read || 0) / context * 100 : 0;
}
function clampPct(n) { return Math.max(0, Math.min(100, n)); }
function timeBounds() {
  const tl = (state && state.timeline) || {};
  const start = ms(tl.start_time);
  let end = ms(tl.end_time);
  if (end <= start) end = start + 1000;
  return {start, end, span: Math.max(1, end - start)};
}
function timelineRows() {
  return (((state || {}).timeline || {}).rows || []);
}
function visibleTimelineRows() {
  const rows = timelineRows();
  const hasRealAgents = rows.some(r => r.kind === 'agent' && r.agent_id !== 'assistant');
  return rows.filter(r => !(hasRealAgents && r.kind === 'agent' && r.agent_id === 'assistant' && !Number(r.token_grand_total || 0)));
}
function leftOf(time) {
  const b = timeBounds();
  return clampPct((ms(time) - b.start) / b.span * 100);
}
function widthOf(row) {
  const b = timeBounds();
  return Math.max(.6, clampPct((ms(row.end_time) - ms(row.start_time)) / b.span * 100));
}
function tokenSegments(usage) {
  usage = usage || {};
  const total = Math.max(1, totalTokens(usage));
  const parts = [
    ['input', usage.input],
    ['cache', usage.cache_read],
    ['create', usage.cache_creation],
    ['output', usage.output]
  ];
  return parts.map(p => '<span class="token-seg ' + p[0] + '" style="width:' + clampPct(Number(p[1] || 0) / total * 100) + '%"></span>').join('');
}
function renderSummary() {
  const p = (state && state.pipeline) || {total:{}};
  const tl = (state && state.timeline) || {rows:[]};
  const rows = visibleTimelineRows();
  const agentRows = rows.filter(r => r.kind === 'agent');
  const allDone = agentRows.length > 0 && agentRows.every(r => r.status === 'completed');
  const visualStatus = (tl.error || rows.some(r => r.status === 'failed')) ? 'failed' : (allDone ? 'completed' : (p.status || 'live'));
  document.getElementById('pipeline-name').textContent = (p.name || 'Paw') + (state && state.session_id ? ' · ' + state.session_id : '');
  document.querySelector('.status').className = 'status ' + visualStatus;
  document.getElementById('status-text').textContent = visualStatus;
  document.getElementById('duration').textContent = dur(tl.duration_ms);
  document.getElementById('calls').textContent = fmt(p.calls);
  document.getElementById('context').textContent = fmt((p.total || {}).total_context);
  document.getElementById('cache-hit').textContent = pct((p.total || {}).cache_hit_rate);
  document.getElementById('output').textContent = fmt((p.total || {}).output);
  const parallel = Math.max(0, Number(tl.max_concurrency || 0));
  const parallelEl = document.getElementById('parallel');
  parallelEl.textContent = parallel + 'x';
  parallelEl.title = parallel > 1 ? dur(tl.overlap_ms) + ' overlapped' : 'no agent overlap in this run';
  const error = tl.error || '';
  const strip = document.getElementById('error-strip');
  strip.style.display = error ? 'block' : 'none';
  strip.textContent = error ? 'Error: ' + error : '';
}
function renderAxis() {
  const b = timeBounds();
  const axis = document.getElementById('axis');
  let html = '';
  for (let i = 0; i <= 4; i++) {
    const left = i * 25;
    const t = new Date(b.start + b.span * (i / 4));
    html += '<div class="tick" style="left:' + left + '%"><span>' + esc(t.toLocaleTimeString()) + '</span></div>';
  }
  axis.innerHTML = html;
}
function renderTimeline() {
  const rows = visibleTimelineRows();
  if (selectedRowID && !rows.some(r => r.id === selectedRowID)) {
    selectedRowID = '';
  }
  if (!selectedRowID && rows.length) {
    const failed = rows.find(r => r.status === 'failed' && r.kind === 'agent');
    selectedRowID = (failed || rows.find(r => r.kind === 'agent') || rows[0]).id;
  }
  const html = rows.map(row => {
    const left = leftOf(row.start_time);
    const width = widthOf(row);
    const markers = (row.markers || []).map(m => {
      const l = leftOf(m.time);
      return '<span class="marker ' + esc(m.type) + '" style="left:' + l + '%" title="' + esc(m.label + (m.detail ? ': ' + m.detail : '')) + '"></span>';
    }).join('');
    const status = row.status || 'live';
    const liveClass = status === 'live' ? ' live-pulse' : '';
    const cache = cachePct(row.usage);
    return '<div class="timeline-row ' + (row.id === selectedRowID ? 'selected' : '') + '" data-row="' + esc(row.id) + '">' +
      '<div class="row-label"><div class="row-kind">' + esc(row.kind) + ' · ' + esc(status) + '</div><div class="row-name">' + esc(row.display_name || row.name) + '</div><div class="row-meta">' + esc(row.session_id ? row.session_id.slice(0, 10) : row.stage_id || '') + ' · ' + dur(row.duration_ms) + ' · ' + fmt(row.token_grand_total) + ' tok · cache ' + pct(cache) + '</div></div>' +
      '<div class="row-track">' + markers + '<button class="gantt-bar ' + esc(row.kind) + ' ' + esc(status) + liveClass + '" style="left:' + left + '%;width:' + width + '%" data-row="' + esc(row.id) + '" title="' + esc(row.name + ' · ' + status) + '">' +
      '<span class="bar-fill"></span><span class="bar-text">' + esc(row.name) + ' · ' + fmt(row.token_grand_total) + '</span><span class="token-stack">' + tokenSegments(row.usage) + '</span></button></div></div>';
  }).join('');
  document.getElementById('timeline').innerHTML = html || '<div class="timeline-row"><div class="row-label"><div class="row-name">waiting</div><div class="row-meta">no timeline rows yet</div></div><div class="row-track"></div></div>';
  document.querySelectorAll('[data-row]').forEach(el => {
    el.addEventListener('click', ev => {
      selectedRowID = ev.currentTarget.getAttribute('data-row');
      render();
    });
  });
}
function renderDistribution() {
  const rows = ((((state || {}).timeline || {}).rows || []).filter(r => r.kind === 'agent' && r.token_grand_total > 0)).sort((a,b) => b.token_grand_total - a.token_grand_total);
  const max = Math.max(1, ...rows.map(r => r.token_grand_total));
  let html = rows.map(row => '<div class="dist-row"><div class="dist-name">' + esc(row.name) + '</div><div class="dist-bar" style="width:' + clampPct(row.token_grand_total / max * 100) + '%">' + tokenSegments(row.usage) + '</div><div class="dist-value">' + pct(row.token_share) + '</div></div>').join('');
  html += '<div class="legend"><span><span class="swatch" style="background:var(--input)"></span>input</span><span><span class="swatch" style="background:var(--cache)"></span>cache</span><span><span class="swatch" style="background:var(--create)"></span>create</span><span><span class="swatch" style="background:var(--output)"></span>output</span></div>';
  document.getElementById('distribution').innerHTML = html || '<div class="dist-name">waiting for model usage</div>';
}
function selectedRow() {
  const rows = visibleTimelineRows();
  return rows.find(r => r.id === selectedRowID) || rows.find(r => r.status === 'failed') || rows.find(r => r.kind === 'agent') || rows[0];
}
function renderInspector() {
  const row = selectedRow();
  const box = document.getElementById('inspector');
  if (!row) {
    box.innerHTML = '<h3>No row selected</h3><div class="event-list">Timeline data will appear after the first trace event.</div>';
    return;
  }
  const u = row.usage || {};
  const markers = (row.markers || []).slice(-8).reverse().map(m => '<div class="event-item">' + esc(new Date(ms(m.time)).toLocaleTimeString()) + ' · ' + esc(m.label) + (m.detail ? ' · ' + esc(m.detail) : '') + '</div>').join('');
  box.innerHTML = '<h3>' + esc(row.display_name || row.name) + '</h3><div class="kv">' +
    '<div>Status</div><div>' + esc(row.status) + '</div>' +
    '<div>Stage</div><div>' + esc(row.stage_name || row.stage_id) + '</div>' +
    '<div>Role</div><div>' + esc(row.role || row.kind) + '</div>' +
    '<div>Session</div><div>' + esc(row.session_id || '-') + '</div>' +
    '<div>Duration</div><div>' + dur(row.duration_ms) + '</div>' +
    '<div>Tokens</div><div>' + fmt(row.token_grand_total) + ' · ' + pct(row.token_share) + '</div>' +
    '<div>Input</div><div>' + fmt(u.input) + '</div>' +
    '<div>Cache</div><div>' + fmt(u.cache_read) + '</div>' +
    '<div>Output</div><div>' + fmt(u.output) + '</div>' +
    (row.error ? '<div>Error</div><div>' + esc(row.error) + '</div>' : '') +
    '</div><div class="event-list">' + (markers || 'no markers') + '</div>';
}
function renderEvents() {
  const compact = compactEvents(events);
  const html = compact.items.slice(-12).reverse().map(e => {
    const d = e.data || {};
    const detail = eventDetail(e);
    const label = eventLabel(e);
    const cls = d.error ? ' error' : '';
    return '<div class="event-item' + cls + '">' + esc(new Date(ms(e.timestamp)).toLocaleTimeString()) + ' · ' + esc(label) + (detail ? ' · ' + esc(detail) : '') + '</div>';
  }).join('');
  const note = compact.hidden ? '<div class="event-note">' + compact.hidden + ' duplicate/cleanup events hidden</div>' : '';
  document.getElementById('events').innerHTML = html ? html + note : '<div class="event-item">waiting for events</div>';
}
function eventDetail(event) {
  const d = (event && event.data) || {};
  return d.error || d.agent_id || d.name || d.role || '';
}
function eventLabel(event) {
  if (!event) return '';
  if (event.type === 'streamma.run.failed') return 'root cause';
  if (event.type === 'subagent_task_end') return 'subagent finished';
  return event.type;
}
function isWeakError(detail) {
  const value = String(detail || '').toLowerCase();
  return value.includes('context canceled') || value.includes('context cancelled') || value.includes('context deadline exceeded') || value === 'subagent failed';
}
function isTerminalCascade(type) {
  return type === 'agent_end' || type === 'stage_end' || type === 'turn_end';
}
function compactEvents(sourceEvents) {
  const rootError = String((((state || {}).timeline || {}).error) || '');
  const hasRootFailure = sourceEvents.some(e => e.type === 'streamma.run.failed' && eventDetail(e) === rootError);
  const seenErrorDetails = new Set();
  const items = [];
  let hidden = 0;
  for (const event of sourceEvents) {
    const detail = eventDetail(event);
    const hasError = !!((event.data || {}).error);
    if (hasError && rootError && isWeakError(detail)) {
      hidden++;
      continue;
    }
    if (hasError && rootError && hasRootFailure && detail === rootError && isTerminalCascade(event.type)) {
      hidden++;
      continue;
    }
    if (hasError && detail) {
      const key = event.type === 'streamma.run.failed' ? 'root:' + detail : 'error:' + detail;
      if (seenErrorDetails.has(key)) {
        hidden++;
        continue;
      }
      seenErrorDetails.add(key);
    }
    items.push(event);
  }
  return {items, hidden};
}
function render() {
  if (!state) return;
  renderSummary();
  renderAxis();
  renderTimeline();
  renderDistribution();
  renderInspector();
  renderEvents();
}
async function loadState() {
  const response = await fetch('/api/state', {cache: 'no-store'});
  state = await response.json();
  events = [];
  eventKeys = new Set();
  (state.events || []).forEach(addEvent);
  render();
}
function eventKey(event) {
  if (event && event.seq) return String(event.seq);
  return JSON.stringify([event && event.timestamp, event && event.type, event && event.data]);
}
function addEvent(event) {
  const key = eventKey(event);
  if (eventKeys.has(key)) return false;
  eventKeys.add(key);
  events.push(event);
  return true;
}
function scheduleRefresh() {
  if (pendingRefresh) return;
  pendingRefresh = setTimeout(() => {
    pendingRefresh = null;
    loadState().catch(() => {});
  }, 120);
}
loadState().catch(() => {});
const source = new EventSource('/events');
source.addEventListener('token_tracer', ev => {
  try { addEvent(JSON.parse(ev.data)); } catch (_) {}
  renderEvents();
  scheduleRefresh();
});
source.onerror = () => {
  document.getElementById('status-text').textContent = 'reconnecting';
};
</script>
</body>
</html>`

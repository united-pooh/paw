package tokentracer

const dashboardHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>运行可观测性 · 令牌追踪</title>
<style>
:root {
  --bg:#0a0f16; --surface:#111923; --surface-soft:#0e1620; --surface-muted:#16212d;
  --ink:#e7eef7; --muted:#8d9bad; --subtle:#5f7083; --line:#243241; --line-strong:#34485c;
  --blue:#5aa8ff; --blue-soft:#102b45; --teal:#49d3bd; --violet:#a88cff;
  --amber:#f2bd63; --red:#ff7373; --green:#56d68d;
  --input:#5b9cff; --cache:#46c9b5; --create:#e8b45d; --output:#ff806b;
}
*{box-sizing:border-box}
html,body{min-width:0;min-height:100%;}
body{margin:0;background:var(--bg);color:var(--ink);font:12px Inter,-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;letter-spacing:.01em}
.header-tools{display:flex;align-items:center;gap:7px}.run-chip{display:flex;align-items:center;gap:7px;color:var(--green);font-size:11px;font-weight:650;margin-right:4px}.tool-btn{border:1px solid var(--line);border-radius:6px;background:#121d28;color:var(--muted);padding:6px 9px;font-size:10px;cursor:pointer}.tool-btn:hover,.tool-btn.active{border-color:var(--blue);color:var(--ink);background:var(--blue-soft)}
header{height:58px;display:flex;align-items:center;justify-content:space-between;padding:0 24px;border-bottom:1px solid var(--line);background:rgba(10,15,22,.88);backdrop-filter:blur(14px);box-shadow:0 1px 20px #0005;position:sticky;top:0;z-index:5}
.brand{display:flex;align-items:center;gap:11px;min-width:0}.mark{width:28px;height:28px;border-radius:8px;display:grid;place-items:center;background:linear-gradient(135deg,#5aa8ff,#4bd1bc);color:#071019;font-size:10px;font-weight:800;letter-spacing:-.08em;box-shadow:0 4px 16px #4ca9d633}.title{font-size:15px;font-weight:750;white-space:nowrap}.pipe{margin-left:4px;padding-left:13px;border-left:1px solid var(--line);color:var(--muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.status{display:flex;align-items:center;gap:8px;color:var(--green);font-size:11px;font-weight:650}.status-dot{width:8px;height:8px;border-radius:50%;background:currentColor;box-shadow:0 0 0 4px color-mix(in srgb,currentColor 13%,transparent)}.status.failed,.run-chip.failed{color:var(--red)}.status.completed,.run-chip.completed{color:var(--muted)}
main{max-width:1720px;margin:0 auto;padding:20px 24px 30px}.summary{display:grid;grid-template-columns:1.35fr repeat(5,minmax(110px,1fr));border:1px solid var(--line);border-radius:12px;background:linear-gradient(135deg,#131e2a,#101821);box-shadow:0 10px 28px #0004;overflow:hidden}.stat{min-width:0;padding:13px 16px;border-right:1px solid var(--line)}.stat:last-child{border-right:0}.k{color:var(--muted);font-size:10px;font-weight:650;letter-spacing:.06em}.v{margin-top:7px;font-size:21px;line-height:1;font-weight:760;color:var(--ink);overflow-wrap:anywhere}.observe-grid{display:grid;grid-template-columns:1.05fr 1.25fr .9fr;gap:12px;margin-top:14px}.observe-card{padding:13px 14px;border:1px solid var(--line);border-radius:10px;background:linear-gradient(145deg,#121e2a,#0f1720);box-shadow:0 8px 22px #0003;min-height:108px}.observe-head{display:flex;align-items:center;justify-content:space-between;margin-bottom:11px}.observe-title{color:var(--muted);font-size:10px;font-weight:700;letter-spacing:.05em}.observe-value{font-size:19px;font-weight:760;color:var(--ink)}.observe-meta{color:var(--subtle);font-size:10px}.health-bar{height:7px;margin:12px 0 9px;border-radius:99px;background:#1d2a37;overflow:hidden}.health-fill{height:100%;border-radius:inherit;background:linear-gradient(90deg,var(--teal),var(--blue));box-shadow:0 0 12px #49d3bd66}.health-fill.warn{background:linear-gradient(90deg,var(--amber),var(--red))}.mini-bars{display:flex;align-items:end;gap:4px;height:42px}.mini-bar{flex:1;min-width:4px;border-radius:3px 3px 1px 1px;background:linear-gradient(180deg,var(--blue),#28517d);opacity:.86}.mini-bar.hot{background:linear-gradient(180deg,var(--amber),#8b5727)}.mini-bar.fail{background:linear-gradient(180deg,var(--red),#743341)}.signal-list{display:grid;gap:7px}.signal{display:flex;align-items:center;justify-content:space-between;color:var(--muted);font-size:10px}.signal strong{color:var(--ink);font-size:11px}.signal-dot{width:6px;height:6px;border-radius:50%;display:inline-block;margin-right:6px;background:var(--teal);box-shadow:0 0 8px currentColor}.signal-dot.warn{background:var(--amber)}.signal-dot.fail{background:var(--red)}
.error-strip{margin-top:12px;padding:10px 13px;border:1px solid #713f4a;border-radius:8px;background:#27171d;color:#ff9a9a;display:none}.layout{display:grid;grid-template-columns:minmax(0,1fr) 320px;gap:16px;margin-top:18px}.section-title{display:flex;align-items:center;gap:8px;margin:0 0 8px;color:var(--ink);font-size:12px;font-weight:750}.section-title:before{content:"";width:3px;height:14px;border-radius:3px;background:var(--blue);box-shadow:0 0 12px #5aa8ff88}.panel{border:1px solid var(--line);border-radius:12px;background:var(--surface);min-width:0;box-shadow:0 10px 28px #0003;overflow:hidden}.timeline-panel{background:#0e1721}.time-axis{display:grid;grid-template-columns:205px minmax(0,1fr);border-bottom:1px solid var(--line);background:#131e29}.axis-label{padding:10px 13px;color:var(--muted);border-right:1px solid var(--line);font-size:10px;font-weight:650}.axis-track{position:relative;min-height:31px}.tick{position:absolute;top:0;bottom:0;width:1px;background:#7890a522}.tick span{position:absolute;top:10px;left:6px;color:var(--subtle);white-space:nowrap;font-size:9px}.timeline-rows{max-height:560px;overflow:auto}.timeline-row.dimmed{opacity:.22}.timeline-row.critical .gantt-bar{border-color:var(--blue);box-shadow:0 0 0 2px #5aa8ff33,0 2px 7px #315da555}.timeline-row.bottleneck .gantt-bar{border-color:var(--red);box-shadow:0 0 0 2px #ff737344,0 2px 8px #ff737355}.timeline-row.filtered-out{display:none}.timeline-toolbar{display:flex;align-items:center;gap:8px;padding:8px 10px;border-bottom:1px solid var(--line);background:#101a25;color:var(--muted);font-size:10px}.timeline-toolbar strong{color:var(--ink)}.timeline-row{display:grid;grid-template-columns:205px minmax(0,1fr);height:35px;border-bottom:1px solid #1b2835}.timeline-row:last-child{border-bottom:0}.timeline-row:hover{background:#152536}.timeline-row.selected{background:#102b45}.row-label{display:flex;align-items:center;gap:7px;padding:0 12px;border-right:1px solid var(--line);overflow:hidden;color:var(--muted)}.row-kind{flex:0 0 auto;color:var(--subtle);font-size:9px}.row-name{min-width:0;font-size:11px;font-weight:650;color:var(--ink);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.row-track{position:relative;min-height:35px;background:repeating-linear-gradient(90deg,transparent 0,transparent calc(10% - 1px),#dce3ec55 calc(10% - 1px),#dce3ec55 10%)}.gantt-bar{position:absolute;top:8px;height:19px;min-width:6px;border:1px solid #8eb0ef;border-radius:5px;background:linear-gradient(180deg,#6d9def,#4f80dc);overflow:hidden;cursor:pointer;box-shadow:0 2px 5px #315da522}.gantt-bar.stage{top:7px;height:21px;background:linear-gradient(180deg,#38b5a9,#159486);border-color:#70cfc6}.gantt-bar.live{border-color:var(--amber);box-shadow:0 0 0 3px #d9911622,0 2px 6px #315da522}.gantt-bar.failed{border-color:#ef9797;background:linear-gradient(180deg,#df7777,#c84f4f)}.bar-fill{position:absolute;inset:0;background:linear-gradient(90deg,#fff5,transparent 70%)}.token-stack{position:absolute;left:0;right:0;bottom:0;height:4px;display:flex;opacity:.95}.token-seg.input{background:var(--input)}.token-seg.cache{background:var(--cache)}.token-seg.create{background:var(--create)}.token-seg.output{background:var(--output)}.bar-text{position:absolute;left:7px;right:7px;top:2px;color:#fff;font-size:10px;font-weight:700;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;text-shadow:0 1px 2px #123}.marker{position:absolute;top:7px;width:1px;height:21px;background:#9eacbf;opacity:.8}.marker.api_call{background:var(--violet)}.marker.step{top:14px;width:7px;height:7px;transform:rotate(45deg);background:var(--amber)}.marker.failure{top:5px;width:3px;height:25px;background:var(--red);opacity:1}.marker.start{background:var(--teal)}.marker.end{background:#8793a3}.bottom-grid{display:grid;grid-template-columns:1.1fr 1fr 1fr;gap:12px;margin-top:18px}.metric-panel{padding:13px}.metric-list{display:grid;gap:8px}.metric-row{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:8px;align-items:center;color:var(--muted);font-size:10px}.metric-row strong{color:var(--ink);font-size:11px}.metric-bar{height:5px;margin-top:4px;border-radius:99px;background:#1b2835;overflow:hidden}.metric-bar i{display:block;height:100%;border-radius:inherit;background:linear-gradient(90deg,var(--blue),var(--teal))}.metric-bar i.warn{background:linear-gradient(90deg,var(--amber),var(--red))}
.dist-row{display:grid;grid-template-columns:118px minmax(0,1fr) 58px;gap:10px;align-items:center;margin:9px 0}.dist-name{color:var(--muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.dist-bar{height:20px;border:1px solid var(--line);border-radius:5px;background:var(--surface-muted);display:flex;overflow:hidden}.dist-value{text-align:right;color:var(--ink);font-weight:700}.legend{display:flex;gap:14px;flex-wrap:wrap;padding-top:10px;color:var(--muted);font-size:10px;border-top:1px solid var(--line);margin-top:12px}.swatch{display:inline-block;width:9px;height:9px;margin-right:5px;vertical-align:-1px;border-radius:3px}.inspector{padding:15px;min-height:230px}.inspector h3{margin:0;font-size:14px;font-weight:750}.kv{display:grid;grid-template-columns:64px minmax(0,1fr);gap:8px 10px;margin-top:13px}.kv div:nth-child(odd){color:var(--muted)}.kv div:nth-child(even){overflow-wrap:anywhere;font-weight:600}.event-list{margin-top:14px;border-top:1px solid var(--line);padding-top:10px;color:var(--muted)}.event-item{padding:5px 0;overflow-wrap:anywhere;line-height:1.35}.event-item.error{color:var(--red)}.event-note{padding-top:7px;color:var(--subtle);font-size:10px}.live-pulse{animation:pulse 1.3s ease-in-out infinite}@keyframes pulse{0%,100%{opacity:.82}50%{opacity:1}}@media(max-width:980px){header{padding:0 16px}.layout,.bottom-grid{grid-template-columns:1fr}main{padding:16px}.summary{grid-template-columns:repeat(2,minmax(0,1fr))}.stat{border-bottom:1px solid var(--line)}.time-axis,.timeline-row{grid-template-columns:170px minmax(520px,1fr)}}
</style>
</head>
<body>
<header>
  <div class="brand">
    <span class="mark">TT</span>
    <span class="title">令牌追踪</span>
    <span class="pipe" id="pipeline-name">Paw</span>
  </div>
  <div class="header-tools"><span class="run-chip"><span class="status-dot"></span><span id="status-text">实时</span></span><button class="tool-btn" id="pause-btn" type="button">暂停刷新</button><button class="tool-btn" id="critical-btn" type="button">关键路径</button><button class="tool-btn" id="error-btn" type="button">仅看异常</button><button class="tool-btn" id="export-btn" type="button">导出 JSON</button></div>
</header>
<main>
  <section class="summary">
    <div class="stat"><div class="k">运行时长</div><div class="v" id="duration">0s</div></div>
    <div class="stat"><div class="k">调用次数</div><div class="v" id="calls">0</div></div>
    <div class="stat"><div class="k">上下文</div><div class="v" id="context">0</div></div>
    <div class="stat"><div class="k">缓存命中</div><div class="v" id="cache-hit">0%</div></div>
    <div class="stat"><div class="k">输出令牌</div><div class="v" id="output">0</div></div>
    <div class="stat"><div class="k">并行度</div><div class="v" id="parallel">0x</div></div>
  </section>
  <section class="observe-grid">
    <div class="observe-card">
      <div class="observe-head"><span class="observe-title">运行健康</span><span class="observe-meta" id="health-label">正常</span></div>
      <div class="observe-value" id="health-value">100%</div>
      <div class="health-bar"><div class="health-fill" id="health-fill" style="width:100%"></div></div>
      <div class="observe-meta" id="health-meta">暂无异常信号</div>
    </div>
    <div class="observe-card">
      <div class="observe-head"><span class="observe-title">令牌吞吐</span><span class="observe-meta" id="throughput-meta">平均速率</span></div>
      <div class="observe-value" id="throughput-value">0 / 秒</div>
      <div class="mini-bars" id="throughput-bars"></div>
      <div class="observe-meta" id="throughput-detail">输入 0 · 输出 0</div>
    </div>
    <div class="observe-card">
      <div class="observe-head"><span class="observe-title">运行信号</span><span class="observe-meta">实时</span></div>
      <div class="signal-list" id="signal-list"></div>
    </div>
  </section>
  <div class="error-strip" id="error-strip"></div>
  <div class="layout">
    <section>
      <h2 class="section-title">执行瀑布</h2>
      <div class="panel timeline-panel">
        <div class="timeline-toolbar"><strong>执行火焰图</strong><span id="timeline-summary">全部节点</span><span style="margin-left:auto" id="timeline-mode">实时视图</span></div>
        <div class="time-axis">
          <div class="axis-label">执行层级 / 时间</div>
          <div class="axis-track" id="axis"></div>
        </div>
        <div class="timeline-rows" id="timeline"></div>
      </div>
    </section>
    <aside>
      <h2 class="section-title">详情检查器</h2>
      <div class="panel inspector" id="inspector"></div>
    </aside>
  </div>
  <div class="bottom-grid">
    <section>
      <h2 class="section-title">令牌用量</h2>
      <div class="panel dist" id="distribution"></div>
    </section>
    <section>
      <h2 class="section-title">模型观测</h2>
      <div class="panel metric-panel" id="model-metrics"></div>
    </section>
    <section>
      <h2 class="section-title">错误聚合</h2>
      <div class="panel metric-panel" id="error-metrics"></div>
    </section>
  </div>
  <div class="bottom-grid event-grid">
    <section style="grid-column:1 / -1">
      <h2 class="section-title">事件流</h2>
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
let paused = false;
let criticalOnly = false;
let errorsOnly = false;
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
  return rows.filter(r => {
    if (hasRealAgents && r.kind === 'agent' && r.agent_id === 'assistant' && !Number(r.token_grand_total || 0)) return false;
    if (criticalOnly && !r.critical && !r.bottleneck) return false;
    if (errorsOnly && r.status !== 'failed' && !r.error) return false;
    return true;
  });
}
function allTimelineRows() {
  return timelineRows().filter(r => {
    const hasRealAgents = timelineRows().some(item => item.kind === 'agent' && item.agent_id !== 'assistant');
    return !(hasRealAgents && r.kind === 'agent' && r.agent_id === 'assistant' && !Number(r.token_grand_total || 0));
  });
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
  document.querySelector('.run-chip').className = 'run-chip ' + visualStatus;
  document.getElementById('status-text').textContent = ({live:'实时运行', running:'运行中', completed:'已完成', failed:'运行失败', pending:'等待中'})[visualStatus] || visualStatus;
  document.getElementById('duration').textContent = dur(tl.duration_ms);
  document.getElementById('calls').textContent = fmt(p.calls);
  document.getElementById('context').textContent = fmt((p.total || {}).total_context);
  document.getElementById('cache-hit').textContent = pct((p.total || {}).cache_hit_rate);
  document.getElementById('output').textContent = fmt((p.total || {}).output);
  const parallel = Math.max(0, Number(tl.max_concurrency || 0));
  const parallelEl = document.getElementById('parallel');
  parallelEl.textContent = parallel + 'x';
  parallelEl.title = parallel > 1 ? dur(tl.overlap_ms) + ' 重叠' : '本次运行没有代理重叠';
  const error = tl.error || '';
  const strip = document.getElementById('error-strip');
  strip.style.display = error ? 'block' : 'none';
  strip.textContent = error ? '错误: ' + error : '';
}
function renderObserve() {
  const p = (state && state.pipeline) || {total:{}};
  const tl = (state && state.timeline) || {};
  const rows = visibleTimelineRows();
  const failed = rows.filter(r => r.status === 'failed').length;
  const live = rows.filter(r => r.status === 'live' || r.status === 'running').length;
  const total = rows.length;
  const health = failed ? Math.max(18, Math.round((1 - failed / Math.max(1, total)) * 100)) : 100;
  const healthLabel = failed ? '需关注' : (live ? '运行中' : '正常');
  const healthFill = document.getElementById('health-fill');
  document.getElementById('health-value').textContent = health + '%';
  document.getElementById('health-label').textContent = healthLabel;
  document.getElementById('health-meta').textContent = failed ? failed + ' 个失败信号' : (live ? live + ' 个活动执行单元' : '暂无异常信号');
  healthFill.style.width = health + '%';
  healthFill.className = 'health-fill' + (failed ? ' warn' : '');

  const usage = p.total || {};
  const tokenTotal = totalTokens(usage);
  const durationMS = Math.max(1000, Number(tl.duration_ms || 0));
  const rate = tokenTotal / (durationMS / 1000);
  document.getElementById('throughput-value').textContent = fmt(Math.round(rate)) + ' / 秒';
  document.getElementById('throughput-detail').textContent = '输入 ' + fmt(usage.input) + ' · 输出 ' + fmt(usage.output);
  const bars = rows.slice(-12).map(row => {
    const value = Math.max(2, Number(row.token_grand_total || 0));
    const height = Math.min(100, 14 + Math.log10(value + 1) * 22);
    const cls = row.status === 'failed' ? ' fail' : (row.status === 'live' || row.status === 'running' ? ' hot' : '');
    return '<span class="mini-bar' + cls + '" style="height:' + height + '%" title="' + esc(row.display_name || row.name) + '"></span>';
  }).join('');
  document.getElementById('throughput-bars').innerHTML = bars || '<span class="observe-meta">等待令牌数据</span>';

  const cacheRate = Number(usage.cache_hit_rate || 0);
  document.getElementById('signal-list').innerHTML =
    '<div class="signal"><span><i class="signal-dot"></i>缓存命中</span><strong>' + pct(cacheRate) + '</strong></div>' +
    '<div class="signal"><span><i class="signal-dot"></i>并行执行</span><strong>' + fmt(tl.max_concurrency) + ' 路</strong></div>' +
    '<div class="signal"><span><i class="signal-dot ' + (failed ? 'fail' : (live ? 'warn' : '')) + '"></i>异常事件</span><strong>' + fmt(failed) + '</strong></div>';
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
function displayKind(kind) {
  return ({agent:'代理', stage:'阶段', session:'会话', tool:'工具', model:'模型'})[kind] || kind || '事件';
}
function displayStatus(status) {
  return ({live:'实时', running:'运行中', completed:'已完成', failed:'失败', pending:'等待中'})[status] || status || '未知';
}
function renderTimeline() {
  const rows = visibleTimelineRows();
  const allRows = allTimelineRows();
  if (selectedRowID && !rows.some(r => r.id === selectedRowID)) selectedRowID = '';
  if (!selectedRowID && rows.length) {
    const failed = rows.find(r => r.status === 'failed' && r.kind === 'agent');
    selectedRowID = (failed || rows.find(r => r.kind === 'agent') || rows[0]).id;
  }
  document.getElementById('timeline-summary').textContent = (criticalOnly ? '关键路径 · ' : (errorsOnly ? '异常节点 · ' : '全部节点 · ')) + rows.length + '/' + allRows.length;
  document.getElementById('timeline-mode').textContent = paused ? '已暂停' : (criticalOnly ? '关键路径视图' : (errorsOnly ? '异常视图' : '实时视图'));
  const html = rows.map(row => {
    const left = leftOf(row.start_time), width = widthOf(row);
    const markers = (row.markers || []).map(m => {
      const label = m.label + (m.detail ? ': ' + m.detail : '');
      return '<span class="marker ' + esc(m.type) + '" style="left:' + leftOf(m.time) + '%" title="' + esc(label) + '"></span>';
    }).join('');
    const status = row.status || 'live';
    const liveClass = status === 'live' ? ' live-pulse' : '';
    const indent = row.kind === 'agent' ? '　' : row.kind === 'stage' ? '' : '　　';
    const rowClasses = [
      'timeline-row',
      row.id === selectedRowID ? 'selected' : '',
      row.critical ? 'critical' : '',
      row.bottleneck ? 'bottleneck' : '',
      errorsOnly && row.status !== 'failed' && !row.error ? 'dimmed' : ''
    ].filter(Boolean).join(' ');
    return '<div class="' + rowClasses + '" data-row="' + esc(row.id) + '">' +
      '<div class="row-label"><span class="row-kind">' + displayKind(row.kind) + ' · ' + displayStatus(status) + '</span><span class="row-name">' + indent + esc(row.display_name || row.name) + '</span></div>' +
      '<div class="row-track">' + markers + '<button class="gantt-bar ' + esc(row.kind) + ' ' + esc(status) + liveClass + '" style="left:' + left + '%;width:' + width + '%" data-row="' + esc(row.id) + '" title="' + esc(row.name + ' · ' + displayStatus(status)) + '">' +
      '<span class="bar-fill"></span><span class="bar-text">' + esc(row.display_name || row.name) + ' · ' + fmt(row.token_grand_total) + ' 令牌</span><span class="token-stack">' + tokenSegments(row.usage) + '</span></button></div></div>';
  }).join('');
  document.getElementById('timeline').innerHTML = html || '<div class="timeline-row"><div class="row-label"><span class="row-name">等待数据</span></div><div class="row-track"></div></div>';
  document.querySelectorAll('[data-row]').forEach(el => el.addEventListener('click', ev => { selectedRowID = ev.currentTarget.getAttribute('data-row'); render(); }));
}
function renderDistribution() {
  const rows = ((((state || {}).timeline || {}).rows || []).filter(r => r.kind === 'agent' && r.token_grand_total > 0)).sort((a,b) => b.token_grand_total - a.token_grand_total);
  const max = Math.max(1, ...rows.map(r => r.token_grand_total));
  let html = rows.map(row => '<div class="dist-row"><div class="dist-name">' + esc(row.name) + '</div><div class="dist-bar" style="width:' + clampPct(row.token_grand_total / max * 100) + '%">' + tokenSegments(row.usage) + '</div><div class="dist-value">' + pct(row.token_share) + '</div></div>').join('');
  html += '<div class="legend"><span><span class="swatch" style="background:var(--input)"></span>输入</span><span><span class="swatch" style="background:var(--cache)"></span>缓存</span><span><span class="swatch" style="background:var(--create)"></span>创建</span><span><span class="swatch" style="background:var(--output)"></span>输出</span></div>';
  document.getElementById('distribution').innerHTML = html || '<div class="dist-name">等待模型用量</div>';
}
function renderModelMetrics() {
  const rows = allTimelineRows().filter(r => r.model || r.provider || r.calls);
  const groups = {};
  rows.forEach(row => {
    const key = row.provider || row.model ? (row.provider || '未知提供方') + ' · ' + (row.model || '未知模型') : '未标注模型';
    const item = groups[key] || {calls:0, tokens:0, duration:0, failed:0};
    item.calls += Number(row.calls || 0);
    item.tokens += Number(row.token_grand_total || 0);
    item.duration += Number(row.duration_ms || 0);
    if (row.status === 'failed' || row.error) item.failed++;
    groups[key] = item;
  });
  const items = Object.entries(groups).sort((a,b) => b[1].tokens - a[1].tokens).slice(0, 6);
  const max = Math.max(1, ...items.map(item => item[1].tokens));
  document.getElementById('model-metrics').innerHTML = items.length ? '<div class="metric-list">' + items.map(([name, item]) =>
    '<div class="metric-row"><span>' + esc(name) + '<div class="metric-bar"><i style="width:' + clampPct(item.tokens / max * 100) + '%"></i></div></span><strong>' + fmt(item.tokens) + ' 令牌<br><small>' + fmt(item.calls) + ' 次 · ' + dur(item.duration) + '</small></strong></div>'
  ).join('') + '</div>' : '<div class="observe-meta">暂无模型调用数据</div>';
}
function renderErrorMetrics() {
  const rows = allTimelineRows();
  const errors = rows.filter(row => row.status === 'failed' || row.error);
  const groups = {};
  errors.forEach(row => {
    const key = row.error || '未分类异常';
    groups[key] = (groups[key] || 0) + 1;
  });
  const items = Object.entries(groups).sort((a,b) => b[1] - a[1]).slice(0, 6);
  const max = Math.max(1, ...items.map(item => item[1]));
  document.getElementById('error-metrics').innerHTML = items.length ? '<div class="metric-list">' + items.map(([name, count]) =>
    '<div class="metric-row"><span>' + esc(name) + '<div class="metric-bar"><i class="warn" style="width:' + clampPct(count / max * 100) + '%"></i></div></span><strong>' + fmt(count) + ' 次</strong></div>'
  ).join('') + '</div>' : '<div class="observe-meta">暂无错误信号</div>';
}
function selectedRow() {
  const rows = visibleTimelineRows();
  return rows.find(r => r.id === selectedRowID) || rows.find(r => r.status === 'failed') || rows.find(r => r.kind === 'agent') || rows[0];
}
function renderInspector() {
  const row = selectedRow();
  const box = document.getElementById('inspector');
  if (!row) {
    box.innerHTML = '<h3>未选择行</h3><div class="event-list">首个追踪事件出现后将显示时间线数据。</div>';
    return;
  }
  const u = row.usage || {};
  const markers = (row.markers || []).slice(-8).reverse().map(m => '<div class="event-item">' + esc(new Date(ms(m.time)).toLocaleTimeString()) + ' · ' + esc(m.label) + (m.detail ? ' · ' + esc(m.detail) : '') + '</div>').join('');
  box.innerHTML = '<h3>' + esc(row.display_name || row.name) + '</h3><div class="kv">' +
    '<div>状态</div><div>' + esc(row.status) + '</div>' +
    '<div>阶段</div><div>' + esc(row.stage_name || row.stage_id) + '</div>' +
    '<div>角色</div><div>' + esc(row.role || row.kind) + '</div>' +
    '<div>会话</div><div>' + esc(row.session_id || '-') + '</div>' +
    '<div>耗时</div><div>' + dur(row.duration_ms) + '</div>' +
    '<div>令牌</div><div>' + fmt(row.token_grand_total) + ' · ' + pct(row.token_share) + '</div>' +
    '<div>输入</div><div>' + fmt(u.input) + '</div>' +
    '<div>缓存</div><div>' + fmt(u.cache_read) + '</div>' +
    '<div>输出令牌</div><div>' + fmt(u.output) + '</div>' +
    (row.error ? '<div>错误</div><div>' + esc(row.error) + '</div>' : '') +
    '</div><div class="event-list">' + (markers || '暂无标记') + '</div>';
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
  document.getElementById('events').innerHTML = html ? html + note : '<div class="event-item">等待事件</div>';
}
function eventDetail(event) {
  const d = (event && event.data) || {};
  return d.error || d.agent_id || d.name || d.role || '';
}
function eventLabel(event) {
  if (!event) return '';
  const labels = {stage_start:'阶段开始', stage_end:'阶段结束', agent_start:'代理开始', agent_end:'代理结束', api_call:'模型调用', subagent_task_start:'子任务开始', subagent_task_end:'子任务完成', 'streamma.subagent_start':'子代理启动', 'streamma.subagent_end':'子代理结束', 'streamma.agent.step.committed':'步骤提交', 'streamma.run.failed':'根因'};
  return labels[event.type] || event.type || '事件';
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
  renderObserve();
  renderAxis();
  renderTimeline();
  renderDistribution();
  renderModelMetrics();
  renderErrorMetrics();
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
  if (paused) return;
  if (pendingRefresh) return;
  pendingRefresh = setTimeout(() => {
    pendingRefresh = null;
    loadState().catch(() => {});
  }, 120);
}
function updateControlButtons() {
  const pause = document.getElementById('pause-btn');
  const critical = document.getElementById('critical-btn');
  const error = document.getElementById('error-btn');
  pause.textContent = paused ? '继续刷新' : '暂停刷新';
  pause.classList.toggle('active', paused);
  critical.classList.toggle('active', criticalOnly);
  error.classList.toggle('active', errorsOnly);
}
function exportSnapshot() {
  const payload = JSON.stringify({state: state, events: events}, null, 2);
  const blob = new Blob([payload], {type: 'application/json'});
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = ((state && state.run_id) || 'token-trace') + '.json';
  link.click();
  URL.revokeObjectURL(url);
}
document.getElementById('pause-btn').addEventListener('click', () => {
  paused = !paused;
  updateControlButtons();
  render();
});
document.getElementById('critical-btn').addEventListener('click', () => {
  criticalOnly = !criticalOnly;
  updateControlButtons();
  render();
});
document.getElementById('error-btn').addEventListener('click', () => {
  errorsOnly = !errorsOnly;
  updateControlButtons();
  render();
});
document.getElementById('export-btn').addEventListener('click', exportSnapshot);
updateControlButtons();
loadState().catch(() => {});
const source = new EventSource('/events');
source.addEventListener('token_tracer', ev => {
  try { addEvent(JSON.parse(ev.data)); } catch (_) {}
  renderEvents();
  scheduleRefresh();
});
source.onerror = () => {
  document.getElementById('status-text').textContent = '重新连接中';
};
</script>
</body>
</html>`

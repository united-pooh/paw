package tokentracer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type ServerConfig struct {
	Host        string
	Port        int
	OpenBrowser bool
}

type Server struct {
	tracer *Tracer
	cfg    ServerConfig
	server *http.Server
	url    string
}

func NewServer(tracer *Tracer, cfg ServerConfig) *Server {
	if strings.TrimSpace(cfg.Host) == "" {
		cfg.Host = "127.0.0.1"
	}
	return &Server{tracer: tracer, cfg: cfg}
}

func (s *Server) Start(ctx context.Context) error {
	if s == nil || s.tracer == nil {
		return fmt.Errorf("token tracer server requires a tracer")
	}
	host := strings.TrimSpace(s.cfg.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(s.cfg.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("start token tracer listener: %w", err)
	}
	actual := listener.Addr().(*net.TCPAddr)
	s.url = fmt.Sprintf("http://%s:%d", host, actual.Port)
	s.tracer.SetServerURL(s.url)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/healthz", s.handleHealthz)

	s.server = &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.tracer.RecordEvent("server_error", map[string]any{"error": err.Error()})
		}
	}()
	if s.cfg.OpenBrowser {
		openBrowser(s.url)
	}
	return nil
}

func (s *Server) URL() string {
	if s == nil {
		return ""
	}
	return s.url
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(dashboardHTML))
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(s.tracer.Snapshot()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	events, unsubscribe := s.tracer.Subscribe(true)
	defer unsubscribe()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: token_tracer\n")
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte(`{"ok":true}` + "\n"))
}

func openBrowser(url string) {
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

const legacyDashboardHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Token Tracer</title>
<style>
:root {
  --bg: #101318;
  --surface: #151a20;
  --surface-2: #1b222b;
  --line: #2a313b;
  --text: #e5e7eb;
  --muted: #9aa4b2;
  --soft: #6b7280;
  --accent: #51d0b5;
  --input: #5b8def;
  --cache: #55c0a6;
  --create: #d6a85f;
  --output: #ef7f64;
  --error: #ff6b6b;
}
* { box-sizing: border-box; }
body {
  margin: 0;
  background: var(--bg);
  color: var(--text);
  font: 13px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
header {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 16px;
  align-items: start;
  padding: 18px 24px 14px;
  border-bottom: 1px solid var(--line);
}
.brand { display: flex; align-items: center; gap: 10px; min-width: 0; }
.mark {
  width: 34px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid #377d74;
  background: #102622;
  color: var(--accent);
  font-weight: 800;
}
.title { font-size: 16px; font-weight: 800; white-space: nowrap; }
.pipe { min-width: 0; overflow: hidden; text-overflow: ellipsis; color: var(--muted); }
.status { display: flex; gap: 8px; align-items: center; justify-content: flex-end; color: var(--accent); }
.status-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--accent); box-shadow: 0 0 0 5px rgba(81,208,181,.1); }
main { padding: 20px 24px 30px; }
.summary {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  border: 1px solid var(--line);
  background: var(--surface);
}
.stat { min-width: 0; padding: 13px 15px; border-right: 1px solid var(--line); }
.stat:last-child { border-right: 0; }
.k { color: var(--muted); font-size: 10px; letter-spacing: .08em; text-transform: uppercase; }
.v { margin-top: 7px; font-size: 22px; line-height: 1.1; font-weight: 800; overflow-wrap: anywhere; }
.layout {
  display: grid;
  grid-template-columns: minmax(0, 1.65fr) minmax(320px, .85fr);
  gap: 22px;
  margin-top: 22px;
}
section { min-width: 0; }
.section-title {
  margin: 0 0 10px;
  color: var(--muted);
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: .08em;
}
.panel {
  border: 1px solid var(--line);
  background: var(--surface);
  min-width: 0;
}
.lanes { padding: 14px; }
.lane { display: grid; grid-template-columns: 92px minmax(0, 1fr); gap: 10px; align-items: center; margin: 8px 0; }
.lane-label { color: var(--muted); text-align: right; font-size: 11px; overflow: hidden; text-overflow: ellipsis; }
.track { height: 32px; position: relative; background: #0d1117; overflow: hidden; border: 1px solid #202732; }
.bar { position: absolute; top: 0; bottom: 0; min-width: 2px; display: flex; overflow: hidden; border-right: 1px solid #0d1117; }
.seg { height: 100%; }
.seg.input { background: var(--input); }
.seg.cache { background: var(--cache); }
.seg.create { background: var(--create); }
.bar-label { position: absolute; left: 7px; top: 8px; color: #fff; font-size: 11px; font-weight: 800; white-space: nowrap; text-shadow: 0 1px 3px #000; max-width: calc(100% - 12px); overflow: hidden; text-overflow: ellipsis; }
.legend { display: flex; flex-wrap: wrap; gap: 12px; padding: 0 14px 14px; color: var(--muted); font-size: 11px; }
.swatch { width: 10px; height: 10px; display: inline-block; margin-right: 5px; vertical-align: -1px; }
table { width: 100%; border-collapse: collapse; font-variant-numeric: tabular-nums; }
th, td { border-bottom: 1px solid #202732; padding: 8px 9px; text-align: right; white-space: nowrap; }
th { color: var(--muted); font-size: 10px; letter-spacing: .06em; text-transform: uppercase; }
th:first-child, td:first-child { text-align: left; white-space: normal; }
.stage-row td { color: var(--text); background: var(--surface-2); font-weight: 800; }
.agent-name { color: var(--muted); padding-left: 18px; }
.events { height: 520px; overflow: auto; padding: 10px; }
.event {
  border-bottom: 1px solid #202732;
  padding: 8px 2px;
  color: var(--muted);
  overflow-wrap: anywhere;
}
.event strong { color: var(--text); }
.event-time { color: var(--soft); }
.error { color: var(--error); }
@media (max-width: 980px) {
  header { grid-template-columns: 1fr; }
  .status { justify-content: flex-start; }
  main { padding: 16px; }
  .summary { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .stat { border-bottom: 1px solid var(--line); }
  .layout { grid-template-columns: 1fr; }
  .lane { grid-template-columns: 72px minmax(0, 1fr); }
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
  <div class="status"><span class="status-dot"></span><span id="status-text">LIVE</span></div>
</header>
<main>
  <section class="summary">
    <div class="stat"><div class="k">Context</div><div class="v" id="total-context">0</div></div>
    <div class="stat"><div class="k">Input</div><div class="v" id="input-tokens">0</div></div>
    <div class="stat"><div class="k">Cached</div><div class="v" id="cache-tokens">0</div></div>
    <div class="stat"><div class="k">Output</div><div class="v" id="output-tokens">0</div></div>
    <div class="stat"><div class="k">Calls</div><div class="v" id="calls">0</div></div>
  </section>
  <div class="layout">
    <section>
      <h2 class="section-title">Token Lanes</h2>
      <div class="panel">
        <div class="lanes" id="lanes"></div>
        <div class="legend">
          <span><span class="swatch" style="background:var(--input)"></span>input</span>
          <span><span class="swatch" style="background:var(--cache)"></span>cache read</span>
          <span><span class="swatch" style="background:var(--create)"></span>cache creation</span>
          <span><span class="swatch" style="background:var(--output)"></span>output is listed, not part of lane width</span>
        </div>
      </div>
      <h2 class="section-title" style="margin-top:18px">Stage And Agent Totals</h2>
      <div class="panel">
        <table>
          <thead><tr><th>Name</th><th>Calls</th><th>Input</th><th>Cached</th><th>Created</th><th>Output</th><th>Hit</th></tr></thead>
          <tbody id="breakdown"></tbody>
        </table>
      </div>
    </section>
    <section>
      <h2 class="section-title">Realtime Events</h2>
      <div class="panel events" id="events"></div>
    </section>
  </div>
</main>
<script>
let state = null;
let events = [];
let eventKeys = new Set();
const nf = new Intl.NumberFormat();
function fmt(n) { return nf.format(Number(n || 0)); }
function pct(n) { return Number(n || 0).toFixed(1) + '%'; }
function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, ch => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[ch]));
}
function usageOf(node, key) { return (node && (node[key] || node.total || node.subtotal)) || {}; }
function laneBar(node, left, width, label) {
  const usage = usageOf(node);
  const total = Math.max(Number(usage.total_context || 0), 1);
  const input = Math.max(0, Number(usage.input || 0) / total * 100);
  const cache = Math.max(0, Number(usage.cache_read || 0) / total * 100);
  const create = Math.max(0, Number(usage.cache_creation || 0) / total * 100);
  return '<div class="bar" style="left:' + left + '%;width:' + width + '%">' +
    '<div class="seg input" style="width:' + input + '%"></div>' +
    '<div class="seg cache" style="width:' + cache + '%"></div>' +
    '<div class="seg create" style="width:' + create + '%"></div>' +
    '<span class="bar-label">' + esc(label) + '</span></div>';
}
function renderLanes() {
  const p = state.pipeline || {stages: [], total: {}};
  const total = Math.max(Number((p.total || {}).total_context || 0), 1);
  let html = '<div class="lane"><div class="lane-label">pipeline</div><div class="track">' + laneBar(p, 0, 100, (p.name || 'pipeline') + ' · ' + fmt(total)) + '</div></div>';
  let left = 0;
  html += '<div class="lane"><div class="lane-label">stage</div><div class="track">';
  (p.stages || []).forEach(stage => {
    const width = Number((stage.subtotal || {}).total_context || 0) / total * 100;
    html += laneBar(stage, left, width, stage.name + ' · ' + fmt((stage.subtotal || {}).total_context));
    left += width;
  });
  html += '</div></div>';
  left = 0;
  html += '<div class="lane"><div class="lane-label">agent</div><div class="track">';
  (p.stages || []).forEach(stage => (stage.agents || []).forEach(agent => {
    const width = Number((agent.total || {}).total_context || 0) / total * 100;
    html += laneBar(agent, left, width, agent.name);
    left += width;
  }));
  html += '</div></div>';
  document.getElementById('lanes').innerHTML = html;
}
function tableRow(name, usage, calls, cls) {
  usage = usage || {};
  return '<tr class="' + cls + '"><td>' + esc(name) + '</td><td>' + fmt(calls) + '</td><td>' + fmt(usage.input) + '</td><td>' + fmt(usage.cache_read) + '</td><td>' + fmt(usage.cache_creation) + '</td><td>' + fmt(usage.output) + '</td><td>' + pct(usage.cache_hit_rate) + '</td></tr>';
}
function renderBreakdown() {
  const p = state.pipeline || {stages: []};
  let html = '';
  (p.stages || []).forEach(stage => {
    html += tableRow(stage.name, stage.subtotal, stage.calls, 'stage-row');
    (stage.agents || []).forEach(agent => {
      const suffix = agent.provider ? ' [' + agent.provider + (agent.model ? ' ' + agent.model : '') + ']' : '';
      html += tableRow(agent.name + suffix, agent.total, agent.calls, '');
    });
  });
  document.getElementById('breakdown').innerHTML = html || '<tr><td colspan="7" class="agent-name">waiting for model usage</td></tr>';
}
function eventLine(event) {
  const data = event.data || {};
  const t = event.timestamp ? new Date(event.timestamp).toLocaleTimeString() : '';
  let detail = '';
  if (data.usage) detail = ' ' + JSON.stringify(data.usage);
  else if (data.error) detail = ' ' + data.error;
  else if (data.name) detail = ' ' + data.name;
  else if (data.url) detail = ' ' + data.url;
  else if (data.agent_id) detail = ' ' + data.agent_id;
  const cls = String(event.type).includes('error') || data.error ? ' error' : '';
  return '<div class="event' + cls + '"><span class="event-time">' + esc(t) + '</span> <strong>' + esc(event.type) + '</strong>' + esc(detail) + '</div>';
}
function renderEvents() {
  const box = document.getElementById('events');
  box.innerHTML = events.slice(-350).reverse().map(eventLine).join('');
}
function render() {
  if (!state) return;
  const p = state.pipeline || {total:{}};
  const total = p.total || {};
  document.getElementById('pipeline-name').textContent = (p.name || 'Paw') + (state.session_id ? ' · ' + state.session_id : '');
  document.getElementById('status-text').textContent = p.status || 'live';
  document.getElementById('total-context').textContent = fmt(total.total_context);
  document.getElementById('input-tokens').textContent = fmt(total.input);
  document.getElementById('cache-tokens').textContent = fmt(total.cache_read);
  document.getElementById('output-tokens').textContent = fmt(total.output);
  document.getElementById('calls').textContent = fmt(p.calls);
  renderLanes();
  renderBreakdown();
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
let pendingRefresh = null;
function scheduleRefresh() {
  if (pendingRefresh) return;
  pendingRefresh = setTimeout(() => {
    pendingRefresh = null;
    loadState().catch(() => {});
  }, 80);
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

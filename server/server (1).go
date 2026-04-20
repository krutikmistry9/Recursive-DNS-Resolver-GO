package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"dns-resolver/cache"
	"dns-resolver/resolver"
)

type Server struct {
	res    *resolver.Resolver
	cache  *cache.Cache
	logger *log.Logger
	addr   string
	mux    *http.ServeMux
}

func New(res *resolver.Resolver, c *cache.Cache, logger *log.Logger, addr string) *Server {
	s := &Server{res: res, cache: c, logger: logger, addr: addr, mux: http.NewServeMux()}
	s.registerRoutes()
	return s
}

func (s *Server) Start() error {
	return http.ListenAndServe(s.addr, s.mux)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/resolve", s.withCORS(s.handleResolve))
	s.mux.HandleFunc("/cache", s.withCORS(s.handleCache))
	s.mux.HandleFunc("/cache/flush", s.withCORS(s.handleCacheFlush))
	s.mux.HandleFunc("/health", s.withCORS(s.handleHealth))
	s.mux.HandleFunc("/", s.handleUI) // browser UI pre-connected to localhost
}

// withCORS wraps a handler to allow cross-origin requests from the client machine.
func (s *Server) withCORS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h(w, r)
	}
}

func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	qtype := strings.TrimSpace(r.URL.Query().Get("type"))
	if domain == "" {
		jsonError(w, "missing 'domain' query parameter", http.StatusBadRequest)
		return
	}
	if qtype == "" {
		qtype = "A"
	}
	s.logger.Printf("[HTTP] /resolve domain=%s type=%s remote=%s", domain, qtype, r.RemoteAddr)
	result := s.res.Resolve(domain, qtype)
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(result)
}

func (s *Server) handleCache(w http.ResponseWriter, r *http.Request) {
	type cacheView struct {
		Key       string         `json:"key"`
		Records   []cache.Record `json:"records"`
		ExpiresAt time.Time      `json:"expires_at"`
		HitCount  int            `json:"hit_count"`
		TTLLeft   string         `json:"ttl_left"`
	}
	var entries []cacheView
	for k, v := range s.cache.Snapshot() {
		entries = append(entries, cacheView{
			Key: k, Records: v.Records, ExpiresAt: v.ExpiresAt,
			HitCount: v.HitCount, TTLLeft: time.Until(v.ExpiresAt).Round(time.Second).String(),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(map[string]interface{}{"stats": s.cache.Stats(), "size": s.cache.Size(), "entries": entries})
}

func (s *Server) handleCacheFlush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.cache.Flush()
	s.logger.Printf("[HTTP] Cache flushed by %s", r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "flushed"})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(map[string]interface{}{
		"status":       "ok",
		"cache_size":   s.cache.Size(),
		"root_servers": s.res.RootServers(),
		"time":         time.Now().UTC(),
	})
}

// handleUI serves the browser UI pre-connected to localhost.
// Open http://localhost:8053 on the server machine to use it directly.
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(serverUI))
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

const serverUI = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>DNS Resolver — Server</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500;600&family=IBM+Plex+Sans:wght@300;400;500&display=swap" rel="stylesheet">
<style>
  :root {
    --bg: #0a0e17;
    --surface: #0f1521;
    --border: #1e2d45;
    --border-bright: #2a4060;
    --text: #c8d8f0;
    --text-dim: #4a6080;
    --text-muted: #2a3850;
    --accent: #00d4ff;
    --accent-dim: #0066aa;
    --green: #00ff88;
    --yellow: #ffd700;
    --orange: #ff8c42;
    --red: #ff4455;
    --purple: #a78bfa;
    --mono: 'IBM Plex Mono', monospace;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { background: var(--bg); color: var(--text); font-family: var(--mono); min-height: 100vh; display: flex; flex-direction: column; }
  body::before { content: ''; position: fixed; inset: 0; background: repeating-linear-gradient(0deg, transparent, transparent 2px, rgba(0,0,0,0.03) 2px, rgba(0,0,0,0.03) 4px); pointer-events: none; z-index: 1000; }

  header { padding: 20px 32px 18px; border-bottom: 1px solid var(--border); display: flex; align-items: center; gap: 12px; }
  .logo { font-size: 11px; font-weight: 600; letter-spacing: 0.2em; color: var(--accent); text-transform: uppercase; }
  .logo span { color: var(--text-muted); font-weight: 400; }
  .server-badge { font-size: 10px; background: rgba(0,255,136,0.08); border: 1px solid rgba(0,255,136,0.2); color: var(--green); padding: 2px 9px; border-radius: 3px; letter-spacing: 0.12em; }
  .status-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--green); box-shadow: 0 0 8px var(--green); animation: pulse 2s ease-in-out infinite; margin-left: auto; }
  @keyframes pulse { 0%,100%{opacity:1} 50%{opacity:0.4} }

  main { display: flex; flex: 1; overflow: hidden; }

  .panel-left { width: 360px; min-width: 300px; border-right: 1px solid var(--border); display: flex; flex-direction: column; padding: 24px; gap: 20px; overflow-y: auto; }
  .section-label { font-size: 10px; letter-spacing: 0.2em; color: var(--text-dim); text-transform: uppercase; margin-bottom: 8px; }

  input[type="text"] { background: var(--surface); border: 1px solid var(--border); color: var(--text); font-family: var(--mono); font-size: 13px; padding: 10px 14px; border-radius: 4px; outline: none; transition: border-color 0.15s; width: 100%; }
  input[type="text"]:focus { border-color: var(--accent-dim); }
  input[type="text"]::placeholder { color: var(--text-muted); }

  .type-grid { display: grid; grid-template-columns: repeat(3,1fr); gap: 6px; }
  .type-btn { background: var(--surface); border: 1px solid var(--border); color: var(--text-dim); font-family: var(--mono); font-size: 12px; padding: 8px; border-radius: 4px; cursor: pointer; transition: all 0.15s; text-align: center; letter-spacing: 0.05em; }
  .type-btn:hover { border-color: var(--border-bright); color: var(--text); }
  .type-btn.active { border-color: var(--accent); color: var(--accent); background: rgba(0,212,255,0.06); }

  .resolve-btn { background: transparent; border: 1px solid var(--accent); color: var(--accent); font-family: var(--mono); font-size: 12px; letter-spacing: 0.15em; padding: 12px; border-radius: 4px; cursor: pointer; text-transform: uppercase; transition: all 0.15s; width: 100%; }
  .resolve-btn:hover:not(:disabled) { background: rgba(0,212,255,0.08); box-shadow: 0 0 16px rgba(0,212,255,0.15); }
  .resolve-btn:disabled { opacity: 0.4; cursor: not-allowed; }

  .divider { height: 1px; background: var(--border); }

  .quick-links { display: flex; flex-direction: column; gap: 4px; }
  .quick-link { font-size: 12px; color: var(--text-dim); cursor: pointer; padding: 6px 10px; border-radius: 3px; transition: all 0.1s; display: flex; justify-content: space-between; align-items: center; }
  .quick-link:hover { background: var(--surface); color: var(--text); }
  .quick-link .badge { font-size: 10px; color: var(--text-muted); }

  .stats-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
  .stat-box { background: var(--surface); border: 1px solid var(--border); border-radius: 4px; padding: 10px 12px; }
  .stat-val { font-size: 20px; font-weight: 600; color: var(--accent); }
  .stat-key { font-size: 10px; color: var(--text-dim); letter-spacing: 0.1em; margin-top: 2px; }

  .flush-btn { background: transparent; border: 1px solid rgba(255,68,85,0.3); color: var(--red); font-family: var(--mono); font-size: 11px; letter-spacing: 0.1em; padding: 9px; border-radius: 4px; cursor: pointer; transition: all 0.15s; width: 100%; display: flex; align-items: center; justify-content: center; gap: 7px; }
  .flush-btn:hover { background: rgba(255,68,85,0.06); border-color: rgba(255,68,85,0.6); }
  .flush-btn:active { transform: scale(0.98); }
  .flush-btn.flushing { opacity: 0.5; cursor: not-allowed; }
  .flush-btn.success { border-color: rgba(0,255,136,0.4); color: var(--green); }

  .panel-right { flex: 1; display: flex; flex-direction: column; overflow: hidden; }

  .tabs { display: flex; border-bottom: 1px solid var(--border); padding: 0 24px; }
  .tab { font-size: 11px; letter-spacing: 0.12em; text-transform: uppercase; color: var(--text-dim); padding: 14px 16px 12px; cursor: pointer; border-bottom: 2px solid transparent; margin-bottom: -1px; transition: all 0.15s; }
  .tab:hover { color: var(--text); }
  .tab.active { color: var(--accent); border-bottom-color: var(--accent); }

  .tab-content { flex: 1; overflow: hidden; display: none; }
  .tab-content.active { display: flex; flex-direction: column; }

  .url-bar { display: flex; align-items: center; gap: 10px; padding: 10px 24px; border-bottom: 1px solid var(--border); background: var(--surface); font-size: 12px; }
  .url-method { color: var(--green); font-weight: 600; font-size: 11px; letter-spacing: 0.1em; }
  .url-text { color: var(--text-dim); flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .url-path { color: var(--accent); }
  .url-param { color: var(--yellow); }
  .url-copy { font-size: 10px; color: var(--text-muted); cursor: pointer; padding: 3px 8px; border: 1px solid var(--border); border-radius: 3px; transition: all 0.1s; background: none; font-family: var(--mono); }
  .url-copy:hover { color: var(--text); border-color: var(--border-bright); }

  .loading-bar { height: 2px; background: linear-gradient(90deg, transparent, var(--accent), transparent); background-size: 200% 100%; animation: shimmer 1s ease-in-out infinite; display: none; }
  @keyframes shimmer { 0%{background-position:-200% 0} 100%{background-position:200% 0} }

  .json-pane, .records-pane, .steps-pane { flex: 1; padding: 24px; overflow-y: auto; font-size: 13px; line-height: 1.7; }

  .empty-state { display: flex; flex-direction: column; align-items: center; justify-content: center; height: 100%; gap: 12px; color: var(--text-muted); }
  .empty-state .icon { font-size: 32px; opacity: 0.4; }
  .empty-state p { font-size: 12px; letter-spacing: 0.1em; }

  .error-banner { margin: 24px; padding: 12px 16px; background: rgba(255,68,85,0.06); border: 1px solid rgba(255,68,85,0.3); border-radius: 4px; color: var(--red); font-size: 13px; display: none; }

  .records-header { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; }
  .records-domain { font-size: 16px; color: var(--text); }
  .records-type { font-size: 11px; color: var(--accent); background: rgba(0,212,255,0.08); border: 1px solid rgba(0,212,255,0.2); padding: 2px 8px; border-radius: 3px; }
  .cached-badge { font-size: 10px; color: var(--purple); background: rgba(167,139,250,0.08); border: 1px solid rgba(167,139,250,0.2); padding: 2px 8px; border-radius: 3px; letter-spacing: 0.1em; margin-left: auto; }
  .records-meta { font-size: 11px; color: var(--text-dim); margin-bottom: 20px; }

  .record-table { width: 100%; border-collapse: collapse; }
  .record-table th { font-size: 10px; letter-spacing: 0.15em; color: var(--text-muted); text-transform: uppercase; text-align: left; padding: 8px 12px; border-bottom: 1px solid var(--border); }
  .record-table td { padding: 10px 12px; border-bottom: 1px solid var(--border); font-size: 13px; vertical-align: top; }
  .record-table tr:last-child td { border-bottom: none; }
  .record-table tr:hover td { background: var(--surface); }
  .rtype { color: var(--yellow); font-weight: 600; font-size: 12px; }
  .rvalue { color: var(--text); word-break: break-all; }
  .rttl { color: var(--text-dim); font-size: 12px; }

  .step-row { display: flex; gap: 0; position: relative; animation: fadeIn 0.2s ease both; }
  @keyframes fadeIn { from{opacity:0;transform:translateY(4px)} to{opacity:1;transform:none} }
  .step-line { width: 40px; display: flex; flex-direction: column; align-items: center; flex-shrink: 0; }
  .step-dot { width: 10px; height: 10px; border-radius: 50%; border: 2px solid var(--border); background: var(--bg); flex-shrink: 0; margin-top: 4px; }
  .step-dot.root          { border-color: var(--accent); box-shadow: 0 0 6px rgba(0,212,255,0.4); }
  .step-dot.tld           { border-color: var(--yellow); box-shadow: 0 0 6px rgba(255,215,0,0.4); }
  .step-dot.authoritative { border-color: var(--green);  box-shadow: 0 0 6px rgba(0,255,136,0.4); }
  .step-dot.cache         { border-color: var(--purple); box-shadow: 0 0 6px rgba(167,139,250,0.4); }
  .step-connector { width: 2px; flex: 1; background: var(--border); min-height: 20px; }
  .step-body { flex: 1; padding: 0 0 20px 16px; }
  .step-header { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
  .step-stage { font-size: 10px; letter-spacing: 0.15em; text-transform: uppercase; font-weight: 600; padding: 2px 7px; border-radius: 3px; }
  .step-stage.root          { color: var(--accent); background: rgba(0,212,255,0.08); }
  .step-stage.tld           { color: var(--yellow); background: rgba(255,215,0,0.08); }
  .step-stage.authoritative { color: var(--green);  background: rgba(0,255,136,0.08); }
  .step-stage.cache         { color: var(--purple); background: rgba(167,139,250,0.08); }
  .step-server { font-size: 12px; color: var(--text-dim); }
  .step-duration { font-size: 11px; color: var(--text-muted); margin-left: auto; }
  .step-response { font-size: 12px; color: var(--text); padding: 8px 12px; background: var(--surface); border: 1px solid var(--border); border-radius: 4px; }
  .step-error { border-color: rgba(255,68,85,0.3); color: var(--red); }

  .j-key  { color: var(--accent); }
  .j-str  { color: var(--green); }
  .j-num  { color: var(--yellow); }
  .j-bool { color: var(--orange); }
  .j-null { color: var(--text-dim); }

  ::-webkit-scrollbar { width: 6px; }
  ::-webkit-scrollbar-track { background: transparent; }
  ::-webkit-scrollbar-thumb { background: var(--border); border-radius: 3px; }
  ::-webkit-scrollbar-thumb:hover { background: var(--border-bright); }
</style>
</head>
<body>

<header>
  <div class="logo">DNS<span>/</span>RESOLVER <span style="color:var(--text-muted);margin-left:4px;font-size:10px">SERVER</span></div>
  <div class="server-badge">LOCAL</div>
  <div class="status-dot"></div>
</header>

<main>
  <div class="panel-left">
    <div>
      <div class="section-label">Domain</div>
      <input type="text" id="domainInput" placeholder="github.com" autocomplete="off" spellcheck="false">
    </div>

    <div>
      <div class="section-label">Record Type</div>
      <div class="type-grid">
        <button class="type-btn active" data-type="A">A</button>
        <button class="type-btn" data-type="AAAA">AAAA</button>
        <button class="type-btn" data-type="MX">MX</button>
        <button class="type-btn" data-type="NS">NS</button>
        <button class="type-btn" data-type="CNAME">CNAME</button>
        <button class="type-btn" data-type="TXT">TXT</button>
      </div>
    </div>

    <button class="resolve-btn" id="resolveBtn" onclick="runResolve()">&#9654; Resolve</button>

    <div class="divider"></div>

    <div>
      <div class="section-label">Quick Queries</div>
      <div class="quick-links">
        <div class="quick-link" onclick="quickQuery('github.com','A')">github.com <span class="badge">A</span></div>
        <div class="quick-link" onclick="quickQuery('amazon.com','NS')">amazon.com <span class="badge">NS</span></div>
        <div class="quick-link" onclick="quickQuery('gmail.com','MX')">gmail.com <span class="badge">MX</span></div>
        <div class="quick-link" onclick="quickQuery('cloudflare.com','AAAA')">cloudflare.com <span class="badge">AAAA</span></div>
        <div class="quick-link" onclick="quickQuery('www.github.com','A')">www.github.com <span class="badge">CNAME</span></div>
        <div class="quick-link" onclick="quickQuery('thisdoesnotexist.invalid','A')">nonexistent.invalid <span class="badge">NXDOMAIN</span></div>
      </div>
    </div>

    <div class="divider"></div>

    <div>
      <div class="section-label">Cache</div>
      <div class="stats-grid">
        <div class="stat-box"><div class="stat-val" id="statHits">—</div><div class="stat-key">HITS</div></div>
        <div class="stat-box"><div class="stat-val" id="statMisses">—</div><div class="stat-key">MISSES</div></div>
        <div class="stat-box"><div class="stat-val" id="statSize">—</div><div class="stat-key">ENTRIES</div></div>
        <div class="stat-box"><div class="stat-val" id="statTotal">—</div><div class="stat-key">TOTAL</div></div>
      </div>
      <div style="margin-top:10px">
        <button class="flush-btn" id="flushBtn" onclick="flushCache()">
          <span id="flushIcon">&#10005;</span>
          <span id="flushLabel">Flush Cache</span>
        </button>
      </div>
    </div>
  </div>

  <div class="panel-right">
    <div class="loading-bar" id="loadingBar"></div>

    <div class="tabs">
      <div class="tab active" onclick="switchTab('records')">Records</div>
      <div class="tab" onclick="switchTab('steps')">Resolution Steps</div>
      <div class="tab" onclick="switchTab('json')">Raw JSON</div>
    </div>

    <div class="url-bar" id="urlBar" style="display:none">
      <span class="url-method">GET</span>
      <span class="url-text" id="urlText"></span>
      <button class="url-copy" onclick="copyURL()">copy</button>
    </div>

    <div class="error-banner" id="errorBanner"></div>

    <div class="tab-content active" id="tab-records">
      <div class="records-pane" id="recordsPane">
        <div class="empty-state"><div class="icon">&#9674;</div><p>Enter a domain and press Resolve</p></div>
      </div>
    </div>

    <div class="tab-content" id="tab-steps">
      <div class="steps-pane" id="stepsPane">
        <div class="empty-state"><div class="icon">&#9674;</div><p>Resolution steps will appear here</p></div>
      </div>
    </div>

    <div class="tab-content" id="tab-json">
      <div class="json-pane" id="jsonPane">
        <div class="empty-state"><div class="icon">&#9674;</div><p>Raw JSON response will appear here</p></div>
      </div>
    </div>
  </div>
</main>

<script>
// Server UI is always talking to localhost — no config needed
const SERVER = window.location.origin;
let selectedType = 'A';
let currentURL = '';

document.querySelectorAll('.type-btn').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('.type-btn').forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
    selectedType = btn.dataset.type;
  });
});

document.getElementById('domainInput').addEventListener('keydown', e => {
  if (e.key === 'Enter') runResolve();
});

function quickQuery(domain, type) {
  document.getElementById('domainInput').value = domain;
  document.querySelectorAll('.type-btn').forEach(b => b.classList.toggle('active', b.dataset.type === type));
  selectedType = type;
  runResolve();
}

function switchTab(name) {
  document.querySelectorAll('.tab').forEach((t, i) => t.classList.toggle('active', ['records','steps','json'][i] === name));
  document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
  document.getElementById('tab-' + name).classList.add('active');
}

function setLoading(on) {
  document.getElementById('loadingBar').style.display = on ? 'block' : 'none';
  document.getElementById('resolveBtn').disabled = on;
  document.getElementById('resolveBtn').textContent = on ? '\u27F3 Resolving\u2026' : '\u25BA Resolve';
}

function showError(msg) {
  const el = document.getElementById('errorBanner');
  el.textContent = '\u2715 ' + msg;
  el.style.display = 'block';
}

function hideError() { document.getElementById('errorBanner').style.display = 'none'; }

async function runResolve() {
  const domain = document.getElementById('domainInput').value.trim();
  if (!domain) return;

  hideError();
  setLoading(true);

  const path = '/resolve?domain=' + encodeURIComponent(domain) + '&type=' + selectedType;
  currentURL = SERVER + path;

  document.getElementById('urlBar').style.display = 'flex';
  document.getElementById('urlText').innerHTML =
    '<span style="color:var(--text-dim)">' + escHtml(SERVER) + '</span>' +
    '<span class="url-path">/resolve</span>' +
    '<span class="url-param">?domain=' + escHtml(domain) + '&type=' + selectedType + '</span>';

  try {
    const res = await fetch(path);
    const data = await res.json();
    renderRecords(data);
    renderSteps(data);
    renderJSON(data);
    refreshCacheStats();
  } catch(e) {
    showError('Request failed: ' + e.message);
  } finally {
    setLoading(false);
  }
}

function renderRecords(data) {
  const pane = document.getElementById('recordsPane');
  if (data.Error) { pane.innerHTML = '<div class="error-banner" style="display:block;margin:0">\u2715 ' + escHtml(data.Error) + '</div>'; return; }
  const records = data.Records || [];
  const latencyMs = (data.Latency / 1e6).toFixed(1);
  let html = '<div class="records-header">';
  html += '<span class="records-domain">' + escHtml(data.Domain) + '</span>';
  html += '<span class="records-type">' + escHtml(data.Type) + '</span>';
  if (data.Cached) html += '<span class="cached-badge">\u26A1 CACHED</span>';
  html += '</div><div class="records-meta">' + records.length + ' record(s) \u00B7 ' + latencyMs + 'ms</div>';
  if (!records.length) {
    html += '<div class="empty-state" style="height:auto;padding:40px 0"><p>No records returned</p></div>';
  } else {
    html += '<table class="record-table"><thead><tr><th>Type</th><th>Value</th><th>TTL</th></tr></thead><tbody>';
    records.forEach(r => {
      html += '<tr><td><span class="rtype">' + escHtml(r.Type) + '</span></td>';
      html += '<td><span class="rvalue">' + escHtml(r.Value) + '</span></td>';
      html += '<td><span class="rttl">' + r.TTL + 's</span></td></tr>';
    });
    html += '</tbody></table>';
  }
  pane.innerHTML = html;
}

function renderSteps(data) {
  const pane = document.getElementById('stepsPane');
  const steps = data.Steps || [];
  if (!steps.length) { pane.innerHTML = '<div class="empty-state"><div class="icon">&#9674;</div><p>No steps available</p></div>'; return; }
  let html = '';
  steps.forEach((step, i) => {
    const isLast = i === steps.length - 1;
    const stage = (step.Stage || 'unknown').toLowerCase();
    const durMs = step.Duration ? (step.Duration / 1e6).toFixed(1) + 'ms' : '';
    html += '<div class="step-row" style="animation-delay:' + (i * 0.04) + 's">';
    html += '<div class="step-line"><div class="step-dot ' + stage + '"></div>';
    if (!isLast) html += '<div class="step-connector"></div>';
    html += '</div><div class="step-body"><div class="step-header">';
    html += '<span class="step-stage ' + stage + '">' + stage + '</span>';
    if (step.Server) html += '<span class="step-server">' + escHtml(step.Server) + '</span>';
    if (durMs) html += '<span class="step-duration">' + durMs + '</span>';
    html += '</div><div class="step-response' + (step.Error ? ' step-error' : '') + '">';
    html += escHtml(step.Response || step.Error || '\u2014') + '</div></div></div>';
  });
  pane.innerHTML = html;
}

function renderJSON(data) {
  document.getElementById('jsonPane').innerHTML = syntaxHighlight(JSON.stringify(data, null, 2));
}

function syntaxHighlight(json) {
  return json
    .replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;')
    .replace(/("(\\u[a-zA-Z0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d*)?(?:[eE][+\-]?\d+)?)/g, m => {
      if (/^"/.test(m) && /:$/.test(m)) return '<span class="j-key">' + m + '</span>';
      if (/^"/.test(m)) return '<span class="j-str">' + m + '</span>';
      if (/true|false/.test(m)) return '<span class="j-bool">' + m + '</span>';
      if (/null/.test(m)) return '<span class="j-null">' + m + '</span>';
      return '<span class="j-num">' + m + '</span>';
    });
}

async function refreshCacheStats() {
  try {
    const res = await fetch('/cache');
    const data = await res.json();
    document.getElementById('statHits').textContent   = data.stats?.Hits   ?? 0;
    document.getElementById('statMisses').textContent = data.stats?.Misses ?? 0;
    document.getElementById('statSize').textContent   = data.size          ?? 0;
    document.getElementById('statTotal').textContent  = data.stats?.Total  ?? 0;
  } catch(_) {}
}

async function flushCache() {
  const btn = document.getElementById('flushBtn');
  const icon = document.getElementById('flushIcon');
  const label = document.getElementById('flushLabel');

  if (btn.classList.contains('flushing')) return;

  btn.classList.add('flushing');
  icon.textContent = '\u27F3';
  label.textContent = 'Flushing\u2026';

  try {
    await fetch('/cache/flush', { method: 'POST' });
    btn.classList.remove('flushing');
    btn.classList.add('success');
    icon.textContent = '\u2713';
    label.textContent = 'Cache Cleared';
    refreshCacheStats();
    setTimeout(() => {
      btn.classList.remove('success');
      icon.textContent = '\u2715';
      label.textContent = 'Flush Cache';
    }, 2000);
  } catch(e) {
    btn.classList.remove('flushing');
    icon.textContent = '\u2715';
    label.textContent = 'Flush Failed';
    setTimeout(() => { label.textContent = 'Flush Cache'; }, 2000);
  }
}

function copyURL() {
  navigator.clipboard.writeText(currentURL).then(() => {
    const btn = document.querySelector('.url-copy');
    btn.textContent = 'copied!';
    setTimeout(() => btn.textContent = 'copy', 1500);
  });
}

function escHtml(s) {
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

refreshCacheStats();
setInterval(refreshCacheStats, 10000);
</script>
</body>
</html>`

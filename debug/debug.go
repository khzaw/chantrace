// Package debug registers chantrace HTTP debug handlers on http.DefaultServeMux.
//
// Usage (blank import, like net/http/pprof):
//
//	import _ "github.com/khzaw/chantrace/debug"
//
// This registers the following endpoints:
//
//	GET /debug/chantrace/          — index page
//	GET /debug/chantrace/events    — recent traced events (JSON)
//	GET /debug/chantrace/channels  — registered channels (JSON)
//	GET /debug/chantrace/notouch   — full no-touch probe snapshot (JSON)
//	GET /debug/chantrace/report    — compact no-touch incident report (JSON)
package debug

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/khzaw/chantrace"
)

func init() {
	http.HandleFunc("GET /debug/chantrace/", handleIndex)
	http.HandleFunc("GET /debug/chantrace/events", handleEvents)
	http.HandleFunc("GET /debug/chantrace/channels", handleChannels)
	http.HandleFunc("GET /debug/chantrace/notouch", handleNoTouch)
	http.HandleFunc("GET /debug/chantrace/report", handleReport)
}

func handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>chantrace</title>
<style>
:root {
  color-scheme: light dark;
  font-family: ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  background: #101418;
  color: #edf2f7;
}
body {
  margin: 0;
  background:
    radial-gradient(circle at top right, rgba(72, 187, 120, 0.18), transparent 28rem),
    radial-gradient(circle at top left, rgba(56, 178, 172, 0.18), transparent 24rem),
    #101418;
}
main {
  max-width: 72rem;
  margin: 0 auto;
  padding: 2rem 1rem 4rem;
}
h1, h2, h3, p {
  margin-top: 0;
}
.header {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  align-items: flex-start;
  margin-bottom: 1.5rem;
}
.muted {
  color: #a0aec0;
}
.status {
  padding: 0.5rem 0.75rem;
  border-radius: 999px;
  font-weight: 700;
  background: rgba(237, 242, 247, 0.08);
}
.status[data-enabled="true"] {
  background: rgba(72, 187, 120, 0.18);
  color: #9ae6b4;
}
.status[data-trigger="true"] {
  background: rgba(245, 101, 101, 0.22);
  color: #feb2b2;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
  gap: 0.9rem;
}
.card {
  background: rgba(15, 23, 42, 0.72);
  border: 1px solid rgba(160, 174, 192, 0.16);
  border-radius: 1rem;
  padding: 1rem;
  box-shadow: 0 18px 50px rgba(0, 0, 0, 0.16);
  backdrop-filter: blur(10px);
}
.metric {
  font-size: 1.75rem;
  font-weight: 800;
  line-height: 1;
}
.table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.95rem;
}
.table th,
.table td {
  text-align: left;
  padding: 0.55rem 0;
  border-bottom: 1px solid rgba(160, 174, 192, 0.12);
  vertical-align: top;
}
.table th {
  color: #a0aec0;
  font-size: 0.82rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, SFMono-Regular, Menlo, monospace;
  word-break: break-word;
}
.links {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  margin-top: 1rem;
}
a {
  color: #81e6d9;
}
.empty {
  color: #a0aec0;
  font-style: italic;
}
</style>
</head>
<body>
<main>
  <div class="header">
    <div>
      <h1>chantrace runtime probe</h1>
      <p class="muted">Polling dashboard for the no-touch incident recorder.</p>
      <div class="links">
        <a href="/debug/chantrace/report">/debug/chantrace/report</a>
        <a href="/debug/chantrace/notouch">/debug/chantrace/notouch</a>
        <a href="/debug/chantrace/events?n=100">/debug/chantrace/events?n=100</a>
        <a href="/debug/chantrace/channels">/debug/chantrace/channels</a>
      </div>
    </div>
    <div id="probe-status" class="status" data-enabled="false" data-trigger="false">disabled</div>
  </div>

  <section class="grid">
    <article class="card"><p class="muted">Mode</p><div id="mode" class="metric">disabled</div></article>
    <article class="card"><p class="muted">Current goroutines</p><div id="goroutines" class="metric">0</div></article>
    <article class="card"><p class="muted">Baseline</p><div id="baseline" class="metric">0</div></article>
    <article class="card"><p class="muted">Delta</p><div id="delta" class="metric">0</div></article>
    <article class="card"><p class="muted">Trigger status</p><div id="trigger" class="metric">idle</div></article>
    <article class="card"><p class="muted">Last trigger</p><div id="last-trigger" class="metric">never</div></article>
  </section>

  <section class="card" style="margin-top: 1rem;">
    <h2>Recent incidents</h2>
    <div id="incidents" class="empty">No incidents recorded.</div>
  </section>
</main>

<template id="incident-template">
  <article class="card" style="margin-top: 0.9rem;">
    <div class="grid">
      <div><p class="muted">Triggered</p><div class="incident-triggered mono"></div></div>
      <div><p class="muted">Resolved</p><div class="incident-resolved mono"></div></div>
      <div><p class="muted">Peak goroutines</p><div class="incident-peak-g metric"></div></div>
      <div><p class="muted">Peak delta</p><div class="incident-peak-d metric"></div></div>
    </div>
    <div class="incident-hotspots" style="margin-top: 1rem;"></div>
  </article>
</template>

<script>
const reportEndpoint = "/debug/chantrace/report";
const statusEl = document.getElementById("probe-status");
const incidentsEl = document.getElementById("incidents");

function fmtTime(ns) {
  if (!ns) return "never";
  return new Date(ns / 1e6).toLocaleString();
}

function hotspotTable(hotspots) {
  if (!hotspots || hotspots.length === 0) {
    return '<p class="empty">No goroutine hotspots captured.</p>';
  }
  const rows = hotspots.map((hotspot) => {
    const persistent = hotspot.persistent ? "yes" : "no";
    return '<tr>' +
      '<td>' + hotspot.state + '</td>' +
      '<td class="mono">' + hotspot.top_frame + '</td>' +
      '<td>' + hotspot.count + '</td>' +
      '<td>' + persistent + '</td>' +
      '</tr>';
  }).join("");
  return '<table class="table">' +
    '<thead><tr><th>State</th><th>Top frame</th><th>Count</th><th>Persistent</th></tr></thead>' +
    '<tbody>' + rows + '</tbody></table>';
}

function renderIncidents(report) {
  if (!report.recent_incidents || report.recent_incidents.length === 0) {
    incidentsEl.innerHTML = '<p class="empty">No incidents recorded.</p>';
    return;
  }
  const template = document.getElementById("incident-template");
  incidentsEl.innerHTML = "";
  for (const incident of report.recent_incidents.slice().reverse()) {
    const node = template.content.firstElementChild.cloneNode(true);
    node.querySelector(".incident-triggered").textContent = fmtTime(incident.triggered_at);
    node.querySelector(".incident-resolved").textContent = fmtTime(incident.resolved_at);
    node.querySelector(".incident-peak-g").textContent = String(incident.peak_goroutines || 0);
    node.querySelector(".incident-peak-d").textContent = String(incident.peak_delta || 0);
    node.querySelector(".incident-hotspots").innerHTML = hotspotTable(incident.hotspots);
    incidentsEl.appendChild(node);
  }
}

async function refreshDashboard() {
  const response = await fetch(reportEndpoint, { cache: "no-store" });
  if (!response.ok) {
    throw new Error("report request failed: " + response.status);
  }
  const report = await response.json();
  document.getElementById("mode").textContent = report.mode || "disabled";
  document.getElementById("goroutines").textContent = String(report.current_goroutines || 0);
  document.getElementById("baseline").textContent = String(report.baseline || 0);
  document.getElementById("delta").textContent = String(report.current_delta || 0);
  document.getElementById("trigger").textContent = report.trigger_active ? "active" : "idle";
  document.getElementById("last-trigger").textContent = fmtTime(report.last_trigger_at);

  statusEl.textContent = report.enabled ? (report.trigger_active ? "triggered" : "enabled") : "disabled";
  statusEl.dataset.enabled = String(Boolean(report.enabled));
  statusEl.dataset.trigger = String(Boolean(report.trigger_active));

  renderIncidents(report);
}

async function startDashboardPolling() {
  try {
    await refreshDashboard();
  } catch (err) {
    statusEl.textContent = "error";
    incidentsEl.innerHTML = '<p class="empty">Unable to load report: ' + err.message + '</p>';
  }
  setInterval(() => {
    refreshDashboard().catch((err) => {
      statusEl.textContent = "error";
      incidentsEl.innerHTML = '<p class="empty">Unable to load report: ' + err.message + '</p>';
    });
  }, 2000);
}

startDashboardPolling();
</script>
</body>
</html>`))
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	n := 100
	if s := r.URL.Query().Get("n"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n = v
		}
	}
	events := chantrace.Snapshot(n)
	data, err := json.Marshal(events)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func handleChannels(w http.ResponseWriter, _ *http.Request) {
	channels := chantrace.Channels()
	data, err := json.Marshal(channels)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func handleNoTouch(w http.ResponseWriter, _ *http.Request) {
	report := chantrace.NoTouchReport()
	data, err := json.Marshal(report)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func handleReport(w http.ResponseWriter, _ *http.Request) {
	report := chantrace.NoTouchDashboardReport()
	data, err := json.Marshal(report)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

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
//	GET /debug/chantrace/notouch   — no-touch probe snapshot (JSON)
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
}

func handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>chantrace</title></head>
<body>
<h1>chantrace debug</h1>
<ul>
  <li><a href="/debug/chantrace/events?n=100">/debug/chantrace/events</a> — recent events (JSON)</li>
  <li><a href="/debug/chantrace/channels">/debug/chantrace/channels</a> — registered channels (JSON)</li>
  <li><a href="/debug/chantrace/notouch">/debug/chantrace/notouch</a> — no-touch probe snapshot (JSON)</li>
</ul>
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

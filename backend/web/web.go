package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/khzaw/chantrace"
)

const maxBufferedEvents = 2048

type backend struct {
	mu     sync.Mutex
	events [maxBufferedEvents]chantrace.Event
	head   int
	count  int
	srv    *http.Server
}

func newBackend(addr string) chantrace.Backend {
	if addr == "" {
		addr = ":4884"
	}

	b := &backend{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", b.handleIndex)
	mux.HandleFunc("/events", b.handleEvents)

	b.srv = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	go func() {
		err := b.srv.ListenAndServe()
		if err == nil || err == http.ErrServerClosed {
			return
		}
	}()
	return b
}

func (b *backend) HandleEvent(e chantrace.Event) {
	b.mu.Lock()
	idx := (b.head + b.count) % maxBufferedEvents
	b.events[idx] = e
	if b.count < maxBufferedEvents {
		b.count++
	} else {
		b.head = (b.head + 1) % maxBufferedEvents
	}
	b.mu.Unlock()
}

func (b *backend) Close() error {
	if b.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return b.srv.Shutdown(ctx)
}

func (b *backend) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>chantrace web</title></head>
<body>
<h1>chantrace web backend</h1>
<p>Recent events are available at <a href="/events">/events</a>.</p>
</body>
</html>`))
}

func (b *backend) handleEvents(w http.ResponseWriter, _ *http.Request) {
	b.mu.Lock()
	events := make([]chantrace.Event, b.count)
	for i := range b.count {
		events[i] = b.events[(b.head+i)%maxBufferedEvents]
	}
	b.mu.Unlock()

	data, err := json.Marshal(events)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func init() {
	chantrace.RegisterBackendFactory("web", func(addr string) chantrace.Backend {
		return newBackend(addr)
	})
}

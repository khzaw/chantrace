package tui

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/khzaw/chantrace"
)

type backend struct {
	mu sync.Mutex
	w  io.Writer
}

func newBackend() chantrace.Backend {
	return &backend{w: os.Stderr}
}

func (b *backend) HandleEvent(e chantrace.Event) {
	ts := time.Unix(0, e.Timestamp).Format("15:04:05.000")
	msg := fmt.Sprintf("%s [%s] ch=%s gid=%d", ts, e.Kind, e.ChannelName, e.GoroutineID)

	b.mu.Lock()
	fmt.Fprintln(b.w, msg)
	b.mu.Unlock()
}

func (b *backend) Close() error {
	return nil
}

func init() {
	chantrace.RegisterBackendFactory("tui", func() chantrace.Backend {
		return newBackend()
	})
}

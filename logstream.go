package chantrace

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

const (
	colorReset   = "\033[0m"
	colorDim     = "\033[2m"
	colorBold    = "\033[1m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
)

type logStream struct {
	mu sync.Mutex
	w  io.Writer
}

func newLogStream() *logStream {
	return &logStream{w: os.Stderr}
}

func (l *logStream) HandleEvent(e Event) {
	ts := time.Unix(0, e.Timestamp).Format("15:04:05.000")
	kind, kindColor := kindDisplay(e.Kind)

	var msg string
	switch e.Kind {
	case ChanMake, ChanRegister:
		msg = fmt.Sprintf("%s%s%s %s[%s]%s %s%s%s (%s) cap=%d %s%s:%d%s",
			colorDim, ts, colorReset,
			kindColor, kind, colorReset,
			colorBold, e.ChannelName, colorReset,
			e.ValueType, e.BufCap,
			colorDim, e.File, e.Line, colorReset,
		)
	case ChanSendStart:
		msg = fmt.Sprintf("%s%s%s %s[%s]%s %s%s%s \u2190 %s%s%s (%s) %s%s:%d%s",
			colorDim, ts, colorReset,
			kindColor, kind, colorReset,
			colorBold, e.ChannelName, colorReset,
			colorCyan, e.ValueStr, colorReset,
			e.ValueType,
			colorDim, e.File, e.Line, colorReset,
		)
	case ChanSendDone:
		msg = fmt.Sprintf("%s%s%s %s[%s]%s %s%s%s %s%s:%d%s",
			colorDim, ts, colorReset,
			kindColor, kind, colorReset,
			colorBold, e.ChannelName, colorReset,
			colorDim, e.File, e.Line, colorReset,
		)
	case ChanRecvStart, ChanRangeStart:
		msg = fmt.Sprintf("%s%s%s %s[%s]%s %s%s%s (%s) %s%s:%d%s",
			colorDim, ts, colorReset,
			kindColor, kind, colorReset,
			colorBold, e.ChannelName, colorReset,
			e.ValueType,
			colorDim, e.File, e.Line, colorReset,
		)
	case ChanRecvDone:
		msg = fmt.Sprintf("%s%s%s %s[%s]%s %s%s%s \u2192 %s%s%s (%s) %s%s:%d%s",
			colorDim, ts, colorReset,
			kindColor, kind, colorReset,
			colorBold, e.ChannelName, colorReset,
			colorCyan, e.ValueStr, colorReset,
			e.ValueType,
			colorDim, e.File, e.Line, colorReset,
		)
	case ChanClose:
		msg = fmt.Sprintf("%s%s%s %s[%s]%s %s%s%s buf=%d/%d %s%s:%d%s",
			colorDim, ts, colorReset,
			kindColor, kind, colorReset,
			colorBold, e.ChannelName, colorReset,
			e.BufLen, e.BufCap,
			colorDim, e.File, e.Line, colorReset,
		)
	case ChanSelectStart:
		msg = fmt.Sprintf("%s%s%s %s[%s]%s %s%s:%d%s",
			colorDim, ts, colorReset,
			kindColor, kind, colorReset,
			colorDim, e.File, e.Line, colorReset,
		)
	case ChanSelectDone:
		ch := e.ChannelName
		if ch == "" {
			ch = "default"
		}
		val := ""
		if e.ValueStr != "" {
			val = fmt.Sprintf(" %s%s%s", colorCyan, e.ValueStr, colorReset)
		}
		msg = fmt.Sprintf("%s%s%s %s[%s]%s case=%d %s%s%s%s %s%s:%d%s",
			colorDim, ts, colorReset,
			kindColor, kind, colorReset,
			e.SelectIndex,
			colorBold, ch, colorReset,
			val,
			colorDim, e.File, e.Line, colorReset,
		)
	case ChanRange:
		msg = fmt.Sprintf("%s%s%s %s[%s]%s %s%s%s \u2192 %s%s%s (%s) %s%s:%d%s",
			colorDim, ts, colorReset,
			kindColor, kind, colorReset,
			colorBold, e.ChannelName, colorReset,
			colorCyan, e.ValueStr, colorReset,
			e.ValueType,
			colorDim, e.File, e.Line, colorReset,
		)
	case ChanRangeDone:
		msg = fmt.Sprintf("%s%s%s %s[%s]%s %s%s%s done %s%s:%d%s",
			colorDim, ts, colorReset,
			kindColor, kind, colorReset,
			colorBold, e.ChannelName, colorReset,
			colorDim, e.File, e.Line, colorReset,
		)
	case GoSpawn:
		msg = fmt.Sprintf("%s%s%s %s[%s]%s %s%s%s %sg=%d parent=%d%s %s%s:%d%s",
			colorDim, ts, colorReset,
			kindColor, kind, colorReset,
			colorCyan, e.GoLabel, colorReset,
			colorDim, e.GoroutineID, e.ParentGID, colorReset,
			colorDim, e.File, e.Line, colorReset,
		)
	case GoExit:
		msg = fmt.Sprintf("%s%s%s %s[%s]%s %s%s%s %sg=%d%s %s%s:%d%s",
			colorDim, ts, colorReset,
			kindColor, kind, colorReset,
			colorCyan, e.GoLabel, colorReset,
			colorDim, e.GoroutineID, colorReset,
			colorDim, e.File, e.Line, colorReset,
		)
	case TraceLost:
		msg = fmt.Sprintf("%s%s%s %s[%s]%s %d events dropped from backend dispatch",
			colorDim, ts, colorReset,
			kindColor, kind, colorReset,
			e.Dropped,
		)
	default:
		msg = fmt.Sprintf("%s%s%s [%s] %+v", colorDim, ts, colorReset, kind, e)
	}

	l.mu.Lock()
	fmt.Fprintln(l.w, msg)
	l.mu.Unlock()
}

func (l *logStream) Close() error {
	return nil
}

func kindDisplay(k EventKind) (string, string) {
	return k.String(), kindColor(k)
}

func kindColor(k EventKind) string {
	switch k {
	case ChanMake, ChanRegister:
		return colorYellow
	case ChanSendStart, ChanSendDone:
		return colorGreen
	case ChanRecvStart, ChanRecvDone, ChanRangeStart, ChanRange, ChanRangeDone:
		return colorBlue
	case ChanClose, TraceLost:
		return colorRed
	case ChanSelectStart, ChanSelectDone:
		return colorMagenta
	case GoSpawn, GoExit:
		return colorCyan
	default:
		return colorReset
	}
}

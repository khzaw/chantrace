package chantrace

import (
	"reflect"
	"time"
)

// SelectCase represents a single case in a traced select statement.
type SelectCase struct {
	dir      reflect.SelectDir
	ch       any
	val      any
	fn       any
	name     string
	elemType string
	withOK   bool
}

// OnRecv creates a receive case for Select.
func OnRecv[T any](ch <-chan T, fn func(T)) SelectCase {
	_, meta := lookupChan(ch)
	sc := SelectCase{
		dir: reflect.SelectRecv,
		ch:  ch,
		fn:  fn,
	}
	if meta != nil {
		sc.name = meta.Name
		sc.elemType = meta.ElemType
	}
	return sc
}

// OnRecvOK creates a receive case for Select whose callback receives both the
// value and ok flag, mirroring "v, ok := <-ch" semantics.
func OnRecvOK[T any](ch <-chan T, fn func(T, bool)) SelectCase {
	_, meta := lookupChan(ch)
	sc := SelectCase{
		dir:    reflect.SelectRecv,
		ch:     ch,
		fn:     fn,
		withOK: true,
	}
	if meta != nil {
		sc.name = meta.Name
		sc.elemType = meta.ElemType
	}
	return sc
}

// OnSend creates a send case for Select.
func OnSend[T any](ch chan<- T, val T, fn func()) SelectCase {
	_, meta := lookupChan(ch)
	sc := SelectCase{
		dir: reflect.SelectSend,
		ch:  ch,
		val: val,
		fn:  fn,
	}
	if meta != nil {
		sc.name = meta.Name
		sc.elemType = meta.ElemType
	}
	return sc
}

// OnDefault creates a default case for Select.
func OnDefault(fn func()) SelectCase {
	return SelectCase{
		dir: reflect.SelectDefault,
		fn:  fn,
	}
}

// Select performs a traced select operation using reflect.Select.
// Emits ChanSelectStart before and ChanSelectDone after the select.
//
// Note: reflect.Select is significantly slower than a native select statement.
// This overhead is acceptable for debugging but should not be used in
// performance-critical hot paths in production.
func Select(cases ...SelectCase) {
	if len(cases) == 0 {
		return
	}

	rCases := buildReflectCases(cases)

	tracing := enabled.Load()

	var pc uintptr
	var opID uint64
	var gid int64
	if tracing {
		pc = maybeCapturePC()
		opID = nextOpID()
		gid = currentRuntimeGID()
		defaultCollector.emit(Event{
			Kind:        ChanSelectStart,
			OpID:        opID,
			Timestamp:   time.Now().UnixNano(),
			GoroutineID: gid,
			PC:          pc,
		})
	}

	chosen, recv, recvOK := reflect.Select(rCases)

	if tracing {
		c := cases[chosen]
		e := Event{
			Kind:        ChanSelectDone,
			OpID:        opID,
			Timestamp:   time.Now().UnixNano(),
			GoroutineID: gid,
			SelectIndex: chosen,
			PC:          pc,
		}
		if c.ch != nil {
			e.ChannelID = chanPtr(c.ch)
			e.ChannelName = c.name
			e.ValueType = c.elemType
		}
		if c.dir == reflect.SelectRecv && recv.IsValid() {
			e.ValueStr = captureValue(recv.Interface())
			e.RecvOK = recvOK
		} else if c.dir == reflect.SelectSend && c.val != nil {
			e.ValueStr = captureValue(c.val)
		} else if c.dir == reflect.SelectRecv {
			e.RecvOK = recvOK
		}
		defaultCollector.emit(e)
	}

	execCallback(cases[chosen], recv, recvOK)
}

func buildReflectCases(cases []SelectCase) []reflect.SelectCase {
	rCases := make([]reflect.SelectCase, len(cases))
	for i, c := range cases {
		switch c.dir {
		case reflect.SelectRecv:
			rCases[i] = reflect.SelectCase{
				Dir:  reflect.SelectRecv,
				Chan: reflect.ValueOf(c.ch),
			}
		case reflect.SelectSend:
			sendVal := reflect.ValueOf(c.val)
			if !sendVal.IsValid() {
				// nil interface value: create zero Value of the channel's element type
				sendVal = reflect.Zero(reflect.TypeOf(c.ch).Elem())
			}
			rCases[i] = reflect.SelectCase{
				Dir:  reflect.SelectSend,
				Chan: reflect.ValueOf(c.ch),
				Send: sendVal,
			}
		case reflect.SelectDefault:
			rCases[i] = reflect.SelectCase{
				Dir: reflect.SelectDefault,
			}
		}
	}
	return rCases
}

func execCallback(c SelectCase, recv reflect.Value, recvOK bool) {
	switch c.dir {
	case reflect.SelectRecv:
		rv := reflect.ValueOf(c.fn)
		if !rv.IsValid() || rv.Kind() != reflect.Func || rv.IsNil() {
			return
		}
		fnType := rv.Type()
		recvVal := recv
		if !recvVal.IsValid() {
			recvVal = reflect.Zero(fnType.In(0))
		}
		if c.withOK {
			rv.Call([]reflect.Value{recvVal, reflect.ValueOf(recvOK)})
			return
		}
		rv.Call([]reflect.Value{recvVal})
	case reflect.SelectSend:
		if fn, ok := c.fn.(func()); ok && fn != nil {
			fn()
		}
	case reflect.SelectDefault:
		if fn, ok := c.fn.(func()); ok && fn != nil {
			fn()
		}
	}
}

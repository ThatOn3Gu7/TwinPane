package proc

import (
	"sync"
	"sync/atomic"
	"time"
)

// Command tracing: an optional observer for every program TwinPane starts.
//
// TwinPane runs a handful of external programs (rclone, 7-Zip, the editor,
// openers, the search tools). They all go through Start, so one hook here sees
// every command with its arguments, its start, how long it ran and how it
// ended — instead of a hook in each caller, which would miss new ones.
//
// The hook is diagnostic only: it is never consulted for control flow, it
// cannot change what is started, and a panic inside it is swallowed so logging
// can never take the program down. When no hook is installed (the default, and
// everything that is not the TUI) the cost is one atomic load per Start.
//
// Arguments are passed through unchanged: redaction is the *sink's* job (see
// internal/activity), so that raw arguments never reach a file by accident.

// TraceEvent describes one started program.
type TraceEvent struct {
	Path    string        // the executable that was started
	Args    []string      // argv including argv[0], unredacted
	Started time.Time     // when it was started
	Elapsed time.Duration // how long it ran (0 while it is still running)
	Status  Status        // exit status, valid once Finished
	Err     error         // why it could not be waited for, if it could not
	Done    bool          // false for the start event, true when it ended
}

// TraceFunc receives one event per call, in the goroutine that started or
// waited for the program.
type TraceFunc func(TraceEvent)

var traceHook atomic.Pointer[TraceFunc]

// SetTrace installs f as the observer for every program started from now on,
// and returns a function that restores the previous observer. Passing nil
// disables tracing. Only the most recently installed observer runs: the log
// belongs to one application instance.
func SetTrace(f TraceFunc) func() {
	prev := TraceFunc(nil)
	if p := traceHook.Load(); p != nil {
		prev = *p
	}
	if f == nil {
		traceHook.Store(nil)
	} else {
		fn := f
		traceHook.Store(&fn)
	}
	return func() { SetTrace(prev) }
}

// traced reports whether anyone is listening.
func traced() bool { return traceHook.Load() != nil }

// emitTrace passes ev to the installed observer, swallowing its panics. The
// arguments are copied so a caller reusing its slice cannot race with a sink
// that keeps the event.
func emitTrace(ev TraceEvent) {
	p := traceHook.Load()
	if p == nil {
		return
	}
	f := *p
	ev.Args = append([]string(nil), ev.Args...)
	defer func() { _ = recover() }()
	f(ev)
}

// startTrace records the start of a program and returns the function that
// records its end (nil when nobody is listening, so Wait pays nothing).
func startTrace(s Spec, argv []string, started time.Time) func(Status, error) {
	if !traced() {
		return nil
	}
	path := s.Path
	// Copy once, here: the caller may reuse its argv buffer, and the end event
	// is emitted much later.
	args := append([]string(nil), argv...)
	if len(args) == 0 {
		args = []string{path}
	}
	emitTrace(TraceEvent{Path: path, Args: args, Started: started})
	var once sync.Once
	return func(st Status, err error) {
		once.Do(func() {
			emitTrace(TraceEvent{
				Path: path, Args: args, Started: started,
				Elapsed: time.Since(started), Status: st, Err: err, Done: true,
			})
		})
	}
}

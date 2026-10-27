package proc

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// collectTraces installs a trace hook for one test and returns the events it
// saw. The hook is removed when the test ends.
func collectTraces(t *testing.T) *[]TraceEvent {
	t.Helper()
	var (
		mu     sync.Mutex
		events []TraceEvent
	)
	restore := SetTrace(func(ev TraceEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
	})
	t.Cleanup(restore)
	return &events
}

func TestTraceStartAndDone(t *testing.T) {
	events := collectTraces(t)
	child, err := Start(Spec{Path: "/bin/sh", Args: []string{"sh", "-c", "exit 3"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := child.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	got := *events
	if len(got) != 2 {
		t.Fatalf("events = %d, want 2: %+v", len(got), got)
	}
	start, end := got[0], got[1]
	if start.Done {
		t.Fatal("first event is not the start event")
	}
	if start.Path != "/bin/sh" {
		t.Fatalf("Path = %q", start.Path)
	}
	if len(start.Args) != 3 || start.Args[2] != "exit 3" {
		t.Fatalf("Args = %q", start.Args)
	}
	if start.Started.IsZero() || start.Started.After(time.Now()) {
		t.Fatalf("Started = %v", start.Started)
	}
	if !end.Done {
		t.Fatal("second event is not the done event")
	}
	if end.Status.Code != 3 {
		t.Fatalf("Status = %+v, want code 3", end.Status)
	}
	if end.Elapsed < 0 {
		t.Fatalf("Elapsed = %v", end.Elapsed)
	}
	if !end.Started.Equal(start.Started) {
		t.Fatalf("the two events disagree about the start time: %v vs %v", start.Started, end.Started)
	}
	if end.Err != nil {
		t.Fatalf("Err = %v", end.Err)
	}
}

func TestTraceEmitsOnceForRepeatedWait(t *testing.T) {
	events := collectTraces(t)
	child, err := Start(Spec{Path: "/bin/sh", Args: []string{"sh", "-c", "true"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := child.Wait(); err != nil {
			t.Fatalf("Wait %d: %v", i, err)
		}
	}
	if n := len(*events); n != 2 {
		t.Fatalf("events = %d, want 2 (one start, one done): %+v", n, *events)
	}
}

func TestTraceEndOnlyOnceEvenIfCalledTwice(t *testing.T) {
	// The end function is guarded by a sync.Once: Wait may be reached through
	// several code paths, and a duplicated end event would distort durations.
	var (
		mu     sync.Mutex
		events []TraceEvent
	)
	restore := SetTrace(func(ev TraceEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
	})
	defer restore()

	end := startTrace(Spec{Path: "/bin/echo"}, []string{"/bin/echo", "hi"}, time.Now())
	if end == nil {
		t.Fatal("startTrace returned nil while a hook was installed")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); end(Status{Code: 0}, nil) }()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	dones := 0
	for _, ev := range events {
		if ev.Done {
			dones++
		}
	}
	if dones != 1 {
		t.Fatalf("done events = %d, want 1", dones)
	}
}

func TestTraceDisabledCostsNothing(t *testing.T) {
	restore := SetTrace(nil)
	defer restore()
	if end := startTrace(Spec{Path: "/bin/true"}, []string{"/bin/true"}, time.Now()); end != nil {
		t.Fatal("startTrace returned a function with no observer installed")
	}
	if traced() {
		t.Fatal("traced() = true with no observer")
	}
}

func TestTracePanicIsSwallowed(t *testing.T) {
	restore := SetTrace(func(TraceEvent) { panic("boom") })
	defer restore()
	// Starting and waiting a real process must succeed even when the observer
	// panics: tracing must never take the program down or change its result.
	child, err := Start(Spec{Path: "/bin/sh", Args: []string{"sh", "-c", "exit 7"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	st, err := child.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if st.Code != 7 {
		t.Fatalf("Status = %+v, want code 7", st)
	}
}

func TestTraceArgsAreCopied(t *testing.T) {
	var got []string
	restore := SetTrace(func(ev TraceEvent) { got = ev.Args })
	defer restore()

	args := []string{"/bin/echo", "original"}
	end := startTrace(Spec{Path: "/bin/echo"}, args, time.Now())
	// A caller that reuses its slice (a prepared argv buffer) must not be able
	// to change the event the sink kept.
	args[1] = "mutated"
	args[0] = "/bin/false"
	end(Status{Code: 0}, nil)

	if len(got) != 2 || got[1] != "original" || got[0] != "/bin/echo" {
		t.Fatalf("Args were not copied: %q", got)
	}
}

func TestTraceWaitErrorIsReported(t *testing.T) {
	// A start event with an end that carries an error must still be emitted;
	// the observer is how a failed reap becomes visible.
	var end TraceEvent
	restore := SetTrace(func(ev TraceEvent) {
		if ev.Done {
			end = ev
		}
	})
	defer restore()

	child, err := Start(Spec{Path: "/bin/sh", Args: []string{"sh", "-c", "exit 0"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := child.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !end.Done || end.Err != nil {
		t.Fatalf("done event = %+v", end)
	}

	// Now call the stored end function with an error, as the failing-reap path
	// in Wait does.
	var withErr TraceEvent
	restore2 := SetTrace(func(ev TraceEvent) { withErr = ev })
	defer restore2()
	boom := errors.New("reap failed")
	endFn := startTrace(Spec{Path: "/bin/x"}, []string{"/bin/x"}, time.Now())
	endFn(Status{}, boom)
	if withErr.Err == nil || withErr.Err.Error() != "reap failed" {
		t.Fatalf("the reap error was lost: %+v", withErr)
	}
}

// TestTraceCoversEveryStart proves the hook sits in Start itself rather than in
// one caller: two very different specs both produce events.
func TestTraceCoversEveryStart(t *testing.T) {
	events := collectTraces(t)
	for _, spec := range []Spec{
		{Path: "/bin/sh", Args: []string{"sh", "-c", "true"}},
		{Path: "/bin/cat", Args: []string{"cat", "/dev/null"}},
	} {
		child, err := Start(spec)
		if err != nil {
			t.Fatalf("Start %q: %v", spec.Path, err)
		}
		if _, err := child.Wait(); err != nil {
			t.Fatalf("Wait %q: %v", spec.Path, err)
		}
	}
	if n := len(*events); n != 4 {
		t.Fatalf("events = %d, want 4", n)
	}
}

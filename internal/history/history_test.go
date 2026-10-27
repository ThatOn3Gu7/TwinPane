package history

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// clock is a deterministic, advancing clock for tests.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock {
	return &clock{t: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

func openStore(t *testing.T, path string, max int) (*Store, error) {
	t.Helper()
	return Open(path, Options{MaxSessions: max, Version: "test"})
}

func mustOpen(t *testing.T, path string, max int) *Store {
	t.Helper()
	s, err := openStore(t, path, max)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestPathInConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TWINPANE_CONFIG_DIR", dir)
	p, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if want := filepath.Join(dir, FileName); p != want {
		t.Fatalf("DefaultPath = %q, want %q", p, want)
	}
}

func TestRecordAndPersistAcrossRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	c := newClock()
	s, err := Open(path, Options{MaxSessions: 20, Now: c.now})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id := s.Start(Record{Op: "Copy", Sources: []string{"/a/f.txt"}, Dest: "/b", Items: []string{"f.txt"}})
	if id == "" {
		t.Fatal("Start returned no id")
	}
	s.Finish(id, Finish{Status: StatusSuccess, Summary: "Copied 1 file", Files: 1, Bytes: 12})

	// A second run sees the first run's session plus its own, newest first.
	s2, err := Open(path, Options{MaxSessions: 20, Now: c.now})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	sessions := s2.Sessions()
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}
	if sessions[0].ID == sessions[1].ID {
		t.Fatalf("session ids collide: %q", sessions[0].ID)
	}
	if !sessions[0].Started.After(sessions[1].Started) {
		t.Fatalf("sessions are not newest first: %v then %v", sessions[0].Started, sessions[1].Started)
	}
	if len(sessions[0].Ops) != 0 {
		t.Fatalf("new run already has %d records", len(sessions[0].Ops))
	}
	older := sessions[1]
	if len(older.Ops) != 1 {
		t.Fatalf("old session ops = %d, want 1", len(older.Ops))
	}
	r := older.Ops[0]
	if r.Op != "Copy" || r.Status != StatusSuccess || r.Dest != "/b" || r.Files != 1 || r.Bytes != 12 {
		t.Fatalf("record not round-tripped: %+v", r)
	}
	if r.Summary != "Copied 1 file" {
		t.Fatalf("summary = %q", r.Summary)
	}
	if d := r.Duration(); d != time.Second {
		t.Fatalf("duration = %v, want 1s", d)
	}
	if r.Finished.Before(r.Started) {
		t.Fatalf("finished before started")
	}
}

func TestNewestOperationFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	c := newClock()
	s, err := Open(path, Options{Now: c.now})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Add(Record{Op: "New folder", Dest: "/a/one"})
	s.Add(Record{Op: "New folder", Dest: "/a/two"})
	s.Add(Record{Op: "Rename", Sources: []string{"/a/x"}, Dest: "/a/y"})
	ops := s.Sessions()[0].Ops
	if len(ops) != 3 {
		t.Fatalf("ops = %d, want 3", len(ops))
	}
	if ops[0].Op != "Rename" || ops[2].Dest != "/a/one" {
		t.Fatalf("order wrong: %q .. %q", ops[0].Op, ops[2].Dest)
	}
	// Also after a reload.
	s2 := mustOpen(t, path, 20)
	ops2 := s2.Sessions()[1].Ops
	if len(ops2) != 3 || ops2[0].Op != "Rename" {
		t.Fatalf("order not persisted: %+v", ops2)
	}
}

func TestRetentionDefault(t *testing.T) {
	if got := ClampSessions(0); got != DefaultSessions {
		t.Fatalf("ClampSessions(0) = %d, want %d", got, DefaultSessions)
	}
	if DefaultSessions != 20 {
		t.Fatalf("DefaultSessions = %d, want 20", DefaultSessions)
	}
	if got := ClampSessions(-5); got != DefaultSessions {
		t.Fatalf("ClampSessions(-5) = %d", got)
	}
	if got := ClampSessions(10_000); got != MaxSessions {
		t.Fatalf("ClampSessions(10000) = %d, want %d", got, MaxSessions)
	}
}

func TestRetentionPrunesOldestSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	c := newClock()
	for i := 0; i < 6; i++ {
		s, err := Open(path, Options{MaxSessions: 3, Now: c.now})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		s.Add(Record{Op: "Copy", Dest: filepath.Join("/d", string(rune('a'+i)))})
	}
	s := mustOpen(t, path, 3)
	sessions := s.Sessions()
	if len(sessions) != 3 {
		t.Fatalf("kept %d sessions, want 3", len(sessions))
	}
	// Only the newest records survive: the pruning opens added sessions of
	// their own, so the kept operations belong to the last two runs.
	var dests []string
	for i := len(sessions) - 1; i >= 0; i-- { // oldest first
		for _, r := range sessions[i].Ops {
			dests = append(dests, filepath.Base(r.Dest))
		}
	}
	if want := "ef"; strings.Join(dests, "") != want {
		t.Fatalf("kept %v, want %s", dests, want)
	}
}

func TestCurrentSessionNeverPruned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	c := newClock()
	// Fill five sessions with a generous limit, then shrink to 1.
	var last *Store
	for i := 0; i < 5; i++ {
		s, err := Open(path, Options{MaxSessions: 10, Now: c.now})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		s.Add(Record{Op: "Copy", Dest: "/d"})
		last = s
	}
	cur := last.CurrentID()
	if cur == "" {
		t.Fatal("no current session")
	}
	if err := last.SetLimit(1); err != nil {
		t.Fatalf("SetLimit: %v", err)
	}
	sessions := last.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("kept %d sessions, want 1", len(sessions))
	}
	if sessions[0].ID != cur {
		t.Fatalf("retention dropped the running session (%q != %q)", sessions[0].ID, cur)
	}
	// The record of the running session is still there and still writable.
	id := last.Start(Record{Op: "Move"})
	last.Finish(id, Finish{Status: StatusFailed, Errors: []string{"permission denied"}})
	if got := last.Sessions()[0].Ops[0]; got.Status != StatusFailed || got.Errors[0] != "permission denied" {
		t.Fatalf("record after prune = %+v", got)
	}
	if last.Limit() != 1 {
		t.Fatalf("limit = %d", last.Limit())
	}
	// The pruned file only holds that session.
	s2 := mustOpen(t, path, 20)
	sessions = s2.Sessions()
	if len(sessions) != 2 { // the surviving session plus this run's
		t.Fatalf("reloaded sessions = %d, want 2", len(sessions))
	}
	if sessions[1].ID != cur || len(sessions[1].Ops) != 2 {
		t.Fatalf("reloaded kept session = %q with %d ops", sessions[1].ID, len(sessions[1].Ops))
	}
}

func TestFailureAndCancelStatuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	c := newClock()
	s, err := Open(path, Options{Now: c.now})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	fail := s.Start(Record{Op: "Delete", Items: []string{"x"}})
	s.Finish(fail, Finish{
		Status:  StatusFailed,
		Summary: "Delete failed",
		Errors:  []string{"x: permission denied"},
		Result:  "rclone exit code 3",
	})
	cancel := s.Start(Record{Op: "Copy", Items: []string{"big.iso"}})
	s.Finish(cancel, Finish{Status: StatusCanceled, Summary: "Canceled after 2 files", Files: 2})
	ops := s.Sessions()[0].Ops
	if ops[0].Status != StatusCanceled || ops[0].Status.Label() != "CANCELED" {
		t.Fatalf("cancel record = %+v", ops[0])
	}
	if ops[1].Status != StatusFailed || ops[1].Result != "rclone exit code 3" || ops[1].Errors[0] != "x: permission denied" {
		t.Fatalf("failure record = %+v", ops[1])
	}
	if ops[1].Status.Label() != "FAILED" {
		t.Fatalf("label = %q", ops[1].Status.Label())
	}
}

func TestInterruptedOnReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	c := newClock()
	s, err := Open(path, Options{Now: c.now})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Started but never finished: TwinPane is killed here.
	s.Start(Record{Op: "Move", Sources: []string{"/a"}, Dest: "/b"})

	s2 := mustOpen(t, path, 20)
	ops := s2.Sessions()[1].Ops
	if len(ops) != 1 {
		t.Fatalf("ops = %d", len(ops))
	}
	r := ops[0]
	if r.Status != StatusInterrupted {
		t.Fatalf("status = %q, want interrupted", r.Status)
	}
	if r.Status == StatusSuccess || !r.Finished.IsZero() {
		t.Fatalf("interrupted record claims an outcome: %+v", r)
	}
	if len(r.Details) == 0 || !strings.Contains(r.Details[len(r.Details)-1], "outcome is unknown") {
		t.Fatalf("details = %v", r.Details)
	}
	if r.Duration() != 0 {
		t.Fatalf("interrupted record has a duration: %v", r.Duration())
	}
	// Rewriting keeps the status: it must not become running again.
	s3 := mustOpen(t, path, 20)
	found := false
	for _, sess := range s3.Sessions() {
		for _, rec := range sess.Ops {
			if rec.Op == "Move" {
				found = true
				if rec.Status != StatusInterrupted {
					t.Fatalf("status after second reload = %q", rec.Status)
				}
			}
		}
	}
	if !found {
		t.Fatal("interrupted record disappeared")
	}
}

func TestCorruptFileIsPreservedAndReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"sessions":[{"id":"x"`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, Options{})
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want CorruptError", err)
	}
	if ce.Backup == "" {
		t.Fatal("no backup path")
	}
	if _, statErr := os.Stat(ce.Backup); statErr != nil {
		t.Fatalf("damaged file not kept: %v", statErr)
	}
	if got, _ := os.ReadFile(ce.Backup); !strings.Contains(string(got), `"sessions"`) {
		t.Fatalf("backup content = %q", got)
	}
	if s == nil || s.ReadOnly() {
		t.Fatal("store unusable after a damaged file")
	}
	// The new file works.
	id := s.Add(Record{Op: "Copy", Status: StatusSuccess})
	if id == "" {
		t.Fatal("cannot record after recovery")
	}
	s2 := mustOpen(t, path, 20)
	if len(s2.Sessions()) != 2 || len(s2.Sessions()[1].Ops) != 1 {
		t.Fatalf("recovered history = %d sessions", len(s2.Sessions()))
	}
	// The damaged file is only moved aside once.
	if _, err := Open(path, Options{}); err != nil {
		t.Fatalf("second open: %v", err)
	}
}

func TestEmptyAndTruncatedFileStartFresh(t *testing.T) {
	for _, content := range []string{"", "   \n", "\x00\x00"} {
		path := filepath.Join(t.TempDir(), "history.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Open(path, Options{})
		if content == "\x00\x00" {
			// NUL bytes are not valid JSON: that is a damaged file.
			var ce *CorruptError
			if !errors.As(err, &ce) {
				t.Fatalf("NUL file: err = %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("content %q: %v", content, err)
		}
		if len(s.Sessions()) != 1 {
			t.Fatalf("content %q: sessions = %d", content, len(s.Sessions()))
		}
	}
}

func TestNewerFormatIsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	body := `{"version":99,"sessions":[{"id":"s1","started":"2026-01-01T00:00:00Z","operations":[]}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, Options{})
	var ro *ReadOnlyError
	if !errors.As(err, &ro) {
		t.Fatalf("err = %v, want ReadOnlyError", err)
	}
	if !s.ReadOnly() || s.CurrentID() != "" {
		t.Fatal("newer file must be read-only with no session")
	}
	if id := s.Add(Record{Op: "Copy"}); id != "" {
		t.Fatal("read-only store recorded an operation")
	}
	if len(s.Sessions()) != 1 {
		t.Fatalf("sessions = %d, want the preserved one", len(s.Sessions()))
	}
	after, _ := os.ReadFile(path)
	if string(after) != body {
		t.Fatalf("read-only store rewrote the file: %q", after)
	}
}

func TestBadRecordsAreKeptFromLaterRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	good := `{"version":1,"sessions":[
	  {"id":"s1","started":"2026-01-01T00:00:00Z","operations":[
	    {"id":"1","op":"Copy","status":"success","started":"2026-01-01T00:00:00Z","finished":"2026-01-01T00:00:01Z"},
	    {"id":"2","op":"Move","status":"invented","started":"2026-01-01T00:00:02Z"}
	  ]},
	  {"id":"","started":"2026-01-01T00:00:00Z","operations":[]}
	]}`
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sessions := s.Sessions()
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 1 loaded + 1 new", len(sessions))
	}
	old := sessions[1]
	if len(old.Ops) != 2 {
		t.Fatalf("ops = %d", len(old.Ops))
	}
	if old.Ops[1].Status != StatusInterrupted {
		t.Fatalf("unknown status became %q", old.Ops[1].Status)
	}
	if old.Ops[0].Status != StatusSuccess {
		t.Fatalf("valid record lost: %+v", old.Ops[0])
	}
}

func TestConcurrentRecordsStayConsistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := mustOpen(t, path, 20)
	var wg sync.WaitGroup
	const n = 40
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := s.Start(Record{Op: "Copy", Dest: filepath.Join("/d", string(rune('a'+i%26)))})
			s.Finish(id, Finish{Status: StatusSuccess, Summary: "copied"})
		}(i)
	}
	wg.Wait()
	ops := s.Sessions()[0].Ops
	if len(ops) != n {
		t.Fatalf("ops = %d, want %d", len(ops), n)
	}
	for _, r := range ops {
		if r.Status != StatusSuccess {
			t.Fatalf("record %q status = %q", r.ID, r.Status)
		}
		if r.Finished.IsZero() {
			t.Fatalf("record %q not finished", r.ID)
		}
	}
	// The file on disk is valid JSON with the same records.
	var saved file
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("file is not valid JSON: %v", err)
	}
	if got := len(saved.Sessions[0].Ops); got < 1 {
		t.Fatalf("saved ops = %d", got)
	}
}

func TestConcurrentStoresShareSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	a := mustOpen(t, path, 20)
	a.Add(Record{Op: "Copy"})
	// A second TwinPane instance keeps the first instance's records.
	b := mustOpen(t, path, 20)
	b.Add(Record{Op: "Delete"})
	if len(b.Sessions()) != 2 {
		t.Fatalf("sessions = %d, want 2", len(b.Sessions()))
	}
	// Writing merges what the other instance saved.
	a.Add(Record{Op: "Move"})
	if len(a.Sessions()) != 2 {
		t.Fatalf("first store sessions = %d, want 2 (merged on save)", len(a.Sessions()))
	}
	saved := mustOpen(t, path, 20)
	if len(saved.Sessions()) < 2 {
		t.Fatalf("sessions after concurrent writes = %d", len(saved.Sessions()))
	}
	ids := map[string]bool{}
	for _, sess := range saved.Sessions() {
		ids[sess.ID] = true
	}
	if len(ids) != len(saved.Sessions()) {
		t.Fatalf("duplicate sessions: %v", ids)
	}
}

func TestFileIsPrivateAndBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "history.json")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	long := strings.Repeat("x", 4*maxText)
	for i := 0; i < 30; i++ {
		id := s.Start(Record{Op: "Copy", Dest: "/d", Details: []string{long}})
		s.Finish(id, Finish{Status: StatusFailed, Errors: []string{long, long}})
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	if info.Size() > MaxFileBytes {
		t.Fatalf("history file is %d bytes, limit %d", info.Size(), MaxFileBytes)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", dirInfo.Mode().Perm())
	}
	// No temporary files are left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file %q", e.Name())
		}
	}
	r := s.Sessions()[0].Ops[0]
	if len(r.Errors) != 2 || len(r.Details) != 0 || len(r.Errors[0]) > maxText {
		t.Fatalf("record not bounded: %d errors (%d bytes), %d details", len(r.Errors), len(r.Errors[0]), len(r.Details))
	}
}

func TestSessionWideCapKeepsRunningRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := mustOpen(t, path, 20)
	running := s.Start(Record{Op: "Move", Dest: "/slow"})
	for i := 0; i < MaxOperationsPerSession+10; i++ {
		s.Add(Record{Op: "Copy", Status: StatusSuccess})
	}
	ops := s.Sessions()[0].Ops
	if len(ops) != MaxOperationsPerSession {
		t.Fatalf("ops = %d, want %d", len(ops), MaxOperationsPerSession)
	}
	found := false
	for _, r := range ops {
		if r.ID == running && r.Op == "Move" {
			found = true
		}
	}
	if !found {
		t.Fatal("the running record was pruned")
	}
}

func TestNilStoreIsSafe(t *testing.T) {
	var s *Store
	if s.CurrentID() != "" || s.Path() != "" || len(s.Sessions()) != 0 || s.Err() != nil || s.ReadOnly() {
		t.Fatal("nil store misbehaves")
	}
	if s.Start(Record{Op: "Copy"}) != "" || s.Add(Record{Op: "Copy"}) != "" {
		t.Fatal("nil store recorded something")
	}
	s.Finish("1", Finish{})
	if err := s.SetLimit(3); err != nil {
		t.Fatalf("SetLimit on nil store: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close on nil store: %v", err)
	}
}

func TestSanitizeKeepsUTF8(t *testing.T) {
	s := strings.Repeat("é", maxText) // 2 bytes each: clipping must not split one
	got := clip(s)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("clip did not abbreviate: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("clip split a character: %q", got)
	}
	for _, r := range got {
		if r == '\uFFFD' {
			t.Fatalf("clip produced an invalid rune")
		}
	}
}

func TestFailedWriteIsReportedNotFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")
	s := mustOpen(t, path, 20)
	// Make the directory unwritable so the atomic replace fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("cannot chmod")
	}
	defer os.Chmod(dir, 0o700)
	id := s.Start(Record{Op: "Copy"})
	s.Finish(id, Finish{Status: StatusSuccess})
	if s.Err() == nil {
		t.Skip("directory still writable (running as root)")
	}
	// Nothing was lost in memory and a later, successful save clears the error.
	os.Chmod(dir, 0o700)
	if err := s.SetLimit(19); err != nil {
		t.Fatalf("save after fixing permissions: %v", err)
	}
	if s.Err() != nil {
		t.Fatalf("error not cleared: %v", s.Err())
	}
}

func TestSessionSummary(t *testing.T) {
	base := time.Date(2026, 10, 27, 15, 4, 0, 0, time.UTC)
	s := Session{Started: base, Ops: []Record{{Status: StatusSuccess}, {Status: StatusFailed}}}
	if got := SessionSummary(s); !strings.Contains(got, "2 operations") || !strings.Contains(got, "1 successful") {
		t.Fatalf("summary = %q", got)
	}
	if got := SessionSummary(Session{Started: base}); !strings.Contains(got, "no operations") {
		t.Fatalf("empty summary = %q", got)
	}
	one := Session{Started: base, Ops: []Record{{Status: StatusSuccess}}}
	if got := SessionSummary(one); !strings.Contains(got, "1 operation") {
		t.Fatalf("single summary = %q", got)
	}
}

func TestUpdateKeepsRunningAndSetsPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	c := newClock()
	s, err := Open(path, Options{Now: c.now})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id := s.Start(Record{Op: "Copy", Sources: []string{"/a/f.txt"}})
	s.Update(id, Update{Op: "Move", Dest: "/b", Items: []string{"f.txt"}})
	ops := s.Sessions()[0].Ops
	if len(ops) != 1 {
		t.Fatalf("ops = %d", len(ops))
	}
	r := ops[0]
	if r.Op != "Move" || r.Dest != "/b" || r.Items[0] != "f.txt" {
		t.Fatalf("update not applied: %+v", r)
	}
	if r.Status != StatusRunning || !r.Finished.IsZero() {
		t.Fatalf("update changed the outcome: %+v", r)
	}
	// Unknown IDs and read-only stores are ignored.
	s.Update("nope", Update{Dest: "/c"})
	if got := s.Sessions()[0].Ops[0].Dest; got != "/b" {
		t.Fatalf("unknown id changed a record: %q", got)
	}
}

func TestUpdateIsPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	c := newClock()
	s, err := Open(path, Options{Now: c.now})
	if err != nil {
		t.Fatal(err)
	}
	id := s.Start(Record{Op: "Copy"})
	s.Update(id, Update{Dest: "/dest"})
	// A reload sees the destination of the operation that never finished.
	s2 := mustOpen(t, path, 20)
	r := s2.Sessions()[1].Ops[0]
	if r.Dest != "/dest" || r.Status != StatusInterrupted {
		t.Fatalf("reloaded record = %+v", r)
	}
}

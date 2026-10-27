package activity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T, opts Options) *Store {
	t.Helper()
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }
	}
	if opts.Run == "" {
		opts.Run = "run-1"
	}
	s, err := Open(filepath.Join(t.TempDir(), FileName), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStoreWritesOneLinePerEntry(t *testing.T) {
	s := newTestStore(t, Options{Mode: ModeActions})
	s.Log(Entry{Level: LevelInfo.String(), Cat: CatNavigation, Ev: "nav.open", Msg: "opened /home/u"})
	s.Log(Entry{Level: LevelError.String(), Cat: CatCommand, Ev: "command.done", Msg: "rclone failed", Code: 1})

	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2:\n%s", len(lines), data)
	}
	var e Entry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("line is not JSON: %v\n%s", err, lines[0])
	}
	if e.V != formatVersion || e.Run != "run-1" || e.Seq != 1 || e.Cat != CatNavigation || e.Ev != "nav.open" {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if e.Time.IsZero() {
		t.Fatal("timestamp not filled in")
	}
}

func TestStoreModeFilters(t *testing.T) {
	cases := []struct {
		mode Mode
		// what survives
		debug, info, warn, err bool
	}{
		{ModeOff, false, false, false, false},
		{ModeWarnings, false, false, true, true},
		{ModeActions, false, true, true, true},
		{ModeVerbose, true, true, true, true},
	}
	for _, c := range cases {
		t.Run(c.mode.String(), func(t *testing.T) {
			s := newTestStore(t, Options{Mode: c.mode})
			s.Log(Entry{Level: LevelDebug.String(), Cat: CatUI, Ev: "key.action", Msg: "d"})
			s.Log(Entry{Level: LevelInfo.String(), Cat: CatUI, Ev: "ui.dialog", Msg: "copy"})
			s.Log(Entry{Level: LevelWarn.String(), Cat: CatUI, Ev: "ui.refused", Msg: "warn"})
			s.Log(Entry{Level: LevelError.String(), Cat: CatUI, Ev: "ui.fail", Msg: "err"})
			got := map[Level]bool{}
			for _, e := range Read(s.Path(), 100).Entries {
				got[e.LevelOf()] = true
			}
			for lvl, want := range map[Level]bool{
				LevelDebug: c.debug, LevelInfo: c.info, LevelWarn: c.warn, LevelError: c.err,
			} {
				if got[lvl] != want {
					t.Errorf("level %v present = %v, want %v", lvl, got[lvl], want)
				}
			}
		})
	}
}

func TestStoreOffWritesNothing(t *testing.T) {
	s := newTestStore(t, Options{Mode: ModeOff})
	s.Log(Entry{Level: LevelError.String(), Cat: CatUI, Ev: "ui.fail", Msg: "x"})
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Fatalf("off mode created a file: %v", err)
	}
	if s.Enabled() {
		t.Fatal("Enabled() = true in off mode")
	}
}

func TestStoreRotatesAndKeeps(t *testing.T) {
	s := newTestStore(t, Options{Mode: ModeActions, MaxBytes: MinMaxBytes, Keep: 2})
	// Write enough to rotate a few times.
	big := strings.Repeat("x", 200)
	for i := 0; i < 2000; i++ {
		s.Log(Entry{Level: LevelInfo.String(), Cat: CatUI, Ev: "ui.action", Msg: big})
	}
	s.Close()
	dir := filepath.Dir(s.Path())
	base := strings.TrimSuffix(FileName, filepath.Ext(FileName))
	for i := 1; i <= 2; i++ {
		if _, err := os.Stat(filepath.Join(dir, base+"."+itoa(i)+".jsonl")); err != nil {
			t.Fatalf("rotated file %d missing: %v", i, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, base+".3.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("kept more files than Keep=2: %v", err)
	}
	st, err := os.Stat(s.Path())
	if err != nil {
		t.Fatalf("current file: %v", err)
	}
	if st.Size() > MinMaxBytes*2 {
		t.Fatalf("current file grew past the limit (%d bytes) — rotation is not working", st.Size())
	}
	// Rotation must not lose entries: every file is whole lines of valid JSON.
	total := 0
	for _, f := range []string{s.Path(), filepath.Join(dir, base+".1.jsonl"), filepath.Join(dir, base+".2.jsonl")} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
			t.Fatalf("%s does not end with a newline — an entry was cut in half", f)
		}
		total += len(Read(f, 100000).Entries)
	}
	if total == 0 {
		t.Fatal("no entries after rotation")
	}
}

func TestStorePrunesRotatedByAge(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	old := filepath.Join(dir, "activity.1.jsonl")
	if err := os.WriteFile(old, []byte(`{"v":1,"ev":"x"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := now.Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, "activity.2.jsonl")
	if err := os.WriteFile(fresh, []byte(`{"v":1,"ev":"y"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, Options{Mode: ModeActions, KeepDays: 30, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("40-day-old rotated file was not pruned: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("recent rotated file was pruned: %v", err)
	}
	// The current file is never pruned by age.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("current file was created by Open: %v", err)
	}
}

func TestStoreSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	content := `{"v":1,"ev":"ok","cat":"ui","lvl":"info"}` + "\n" +
		"not json at all\n" +
		`{"v":1,"ev":"ok2","cat":"nav","lvl":"warn"}` + "\n" +
		"\n" +
		`{"v":1}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	res := Read(path, 100)
	if len(res.Entries) != 2 {
		t.Fatalf("entries = %d, want 2: %+v", len(res.Entries), res.Entries)
	}
	if res.Stat.Skipped != 2 {
		t.Fatalf("skipped = %d, want 2", res.Stat.Skipped)
	}
	// Appending to a file with bad lines keeps working.
	s, err := Open(path, Options{Mode: ModeActions})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Log(Entry{Level: LevelInfo.String(), Cat: CatUI, Ev: "after"})
	if got := len(Read(path, 100).Entries); got != 3 {
		t.Fatalf("entries after append = %d, want 3", got)
	}
}

func TestStoreNewestFirstAcrossFiles(t *testing.T) {
	s := newTestStore(t, Options{Mode: ModeActions, MaxBytes: MinMaxBytes, Keep: 3})
	for i := 0; i < 600; i++ {
		s.Log(Entry{
			Level: LevelInfo.String(), Cat: CatUI, Ev: "ui.action",
			Msg:  strings.Repeat("y", 150),
			Time: time.Date(2026, 10, 27, 9, 0, 0, i*int(time.Second), time.UTC),
		})
	}
	s.Close()
	res := Read(s.Path(), 50)
	if len(res.Entries) != 50 {
		t.Fatalf("entries = %d, want 50", len(res.Entries))
	}
	for i := 1; i < len(res.Entries); i++ {
		if res.Entries[i-1].Time.Before(res.Entries[i].Time) {
			t.Fatalf("entries are not newest-first at %d", i)
		}
	}
}

func TestStorePermissions(t *testing.T) {
	s := newTestStore(t, Options{Mode: ModeActions})
	s.Log(Entry{Level: LevelInfo.String(), Cat: CatUI, Ev: "ui.action"})
	s.Close()
	st, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %o, want 600", got)
	}
}

func TestStoreConcurrentWritesStayWhole(t *testing.T) {
	s := newTestStore(t, Options{Mode: ModeActions})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s.Log(Entry{Level: LevelInfo.String(), Cat: CatCommand, Ev: "command.done",
					Msg: strings.Repeat("z", 100)})
			}
		}(i)
	}
	wg.Wait()
	s.Close()
	res := Read(s.Path(), 1000)
	if len(res.Entries) != 400 {
		t.Fatalf("entries = %d, want 400", len(res.Entries))
	}
	if res.Stat.Skipped != 0 {
		t.Fatalf("skipped = %d, concurrent writes interleaved", res.Stat.Skipped)
	}
}

func TestStoreCapsEntrySize(t *testing.T) {
	s := newTestStore(t, Options{Mode: ModeVerbose})
	data := map[string]string{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		data[k] = strings.Repeat("v", 500)
	}
	s.Log(Entry{
		Level: LevelError.String(), Cat: CatCommand, Ev: "command.done",
		Msg: strings.Repeat("m", 5000), Err: strings.Repeat("e", 5000), Data: data,
	})
	s.Close()
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > MaxEntryBytes*2 {
		t.Fatalf("entry is %d bytes, cap is %d", len(raw), MaxEntryBytes)
	}
	e := Read(s.Path(), 10).Entries
	if len(e) != 1 {
		t.Fatalf("entries = %d", len(e))
	}
	if len(e[0].Msg) > maxMsg+4 {
		t.Fatalf("message not clipped: %d bytes", len(e[0].Msg))
	}
	if len(e[0].Data) > maxData {
		t.Fatalf("data has %d keys, cap is %d", len(e[0].Data), maxData)
	}
}

func TestStoreUnwritablePathReportsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blocked", FileName)
	if err := os.WriteFile(filepath.Join(dir, "blocked"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, Options{Mode: ModeActions})
	if err != nil {
		t.Fatalf("Open must not fail for an unusable path: %v", err)
	}
	defer s.Close()
	s.Log(Entry{Level: LevelInfo.String(), Cat: CatUI, Ev: "ui.action"})
	if s.Err() == nil {
		t.Fatal("expected a write error to be reported")
	}
	// Logging keeps being safe (no panic, no growth) after the failure.
	s.Log(Entry{Level: LevelInfo.String(), Cat: CatUI, Ev: "ui.action"})
	if s.Written() != 0 {
		t.Fatalf("written = %d, want 0", s.Written())
	}
}

func TestStoreEmptyPath(t *testing.T) {
	if _, err := Open("", Options{Mode: ModeActions}); err == nil {
		t.Fatal("Open(\"\") should fail")
	}
	if !Read("", 10).Stat.Missing {
		t.Fatal("Read(\"\") should report a missing log")
	}
}

func TestStatString(t *testing.T) {
	if got := (Stat{Missing: true}).String(); !strings.Contains(got, "no activity log") {
		t.Fatalf("Stat.String() = %q", got)
	}
	got := Stat{Entries: 12, Warns: 2, Errors: 1}.String()
	for _, want := range []string{"12 entries", "2 warnings", "1 errors"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Stat.String() = %q, want %q", got, want)
		}
	}
}

func TestModeCycleAndParse(t *testing.T) {
	if ParseMode("nonsense") != ModeActions {
		t.Fatal("unknown mode must fall back to the default, not to off or verbose")
	}
	if ParseMode("verbose") != ModeVerbose || ParseMode(" OFF ") != ModeOff || ParseMode("warnings") != ModeWarnings {
		t.Fatal("ParseMode round trip failed")
	}
	m := ModeOff
	for _, want := range []Mode{ModeWarnings, ModeActions, ModeVerbose, ModeOff} {
		m = m.Next(1)
		if m != want {
			t.Fatalf("Next() = %v, want %v", m, want)
		}
	}
	if ModeOff.Next(-1) != ModeVerbose {
		t.Fatal("Next(-1) should wrap backwards")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

package activity

import (
	"testing"
	"time"
)

func sampleEntries() []Entry {
	return []Entry{
		{Ev: "nav.open", Cat: CatNavigation, Level: "info", Msg: "opened /home/u/docs"},
		{Ev: "command.start", Cat: CatCommand, Level: "info", Msg: "rclone lsf gdrive:"},
		{Ev: "command.done", Cat: CatCommand, Level: "error", Msg: "rclone failed", Code: 1},
		{Ev: "op.copy.start", Cat: CatOperation, Level: "info", Msg: "copy 2 files"},
		{Ev: "task.cancel", Cat: CatTask, Level: "warn", Msg: "cancelled"},
		{Ev: "cloud.fail", Cat: CatCloud, Level: "error", Msg: "gdrive: quota exceeded"},
		{Ev: "settings.change", Cat: CatSettings, Level: "info", Msg: "activity=verbose"},
		{Ev: "recovery.quarantine", Cat: CatRecovery, Level: "warn", Msg: "history.json was unreadable"},
		{Ev: "ui.menu", Cat: CatUI, Level: "info", Msg: "menu: file"},
		{Ev: "key.action", Cat: CatUI, Level: "debug", Msg: "shortcut f7"},
	}
}

func count(es []Entry, f Filter) int {
	n := 0
	for _, e := range es {
		if f.Match(e) {
			n++
		}
	}
	return n
}

func TestCategoryFilters(t *testing.T) {
	es := sampleEntries()
	cases := map[CatFilter]int{
		FilterAll:        10,
		FilterCommands:   2,
		FilterFileOps:    1,
		FilterNavigation: 1,
		FilterUI:         2,
		FilterTasks:      1,
		FilterCloud:      1,
		FilterWarnings:   4, // command.done, task.cancel, cloud.fail, recovery.quarantine
		FilterSettings:   1,
		FilterRecovery:   1,
	}
	for f, want := range cases {
		if got := count(es, Filter{Cat: f, Level: LevelsAll}); got != want {
			t.Errorf("%s: got %d entries, want %d", f.Label(), got, want)
		}
	}
}

func TestCategoryFilterCycle(t *testing.T) {
	// Ten filters, all reachable and each distinct, wrapping both ways.
	seen := map[CatFilter]bool{}
	c := FilterAll
	for i := 0; i < len(CatFilters); i++ {
		if seen[c] {
			t.Fatalf("cycle repeated %v after %d steps", c, i)
		}
		seen[c] = true
		c = c.Next(1)
	}
	if c != FilterAll {
		t.Fatalf("cycle did not wrap back to All, got %v", c)
	}
	if FilterAll.Next(-1) != FilterRecovery {
		t.Fatal("backwards cycle should wrap to the last filter")
	}
	if FilterCommands.Next(-1) != FilterAll {
		t.Fatal("backwards cycle from the second filter should reach All")
	}
}

func TestLevelFilter(t *testing.T) {
	es := sampleEntries()
	if got := count(es, Filter{Cat: FilterAll, Level: LevelsAll}); got != 10 {
		t.Fatalf("all levels = %d, want 10", got)
	}
	if got := count(es, Filter{Cat: FilterAll, Level: LevelsInfo}); got != 9 {
		t.Fatalf("info and up = %d, want 9 (debug is excluded)", got)
	}
	if got := count(es, Filter{Cat: FilterAll, Level: LevelsWarn}); got != 4 {
		t.Fatalf("warn and up = %d, want 4", got)
	}
	if got := count(es, Filter{Cat: FilterAll, Level: LevelsError}); got != 2 {
		t.Fatalf("errors only = %d, want 2", got)
	}
	if LevelsAll.Next(-1) != LevelsError {
		t.Fatal("level cycle should wrap backwards")
	}
}

func TestQueryParsing(t *testing.T) {
	es := sampleEntries()
	cases := []struct {
		query string
		want  int
	}{
		{"", 10},
		{"cat:cmd", 2},
		{"cat:commands", 2},
		{"cat:cloud", 1},
		{"cat:warn", 4},
		{"cat:all", 10},
		{"lvl:warn", 4},
		{"lvl:error", 2},
		{"lvl:info", 9},
		{"cat:cmd lvl:error", 1},
		{"ev:command", 2},
		{"ev:command.done", 1},
		{"rclone", 2},
		{"quota", 1},
		{"RLONE", 0},        // case-insensitive but not fuzzy
		{"gdrive:", 2},      // matched in the message
		{"cat:nonsense", 0}, // unknown value becomes text, so it narrows
	}
	for _, c := range cases {
		f := Filter{}.ApplyQuery(c.query)
		if got := count(es, f); got != c.want {
			t.Errorf("query %q matched %d entries, want %d", c.query, got, c.want)
		}
	}
}

func TestQueryUnknownCatBecomesText(t *testing.T) {
	// A typo must narrow the list, not silently show everything.
	f := Filter{}.ApplyQuery("cat:nonsense")
	if f.Cat != FilterAll {
		t.Fatalf("unknown category was accepted: %v", f.Cat)
	}
	if f.Text != "cat:nonsense" {
		t.Fatalf("unknown token was dropped instead of searched: %q", f.Text)
	}
	es := sampleEntries()
	if got := count(es, f); got != 0 {
		t.Fatalf("nonsense query matched %d entries", got)
	}
}

func TestQueryOnTopOfCategoryFilter(t *testing.T) {
	// Typed queries refine the current category filter rather than replacing
	// it: the viewer's `c` cycle and `/` combine.
	base := Filter{Cat: FilterCommands, Level: LevelsAll}
	f := base.ApplyQuery("lvl:error")
	if f.Cat != FilterAll {
		t.Fatalf("ApplyQuery kept the old category (%v); it starts from a clean filter", f.Cat)
	}
	// The viewer re-applies its category after parsing, which is exercised
	// here the same way activityview does it.
	f.Cat = base.Cat
	if got := count(sampleEntries(), f); got != 1 {
		t.Fatalf("combined filter matched %d, want 1", got)
	}
}

func TestFilterDescribeAndEmpty(t *testing.T) {
	if !(Filter{}).Empty() {
		t.Fatal("the zero filter should be empty")
	}
	if got := (Filter{}).Describe(); got != "" {
		t.Fatalf("empty filter describes as %q", got)
	}
	got := Filter{Cat: FilterCloud, Level: LevelsWarn, Text: "quota"}.Describe()
	for _, want := range []string{"Cloud", "warnings and up", "quota"} {
		if !contains(got, want) {
			t.Fatalf("Describe() = %q, missing %q", got, want)
		}
	}
	if (Filter{Cat: FilterCloud}).Empty() {
		t.Fatal("a category filter is not empty")
	}
}

func TestFilterMatchIsCaseInsensitive(t *testing.T) {
	es := sampleEntries()
	f := Filter{}.ApplyQuery("RECOVERY")
	if got := count(es, f); got == 0 {
		t.Fatal("text matching should be case-insensitive")
	}
}

func TestFilterMatchesRedactedEntries(t *testing.T) {
	// Stored entries are already redacted; filtering must not resurrect
	// anything and must still find them by event and message.
	e := sanitizeEntry(Entry{
		Ev: "command.done", Cat: CatCommand, Level: "error", Msg: "rclone: --password *** rejected",
		Time: time.Now(),
	})
	if !(Filter{Cat: FilterCommands}).Match(e) {
		t.Fatal("redacted entry did not match its category")
	}
	if !(Filter{}).ApplyQuery("rejected").Match(e) {
		t.Fatal("redacted entry not found by its message")
	}
	if !(Filter{}).ApplyQuery("ev:command").Match(e) {
		t.Fatal("redacted entry not found by its event")
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

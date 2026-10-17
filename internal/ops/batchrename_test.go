package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// batchDir creates files name=content in a temp dir.
func batchDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for n, c := range files {
		if strings.HasSuffix(n, "/") {
			os.Mkdir(filepath.Join(dir, strings.TrimSuffix(n, "/")), 0o755)
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// dirState returns name=content of every file in dir (sorted), so tests can
// check that nothing was lost or overwritten.
func dirState(t *testing.T, dir string) string {
	t.Helper()
	es, _ := os.ReadDir(dir)
	var out []string
	for _, e := range es {
		if e.IsDir() {
			out = append(out, e.Name()+"/")
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		out = append(out, e.Name()+"="+string(b))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func statuses(p RenamePlan) []RenameStatus {
	var out []RenameStatus
	for _, it := range p.Items {
		out = append(out, it.Status)
	}
	return out
}

func TestPlanBatchRenameValidation(t *testing.T) {
	dir := batchDir(t, map[string]string{"a": "A", "b": "B", "c": "C", "keep": "K", "outside": "O"})
	cases := []struct {
		name       string
		olds, news []string
		want       []RenameStatus
		reason     string
	}{
		{"unchanged", []string{"a", "b"}, []string{"a", "b"}, []RenameStatus{RenameUnchanged, RenameUnchanged}, ""},
		{"simple", []string{"a", "b"}, []string{"x", "b"}, []RenameStatus{RenameOK, RenameUnchanged}, ""},
		{"empty", []string{"a", "b"}, []string{"", "y"}, []RenameStatus{RenameInvalid, RenameOK}, "empty"},
		{"slash", []string{"a", "b"}, []string{"x/y", "y"}, []RenameStatus{RenameInvalid, RenameOK}, "/"},
		{"dotdot", []string{"a", "b"}, []string{"..", "y"}, []RenameStatus{RenameInvalid, RenameOK}, ""},
		{"duplicate", []string{"a", "b"}, []string{"x", "x"}, []RenameStatus{RenameDuplicate, RenameDuplicate}, "gets the same name"},
		{"existing outside batch", []string{"a", "b"}, []string{"outside", "y"}, []RenameStatus{RenameExists, RenameOK}, "already exists"},
		{"held by unchanged batch item", []string{"a", "keep"}, []string{"keep", "keep"}, []RenameStatus{RenameExists, RenameUnchanged}, `"keep" keeps this name`},
		{"swap is fine", []string{"a", "b"}, []string{"b", "a"}, []RenameStatus{RenameOK, RenameOK}, ""},
		{"chain is fine", []string{"a", "b"}, []string{"b", "z"}, []RenameStatus{RenameOK, RenameOK}, ""},
		{"missing source", []string{"a", "gone"}, []string{"x", "y"}, []RenameStatus{RenameOK, RenameMissing}, "no longer exists"},
		{"case only", []string{"a", "b"}, []string{"A", "b"}, []RenameStatus{RenameOK, RenameUnchanged}, ""},
	}
	for _, c := range cases {
		p := PlanBatchRename(dir, c.olds, c.news)
		got := statuses(p)
		if strings.Join(statusNames(got), ",") != strings.Join(statusNames(c.want), ",") {
			t.Errorf("%s: statuses %v, want %v (%+v)", c.name, got, c.want, p.Items)
			continue
		}
		if c.reason != "" {
			found := false
			for _, it := range p.Items {
				if strings.Contains(it.Reason, c.reason) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: no reason containing %q: %+v", c.name, c.reason, p.Items)
			}
		}
		wantReady := true
		changes := 0
		for _, s := range c.want {
			if s.IsError() {
				wantReady = false
			}
			if s == RenameOK {
				changes++
			}
		}
		if changes == 0 {
			wantReady = false
		}
		if p.Ready() != wantReady {
			t.Errorf("%s: Ready()=%v", c.name, p.Ready())
		}
	}
	if PlanBatchRename(dir, []string{"a"}, []string{"A"}).CaseInsensitive {
		t.Log("note: temp dir is on a case-insensitive filesystem")
	}
}

func statusNames(ss []RenameStatus) []string {
	var out []string
	for _, s := range ss {
		out = append(out, []string{"unchanged", "ok", "invalid", "duplicate", "exists", "missing"}[s])
	}
	return out
}

func applyOK(t *testing.T, dir string, olds, news []string) BatchResult {
	t.Helper()
	p := PlanBatchRename(dir, olds, news)
	if !p.Ready() {
		t.Fatalf("plan not ready: %+v", p.Items)
	}
	res, err := ApplyBatchRename(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func noBatchTemps(t *testing.T, dir string) {
	t.Helper()
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if IsBatchTemp(e.Name()) {
			t.Errorf("temporary name left behind: %s", e.Name())
		}
	}
}

func TestApplyBatchRenameOrderSwapsAndCycles(t *testing.T) {
	// chain: a→b must wait until b→c has run
	dir := batchDir(t, map[string]string{"a": "A", "b": "B"})
	res := applyOK(t, dir, []string{"a", "b"}, []string{"b", "c"})
	if res.Renamed != 2 || dirState(t, dir) != "b=A c=B" {
		t.Fatalf("chain: %+v %s", res, dirState(t, dir))
	}
	// swap (2-cycle)
	dir = batchDir(t, map[string]string{"a": "A", "b": "B"})
	res = applyOK(t, dir, []string{"a", "b"}, []string{"b", "a"})
	if res.Renamed != 2 || dirState(t, dir) != "a=B b=A" {
		t.Fatalf("swap: %+v %s", res, dirState(t, dir))
	}
	noBatchTemps(t, dir)
	// 3-cycle plus an independent rename and an unchanged item
	dir = batchDir(t, map[string]string{"1": "one", "2": "two", "3": "three", "x": "X", "k": "K"})
	res = applyOK(t, dir, []string{"1", "2", "3", "x", "k"}, []string{"2", "3", "1", "y", "k"})
	if res.Renamed != 4 || dirState(t, dir) != "1=three 2=one 3=two k=K y=X" {
		t.Fatalf("cycle: %+v %s", res, dirState(t, dir))
	}
	noBatchTemps(t, dir)
	// case-only rename (a different name on this case-sensitive fs)
	dir = batchDir(t, map[string]string{"photo.JPG": "p", "Readme": "r"})
	res = applyOK(t, dir, []string{"photo.JPG", "Readme"}, []string{"photo.jpg", "README"})
	if res.Renamed != 2 || dirState(t, dir) != "README=r photo.jpg=p" {
		t.Fatalf("case: %+v %s", res, dirState(t, dir))
	}
	// folders are renamed too
	dir = batchDir(t, map[string]string{"d1/": "", "d2/": ""})
	res = applyOK(t, dir, []string{"d1", "d2"}, []string{"d2", "d1"})
	if res.Renamed != 2 || dirState(t, dir) != "d1/ d2/" {
		t.Fatalf("dirs: %+v %s", res, dirState(t, dir))
	}
}

func TestApplyBatchRenameCaseInsensitivePath(t *testing.T) {
	// Simulate a case-insensitive filesystem: the plan says so, so case-only
	// changes must go through a temporary name. That path works on any fs.
	dir := batchDir(t, map[string]string{"a.txt": "a", "B.txt": "b"})
	p := PlanBatchRename(dir, []string{"a.txt", "B.txt"}, []string{"A.TXT", "b.txt"})
	p.CaseInsensitive = true
	var steps []string
	old := batchRenameFn
	batchRenameFn = func(src, dst string) error {
		steps = append(steps, filepath.Base(src)+">"+filepath.Base(dst))
		return old(src, dst)
	}
	defer func() { batchRenameFn = old }()
	res, err := ApplyBatchRename(context.Background(), p)
	if err != nil || res.Renamed != 2 || dirState(t, dir) != "A.TXT=a b.txt=b" {
		t.Fatalf("%v %+v %s", err, res, dirState(t, dir))
	}
	if len(steps) != 4 || !IsBatchTemp(strings.Split(steps[0], ">")[1]) {
		t.Fatalf("case-only renames must use a temporary name: %v", steps)
	}
	noBatchTemps(t, dir)
}

func TestApplyBatchRenameNeverOverwrites(t *testing.T) {
	dir := batchDir(t, map[string]string{"a": "A", "b": "B"})
	p := PlanBatchRename(dir, []string{"a", "b"}, []string{"x", "y"})
	if !p.Ready() {
		t.Fatal(p.Items)
	}
	// Another program creates "x" after the plan was made.
	os.WriteFile(filepath.Join(dir, "x"), []byte("precious"), 0o644)
	res, err := ApplyBatchRename(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Renamed != 1 || res.Items[0].Outcome != OutcomeFailed || !errors.Is(res.Items[0].Err, os.ErrExist) {
		t.Fatalf("%+v", res)
	}
	if dirState(t, dir) != "a=A x=precious y=B" {
		t.Fatalf("existing file touched: %s", dirState(t, dir))
	}
	// A plan with errors is refused outright.
	bad := PlanBatchRename(dir, []string{"a", "y"}, []string{"z", "z"})
	if _, err := ApplyBatchRename(context.Background(), bad); err == nil {
		t.Fatal("plan with errors must be refused")
	}
	if dirState(t, dir) != "a=A x=precious y=B" {
		t.Fatal("refused plan changed something")
	}
}

func TestApplyBatchRenamePartialFailureBlocksDependents(t *testing.T) {
	// a→b, b→c, d→e: b→c fails, so a→b is blocked; d→e is independent.
	dir := batchDir(t, map[string]string{"a": "A", "b": "B", "d": "D"})
	p := PlanBatchRename(dir, []string{"a", "b", "d"}, []string{"b", "c", "e"})
	old := batchRenameFn
	batchRenameFn = func(src, dst string) error {
		if filepath.Base(dst) == "c" {
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: os.ErrPermission}
		}
		return old(src, dst)
	}
	defer func() { batchRenameFn = old }()
	res, err := ApplyBatchRename(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]BatchOutcome{}
	for _, r := range res.Items {
		got[r.Old] = r.Outcome
	}
	if res.Renamed != 1 || got["a"] != OutcomeBlocked || got["b"] != OutcomeFailed || got["d"] != OutcomeRenamed {
		t.Fatalf("%+v", res)
	}
	if dirState(t, dir) != "a=A b=B e=D" {
		t.Fatalf("state %s", dirState(t, dir))
	}
}

func TestApplyBatchRenameRestoresTemporaryNames(t *testing.T) {
	// Swap a↔b where the second step fails: the parked item must get its
	// original name back.
	dir := batchDir(t, map[string]string{"a": "A", "b": "B"})
	p := PlanBatchRename(dir, []string{"a", "b"}, []string{"b", "a"})
	old := batchRenameFn
	calls := 0
	batchRenameFn = func(src, dst string) error {
		calls++
		if calls == 2 { // step 1 parks an item, step 2 fails
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: os.ErrPermission}
		}
		return old(src, dst)
	}
	defer func() { batchRenameFn = old }()
	res, err := ApplyBatchRename(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Renamed != 0 || dirState(t, dir) != "a=A b=B" {
		t.Fatalf("%+v %s", res, dirState(t, dir))
	}
	for _, r := range res.Items {
		if r.Stranded != "" {
			t.Fatalf("nothing should be stranded: %+v", r)
		}
	}
	noBatchTemps(t, dir)
}

func TestApplyBatchRenameReportsStrandedTemp(t *testing.T) {
	// 3-cycle 1→2→3→1. Make the parked item's final step fail after its
	// original name has been taken: it must be reported as stranded.
	dir := batchDir(t, map[string]string{"1": "one", "2": "two", "3": "three"})
	p := PlanBatchRename(dir, []string{"1", "2", "3"}, []string{"2", "3", "1"})
	old := batchRenameFn
	batchRenameFn = func(src, dst string) error {
		if IsBatchTemp(filepath.Base(src)) {
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: os.ErrPermission}
		}
		return old(src, dst)
	}
	defer func() { batchRenameFn = old }()
	res, err := ApplyBatchRename(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	stranded := 0
	for _, r := range res.Items {
		if r.Stranded != "" {
			stranded++
			if !IsBatchTemp(r.Stranded) || !pathExists(filepath.Join(dir, r.Stranded)) {
				t.Fatalf("bad stranded report %+v", r)
			}
		}
	}
	if stranded != 1 || res.Renamed != 2 {
		t.Fatalf("%+v %s", res, dirState(t, dir))
	}
	// No data lost: still three files with the three contents.
	if s := dirState(t, dir); !strings.Contains(s, "=one") || !strings.Contains(s, "=two") || !strings.Contains(s, "=three") {
		t.Fatalf("data lost: %s", s)
	}
}

func TestApplyBatchRenameCanceled(t *testing.T) {
	dir := batchDir(t, map[string]string{"a": "A", "b": "B"})
	p := PlanBatchRename(dir, []string{"a", "b"}, []string{"b", "a"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := ApplyBatchRename(ctx, p)
	if err != nil || res.Renamed != 0 || res.Items[0].Outcome != OutcomeCanceled {
		t.Fatalf("%v %+v", err, res)
	}
	if dirState(t, dir) != "a=A b=B" {
		t.Fatal("cancel changed something")
	}
}

func TestNameTransforms(t *testing.T) {
	for _, c := range []struct {
		name      string
		dir       bool
		stem, ext string
	}{
		{"a.txt", false, "a", ".txt"}, {"archive.tar.gz", false, "archive.tar", ".gz"},
		{".bashrc", false, ".bashrc", ""}, {".config.json", false, ".config", ".json"},
		{"noext", false, "noext", ""}, {"trailing.", false, "trailing.", ""},
		{"folder.d", true, "folder.d", ""},
	} {
		if s, e := SplitExt(c.name, c.dir); s != c.stem || e != c.ext {
			t.Errorf("SplitExt(%q) = %q %q", c.name, s, e)
		}
	}

	names := []string{"IMG_001.JPG", "img_002.jpg", "notes.txt"}
	got, err := ReplaceNames(names, "img_", "photo-", false, false)
	if err != nil || strings.Join(got, ",") != "photo-001.JPG,photo-002.jpg,notes.txt" {
		t.Fatalf("literal/ignore case: %v %v", got, err)
	}
	got, _ = ReplaceNames(names, "img_", "photo-", false, true)
	if strings.Join(got, ",") != "IMG_001.JPG,photo-002.jpg,notes.txt" {
		t.Fatalf("match case: %v", got)
	}
	got, _ = ReplaceNames(names, "a.b", "$1", false, false) // literal: no regex, no group expansion
	if strings.Join(got, ",") != strings.Join(names, ",") {
		t.Fatalf("literal dot: %v", got)
	}
	got, _ = ReplaceNames([]string{"a.txt"}, "a", "$1x", false, false)
	if got[0] != "$1x.txt" {
		t.Fatalf("literal $ in replacement: %v", got)
	}
	got, err = ReplaceNames(names, `^img_(\d+)\.jpg$`, "${1}-pic.jpg", true, false)
	if err != nil || strings.Join(got, ",") != "001-pic.jpg,002-pic.jpg,notes.txt" {
		t.Fatalf("regex: %v %v", got, err)
	}
	if _, err := ReplaceNames(names, "(", "", true, false); err == nil {
		t.Fatal("bad regex accepted")
	}
	if _, err := ReplaceNames(names, "", "x", false, false); err == nil {
		t.Fatal("empty find accepted")
	}

	items := []TemplateItem{{"a.txt", false}, {"b.tar.gz", false}, {"dir.v2", true}}
	got, err = ApplyTemplate(items, "{n:3}-{name}{ext}")
	if err != nil || strings.Join(got, ",") != "001-a.txt,002-b.tar.gz,003-dir.v2" {
		t.Fatalf("template: %v %v", got, err)
	}
	got, _ = ApplyTemplate(items, "x{n}{{y}}")
	if strings.Join(got, ",") != "x1{y},x2{y},x3{y}" {
		t.Fatalf("braces: %v", got)
	}
	for _, bad := range []string{"{nope}", "{n:0}", "{n:x}", "{name", "a}b"} {
		if _, err := ApplyTemplate(items, bad); err == nil {
			t.Errorf("template %q accepted", bad)
		}
	}
	if strings.Join(LowerNames([]string{"A.TXT", "Bé"}), ",") != "a.txt,bé" || UpperNames([]string{"a"})[0] != "A" {
		t.Fatal("case")
	}

	ns, err := ParseNameList("x\r\ny\nz\n", 3)
	if err != nil || strings.Join(ns, ",") != "x,y,z" {
		t.Fatalf("parse: %v %v", ns, err)
	}
	if _, err := ParseNameList("x\ny\n", 3); err == nil || !strings.Contains(err.Error(), "exactly 3 lines") {
		t.Fatalf("line count: %v", err)
	}
	if ns, err := ParseNameList("x\n\nz", 3); err != nil || ns[1] != "" {
		t.Fatal("an emptied line keeps its row (and is then rejected by the planner)")
	}
}

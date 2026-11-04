package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCheckDeletableSafeguards(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home", "me")
	os.MkdirAll(home, 0o755)
	setSeam(t, &homeDirFn, func() string { return home })
	write(t, filepath.Join(home, "ok.txt"), "x")
	os.Symlink(home, filepath.Join(root, "homelink"))

	bad := []string{"", "relative/path", "/", filepath.Join(root, "missing"), home, filepath.Join(root, "home"), root}
	for _, p := range bad {
		if err := CheckDeletable(p); err == nil {
			t.Errorf("CheckDeletable(%q) should fail", p)
		}
	}
	if err := CheckDeletable(home + "/.."); err == nil {
		t.Error("path ending in .. must be refused")
	}
	for _, p := range []string{filepath.Join(home, "ok.txt"), filepath.Join(root, "homelink")} {
		if err := CheckDeletable(p); err != nil {
			t.Errorf("CheckDeletable(%q) = %v (a symlink to home is just a link)", p, err)
		}
	}
}

func TestPlanDeleteCounts(t *testing.T) {
	root := t.TempDir()
	src := tree(t, root)
	write(t, filepath.Join(root, "lone.txt"), "12345")
	os.Mkdir(filepath.Join(root, "empty"), 0o755)

	plan, err := PlanDelete(context.Background(), []string{src, filepath.Join(root, "lone.txt"), filepath.Join(root, "empty")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TopDirs != 2 || plan.TopFiles != 1 || plan.Files != 4 || plan.Dirs != 4 || !plan.Recursive || plan.UseTrash {
		t.Fatalf("%+v", plan)
	}
	if plan.Bytes != int64(len("alphabravocharlie12345")) {
		t.Fatalf("bytes %d", plan.Bytes)
	}
	plan, _ = PlanDelete(context.Background(), []string{filepath.Join(root, "empty")}, nil)
	if plan.Recursive {
		t.Fatal("empty folder is not recursive")
	}
	if _, err := PlanDelete(context.Background(), nil, nil); err == nil {
		t.Fatal("empty plan must fail")
	}
	if _, err := PlanDelete(context.Background(), []string{"/"}, nil); err == nil {
		t.Fatal("root must be refused")
	}
}

// An item that vanishes between CheckDeletable and the measuring look must
// not crash the planner: the plan is reported as incomplete and the item is
// not counted.
func TestPlanDeleteVanishingItem(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "gone.txt")
	write(t, p, "x")
	setSeam(t, &planLstatFn, func(name string) (os.FileInfo, error) {
		os.Remove(name) // the item vanishes after CheckDeletable validates it
		return nil, os.ErrNotExist
	})
	plan, err := PlanDelete(context.Background(), []string{p}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Incomplete {
		t.Fatalf("a vanishing item must mark the plan incomplete: %+v", plan)
	}
	if plan.TopFiles != 0 || plan.Files != 0 || plan.TopDirs != 0 {
		t.Fatalf("a vanished item cannot be counted: %+v", plan)
	}
	if len(plan.Paths) != 1 {
		t.Fatalf("the path stays in the plan so the deletion can report it: %+v", plan)
	}
}

func TestPermanentDeleteDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	write(t, filepath.Join(outside, "keep.txt"), "keep me")
	victim := tree(t, root)
	os.Symlink(outside, filepath.Join(victim, "link-to-outside"))
	os.Symlink(".", filepath.Join(victim, "loop"))

	plan, err := PlanDelete(context.Background(), []string{victim}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := Delete(context.Background(), plan, nil, Hooks{})
	if !res.OK() || res.Removed != 1 || exists(victim) {
		t.Fatalf("%+v", res)
	}
	if read(t, filepath.Join(outside, "keep.txt")) != "keep me" {
		t.Fatal("deletion followed a symlink out of the tree")
	}
}

func TestPartialDeleteReportsFailures(t *testing.T) {
	needUnixNonRoot(t)
	root := t.TempDir()
	victim := tree(t, root)
	other := filepath.Join(root, "other.txt")
	write(t, other, "x")
	chmodRestore(t, filepath.Join(victim, "sub", "deeper"), 0o555) // c.txt cannot be removed

	plan, _ := PlanDelete(context.Background(), []string{victim, other}, nil)
	res := Delete(context.Background(), plan, nil, Hooks{})
	if res.OK() || res.Removed != 1 || len(res.Failed) != 1 {
		t.Fatalf("%+v", res)
	}
	if !errors.Is(res.Failed[0], os.ErrPermission) || !strings.HasSuffix(res.Failed[0].Path, "c.txt") {
		t.Fatalf("failure: %v", res.Failed[0])
	}
	if exists(other) || exists(filepath.Join(victim, "a.txt")) {
		t.Fatal("removable items should be gone")
	}
	if !exists(filepath.Join(victim, "sub", "deeper", "c.txt")) {
		t.Fatal("protected file should remain")
	}
}

func TestDeleteRechecksSafetyAtExecution(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	write(t, filepath.Join(home, "f"), "x")
	plan := DeletePlan{Paths: []string{home, "/", "relative"}} // forged plan
	setSeam(t, &homeDirFn, func() string { return home })
	res := Delete(context.Background(), plan, nil, Hooks{})
	if len(res.Failed) != 3 || res.Removed != 0 || !exists(filepath.Join(home, "f")) {
		t.Fatalf("%+v", res)
	}
}

func TestDeleteCancel(t *testing.T) {
	root := t.TempDir()
	var paths []string
	for i := 0; i < 3; i++ {
		p := filepath.Join(root, string(rune('a'+i)))
		write(t, p, "x")
		paths = append(paths, p)
	}
	plan, _ := PlanDelete(context.Background(), paths, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := Delete(ctx, plan, nil, Hooks{})
	if !res.Canceled || res.OK() || res.Removed != 0 || !exists(paths[0]) {
		t.Fatalf("%+v", res)
	}
}

func testTrash(t *testing.T, dir string, now time.Time) *TrashStore {
	t.Helper()
	st, err := OpenTrash(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Now = func() time.Time { return now }
	return st
}

func TestTrashStore(t *testing.T) {
	root := t.TempDir()
	fixed := time.Date(2026, 10, 15, 9, 30, 0, 0, time.Local)
	tr := testTrash(t, filepath.Join(root, "trash"), fixed)
	f1 := filepath.Join(root, "my file.txt")
	write(t, f1, "one")
	dir := tree(t, root)

	plan, err := PlanDelete(context.Background(), []string{f1, dir}, tr)
	if err != nil || !plan.UseTrash {
		t.Fatalf("plan %+v %v", plan, err)
	}
	res := Delete(context.Background(), plan, tr, Hooks{})
	if !res.OK() || res.Removed != 2 || !res.UseTrash || exists(f1) || exists(dir) {
		t.Fatalf("%+v", res)
	}
	entries, err := tr.Entries()
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries %+v %v", entries, err)
	}
	byName := map[string]TrashEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	file, ok := byName["my file.txt"]
	if !ok || file.Origin != f1 || !file.DeletedAt.Equal(fixed) || file.Bytes != 3 || file.Dir {
		t.Fatalf("file entry %+v", file)
	}
	folder, ok := byName["proj"]
	if !ok || !folder.Dir || folder.Bytes != 5+5+7 { // alpha, bravo, charlie
		t.Fatalf("folder entry %+v", folder)
	}
	if read(t, filepath.Join(tr.dataDir(), folder.ID, "a.txt")) != "alpha" {
		t.Fatal("folder contents not in the trash")
	}

	// the same name again: a second entry, never a collision
	write(t, f1, "two")
	second, err := tr.Put(f1)
	if err != nil || second.ID == file.ID || second.Name != "my file.txt" {
		t.Fatalf("second %+v %v", second, err)
	}
	if n, _ := tr.Total(); n != 3 {
		t.Fatalf("three items expected, got %d", n)
	}

	// restore puts it back and takes it out of the index
	if err := tr.Restore(file, f1); err != nil {
		t.Fatal(err)
	}
	if read(t, f1) != "one" {
		t.Fatalf("restored content %q", read(t, f1))
	}
	if n, _ := tr.Total(); n != 2 {
		t.Fatalf("restore must remove the entry: %d left", n)
	}
	// restoring onto an existing path is refused, not overwritten
	if err := tr.Restore(second, f1); err == nil {
		t.Fatal("restore onto an existing path must fail")
	}
	if read(t, f1) != "one" {
		t.Fatal("a refused restore overwrote the file")
	}
}

func TestTrashSweep(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.Local)
	tr := testTrash(t, filepath.Join(root, "trash"), now)
	add := func(name string, ageDays int, size int) TrashEntry {
		path := filepath.Join(root, name)
		write(t, path, strings.Repeat("x", size))
		tr.Now = func() time.Time { return now.Add(-time.Duration(ageDays) * 24 * time.Hour) }
		e, err := tr.Put(path)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	old := add("old.txt", 40, 10)
	edge := add("edge.txt", 20, 10)
	fresh := add("fresh.txt", 1, 10)

	tr.Now = func() time.Time { return now }
	res := tr.Sweep(29, 0)
	if res.Removed != 1 || res.Expired != 1 || res.Bytes != 10 {
		t.Fatalf("sweep %+v", res)
	}
	left := map[string]bool{}
	for _, e := range mustEntries(t, tr) {
		left[e.ID] = true
	}
	if left[old.ID] || !left[edge.ID] || !left[fresh.ID] {
		t.Fatalf("wrong items kept: %+v", left)
	}

	// the size cap removes the oldest first
	res = tr.Sweep(29, 12)
	if res.Removed != 1 || res.Capped != 1 {
		t.Fatalf("cap sweep %+v", res)
	}
	rest := mustEntries(t, tr)
	if len(rest) != 1 || rest[0].ID != fresh.ID {
		t.Fatalf("cap kept the wrong item: %+v", rest)
	}
}

func TestTrashEmpty(t *testing.T) {
	root := t.TempDir()
	tr := testTrash(t, filepath.Join(root, "trash"), time.Date(2026, 11, 2, 12, 0, 0, 0, time.Local))
	for _, name := range []string{"a.txt", "b.txt"} {
		p := filepath.Join(root, name)
		write(t, p, "xy")
		if _, err := tr.Put(p); err != nil {
			t.Fatal(err)
		}
	}
	res := tr.Empty()
	if res.Removed != 2 || res.Bytes != 4 {
		t.Fatalf("empty %+v", res)
	}
	if n, _ := tr.Total(); n != 0 {
		t.Fatalf("%d items left", n)
	}
}

func mustEntries(t *testing.T, tr *TrashStore) []TrashEntry {
	t.Helper()
	entries, err := tr.Entries()
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestTrashUnavailableFallsBackToPermanentPlanOnly(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "f")
	write(t, f, "x")
	tr := testTrash(t, filepath.Join(root, "Trash"), time.Now())
	setSeam(t, &sameDeviceFn, func(a, b string) (bool, error) { return false, nil })
	plan, err := PlanDelete(context.Background(), []string{f}, tr)
	if err != nil {
		t.Fatal(err)
	}
	if plan.UseTrash || !strings.Contains(plan.TrashNote, "different filesystem") {
		t.Fatalf("plan should be permanent with a reason: %+v", plan)
	}
	if !exists(f) {
		t.Fatal("planning must not delete")
	}
}

func TestTrashFailureNeverDeletesPermanently(t *testing.T) {
	needUnixNonRoot(t)
	root := t.TempDir()
	f := filepath.Join(root, "f")
	write(t, f, "x")
	tr := testTrash(t, filepath.Join(root, "Trash"), time.Now())
	plan, _ := PlanDelete(context.Background(), []string{f}, tr)
	if !plan.UseTrash {
		t.Fatal("trash should be usable")
	}
	chmodRestore(t, filepath.Join(root, "Trash", "data"), 0o555)
	res := Delete(context.Background(), plan, tr, Hooks{})
	if res.OK() || len(res.Failed) != 1 || !exists(f) {
		t.Fatalf("%+v exists=%v", res, exists(f))
	}
	if n, _ := tr.Total(); n != 0 {
		t.Fatal("a failed trashing left an index record behind")
	}
}

func TestNoTrash(t *testing.T) {
	ok, why := NoTrash{Reason: "nope"}.Available("/x")
	if ok || why != "nope" {
		t.Fatal()
	}
}

// ---- the trash folder removed from outside -------------------------------------

func TestTrashIsNotDeletableFromThePanels(t *testing.T) {
	root := t.TempDir()
	tr := testTrash(t, filepath.Join(root, "Trash"), time.Now())
	f := filepath.Join(root, "a.txt")
	write(t, f, "x")
	if _, err := tr.Put(f); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{tr.Dir, filepath.Join(tr.Dir, "data"), filepath.Join(tr.Dir, "info")} {
		if _, err := PlanDelete(context.Background(), []string{p}, tr); err == nil ||
			!strings.Contains(err.Error(), "TwinPane's trash") {
			t.Fatalf("planning %s: %v", p, err)
		}
		plan, err := PlanDelete(context.Background(), []string{filepath.Join(root, "a.txt"), p}, tr)
		if err == nil {
			t.Fatalf("%s was planned anyway: %+v", p, plan)
		}
	}
	if !exists(filepath.Join(tr.Dir, "data")) {
		t.Fatal("the trash was touched by planning")
	}
}

func TestContainsComparesWholePathParts(t *testing.T) {
	root := t.TempDir()
	tr := testTrash(t, filepath.Join(root, "Trash"), time.Now())
	for _, p := range []string{tr.Dir, filepath.Join(tr.Dir, "data"), filepath.Join(tr.Dir, "data", "x")} {
		if !tr.Contains(p) {
			t.Errorf("Contains(%q) = false", p)
		}
	}
	// A sibling whose name merely starts the same must not match.
	for _, p := range []string{filepath.Join(root, "Trashy"), filepath.Join(root, "trash"), root} {
		if tr.Contains(p) {
			t.Errorf("Contains(%q) = true", p)
		}
	}
	var none *TrashStore
	if none.Contains(tr.Dir) {
		t.Error("a nil store contains nothing")
	}
}

// Starting fresh is silent; finding the folders gone is news.
func TestTrashRecreatedFlag(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Trash")
	st := testTrash(t, dir, time.Now())
	if st.Recreated {
		t.Fatal("a first start must not report a re-created trash")
	}
	// A store opened again over an existing folder stays quiet too.
	if again := testTrash(t, dir, time.Now()); again.Recreated {
		t.Fatal("an existing trash was reported as re-created")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	// The next look at the store is what notices (and rebuilds it).
	if n, _ := st.Total(); n != 0 {
		t.Fatalf("an empty rebuilt trash reported %d items", n)
	}
	if !st.Recreated {
		t.Fatal("the next look after the folder vanished should notice")
	}
	st.Acknowledge()
	if st.Recreated {
		t.Fatal("Acknowledge keeps the flag")
	}
	if _, err := os.Stat(filepath.Join(dir, "data")); err != nil {
		t.Fatalf("the store did not rebuild itself: %v", err)
	}
	// and it works again
	f := filepath.Join(root, "b.txt")
	write(t, f, "y")
	if err := st.Trash(f); err != nil {
		t.Fatalf("trashing after the folder was removed: %v", err)
	}
	if n, _ := st.Total(); n != 1 {
		t.Fatalf("trash holds %d items", n)
	}
}

// An item without an index record cannot be listed or restored, so a sweep
// clears it out instead of hiding it in the trash forever.
func TestSweepClearsOrphansAndLostRecords(t *testing.T) {
	root := t.TempDir()
	tr := testTrash(t, filepath.Join(root, "Trash"), time.Now())
	a, b := filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")
	write(t, a, "aaaa")
	write(t, b, "bbbb")
	if _, err := tr.Put(a); err != nil {
		t.Fatal(err)
	}
	entries, err := tr.Entries()
	if err != nil || len(entries) != 1 {
		t.Fatalf("%v %v", entries, err)
	}
	kept := entries[0]
	// An orphan: an item in data with no record.
	orphan := filepath.Join(tr.Dir, "data", "20260101T000000.000000000-999")
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(orphan, "inside.txt"), "cccc")
	// A record whose item is gone.
	write(t, filepath.Join(tr.Dir, "info", "20260101T000000.000000000-998.json"),
		`{"id":"20260101T000000.000000000-998","name":"ghost.txt","origin":"/x/ghost.txt"}`)

	sweep := tr.Sweep(TrashKeepDaysDef, 0)
	if sweep.Orphans != 1 || sweep.OrphanBytes != 4 {
		t.Fatalf("orphan report: %+v", sweep)
	}
	if sweep.Lost != 1 {
		t.Fatalf("lost record report: %+v", sweep)
	}
	if _, err := os.Lstat(orphan); err == nil {
		t.Fatal("the orphan was kept")
	}
	if _, err := os.Lstat(filepath.Join(tr.Dir, "info", "20260101T000000.000000000-998.json")); err == nil {
		t.Fatal("the record without an item was kept")
	}
	// Nothing else was touched.
	if !exists(filepath.Join(tr.Dir, "data", kept.ID)) {
		t.Fatal("the real item was removed")
	}
	if n, _ := tr.Total(); n != 1 {
		t.Fatalf("trash holds %d items", n)
	}
}

// Emptying the trash empties the folder, orphans included.
func TestEmptyAlsoRemovesOrphans(t *testing.T) {
	root := t.TempDir()
	tr := testTrash(t, filepath.Join(root, "Trash"), time.Now())
	f := filepath.Join(root, "a.txt")
	write(t, f, "aaaa")
	if _, err := tr.Put(f); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(tr.Dir, "data", "leftover"), "zz")
	res := tr.Empty()
	if res.Removed != 2 || res.Orphans != 1 {
		t.Fatalf("%+v", res)
	}
	des, _ := os.ReadDir(filepath.Join(tr.Dir, "data"))
	if len(des) != 0 {
		t.Fatalf("data folder still holds %d entries", len(des))
	}
}

// ---- trash: ids, removal order and eligibility ----------------------------------

// Two deletes in the same instant must not collide, and a taken id (another
// TwinPane sharing the trash, or a clock that stepped back) is answered with a
// new one rather than a failed delete.
func TestTrashIDCollisionIsRetried(t *testing.T) {
	root := t.TempDir()
	tr := testTrash(t, filepath.Join(root, "Trash"), time.Now())
	taken := tr.newID(time.Now())
	calls := 0
	tr.ID = func(time.Time) string {
		calls++
		if calls == 1 {
			return taken // pin one collision, then behave
		}
		return taken + "-" + strconv.Itoa(calls)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		p := filepath.Join(root, name)
		write(t, p, name)
		if err := tr.Trash(p); err != nil {
			t.Fatalf("trashing %s: %v", name, err)
		}
	}
	if n, _ := tr.Total(); n != 2 {
		t.Fatalf("trash holds %d items, want 2", n)
	}

	// An id function that never produces a free name is a clear error, not a
	// raw "file exists" leaking out of the index write.
	tr2 := testTrash(t, filepath.Join(root, "Trash2"), time.Now())
	tr2.ID = func(time.Time) string { return "fixed" }
	for _, name := range []string{"c.txt", "d.txt"} {
		write(t, filepath.Join(root, name), name)
	}
	if err := tr2.Trash(filepath.Join(root, "c.txt")); err != nil {
		t.Fatalf("first trash: %v", err)
	}
	err := tr2.Trash(filepath.Join(root, "d.txt"))
	if err == nil || !strings.Contains(err.Error(), "unused trash id") {
		t.Fatalf("second trash with a fixed id: %v", err)
	}
	if !exists(filepath.Join(root, "d.txt")) {
		t.Fatal("a failed trashing removed the item")
	}
}

// Ids handed out by the default generator differ even when the clock does not.
func TestTrashIDIsUniquePerCall(t *testing.T) {
	at := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	st := &TrashStore{Dir: t.TempDir()}
	seen := map[string]bool{}
	for i := 0; i < 5000; i++ {
		id := st.newID(at)
		if seen[id] {
			t.Fatalf("duplicate id %q after %d ids", id, i)
		}
		seen[id] = true
	}
}

// A removal claims the record first, so a crash in the middle leaves something
// a sweep can finish instead of data nothing points at.
func TestInterruptedRemovalIsFinishedBySweep(t *testing.T) {
	root := t.TempDir()
	tr := testTrash(t, filepath.Join(root, "Trash"), time.Now())
	f := filepath.Join(root, "a.txt")
	write(t, f, "aaaa")
	e, err := tr.Put(f)
	if err != nil {
		t.Fatal(err)
	}
	// This is the state a crash between "claim the record" and "remove the
	// item" leaves behind.
	if err := os.Rename(filepath.Join(tr.Dir, "info", e.ID+".json"),
		filepath.Join(tr.Dir, "info", e.ID+".json.removing")); err != nil {
		t.Fatal(err)
	}
	if list, _ := tr.Entries(); len(list) != 0 {
		t.Fatalf("a claimed item is still listed: %+v", list)
	}
	tr.Sweep(TrashKeepDaysDef, 0)
	if exists(filepath.Join(tr.Dir, "data", e.ID)) {
		t.Fatal("the item of an interrupted removal is still in data/")
	}
	if _, err := os.Lstat(filepath.Join(tr.Dir, "info", e.ID+".json.removing")); err == nil {
		t.Fatal("the claim outlived the sweep")
	}
}

// The trash folder cannot be trashed: neither itself, nor the folder that
// contains it (which would move the trash into itself).
func TestTrashRefusesItselfAndItsParents(t *testing.T) {
	root := t.TempDir()
	tr := testTrash(t, filepath.Join(root, "data", "twinpane", "trash"), time.Now())
	for _, p := range []string{tr.Dir, filepath.Join(root, "data"), filepath.Join(root, "data", "twinpane")} {
		if ok, why := tr.Available(p); ok {
			t.Errorf("Available(%q) = true", p)
		} else if !strings.Contains(why, "trash") {
			t.Errorf("Available(%q) refused without a reason: %q", p, why)
		}
	}
	// An ordinary item, and one whose name merely starts like the trash's, are
	// still eligible.
	sibling := filepath.Join(root, "data", "trashy")
	write(t, sibling, "x")
	if ok, why := tr.Available(sibling); !ok {
		t.Errorf("Available(%q) = false: %s", sibling, why)
	}
}

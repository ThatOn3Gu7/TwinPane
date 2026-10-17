package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestFreeDesktopTrash(t *testing.T) {
	root := t.TempDir()
	trashDir := filepath.Join(root, "Trash")
	fixed := time.Date(2026, 10, 15, 9, 30, 0, 0, time.Local)
	tr := &FreeDesktopTrash{Dir: trashDir, Now: func() time.Time { return fixed }}
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
	if read(t, filepath.Join(trashDir, "files", "my file.txt")) != "one" || read(t, filepath.Join(trashDir, "files", "proj", "a.txt")) != "alpha" {
		t.Fatal("not in trash")
	}
	info := read(t, filepath.Join(trashDir, "info", "my file.txt.trashinfo"))
	want := "[Trash Info]\nPath=" + strings.ReplaceAll(f1, " ", "%20") + "\nDeletionDate=2026-10-15T09:30:00\n"
	if info != want {
		t.Fatalf("trashinfo:\n%q\nwant\n%q", info, want)
	}
	// same name again -> unique name
	write(t, f1, "two")
	if err := tr.Trash(f1); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(trashDir, "files", "my file.2.txt")) != "two" || !exists(filepath.Join(trashDir, "info", "my file.2.txt.trashinfo")) {
		t.Fatal("collision naming")
	}
}

func TestTrashUnavailableFallsBackToPermanentPlanOnly(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "f")
	write(t, f, "x")
	tr := &FreeDesktopTrash{Dir: filepath.Join(root, "Trash")}
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
	tr := &FreeDesktopTrash{Dir: filepath.Join(root, "Trash")}
	plan, _ := PlanDelete(context.Background(), []string{f}, tr)
	if !plan.UseTrash {
		t.Fatal("trash should be usable")
	}
	chmodRestore(t, filepath.Join(root, "Trash", "files"), 0o555)
	res := Delete(context.Background(), plan, tr, Hooks{})
	if res.OK() || len(res.Failed) != 1 || !exists(f) {
		t.Fatalf("%+v exists=%v", res, exists(f))
	}
	if exists(filepath.Join(root, "Trash", "info", "f.trashinfo")) {
		t.Fatal("stale trashinfo left behind")
	}
}

func TestNoTrash(t *testing.T) {
	ok, why := NoTrash{Reason: "nope"}.Available("/x")
	if ok || why != "nope" {
		t.Fatal()
	}
}

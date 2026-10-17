package cloud

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rename must never replace another item, whatever the letter case of the
// names and whether the remote keeps case (Google Drive, S3, Linux disks)
// or ignores it (OneDrive, Dropbox, Box, Windows and macOS disks).

// ---- simulated remotes (no rclone needed) ----------------------------------

func renameFake(t *testing.T, list string, moveExit string) (*Client, string) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "log")
	return fakeClient(t, "rename", "FAKE_LOG="+log, "FAKE_LIST="+list, "FAKE_MOVE_EXIT="+moveExit), log
}

// moves returns the moveto argument lists in the fake rclone's log.
func moves(t *testing.T, log string) [][]string {
	t.Helper()
	b, _ := os.ReadFile(log)
	var out [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if f := strings.Split(line, "\x1f"); f[0] == "moveto" {
			out = append(out, f)
		}
	}
	return out
}

func has(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

const (
	itemLower = `{"Path":"a.txt","Name":"a.txt","Size":5,"IsDir":false}`
	itemUpper = `{"Path":"A.txt","Name":"A.txt","Size":5,"IsDir":false}`
	itemX     = `{"Path":"x.txt","Name":"x.txt","Size":1,"IsDir":false}`
	itemDir   = `{"Path":"Photos","Name":"Photos","Size":-1,"IsDir":true}`
)

func TestRenameCaseOnlyOntoAnotherItemRefused(t *testing.T) {
	// a case-sensitive remote holding both a.txt and A.txt: the old check
	// (strings.EqualFold) let this through and rclone replaced A.txt
	c, log := renameFake(t, "["+itemLower+","+itemUpper+"]", "0")
	err := c.Rename(context.Background(), On("gd", "docs/a.txt"), "A.txt")
	if KindOf(err) != KindExists {
		t.Fatalf("want KindExists, got %v", err)
	}
	if m := moves(t, log); len(m) != 0 {
		t.Fatalf("rclone must not be asked to move: %q", m)
	}
}

func TestRenameCaseOnlyOfItselfAllowed(t *testing.T) {
	// only a.txt: changing its case is a real rename on any remote; no
	// --ignore-existing, which a case-insensitive remote would answer with
	// "A.txt exists" (it is a.txt itself) and skip
	c, log := renameFake(t, "["+itemLower+","+itemX+"]", "0")
	if err := c.Rename(context.Background(), On("od", "a.txt"), "A.txt"); err != nil {
		t.Fatal(err)
	}
	m := moves(t, log)
	if len(m) != 1 || has(m[0], "--ignore-existing") || m[0][len(m[0])-2] != "od:a.txt" || m[0][len(m[0])-1] != "od:A.txt" {
		t.Fatalf("moveto: %q", m)
	}
}

func TestRenameOntoNameDifferingInCaseRefused(t *testing.T) {
	// x.txt -> A.txt next to a.txt: on a case-insensitive remote A.txt IS
	// a.txt, so this is refused (TwinPane cannot tell the kinds apart)
	c, log := renameFake(t, "["+itemLower+","+itemX+"]", "0")
	err := c.Rename(context.Background(), On("od", "x.txt"), "A.txt")
	if KindOf(err) != KindExists || !strings.Contains(err.Error(), `"a.txt"`) {
		t.Fatalf("want KindExists naming a.txt, got %v", err)
	}
	if len(moves(t, log)) != 0 {
		t.Fatal("rclone must not be asked to move")
	}
}

func TestRenameExactExistingRefused(t *testing.T) {
	c, log := renameFake(t, "["+itemLower+","+itemX+"]", "0")
	if err := c.Rename(context.Background(), On("gd", "x.txt"), "a.txt"); KindOf(err) != KindExists {
		t.Fatalf("want KindExists, got %v", err)
	}
	if len(moves(t, log)) != 0 {
		t.Fatal("rclone must not be asked to move")
	}
}

func TestRenameFileGuardedAgainstLateArrival(t *testing.T) {
	// the new name was free when listed, but a file with it appeared before
	// rclone moved: --ignore-existing keeps it, exit 9 is reported
	c, log := renameFake(t, "["+itemX+"]", "9")
	err := c.Rename(context.Background(), On("gd", "x.txt"), "new.txt")
	if KindOf(err) != KindExists || !strings.Contains(err.Error(), "nothing was renamed") {
		t.Fatalf("want KindExists, got %v", err)
	}
	m := moves(t, log)
	if len(m) != 1 || !has(m[0], "--ignore-existing") || !has(m[0], "--error-on-no-transfer") {
		t.Fatalf("a file rename must be guarded: %q", m)
	}
}

func TestRenameFolderNotGuardedByExitCode(t *testing.T) {
	// rclone reports no transfer for a moved folder, so exit 9 is not used
	// for folders (and a real failure is still an error)
	c, log := renameFake(t, "["+itemDir+"]", "0")
	if err := c.Rename(context.Background(), On("gd", "Photos"), "Pictures"); err != nil {
		t.Fatal(err)
	}
	if m := moves(t, log); len(m) != 1 || has(m[0], "--ignore-existing") || has(m[0], "--error-on-no-transfer") {
		t.Fatalf("folder moveto: %q", m)
	}
	c, _ = renameFake(t, "["+itemDir+"]", "1")
	if err := c.Rename(context.Background(), On("gd", "Photos"), "Pictures"); err == nil {
		t.Fatal("a failed move must be an error")
	}
}

func TestRenameMissingOrDuplicateSource(t *testing.T) {
	c, log := renameFake(t, "["+itemX+"]", "0")
	if err := c.Rename(context.Background(), On("gd", "gone.txt"), "b.txt"); KindOf(err) != KindNotFound {
		t.Fatalf("missing source: %v", err)
	}
	// Google Drive allows two items with the same name
	c, log = renameFake(t, "["+itemLower+","+itemLower+"]", "0")
	if err := c.Rename(context.Background(), On("gd", "a.txt"), "b.txt"); KindOf(err) != KindInvalid || !strings.Contains(err.Error(), "2 items") {
		t.Fatalf("duplicate source: %v", err)
	}
	if len(moves(t, log)) != 0 {
		t.Fatal("rclone must not be asked to move")
	}
}

// ---- REAL rclone, "alias" remote on a local folder (case-sensitive) ----------

func TestRealRenameNeverReplaces(t *testing.T) {
	b := realBed(t)
	ctx := context.Background()
	write(t, filepath.Join(b.cloud, "a.txt"), "lower")
	write(t, filepath.Join(b.cloud, "A.txt"), "UPPER")
	write(t, filepath.Join(b.cloud, "x.txt"), "x")
	write(t, filepath.Join(b.cloud, "Dir1", "f"), "f")
	write(t, filepath.Join(b.cloud, "Dir2", "g"), "g")

	// both exist: before the fix rclone replaced one and a.txt was lost
	if err := b.c.Rename(ctx, b.box.Join("a.txt"), "A.txt"); KindOf(err) != KindExists {
		t.Fatalf("case-only onto another file: %v", err)
	}
	if err := b.c.Rename(ctx, b.box.Join("x.txt"), "A.TXT"); KindOf(err) != KindExists {
		t.Fatalf("onto a name differing in case: %v", err)
	}
	// a folder onto an existing folder (rclone would merge them)
	if err := b.c.Rename(ctx, b.box.Join("Dir1"), "Dir2"); KindOf(err) != KindExists {
		t.Fatalf("folder onto folder: %v", err)
	}
	if err := b.c.Rename(ctx, b.box.Join("Dir1"), "dir2"); KindOf(err) != KindExists {
		t.Fatalf("folder onto folder differing in case: %v", err)
	}
	if got := tree(t, b.cloud); got != "A.txt Dir1/ Dir1/f Dir2/ Dir2/g a.txt x.txt" {
		t.Fatalf("nothing may change: %q", got)
	}
	if read(t, filepath.Join(b.cloud, "a.txt")) != "lower" || read(t, filepath.Join(b.cloud, "A.txt")) != "UPPER" {
		t.Fatal("contents changed")
	}

	// legitimate renames: case-only of the item itself (file and folder)
	os.Remove(filepath.Join(b.cloud, "A.txt"))
	if err := b.c.Rename(ctx, b.box.Join("a.txt"), "A.txt"); err != nil {
		t.Fatal(err)
	}
	if err := b.c.Rename(ctx, b.box.Join("Dir1"), "DIR1"); err != nil {
		t.Fatal(err)
	}
	if err := b.c.Rename(ctx, b.box.Join("x.txt"), "y.txt"); err != nil {
		t.Fatal(err)
	}
	if got := tree(t, b.cloud); got != "A.txt DIR1/ DIR1/f Dir2/ Dir2/g y.txt" {
		t.Fatalf("after renames: %q", got)
	}
	if read(t, filepath.Join(b.cloud, "A.txt")) != "lower" {
		t.Fatal("renamed content")
	}
	// renaming to its own name is a no-op
	if err := b.c.Rename(ctx, b.box.Join("y.txt"), "y.txt"); err != nil {
		t.Fatal(err)
	}
}

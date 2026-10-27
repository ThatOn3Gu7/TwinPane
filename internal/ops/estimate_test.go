package ops

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writeFiles(t *testing.T, dir string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		name := filepath.Join(dir, "f"+strconv.Itoa(i)+".txt")
		if err := os.WriteFile(name, []byte("12345"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEstimateTransferCountsTree(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	os.MkdirAll(filepath.Join(src, "sub", "deep"), 0o755)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("12345"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("123"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "deep", "c.txt"), []byte("1"), 0o644)

	est := EstimateTransfer(Request{Sources: []string{src}})
	if est.Dirs != 3 {
		t.Errorf("dirs = %d, want 3", est.Dirs)
	}
	if est.Files != 3 {
		t.Errorf("files = %d, want 3", est.Files)
	}
	if est.Bytes != int64(len("12345")+len("123")+len("1")) {
		t.Errorf("bytes = %d", est.Bytes)
	}
	if est.Truncated {
		t.Error("a small tree must not be truncated")
	}
	if est.Items() != 6 {
		t.Errorf("items = %d, want 6", est.Items())
	}
}

func TestEstimateTransferDoesNotFollowLinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	os.MkdirAll(outside, 0o755)
	os.WriteFile(filepath.Join(outside, "big.txt"), []byte("1234567890"), 0o644)
	src := filepath.Join(root, "src")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("1"), 0o644)
	if err := os.Symlink(outside, filepath.Join(src, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	est := EstimateTransfer(Request{Sources: []string{src}})
	if est.Bytes != 1 {
		t.Errorf("bytes = %d, want 1 (the link must not be followed)", est.Bytes)
	}
	if est.Dirs != 1 || est.Files != 2 {
		t.Errorf("dirs/files = %d/%d, want 1/2 (the link counts as an entry)", est.Dirs, est.Files)
	}
}

func TestEstimateTransferStopsAtTheCap(t *testing.T) {
	if testing.Short() {
		t.Skip("creates thousands of files")
	}
	root := t.TempDir()
	src := filepath.Join(root, "many")
	os.MkdirAll(src, 0o755)
	writeFiles(t, src, EstimateEntryCap+50)

	est := EstimateTransfer(Request{Sources: []string{src}})
	if !est.Truncated {
		t.Fatal("a tree above the cap must report Truncated")
	}
	if est.Items() < EstimateEntryCap {
		t.Fatalf("items = %d, want at least %d", est.Items(), EstimateEntryCap)
	}
	if est.Files > EstimateEntryCap+1 {
		t.Fatalf("the walk kept going past the cap: %d files", est.Files)
	}
}

func TestEstimateDeleteReusesPlanCounts(t *testing.T) {
	plan := DeletePlan{Files: 7, Dirs: 3, Bytes: 1 << 20, Incomplete: true}
	est := EstimateDelete(plan)
	if est.Files != 7 || est.Dirs != 3 || est.Bytes != 1<<20 {
		t.Fatalf("estimate = %+v", est)
	}
	if !est.Truncated {
		t.Error("an incomplete plan reports minimum totals, so it must be truncated")
	}
	if est.Items() != 10 {
		t.Errorf("items = %d, want 10", est.Items())
	}
}

package ops

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// run executes a copy or move of srcs into dest with a background context.
func run(kind Kind, dest string, h Hooks, srcs ...string) Result {
	return Run(context.Background(), Request{Kind: kind, Sources: srcs, DestDir: dest}, h)
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// noTemps fails if any TwinPane temporary file was left behind under dir.
func noTemps(t *testing.T, dir string) {
	t.Helper()
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), tmpPrefix) {
			t.Errorf("temporary file left behind: %s", p)
		}
		return nil
	})
}

// makeRemovable restores write permission under root before TempDir cleanup
// (copies faithfully preserve read-only folder modes).
func makeRemovable(t *testing.T, root string) {
	t.Cleanup(func() {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(p, 0o755)
			}
			return nil
		})
	})
}

func needUnixNonRoot(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires permission checks (non-root unix)")
	}
}

// chmodRestore changes mode and restores it at cleanup so TempDir removal works.
func chmodRestore(t *testing.T, p string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p, 0o755) })
}

// tree creates src/{a.txt, sub/b.txt, sub/deeper/c.txt} and returns src.
func tree(t *testing.T, root string) string {
	t.Helper()
	src := filepath.Join(root, "proj")
	write(t, filepath.Join(src, "a.txt"), "alpha")
	write(t, filepath.Join(src, "sub", "b.txt"), "bravo")
	write(t, filepath.Join(src, "sub", "deeper", "c.txt"), "charlie")
	return src
}

func setSeam[T any](t *testing.T, p *T, v T) {
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

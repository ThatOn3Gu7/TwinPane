package panel

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

func mkZip(t *testing.T, path string, names ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, n := range names {
		w, _ := zw.Create(n)
		if !strings.HasSuffix(n, "/") {
			w.Write([]byte("data of " + n))
		}
	}
	zw.Close()
	f.Close()
}

func entryList(p *Panel) string {
	var out []string
	for _, e := range p.Entries() {
		out = append(out, e.Name)
	}
	return strings.Join(out, ",")
}

func TestArchiveModeNavigation(t *testing.T) {
	dir := tree(t, 2)
	z := filepath.Join(dir, "pack.zip")
	mkZip(t, z, "readme.md", "docs/", "docs/guide/intro.txt", "src/main.go")
	p := newLoaded(t, dir)
	if err := apply(t, p, p.OpenArchive(z)); err != nil {
		t.Fatal(err)
	}
	if !p.InArchive() || p.ArchiveInner() != "" || p.RealDir() != dir {
		t.Fatalf("not in archive: %v %q %q", p.InArchive(), p.ArchiveInner(), p.RealDir())
	}
	if got := entryList(p); got != "..,docs,src,readme.md" {
		t.Fatalf("root %s", got)
	}
	v := ansi.Strip(p.View(theme.Default(), true))
	if !strings.Contains(v, "ZIP") || !strings.Contains(v, "pack.zip") || !strings.Contains(v, "read-only") {
		t.Fatalf("view lacks the archive indicator:\n%s", v)
	}
	// into docs/guide and back
	p.SetCursor(1)
	if err := apply(t, p, p.Open()); err != nil || p.ArchiveInner() != "docs" {
		t.Fatalf("open docs: %v %q", err, p.ArchiveInner())
	}
	p.SetCursor(1)
	apply(t, p, p.Open())
	if p.ArchiveInner() != "docs/guide" || entryList(p) != "..,intro.txt" {
		t.Fatalf("nested: %q %s", p.ArchiveInner(), entryList(p))
	}
	apply(t, p, p.Parent())
	if e, _ := p.Current(); p.ArchiveInner() != "docs" || e.Name != "guide" {
		t.Fatalf("parent: %q cursor %q", p.ArchiveInner(), e.Name)
	}
	apply(t, p, p.Parent())
	apply(t, p, p.Parent()) // ".." of the root leaves the archive
	if p.InArchive() || p.Path() != dir {
		t.Fatalf("did not leave: %s", p.Path())
	}
	if e, _ := p.Current(); e.Name != "pack.zip" {
		t.Fatalf("cursor %q after leaving", e.Name)
	}
	// LeaveArchive from deep inside
	apply(t, p, p.OpenArchive(z))
	p.SetCursor(2)
	apply(t, p, p.Open())
	apply(t, p, p.LeaveArchive())
	if p.InArchive() || p.Path() != dir {
		t.Fatal("LeaveArchive")
	}
	// browsing never extracted anything
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if e.Name() == "docs" || e.Name() == "src" || e.Name() == "readme.md" {
			t.Fatalf("browsing extracted %s", e.Name())
		}
	}
}

func TestArchiveRefreshFallsBackWhenGone(t *testing.T) {
	dir := tree(t, 0)
	z := filepath.Join(dir, "gone.zip")
	mkZip(t, z, "a/b.txt")
	p := newLoaded(t, dir)
	apply(t, p, p.OpenArchive(z))
	p.SetCursor(1)
	apply(t, p, p.Open())
	os.Remove(z)
	err := apply(t, p, p.Refresh())
	if err == nil || !strings.Contains(err.Error(), "left the archive") {
		t.Fatalf("warning: %v", err)
	}
	if p.InArchive() || p.Path() != dir {
		t.Fatalf("panel stuck in a deleted archive: %s", p.Path())
	}
	// replaced archive: refresh shows the new content
	mkZip(t, z, "x.txt")
	apply(t, p, p.OpenArchive(z))
	mkZip(t, z, "x.txt", "y.txt")
	os.Chtimes(z, p.Archive().ModTime.Add(2e9), p.Archive().ModTime.Add(2e9))
	apply(t, p, p.Refresh())
	if got := entryList(p); got != "..,x.txt,y.txt" {
		t.Fatalf("refreshed archive: %s", got)
	}
}

func TestOpenArchiveErrorKeepsPanel(t *testing.T) {
	dir := tree(t, 1)
	bad := filepath.Join(dir, "bad.zip")
	os.WriteFile(bad, []byte("PK\x03\x04 not really"), 0o644)
	p := newLoaded(t, dir)
	before := entryList(p)
	err := apply(t, p, p.OpenArchive(bad))
	if err == nil || p.InArchive() || p.Path() != dir || entryList(p) != before {
		t.Fatalf("corrupt archive: err %v, in %v", err, p.InArchive())
	}
}

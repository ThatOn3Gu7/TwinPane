package panel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ThatOn3Gu7/TwinPane/internal/cloud"
	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

// Remote mode (phase 9). REAL rclone with an "alias" remote that points at
// a temp folder (skipped when rclone is not installed).

func remoteSource(t *testing.T) (*RemoteSource, string) {
	t.Helper()
	c, err := cloud.Detect(proc.LookPath)
	if err != nil {
		if os.Getenv("TWINPANE_REQUIRE_TOOLS") != "" {
			t.Fatal("REAL rclone tests: rclone is not installed, and TWINPANE_REQUIRE_TOOLS is set")
		}
		t.Skip("REAL rclone tests: rclone is not installed")
	}
	root := t.TempDir()
	box := filepath.Join(root, "box")
	os.MkdirAll(filepath.Join(box, "docs", "deep"), 0o755)
	os.WriteFile(filepath.Join(box, "readme.txt"), []byte("hi"), 0o644)
	conf := filepath.Join(root, "rclone.conf")
	os.WriteFile(conf, []byte("[box]\ntype = alias\nremote = "+box+"\n"), 0o600)
	t.Setenv("RCLONE_CONFIG", conf)
	return &RemoteSource{Client: c, Remote: cloud.Remote{Name: "box", Type: "alias"}}, box
}

func TestRemoteOpenNavigateAndBack(t *testing.T) {
	src, box := remoteSource(t)
	dir := tree(t, 2)
	p := newLoaded(t, dir)
	if err := apply(t, p, p.OpenRemote(src)); err != nil {
		t.Fatal(err)
	}
	if !p.InRemote() || p.RealDir() != "" || p.LastLocal() != dir {
		t.Fatalf("remote=%v realdir=%q last=%q", p.InRemote(), p.RealDir(), p.LastLocal())
	}
	if got := entryList(p); !strings.Contains(got, "docs") || !strings.Contains(got, "readme.txt") {
		t.Fatalf("root listing: %s", got)
	}
	// open docs, then parent puts the cursor back on it
	focusName(t, p, "docs")
	if err := apply(t, p, p.Open()); err != nil {
		t.Fatal(err)
	}
	if p.Location().Path != "docs" && p.Location().Path != "/docs" {
		t.Fatalf("location: %+v", p.Location())
	}
	if err := apply(t, p, p.Parent()); err != nil {
		t.Fatal(err)
	}
	if e, _ := p.Current(); e.Name != "docs" {
		t.Fatalf("cursor after parent: %q", e.Name)
	}
	// a refresh of a folder that vanished climbs to the nearest parent
	if err := apply(t, p, p.Open()); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(box, "docs"))
	err := apply(t, p, p.Refresh())
	if err == nil || !strings.Contains(err.Error(), "no longer exists") || !p.Location().IsRoot() || p.Err() != nil {
		t.Fatalf("vanished folder: err=%v loc=%+v perr=%v", err, p.Location(), p.Err())
	}
	// back to local files
	if err := apply(t, p, p.GoLocal(p.LastLocal(), "")); err != nil {
		t.Fatal(err)
	}
	if p.InRemote() || p.Path() != dir {
		t.Fatalf("local again: %v %s", p.InRemote(), p.Path())
	}
}

func TestGoLocalFailureKeepsArchive(t *testing.T) {
	dir := tree(t, 1)
	zp := filepath.Join(dir, "a.zip")
	mkZip(t, zp, "x/y.txt")
	p := newLoaded(t, dir)
	if err := apply(t, p, p.OpenArchive(zp)); err != nil {
		t.Fatal(err)
	}
	if err := apply(t, p, p.GoLocal(filepath.Join(dir, "missing"), "")); err == nil {
		t.Fatal("GoLocal to a missing folder must fail")
	}
	if !p.InArchive() || p.Err() != nil {
		t.Fatal("a failed GoLocal must leave the panel in the archive, consistent")
	}
}

func focusName(t *testing.T, p *Panel, name string) {
	t.Helper()
	p.Top()
	for i := 0; i < len(p.Entries()); i++ {
		if e, ok := p.Current(); ok && e.Name == name {
			return
		}
		p.Down()
	}
	t.Fatalf("%q not in %s", name, entryList(p))
}

package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- helpers ---------------------------------------------------------------------

// navMove applies the command of a Back/Forward request and returns it.
func runNav(t *testing.T, p *Panel, m NavMove) {
	t.Helper()
	if !m.OK || m.Cmd == nil {
		t.Fatalf("history move not available: %+v", m)
	}
	if err := apply(t, p, m.Cmd); err != nil {
		t.Fatalf("history move failed: %v", err)
	}
}

func currentName(p *Panel) string {
	e, ok := p.Current()
	if !ok {
		return ""
	}
	return e.Name
}

// ---- local folders ------------------------------------------------------------------

func TestHistoryBackRestoresFolderAndCursor(t *testing.T) {
	dir := tree(t, 3)
	p := newLoaded(t, dir)

	// alpha -> inner, so going back must show the cursor on "alpha"
	focusName(t, p, "alpha")
	if err := apply(t, p, p.Open()); err != nil {
		t.Fatal(err)
	}
	focusName(t, p, "inner")
	if err := apply(t, p, p.Open()); err != nil {
		t.Fatal(err)
	}
	if p.Path() != filepath.Join(dir, "alpha", "inner") {
		t.Fatalf("path %s", p.Path())
	}
	if back, forward := p.NavDepth(); back != 2 || forward != 0 {
		t.Fatalf("depth back=%d forward=%d", back, forward)
	}

	runNav(t, p, p.Back())
	if p.Path() != filepath.Join(dir, "alpha") || currentName(p) != "inner" {
		t.Fatalf("back: path=%s cursor=%q", p.Path(), currentName(p))
	}
	runNav(t, p, p.Back())
	if p.Path() != dir || currentName(p) != "alpha" {
		t.Fatalf("back to the root: path=%s cursor=%q", p.Path(), currentName(p))
	}
	if p.CanBack() {
		t.Fatal("the initial folder must not be reachable by Back")
	}
	if back, forward := p.NavDepth(); back != 0 || forward != 2 {
		t.Fatalf("depth back=%d forward=%d", back, forward)
	}

	// forward retraces both steps, with the cursors they had
	runNav(t, p, p.Forward())
	if p.Path() != filepath.Join(dir, "alpha") || currentName(p) != "inner" {
		t.Fatalf("forward: path=%s cursor=%q", p.Path(), currentName(p))
	}
	runNav(t, p, p.Forward())
	if p.Path() != filepath.Join(dir, "alpha", "inner") {
		t.Fatalf("forward again: %s", p.Path())
	}
	if p.CanForward() {
		t.Fatal("nothing may be left to go forward to")
	}
	if m := p.Forward(); m.OK || m.Note == "" {
		t.Fatalf("forward at the end: %+v", m)
	}
}

func TestHistoryNewNavigationDropsForward(t *testing.T) {
	dir := tree(t, 3)
	p := newLoaded(t, dir)
	focusName(t, p, "alpha")
	apply(t, p, p.Open())
	runNav(t, p, p.Back())
	if !p.CanForward() {
		t.Fatal("forward must be available after going back")
	}
	focusName(t, p, "beta")
	if err := apply(t, p, p.Open()); err != nil {
		t.Fatal(err)
	}
	if p.CanForward() {
		t.Fatal("a new navigation must drop the forward entries")
	}
	runNav(t, p, p.Back())
	if p.Path() != dir || currentName(p) != "beta" {
		t.Fatalf("back after the new navigation: path=%s cursor=%q", p.Path(), currentName(p))
	}
}

func TestHistoryParentAndRefresh(t *testing.T) {
	dir := tree(t, 4)
	p := newLoaded(t, dir)
	focusName(t, p, "gamma")
	apply(t, p, p.Open())
	apply(t, p, p.Parent())
	if p.Path() != dir || currentName(p) != "gamma" {
		t.Fatalf("parent: path=%s cursor=%q", p.Path(), currentName(p))
	}
	// a refresh is not a navigation: it must not add history
	before, _ := p.NavDepth()
	apply(t, p, p.Refresh())
	if after, _ := p.NavDepth(); after != before {
		t.Fatalf("refresh changed the history: %d -> %d", before, after)
	}
	// back still returns to the folder visited before
	runNav(t, p, p.Back())
	if p.Path() != filepath.Join(dir, "gamma") {
		t.Fatalf("back after a refresh: %s", p.Path())
	}
}

func TestHistoryRestoresScrollOffset(t *testing.T) {
	// more folders than the five visible rows, so leaving one at the bottom
	// of the list happens with the list scrolled
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		if err := os.MkdirAll(filepath.Join(dir, fmt.Sprintf("d%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := newLoaded(t, dir)
	p.Bottom()
	offset := p.Offset()
	if offset == 0 || currentName(p) != "d19" {
		t.Fatalf("setup: cursor=%q offset=%d", currentName(p), offset)
	}
	apply(t, p, p.Open())
	runNav(t, p, p.Back())
	if p.Offset() != offset {
		t.Fatalf("offset after back: %d, want %d", p.Offset(), offset)
	}
	if currentName(p) != "d19" {
		t.Fatalf("cursor after back: %q", currentName(p))
	}
}

func TestHistorySurvivesDeletedFolder(t *testing.T) {
	dir := tree(t, 2)
	os.MkdirAll(filepath.Join(dir, "extra"), 0o755)
	p := newLoaded(t, dir)
	focusName(t, p, "extra")
	apply(t, p, p.Open())
	apply(t, p, p.Parent())
	// the folder disappears before we go back to it
	os.RemoveAll(filepath.Join(dir, "extra"))
	focusName(t, p, "extra")
	apply(t, p, p.Open()) // fails: gone
	if p.Path() != dir {
		t.Fatalf("a failed open must not move the panel: %s", p.Path())
	}
	// the dead entry is skipped, with an explanation, and history stays usable
	m := p.Back()
	if !m.OK {
		t.Fatalf("back must skip the dead folder: %+v", m)
	}
	if !strings.Contains(m.Note, "no longer available") || !strings.Contains(m.Note, "extra") {
		t.Fatalf("the skipped folder must be named: %q", m.Note)
	}
	runNav(t, p, m)
	if p.Path() != dir {
		t.Fatalf("path after skipping: %s", p.Path())
	}
	if p.Back().OK {
		t.Fatal("going back again must not be possible")
	}
}

func TestHistoryFailedRestorePutsTheEntryBack(t *testing.T) {
	dir := tree(t, 2)
	p := newLoaded(t, dir)
	focusName(t, p, "alpha")
	apply(t, p, p.Open())
	runNav(t, p, p.Back())
	// make the forward target unreadable so its load fails asynchronously
	inner := filepath.Join(dir, "alpha")
	if err := os.Chmod(inner, 0o000); err != nil {
		t.Skip("cannot change permissions here")
	}
	defer os.Chmod(inner, 0o755)
	m := p.Forward()
	if !m.OK {
		t.Fatalf("forward: %+v", m)
	}
	if err := apply(t, p, m.Cmd); err == nil {
		t.Skip("the folder is still readable (running as root?)")
	}
	if p.Path() != dir {
		t.Fatalf("a failed forward must keep the panel: %s", p.Path())
	}
	// the entry came back: it can be tried again
	if !p.CanForward() {
		t.Fatal("a failed forward must put the entry back")
	}
}

// ---- archives ---------------------------------------------------------------------

func TestHistoryInsideArchive(t *testing.T) {
	dir := tree(t, 1)
	zp := filepath.Join(dir, "pack.zip")
	mkZip(t, zp, "docs/", "docs/deep/", "docs/deep/note.txt", "readme.txt")
	p := newLoaded(t, dir)
	if err := apply(t, p, p.OpenArchive(zp)); err != nil {
		t.Fatal(err)
	}
	focusName(t, p, "docs")
	apply(t, p, p.Open())
	focusName(t, p, "deep")
	apply(t, p, p.Open())
	if !p.InArchive() || p.ArchiveInner() != "docs/deep" {
		t.Fatalf("inner: %q", p.ArchiveInner())
	}
	// back inside the archive restores the virtual folder and the cursor
	runNav(t, p, p.Back())
	if !p.InArchive() || p.ArchiveInner() != "docs" || currentName(p) != "deep" {
		t.Fatalf("back in archive: inner=%q cursor=%q", p.ArchiveInner(), currentName(p))
	}
	runNav(t, p, p.Back())
	if !p.InArchive() || p.ArchiveInner() != "" || currentName(p) != "docs" {
		t.Fatalf("back to the root: inner=%q cursor=%q", p.ArchiveInner(), currentName(p))
	}
	// and forward again
	runNav(t, p, p.Forward())
	if !p.InArchive() || p.ArchiveInner() != "docs" {
		t.Fatalf("forward in archive: inner=%q", p.ArchiveInner())
	}

	// leaving the archive and going back re-enters it at the same folder
	if err := apply(t, p, p.LeaveArchive()); err != nil {
		t.Fatal(err)
	}
	if p.InArchive() || currentName(p) != "pack.zip" {
		t.Fatalf("left the archive: inArc=%v cursor=%q", p.InArchive(), currentName(p))
	}
	runNav(t, p, p.Back())
	if !p.InArchive() || p.ArchiveInner() != "docs" || currentName(p) != "deep" {
		t.Fatalf("back into the archive: inArc=%v inner=%q cursor=%q", p.InArchive(), p.ArchiveInner(), currentName(p))
	}
}

func TestHistoryArchiveThatVanished(t *testing.T) {
	dir := tree(t, 1)
	zp := filepath.Join(dir, "gone.zip")
	mkZip(t, zp, "a.txt")
	p := newLoaded(t, dir)
	if err := apply(t, p, p.OpenArchive(zp)); err != nil {
		t.Fatal(err)
	}
	apply(t, p, p.LeaveArchive())
	os.Remove(zp)
	// the dead entry is skipped (the folder it was opened from is where the
	// panel already is) and the user is told why
	m := p.Back()
	if !m.OK || !strings.Contains(m.Note, "gone.zip") || !strings.Contains(m.Note, "no longer available") {
		t.Fatalf("a deleted archive must be skipped with a note: %+v", m)
	}
	runNav(t, p, m)
	if p.Path() != dir || p.InArchive() || p.Err() != nil {
		t.Fatalf("the panel must stay on its folder: %s inArc=%v err=%v", p.Path(), p.InArchive(), p.Err())
	}
	if p.CanBack() {
		t.Fatal("the skipped entry must be gone for good")
	}
	if _, forward := p.NavDepth(); forward != 1 {
		t.Fatalf("forward holds the place it came from: %d", forward)
	}
}

// ---- remotes ------------------------------------------------------------------------

func TestHistoryRemotesAndLocalMix(t *testing.T) {
	src, box := remoteSource(t)
	dir := tree(t, 1)
	p := newLoaded(t, dir)
	if err := apply(t, p, p.OpenRemote(src)); err != nil {
		t.Fatal(err)
	}
	focusName(t, p, "docs")
	apply(t, p, p.Open())
	if !p.InRemote() || p.Location().Path == "" {
		t.Fatalf("remote folder: %+v", p.Location())
	}
	// back: the remote root, with the cursor on the folder
	runNav(t, p, p.Back())
	if !p.InRemote() || !p.Location().IsRoot() || currentName(p) != "docs" {
		t.Fatalf("back on the remote: %v %+v %q", p.InRemote(), p.Location(), currentName(p))
	}
	// back: local files again
	runNav(t, p, p.Back())
	if p.InRemote() || p.Path() != dir {
		t.Fatalf("back to local: remote=%v path=%s", p.InRemote(), p.Path())
	}
	// forward returns to the remote root, using the recorded source
	runNav(t, p, p.Forward())
	if !p.InRemote() || p.Source() == nil || p.Source().Remote.Name != "box" {
		t.Fatalf("forward to the remote: %v", p.Source())
	}
	if _, err := os.Stat(filepath.Join(box, "docs")); err != nil {
		t.Fatal(err)
	}
	// and forward into the remote folder
	runNav(t, p, p.Forward())
	if p.Location().Path != "docs" && p.Location().Path != "/docs" {
		t.Fatalf("forward into the remote folder: %+v", p.Location())
	}
}

func TestHistoryRemoteSourceStaysTyped(t *testing.T) {
	src, _ := remoteSource(t)
	dir := tree(t, 1)
	p := newLoaded(t, dir)
	if err := apply(t, p, p.OpenRemote(src)); err != nil {
		t.Fatal(err)
	}
	if err := apply(t, p, p.GoLocal(dir, "")); err != nil {
		t.Fatal(err)
	}
	if p.InRemote() {
		t.Fatal("the panel must be local")
	}
	// a local path that looks like an rclone argument is still local
	odd := filepath.Join(dir, "box:weird")
	if err := os.MkdirAll(odd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := apply(t, p, p.Load(odd, "", LoadNavigate)); err != nil {
		t.Fatal(err)
	}
	if p.InRemote() {
		t.Fatalf("%q was mistaken for a remote", odd)
	}
	m := p.Back()
	if !m.OK || m.Label == "" {
		t.Fatalf("back: %+v", m)
	}
	runNav(t, p, m)
	if p.Path() != dir {
		t.Fatalf("back from the odd name: %s", p.Path())
	}
}

func TestHistoryPerPanelIndependence(t *testing.T) {
	dir := tree(t, 2)
	a := newLoaded(t, dir)
	b := New(1, nil)
	b.SetSize(60, 12)
	other := t.TempDir()
	os.MkdirAll(filepath.Join(other, "x"), 0o755)
	if err := apply(t, b, b.Load(other, "", LoadForce)); err != nil {
		t.Fatal(err)
	}
	focusName(t, a, "alpha")
	apply(t, a, a.Open())
	if b.CanBack() || b.CanForward() {
		t.Fatal("the other panel must not gain history")
	}
	if !a.CanBack() {
		t.Fatal("the panel that navigated must have history")
	}
	// each panel goes back on its own
	runNav(t, a, a.Back())
	if a.Path() != dir || b.Path() != other {
		t.Fatalf("paths: a=%s b=%s", a.Path(), b.Path())
	}
	if b.CanForward() {
		t.Fatal("history must not be shared")
	}
	if !a.CanForward() {
		t.Fatal("the navigating panel keeps its forward entry")
	}
}

func TestHistoryLimitsAndLabels(t *testing.T) {
	dir := t.TempDir()
	cur := dir
	for i := 0; i < navLimit+5; i++ {
		cur = filepath.Join(cur, "d")
		os.MkdirAll(cur, 0o755)
	}
	// walk down the chain so the back stack overflows
	p := newLoaded(t, dir)
	path := dir
	for i := 0; i < navLimit+5; i++ {
		path = filepath.Join(path, "d")
		if err := apply(t, p, p.Load(path, "", LoadNavigate)); err != nil {
			t.Fatal(err)
		}
	}
	back, _ := p.NavDepth()
	if back != navLimit {
		t.Fatalf("history must be capped at %d, got %d", navLimit, back)
	}
	if l := (NavLocation{Kind: NavLocal, Path: "/tmp/x"}).Label(); l != "/tmp/x" {
		t.Fatalf("local label %q", l)
	}
	if l := (NavLocation{Kind: NavArchive, Path: "/tmp/a.zip", Inner: "sub"}).Label(); l != "a.zip: /sub" {
		t.Fatalf("archive label %q", l)
	}
}

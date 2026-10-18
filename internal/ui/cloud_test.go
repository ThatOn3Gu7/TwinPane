package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

// Cloud remotes in the UI (phase 9).
//
// Two kinds of tests:
//   - REAL rclone (skipped when rclone is not installed): rclone runs for
//     real against "alias" remotes that point at temp folders, configured
//     in a throw-away RCLONE_CONFIG. No network, no cloud account.
//   - SIMULATED: a tiny shell script stands in for rclone (a listing that
//     hangs, so cancellation can be tested), or LookPath hides rclone.

// cloudEnv is an ops app plus an rclone config with two remotes:
// "box" -> root/cloud and "second box" -> root/cloud2, and "gone" pointing
// at a folder that does not exist.
type cloudEnv struct {
	*opsEnv
	cloud, cloud2 string
}

func newCloudApp(t *testing.T) (*harness, *cloudEnv) {
	t.Helper()
	if _, err := proc.LookPath("rclone"); err != nil {
		if os.Getenv("TWINPANE_REQUIRE_TOOLS") != "" {
			t.Fatal("REAL rclone tests: rclone is not installed, and TWINPANE_REQUIRE_TOOLS is set")
		}
		t.Skip("REAL rclone tests: rclone is not installed")
	}
	h, env := newOpsApp(t, nil, nil)
	ce := &cloudEnv{opsEnv: env, cloud: filepath.Join(env.root, "cloud"), cloud2: filepath.Join(env.root, "cloud2")}
	os.MkdirAll(filepath.Join(ce.cloud, "photos"), 0o755)
	os.MkdirAll(ce.cloud2, 0o755)
	os.WriteFile(filepath.Join(ce.cloud, "notes.md"), []byte("remote notes"), 0o644)
	os.WriteFile(filepath.Join(ce.cloud, "photos", "p1.jpg"), []byte("jpeg"), 0o644)
	conf := filepath.Join(env.root, "rclone.conf")
	body := "[box]\ntype = alias\nremote = " + ce.cloud + "\n\n" +
		"[second box]\ntype = alias\nremote = " + ce.cloud2 + "\n\n" +
		"[gone]\ntype = alias\nremote = " + filepath.Join(env.root, "no-such-dir") + "\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RCLONE_CONFIG", conf)
	return h, ce
}

// openRemote opens the named remote in the active panel through the
// Sources picker, pressing the key the picker shows for it.
func (h *harness) openRemote(name string) {
	h.t.Helper()
	h.press("ctrl+r")
	h.waitDialog(dlgSources)
	key := ""
	h.until("remotes listed", func() bool {
		if !h.dialogKind(dlgSources) {
			return false
		}
		for _, it := range h.a.dialog.list {
			if it.label == name+":" {
				key = it.key
				return true
			}
		}
		return false
	})
	h.press(key)
	p := h.a.ActivePanel()
	h.until("remote opened", func() bool {
		return !p.Loading() && (p.Err() != nil || h.a.statusErr || (p.InRemote() && p.Source().Remote.Name == name))
	})
	h.settle()
}

func (h *harness) waitIdlePanels() {
	h.t.Helper()
	h.until("panels loaded", func() bool {
		return !h.a.panels[0].Loading() && !h.a.panels[1].Loading() && !h.a.busy()
	})
	h.settle()
}

func TestCloudSourceSwitchKeepsOtherPanel(t *testing.T) { // REAL rclone
	h, env := newCloudApp(t)
	// right panel: a folder with a selection that must survive
	os.WriteFile(filepath.Join(env.dst, "keep1"), nil, 0o644)
	os.WriteFile(filepath.Join(env.dst, "keep2"), nil, 0o644)
	h.press("tab", "ctrl+l")
	h.waitIdlePanels()
	h.focus("keep1")
	h.press(" ")
	h.press("tab")

	h.openRemote("box")
	left, right := h.a.panels[0], h.a.panels[1]
	if !left.InRemote() || left.Err() != nil {
		t.Fatalf("left must show box: err=%v", left.Err())
	}
	if !panelHas(left, "notes.md") || !panelHas(left, "photos") {
		t.Fatalf("remote listing: %v", names(left))
	}
	if right.Path() != env.dst || !right.IsSelected("keep1") {
		t.Fatalf("other panel changed: %s sel=%v", right.Path(), right.Selection())
	}
	v := h.view()
	if !strings.Contains(v, "box:") || !strings.Contains(v, "LOCAL") {
		t.Fatalf("labels missing:\n%s", v)
	}
	if !strings.Contains(h.status(), "box") {
		t.Fatalf("status: %q", h.status())
	}
	// navigation: into a folder and back
	h.focus("photos")
	h.press("enter")
	h.until("photos listed", func() bool { return !left.Loading() && panelHas(left, "p1.jpg") })
	if !strings.Contains(h.view(), "/photos") {
		t.Fatalf("path line must show the remote folder:\n%s", h.view())
	}
	h.press("backspace")
	h.until("root again", func() bool { return !left.Loading() && panelHas(left, "notes.md") })
	if e, _ := left.Current(); e.Name != "photos" {
		t.Fatalf("cursor must return to the folder, got %q", e.Name)
	}
	// back to local files: the folder shown before
	h.press("ctrl+r")
	h.waitDialog(dlgSources)
	h.press("l")
	h.until("local again", func() bool { return !left.Loading() && !left.InRemote() })
	if left.Path() != env.src {
		t.Fatalf("local folder: %s, want %s", left.Path(), env.src)
	}
	if strings.Contains(h.view(), "LOCAL") {
		t.Fatal("the LOCAL label is only shown while a panel shows a remote")
	}
}

func TestCloudTransfersBothWays(t *testing.T) { // REAL rclone
	h, env := newCloudApp(t)
	special := "it's a $file (1) [x].txt"
	os.WriteFile(filepath.Join(env.src, special), []byte("special"), 0o644)
	h.press("tab")
	h.openRemote("box") // right panel = box
	h.press("tab")      // left = local src

	// upload: multi-select a file, a folder and the special name
	h.press("ctrl+l")
	h.waitIdlePanels()
	for _, n := range []string{"a.txt", "sub", special} {
		h.focus(n)
		h.press(" ")
	}
	h.press("c")
	if !h.dialogKind(dlgConfirm) || !strings.Contains(h.view(), "box:") {
		t.Fatalf("upload confirmation:\n%s", h.view())
	}
	h.press("enter")
	h.idle()
	for _, rel := range []string{"a.txt", special, "sub/deep/c.txt"} {
		if _, err := os.Stat(filepath.Join(env.cloud, rel)); err != nil {
			t.Fatalf("not uploaded: %s (%v); status %q", rel, err, h.status())
		}
	}
	h.waitIdlePanels()
	if !panelHas(h.a.panels[1], special) {
		t.Fatalf("remote panel not refreshed: %v", names(h.a.panels[1]))
	}

	// upload again: existing destination -> conflict dialog, overwrite
	os.WriteFile(filepath.Join(env.src, "a.txt"), []byte("changed a"), 0o644)
	h.press("u")
	h.focus("a.txt")
	h.press("c")
	h.press("enter")
	h.waitDialog(dlgConflict)
	h.press("o")
	h.idle()
	if b, _ := os.ReadFile(filepath.Join(env.cloud, "a.txt")); string(b) != "changed a" {
		t.Fatalf("overwrite: %q", b)
	}
	// and skip keeps the remote file
	os.WriteFile(filepath.Join(env.src, "a.txt"), []byte("third"), 0o644)
	h.focus("a.txt")
	h.press("c", "enter")
	h.waitDialog(dlgConflict)
	h.press("s")
	h.idle()
	if b, _ := os.ReadFile(filepath.Join(env.cloud, "a.txt")); string(b) != "changed a" {
		t.Fatalf("skip must keep the file: %q", b)
	}

	// download with move: remote notes.md -> local, gone from the remote
	h.press("tab")
	h.waitIdlePanels()
	h.focus("notes.md")
	h.press("m")
	if !strings.Contains(h.view(), "Downloading (move)") {
		t.Fatalf("download confirmation:\n%s", h.view())
	}
	h.press("enter")
	h.idle()
	if b, err := os.ReadFile(filepath.Join(env.src, "notes.md")); err != nil || string(b) != "remote notes" {
		t.Fatalf("download: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(env.cloud, "notes.md")); !os.IsNotExist(err) {
		t.Fatal("move must remove the remote file")
	}

	// remote -> remote: box (right) to "second box" (left)
	h.press("tab")
	h.openRemote("second box")
	h.press("tab")
	h.waitIdlePanels()
	h.focus("photos")
	h.press("c")
	if !strings.Contains(h.view(), "second box:") {
		t.Fatalf("remote to remote confirmation:\n%s", h.view())
	}
	h.press("enter")
	h.idle()
	if _, err := os.Stat(filepath.Join(env.cloud2, "photos", "p1.jpg")); err != nil {
		t.Fatalf("remote to remote copy: %v (status %q)", err, h.status())
	}
}

// Local folder creation validates names like rename does (control
// characters, overlong names): refused in the dialog, nothing created.
// No rclone is involved: this is the local path of the same dialog.
func TestLocalMkdirNameValidation(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.press("n")
	if !h.dialogKind(dlgMkdir) {
		t.Fatalf("new folder prompt:\n%s", h.view())
	}
	// a name with a control character, as a hostile paste could carry it
	h.a.dialog.input = newLineInput("bad\x01name")
	h.press("enter")
	if !h.dialogKind(dlgMkdir) {
		t.Fatalf("a name with a control character was accepted and the dialog closed")
	}
	if !strings.Contains(h.a.dialog.errLine, "control") {
		t.Fatalf("a name with a control character must be refused: %q", h.a.dialog.errLine)
	}
	if ents, _ := os.ReadDir(env.src); len(ents) != 3 { // a.txt b.txt sub
		t.Fatalf("nothing may be created: %v", ents)
	}
	// an overlong name
	h.press("ctrl+u")
	h.typeText(strings.Repeat("a", 256), false)
	h.press("enter")
	if !h.dialogKind(dlgMkdir) || !strings.Contains(h.a.dialog.errLine, "255") {
		t.Fatalf("an overlong name must be refused: %q", h.a.dialog.errLine)
	}
	// a valid name still works
	h.press("ctrl+u")
	h.typeText("New Folder #1", false)
	h.press("enter")
	h.idle()
	if fi, err := os.Stat(filepath.Join(env.src, "New Folder #1")); err != nil || !fi.IsDir() {
		t.Fatalf("mkdir locally: %v (status %q)", err, h.status())
	}
}

func TestCloudMkdirRenameDelete(t *testing.T) { // REAL rclone
	h, env := newCloudApp(t)
	h.openRemote("box")
	p := h.a.ActivePanel()

	h.press("n")
	if !h.dialogKind(dlgMkdir) {
		t.Fatalf("new folder prompt:\n%s", h.view())
	}
	h.typeText("bad/name", false)
	h.press("enter")
	if !h.dialogKind(dlgMkdir) || h.a.dialog.errLine == "" {
		t.Fatal("a name with a slash must be refused in the dialog")
	}
	h.press("ctrl+u")
	h.typeText("New Folder #1", false)
	h.press("enter")
	h.idle()
	if fi, err := os.Stat(filepath.Join(env.cloud, "New Folder #1")); err != nil || !fi.IsDir() {
		t.Fatalf("mkdir on the remote: %v (status %q)", err, h.status())
	}
	h.waitIdlePanels()
	if e, _ := p.Current(); e.Name != "New Folder #1" {
		t.Fatalf("cursor must be on the new folder, got %q", e.Name)
	}

	// rename a file (single item)
	h.focus("notes.md")
	h.press("r")
	if !h.dialogKind(dlgRename) {
		t.Fatalf("rename prompt:\n%s", h.view())
	}
	h.press("ctrl+u")
	h.typeText("renamed é", false) // the cursor starts before ".md", which is kept
	h.press("enter")
	h.idle()
	if _, err := os.Stat(filepath.Join(env.cloud, "renamed é.md")); err != nil {
		t.Fatalf("rename: %v (status %q)", err, h.status())
	}
	// bulk rename is refused on a remote
	h.waitIdlePanels()
	h.press("a")
	h.press("r")
	if h.a.bulk != nil || !strings.Contains(h.status(), "local panel") {
		t.Fatalf("bulk rename on a remote: %q", h.status())
	}
	h.press("u")

	// delete one file: y/n confirmation
	h.focus("renamed é.md")
	h.press("d")
	if !h.dialogKind(dlgDelete) {
		t.Fatalf("delete confirmation:\n%s", h.view())
	}
	h.press("n")
	if _, err := os.Stat(filepath.Join(env.cloud, "renamed é.md")); err != nil {
		t.Fatal("n must not delete")
	}
	h.press("d", "y")
	h.idle()
	if _, err := os.Stat(filepath.Join(env.cloud, "renamed é.md")); !os.IsNotExist(err) {
		t.Fatalf("delete: %v", err)
	}
	// delete a folder: typed confirmation
	h.waitIdlePanels()
	h.focus("photos")
	h.press("d")
	h.typeText("yes", false)
	h.press("enter")
	if !h.dialogKind(dlgDelete) {
		t.Fatal("only \"delete\" confirms deleting a folder")
	}
	h.press("ctrl+u")
	h.typeText("delete", false)
	h.press("enter")
	h.idle()
	if _, err := os.Stat(filepath.Join(env.cloud, "photos")); !os.IsNotExist(err) {
		t.Fatalf("folder delete: %v", err)
	}
}

func TestCloudGatingAndErrorState(t *testing.T) { // REAL rclone
	h, env := newCloudApp(t)
	h.openRemote("box")
	h.focus("notes.md")
	for _, k := range []string{"v", "e", "z", "P", "/", "B", "i", "enter"} {
		h.a.setStatus("", false)
		h.press(k)
		if h.a.dialog != nil || h.a.viewer != nil || h.a.search != nil || len(env.execs) != 0 {
			t.Fatalf("%q must not act on a remote file", k)
		}
		if st := h.status(); !strings.Contains(st, "copy") || !h.a.statusErr {
			t.Fatalf("%q: status %q", k, st)
		}
	}
	// a bookmark jump from a remote panel returns it to local files
	s, err := h.a.bookmarkStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(env.dst, ""); err != nil {
		t.Fatal(err)
	}
	h.press("b")
	h.waitDialog(dlgBookmarks)
	h.press("enter")
	left := h.a.ActivePanel()
	h.until("bookmark opened", func() bool { return !left.Loading() && !left.InRemote() })
	if left.Path() != env.dst || left.Err() != nil {
		t.Fatalf("bookmark from a remote: %s %v", left.Path(), left.Err())
	}
	h.openRemote("box")

	// opening an unavailable remote: the panel stays where it was and the
	// status line says why
	h.press("ctrl+r")
	h.waitDialog(dlgSources)
	h.until("listed", func() bool { return strings.Contains(h.view(), "gone:") })
	for _, it := range h.a.dialog.list {
		if it.label == "gone:" {
			h.press(it.key)
		}
	}
	p := h.a.ActivePanel()
	h.until("failed open", func() bool { return !p.Loading() && h.a.statusErr })
	if p.Source().Remote.Name != "box" || p.Err() != nil || !strings.Contains(h.status(), "gone:") {
		t.Fatalf("failed open: remote=%s err=%v status=%q", p.Source().Remote.Name, p.Err(), h.status())
	}
	// the shown remote becomes unavailable: error state with retry/sources
	// hints, not frozen
	if err := os.Rename(env.cloud, env.cloud+".away"); err != nil {
		t.Fatal(err)
	}
	h.press("ctrl+l")
	h.until("error state", func() bool { return !p.Loading() && p.Err() != nil })
	if f := h.footer(); !strings.Contains(f, "^L  retry") || !strings.Contains(f, "^R  sources") {
		t.Fatalf("error hints: %q", f)
	}
	if !strings.Contains(h.view(), "^L: retry") {
		t.Fatalf("error panel:\n%s", h.view())
	}
	os.Rename(env.cloud+".away", env.cloud)
	h.press("ctrl+l")
	h.until("retried", func() bool { return !p.Loading() && p.Err() == nil && panelHas(p, "notes.md") })
	h.press("tab") // still responsive
	if h.a.active != 1 {
		t.Fatal("tab must still work")
	}
	h.press("tab", "~")
	h.until("home", func() bool { return !p.Loading() && !p.InRemote() })
	if p.Path() != env.root+"/home" {
		t.Fatalf("~ from a remote goes to the local home: %s", p.Path())
	}
}

func TestCloudSyncOtherPanel(t *testing.T) { // REAL rclone
	h, _ := newCloudApp(t)
	h.openRemote("box")
	h.focus("photos")
	h.press("enter")
	left, right := h.a.panels[0], h.a.panels[1]
	h.until("photos", func() bool { return !left.Loading() && panelHas(left, "p1.jpg") })
	h.press("=")
	h.until("right on box", func() bool { return !right.Loading() && right.InRemote() })
	if right.Location() != left.Location() || !panelHas(right, "p1.jpg") {
		t.Fatalf("= must show the same remote folder: %v vs %v", right.Location(), left.Location())
	}
	// from a local panel, = brings a remote panel back to local files
	h.press("tab")
	h.press("ctrl+r")
	h.waitDialog(dlgSources)
	h.press("l")
	h.until("right local", func() bool { return !right.Loading() && !right.InRemote() })
	h.press("tab")
	h.press("ctrl+r")
	h.waitDialog(dlgSources)
	h.press("l")
	h.until("left local", func() bool { return !left.Loading() && !left.InRemote() })
	h.press("tab")
	h.openRemote("box")
	h.press("tab", "=")
	h.until("right local again", func() bool { return !right.Loading() })
	if right.InRemote() {
		t.Fatal("= from a remote panel copies the remote location; the local panel must stay")
	}
}

// ---- simulated rclone ---------------------------------------------------------------

func TestCloudRcloneMissing(t *testing.T) { // SIMULATED: LookPath finds no rclone
	h, env := newOpsApp(t, nil, nil)
	env.hide["rclone"] = true
	h.press("ctrl+r")
	if !h.dialogKind(dlgSources) {
		t.Fatal("Ctrl+R must open the Sources picker")
	}
	v := h.view()
	for _, want := range []string{"rclone is not installed", "Local files", "rclone config"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	h.press("esc")
	if h.a.dialog != nil {
		t.Fatal("Esc closes the picker")
	}
	// local features still work: copy a file to the other panel
	h.focus("a.txt")
	h.press("c", "enter")
	h.idle()
	if _, err := os.Stat(filepath.Join(env.dst, "a.txt")); err != nil {
		t.Fatalf("local copy without rclone: %v", err)
	}
	// Termux gets the pkg hint
	env.env["TERMUX_VERSION"] = "0.118"
	env.env["PREFIX"] = "/data/data/com.termux/files/usr"
	h.a.platCache = nil
	h.press("ctrl+r")
	if !strings.Contains(h.view(), "pkg install rclone") {
		t.Fatalf("Termux install hint:\n%s", h.view())
	}
}

// fakeRclone puts a shell-script "rclone" first in PATH.
func fakeRclone(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the simulated rclone is a POSIX shell script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rclone"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCloudCancelSlowListing(t *testing.T) { // SIMULATED: a listing that hangs
	h, env := newOpsApp(t, nil, nil)
	pidFile := filepath.Join(env.root, "pid")
	fakeRclone(t, `for a in "$@"; do
  case "$a" in
    listremotes) echo "slow:         drive"; echo "empty:        alias"; exit 0 ;;
    lsjson) echo $$ > `+pidFile+`; exec sleep 60 ;;
  esac
done
exit 1
`)
	h.press("ctrl+r")
	h.waitDialog(dlgSources)
	h.until("remotes", func() bool { return strings.Contains(h.view(), "slow:") })
	if !strings.Contains(h.view(), "rclone keeps the logins") {
		t.Fatal("the picker must say TwinPane stores no credentials")
	}
	h.press("1")
	p := h.a.ActivePanel()
	h.until("rclone started", func() bool { _, err := os.Stat(pidFile); return err == nil })
	if !p.Loading() || p.InRemote() {
		t.Fatal("the panel must be loading and still local")
	}
	// the UI stays responsive while rclone hangs
	h.press("down")
	start := time.Now()
	h.press("esc")
	h.until("listing cancelled", func() bool { return !p.Loading() })
	if time.Since(start) > 5*time.Second {
		t.Fatalf("cancelling took %v", time.Since(start))
	}
	if p.InRemote() || p.Path() != env.src || p.Err() != nil {
		t.Fatalf("a cancelled listing must leave the panel where it was: %s remote=%v err=%v", p.Path(), p.InRemote(), p.Err())
	}
	if st := h.status(); !strings.Contains(st, "cancelled") {
		t.Fatalf("status: %q", st)
	}
}

func TestCloudNoRemotesExplainsConfig(t *testing.T) { // SIMULATED: rclone without remotes
	h, _ := newOpsApp(t, nil, nil)
	fakeRclone(t, "exit 0\n")
	h.press("ctrl+r")
	h.waitDialog(dlgSources)
	h.until("listed", func() bool { return strings.Contains(h.view(), "No rclone remotes") })
	if !strings.Contains(h.view(), "rclone config") {
		t.Fatalf("must explain rclone config:\n%s", h.view())
	}
}

func TestCloudCancelTransfer(t *testing.T) { // REAL rclone, slowed down with RCLONE_BWLIMIT
	h, env := newCloudApp(t)
	t.Setenv("RCLONE_BWLIMIT", "64k")
	big := filepath.Join(env.src, "big.bin")
	os.WriteFile(big, []byte(strings.Repeat("z", 4<<20)), 0o644) // ~60 s at 64 KiB/s
	h.press("tab")
	h.openRemote("box")
	h.press("tab", "ctrl+l")
	h.waitIdlePanels()
	h.focus("big.bin")
	h.press("c", "enter")
	h.until("transfer running", func() bool {
		return h.a.op != nil && h.a.op.prog.DoneBytes > 0
	})
	start := time.Now()
	h.press("esc")
	h.idle()
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("cancelling took %v", d)
	}
	if st := strings.ToLower(h.status()); !strings.Contains(st, "cancel") {
		t.Fatalf("status: %q", h.status())
	}
	ents, _ := os.ReadDir(env.cloud)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "big.bin") {
			t.Fatalf("an interrupted upload must leave nothing behind: %s", e.Name())
		}
	}
	if _, err := os.Stat(big); err != nil {
		t.Fatal("the source must stay")
	}
}

func TestCloudAuthFailureExplained(t *testing.T) { // SIMULATED: rclone reports an expired login
	h, _ := newOpsApp(t, nil, nil)
	fakeRclone(t, `for a in "$@"; do
  case "$a" in
    listremotes) echo "drive:        drive"; exit 0 ;;
    lsjson) echo 'ERROR : couldn'"'"'t list: oauth2: "invalid_grant" "Bad Request" - maybe token expired? - try refreshing with "rclone config reconnect drive:"' >&2; exit 1 ;;
  esac
done
exit 1
`)
	h.openRemote("drive")
	p := h.a.ActivePanel()
	if p.InRemote() || p.Err() != nil {
		t.Fatal("a refused login must leave the panel where it was")
	}
	st := h.status()
	if !h.a.statusErr || !strings.Contains(st, "rclone config reconnect drive:") || strings.Count(st, "reconnect") != 1 {
		t.Fatalf("auth status: %q", st)
	}
}

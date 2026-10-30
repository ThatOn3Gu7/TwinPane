package ui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/cloud"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
)

// Remote previews (read-only viewer over rclone).
//
// A shell script stands in for rclone: providers differ in what they do with
// "cat" (stream, keep streaming, refuse, fail midway, hang), so each case is
// exercised without a cloud account. The real-rclone path is covered by the
// cloud package's alias-remote tests.

// fakeRcloneBin writes an rclone stand-in that handles "lsjson" (listing) and
// "cat" (previews) and returns its path.
func fakeRcloneBin(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the simulated rclone is a POSIX shell script")
	}
	p := filepath.Join(t.TempDir(), "rclone")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// listingBin is a fake rclone whose folder holds notes.txt and huge.txt.
func listingBin(t *testing.T, cat string) string {
	t.Helper()
	return fakeRcloneBin(t, `case "$1" in
  lsjson)
    echo '[{"Path":"notes.txt","Name":"notes.txt","Size":25,"ModTime":"2026-01-02T03:04:05Z","IsDir":false},'
    echo '{"Path":"huge.txt","Name":"huge.txt","Size":'`+itoa(textview.Defaults.MaxBytes+1)+`',"ModTime":"2026-01-02T03:04:05Z","IsDir":false}]'
    exit 0
    ;;
  cat)
    `+cat+`
    exit 0
    ;;
esac
exit 2
`)
}

func itoa(n int64) string {
	var b []byte
	if n == 0 {
		return "0"
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// previewRoot isolates the staging area (and checks that nothing is left).
func previewRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	old := remotePreviewRoot
	remotePreviewRoot = root
	t.Cleanup(func() { remotePreviewRoot = old })
	return root
}

func rootEmpty(t *testing.T, root string) {
	t.Helper()
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("preview staging left behind: %v", names)
	}
}

// ---- stageRemotePreview ------------------------------------------------------------

func TestStageRemotePreviewContentAndCleanup(t *testing.T) { // SIMULATED
	root := previewRoot(t)
	log := filepath.Join(t.TempDir(), "modes")
	script := `case "$1" in
  cat)
    # the staging file is private while the transfer runs
    ls -ld "` + root + `"/twinpane-preview-*/ "` + root + `"/twinpane-preview-*/preview 2>&1 >> "` + log + `"
    printf 'remote notes\nsecond line\n'
    exit 0
    ;;
esac
exit 2
`
	bin := fakeRcloneBin(t, script)
	c := &cloud.Client{Bin: bin}
	loc := cloud.On("box", "notes.txt")
	doc, err := stageRemotePreview(context.Background(), c, loc, 25, textview.Defaults.MaxBytes)
	if err != nil {
		t.Fatalf("stageRemotePreview: %v", err)
	}
	if len(doc.Lines) != 2 || strings.TrimSpace(doc.Lines[0]) != "remote notes" || doc.Truncated {
		t.Fatalf("document: %q truncated=%v", doc.Lines, doc.Truncated)
	}
	rootEmpty(t, root)

	// the directory and the file are private to the user
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the staging mode was not recorded: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("staging listing: %q", b)
	}
	if !strings.HasPrefix(lines[0], "drwx------") {
		t.Fatalf("staging directory is not private: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "-rw-------") {
		t.Fatalf("staging file is not private: %q", lines[1])
	}
}

func TestStageRemotePreviewTruncatesUnknownSize(t *testing.T) { // SIMULATED
	root := previewRoot(t)
	bin := fakeRcloneBin(t, `case "$1" in
  cat) exec yes "0123456789" ;;
esac
exit 2
`)
	c := &cloud.Client{Bin: bin}
	start := time.Now()
	// the provider reported no size (0): the read is measured and cut instead
	doc, err := stageRemotePreview(context.Background(), c, cloud.On("box", "live.log"), 0, 400)
	if err != nil {
		t.Fatalf("stageRemotePreview: %v", err)
	}
	if !doc.Truncated || doc.Read != 400 {
		t.Fatalf("truncated=%v read=%d", doc.Truncated, doc.Read)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("an endless backend took %v", d)
	}
	rootEmpty(t, root)
}

func TestStageRemotePreviewRefusesKnownOversize(t *testing.T) { // SIMULATED
	root := previewRoot(t)
	log := filepath.Join(t.TempDir(), "runs")
	bin := fakeRcloneBin(t, `echo ran >> "`+log+`"
exit 0
`)
	c := &cloud.Client{Bin: bin}
	_, err := stageRemotePreview(context.Background(), c, cloud.On("box", "movie.mkv"), textview.Defaults.MaxBytes+1, textview.Defaults.MaxBytes)
	if err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("a known oversize file must be refused: %v", err)
	}
	rootEmpty(t, root)
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Fatal("nothing may be transferred for a file that is too large")
	}
}

func TestStageRemotePreviewBackendFailureCleansUp(t *testing.T) { // SIMULATED
	root := previewRoot(t)
	bin := fakeRcloneBin(t, `case "$1" in
  cat) echo "cannot download server-side document" >&2; exit 3 ;;
esac
exit 2
`)
	c := &cloud.Client{Bin: bin}
	_, err := stageRemotePreview(context.Background(), c, cloud.On("box", "tracker.gsheet"), 10, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "cannot download") {
		t.Fatalf("the backend's reason must reach the user: %v", err)
	}
	rootEmpty(t, root)
}

func TestStageRemotePreviewCancellationCleansUp(t *testing.T) { // SIMULATED
	root := previewRoot(t)
	pid := filepath.Join(t.TempDir(), "pid")
	bin := fakeRcloneBin(t, `case "$1" in
  cat) echo $$ > "`+pid+`"; exec sleep 60 ;;
esac
exit 2
`)
	c := &cloud.Client{Bin: bin, Grace: 500 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := stageRemotePreview(ctx, c, cloud.On("box", "slow.txt"), 0, 1<<20)
		errc <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(pid); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the simulated rclone never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-errc:
		if cloud.KindOf(err) != cloud.KindCanceled {
			t.Fatalf("cancellation must be reported as such: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelling did not stop the preview")
	}
	rootEmpty(t, root)
}

// ---- the viewer --------------------------------------------------------------------

// remoteApp puts the left panel on a remote served by the given fake rclone.
func remoteApp(t *testing.T, bin string) (*harness, *opsEnv) {
	t.Helper()
	h, env := newOpsApp(t, nil, nil)
	src := &panel.RemoteSource{Client: &cloud.Client{Bin: bin}, Remote: cloud.Remote{Name: "box", Type: "alias"}}
	h.exec(h.a.Panel(0).OpenRemote(src))
	h.until("remote listing", func() bool {
		return h.a.Panel(0).InRemote() && !h.a.Panel(0).Loading()
	})
	return h, env
}

func TestRemotePreviewViewerIsReadOnly(t *testing.T) { // SIMULATED
	root := previewRoot(t)
	h, env := remoteApp(t, listingBin(t, `printf 'remote notes\nsecond line\n'`))
	h.focus("notes.txt")
	h.press("v")
	h.until("preview", func() bool { return h.a.viewer != nil && !h.a.viewer.loading })
	v := h.view()
	for _, want := range []string{"VIEW", "READ-ONLY", "notes.txt", "remote notes", "read-only remote preview", "box:"} {
		if !strings.Contains(v, want) {
			t.Fatalf("remote preview lacks %q:\n%s", want, v)
		}
	}
	if f := h.footer(); strings.Contains(f, "edit") {
		t.Fatalf("editing must not be offered for a remote file: %q", f)
	}
	// editing is refused, and nothing is started
	h.press("e")
	if len(env.execs) != 0 {
		t.Fatalf("an external editor was started for a remote file: %v", env.execs)
	}
	if s := h.status(); !strings.Contains(s, "read-only") {
		t.Fatalf("status after e: %q", s)
	}
	h.press("esc")
	if h.a.viewer != nil {
		t.Fatal("Esc must close the viewer")
	}
	rootEmpty(t, root)
}

func TestRemotePreviewOpensWithEnter(t *testing.T) { // SIMULATED
	root := previewRoot(t)
	h, _ := remoteApp(t, listingBin(t, `printf 'remote notes\nsecond line\n'`))
	h.focus("notes.txt")
	h.press("enter")
	h.until("preview", func() bool { return h.a.viewer != nil && !h.a.viewer.loading })
	if v := h.view(); !strings.Contains(v, "remote notes") || !strings.Contains(v, "read-only remote preview") {
		t.Fatalf("Enter opens the read-only preview:\n%s", v)
	}
	h.press("esc")
	rootEmpty(t, root)
}

func TestRemotePreviewOversizeRefusedBeforeTransfer(t *testing.T) { // SIMULATED
	root := previewRoot(t)
	log := filepath.Join(t.TempDir(), "cats")
	h, _ := remoteApp(t, listingBin(t, `echo "$@" >> "`+log+`"; printf 'x'`))
	h.focus("huge.txt")
	h.press("v")
	h.settle()
	if h.a.viewer != nil {
		t.Fatal("a file beyond the preview limit must not open")
	}
	s := h.reported()
	if !strings.Contains(s, "at most") || !strings.Contains(s, "copy") {
		t.Fatalf("the refusal must explain the limit and the way out: %q", s)
	}
	if h.reportedLevel() != lvlWarn {
		t.Fatalf("the refusal is a warning, level %v", h.reportedLevel())
	}
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Fatalf("the file was transferred anyway: %q", b)
	}
	rootEmpty(t, root)
}

func TestRemotePreviewCancelledWhileLoading(t *testing.T) { // SIMULATED
	root := previewRoot(t)
	pid := filepath.Join(t.TempDir(), "pid")
	h, _ := remoteApp(t, listingBin(t, `echo $$ > "`+pid+`"; exec sleep 60`))
	h.focus("notes.txt")
	h.press("v")
	h.until("transfer started", func() bool { _, err := os.Stat(pid); return err == nil })
	if v := h.view(); !strings.Contains(v, "Reading from box:") {
		t.Fatalf("the loading screen must say where the text comes from:\n%s", v)
	}
	h.press("esc")
	h.until("cancelled", func() bool { return h.a.viewer == nil })
	rootEmpty(t, root)
}

func TestRemotePreviewBackendErrorExplained(t *testing.T) { // SIMULATED
	root := previewRoot(t)
	h, env := remoteApp(t, listingBin(t, `echo "gc: cannot download a Google Docs file" >&2; exit 3`))
	h.focus("notes.txt")
	h.press("v")
	h.until("loaded", func() bool { return h.a.viewer != nil && !h.a.viewer.loading })
	v := h.view()
	if !strings.Contains(v, "Cannot show this file") || !strings.Contains(v, "cannot download") {
		t.Fatalf("the failure must be explained:\n%s", v)
	}
	if f := h.footer(); strings.Contains(f, "edit") {
		t.Fatalf("hints must stay read-only after a failure: %q", f)
	}
	h.press("e")
	if len(env.execs) != 0 {
		t.Fatalf("an editor was started after a failed preview: %v", env.execs)
	}
	h.press("esc")
	rootEmpty(t, root)
}

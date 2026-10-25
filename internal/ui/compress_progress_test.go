package ui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ThatOn3Gu7/TwinPane/internal/compress"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

func testCompressionDialog(t *testing.T) (*harness, *opSession, *compressProgressState, string) {
	t.Helper()
	h, env, _ := p4App(t, nil)
	f, _ := compress.Lookup(compress.TGZ)
	source := filepath.Join(env.src, "a.txt")
	out := filepath.Join(env.dst, "final.tar.gz")
	req := compress.Request{Format: f.ID, Sources: []string{source}, Output: out}
	s, _ := h.a.newSession("Creating TAR.GZ", 0, req.Sources)
	s.to = out
	cp := &compressProgressState{id: s.id, req: req, format: f, started: h.a.now(), progress: ops.Progress{Phase: "starting"}}
	h.a.compressProgress = cp
	h.a.dialog = &dialog{kind: dlgCompressProgress, width: 72}
	h.a.updateCompressionDialog()
	return h, s, cp, out
}

func TestCompressionProgressDialogCoversEverySupportedFormat(t *testing.T) {
	h, _, _ := p4App(t, nil)
	for _, f := range compress.Formats() {
		out := filepath.Join(t.TempDir(), "output"+f.Ext)
		cp := &compressProgressState{
			id: 1, format: f,
			req: compress.Request{Format: f.ID, Output: out}, started: h.a.now(),
			progress: ops.Progress{Phase: "compressing", TotalItems: 1, TotalBytes: 100, DoneBytes: 25},
		}
		h.a.compressProgress = cp
		h.a.dialog = &dialog{kind: dlgCompressProgress, width: 72}
		h.a.updateCompressionDialog()
		v := h.view()
		if !strings.Contains(v, "Format: "+f.Name+" ("+f.Ext+")") || !strings.Contains(v, "25%") {
			t.Errorf("%s progress dialog is incomplete:\n%s", f.Name, v)
		}
	}
}

func TestCompressionProgressDialogShowsAccurateProgress(t *testing.T) {
	h, s, cp, out := testCompressionDialog(t)
	cp.progress = ops.Progress{Phase: "scanning", Current: filepath.Join(filepath.Dir(out), "long-current-name.txt"), TotalBytes: 100, DoneBytes: 80}
	h.a.updateCompressionDialog()
	v := h.view()
	if !strings.Contains(v, "Format: TAR.GZ") || !strings.Contains(v, "final.tar.gz") || !strings.Contains(v, "Scanning selected files") || !strings.Contains(v, "long-current-name.txt") {
		t.Fatalf("progress dialog lacks operation context:\n%s", v)
	}
	if !strings.Contains(v, "working") || strings.Contains(v, "80%") {
		t.Fatalf("scanning progress should be indeterminate:\n%s", v)
	}

	cp.progress = ops.Progress{Phase: "compressing", Current: filepath.Join(filepath.Dir(out), "current.bin"), TotalBytes: 100, DoneBytes: 25}
	h.a.updateCompressionDialog()
	v = h.view()
	if !strings.Contains(v, "Compressing archive") || !strings.Contains(v, "current.bin") || !strings.Contains(v, "25%") {
		t.Fatalf("reliable byte progress is not shown:\n%s", v)
	}
	if pct, ok := compressionPercent(ops.Progress{Phase: "compressing", ByteOnly: true, TotalBytes: 0}); ok || pct != 0 {
		t.Fatalf("unknown byte denominator should be indeterminate: %v %v", pct, ok)
	}
	h.a.finishZip(s, compress.Result{Format: cp.format, Output: out, Canceled: true})
	h.dismissCompression()
}

func TestCompressionCancelRemainsModalUntilCleanupSummary(t *testing.T) {
	h, s, cp, out := testCompressionDialog(t)
	d := h.a.dialog
	h.a.requestCompressionCancel(s.id)
	if !cp.cancelPending || h.a.dialog != d || !strings.Contains(h.view(), "Cancellation requested") {
		t.Fatalf("cancel was not retained as pending:\n%s", h.view())
	}
	res := compress.Result{Format: cp.format, Output: out, Canceled: true}
	h.a.finishZip(s, res)
	v := h.view()
	if !cp.finished || h.a.dialog != d || !strings.Contains(v, "compression canceled") ||
		!strings.Contains(v, "destination was not published") || !strings.Contains(v, "No temporary output remains") {
		t.Fatalf("cancellation summary/cleanup missing:\n%s", v)
	}
	h.dismissCompression()
}

func TestCompressionCleanupFailureIsPersistentAndHonest(t *testing.T) {
	h, s, cp, out := testCompressionDialog(t)
	res := compress.Result{Format: cp.format, Output: out, CleanupErr: errors.New("permission denied")}
	h.a.finishZip(s, res)
	v := h.view()
	// The dialog wraps its text, so the sentence is checked in pieces; the
	// failure itself must be named.
	if !strings.Contains(v, "created with warnings") || !strings.Contains(v, "Cleanup warning: permission denied") ||
		!strings.Contains(v, "temporary cleanup") || !strings.Contains(v, "failed; see the warning below") {
		t.Fatalf("cleanup failure was not retained:\n%s", v)
	}
	if strings.Contains(v, "no temporary output remains") {
		t.Fatalf("dialog claimed failed cleanup succeeded:\n%s", v)
	}
	h.dismissCompression()
}

func TestCompressionFailureDialogWrapsLongErrorAndCanBeDismissed(t *testing.T) {
	h, s, cp, out := testCompressionDialog(t)
	long := strings.Repeat("source read failed because the device returned a detailed diagnostic ", 120)
	res := compress.Result{Format: cp.format, Output: out, Fatal: fmt.Errorf("%s", long)}
	h.a.finishZip(s, res)
	h.view()
	d := h.a.dialog
	if d == nil || d.kind != dlgCompressProgress || !d.scrollable {
		t.Fatalf("long error did not remain in a scrollable final dialog: %v", d)
	}
	wrapped := wrapText("Error: "+long, boxWidth(d.width, h.a.width))
	if len(wrapped) < 3 || !strings.Contains(strings.Join(wrapped, "\n"), "Error:") {
		t.Fatalf("long error was not wrapped: %d lines", len(wrapped))
	}
	h.press("pgdown")
	v := h.view()
	if d.scroll == 0 || !strings.Contains(v, "diagnostic source read failed") {
		t.Fatalf("wrapped error cannot be reached by scrolling:\n%s", v)
	}
	h.dismissCompression()
}

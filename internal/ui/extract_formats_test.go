package ui

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Extraction of TAR / compressed files from the UI (phase 9).

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	for _, n := range []string{"pkg/", "pkg/readme.txt", "pkg/bin/tool"} {
		body, ok := files[n]
		if !ok && !strings.HasSuffix(n, "/") {
			continue
		}
		h := &tar.Header{Name: n, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if strings.HasSuffix(n, "/") {
			h.Typeflag, h.Mode, h.Size = tar.TypeDir, 0o755, 0
		}
		tw.WriteHeader(h)
		tw.Write([]byte(body))
	}
	tw.Close()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(tb.Bytes())
	zw.Close()
	return gz.Bytes()
}

func TestExtractTarGzFromUI(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	os.WriteFile(filepath.Join(env.src, "release.tar.gz"), tarGz(t, map[string]string{"pkg/readme.txt": "hello", "pkg/bin/tool": "x"}), 0o644)
	h.press("ctrl+l")
	h.focus("release.tar.gz")
	h.send(tea.WindowSizeMsg{Width: 220, Height: 30})
	if f := h.footer(); !strings.Contains(f, "x  extract") {
		t.Fatalf("hint: %q", f)
	}
	h.press("x")
	if !h.dialogKind(dlgExtract) {
		t.Fatalf("dialog: %q", h.status())
	}
	v := h.view()
	if !strings.Contains(v, "TAR.GZ") || h.a.dialog.input.Value() != filepath.Join(env.src, "release") {
		t.Fatalf("dialog %q:\n%s", h.a.dialog.input.Value(), v)
	}
	h.press("enter")
	h.idle()
	if b, _ := os.ReadFile(filepath.Join(env.src, "release", "pkg", "readme.txt")); string(b) != "hello" {
		t.Fatalf("extracted: %q (status %q)", b, h.status())
	}
	if !strings.Contains(h.reported(), "Extracted 2 files") {
		t.Fatalf("report: %q", h.reported())
	}
	// again: the existing files are asked about
	h.focus("release.tar.gz")
	h.press("x", "enter")
	h.waitDialog(dlgConflict)
	h.press("s")
	h.waitDialog(dlgConflict)
	h.press("esc")
	h.idle()
	if !strings.Contains(strings.ToLower(h.reported()), "cancel") {
		t.Fatalf("report: %q", h.reported())
	}
	// the menu offers it too
	h.focus("release.tar.gz")
	h.press("M")
	if !strings.Contains(h.view(), "Extract to folder…") {
		t.Fatal("menu entry")
	}
}

func TestDecompressSingleFileFromUI(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte("log line"))
	zw.Close()
	os.WriteFile(filepath.Join(env.src, "app.log.gz"), gz.Bytes(), 0o644)
	h.press("ctrl+l")
	h.focus("app.log.gz")
	h.press("x")
	v := h.view()
	if !h.dialogKind(dlgExtract) || !strings.Contains(v, "Decompress") || !strings.Contains(v, "app.log") || h.a.dialog.input.Value() != env.src {
		t.Fatalf("dialog:\n%s", v)
	}
	// The archive folder is the default, but the destination is still editable.
	h.press("ctrl+u")
	h.typeText(env.dst, false)
	if got := h.a.dialog.input.Value(); got != env.dst {
		t.Fatalf("edited destination %q", got)
	}
	h.press("enter")
	h.idle()
	if b, _ := os.ReadFile(filepath.Join(env.dst, "app.log")); string(b) != "log line" {
		t.Fatalf("decompressed: %q (status %q)", b, h.status())
	}
}

func TestExtractNeeds7zipAndCorrupt(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	for _, n := range []string{"7zz", "7z", "7za", "7zr"} {
		env.hide[n] = true
	}
	os.WriteFile(filepath.Join(env.src, "pics.7z"), append([]byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}, make([]byte, 64)...), 0o644)
	os.WriteFile(filepath.Join(env.src, "cut.tar.gz"), []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 3, 0xff, 0xff}, 0o644)
	h.press("ctrl+l")
	h.focus("pics.7z")
	h.press("x")
	if h.a.dialog != nil || !strings.Contains(h.status(), "needs 7-Zip") {
		t.Fatalf("7z without 7-Zip: %q", h.status())
	}
	env.env["TERMUX_VERSION"] = "1"
	h.a.platCache = nil
	h.press("x")
	if !strings.Contains(h.status(), "pkg install 7zip") {
		t.Fatalf("Termux hint: %q", h.status())
	}
	// a damaged archive: offered, then a clear report
	h.focus("cut.tar.gz")
	h.press("x", "enter")
	h.idle()
	if !strings.Contains(h.reported(), "damaged") || h.reportedLevel() != lvlErr {
		t.Fatalf("damaged: %q (level %v)", h.reported(), h.reportedLevel())
	}
}

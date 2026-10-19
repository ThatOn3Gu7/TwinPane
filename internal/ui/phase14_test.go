package ui

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTARGzip(t *testing.T, name string, entries map[string]string) {
	t.Helper()
	var tarData bytes.Buffer
	tw := tar.NewWriter(&tarData)
	for _, n := range []string{"docs/", "docs/readme.txt", "root.txt"} {
		body, regular := entries[n]
		h := &tar.Header{Name: n, Mode: 0o644, ModTime: time.Unix(1_700_000_000, 0), Format: tar.FormatPAX}
		if strings.HasSuffix(n, "/") {
			h.Typeflag = tar.TypeDir
		} else {
			h.Typeflag = tar.TypeReg
			h.Size = int64(len(body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if regular {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(tarData.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, gz.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTARArchiveBrowseAndSelectiveExtraction(t *testing.T) {
	h, env, _ := p4App(t, nil)
	archivePath := filepath.Join(env.src, "bundle.tar.gz")
	writeTARGzip(t, archivePath, map[string]string{
		"docs/readme.txt": "read from the virtual TAR\n",
		"root.txt":        "not selected\n",
	})
	h.exec(h.a.panels[0].RefreshFocus("bundle.tar.gz"))
	h.settle()
	h.focus("bundle.tar.gz")
	if f := h.footer(); !strings.Contains(f, "browse archive") || !strings.Contains(f, "extract") {
		t.Fatalf("TAR browse/extract hints missing: %q", f)
	}
	h.press("enter")
	h.until("TAR archive", func() bool { return h.a.ActivePanel().InArchive() })
	p := h.a.ActivePanel()
	if p.Archive().Format.Name() != "TAR.GZ" || !strings.Contains(h.status(), "read-only") {
		t.Fatalf("TAR archive was not clearly identified: %q", h.status())
	}
	if got := strings.Join(entryNames(p), ","); got != "..,docs,root.txt" {
		t.Fatalf("TAR root listing %q", got)
	}
	h.focus("docs")
	h.press("enter")
	h.settle()
	if got := strings.Join(entryNames(p), ","); got != "..,readme.txt" || p.ArchiveInner() != "docs" {
		t.Fatalf("nested TAR listing %q at %q", got, p.ArchiveInner())
	}
	if _, err := os.Stat(filepath.Join(env.dst, "docs")); !os.IsNotExist(err) {
		t.Fatalf("browsing extracted content: %v", err)
	}

	h.focus("readme.txt")
	h.press("x")
	if !h.dialogKind(dlgExtract) || h.a.dialog.input.Value() != env.src {
		t.Fatalf("selective TAR extraction prompt: %v", h.a.dialog)
	}
	h.press("enter")
	h.idle()
	if got := readFile(t, filepath.Join(env.src, "readme.txt")); got != "read from the virtual TAR\n" {
		t.Fatalf("selected TAR entry content %q", got)
	}
	if _, err := os.Stat(filepath.Join(env.src, "root.txt")); !os.IsNotExist(err) {
		t.Fatalf("unselected TAR entry was extracted: %v", err)
	}
	if !p.InArchive() {
		t.Fatal("selective extraction changed the browsing panel")
	}

	h.press("X")
	wholeDest := filepath.Join(env.src, "bundle")
	if !h.dialogKind(dlgExtract) || h.a.dialog.input.Value() != wholeDest {
		t.Fatalf("whole TAR extraction default folder should strip .tar.gz: got %q", h.a.dialog.input.Value())
	}
	h.press("enter")
	h.idle()
	if got := readFile(t, filepath.Join(wholeDest, "docs", "readme.txt")); got != "read from the virtual TAR\n" {
		t.Fatalf("whole TAR extraction content %q", got)
	}
	if got := readFile(t, filepath.Join(wholeDest, "root.txt")); got != "not selected\n" {
		t.Fatalf("whole TAR extraction omitted root entry: %q", got)
	}
}

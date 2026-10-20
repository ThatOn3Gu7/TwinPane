package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/cloud"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
)

// Read-only preview of remote text files.
//
// The viewer already reads bounded, read-only text (local files and archive
// entries); remote files join it the same way. The bytes are streamed by
// rclone into a private staging directory (mode 0700, file 0600, created in
// the operating system's temporary area — never in the folder the user is
// browsing) and removed as soon as the document is in memory, so no preview
// data outlives the read, is left behind on cancellation, or shows up in a
// panel listing. Editing, open-with and every other write path stay
// unavailable for remote files.

// remotePreviewRoot is the parent of the private staging directories ("" =
// the OS temporary directory). Tests point it at their own folder to check
// that nothing is left behind.
var remotePreviewRoot string

// remotePreviewFile is the staged file's name inside its private directory.
const remotePreviewFile = "preview"

// openRemoteViewer shows a file of an rclone remote in the read-only viewer.
func (a *App) openRemoteViewer(p *panel.Panel, e fsys.Entry) tea.Cmd {
	src := p.Source()
	if src == nil || src.Client == nil {
		a.setStatus("cannot read "+fsys.SanitizeName(e.Name)+": the remote is not available", true)
		return nil
	}
	loc, ok := cloud.Parse(e.Path)
	if !ok {
		a.setStatus("cannot read "+fsys.SanitizeName(e.Name)+": not a remote path", true)
		return nil
	}
	// Refuse before transferring anything when the provider told us the size
	// and it is beyond what the viewer shows.
	if max := textview.Defaults.MaxBytes; e.Size > max {
		a.setStatusLevel(fmt.Sprintf("%s is %s: the preview shows at most %s — copy it to a local panel (c) to read all of it",
			fsys.SanitizeName(e.Name), fsys.HumanSize(e.Size), fsys.HumanSize(max)), lvlWarn)
		return nil
	}
	a.opSeq++
	ctx, cancel := context.WithCancel(context.Background())
	v := &viewer{
		seq: a.opSeq, name: e.Name, where: loc.Display(), loading: true,
		wrap: true, numbers: true, mcur: -1,
		remote: true, remoteLoc: loc, remoteSize: e.Size, loadCancel: cancel,
	}
	a.viewer = v
	a.setStatus("reading "+fsys.SanitizeName(e.Name)+" from "+src.Remote.Name+":…", false)
	seq, client, size := v.seq, src.Client, e.Size
	return func() tea.Msg {
		doc, err := stageRemotePreview(ctx, client, loc, size, textview.Defaults.MaxBytes)
		return viewLoadedMsg{seq: seq, doc: doc, err: err}
	}
}

// remoteLabel names the remote a preview is being read from.
func remoteLabel(v *viewer) string {
	if v == nil || v.remoteLoc.Remote == "" {
		return "the remote"
	}
	return v.remoteLoc.Remote + ":"
}

// stageRemotePreview copies at most maxBytes of loc into a private temporary
// file and loads it as a bounded text document. The staging directory is
// removed before this returns, whether the read succeeded, failed or was
// cancelled.
func stageRemotePreview(ctx context.Context, c *cloud.Client, loc cloud.Path, size, maxBytes int64) (*textview.Doc, error) {
	if c == nil {
		return nil, cloud.ErrNotInstalled
	}
	if size > maxBytes {
		return nil, fmt.Errorf("%s is %s: the preview shows at most %s", loc.Display(), fsys.HumanSize(size), fsys.HumanSize(maxBytes))
	}
	dir, err := os.MkdirTemp(remotePreviewRoot, "twinpane-preview-")
	if err != nil {
		return nil, fmt.Errorf("cannot stage the preview: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, remotePreviewFile)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot stage the preview: %w", err)
	}
	defer f.Close()

	n, truncated, err := c.ReadFile(ctx, loc, f, maxBytes)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("cannot read the staged preview: %w", err)
	}
	// The provider's size is trusted only when it reported one: files whose
	// size is unknown (0 on several backends) are measured as they are read.
	total := size
	if total <= 0 {
		total = n
	}
	doc, err := textview.Load(f, total, textview.Defaults)
	if doc != nil && truncated {
		doc.Truncated = true
	}
	return doc, err
}

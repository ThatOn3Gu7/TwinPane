package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/compress"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

const compressProgressInterval = 100 * time.Millisecond

// compressProgressState is owned by the retained modal from the moment the
// backend starts until the user dismisses its final summary.
type compressProgressState struct {
	id            int
	req           compress.Request
	format        compress.Format
	started       time.Time
	finishedAt    time.Time
	progress      ops.Progress
	result        compress.Result
	frame         int
	cancelPending bool
	finished      bool
}

type compressPulseMsg struct{ id int }

func (a *App) compressPulse(id int) tea.Cmd {
	return tea.Tick(compressProgressInterval, func(time.Time) tea.Msg { return compressPulseMsg{id: id} })
}

func (a *App) updateCompressionDialog() {
	cp, d := a.compressProgress, a.dialog
	if cp == nil || d == nil || d.kind != dlgCompressProgress {
		return
	}
	verb := "archive"
	if cp.format.Kind == compress.Single {
		verb = "file"
	}
	if cp.finished {
		switch {
		case cp.result.Fatal != nil:
			d.title = cp.format.Name + " compression failed"
		case cp.result.Canceled:
			d.title = cp.format.Name + " compression canceled"
		case len(cp.result.Errors) > 0 || cp.result.CleanupErr != nil:
			d.title = cp.format.Name + " created with warnings"
		default:
			d.title = cp.format.Name + " compression complete"
		}
	} else {
		d.title = "Compressing " + cp.format.Name
	}
	d.width = 72
	d.body = []dline{
		text("Format: " + cp.format.Name + " (" + cp.format.Ext + ")"),
		path("Destination: " + cp.req.Output),
		blank(),
	}
	if !cp.finished {
		status := "Preparing " + cp.format.Name + "…"
		switch cp.progress.Phase {
		case "scanning":
			status = "Scanning selected files…"
		case "compressing":
			status = "Compressing " + verb + "…"
		}
		d.body = append(d.body, text("Status: "+status))
		if cp.progress.Current != "" {
			d.body = append(d.body, path("Current item: "+cp.progress.Current))
		}
		d.body = append(d.body, compressProgressBar(a, cp))
		if cp.cancelPending {
			d.body = append(d.body, warn("Cancellation requested — waiting for the current safe stopping point and temporary-file cleanup."))
		} else {
			d.body = append(d.body, hint("Cancel is safe; cleanup finishes before this summary can be dismissed."))
		}
		cancelLabel := "Cancel (Esc)"
		if cp.cancelPending {
			cancelLabel = "Cancellation pending"
		}
		d.buttons = []button{{label: cancelLabel, danger: true, run: func(a *App) tea.Cmd { return a.requestCompressionCancel(cp.id) }}}
		d.onEsc = func(a *App) tea.Cmd { return a.requestCompressionCancel(cp.id) }
		if cp.cancelPending {
			d.footer = "Cancellation pending · the destination is not published until success"
		} else {
			d.footer = "Esc cancel · progress continues in the background only until it is safely stopped"
		}
		return
	}

	res := cp.result
	elapsed := cp.finishedAt.Sub(cp.started)
	if elapsed < 0 {
		elapsed = 0
	}
	state := "Completed"
	switch {
	case res.Fatal != nil:
		state = "Failed"
	case res.Canceled:
		state = "Canceled"
	case len(res.Errors) > 0 || res.CleanupErr != nil:
		state = "Completed with warnings"
	}
	inputLabel, inputBytes := "Input data: ", res.Bytes
	if !res.Created() {
		inputLabel, inputBytes = "Input processed before stop: ", cp.progress.DoneBytes
	}
	d.body = append(d.body,
		text("Status: "+state),
		text("Elapsed: "+elapsed.Round(100*time.Millisecond).String()),
		text(fmt.Sprintf("Included: %d files · %d folders · %d links", res.Files, res.Dirs, res.Links)),
		text(inputLabel+fsys.HumanSize(inputBytes)),
	)
	if cp.format.Kind == compress.Single && res.Created() {
		d.body = append(d.body, text("Compressed file size: "+fsys.HumanSize(res.Size)))
	} else if res.Created() {
		d.body = append(d.body, text("Final archive size: "+fsys.HumanSize(res.Size)))
	}
	if res.Fatal != nil {
		d.body = append(d.body, blank(), warn("Error: "+res.Fatal.Error()))
	}
	if len(res.Errors) > 0 {
		d.body = append(d.body, blank(), warn(fmt.Sprintf("%d item(s) could not be included:", len(res.Errors))))
		for _, item := range res.Errors[:min(len(res.Errors), 5)] {
			d.body = append(d.body, warn(item.Error()))
		}
		if len(res.Errors) > 5 {
			d.body = append(d.body, hint(fmt.Sprintf("… %d additional item error(s) omitted from this summary.", len(res.Errors)-5)))
		}
	}
	if len(res.Excluded) > 0 {
		d.body = append(d.body, hint(fmt.Sprintf("%d output or temporary item(s) were excluded.", len(res.Excluded))))
	}
	if res.Canceled {
		d.body = append(d.body, blank(), warn("The destination was not published; any existing file was left unchanged."))
	}
	if res.Created() {
		if res.CleanupErr == nil {
			d.body = append(d.body, hint("The completed output was published; no temporary output remains."))
		} else {
			d.body = append(d.body, warn("The destination was published, but operation-owned temporary cleanup failed; see the warning below."))
		}
	} else if res.CleanupErr == nil {
		d.body = append(d.body, hint("No temporary output remains; any existing destination was preserved."))
	}
	if res.CleanupErr != nil {
		d.body = append(d.body, warn("Cleanup warning: "+res.CleanupErr.Error()))
	}
	d.buttons = []button{{label: "Dismiss (Esc)", run: func(a *App) tea.Cmd { a.dismissCompressionDialog(cp.id); return nil }}}
	d.onEsc = func(a *App) tea.Cmd { a.dismissCompressionDialog(cp.id); return nil }
	d.footer = "Enter/Esc dismiss · ↑↓ / PgUp/PgDn scroll"
}

func compressProgressBar(a *App, cp *compressProgressState) dline {
	p := cp.progress
	percent, reliable := compressionPercent(p)
	if cp.finished && cp.result.Created() {
		percent, reliable = 100, true
	}
	width := max(3, min(32, boxWidth(72, a.width)-18))
	var bar string
	if reliable {
		filled := int(float64(width) * percent / 100)
		if percent >= 100 {
			filled = width
		}
		bar = a.theme.ProgressFill.Render(strings.Repeat("█", filled)) +
			a.theme.ProgressEmpty.Render(strings.Repeat("░", width-filled))
		return dline{kind: lnRaw, text: "Progress: " + bar + fmt.Sprintf(" %3.0f%%", percent)}
	}
	cells := []rune(strings.Repeat("░", width))
	marker := min(4, width)
	start := cp.frame % max(1, width-marker+1)
	for i := 0; i < marker; i++ {
		cells[start+i] = '▓'
	}
	bar = a.theme.ProgressEmpty.Render(string(cells[:start])) +
		a.theme.ProgressFill.Render(string(cells[start:start+marker])) +
		a.theme.ProgressEmpty.Render(string(cells[start+marker:]))
	return dline{kind: lnRaw, text: "Progress: " + bar + " working"}
}

// compressionPercent only reports a percentage during the backend's
// compression phase and only when the backend exposes a known denominator.
func compressionPercent(p ops.Progress) (float64, bool) {
	if p.Phase != "compressing" {
		return 0, false
	}
	if p.ByteOnly {
		if p.TotalBytes <= 0 {
			return 0, false
		}
		return p.Fraction() * 100, true
	}
	if p.TotalBytes > 0 || p.TotalItems > 0 {
		return p.Fraction() * 100, true
	}
	return 0, false
}

func (a *App) requestCompressionCancel(id int) tea.Cmd {
	cp := a.compressProgress
	if cp == nil || cp.id != id {
		return nil
	}
	if cp.finished {
		a.dismissCompressionDialog(id)
		return nil
	}
	if cp.cancelPending {
		return nil
	}
	if a.cancelTask(id) {
		cp.cancelPending = true
		a.updateCompressionDialog()
	}
	return nil
}

func (a *App) dismissCompressionDialog(id int) {
	if cp := a.compressProgress; cp != nil && cp.id == id && cp.finished {
		a.compressProgress = nil
		if a.dialog != nil && a.dialog.kind == dlgCompressProgress {
			a.dialog = nil
		}
	}
}

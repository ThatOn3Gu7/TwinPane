package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// Choosing between the background and a live progress screen.
//
// Small operations keep their old behaviour: they start as soon as the user
// confirms, run in the background, and report through the status line and the
// task list. Only operations that are clearly going to take a while ask how
// they should run, so the everyday copy of a couple of files never gains an
// extra dialog.
//
// "Large enough" is deliberately simple and deterministic:
//
//   - at least 256 MiB of regular files (largeOpBytes), or
//   - at least 500 entries in total (largeOpItems),
//
// measured by ops.EstimateTransfer for a copy or move and by the delete plan's
// own counts for a delete. The walk behind the transfer estimate stops after
// ops.EstimateEntryCap entries, so a huge tree is caught early instead of being
// measured for minutes. Unknown sizes (for example a remote location) simply
// count as small: no prompt, exactly as before.

const (
	// largeOpBytes is the byte size from which an operation is worth a choice.
	largeOpBytes = 256 << 20 // 256 MiB
	// largeOpItems is the entry count from which an operation is worth a
	// choice. It catches many small files, where the byte count says little.
	largeOpItems = 500
)

// opMode is how a running operation reports itself.
type opMode int

const (
	// modeBackground runs the task in the task list only. It is the default:
	// small operations never ask and never take over the screen.
	modeBackground opMode = iota
	// modeLive opens the live progress screen for the operation.
	modeLive
)

// largeOp reports whether an estimate is big enough to offer the choice.
func largeOp(est ops.Estimate) bool {
	return est.Bytes >= largeOpBytes || est.Items() >= largeOpItems
}

// estimateText describes an estimate for the choice dialog.
func estimateText(est ops.Estimate) string {
	var parts []string
	if est.Files > 0 {
		parts = append(parts, plural(est.Files, "file"))
	}
	if est.Dirs > 0 {
		parts = append(parts, plural(est.Dirs, "folder"))
	}
	if est.Bytes > 0 {
		parts = append(parts, fsys.HumanSize(est.Bytes))
	}
	if len(parts) == 0 {
		return "This operation may take a while."
	}
	text := "About " + strings.Join(parts, ", ") + "."
	if est.Truncated {
		text += " The real totals are larger."
	}
	return text
}

// confirmTaskMode runs start and decides how the operation reports itself.
// Small operations start immediately, without a dialog, which keeps the
// behaviour of ordinary copies and deletes unchanged.
func (a *App) confirmTaskMode(s *opSession, est ops.Estimate, start func() tea.Cmd) tea.Cmd {
	if s == nil {
		return start()
	}
	s.est = est
	if !largeOp(est) {
		s.mode = modeBackground
		return start()
	}
	a.modeChoiceDialog(s, est, start)
	return nil
}

// runStarted finishes an operation start: it opens the live screen when the
// user chose to watch it.
func (a *App) runStarted(s *opSession) tea.Cmd {
	if s != nil && s.mode == modeLive {
		a.watchTask(s)
	}
	return a.startCmds(s)
}

// modeChoiceDialog asks whether a large operation should run in the background
// or on the live progress screen. Both answers run the operation; the question
// is only where its progress is shown.
func (a *App) modeChoiceDialog(s *opSession, est ops.Estimate, start func() tea.Cmd) {
	verb := "Operation"
	switch {
	case s.isDelete && s.plan.UseTrash:
		verb = "Move to trash"
	case s.isDelete:
		verb = "Delete"
	case s.kind != ops.Copy && s.kind != ops.Move:
		verb = capitalize(s.kind.String())
	case s.kind == ops.Move:
		verb = "Move"
	default:
		verb = "Copy"
	}
	d := &dialog{kind: dlgConfirm, title: verb + " is large — watch its progress?"}
	d.body = append(d.body,
		text(estimateText(est)),
		blank(),
		text("Running in the background leaves the panels usable and shows a small notification."),
		text("Watching shows a full progress screen you can still leave at any time."),
	)
	watch := func(a *App) tea.Cmd {
		a.dialog = nil
		s.mode = modeLive
		return start()
	}
	background := func(a *App) tea.Cmd {
		a.dialog = nil
		s.mode = modeBackground
		cmd := start()
		return tea.Batch(cmd, a.notifyBackground(s))
	}
	d.buttons = []button{
		{label: "Stay and watch (Enter)", run: watch},
		{label: "Run in background (b)", key: "b", run: background},
	}
	// Esc is a way out that cannot lose work: it picks the watching default.
	d.onEsc = watch
	d.footer = "Enter or Esc watch the progress · b runs it in the background"
	a.dialog = d
}

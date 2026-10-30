package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// focus_dest.go is the focus-mode copy/move destination chooser. Two panes
// make "the other panel" an obvious destination; one pane does not, so the
// destination becomes an explicit choice: the hidden panel's folder first
// (the two-pane default, unchanged), then the destinations used earlier in
// this session. It is only a picker: the transfer itself still runs through
// the ordinary validation, confirmation, conflict and task machinery.

// destLoc is one candidate destination. Placement is derived from current
// panel state at pick time, so a destination stays valid when a panel moves.
type destLoc struct {
	path  string
	label string // where the candidate comes from ("other panel", "recent")
	seq   int    // insertion order, newest first in the list
}

// destLabel renders a destination for the chooser list.
func destLabel(l destLoc) string {
	if s := shortPath(l.path); s != "" {
		return s
	}
	return l.path
}

// noteDest records a destination that a transfer actually used.
func (a *App) noteDest(path, label string) {
	if path == "" {
		return
	}
	path = filepath.Clean(path)
	for i, r := range a.recents {
		if r.path == path {
			a.destSeq++
			a.recents[i].seq, a.recents[i].label = a.destSeq, label
			return
		}
	}
	a.destSeq++
	a.recents = append(a.recents, destLoc{path: path, label: label, seq: a.destSeq})
	if len(a.recents) > maxRecents {
		a.recents = a.recents[len(a.recents)-maxRecents:]
	}
}

// maxRecents bounds the destination list kept for the chooser.
const maxRecents = 6

// recentDests returns the recorded destinations, most recently used first.
func (a *App) recentDests() []destLoc {
	out := append([]destLoc(nil), a.recents...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].seq > out[j].seq })
	return out
}

// destChoices builds the candidate list for a transfer from srcIdx.
func (a *App) destChoices(srcIdx int) []destLoc {
	var out []destLoc
	seen := map[string]bool{}
	src := ""
	if srcIdx >= 0 && srcIdx < len(a.panels) && a.panels[srcIdx] != nil {
		src = a.panels[srcIdx].Path()
	}
	add := func(path, label string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if seen[path] || path == src {
			return
		}
		seen[path] = true
		out = append(out, destLoc{path: path, label: label})
	}
	if other := 1 - srcIdx; other >= 0 && other < len(a.panels) && a.panels[other] != nil {
		p := a.panels[other]
		if !p.InRemote() && !p.InArchive() {
			add(p.Path(), "other panel")
		}
	}
	for _, r := range a.recentDests() {
		add(r.path, "recent")
	}
	if home := fsys.HomeDir(); home != "" {
		add(home, "home")
	}
	return out
}

// beginFocusTransfer is beginTransfer's focus-mode branch: the destination is
// chosen first, then the transfer proceeds exactly as in two-pane mode.
func (a *App) beginFocusTransfer(kind ops.Kind) tea.Cmd {
	srcIdx := a.active
	p := a.panels[srcIdx]
	targets := p.Targets()
	if len(targets) == 0 {
		a.setStatus(fmt.Sprintf("nothing to %s: put the cursor on an item or select items with Space", kind), true)
		return nil
	}
	choices := a.destChoices(srcIdx)
	if len(choices) == 0 {
		a.setStatus("no destination available: open another folder in the hidden panel or use the two-pane layout", true)
		return nil
	}
	d := &dialog{kind: dlgDest, title: fmt.Sprintf("%s %s to…", capitalize(kind.String()), describe(targets)), width: 64}
	d.body = []dline{
		text("One pane is visible, so pick where it goes. The hidden panel is still updated afterwards."),
		blank(),
	}
	d.body = append(d.body, nameList(targets)...)
	d.list = make([]listItem, len(choices))
	for i, c := range choices {
		d.list[i] = listItem{label: destLabel(c), detail: c.label}
	}
	d.onPick = func(app *App, i int) tea.Cmd {
		app.dialog = nil
		return app.beginTransferTo(kind, srcIdx, choices[i].path)
	}
	d.onEsc = func(app *App) tea.Cmd {
		app.dialog = nil
		app.noteResult(lvlWarn, capitalize(kind.String())+" cancelled — nothing was transferred",
			capitalize(kind.String())+" cancelled", "nothing was transferred")
		return nil
	}
	d.footer = "↑↓ choose · Enter confirm · Esc cancel"
	a.dialog = d
	return nil
}

// beginTransferTo validates a transfer to an explicitly chosen destination.
// It mirrors the two-pane path so the confirmation, conflict handling and task
// bookkeeping are identical.
func (a *App) beginTransferTo(kind ops.Kind, srcIdx int, destDir string) tea.Cmd {
	if srcIdx < 0 || srcIdx >= len(a.panels) || a.panels[srcIdx] == nil {
		return nil
	}
	src := a.panels[srcIdx]
	targets := src.Targets()
	if len(targets) == 0 {
		a.setStatus(fmt.Sprintf("nothing to %s: put the cursor on an item or select items with Space", kind), true)
		return nil
	}
	destDir = filepath.Clean(destDir)
	if fi, err := os.Stat(destDir); err != nil || !fi.IsDir() {
		msg := "the destination is not a folder any more"
		if err != nil {
			msg = errText(err)
		}
		a.setStatus("cannot "+kind.String()+" to "+destLabel(destLoc{path: destDir})+": "+msg, true)
		return nil
	}
	paths := pathsOf(targets)
	req := ops.Request{Kind: kind, Sources: append([]string(nil), paths...), DestDir: destDir}
	s, _ := a.newSession("Checking "+kind.String(), srcIdx, paths)
	s.kind, s.to = kind, destDir
	s.dstPanel = a.panelShowing(destDir)
	s.dstChosen = true
	s.state = taskPlanning
	s.prog = ops.Progress{Phase: "validating", TotalItems: len(paths)}
	a.setStatus("Checking "+kind.String()+" paths…", false)
	targets = append([]fsys.Entry(nil), targets...)
	go func() {
		err := ops.Validate(req)
		est := ops.EstimateTransfer(req)
		s.events <- opTransferValidatedMsg{s.id, req, targets, est, err}
	}()
	return a.startCmds(s)
}

// panelShowing returns the panel index currently showing dir, or -1.
func (a *App) panelShowing(dir string) int {
	dir = filepath.Clean(dir)
	for i, p := range a.panels {
		if p == nil || p.InRemote() || p.InArchive() {
			continue
		}
		if filepath.Clean(p.Path()) == dir {
			return i
		}
	}
	return -1
}

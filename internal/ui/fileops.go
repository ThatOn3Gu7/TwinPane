package ui

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// progressDelay is how long an operation runs before the progress box is
// shown (quick operations finish without flicker).
var progressDelay = 300 * time.Millisecond

// listCap is the maximum number of paths listed in confirmation dialogs.
const listCap = 5

// ---- messages from running operations ---------------------------------------

type opProgressMsg struct {
	id int
	p  ops.Progress
}

type opConflictMsg struct {
	id    int
	c     ops.Conflict
	reply chan ops.Decision
}

type opLowSpaceMsg struct {
	id         int
	need, free uint64
	reply      chan bool
}

type opTransferDoneMsg struct {
	id  int
	res ops.Result
}

type opDeleteDoneMsg struct {
	id  int
	res ops.DeleteResult
}

type opRevealMsg struct{ id int }

type deletePlannedMsg struct {
	id   int
	plan ops.DeletePlan
	err  error
}

// opSession is a running copy / move / delete.
type opSession struct {
	id        int
	title     string
	cancel    context.CancelFunc
	events    chan tea.Msg
	prog      ops.Progress
	canceling bool
	visible   bool

	srcPanel int
	names    map[string]bool // top-level names in the source panel
	first    string          // first top-level name (focused in the destination)
	kind     ops.Kind
	isDelete bool
	plan     ops.DeletePlan
}

func (s *opSession) listen() tea.Cmd {
	ch := s.events
	return func() tea.Msg { return <-ch }
}

// planState is an in-progress delete measurement (cancellable with Esc).
type planState struct {
	id      int
	cancel  context.CancelFunc
	panel   int
	targets []fsys.Entry
}

// busy reports whether an operation or delete scan is running.
func (a *App) busy() bool { return a.op != nil || a.planning != nil }

// busyKey handles keys while an operation runs: only cancel is accepted.
func (a *App) busyKey(msg tea.KeyMsg) tea.Cmd {
	s := msg.String()
	if s != "esc" && s != "ctrl+c" {
		return nil
	}
	if a.planning != nil {
		a.planning.cancel()
		a.planning = nil
		a.setStatusLevel("Delete cancelled — nothing was deleted", lvlWarn)
		return nil
	}
	if a.op != nil && !a.op.canceling {
		a.op.canceling = true
		a.op.cancel()
		a.op.visible = true
	}
	return nil
}

func (a *App) newSession(title string, srcPanel int, targets []string) (*opSession, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	a.opSeq++
	s := &opSession{
		id: a.opSeq, title: title, cancel: cancel,
		events:   make(chan tea.Msg, 32),
		srcPanel: srcPanel, names: map[string]bool{},
	}
	for i, p := range targets {
		n := filepath.Base(p)
		s.names[n] = true
		if i == 0 {
			s.first = n
		}
	}
	a.op = s
	return s, ctx
}

func (a *App) hooks(ctx context.Context, s *opSession) ops.Hooks {
	return ops.Hooks{
		Progress: func(p ops.Progress) {
			select { // never block the engine for a progress update
			case s.events <- opProgressMsg{s.id, p}:
			default:
			}
		},
		Conflict: func(c ops.Conflict) ops.Decision {
			reply := make(chan ops.Decision, 1)
			select {
			case s.events <- opConflictMsg{s.id, c, reply}:
			case <-ctx.Done():
				return ops.Decision{Action: ops.Cancel}
			}
			select {
			case d := <-reply:
				return d
			case <-ctx.Done():
				return ops.Decision{Action: ops.Cancel}
			}
		},
		LowSpace: func(need, free uint64) bool {
			reply := make(chan bool, 1)
			select {
			case s.events <- opLowSpaceMsg{s.id, need, free, reply}:
			case <-ctx.Done():
				return false
			}
			select {
			case ok := <-reply:
				return ok
			case <-ctx.Done():
				return false
			}
		},
	}
}

func (a *App) startCmds(s *opSession) tea.Cmd {
	id := s.id
	return tea.Batch(s.listen(), tea.Tick(progressDelay, func(time.Time) tea.Msg { return opRevealMsg{id} }))
}

// handleOpMsg processes messages from operations. ok=false if msg isn't one.
func (a *App) handleOpMsg(msg tea.Msg) (tea.Cmd, bool) {
	cur := func(id int) bool { return a.op != nil && a.op.id == id }
	switch m := msg.(type) {
	case opRevealMsg:
		if cur(m.id) {
			a.op.visible = true
		}
		return nil, true
	case opProgressMsg:
		if !cur(m.id) {
			return nil, true
		}
		a.op.prog = m.p
		return a.op.listen(), true
	case opConflictMsg:
		if !cur(m.id) {
			m.reply <- ops.Decision{Action: ops.Cancel}
			return nil, true
		}
		a.conflictDialog(a.op, m)
		return a.op.listen(), true
	case opLowSpaceMsg:
		if !cur(m.id) {
			m.reply <- false
			return nil, true
		}
		a.lowSpaceDialog(m)
		return a.op.listen(), true
	case opTransferDoneMsg:
		if !cur(m.id) {
			return nil, true
		}
		return a.finishTransfer(a.op, m.res), true
	case opDeleteDoneMsg:
		if !cur(m.id) {
			return nil, true
		}
		return a.finishDelete(a.op, m.res), true
	case opExtractDoneMsg:
		if !cur(m.id) {
			return nil, true
		}
		return a.finishExtract(a.op, m.res), true
	case opZipDoneMsg:
		if !cur(m.id) {
			return nil, true
		}
		return a.finishZip(a.op, m.res), true
	case opCloudDoneMsg:
		if !cur(m.id) {
			return nil, true
		}
		return a.finishCloudOp(a.op, m), true
	case opCloudDeleteDoneMsg:
		if !cur(m.id) {
			return nil, true
		}
		return a.finishCloudDelete(a.op, m.res), true
	case deletePlannedMsg:
		if a.planning == nil || a.planning.id != m.id {
			return nil, true // cancelled or superseded
		}
		st := a.planning
		a.planning = nil
		if m.err != nil {
			a.setStatus("cannot delete: "+errText(m.err), true)
			return nil, true
		}
		a.setStatus("", false)
		a.deleteDialog(m.plan, st)
		return nil, true
	}
	return nil, false
}

// ---- copy / move -------------------------------------------------------------

func (a *App) beginTransfer(kind ops.Kind) tea.Cmd {
	what := map[ops.Kind]string{ops.Copy: "copy", ops.Move: "move"}[kind]
	if kind == ops.Move && a.refuseInArchive("Moving") {
		return nil
	}
	if a.ActivePanel().InArchive() {
		return a.beginExtract(false)
	}
	if a.refuseArchiveDest(what) {
		return nil
	}
	if a.ActivePanel().InRemote() || a.panels[1-a.active].InRemote() {
		return a.beginCloudTransfer(kind) // cloud.go
	}
	srcIdx := a.active
	src, dst := a.panels[srcIdx], a.panels[1-srcIdx]
	targets := src.Targets()
	if len(targets) == 0 {
		a.setStatus(fmt.Sprintf("nothing to %s: put the cursor on an item or select items with Space", kind), true)
		return nil
	}
	paths := pathsOf(targets)
	req := ops.Request{Kind: kind, Sources: paths, DestDir: dst.Path()}
	if err := ops.Validate(req); err != nil {
		a.setStatus(fmt.Sprintf("cannot %s: %s", kind, errText(err)), true)
		return nil
	}
	verb := capitalize(kind.String())
	d := &dialog{kind: dlgConfirm, title: fmt.Sprintf("%s %s?", verb, describe(targets))}
	d.body = append(d.body, text("From:"), path(src.Path()), text("To:"), path(dst.Path()), blank())
	d.body = append(d.body, nameList(targets)...)
	if kind == ops.Move {
		d.body = append(d.body, blank(), hint("Items are removed from the source only after they have been moved (across filesystems: copied and verified first)."))
	}
	d.body = append(d.body, blank(), hint("Existing items in the destination are never overwritten without asking."))
	d.buttons = []button{
		{label: verb + " (Enter)", run: func(a *App) tea.Cmd { a.dialog = nil; return a.startTransfer(kind, srcIdx, req) }},
		{label: "Cancel (Esc)", run: func(a *App) tea.Cmd { a.dialog = nil; return nil }},
	}
	d.footer = "Enter confirm · Esc cancel · ←/→ choose"
	a.dialog = d
	return nil
}

func (a *App) startTransfer(kind ops.Kind, srcIdx int, req ops.Request) tea.Cmd {
	s, ctx := a.newSession(kind.Verb(), srcIdx, req.Sources)
	s.kind = kind
	h := a.hooks(ctx, s)
	go func() {
		res := ops.Run(ctx, req, h)
		s.events <- opTransferDoneMsg{s.id, res}
	}()
	return a.startCmds(s)
}

func (a *App) conflictDialog(s *opSession, m opConflictMsg) {
	c := m.c
	answered := false
	answer := func(act ops.Action) func(*App) tea.Cmd {
		return func(a *App) tea.Cmd {
			if answered {
				return nil
			}
			answered = true
			all := a.dialog != nil && a.dialog.toggled && act != ops.Cancel
			a.dialog = nil
			if act == ops.Cancel {
				s.canceling = true
				s.visible = true
			}
			m.reply <- ops.Decision{Action: act, ApplyAll: all}
			return nil
		}
	}
	what := "file"
	if c.IsDir {
		what = "folder"
	} else if c.DstInfo != nil && c.DstInfo.Mode()&fs.ModeSymlink != 0 {
		what = "link"
	}
	d := &dialog{kind: dlgConflict, title: fmt.Sprintf("A %s named %q already exists", what, filepath.Base(c.Dst))}
	d.body = []dline{text("Destination:"), path(c.Dst), blank(),
		text("Existing:  " + infoLine(c.DstInfo)),
		text("Incoming:  " + infoLine(c.SrcInfo))}
	if !c.IsDir && c.SrcInfo != nil && c.DstInfo != nil {
		switch {
		case c.SrcInfo.ModTime().After(c.DstInfo.ModTime()):
			d.body = append(d.body, hint("The incoming file is newer."))
		case c.SrcInfo.ModTime().Before(c.DstInfo.ModTime()):
			d.body = append(d.body, hint("The existing file is newer."))
		}
	}
	over := button{label: "Overwrite (o)", key: "o", danger: true, run: answer(ops.Overwrite)}
	if c.IsDir {
		d.body = append(d.body, blank(), text("Merge puts the contents into the existing folder. Items that exist in both are asked about one by one; nothing else in the existing folder is changed."))
		over = button{label: "Merge (o)", key: "o", run: answer(ops.Overwrite)}
	} else {
		d.body = append(d.body, blank(), warn("Overwrite replaces the existing file (only after the new copy is complete)."))
	}
	d.buttons = []button{
		{label: "Skip (s)", key: "s", run: answer(ops.Skip)},
		over,
		{label: "Cancel all (Esc)", run: answer(ops.Cancel)},
	}
	d.toggleLabel, d.toggleKey = "Apply to all remaining conflicts (a)", "a"
	d.onEsc = answer(ops.Cancel)
	d.footer = "s skip · o " + strings.ToLower(strings.Fields(over.label)[0]) + " · a apply to all · Esc cancel operation"
	a.dialog = d
}

func (a *App) lowSpaceDialog(m opLowSpaceMsg) {
	done := false
	answer := func(ok bool) func(*App) tea.Cmd {
		return func(a *App) tea.Cmd {
			if !done {
				done = true
				a.dialog = nil
				m.reply <- ok
			}
			return nil
		}
	}
	d := &dialog{kind: dlgLowSpace, title: "Not enough free space?", danger: true}
	d.body = []dline{
		text(fmt.Sprintf("Needed: %s   Available: %s", fsys.HumanSize(int64(m.need)), fsys.HumanSize(int64(m.free)))),
		blank(),
		text("If the disk fills up, the operation stops: completed items are kept, the incomplete file is removed and nothing is overwritten."),
	}
	d.buttons = []button{
		{label: "Cancel (Esc)", run: answer(false)},
		{label: "Continue anyway (c)", key: "c", danger: true, run: answer(true)},
	}
	d.onEsc = answer(false)
	a.dialog = d
}

func (a *App) finishTransfer(s *opSession, res ops.Result) tea.Cmd {
	a.op = nil
	a.dialog = nil
	src, dst := a.panels[s.srcPanel], a.panels[1-s.srcPanel]
	if res.OK() {
		src.ClearSelection()
	}
	srcFocus := ""
	if s.kind == ops.Move {
		srcFocus = src.NextFocus(s.names)
	}
	cmds := []tea.Cmd{src.RefreshFocus(srcFocus), dst.RefreshFocus(s.first)}

	summary := res.Summary()
	switch {
	case res.OK():
		a.setStatusLevel(summary, lvlOK)
	case res.Fatal == nil && len(res.Errors) == 0 && len(res.Leftovers) == 0: // cancelled cleanly
		a.setStatusLevel(summary, lvlWarn)
	default:
		a.setStatusLevel(summary, lvlErr)
		title := capitalize(s.kind.String()) + " finished with problems"
		if res.Fatal != nil {
			title = capitalize(s.kind.String()) + " stopped"
		}
		var body []dline
		body = append(body, text(summary))
		if res.Fatal != nil {
			body = append(body, blank(), warn("Stopped: "+errText(res.Fatal)))
		}
		if len(res.Leftovers) > 0 {
			body = append(body, blank(), warn(fmt.Sprintf("Copied and verified, but the source could NOT be removed (%d) — the data now exists in both places:", len(res.Leftovers))))
			body = append(body, itemLines(res.Leftovers)...)
		}
		if len(res.Errors) > 0 {
			body = append(body, blank(), warn(fmt.Sprintf("Failed (%d) — these items were not %s:", len(res.Errors), strings.ToLower(s.kind.Past()))))
			body = append(body, itemLines(res.Errors)...)
		}
		if res.Canceled {
			body = append(body, blank(), hint("The operation was cancelled; remaining items were not processed."))
		}
		a.showReport(title, body)
	}
	return tea.Batch(cmds...)
}

// ---- delete ------------------------------------------------------------------

func (a *App) beginDelete() tea.Cmd {
	if a.refuseInArchive("Deleting") {
		return nil
	}
	p := a.ActivePanel()
	if p.InRemote() {
		return a.beginCloudDelete() // cloud.go
	}
	targets := p.Targets()
	if len(targets) == 0 {
		a.setStatus("nothing to delete: put the cursor on an item or select items with Space", true)
		return nil
	}
	paths := pathsOf(targets)
	ctx, cancel := context.WithCancel(context.Background())
	a.opSeq++
	id := a.opSeq
	a.planning = &planState{id: id, cancel: cancel, panel: a.active, targets: targets}
	a.setStatus("Checking what will be deleted… (Esc to cancel)", false)
	tr := a.trasher
	return func() tea.Msg {
		plan, err := ops.PlanDelete(ctx, paths, tr)
		return deletePlannedMsg{id, plan, err}
	}
}

func (a *App) deleteDialog(plan ops.DeletePlan, st *planState) {
	n := len(plan.Paths)
	what := countPhrase(plan.TopDirs, plan.TopFiles)
	typed := !plan.UseTrash && (plan.TopDirs > 0 || n > 1)
	d := &dialog{kind: dlgDelete, danger: true}
	if plan.UseTrash {
		d.title = fmt.Sprintf("Move %s to the trash?", what)
		d.body = append(d.body, text(fmt.Sprintf("Move %s to the trash:", what)))
	} else {
		d.title = fmt.Sprintf("Permanently delete %s?", what)
		d.body = append(d.body, warn(fmt.Sprintf("PERMANENTLY delete %s:", what)))
	}
	for i, p := range plan.Paths {
		if i == listCap {
			d.body = append(d.body, hint(fmt.Sprintf("… and %d more", n-listCap)))
			break
		}
		if fi, err := os.Lstat(p); err == nil && fi.IsDir() {
			p += string(filepath.Separator)
		}
		d.body = append(d.body, path(p))
	}
	d.body = append(d.body, blank())
	contents := plan.Files + plan.Dirs - n // items inside the selected folders
	switch {
	case plan.Recursive:
		verb := "deleted"
		if plan.UseTrash {
			verb = "moved to the trash"
		}
		d.body = append(d.body, warn(fmt.Sprintf("Folders are %s with ALL of their contents: %d items inside.", verb, contents)),
			text(fmt.Sprintf("In total: %s, %s, %s.", plural(plan.Files, "file"), plural(plan.Dirs, "folder"), fsys.HumanSize(plan.Bytes))))
	case plan.TopDirs > 0:
		d.body = append(d.body, text(fmt.Sprintf("In total: %s (the folders are empty).", fsys.HumanSize(plan.Bytes))))
	default:
		d.body = append(d.body, text("Size: "+fsys.HumanSize(plan.Bytes)))
	}
	if plan.Incomplete {
		d.body = append(d.body, warn("Some folders could not be read: the real totals may be larger and those items may fail to delete."))
	}
	d.body = append(d.body, blank())
	if plan.UseTrash {
		d.body = append(d.body, hint("Items can be restored from the trash ("+plan.TrashName+")."))
	} else {
		d.body = append(d.body, warn("This CANNOT be undone."), hint("Trash not used: "+plan.TrashNote))
	}

	cancelDel := func(a *App) tea.Cmd {
		a.dialog = nil
		a.setStatusLevel("Delete cancelled — nothing was deleted", lvlWarn)
		return nil
	}
	start := func(a *App) tea.Cmd {
		a.dialog = nil
		return a.startDelete(plan, st)
	}
	d.onEsc = cancelDel
	if typed {
		d.input = newLineInput("")
		d.inputLabel = `Type "delete" and press Enter to confirm:`
		d.footer = "Esc (or Enter with an empty field) cancels"
		d.onSubmit = func(a *App, v string) tea.Cmd {
			switch strings.TrimSpace(v) {
			case "":
				return cancelDel(a)
			case "delete":
				return start(a)
			}
			a.dialog.errLine = `type "delete" exactly to confirm, or press Esc to cancel`
			return nil
		}
	} else {
		yes := "Yes, move to trash (y)"
		if !plan.UseTrash {
			yes = "Yes, delete permanently (y)"
		}
		d.buttons = []button{
			{label: "No, cancel (n)", key: "n", run: cancelDel},
			{label: yes, key: "y", danger: true, run: start},
		}
		d.footer = "Enter = highlighted button (No) · y confirm · n/Esc cancel"
	}
	a.dialog = d
}

func (a *App) startDelete(plan ops.DeletePlan, st *planState) tea.Cmd {
	title := "Deleting"
	if plan.UseTrash {
		title = "Moving to trash"
	}
	s, ctx := a.newSession(title, st.panel, plan.Paths)
	s.isDelete, s.plan = true, plan
	h := a.hooks(ctx, s)
	tr := a.trasher
	go func() {
		res := ops.Delete(ctx, plan, tr, h)
		s.events <- opDeleteDoneMsg{s.id, res}
	}()
	return a.startCmds(s)
}

func (a *App) finishDelete(s *opSession, res ops.DeleteResult) tea.Cmd {
	a.op = nil
	a.dialog = nil
	p, other := a.panels[s.srcPanel], a.panels[1-s.srcPanel]
	n := len(s.plan.Paths)
	if res.OK() {
		p.ClearSelection()
	}
	cmds := []tea.Cmd{p.RefreshFocus(p.NextFocus(s.names)), other.Refresh()}

	var summary string
	removed := fmt.Sprintf("%d of %d items", res.Removed, n)
	if res.UseTrash {
		summary = fmt.Sprintf("Moved %s to the trash", plural(res.Removed, "item"))
	} else {
		summary = fmt.Sprintf("Deleted %s (%s, %s)", plural(res.Removed, "item"), plural(res.Files, "file"), plural(res.Dirs, "folder"))
	}
	switch {
	case res.OK():
		a.setStatusLevel(summary, lvlOK)
	case res.Canceled && len(res.Failed) == 0:
		msg := "Delete cancelled: " + removed + " removed; remaining items untouched"
		if !res.UseTrash && res.Files+res.Dirs > 0 {
			msg += fmt.Sprintf(" (%s, %s deleted — a folder in progress may be partly emptied)", plural(res.Files, "file"), plural(res.Dirs, "folder"))
		}
		a.setStatusLevel(msg, lvlWarn)
	default:
		msg := fmt.Sprintf("Delete incomplete: %s removed, %s", removed, plural(len(res.Failed), "error"))
		if res.Canceled {
			msg += ", cancelled"
		}
		a.setStatusLevel(msg, lvlErr)
		body := []dline{text(msg)}
		if !res.UseTrash {
			body = append(body, text(fmt.Sprintf("Deleted so far: %s, %s.", plural(res.Files, "file"), plural(res.Dirs, "folder"))))
		}
		body = append(body, blank(), warn(fmt.Sprintf("Could not delete (%d):", len(res.Failed))))
		body = append(body, itemLines(res.Failed)...)
		if !res.UseTrash {
			body = append(body, blank(), hint("Folders that still contain undeletable items were kept."))
		}
		a.showReport("Delete finished with problems", body)
	}
	return tea.Batch(cmds...)
}

// ---- rename ------------------------------------------------------------------

// renameOne renames a single entry (the only target) with a one-line dialog.
// Several targets use the bulk rename overlay (bulkrename.go).
func (a *App) renameOne(e fsys.Entry) tea.Cmd {
	p := a.ActivePanel()
	other := a.panels[1-a.active]
	dir, old := p.Path(), e.Name
	in := newLineInput(old)
	if ext := filepath.Ext(old); !e.IsDir && ext != "" && ext != old {
		in.SetCursor(len([]rune(old)) - len([]rune(ext))) // cursor before the extension
	}
	d := &dialog{kind: dlgRename, title: "Rename"}
	d.body = []dline{text("In folder:"), path(dir), blank(), text("Current name: " + old)}
	d.input, d.inputLabel = in, "New name:"
	d.footer = "Enter rename · Esc cancel"
	d.onSubmit = func(a *App, v string) tea.Cmd {
		if v == old {
			a.dialog = nil
			a.setStatus("name unchanged", false)
			return nil
		}
		if err := ops.Rename(dir, old, v); err != nil {
			a.dialog.errLine = errText(err)
			return nil
		}
		a.dialog = nil
		a.setStatusLevel(fmt.Sprintf("Renamed %q to %q", old, v), lvlOK)
		focus := v
		if cur, ok := p.Current(); ok && cur.Name != old {
			focus = cur.Name // a selected item elsewhere was renamed: keep the cursor
		}
		if p.IsSelected(old) {
			p.ClearSelection()
		}
		return tea.Batch(p.RefreshFocus(focus), other.Refresh())
	}
	a.dialog = d
	return nil
}

// ---- report dialog -------------------------------------------------------------

func (a *App) showReport(title string, body []dline) {
	a.dialog = &dialog{
		kind: dlgReport, title: title, danger: true, body: body, width: 76,
		buttons: []button{{label: "OK (Enter)", run: func(a *App) tea.Cmd { a.dialog = nil; return nil }}},
		footer:  "↑/↓ PgUp/PgDn scroll · Enter/Esc close",
	}
}

// ---- progress rendering ----------------------------------------------------------

func (a *App) renderProgress(s *opSession) string {
	th := a.theme
	p := s.prog
	w := boxWidth(60, a.width)
	d := &dialog{title: s.title + "…", width: 60}
	if s.canceling {
		d.title = "Cancelling…"
	}
	if p.Phase == "scanning" || (p.TotalItems == 0 && p.DoneItems == 0 && !p.ByteOnly) {
		d.body = append(d.body, text(fmt.Sprintf("Scanning… %d items found", p.TotalItems)))
	} else {
		f := p.Fraction()
		filled := int(f * float64(w))
		bar := th.ProgressFill.Render(strings.Repeat("█", filled)) + th.ProgressEmpty.Render(strings.Repeat("░", w-filled))
		d.body = append(d.body, dline{kind: lnRaw, text: bar})
		counts := fmt.Sprintf("%d / %d items", p.DoneItems, p.TotalItems)
		if p.ByteOnly {
			counts = plural(p.TotalItems, "item")
		}
		if p.TotalBytes > 0 {
			counts += fmt.Sprintf(" · %s / %s", fsys.HumanSize(p.DoneBytes), fsys.HumanSize(p.TotalBytes))
		}
		d.body = append(d.body, text(fmt.Sprintf("%3.0f%%  %s", f*100, counts)))
	}
	if p.Current != "" {
		d.body = append(d.body, path(p.Current))
	} else {
		d.body = append(d.body, blank())
	}
	if p.Errors > 0 {
		d.body = append(d.body, warn(fmt.Sprintf("%s so far — last: %s", plural(p.Errors, "error"), p.LastError)))
	}
	if s.canceling {
		d.footer = "Stopping safely after the current item…"
	} else {
		d.footer = "Esc cancel"
	}
	return a.renderDialog(d)
}

// ---- helpers -----------------------------------------------------------------

func pathsOf(es []fsys.Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Path
	}
	return out
}

func countPhrase(dirs, files int) string {
	switch {
	case dirs == 1 && files == 0:
		return "1 folder"
	case dirs == 0 && files == 1:
		return "1 file"
	case dirs == 0:
		return plural(files, "file")
	case files == 0:
		return plural(dirs, "folder")
	}
	return fmt.Sprintf("%d items (%s, %s)", dirs+files, plural(dirs, "folder"), plural(files, "file"))
}

func describe(es []fsys.Entry) string {
	if len(es) == 1 {
		kind := "file"
		if es[0].IsDir && !es[0].IsLink {
			kind = "folder"
		} else if es[0].IsLink {
			kind = "link"
		}
		return fmt.Sprintf("%s %q", kind, es[0].Name)
	}
	dirs := 0
	for _, e := range es {
		if e.IsDir && !e.IsLink {
			dirs++
		}
	}
	return countPhrase(dirs, len(es)-dirs)
}

func nameList(es []fsys.Entry) []dline {
	var out []dline
	for i, e := range es {
		if i == listCap {
			out = append(out, hint(fmt.Sprintf("… and %d more", len(es)-listCap)))
			break
		}
		n := "  " + e.Name
		if e.IsDir && !e.IsLink {
			n += string(filepath.Separator)
		}
		out = append(out, path(n))
	}
	return out
}

func itemLines(items []ops.ItemError) []dline {
	var out []dline
	for _, it := range items {
		out = append(out, path(it.Path), hint("   "+it.Op+": "+errText(it.Err)))
	}
	return out
}

func infoLine(fi fs.FileInfo) string {
	if fi == nil {
		return "?"
	}
	size := fsys.HumanSize(fi.Size())
	switch {
	case fi.IsDir():
		size = "folder"
	case fi.Mode()&fs.ModeSymlink != 0:
		size = "link"
	}
	return fmt.Sprintf("%-10s modified %s", size, fi.ModTime().Format("2006-01-02 15:04:05"))
}

// errText returns err's message without a duplicated path prefix (only for
// bare *PathError / *LinkError values; wrapped errors keep their context).
func errText(err error) string {
	switch e := err.(type) {
	case *fs.PathError:
		return e.Err.Error()
	case *os.LinkError:
		return e.Err.Error()
	}
	return err.Error()
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %s", n, pluralWord(word))
}

// pluralWord forms the English plural of the nouns used in messages
// ("file" → "files", "match" → "matches", "entry" → "entries").
func pluralWord(w string) string {
	switch {
	case strings.HasSuffix(w, "ch"), strings.HasSuffix(w, "sh"),
		strings.HasSuffix(w, "s"), strings.HasSuffix(w, "x"):
		return w + "es"
	case len(w) > 1 && strings.HasSuffix(w, "y") && !strings.ContainsAny(w[len(w)-2:len(w)-1], "aeiou"):
		return w[:len(w)-1] + "ies"
	}
	return w + "s"
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

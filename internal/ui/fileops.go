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

type opTransferValidatedMsg struct {
	id      int
	req     ops.Request
	targets []fsys.Entry
	err     error
}

type opTransferDoneMsg struct {
	id  int
	res ops.Result
}

type opDeleteDoneMsg struct {
	id  int
	res ops.DeleteResult
}

type deletePlannedMsg struct {
	id   int
	plan ops.DeletePlan
	err  error
}

// opSession is the complete retained record for one background file task.
type opSession struct {
	id               int
	title            string
	state            taskState
	waitingFor       string
	cancel           context.CancelFunc
	events           chan tea.Msg
	prog             ops.Progress
	canceling        bool
	lowSpaceDeclined bool
	started          time.Time
	finished         time.Time
	summary          string
	details          []string

	srcPanel   int
	panelPaths [2]string // panel locations captured when the task starts
	from       []string
	to         string
	names      map[string]bool // top-level names in the source panel
	selection  map[string]bool // selected source names captured when the task starts
	first      string          // first top-level name (focused in the destination)
	kind       ops.Kind
	isDelete   bool
	plan       ops.DeletePlan
	// dstPanel is the destination panel of a transfer (the other panel for
	// panel operations, chosen explicitly when the operation starts outside
	// the panels, as in Advanced Search where srcPanel is -1).
	dstPanel int

	// fromLabel replaces the "From:" folder in the transfer confirmation when
	// the operation was not started from the panel's cursor (Advanced Search).
	fromLabel string
	// after runs once a transfer or delete finishes; overlays use it to keep
	// their own lists in sync with the filesystem.
	after func(*App)
}

func (s *opSession) active() bool {
	return s != nil && s.state <= taskWaiting
}

func (s *opSession) listen() tea.Cmd {
	ch := s.events
	return func() tea.Msg { return <-ch }
}

// busy is retained as a query for tests and status consumers. It no longer
// gates input: active operations are background work, not a UI lock.
func (a *App) busy() bool { return a.tasks.activeCount() > 0 }

func (a *App) newSession(title string, srcPanel int, targets []string) (*opSession, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	a.opSeq++
	s := &opSession{
		id: a.opSeq, title: title, state: taskRunning, cancel: cancel,
		events: make(chan tea.Msg, 32), srcPanel: srcPanel, dstPanel: 1 - srcPanel,
		names: map[string]bool{}, selection: map[string]bool{},
		started: a.now(),
	}
	for i, p := range a.panels {
		if p != nil {
			s.panelPaths[i] = p.Path()
		}
	}
	if srcPanel >= 0 && srcPanel < len(a.panels) && a.panels[srcPanel] != nil {
		for _, name := range a.panels[srcPanel].SelectedNames() {
			s.selection[name] = true
		}
	}
	for i, p := range targets {
		path := filepath.Clean(p)
		s.from = append(s.from, path)
		n := filepath.Base(path)
		s.names[n] = true
		if i == 0 {
			s.first = n
		}
	}
	a.tasks.add(s)
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
				if !ok {
					s.lowSpaceDeclined = true
				}
				return ok
			case <-ctx.Done():
				return false
			}
		},
	}
}

func (a *App) startCmds(s *opSession) tea.Cmd { return s.listen() }

func (a *App) finishTask(s *opSession, state taskState, summary string, details ...string) {
	if s == nil || !s.active() {
		return
	}
	s.state, s.waitingFor = state, ""
	s.summary = summary
	s.finished = a.now()
	if len(details) > maxTaskDetails {
		keep := maxTaskDetails - 1
		s.details = append(append([]string(nil), details[:keep]...), fmt.Sprintf("… %d more details omitted", len(details)-keep))
	} else {
		s.details = append([]string(nil), details...)
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

func taskOutcome(canceled bool, fatal error, errors int, partial bool) taskState {
	if canceled {
		if partial || errors > 0 {
			return taskPartial
		}
		return taskCanceled
	}
	if fatal != nil || errors > 0 {
		if partial {
			return taskPartial
		}
		return taskFailed
	}
	return taskCompleted
}

func itemErrorDetails(items []ops.ItemError) []string {
	limit := min(len(items), maxTaskDetails-1)
	out := make([]string, 0, limit+1)
	for _, item := range items[:limit] {
		out = append(out, item.Error())
	}
	if len(items) > limit {
		out = append(out, fmt.Sprintf("… %d more errors omitted", len(items)-limit))
	}
	return out
}

func appendTaskDetail(details []string, label string, err error) []string {
	if err != nil {
		return append(details, label+": "+errText(err))
	}
	return details
}

func (a *App) enqueueTaskPrompt(p taskPrompt) {
	s := a.tasks.get(p.taskID)
	if s == nil || !s.active() {
		return
	}
	s.state = taskWaiting
	s.waitingFor = map[taskPromptKind]string{
		promptConflict:        "conflict decision",
		promptLowSpace:        "free-space decision",
		promptDeleteConfirm:   "delete confirmation",
		promptTransferConfirm: "copy / move confirmation",
	}[p.kind]
	a.tasks.queuePrompt(p)
}

// showNextTaskPrompt arbitrates the single terminal dialog among concurrent
// tasks and any ordinary dialog already open.
func (a *App) showNextTaskPrompt() {
	if a.dialog != nil || a.tasks.activePrompt != 0 {
		return
	}
	for len(a.tasks.prompts) > 0 {
		p := a.tasks.prompts[0]
		a.tasks.prompts = a.tasks.prompts[1:]
		s := a.tasks.get(p.taskID)
		if s == nil || !s.active() || s.canceling {
			continue
		}
		a.tasks.activePrompt = p.taskID
		s.state = taskWaiting
		switch p.kind {
		case promptConflict:
			a.conflictDialog(s, p.conflict)
		case promptLowSpace:
			a.lowSpaceDialog(s, p.lowSpace)
		case promptDeleteConfirm:
			a.deleteDialog(s, p.plan)
		case promptTransferConfirm:
			a.transferDialog(s, p.transfer, p.targets)
		}
		return
	}
}

func (a *App) finishTaskPrompt(s *opSession, resume bool) {
	if s != nil && a.tasks.activePrompt == s.id {
		a.tasks.activePrompt = 0
	}
	if s != nil && resume && s.active() {
		s.state, s.waitingFor = taskRunning, ""
	}
}

func (a *App) cancelTask(id int) bool {
	s := a.tasks.get(id)
	if s == nil || !s.active() || s.canceling {
		return false
	}
	if a.tasks.activePrompt == id {
		// The modal owns the current decision; its Esc action replies safely.
		return false
	}
	s.canceling = true
	s.state = taskCanceling
	a.tasks.removePrompts(id)
	if s.cancel != nil {
		s.cancel()
	}
	switch s.waitingFor {
	case "delete confirmation":
		a.finishTask(s, taskCanceled, "Delete cancelled — nothing was deleted")
		a.setStatusLevel("Delete cancelled — nothing was deleted", lvlWarn)
	case "copy / move confirmation":
		summary := capitalize(s.kind.String()) + " cancelled — nothing was transferred"
		a.finishTask(s, taskCanceled, summary)
		a.setStatusLevel(summary, lvlWarn)
	default:
		a.setStatusLevel("Stopping "+s.title+" safely…", lvlWarn)
	}
	return true
}

// handleOpMsg processes messages from background tasks. ok=false if msg
// isn't one of the task messages.
func (a *App) handleOpMsg(msg tea.Msg) (tea.Cmd, bool) {
	lookup := func(id int) *opSession { return a.tasks.get(id) }
	switch m := msg.(type) {
	case opProgressMsg:
		s := lookup(m.id)
		if s == nil || !s.active() {
			return nil, true
		}
		s.prog = m.p
		if cp := a.compressProgress; cp != nil && cp.id == m.id && !cp.finished {
			cp.progress = m.p
			a.updateCompressionDialog()
		}
		return s.listen(), true
	case opConflictMsg:
		s := lookup(m.id)
		if s == nil || !s.active() {
			m.reply <- ops.Decision{Action: ops.Cancel}
			return nil, true
		}
		if s.canceling {
			m.reply <- ops.Decision{Action: ops.Cancel}
			return s.listen(), true
		}
		a.enqueueTaskPrompt(taskPrompt{taskID: s.id, kind: promptConflict, conflict: m})
		return s.listen(), true
	case opLowSpaceMsg:
		s := lookup(m.id)
		if s == nil || !s.active() {
			m.reply <- false
			return nil, true
		}
		if s.canceling {
			m.reply <- false
			return s.listen(), true
		}
		a.enqueueTaskPrompt(taskPrompt{taskID: s.id, kind: promptLowSpace, lowSpace: m})
		return s.listen(), true
	case opTransferValidatedMsg:
		s := lookup(m.id)
		if s == nil || !s.active() {
			return nil, true
		}
		if s.canceling {
			summary := capitalize(s.kind.String()) + " cancelled — nothing was transferred"
			a.finishTask(s, taskCanceled, summary)
			a.setStatusLevel(summary, lvlWarn)
			return nil, true
		}
		if m.err != nil {
			summary := fmt.Sprintf("cannot %s: %s", s.kind, errText(m.err))
			a.finishTask(s, taskFailed, summary, summary)
			a.setStatusLevel(summary, lvlErr)
			return nil, true
		}
		a.enqueueTaskPrompt(taskPrompt{taskID: s.id, kind: promptTransferConfirm, transfer: m.req, targets: m.targets})
		return nil, true
	case opTransferDoneMsg:
		if s := lookup(m.id); s != nil && s.active() {
			return a.finishTransfer(s, m.res), true
		}
		return nil, true
	case syncDoneMsg:
		if s := lookup(m.id); s != nil && s.active() {
			return a.finishSync(s, m.result, m.plan), true
		}
		return nil, true
	case opDeleteDoneMsg:
		if s := lookup(m.id); s != nil && s.active() {
			return a.finishDelete(s, m.res), true
		}
		return nil, true
	case opExtractDoneMsg:
		if s := lookup(m.id); s != nil && s.active() {
			return a.finishExtract(s, m.res, m.parent, m.parentFocus), true
		}
		return nil, true
	case opZipDoneMsg:
		if s := lookup(m.id); s != nil && s.active() {
			return a.finishZip(s, m.res), true
		}
		return nil, true
	case opCloudDoneMsg:
		if s := lookup(m.id); s != nil && s.active() {
			return a.finishCloudOp(s, m), true
		}
		return nil, true
	case opCloudDeleteDoneMsg:
		if s := lookup(m.id); s != nil && s.active() {
			return a.finishCloudDelete(s, m.res), true
		}
		return nil, true
	case deletePlannedMsg:
		s := lookup(m.id)
		if s == nil || !s.active() || !s.isDelete {
			return nil, true
		}
		if s.canceling {
			a.finishTask(s, taskCanceled, "Delete cancelled — nothing was deleted")
			a.setStatusLevel("Delete cancelled — nothing was deleted", lvlWarn)
			return nil, true
		}
		if m.err != nil {
			state := taskOutcome(false, m.err, 0, false)
			summary := "cannot delete: " + errText(m.err)
			a.finishTask(s, state, summary, summary)
			a.setStatusLevel(summary, lvlErr)
			return nil, true
		}
		s.plan = m.plan
		if m.plan.UseTrash {
			s.to = "Trash (" + m.plan.TrashName + ")"
		} else {
			s.to = "Permanent deletion"
		}
		s.state = taskWaiting
		s.waitingFor = "delete confirmation"
		a.enqueueTaskPrompt(taskPrompt{taskID: s.id, kind: promptDeleteConfirm, plan: m.plan})
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
	req := ops.Request{Kind: kind, Sources: append([]string(nil), paths...), DestDir: filepath.Clean(dst.Path())}
	// Validate before presenting the confirmation, but keep filesystem access
	// in the task goroutine. ops.Run validates once more just before touching
	// any path in case a source or destination changes while the dialog is open.
	s, _ := a.newSession("Checking "+kind.String(), srcIdx, paths)
	s.kind, s.to = kind, req.DestDir
	s.state = taskPlanning
	s.prog = ops.Progress{Phase: "validating", TotalItems: len(paths)}
	a.setStatus("Checking "+kind.String()+" paths…", false)
	targets = append([]fsys.Entry(nil), targets...)
	go func() {
		err := ops.Validate(req)
		s.events <- opTransferValidatedMsg{s.id, req, targets, err}
	}()
	return a.startCmds(s)
}

func (a *App) transferDialog(s *opSession, req ops.Request, targets []fsys.Entry) {
	verb := capitalize(req.Kind.String())
	d := &dialog{kind: dlgConfirm, title: fmt.Sprintf("%s %s?", verb, describe(targets))}
	from := s.fromLabel
	if from == "" && s.srcPanel >= 0 && s.srcPanel < len(s.panelPaths) {
		from = s.panelPaths[s.srcPanel]
	}
	d.body = append(d.body, text("From:"), path(from), text("To:"), path(req.DestDir), blank())
	d.body = append(d.body, nameList(targets)...)
	if req.Kind == ops.Move {
		d.body = append(d.body, blank(), hint("Items are removed from the source only after they have been moved (across filesystems: copied and verified first)."))
	}
	d.body = append(d.body, blank(), hint("Existing items in the destination are never overwritten without asking."))
	cancelTransfer := func(a *App) tea.Cmd {
		a.dialog = nil
		a.finishTaskPrompt(s, false)
		summary := verb + " cancelled — nothing was transferred"
		a.finishTask(s, taskCanceled, summary)
		a.setStatusLevel(summary, lvlWarn)
		return nil
	}
	d.buttons = []button{
		{label: verb + " (Enter)", run: func(a *App) tea.Cmd {
			a.dialog = nil
			a.finishTaskPrompt(s, true)
			return a.startTransfer(s, req)
		}},
		{label: "Cancel (Esc)", run: cancelTransfer},
	}
	d.onEsc = cancelTransfer
	d.footer = "Enter confirm · Esc cancel · ←/→ choose"
	a.dialog = d
}

func (a *App) startTransfer(s *opSession, req ops.Request) tea.Cmd {
	req.Sources = append([]string(nil), req.Sources...)
	req.DestDir = filepath.Clean(req.DestDir)
	if s.cancel != nil {
		s.cancel() // end the completed validation context before transferring
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.title, s.kind, s.to = cancel, req.Kind.Verb(), req.Kind, req.DestDir
	s.state, s.waitingFor = taskRunning, ""
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
				s.state = taskCanceling
				a.finishTaskPrompt(s, false)
			} else {
				a.finishTaskPrompt(s, true)
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

func (a *App) lowSpaceDialog(s *opSession, m opLowSpaceMsg) {
	done := false
	answer := func(ok bool) func(*App) tea.Cmd {
		return func(a *App) tea.Cmd {
			if !done {
				done = true
				a.dialog = nil
				a.finishTaskPrompt(s, true)
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
	var cmds []tea.Cmd
	// A session started outside the panels (Advanced Search) has no source
	// panel: the panel selection and cursor must stay untouched.
	if s.srcPanel >= 0 && s.srcPanel < len(a.panels) && a.panels[s.srcPanel] != nil {
		src := a.panels[s.srcPanel]
		if res.OK() && a.sourceSelectionUnchanged(s) {
			src.ClearSelection()
		}
		if s.kind == ops.Move {
			focus := ""
			if src.Path() == s.panelPaths[s.srcPanel] {
				focus = src.NextFocus(s.names)
			}
			cmds = append(cmds, a.refreshPanelAt(s.srcPanel, s.panelPaths[s.srcPanel], focus))
		}
	}
	dstIdx := s.dstPanel
	if dstIdx < 0 || dstIdx >= len(a.panels) || dstIdx == s.srcPanel {
		dstIdx = 1 - s.srcPanel // sessions built by hand (tests) before dstPanel existed
	}
	cmds = append(cmds, a.refreshPanelAt(dstIdx, s.panelPaths[dstIdx], s.first))

	summary := res.Summary()
	details := appendTaskDetail(nil, "Stopped", res.Fatal)
	for _, item := range res.Leftovers {
		details = append(details, "Copied and verified, but source not removed: "+item.Error())
	}
	for _, item := range res.Errors {
		details = append(details, "Failed: "+item.Error())
	}
	if res.Canceled {
		details = append(details, "Canceled safely; remaining items were not processed")
	}
	partial := res.Files+res.Dirs > 0 || len(res.Leftovers) > 0
	state := taskOutcome(res.Canceled, res.Fatal, len(res.Errors)+len(res.Leftovers), partial)
	if s.lowSpaceDeclined && res.Fatal != nil {
		summary = fmt.Sprintf("%s cancelled — not enough free space", capitalize(s.kind.String()))
		state = taskCanceled
		details = append(details, "No items were processed because the free-space check did not permit continuing")
	}
	a.finishTask(s, state, summary, details...)
	if s.after != nil {
		s.after(a)
	}
	switch {
	case s.lowSpaceDeclined:
		a.setStatusLevel(summary, lvlWarn)
	case res.OK():
		a.setStatusLevel(summary, lvlOK)
	case res.Fatal == nil && len(res.Errors) == 0 && len(res.Leftovers) == 0:
		a.setStatusLevel(summary, lvlWarn)
	default:
		a.setStatusLevel(summary, lvlErr)
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
	paths := append([]string(nil), pathsOf(targets)...)
	s, ctx := a.newSession("Checking delete", a.active, paths)
	s.isDelete = true
	s.state = taskPlanning
	s.prog = ops.Progress{Phase: "scanning", TotalItems: len(paths)}
	a.setStatus("Checking what will be deleted…", false)
	tr := a.trasher
	go func() {
		plan, err := ops.PlanDelete(ctx, paths, tr)
		s.events <- deletePlannedMsg{s.id, plan, err}
	}()
	return a.startCmds(s)
}

func (a *App) deleteDialog(s *opSession, plan ops.DeletePlan) {
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
		a.finishTaskPrompt(s, false)
		a.finishTask(s, taskCanceled, "Delete cancelled — nothing was deleted")
		a.setStatusLevel("Delete cancelled — nothing was deleted", lvlWarn)
		return nil
	}
	start := func(a *App) tea.Cmd {
		a.dialog = nil
		a.finishTaskPrompt(s, true)
		return a.startDelete(s, plan)
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

func (a *App) startDelete(s *opSession, plan ops.DeletePlan) tea.Cmd {
	s.title = "Deleting"
	if plan.UseTrash {
		s.title = "Moving to trash"
		s.to = "Trash (" + plan.TrashName + ")"
	} else {
		s.to = "Permanent deletion"
	}
	s.state, s.waitingFor = taskRunning, ""
	s.isDelete, s.plan = true, plan
	if s.cancel != nil {
		s.cancel() // end the completed planning context before starting deletion
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	h := a.hooks(ctx, s)
	tr := a.trasher
	go func() {
		res := ops.Delete(ctx, plan, tr, h)
		s.events <- opDeleteDoneMsg{s.id, res}
	}()
	return a.startCmds(s)
}

func (a *App) finishDelete(s *opSession, res ops.DeleteResult) tea.Cmd {
	n := len(s.plan.Paths)
	dir, focus := "", ""
	if s.srcPanel >= 0 && s.srcPanel < len(a.panels) && a.panels[s.srcPanel] != nil {
		p := a.panels[s.srcPanel]
		dir = s.panelPaths[s.srcPanel]
		if res.OK() && a.sourceSelectionUnchanged(s) {
			p.ClearSelection()
		}
		if p.Path() == dir {
			focus = p.NextFocus(s.names)
		}
	} else {
		dir = s.fromLabel // Advanced Search: the folder the result lives in
	}
	cmd := a.refreshPanelsAt(dir, focus)

	var summary string
	removed := fmt.Sprintf("%d of %d items", res.Removed, n)
	if res.UseTrash {
		summary = fmt.Sprintf("Moved %s to the trash", plural(res.Removed, "item"))
	} else {
		summary = fmt.Sprintf("Deleted %s (%s, %s)", plural(res.Removed, "item"), plural(res.Files, "file"), plural(res.Dirs, "folder"))
	}
	partial := res.Removed > 0 || res.Files+res.Dirs > 0
	details := itemErrorDetails(res.Failed)
	if res.Canceled && partial && !res.UseTrash {
		details = append(details, fmt.Sprintf("Deleted so far: %s, %s; a folder in progress may be partly emptied", plural(res.Files, "file"), plural(res.Dirs, "folder")))
	}
	if len(res.Failed) > 0 && !res.UseTrash {
		details = append(details, "Folders that still contain undeletable items were kept")
	}
	state := taskOutcome(res.Canceled, nil, len(res.Failed), partial)
	switch {
	case res.OK():
		a.setStatusLevel(summary, lvlOK)
	case res.Canceled && len(res.Failed) == 0:
		msg := "Delete cancelled: " + removed + " removed; remaining items untouched"
		if !res.UseTrash && res.Files+res.Dirs > 0 {
			msg += fmt.Sprintf(" (%s, %s deleted — a folder in progress may be partly emptied)", plural(res.Files, "file"), plural(res.Dirs, "folder"))
		}
		summary = msg
		a.setStatusLevel(msg, lvlWarn)
	default:
		msg := fmt.Sprintf("Delete incomplete: %s removed, %s", removed, plural(len(res.Failed), "error"))
		if res.Canceled {
			msg += ", cancelled"
		}
		summary = msg
		a.setStatusLevel(msg, lvlErr)
	}
	a.finishTask(s, state, summary, details...)
	if s.after != nil {
		s.after(a)
	}
	return cmd
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

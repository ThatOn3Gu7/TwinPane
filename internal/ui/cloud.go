package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/cloud"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
)

// Cloud remotes (phase 9). Either panel can show the local filesystem or a
// remote configured in rclone (Ctrl+R opens the Sources picker). Remote
// folders are listed with rclone in the background (Esc cancels a slow
// listing); copy and move between the panels, new folder, rename and delete
// work on remotes and run as background tasks like local operations. Progress,
// completion and errors stay in the task view, and conflict prompts are
// serialized safely. Actions
// that need a real local file (view, edit, open, properties, compress,
// extract, find, bookmarks) explain that the item must be copied to a local
// panel first. TwinPane never asks for, sees or stores credentials: rclone
// uses its own configuration (`rclone config`).

type remotesMsg struct {
	seq     int
	client  *cloud.Client
	remotes []cloud.Remote
	err     error
}

// opCloudDoneMsg ends a single-item remote operation (new folder, rename).
type opCloudDoneMsg struct {
	id    int
	what  string // "Created folder", "Renamed"
	focus string // entry to put the cursor on afterwards
	err   error
}

type cloudDeleteResult struct {
	deleted  int
	errors   []ops.ItemError
	canceled bool
	fatal    error
}

type opCloudDeleteDoneMsg struct {
	id  int
	res cloudDeleteResult
}

// syncBadges labels local panels "LOCAL" while either panel shows a remote.
func (a *App) syncBadges() {
	on := a.panels[0].InRemote() || a.panels[1].InRemote()
	for _, p := range a.panels {
		p.SetLocalBadge(on)
	}
}

// sideName is "left" / "right" for panel i.
func sideName(i int) string {
	if i == 0 {
		return "left"
	}
	return "right"
}

// ---- Sources picker (Ctrl+R) ---------------------------------------------------

// openSources shows the Sources picker for the active panel: local files
// and every configured rclone remote. The remotes are listed in the
// background; the picker opens at once.
func (a *App) openSources() tea.Cmd {
	a.sourcesSeq++
	seq := a.sourcesSeq
	c, err := cloud.Detect(a.lookPath)
	if err != nil {
		a.dialog = a.sourcesDialog(nil, nil, sourcesMissing)
		return nil
	}
	a.dialog = a.sourcesDialog(c, nil, sourcesLoading)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rs, err := c.Remotes(ctx)
		return remotesMsg{seq: seq, client: c, remotes: rs, err: err}
	}
}

type sourcesState int

const (
	sourcesLoading sourcesState = iota
	sourcesReady
	sourcesMissing
	sourcesFailed
)

// remotesLoaded fills the picker in once rclone listed the remotes.
func (a *App) remotesLoaded(m remotesMsg) {
	if m.seq != a.sourcesSeq || a.dialog == nil || a.dialog.kind != dlgSources {
		return // closed or superseded
	}
	state := sourcesReady
	if m.err != nil {
		state = sourcesFailed
	}
	cursor := a.dialog.listCursor
	d := a.sourcesDialog(m.client, m.remotes, state)
	if m.err != nil {
		d.body = append(d.body, warn("rclone could not list the remotes: "+errText(m.err)))
	}
	if cursor < len(d.list) {
		d.listCursor = cursor
	}
	a.dialog = d
}

func (a *App) sourcesDialog(c *cloud.Client, remotes []cloud.Remote, state sourcesState) *dialog {
	idx := a.active
	p := a.panels[idx]
	d := &dialog{kind: dlgSources, title: "Source for the " + sideName(idx) + " panel", width: 56}
	type choice struct {
		run func(a *App) tea.Cmd
	}
	var choices []choice

	localDir := a.localDirFor(idx)
	localDetail := shortPath(localDir)
	if !p.InRemote() {
		localDetail += " · shown now"
	}
	d.list = append(d.list, listItem{label: "Local files", detail: localDetail, key: "l"})
	choices = append(choices, choice{func(a *App) tea.Cmd {
		if !p.InRemote() {
			a.setStatus("the "+sideName(idx)+" panel already shows local files", false)
			return nil
		}
		a.setStatus("", false)
		return p.GoLocal(localDir, "")
	}})
	cur := ""
	if src := p.Source(); src != nil {
		cur = src.Remote.Name
	}
	for i, r := range remotes {
		r := r
		detail := r.Type
		if r.Name == cur {
			detail += " · shown now"
		}
		key := ""
		if i < 9 {
			key = strconv.Itoa(i + 1)
		}
		d.list = append(d.list, listItem{label: r.Name + ":", detail: detail, key: key})
		choices = append(choices, choice{func(a *App) tea.Cmd {
			a.setStatus("opening "+r.Name+": … (Esc cancels)", false)
			return p.OpenRemote(&panel.RemoteSource{Client: c, Remote: r})
		}})
	}
	d.list = append(d.list, listItem{label: "Refresh both panels", detail: "^L", key: "r"})
	choices = append(choices, choice{func(a *App) tea.Cmd { return a.refreshBoth() }})

	switch state {
	case sourcesLoading:
		d.body = append(d.body, hint("Looking for rclone remotes…"))
	case sourcesMissing:
		hintText := cloud.InstallHint(a.platform().Termux, a.goos)
		d.body = append(d.body,
			warn("rclone is not installed. Cloud remotes (Google Drive, Dropbox, OneDrive, SFTP, …) need it."),
			text("Install it: "+hintText),
			text("Then add a remote with `rclone config` in a terminal and press Ctrl+R again."),
			hint("Local files work without rclone."))
	case sourcesReady:
		if len(remotes) == 0 {
			d.body = append(d.body,
				text("No rclone remotes are configured."),
				text("Run `rclone config` in a terminal (n = new remote, choose the provider, follow the login steps), then press Ctrl+R again."))
		}
	}
	if state != sourcesMissing {
		d.body = append(d.body, hint("rclone keeps the logins in its own configuration; TwinPane never asks for or stores them."))
	}
	d.footer = "⏎ or key: choose · Esc close"
	d.onPick = func(a *App, i int) tea.Cmd {
		a.dialog = nil
		if i < 0 || i >= len(choices) {
			return nil
		}
		return choices[i].run(a)
	}
	return d
}

// localDirFor is where "Local files" takes panel i: the local folder it
// showed last, else the other panel's local folder, else home.
func (a *App) localDirFor(i int) string {
	if d := a.panels[i].LastLocal(); d != "" {
		return d
	}
	if d := a.panels[1-i].RealDir(); d != "" {
		return d
	}
	return fsys.HomeDir()
}

func (a *App) refreshBoth() tea.Cmd {
	a.setStatus("refreshed", false)
	return tea.Batch(a.panels[0].Refresh(), a.panels[1].Refresh())
}

// remoteOpened reports a panel that just started showing a remote.
func (a *App) remoteOpened(p *panel.Panel) {
	src := p.Source()
	a.setStatusLevel(fmt.Sprintf("%s: (%s) — c/m copy·move to the other panel · n new folder · r rename · d delete · ^R sources",
		src.Remote.Name, src.Remote.Type), lvlInfo)
}

// remoteUnsupported returns what the key would do when it cannot work on
// a remote ("" = the key works there).
func (a *App) remoteUnsupported(msg tea.KeyMsg) string {
	k := a.keys
	switch {
	case k.View.Matches(msg):
		return "Viewing"
	case k.Edit.Matches(msg), k.EditWith.Matches(msg):
		return "Editing"
	case k.Properties.Matches(msg):
		return "Properties"
	case k.Extract.Matches(msg), k.ExtractAll.Matches(msg):
		return "Extracting"
	case k.Zip.Matches(msg):
		return "Compressing"
	case k.OpenExt.Matches(msg), k.OpenWith.Matches(msg):
		return "Opening files"
	case k.Search.Matches(msg):
		return "Find"
	case k.Bookmark.Matches(msg):
		return "Bookmarking"
	case k.Storage.Matches(msg):
		return "Storage info"
	}
	return ""
}

func (a *App) refuseOnRemote(what string) {
	name := "a cloud remote"
	if src := a.ActivePanel().Source(); src != nil {
		name = src.Remote.Name + ":"
	}
	a.setStatus(what+" is not available on "+name+" — copy the item to a local panel first (c), or Ctrl+R → Local files", true)
}

// ---- copy / move with a remote ---------------------------------------------------

// cloudClient returns the rclone client of whichever panel shows a remote.
func (a *App) cloudClient() *cloud.Client {
	for _, i := range []int{a.active, 1 - a.active} {
		if src := a.panels[i].Source(); src != nil {
			return src.Client
		}
	}
	return nil
}

// cloudPath converts a panel entry path to a cloud.Path.
func cloudPath(p *panel.Panel, entryPath string) cloud.Path {
	if p.InRemote() {
		if loc, ok := cloud.Parse(entryPath); ok {
			return loc
		}
	}
	return cloud.Local(entryPath)
}

// panelDir is the folder a panel shows, as a cloud.Path.
func panelDir(p *panel.Panel) cloud.Path {
	if p.InRemote() {
		return p.Location()
	}
	return cloud.Local(p.Path())
}

// beginCloudTransfer copies or moves the selection between the panels when
// either of them shows a remote.
func (a *App) beginCloudTransfer(kind ops.Kind) tea.Cmd {
	srcIdx := a.active
	src, dst := a.panels[srcIdx], a.panels[1-srcIdx]
	if src.InArchive() {
		a.setStatus("extract from the archive to a local folder first (x), then copy that to the remote", true)
		return nil
	}
	targets := src.Targets()
	if len(targets) == 0 {
		a.setStatus(fmt.Sprintf("nothing to %s: put the cursor on an item or select items with Space", kind), true)
		return nil
	}
	if dst.Path() == "" || dst.Err() != nil {
		a.setStatus(fmt.Sprintf("cannot %s: the other panel shows no folder (an error) — open a folder there first", kind), true)
		return nil
	}
	if src.Path() == dst.Path() {
		a.setStatus(fmt.Sprintf("cannot %s: both panels show the same folder", kind), true)
		return nil
	}
	c := a.cloudClient()
	if c == nil {
		return nil
	}
	dest := panelDir(dst)
	var items []cloud.TransferItem
	var links []string
	for _, e := range targets {
		if !src.InRemote() && e.IsLink {
			links = append(links, e.Name)
			continue
		}
		items = append(items, cloud.TransferItem{Src: cloudPath(src, e.Path), IsDir: e.IsDir, Size: e.Size, ModTime: e.ModTime})
	}
	if len(items) == 0 {
		a.setStatus("symbolic links cannot be copied to a remote (rclone does not follow them)", true)
		return nil
	}
	verb, title := capitalize(kind.String()), cloudTitle(kind, src.InRemote(), dst.InRemote())
	d := &dialog{kind: dlgConfirm, title: fmt.Sprintf("%s %s?", verb, describe(targets))}
	d.body = append(d.body, text("From:"), path(panelDir(src).Display()), text("To:"), path(dest.Display()), blank())
	d.body = append(d.body, nameList(targets)...)
	d.body = append(d.body, blank(), hint(title+" with rclone."))
	if kind == ops.Move {
		d.body = append(d.body, hint("Each item is removed from the source only after rclone has transferred it; skipped or failed items stay."))
	}
	if len(links) > 0 {
		d.body = append(d.body, warn(fmt.Sprintf("%s not included: rclone does not follow symbolic links (%s).", plural(len(links), "symbolic link"), strings.Join(links, ", "))))
	}
	d.body = append(d.body, hint("Existing items in the destination are never overwritten without asking."))
	req := cloud.TransferRequest{Kind: kind, Items: items, Dest: dest}
	d.buttons = []button{
		{label: verb + " (Enter)", run: func(a *App) tea.Cmd { a.dialog = nil; return a.startCloudTransfer(c, srcIdx, title, req) }},
		{label: "Cancel (Esc)", run: func(a *App) tea.Cmd { a.dialog = nil; return nil }},
	}
	d.footer = "Enter confirm · Esc cancel · ←/→ choose"
	a.dialog = d
	return nil
}

// cloudTitle names the direction: Uploading, Downloading, Copying.
func cloudTitle(kind ops.Kind, fromRemote, toRemote bool) string {
	switch {
	case !fromRemote && toRemote:
		if kind == ops.Move {
			return "Uploading (move)"
		}
		return "Uploading"
	case fromRemote && !toRemote:
		if kind == ops.Move {
			return "Downloading (move)"
		}
		return "Downloading"
	}
	return kind.Verb()
}

func (a *App) startCloudTransfer(c *cloud.Client, srcIdx int, title string, req cloud.TransferRequest) tea.Cmd {
	req.Items = append([]cloud.TransferItem(nil), req.Items...)
	s, ctx := a.newSession(title, srcIdx, nil)
	s.kind, s.to = req.Kind, req.Dest.Display()
	for i, it := range req.Items {
		n := it.Src.Base()
		s.names[n] = true
		s.from = append(s.from, it.Src.Display())
		if i == 0 {
			s.first = n
		}
	}
	h := a.hooks(ctx, s)
	go func() {
		res := c.Transfer(ctx, req, h)
		s.events <- opTransferDoneMsg{s.id, res}
	}()
	return a.startCmds(s)
}

// ---- new folder (n / F7) -----------------------------------------------------------

func (a *App) beginMkdir() tea.Cmd {
	if a.refuseInArchive("Creating folders") {
		return nil
	}
	p := a.ActivePanel()
	if p.Path() == "" || (p.Err() != nil && !p.InRemote()) {
		a.setStatus("no folder to create it in", true)
		return nil
	}
	idx := a.active
	where := panelDir(p)
	d := &dialog{kind: dlgMkdir, title: "New folder"}
	d.body = []dline{text("In:"), path(where.Display())}
	d.input, d.inputLabel = newLineInput(""), "Folder name:"
	d.footer = "Enter create · Esc cancel"
	d.onSubmit = func(a *App, v string) tea.Cmd {
		if p.InRemote() {
			if err := cloud.CheckName(v); err != nil {
				a.dialog.errLine = err.Error()
				return nil
			}
			a.dialog = nil
			client := p.Source().Client
			return a.startCloudOp(idx, "Creating folder", v, "Created folder", where.Display(), where.Join(v).Display(), func(ctx context.Context) error {
				return client.Mkdir(ctx, where, v)
			})
		}
		if err := ops.ValidateName(v); err != nil {
			a.dialog.errLine = errText(err)
			return nil
		}
		target := filepath.Join(p.Path(), v)
		if _, err := os.Lstat(target); err == nil {
			a.dialog.errLine = fmt.Sprintf("%q already exists", v)
			return nil
		}
		if err := os.Mkdir(target, 0o755); err != nil {
			if errors.Is(err, fs.ErrExist) {
				a.dialog.errLine = fmt.Sprintf("%q already exists", v)
			} else {
				a.dialog.errLine = errText(err)
			}
			return nil
		}
		a.dialog = nil
		a.setStatusLevel(fmt.Sprintf("Created folder %q", v), lvlOK)
		return tea.Batch(p.RefreshFocus(v), a.panels[1-idx].Refresh())
	}
	a.dialog = d
	return nil
}

// startCloudOp runs one remote operation as a retained background task.
func (a *App) startCloudOp(idx int, title, name, what, from, to string, run func(ctx context.Context) error) tea.Cmd {
	s, ctx := a.newSession(title, idx, nil)
	s.prog = ops.Progress{Phase: "working", TotalItems: 1, Current: name}
	if from != "" {
		s.from = []string{from}
	}
	s.to = to
	s.names[name], s.first = true, name
	go func() {
		err := run(ctx)
		s.events <- opCloudDoneMsg{id: s.id, what: what, focus: name, err: err}
	}()
	return a.startCmds(s)
}

func (a *App) finishCloudOp(s *opSession, m opCloudDoneMsg) tea.Cmd {
	var summary string
	var level int
	var state taskState
	var details []string
	switch {
	case errors.Is(m.err, cloud.ErrNoEmptyFolders):
		summary, level = fmt.Sprintf("%q: %v", m.focus, m.err), lvlWarn
		state = taskOutcome(false, m.err, 1, false)
		details = append(details, summary)
	case cloud.KindOf(m.err) == cloud.KindCanceled:
		summary, level = "cancelled — rclone was stopped", lvlWarn
		state = taskOutcome(true, m.err, 0, true)
		details = append(details, "The remote may have completed the action before cancellation was acknowledged")
	case m.err != nil:
		summary, level = "failed: "+errText(m.err), lvlErr
		state = taskOutcome(false, m.err, 1, false)
		details = append(details, errText(m.err))
	default:
		summary, level = fmt.Sprintf("%s %q", m.what, m.focus), lvlOK
		state = taskCompleted
	}
	if state == taskCompleted && m.what == "Renamed to" {
		p := a.panels[s.srcPanel]
		if a.sourceSelectionUnchanged(s) {
			p.ClearSelection()
		}
	}
	cmd := a.refreshPanelsAt(s.panelPaths[s.srcPanel], m.focus)
	a.finishTask(s, state, summary, details...)
	a.setStatusLevel(summary, level)
	return cmd
}

// ---- rename on a remote ----------------------------------------------------------

func (a *App) renameRemote(targets []fsys.Entry) tea.Cmd {
	if len(targets) != 1 {
		a.setStatus("several items at once (bulk rename) can only be renamed in a local panel — select one item", true)
		return nil
	}
	p := a.ActivePanel()
	idx := a.active
	e := targets[0]
	loc := cloudPath(p, e.Path)
	client := p.Source().Client
	old := e.Name
	in := newLineInput(old)
	if ext := filepath.Ext(old); !e.IsDir && ext != "" && ext != old {
		in.SetCursor(len([]rune(old)) - len([]rune(ext)))
	}
	d := &dialog{kind: dlgRename, title: "Rename"}
	d.body = []dline{text("In folder:"), path(p.Location().Display()), blank(), text("Current name: " + old)}
	d.input, d.inputLabel = in, "New name:"
	d.footer = "Enter rename · Esc cancel"
	d.onSubmit = func(a *App, v string) tea.Cmd {
		if v == old {
			a.dialog = nil
			a.setStatus("name unchanged", false)
			return nil
		}
		if err := cloud.CheckName(v); err != nil {
			a.dialog.errLine = err.Error()
			return nil
		}
		a.dialog = nil
		return a.startCloudOp(idx, "Renaming", v, "Renamed to", loc.Display(), loc.Dir().Join(v).Display(), func(ctx context.Context) error {
			return client.Rename(ctx, loc, v)
		})
	}
	a.dialog = d
	return nil
}

// ---- delete on a remote ------------------------------------------------------------

func (a *App) beginCloudDelete() tea.Cmd {
	p := a.ActivePanel()
	idx := a.active
	targets := p.Targets()
	if len(targets) == 0 {
		a.setStatus("nothing to delete: put the cursor on an item or select items with Space", true)
		return nil
	}
	src := p.Source()
	dirs, files := 0, 0
	for _, e := range targets {
		if e.IsDir {
			dirs++
		} else {
			files++
		}
	}
	what := countPhrase(dirs, files)
	d := &dialog{kind: dlgDelete, danger: true, title: fmt.Sprintf("Delete %s from %s:?", what, src.Remote.Name)}
	d.body = append(d.body, warn(fmt.Sprintf("Delete %s on the remote %s:", what, src.Remote.Name)))
	for i, e := range targets {
		if i == listCap {
			d.body = append(d.body, hint(fmt.Sprintf("… and %d more", len(targets)-listCap)))
			break
		}
		s := cloudPath(p, e.Path).Display()
		if e.IsDir {
			s += "/"
		}
		d.body = append(d.body, path(s))
	}
	d.body = append(d.body, blank())
	if dirs > 0 {
		d.body = append(d.body, warn("Folders are deleted with ALL of their contents."))
	}
	d.body = append(d.body, text("Some providers keep deleted items in their own trash (Google Drive does by default); on others this CANNOT be undone."))
	cancelDel := func(a *App) tea.Cmd {
		a.dialog = nil
		a.setStatusLevel("Delete cancelled — nothing was deleted", lvlWarn)
		return nil
	}
	start := func(a *App) tea.Cmd {
		a.dialog = nil
		return a.startCloudDelete(idx, src.Client, p, targets)
	}
	d.onEsc = cancelDel
	if dirs > 0 || len(targets) > 1 {
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
		d.buttons = []button{
			{label: "No, cancel (n)", key: "n", run: cancelDel},
			{label: "Yes, delete (y)", key: "y", danger: true, run: start},
		}
		d.footer = "Enter = highlighted button (No) · y confirm · n/Esc cancel"
	}
	a.dialog = d
	return nil
}

func (a *App) startCloudDelete(idx int, c *cloud.Client, p *panel.Panel, targets []fsys.Entry) tea.Cmd {
	s, ctx := a.newSession("Deleting", idx, nil)
	s.to = "Remote provider deletion"
	for i, e := range targets {
		s.names[e.Name] = true
		if i == 0 {
			s.first = e.Name
		}
	}
	h := a.hooks(ctx, s)
	type job struct {
		loc   cloud.Path
		isDir bool
	}
	jobs := make([]job, len(targets))
	for i, e := range targets {
		loc := cloudPath(p, e.Path)
		jobs[i] = job{loc, e.IsDir}
		s.from = append(s.from, loc.Display())
	}
	go func() {
		var res cloudDeleteResult
		prog := ops.Progress{Phase: "deleting", TotalItems: len(jobs)}
		for _, j := range jobs {
			if ctx.Err() != nil {
				res.canceled = true
				break
			}
			prog.Current = j.loc.Display()
			h.Progress(prog)
			err := c.Delete(ctx, j.loc, j.isDir)
			switch {
			case err == nil:
				res.deleted++
			case cloud.KindOf(err) == cloud.KindCanceled:
				res.canceled = true
				res.errors = append(res.errors, ops.ItemError{Path: j.loc.Display(), Op: "delete", Err: errors.New("interrupted: it may be partly deleted")})
			default:
				res.errors = append(res.errors, ops.ItemError{Path: j.loc.Display(), Op: "delete", Err: err})
				prog.Errors++
				prog.LastError = errText(err)
				if cloud.KindOf(err).Fatal() {
					res.fatal = err
				}
			}
			if res.canceled || res.fatal != nil {
				break
			}
			prog.DoneItems++
			h.Progress(prog)
		}
		s.events <- opCloudDeleteDoneMsg{s.id, res}
	}()
	return a.startCmds(s)
}

func (a *App) finishCloudDelete(s *opSession, res cloudDeleteResult) tea.Cmd {
	p := a.panels[s.srcPanel]
	ok := !res.canceled && res.fatal == nil && len(res.errors) == 0
	focus := ""
	if p.Path() == s.panelPaths[s.srcPanel] {
		focus = p.NextFocus(s.names)
		if ok && a.sourceSelectionUnchanged(s) {
			p.ClearSelection()
		}
	}
	cmd := a.refreshPanelsAt(s.panelPaths[s.srcPanel], focus)
	summary := "Deleted " + plural(res.deleted, "item")
	details := itemErrorDetails(res.errors)
	details = appendTaskDetail(details, "Stopped", res.fatal)
	state := taskOutcome(res.canceled, res.fatal, len(res.errors), res.deleted > 0)
	switch {
	case ok:
		a.setStatusLevel(summary, lvlOK)
	case res.canceled && len(res.errors) <= 1 && res.fatal == nil:
		summary += " — cancelled, the rest was not deleted"
		a.setStatusLevel(summary, lvlWarn)
	default:
		summary = fmt.Sprintf("%s, %s", summary, plural(len(res.errors), "error"))
		if res.canceled {
			summary += " — cancelled"
		}
		a.setStatusLevel(summary, lvlErr)
	}
	a.finishTask(s, state, summary, details...)
	return cmd
}

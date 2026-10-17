package ui

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/editor"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/scrollbar"
)

// bulkRename is the overlay that renames several entries of one directory
// at once. Every change only edits the proposal; the plan is re-validated
// after each edit (ops.PlanBatchRename) and nothing on disk changes until
// the user applies it and confirms.
type bulkRename struct {
	panel    int
	dir      string
	items    []bulkItem
	plan     ops.RenamePlan
	cursor   int
	offset   int
	applying bool

	// remembered prompt values
	find, repl     string
	regex, matchCs bool
	template       string
}

type bulkItem struct {
	old, new string
	isDir    bool
}

type bulkDoneMsg struct {
	res ops.BatchResult
	err error
}

func (b *bulkRename) olds() []string {
	out := make([]string, len(b.items))
	for i, it := range b.items {
		out[i] = it.old
	}
	return out
}

func (b *bulkRename) news() []string {
	out := make([]string, len(b.items))
	for i, it := range b.items {
		out[i] = it.new
	}
	return out
}

func (b *bulkRename) setNews(ns []string) {
	for i := range b.items {
		b.items[i].new = ns[i]
	}
	b.replan()
}

func (b *bulkRename) replan() { b.plan = ops.PlanBatchRename(b.dir, b.olds(), b.news()) }

// beginRename renames the targets: one entry uses the simple rename dialog,
// several open the bulk rename overlay.
func (a *App) beginRename() tea.Cmd {
	if a.refuseInArchive("Renaming") {
		return nil
	}
	p := a.ActivePanel()
	targets := p.Targets()
	switch len(targets) {
	case 0:
		a.setStatus("nothing to rename: put the cursor on an item", true)
		return nil
	case 1:
		return a.renameOne(targets[0])
	}
	b := &bulkRename{panel: a.active, dir: p.Path(), template: "{name}{ext}"}
	for _, e := range targets {
		b.items = append(b.items, bulkItem{old: e.Name, new: e.Name, isDir: e.IsDir})
	}
	b.replan()
	a.bulk = b
	a.setStatus("", false)
	return nil
}

func (a *App) bulkKey(msg tea.KeyMsg) tea.Cmd {
	b := a.bulk
	if b.applying || msg.Paste {
		return nil
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 {
		if msg.Alt {
			return nil
		}
		var cmd tea.Cmd
		for i, r := range msg.Runes {
			if i > 0 && (r != 'j' && r != 'k' || a.bulk != b || a.dialog != nil) {
				break
			}
			cmd = a.bulkKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		return cmd
	}
	rows := a.bulkRows()
	switch msg.String() {
	case "ctrl+c":
		a.bulk = nil
		a.quitting = true
		return tea.Quit
	case "esc", "q":
		a.bulk = nil
		a.setStatus("bulk rename canceled: nothing was renamed", false)
		return nil
	case "up", "k":
		b.cursor--
	case "down", "j":
		b.cursor++
	case "pgup", "ctrl+b":
		b.cursor -= rows
	case "pgdown", "ctrl+f":
		b.cursor += rows
	case "home", "g":
		b.cursor = 0
	case "end", "G":
		b.cursor = len(b.items) - 1
	case "enter", "r", "f2":
		a.bulkEditRow()
	case "f":
		a.bulkFind()
	case "n":
		a.bulkTemplate()
	case "L":
		b.setNews(ops.LowerNames(b.news()))
		a.setStatus("lower-cased all new names", false)
	case "U":
		b.setNews(ops.UpperNames(b.news()))
		a.setStatus("upper-cased all new names", false)
	case "R":
		b.setNews(b.olds())
		a.setStatus("all new names reset to the current names", false)
	case "E":
		return a.bulkEditor()
	case "a":
		a.bulkConfirm()
	}
	b.clamp(rows)
	return nil
}

func (b *bulkRename) clamp(rows int) {
	if b.cursor >= len(b.items) {
		b.cursor = len(b.items) - 1
	}
	if b.cursor < 0 {
		b.cursor = 0
	}
	if rows < 1 {
		rows = 1
	}
	if b.cursor < b.offset {
		b.offset = b.cursor
	}
	if b.cursor >= b.offset+rows {
		b.offset = b.cursor - rows + 1
	}
}

// bulkPrompt opens a one-line prompt over the overlay.
func (a *App) bulkPrompt(title string, body []dline, label, initial string, cursor int,
	toggles []*inputToggle, submit func(a *App, v string) error) {
	d := &dialog{kind: dlgBulkEdit, title: title, body: body, width: 70}
	d.input, d.inputLabel = newLineInput(initial), label
	if cursor >= 0 {
		d.input.SetCursor(cursor)
	} else {
		d.input.SetCursor(len([]rune(initial)))
	}
	d.inToggles = toggles
	d.footer = "Enter OK · Esc cancel"
	if len(toggles) > 0 {
		var hs []string
		for _, t := range toggles {
			hs = append(hs, t.help+" "+strings.ToLower(t.label))
		}
		d.footer = "Enter OK · " + strings.Join(hs, " · ") + " · Esc cancel"
	}
	d.onSubmit = func(a *App, v string) tea.Cmd {
		if err := submit(a, v); err != nil {
			a.dialog.errLine = err.Error()
			return nil
		}
		if a.dialog == d {
			a.dialog = nil
		}
		return nil
	}
	a.dialog = d
}

func (a *App) bulkEditRow() {
	b := a.bulk
	it := &b.items[b.cursor]
	stem, _ := ops.SplitExt(it.new, it.isDir)
	a.bulkPrompt("New name", []dline{text("Current name: " + it.old)}, "New name:", it.new,
		len([]rune(stem)), nil, func(a *App, v string) error {
			it.new = v
			b.replan()
			return nil
		})
}

func (a *App) bulkFind() {
	b := a.bulk
	regex := &inputToggle{key: "ctrl+r", help: "^R", label: "Regular expression", on: b.regex}
	mcase := &inputToggle{key: "ctrl+t", help: "^T", label: "Match case", on: b.matchCs}
	a.bulkPrompt("Find and replace in the new names",
		[]dline{hint("Applies to every new name in the list. With regular expressions, the replacement can use $1, $2 … for groups.")},
		"Find:", b.find, -1, []*inputToggle{regex, mcase}, func(a *App, find string) error {
			if find == "" {
				return fmt.Errorf("enter the text to find")
			}
			if _, err := ops.ReplaceNames(nil, find, "", regex.on, mcase.on); err != nil {
				return err
			}
			b.find, b.regex, b.matchCs = find, regex.on, mcase.on
			a.dialog = nil
			mode := "text"
			if regex.on {
				mode = "regular expression"
			}
			a.bulkPrompt("Replace with", []dline{text(fmt.Sprintf("Find (%s): %s", mode, find))},
				"Replace with:", b.repl, -1, nil, func(a *App, repl string) error {
					ns, err := ops.ReplaceNames(b.news(), find, repl, regex.on, mcase.on)
					if err != nil {
						return err
					}
					changed := 0
					for i, n := range ns {
						if n != b.items[i].new {
							changed++
						}
					}
					b.repl = repl
					b.setNews(ns)
					a.setStatus(fmt.Sprintf("replaced in %s", plural(changed, "name")), false)
					return nil
				})
			return nil
		})
}

func (a *App) bulkTemplate() {
	b := a.bulk
	a.bulkPrompt("Rename with a pattern",
		[]dline{
			text("{name}  name without extension    {ext}  extension with its dot"),
			text("{n}     counter 1, 2, 3 …          {n:3}  zero-padded 001, 002 …"),
			hint("Applied to the new names in list order, e.g. photo-{n:3}{ext} or {name}-old{ext}."),
		},
		"Pattern:", b.template, -1, nil, func(a *App, tpl string) error {
			items := make([]ops.TemplateItem, len(b.items))
			for i, it := range b.items {
				items[i] = ops.TemplateItem{Name: it.new, IsDir: it.isDir}
			}
			ns, err := ops.ApplyTemplate(items, tpl)
			if err != nil {
				return err
			}
			b.template = tpl
			b.setNews(ns)
			a.setStatus("pattern applied", false)
			return nil
		})
}

// bulkEditor lets the user edit all new names in $VISUAL/$EDITOR, one per
// line.
func (a *App) bulkEditor() tea.Cmd {
	b := a.bulk
	for _, it := range b.items {
		if strings.ContainsAny(it.old+it.new, "\n\r") {
			a.setStatus("cannot edit the list in an editor: a name contains a line break", true)
			return nil
		}
	}
	ed, err := editor.Resolve(a.edEnv)
	if err != nil {
		a.setStatus("cannot edit: "+err.Error(), true)
		return nil
	}
	f, err := os.CreateTemp("", "twinpane-rename-*.txt")
	if err != nil {
		a.setStatus("cannot create the temporary list: "+errText(err), true)
		return nil
	}
	_, werr := f.WriteString(strings.Join(b.news(), "\n") + "\n")
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(f.Name())
		a.setStatus("cannot write the temporary list", true)
		return nil
	}
	return a.runEditor(ed, []string{f.Name()}, f.Name(), false)
}

// bulkEditorDone reads back the list edited in the external editor.
func (a *App) bulkEditorDone(m editorDoneMsg) {
	defer os.Remove(m.bulkFile)
	b := a.bulk
	if b == nil {
		return
	}
	if m.err != nil {
		a.setStatus(editor.DescribeExit(m.editor, m.err)+"; new names unchanged", true)
		return
	}
	data, err := os.ReadFile(m.bulkFile)
	if err != nil {
		a.setStatus("cannot read the edited list: "+errText(err)+"; new names unchanged", true)
		return
	}
	ns, err := ops.ParseNameList(string(data), len(b.items))
	if err != nil {
		a.setStatus(err.Error()+"; new names unchanged", true)
		return
	}
	if strings.Join(ns, "\n") == strings.Join(b.news(), "\n") {
		msg := "the list was not changed in the editor"
		if m.hint.Known {
			msg += " (to save in " + m.hint.Name + ": " + m.hint.Save + ")"
		}
		delete(a.tipsShown, m.tipKey)
		a.setStatus(msg, false)
		return
	}
	b.setNews(ns)
	a.setStatusLevel("new names taken from the editor — nothing renamed yet: press a, then y to apply", lvlOK)
}

func (a *App) bulkConfirm() {
	b := a.bulk
	b.replan() // re-check against the filesystem as it is now
	if b.plan.Errors > 0 {
		for i, it := range b.plan.Items {
			if it.Status.IsError() {
				b.cursor = i
				break
			}
		}
		a.setStatus(fmt.Sprintf("%s must be fixed before renaming (see the ✗ rows)", plural(b.plan.Errors, "problem")), true)
		return
	}
	if b.plan.Changes == 0 {
		a.setStatus("no new names yet: edit some names first", true)
		return
	}
	d := &dialog{kind: dlgBulkConfirm, title: fmt.Sprintf("Rename %s?", plural(b.plan.Changes, "item")), width: 72}
	d.body = []dline{text("In folder:"), path(b.dir), blank()}
	shown := 0
	for _, it := range b.plan.Items {
		if it.Status != ops.RenameOK {
			continue
		}
		if shown == 8 {
			d.body = append(d.body, text(fmt.Sprintf("… and %d more", b.plan.Changes-shown)))
			break
		}
		d.body = append(d.body, text(it.Old+"  →  "+it.New))
		shown++
	}
	if un := len(b.items) - b.plan.Changes; un > 0 {
		d.body = append(d.body, blank(), text(fmt.Sprintf("%s keep their names.", plural(un, "item"))))
	}
	d.body = append(d.body, blank(), hint("Existing files are never overwritten. Swaps and case-only changes use a temporary name in the same folder."))
	d.buttons = []button{
		{label: "No (Esc)", run: func(a *App) tea.Cmd { a.dialog = nil; return nil }},
		{label: "Rename (y)", key: "y", run: func(a *App) tea.Cmd {
			a.dialog = nil
			return a.bulkApply()
		}},
	}
	d.footer = "y = rename now · Enter = highlighted button · Esc = back to the list (nothing renamed)"
	a.dialog = d
}

func (a *App) bulkApply() tea.Cmd {
	b := a.bulk
	b.applying = true
	plan := b.plan
	a.setStatus(fmt.Sprintf("renaming %s…", plural(plan.Changes, "item")), false)
	return func() tea.Msg {
		res, err := ops.ApplyBatchRename(context.Background(), plan)
		return bulkDoneMsg{res: res, err: err}
	}
}

func (a *App) bulkDone(m bulkDoneMsg) tea.Cmd {
	b := a.bulk
	if b == nil {
		return nil
	}
	a.bulk = nil
	p := a.panels[b.panel]
	other := a.panels[1-b.panel]
	// Keep the panel cursor on the same item under its new name.
	focus := ""
	if e, ok := p.Current(); ok {
		focus = e.Name
		for _, r := range m.res.Items {
			if r.Old == e.Name && r.Outcome == ops.OutcomeRenamed {
				focus = r.New
			}
		}
	}
	p.ClearSelection()
	cmds := tea.Batch(p.RefreshFocus(focus), other.Refresh())
	if m.err != nil {
		a.setStatus("bulk rename refused: "+m.err.Error(), true)
		return cmds
	}
	if m.res.Failed() == 0 {
		a.setStatusLevel(fmt.Sprintf("Renamed %s", plural(m.res.Renamed, "item")), lvlOK)
		return cmds
	}
	body := []dline{text(fmt.Sprintf("Renamed %d of %d items in:", m.res.Renamed, len(m.res.Items))), path(b.dir), blank()}
	for _, r := range m.res.Items {
		switch r.Outcome {
		case ops.OutcomeFailed:
			body = append(body, warn(fmt.Sprintf("✗ %s → %s: %s", r.Old, r.New, errText(r.Err))))
		case ops.OutcomeBlocked:
			body = append(body, warn(fmt.Sprintf("✗ %s → %s: not renamed, because the item holding %q could not be renamed", r.Old, r.New, r.New)))
		case ops.OutcomeCanceled:
			body = append(body, warn(fmt.Sprintf("✗ %s → %s: canceled", r.Old, r.New)))
		}
		if r.Stranded != "" {
			body = append(body, warn(fmt.Sprintf("  %s is now named %q (its original name was taken); rename it by hand", r.Old, r.Stranded)))
		}
	}
	a.setStatus(fmt.Sprintf("bulk rename: %d renamed, %d failed", m.res.Renamed, m.res.Failed()), true)
	a.showReport("Bulk rename finished with problems", body)
	return cmds
}

// bulkRows is the number of list rows that fit on screen.
func (a *App) bulkRows() int {
	n := a.height - 3 - 2 - 7 // title/status/footer; box border; title, folder, summary, columns, detail, 2 hint lines
	if n < 1 {
		n = 1
	}
	return n
}

func (a *App) bulkView(h int) string {
	b := a.bulk
	th := a.theme
	innerW := a.width - 4
	if innerW < 20 {
		innerW = 20
	}
	rows := a.bulkRows()
	b.clamp(rows)

	head := th.HelpTitle.Render(truncRight(fmt.Sprintf("Bulk rename: %d items", len(b.items)), innerW))
	where := th.Info.Render(truncLeft("in "+fsys.SanitizeName(b.dir), innerW))
	sum := fmt.Sprintf("%d to rename · %d unchanged", b.plan.Changes, len(b.items)-b.plan.Changes-b.plan.Errors)
	sumSt := th.Info
	if b.plan.Errors > 0 {
		sum += fmt.Sprintf(" · %s", plural(b.plan.Errors, "problem"))
		sumSt = th.Error
	}
	if b.applying {
		sum = "Renaming…"
	}

	colW := (innerW - 1 - 3 - 2) / 2
	header := th.ColumnHeader.Render(truncRight(" "+padRight("Current name", colW)+"   "+padRight("New name", colW)+"  ", innerW))
	lines := []string{head, where, sumSt.Render(truncRight(sum, innerW)), header}
	for i := b.offset; i < b.offset+rows; i++ {
		if i >= len(b.items) {
			lines = append(lines, strings.Repeat(" ", innerW))
			continue
		}
		it := b.plan.Items[i]
		glyph, st := " ", th.File
		switch {
		case it.Status == ops.RenameOK:
			glyph = "✓"
		case it.Status.IsError():
			glyph, st = "✗", th.Error
		default:
			st = th.Hidden
		}
		oldN, newN := fsys.SanitizeName(it.Old), fsys.SanitizeName(it.New)
		if b.items[i].isDir {
			oldN += "/"
			newN += "/"
		}
		line := " " + padRight(truncRight(oldN, colW), colW) + " → " + padRight(truncRight(newN, colW), colW) + " " + glyph
		line = padRight(truncRight(line, innerW), innerW)
		if i == b.cursor {
			st = th.CursorActive
		}
		lines = append(lines, st.Render(line))
	}
	// detail for the row under the cursor
	var detail string
	detailSt := th.Info
	if len(b.items) > 0 {
		it := b.plan.Items[b.cursor]
		switch {
		case it.Status == ops.RenameOK:
			detail = "will be renamed"
		case it.Status.IsError():
			detail, detailSt = "✗ "+it.Reason, th.Error
		default:
			detail = "unchanged"
		}
		detail = fsys.SanitizeName(it.Old) + ": " + detail
	}
	lines = append(lines, detailSt.Render(truncRight(detail, innerW)),
		th.TitleText.Render(firstFit(innerW,
			"Enter edit name · f find/replace · n pattern · L/U lower/upper case · E edit all in $EDITOR · R reset",
			"Enter edit · f find/replace · n pattern · L/U case · E editor · R reset",
			"⏎ edit · f replace · n pattern · L/U · E · R")),
		a.bulkCommitLine(innerW))
	// scroll indicator in the right padding column of the list rows
	cells := scrollbar.Cells(len(b.items), rows, b.offset, rows)
	for i := range lines {
		col := " "
		if j := i - 4; j >= 0 && j < len(cells) && cells[j] {
			col = th.ScrollThumb.Render(panel.ScrollThumbChar)
		}
		lines[i] = padRight(lines[i], innerW) + col
	}
	box := th.HelpBox.Padding(0, 0, 0, 1).Width(a.width - 2).Render(strings.Join(lines, "\n"))
	return lipgloss.NewStyle().MaxHeight(h).MaxWidth(a.width).Render(box)
}

// bulkCommitLine is the always-visible guidance for finishing a bulk
// rename: which key applies (and that it asks for confirmation) and which
// cancels. It matches bulkKey and bulkConfirm: a opens the confirmation,
// whose y renames (Enter there presses the highlighted button, "No" by
// default); Esc or q leaves without renaming anything.
func (a *App) bulkCommitLine(w int) string {
	th := a.theme
	// inline chips inside a sentence keep one cell of padding (the hint
	// bar's chips have theme.FooterKeyPad)
	key := func(k string) string { return th.FooterKey.Padding(0, 1).Render(k) }
	ok, info, warn := th.StatusOK.Render, th.Info.Render, th.StatusWarn.Render
	var variants []string
	switch {
	case a.bulk.applying:
		variants = []string{info("Renaming…")}
	case a.bulk.plan.Errors > 0:
		variants = []string{
			warn("Fix the ✗ rows before applying") + info("  ·  Cancel: ") + key("Esc") + info(" — nothing is renamed"),
			warn("Fix the ✗ rows first") + info(" · ") + key("Esc") + info(" cancel"),
		}
	case a.bulk.plan.Changes == 0:
		variants = []string{
			info("No new names yet — edit some first  ·  Cancel: ") + key("Esc"),
			info("No new names yet · ") + key("Esc") + info(" cancel"),
		}
	default:
		variants = []string{
			ok("Apply: ") + key("a") + ok(" then ") + key("y") + ok(" to confirm") +
				info("  ·  Cancel: ") + key("Esc") + info(" — nothing is renamed until you confirm"),
			ok("Apply: ") + key("a") + ok(" then ") + key("y") + info("  ·  Cancel: ") + key("Esc") + info(" (nothing renamed yet)"),
			key("a") + ok(" apply, ") + key("y") + ok(" confirm") + info(" · ") + key("Esc") + info(" cancel"),
		}
	}
	for _, v := range variants {
		if ansi.StringWidth(v) <= w {
			return v
		}
	}
	return ansi.Truncate(variants[len(variants)-1], w, "…")
}

// firstFit returns the first text that fits w cells (else the last one,
// truncated), as a muted hint line.
func firstFit(w int, texts ...string) string {
	for _, t := range texts {
		if ansi.StringWidth(t) <= w {
			return t
		}
	}
	return truncRight(texts[len(texts)-1], w)
}

func padRight(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

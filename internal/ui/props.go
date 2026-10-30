package ui

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/archive"
	"github.com/ThatOn3Gu7/TwinPane/internal/fileinfo"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/history"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// The Properties dialog (phase 5, key P): metadata of the item under the
// cursor, renaming it and changing its permissions. Nothing changes until
// Apply (Enter or the Apply button); Esc / Close leaves without changes.
// For an archive entry the dialog is read-only.

// focus positions in the dialog
const (
	pfName  = 0
	pfCell0 = 1 // 1..9: permission boxes (owner r w x, group r w x, others r w x)
	pfApply = 10
	pfClose = 11
)

type propsView struct {
	panel int
	info  *fileinfo.Info // filesystem item (nil for an archive entry)

	arc      *archive.Archive // archive entry (read-only)
	arcEntry *archive.Entry
	arcName  string

	name  *lineInput
	perm  fs.FileMode // pending permission bits (incl. special bits)
	focus int

	msg    string
	msgLvl int

	openedAt time.Time
	hits     []propsHit
	press    *propsHit
	y0, y1   int // screen rows covered by the box
	scroll   int
	clipped  bool // the box was too tall for the screen (scrolls)
}

type propsHit struct {
	y, x0, x1 int
	target    int // a pf* focus position
}

// openProps is P: properties of the item under the cursor (or the only
// selected item when the cursor is on "..").
func (a *App) openProps() tea.Cmd {
	p := a.ActivePanel()
	e, ok := p.Current()
	if !ok || e.IsParent {
		if t := p.Targets(); len(t) > 0 {
			e, ok = t[0], true
		} else {
			ok = false
		}
	}
	if !ok {
		a.setStatus("no item: put the cursor on a file or folder for its properties", true)
		return nil
	}
	pv := &propsView{panel: a.active, openedAt: a.now(), focus: pfClose}
	if p.InArchive() {
		arc := p.Archive()
		inner, ok := arc.InnerOf(e.Path)
		ent, found := arc.Lookup(inner)
		if !ok || !found {
			a.setStatus("cannot read this archive entry", true)
			return nil
		}
		pv.arc, pv.arcEntry, pv.arcName = arc, ent, e.Name
		a.props = pv
		return nil
	}
	in, err := fileinfo.Stat(e.Path)
	if err != nil {
		a.setStatus("cannot read the properties: "+errText(err), true)
		return nil
	}
	pv.info = in
	pv.reset()
	pv.focus = pfName
	a.props = pv
	a.setStatus("", false)
	return nil
}

// reset discards pending edits (after opening and after Apply).
func (pv *propsView) reset() {
	old := pv.name
	pv.name = newLineInput(pv.info.Name)
	if old == nil {
		if ext := filepath.Ext(pv.info.Name); !pv.info.IsDir && ext != "" && ext != pv.info.Name {
			pv.name.SetCursor(len([]rune(pv.info.Name)) - len([]rune(ext)))
		}
	}
	pv.perm = pv.info.Mode & (fs.ModePerm | fileinfo.Special)
}

func (pv *propsView) readOnly() bool { return pv.info == nil }

func (pv *propsView) dirty() bool {
	if pv.readOnly() {
		return false
	}
	return pv.name.Value() != pv.info.Name || (pv.info.ChmodOK && pv.perm != pv.info.Mode&(fs.ModePerm|fileinfo.Special))
}

// focusables lists the focus positions in Tab order.
func (pv *propsView) focusables() []int {
	if pv.readOnly() {
		return []int{pfClose}
	}
	out := []int{pfName}
	if pv.info.ChmodOK {
		for i := 0; i < 9; i++ {
			out = append(out, pfCell0+i)
		}
	}
	return append(out, pfApply, pfClose)
}

func (pv *propsView) cycle(dir int) {
	order := pv.focusables()
	at := 0
	for i, f := range order {
		if f == pv.focus {
			at = i
		}
	}
	pv.focus = order[((at+dir)%len(order)+len(order))%len(order)]
}

func permBit(cell int) fs.FileMode { return 1 << uint(8-cell) }

func (pv *propsView) setMsg(msg string, lvl int) { pv.msg, pv.msgLvl = msg, lvl }

func (a *App) closeProps() {
	pv := a.props
	a.props = nil
	if pv != nil && pv.dirty() {
		a.setStatusLevel("Properties closed — the unapplied changes were discarded", lvlWarn)
	}
}

func (a *App) propsKey(msg tea.KeyMsg) tea.Cmd {
	pv := a.props
	switch {
	case a.is("browse.interrupt", msg):
		a.quitting = true
		return tea.Quit
	case a.is("props.close", msg):
		a.closeProps()
		return nil
	case a.is("props.nextField", msg):
		pv.cycle(1)
		return nil
	case a.is("props.prevField", msg):
		pv.cycle(-1)
		return nil
	}
	switch {
	case pv.focus == pfName && !pv.readOnly():
		switch {
		case a.is("props.apply", msg):
			return a.propsApply()
		case a.is("props.down", msg):
			pv.cycle(1)
			return nil
		case a.is("props.up", msg):
			return nil
		}
		if pv.name.Update(msg) {
			pv.msg = ""
		}
		return nil
	case pv.focus >= pfCell0 && pv.focus < pfCell0+9:
		cell := pv.focus - pfCell0
		r, c := cell/3, cell%3
		switch {
		case a.is("props.toggle", msg):
			pv.perm ^= permBit(cell)
			pv.msg = ""
		case a.is("props.apply", msg):
			return a.propsApply()
		case a.is("props.left", msg):
			if c > 0 {
				pv.focus--
			}
		case a.is("props.right", msg):
			if c < 2 {
				pv.focus++
			}
		case a.is("props.up", msg):
			if r > 0 {
				pv.focus -= 3
			} else {
				pv.focus = pfName
			}
		case a.is("props.down", msg):
			if r < 2 {
				pv.focus += 3
			} else {
				pv.focus = pfApply
			}
		}
		return nil
	default: // buttons
		switch {
		case a.is("props.press", msg):
			if pv.focus == pfApply {
				return a.propsApply()
			}
			a.closeProps()
			return nil
		case a.is("props.left", msg), a.is("props.right", msg):
			if !pv.readOnly() {
				if pv.focus == pfApply {
					pv.focus = pfClose
				} else {
					pv.focus = pfApply
				}
			}
		case a.is("props.up", msg):
			if !pv.readOnly() {
				if pv.info.ChmodOK {
					pv.focus = pfCell0 + 6
				} else {
					pv.focus = pfName
				}
			} else {
				pv.scroll--
			}
		case a.is("props.down", msg):
			if pv.readOnly() {
				pv.scroll++
			}
		case a.is("props.closeReadOnly", msg):
			if pv.readOnly() {
				a.closeProps()
			}
		}
	}
	return nil
}

// propsApply commits the pending rename and permission change. The rename
// never replaces an existing item (ops.Rename); permissions are verified
// after the change (fileinfo.Chmod).
func (a *App) propsApply() tea.Cmd {
	pv := a.props
	if pv.readOnly() {
		return nil
	}
	in := pv.info
	newName := pv.name.Value()
	wantRename := newName != in.Name
	wantPerm := in.ChmodOK && pv.perm != in.Mode&(fs.ModePerm|fileinfo.Special)
	if !wantRename && !wantPerm {
		pv.setMsg("Nothing to apply: edit the name or the permissions first", lvlInfo)
		return nil
	}
	p := a.panels[pv.panel]
	path := in.Path
	started := a.now()
	var done []string
	if wantRename {
		if err := ops.Rename(in.Dir, in.Name, newName); err != nil {
			a.recordRename("Rename", started, in.Path, "", err)
			pv.setMsg("Not renamed: "+errText(err), lvlErr)
			pv.focus = pfName
			return nil
		}
		a.recordRename("Rename", started, in.Path, filepath.Join(in.Dir, newName), nil)
		done = append(done, fmt.Sprintf("renamed to %q", newName))
		path = filepath.Join(in.Dir, newName)
		if p.IsSelected(in.Name) {
			p.ClearSelection()
		}
	}
	var permErr error
	if wantPerm {
		permStarted := a.now()
		if permErr = fileinfo.Chmod(path, pv.perm); permErr == nil {
			done = append(done, "permissions set to "+fileinfo.Symbolic(pv.perm)+" ("+fileinfo.Octal(pv.perm)+")")
		}
		a.recordOperation(history.Record{
			Op:      "Permissions",
			Started: permStarted,
			Sources: []string{path},
			Dest:    filepath.Dir(path),
			Items:   []string{filepath.Base(path)},
			Status:  historyStatusFor(permErr),
			Summary: permSummary(path, pv.perm, permErr),
			Errors:  historyErrors(permErr),
			Result:  fileinfo.Symbolic(pv.perm) + " (" + fileinfo.Octal(pv.perm) + ")",
		})
	}
	// show what is on disk now
	if ni, err := fileinfo.Stat(path); err == nil {
		pv.info = ni
	} else {
		pv.info.Path, pv.info.Name = path, filepath.Base(path)
	}
	pv.name = nil
	pv.reset()
	switch {
	case permErr != nil && len(done) > 0:
		pv.setMsg(capitalize(strings.Join(done, ", "))+" — but the permissions were not changed: "+errText(permErr), lvlWarn)
	case permErr != nil:
		pv.setMsg("Permissions not changed: "+errText(permErr), lvlErr)
	default:
		pv.setMsg(capitalize(strings.Join(done, ", ")), lvlOK)
	}
	focus := ""
	if cur, ok := p.Current(); ok {
		focus = cur.Name
		if cur.Name == in.Name {
			focus = pv.info.Name
		}
	}
	return tea.Batch(p.RefreshFocus(focus), a.panels[1-pv.panel].Refresh())
}

// ---- rendering --------------------------------------------------------------------

type propsLine struct {
	s    string
	hits []propsHit // y unset (line relative); x relative to the content
}

const propsLabelW = 10

func (a *App) renderProps() string {
	pv := a.props
	th := a.theme
	cw := min(64, max(24, a.width-8)) // content width
	label := func(s string) string { return th.PropLabel.Render(padRight(s, propsLabelW)) }
	valW := cw - propsLabelW
	field := func(l, v string) propsLine {
		return propsLine{s: label(l) + th.DialogText.Render(truncRight(sanitize(v), valW))}
	}
	fieldPath := func(l, v string) propsLine {
		return propsLine{s: label(l) + th.DialogPath.Render(truncLeft(sanitize(v), valW))}
	}
	var lines []propsLine
	focusLine := 0
	title := "Properties"
	if pv.readOnly() {
		title = "Properties (archive entry, read-only)"
	}
	lines = append(lines, propsLine{s: th.DialogTitle.Render(truncRight(title, cw))}, propsLine{})

	if pv.readOnly() {
		lines = append(lines, a.archivePropsLines(field, fieldPath)...)
	} else {
		in := pv.info
		// name field
		nameLine := propsLine{s: label("Name")}
		if pv.focus == pfName {
			nameLine.s += pv.name.View(th, valW)
			focusLine = len(lines)
		} else {
			nameLine.s += th.Input.Render(padRight(truncRight(sanitize(pv.name.Value()), valW), valW))
		}
		nameLine.hits = []propsHit{{x0: propsLabelW, x1: cw, target: pfName}}
		lines = append(lines, nameLine)
		lines = append(lines, fieldPath("Folder", in.Dir))
		typ := in.Type
		if in.IsLink {
			if in.TargetType != "" {
				typ += " → " + in.TargetType
			} else {
				typ += " (broken)"
			}
		}
		lines = append(lines, field("Type", typ))
		if in.IsLink {
			lines = append(lines, fieldPath("Link to", in.LinkTarget))
		}
		switch {
		case in.Items >= 0:
			lines = append(lines, field("Contains", plural(in.Items, "item")+" (not counting subfolders' contents)"))
		case in.ItemsErr != nil:
			lines = append(lines, field("Contains", "unknown: "+errText(in.ItemsErr)))
		case !in.IsDir:
			lines = append(lines, field("Size", sizeWithBytes(in.Size)))
		}
		lines = append(lines, field("Modified", in.ModTime.Format("2006-01-02 15:04:05")))
		if in.UID >= 0 {
			lines = append(lines, field("Owner", idName(in.Owner, in.UID)+" · group "+idName(in.Group, in.GID)))
		} else {
			lines = append(lines, field("Owner", "not available on this system"))
		}
		lines = append(lines, propsLine{})

		// permissions
		cur := in.Mode & (fs.ModePerm | fileinfo.Special)
		perm := label("Access") + th.DialogText.Render(fileinfo.Symbolic(pv.perm)+"  "+fileinfo.Octal(pv.perm))
		if pv.perm != cur && in.ChmodOK {
			perm += th.DialogWarn.Render("  (now " + fileinfo.Octal(cur) + ")")
		}
		lines = append(lines, propsLine{s: perm})
		if in.ChmodOK {
			lines = append(lines, propsLine{s: label("") + th.PropLabel.Render("read   write  exec")})
			for r, who := range []string{"owner", "group", "others"} {
				l := propsLine{s: label("  " + who)}
				x := propsLabelW
				for c := 0; c < 3; c++ {
					cell := r*3 + c
					box := "[ ]"
					st := th.DialogText
					if pv.perm&permBit(cell) != 0 {
						box, st = "[x]", th.CheckOn
					}
					if pv.focus == pfCell0+cell {
						st = th.CheckFocus
						focusLine = len(lines)
					}
					if c > 0 {
						l.s += "    "
						x += 4
					}
					l.s += st.Render(box)
					l.hits = append(l.hits, propsHit{x0: x, x1: x + 3, target: pfCell0 + cell})
					x += 3
				}
				lines = append(lines, l)
			}
			if cur&fileinfo.Special != 0 {
				lines = append(lines, propsLine{s: th.DialogHint.Render(truncRight("Special bits (setuid/setgid/sticky) are kept as they are.", cw))})
			}
		} else {
			for _, wl := range wrapText("Cannot be changed here: "+in.ChmodWhy, cw) {
				lines = append(lines, propsLine{s: th.DialogHint.Render(wl)})
			}
		}
	}

	// buttons
	lines = append(lines, propsLine{})
	btn := func(lbl string, f int) (string, int) {
		st := th.Button
		if pv.focus == f {
			st = th.ButtonFocus
		}
		s := st.Render(lbl)
		return s, lipgloss.Width(s)
	}
	bl := propsLine{}
	x := 0
	if !pv.readOnly() {
		s, w := btn("Apply ⏎", pfApply)
		bl.s += s + "  "
		bl.hits = append(bl.hits, propsHit{x0: 0, x1: w, target: pfApply})
		x = w + 2
	}
	s, w := btn("Close Esc", pfClose)
	bl.s += s
	bl.hits = append(bl.hits, propsHit{x0: x, x1: x + w, target: pfClose})
	if pv.focus == pfApply || pv.focus == pfClose {
		focusLine = len(lines)
	}
	lines = append(lines, bl)

	// feedback
	msg, lvl := pv.msg, pv.msgLvl
	if msg == "" && pv.dirty() {
		msg, lvl = "Changes not applied yet: Enter applies, Esc discards", lvlWarn
	}
	if msg != "" {
		st, mark := th.DialogHint, ""
		switch lvl {
		case lvlOK:
			st, mark = th.StatusOK, "✓ "
		case lvlWarn:
			st, mark = th.StatusWarn, "! "
		case lvlErr:
			st, mark = th.StatusErr, "✗ "
		}
		for _, wl := range wrapText(mark+sanitize(msg), cw) {
			lines = append(lines, propsLine{s: st.Render(wl)})
		}
	}

	// fit the height: keep the title, scroll the rest so the focus shows
	maxLines := max(3, a.height-4)
	if len(lines) > maxLines {
		body := lines[1:]
		room := maxLines - 1
		fl := max(0, focusLine-1)
		maxScroll := len(body) - room
		if pv.readOnly() {
			pv.scroll = min(max(0, pv.scroll), maxScroll)
		} else {
			if fl < pv.scroll {
				pv.scroll = fl
			}
			if fl >= pv.scroll+room {
				pv.scroll = fl - room + 1
			}
			pv.scroll = min(max(0, pv.scroll), maxScroll)
		}
		lines = append([]propsLine{lines[0]}, body[pv.scroll:pv.scroll+room]...)
		pv.clipped = true
	} else {
		pv.scroll, pv.clipped = 0, false
	}

	strs := make([]string, len(lines))
	for i, l := range lines {
		strs[i] = l.s + strings.Repeat(" ", max(0, cw-ansi.StringWidth(l.s)))
	}
	box := th.DialogBox.Render(strings.Join(strs, "\n"))
	// record the clickable regions in screen coordinates
	ox, oy := overlayOrigin(box, a.width, a.height)
	pv.hits = pv.hits[:0]
	for i, l := range lines {
		for _, h := range l.hits {
			pv.hits = append(pv.hits, propsHit{y: oy + 1 + i, x0: ox + 3 + h.x0, x1: ox + 3 + h.x1, target: h.target})
		}
	}
	pv.y0, pv.y1 = oy, oy+lipgloss.Height(box)
	return box
}

func (a *App) archivePropsLines(field, fieldPath func(l, v string) propsLine) []propsLine {
	pv := a.props
	e := pv.arcEntry
	var lines []propsLine
	lines = append(lines, field("Name", pv.arcName))
	lines = append(lines, fieldPath("In", pv.arc.Display(innerDir(e.Inner))))
	typ := "file"
	switch {
	case e.IsDir && e.Implicit:
		typ = "folder (implied by the paths of its contents)"
	case e.IsDir:
		typ = "folder"
	case e.Symlink:
		typ = "symbolic link (stored; never extracted as a link)"
	}
	lines = append(lines, field("Type", typ))
	if !e.IsDir {
		lines = append(lines, field("Size", sizeWithBytes(int64(e.Size))))
		packed := fsys.HumanSize(int64(e.Packed))
		if e.Size > 0 {
			packed += fmt.Sprintf(" (%d%%)", int(float64(e.Packed)*100/float64(e.Size)))
		}
		lines = append(lines, field("Packed", packed+" · "+e.MethodName()))
		lines = append(lines, field("CRC-32", fmt.Sprintf("%08x", e.CRC32)))
	} else {
		lines = append(lines, field("Contains", plural(len(pv.arc.Children(e.Inner)), "item")))
	}
	if !e.Modified.IsZero() {
		lines = append(lines, field("Modified", e.Modified.Format("2006-01-02 15:04:05")))
	}
	if e.Mode.Perm() != 0 {
		lines = append(lines, field("Stored", fileinfo.Symbolic(e.Mode)+"  "+fileinfo.Octal(e.Mode)+" (extracted as 0644 / 0755)"))
	}
	if e.Encrypted {
		lines = append(lines, field("Encrypted", "yes (cannot be viewed or extracted)"))
	} else if err := e.Supported(); err != nil && !e.IsDir {
		lines = append(lines, field("Note", errText(err)))
	}
	if e.Comment != "" {
		lines = append(lines, field("Comment", e.Comment))
	}
	lines = append(lines, propsLine{}, propsLine{s: a.theme.DialogHint.Render(truncRight("Read-only: extract it (x) to change it.", 64))})
	return lines
}

func sizeWithBytes(n int64) string {
	if n < 1024 {
		return plural(int(n), "byte")
	}
	return fmt.Sprintf("%s (%s bytes)", fsys.HumanSize(n), groupDigits(n))
}

// groupDigits writes 1234567 as "1 234 567".
func groupDigits(n int64) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func idName(name string, id int) string {
	if name == "" {
		return fmt.Sprint(id)
	}
	return fmt.Sprintf("%s (%d)", name, id)
}

// ---- mouse / touch ------------------------------------------------------------

func (a *App) propsCovers(y int) bool {
	pv := a.props
	return pv != nil && y >= pv.y0 && y < pv.y1
}

func (a *App) propsMouse(ev tea.MouseEvent) tea.Cmd {
	pv := a.props
	if ev.IsWheel() {
		if pv.readOnly() {
			pv.scroll += wheelDelta(ev)
		}
		return nil
	}
	if ev.Button != tea.MouseButtonLeft && ev.Action != tea.MouseActionRelease {
		return nil
	}
	var hit *propsHit
	for i := range pv.hits {
		if h := &pv.hits[i]; h.y == ev.Y && ev.X >= h.x0 && ev.X < h.x1 {
			hit = h
		}
	}
	switch ev.Action {
	case tea.MouseActionPress:
		pv.press = nil
		if hit == nil || a.now().Sub(pv.openedAt) < dialogGuard {
			return nil
		}
		hh := *hit
		pv.press = &hh
		pv.focus = hit.target // show what will act
		return nil
	case tea.MouseActionRelease:
		pr := pv.press
		pv.press = nil
		if pr == nil || hit == nil || pr.target != hit.target || pr.y != hit.y {
			return nil
		}
		switch t := hit.target; {
		case t == pfApply:
			return a.propsApply()
		case t == pfClose:
			a.closeProps()
		case t >= pfCell0 && t < pfCell0+9:
			pv.perm ^= permBit(t - pfCell0)
			pv.msg = ""
		}
	}
	return nil
}

func (a *App) propsHints() []barItem {
	pv := a.props
	if pv.readOnly() {
		out := []barItem{a.bind("props.close", "close")}
		if pv.clipped {
			out = append(out, item("↑↓", "scroll", ""))
		}
		return out
	}
	out := []barItem{a.bind("props.apply", "apply"), a.bind("props.close", "close")}
	switch {
	case pv.focus == pfName:
		out = append(out, a.bind("props.nextField", "next field"), item("type", "new name", ""))
	case pv.focus >= pfCell0 && pv.focus < pfCell0+9:
		out = append(out, a.bind("props.toggle", "toggle"), item("←↑↓→", "move", ""), a.bind("props.nextField", "next"))
	default:
		if pv.focus == pfClose {
			out[0] = a.bind("props.apply", "close")
		}
		out = append(out, item("←→", "choose", ""), a.bind("props.nextField", "next"))
	}
	return out
}

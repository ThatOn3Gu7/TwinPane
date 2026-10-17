// Package panel implements a single, self-contained file panel: directory
// state, cursor/scroll handling, selection and rendering. Two instances make
// up TwinPane's dual-pane view; nothing in here knows about the other panel.
package panel

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/archive"
	"github.com/ThatOn3Gu7/TwinPane/internal/cloud"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
)

// LoadMode controls how a finished directory load is applied.
type LoadMode int

const (
	// LoadNavigate: on failure keep the current directory and report the error.
	LoadNavigate LoadMode = iota
	// LoadForce: on failure still switch to the path and show the error inside
	// the panel (used for the initial directory).
	LoadForce
	// LoadRefresh: re-read the current directory, keeping cursor and
	// selection; if it vanished, climb to the nearest existing ancestor.
	LoadRefresh
)

// LoadedMsg is delivered to the Bubble Tea update loop when an asynchronous
// directory read completes.
type LoadedMsg struct {
	PanelID int
	Seq     uint64
	Path    string // requested path
	Focus   string // entry name to place the cursor on, if present
	Mode    LoadMode
	Listing fsys.Listing
	Err     error

	// Phase 5: archive browsing. Arc is the archive the listing comes from
	// (nil = the real filesystem); Warn is reported by Apply even though
	// the load succeeded (e.g. the archive vanished and the panel fell back
	// to its folder).
	Arc  *archive.Archive
	Warn error

	// Phase 9: Rem is the rclone remote the listing comes from (nil = the
	// local filesystem or an archive).
	Rem *RemoteSource
}

// RemoteSource is the rclone remote a panel shows.
type RemoteSource struct {
	Client *cloud.Client
	Remote cloud.Remote
}

// Panel is one side of the dual-pane view.
type Panel struct {
	id     int
	lister fsys.Lister
	opts   fsys.Options
	now    func() time.Time

	path    string
	listing fsys.Listing
	entries []fsys.Entry
	err     error // error for the current path (shown inside the panel)

	cursor int
	offset int
	sel    *selection // may be shared with the other panel (see ShareSelection)

	seq     uint64 // sequence of the most recent load request
	loading bool

	// arc is set while the panel shows the inside of a ZIP archive (a
	// virtual directory). Paths are then virtual: archive path + "/" +
	// inner path (see archive.VirtualPath). The UI checks InArchive before
	// any operation that would treat them as real paths.
	arc *archive.Archive

	// rem is set while the panel shows an rclone remote (phase 9). Paths
	// are then rclone arguments ("remote:dir/name", see cloud.Path); the UI
	// checks InRemote before any operation that needs real paths.
	rem        *RemoteSource
	lastLocal  string             // the last local folder shown (to return to)
	loadCancel context.CancelFunc // stops the remote listing in progress
	localBadge bool               // label a local panel "LOCAL" (set by the UI)
	pending    string             // display name of the folder being loaded ("" when idle)
	loadSince  time.Time          // when the current load started (for the spinner's seconds)
	spinFrame  int                // spinner frame; > 0 once the load has lasted one tick
	spinFor    time.Duration      // how long the current load has been running, as of the last tick

	width, height int
}

// New creates a panel. Call Load to populate it.
func New(id int, lister fsys.Lister) *Panel {
	if lister == nil {
		lister = fsys.OSLister{}
	}
	return &Panel{id: id, lister: lister, now: time.Now, sel: newSelection()}
}

// ---- accessors -------------------------------------------------------------

func (p *Panel) ID() int                     { return p.id }
func (p *Panel) Path() string                { return p.path }
func (p *Panel) Entries() []fsys.Entry       { return p.entries }
func (p *Panel) Cursor() int                 { return p.cursor }
func (p *Panel) Offset() int                 { return p.offset }
func (p *Panel) Err() error                  { return p.err }
func (p *Panel) Loading() bool               { return p.loading }
func (p *Panel) Options() fsys.Options       { return p.opts }
func (p *Panel) Listing() fsys.Listing       { return p.listing }
func (p *Panel) SetClock(f func() time.Time) { p.now = f }

// InArchive reports whether the panel shows the inside of an archive.
func (p *Panel) InArchive() bool { return p.arc != nil }

// Archive returns the archive being browsed (nil on the real filesystem).
func (p *Panel) Archive() *archive.Archive { return p.arc }

// ArchiveInner returns the folder inside the archive being shown ("" =
// archive root, or not in an archive).
func (p *Panel) ArchiveInner() string {
	if p.arc == nil {
		return ""
	}
	in, _ := p.arc.InnerOf(p.path)
	return in
}

// InRemote reports whether the panel shows an rclone remote.
func (p *Panel) InRemote() bool { return p.rem != nil }

// Source returns the remote being shown (nil = local).
func (p *Panel) Source() *RemoteSource { return p.rem }

// Location returns the remote folder being shown (a zero Path when local).
func (p *Panel) Location() cloud.Path {
	if p.rem == nil {
		return cloud.Path{}
	}
	loc, _ := cloud.Parse(p.path)
	return loc
}

// beginLoad marks the panel as loading label (shown by the spinner line)
// and restarts the spinner.
func (p *Panel) beginLoad(label string) {
	p.loading = true
	p.pending = label
	p.loadSince = time.Now()
	p.spinFrame, p.spinFor = 0, 0
}

// SpinnerTick advances the loading spinner. The UI calls it about ten
// times a second while Loading() is true; it does nothing otherwise.
func (p *Panel) SpinnerTick() {
	if !p.loading {
		return
	}
	p.spinFrame++
	p.spinFor = time.Since(p.loadSince)
}

// pendingLabel names what is being loaded (for the "Loading …" row).
func (p *Panel) pendingLabel() string {
	if p.pending != "" {
		return p.pending
	}
	return p.Location().Display()
}

// LastLocal returns the local folder the panel showed last ("" = none yet).
func (p *Panel) LastLocal() string { return p.lastLocal }

// SetLocalBadge makes a local panel show a "LOCAL" label on its path line
// (the UI turns it on while a remote is shown in either panel).
func (p *Panel) SetLocalBadge(on bool) { p.localBadge = on }

// RealDir returns the real filesystem folder the panel is in: its path, or
// the folder containing the archive being browsed ("" on a remote).
func (p *Panel) RealDir() string {
	if p.rem != nil {
		return ""
	}
	if p.arc != nil {
		return filepath.Dir(p.arc.Path)
	}
	return p.path
}

// Current returns the entry under the cursor.
func (p *Panel) Current() (fsys.Entry, bool) {
	if p.cursor < 0 || p.cursor >= len(p.entries) {
		return fsys.Entry{}, false
	}
	return p.entries[p.cursor], true
}

// IsSelected reports whether the named entry is selected.
func (p *Panel) IsSelected(name string) bool { _, ok := p.sel.m[name]; return ok }

// selection is a set of selected names. When both panels show the same
// directory they share one selection, so selecting in one panel shows in
// the other at once.
type selection struct {
	m    map[string]struct{}
	refs int // panels using it
}

func newSelection() *selection { return &selection{m: map[string]struct{}{}, refs: 1} }

// detach gives the panel a new, empty selection (after it changed
// directory), leaving a shared one to the other panel.
func (p *Panel) detach() {
	p.sel.refs--
	p.sel = newSelection()
}

// ShareSelection makes a and b use one selection: the union of both. The
// app calls it while both panels show the same directory.
func ShareSelection(a, b *Panel) {
	if a.sel == b.sel {
		return
	}
	for name := range b.sel.m {
		a.sel.m[name] = struct{}{}
	}
	b.sel.refs--
	b.sel = a.sel
	a.sel.refs++
}

// SharesSelection reports whether a and b use the same selection.
func SharesSelection(a, b *Panel) bool { return a.sel == b.sel }

// Unshare gives p its own copy of a shared selection.
func (p *Panel) Unshare() {
	if p.sel.refs <= 1 {
		return
	}
	cp := newSelection()
	for name := range p.sel.m {
		cp.m[name] = struct{}{}
	}
	p.sel.refs--
	p.sel = cp
}

// Selection returns the selected entries in display order. Later phases
// (copy/move/delete) operate on this list.
func (p *Panel) Selection() []fsys.Entry {
	var out []fsys.Entry
	for _, e := range p.entries {
		if p.IsSelected(e.Name) && !e.IsParent {
			out = append(out, e)
		}
	}
	return out
}

// Targets returns the entries a file operation should act on: the selection
// if there is one, otherwise the entry under the cursor (never "..").
func (p *Panel) Targets() []fsys.Entry {
	if sel := p.Selection(); len(sel) > 0 {
		return sel
	}
	if e, ok := p.Current(); ok && !e.IsParent {
		return []fsys.Entry{e}
	}
	return nil
}

// NextFocus returns the name of the first entry at or after the cursor that
// is not in gone (falling back to the closest one before it), so the cursor
// lands sensibly after the named entries are deleted or moved away.
func (p *Panel) NextFocus(gone map[string]bool) string {
	for i := p.cursor; i < len(p.entries); i++ {
		if !gone[p.entries[i].Name] {
			return p.entries[i].Name
		}
	}
	for i := p.cursor - 1; i >= 0; i-- {
		if !gone[p.entries[i].Name] {
			return p.entries[i].Name
		}
	}
	return ""
}

// ---- loading ---------------------------------------------------------------

// Load starts an asynchronous read of path and returns the command to run.
// focus names the entry the cursor should land on (may be empty).
func (p *Panel) Load(path, focus string, mode LoadMode) tea.Cmd {
	if src := p.rem; src != nil {
		if loc, ok := cloud.Parse(path); ok && loc.Remote == src.Remote.Name {
			return p.loadRemote(src, loc, focus, mode)
		}
	}
	return p.loadLocal(path, focus, mode, true)
}

// loadLocal reads a local folder (or, with inArc, a folder inside the
// archive shown).
func (p *Panel) loadLocal(path, focus string, mode LoadMode, inArc bool) tea.Cmd {
	p.CancelLoad()
	p.seq++
	p.beginLoad(path)
	seq, id, lister, opts := p.seq, p.id, p.lister, p.opts
	if arc := p.arc; arc != nil && inArc {
		if inner, ok := arc.InnerOf(path); ok {
			p.pending = arc.Display(inner)
			return func() tea.Msg { return loadArchive(id, seq, arc, inner, focus, mode, opts) }
		}
	}
	return func() tea.Msg {
		l, err := lister.List(path, opts)
		if err != nil && mode == LoadRefresh && !exists(path) {
			if up := fsys.NearestExisting(path); up != path {
				if l2, err2 := lister.List(up, opts); err2 == nil {
					return LoadedMsg{PanelID: id, Seq: seq, Path: up, Focus: "", Mode: LoadForce, Listing: l2}
				}
			}
		}
		return LoadedMsg{PanelID: id, Seq: seq, Path: path, Focus: focus, Mode: mode, Listing: l, Err: err}
	}
}

// loadArchive lists folder inner of arc. A refresh re-reads the archive if
// the file changed; if it can no longer be read, the panel falls back to the
// folder containing it (with a warning).
func loadArchive(id int, seq uint64, arc *archive.Archive, inner, focus string, mode LoadMode, opts fsys.Options) tea.Msg {
	if mode == LoadRefresh && arc.Changed() {
		fresh, err := archive.Open(arc.Path)
		if err != nil {
			dir := fsys.NearestExisting(filepath.Dir(arc.Path))
			l, lerr := fsys.OSLister{}.List(dir, opts)
			return LoadedMsg{PanelID: id, Seq: seq, Path: dir, Focus: filepath.Base(arc.Path), Mode: LoadForce,
				Listing: l, Err: lerr, Warn: fmt.Errorf("left the archive: %s: %w", filepath.Base(arc.Path), err)}
		}
		arc = fresh
		if _, ok := arc.Lookup(inner); !ok {
			inner = "" // the folder is gone from the new version
		}
	}
	l, err := arc.List(inner, opts)
	return LoadedMsg{PanelID: id, Seq: seq, Path: arc.VirtualPath(inner), Focus: focus, Mode: mode, Listing: l, Err: err, Arc: arc}
}

// loadRemote lists a folder of a remote with rclone in the background. A
// newer load (or CancelLoad) stops it. A refresh of a folder that no longer
// exists climbs to the nearest folder that does.
func (p *Panel) loadRemote(src *RemoteSource, loc cloud.Path, focus string, mode LoadMode) tea.Cmd {
	p.CancelLoad()
	p.seq++
	p.beginLoad(loc.Display())
	ctx, cancel := context.WithCancel(context.Background())
	p.loadCancel = cancel
	seq, id, opts := p.seq, p.id, p.opts
	return func() tea.Msg {
		defer cancel()
		l, err := src.Client.List(ctx, loc, opts)
		if err != nil && mode == LoadRefresh && cloud.KindOf(err) == cloud.KindNotFound {
			for up := loc.Dir(); ; up = up.Dir() {
				if l2, err2 := src.Client.List(ctx, up, opts); err2 == nil {
					return LoadedMsg{PanelID: id, Seq: seq, Path: up.Arg(), Mode: LoadForce, Listing: l2, Rem: src,
						Warn: fmt.Errorf("%s no longer exists", loc.Display())}
				} else if cloud.KindOf(err2) != cloud.KindNotFound || up.IsRoot() {
					break
				}
			}
		}
		return LoadedMsg{PanelID: id, Seq: seq, Path: loc.Arg(), Focus: focus, Mode: mode, Listing: l, Err: err, Rem: src}
	}
}

// OpenRemote shows the root folder of a remote. On failure the panel stays
// where it is (Apply returns the error).
func (p *Panel) OpenRemote(src *RemoteSource) tea.Cmd {
	return p.loadRemote(src, src.Remote.Root(), "", LoadNavigate)
}

// OpenRemoteAt shows folder loc of a remote (used to show the same remote
// folder in the other panel).
func (p *Panel) OpenRemoteAt(src *RemoteSource, loc cloud.Path, focus string) tea.Cmd {
	return p.loadRemote(src, loc, focus, LoadNavigate)
}

// GoLocal leaves a remote (or archive) and shows the local folder path.
func (p *Panel) GoLocal(path, focus string) tea.Cmd {
	return p.loadLocal(path, focus, LoadNavigate, false)
}

// CancelLoad stops a remote listing in progress. It reports whether there
// was one; its result arrives as a cancelled load.
func (p *Panel) CancelLoad() bool {
	if p.loadCancel == nil {
		return false
	}
	p.loadCancel()
	p.loadCancel = nil
	return p.loading
}

// OpenArchive reads the ZIP archive at path and shows its root folder. On
// failure the panel stays where it is (Apply returns the error).
func (p *Panel) OpenArchive(path string) tea.Cmd {
	p.seq++
	p.beginLoad(path)
	seq, id, opts := p.seq, p.id, p.opts
	return func() tea.Msg {
		arc, err := archive.Open(path)
		if err != nil {
			return LoadedMsg{PanelID: id, Seq: seq, Path: path, Mode: LoadNavigate,
				Err: fmt.Errorf("cannot open %s: %w", filepath.Base(path), err)}
		}
		l, err := arc.List("", opts)
		return LoadedMsg{PanelID: id, Seq: seq, Path: arc.Path, Mode: LoadNavigate, Listing: l, Err: err, Arc: arc}
	}
}

// LeaveArchive returns to the folder containing the archive, with the
// cursor on the archive file.
func (p *Panel) LeaveArchive() tea.Cmd {
	if p.arc == nil {
		return nil
	}
	return p.Load(filepath.Dir(p.arc.Path), filepath.Base(p.arc.Path), LoadNavigate)
}

func exists(path string) bool { return fsys.NearestExisting(path) == filepath.Clean(path) }

// Apply integrates a finished load. It returns a non-nil error when a
// navigation attempt failed and the panel stayed where it was; the caller
// should surface it in the status line.
func (p *Panel) Apply(msg LoadedMsg) error {
	if msg.Seq != p.seq {
		return nil // stale result from a superseded request
	}
	p.loading = false
	p.loadCancel = nil
	p.pending = ""

	if msg.Err != nil {
		switch msg.Mode {
		case LoadNavigate:
			return msg.Err
		default: // Force / Refresh: show the error inside the panel
			p.arc = msg.Arc
			p.rem = msg.Rem
			p.err = msg.Err
			p.entries = nil
			if loc, ok := cloud.Parse(msg.Path); ok && msg.Rem != nil {
				p.path = loc.Arg()
				if !loc.IsRoot() {
					p.entries = []fsys.Entry{{Name: "..", Path: loc.Dir().Arg(), IsDir: true, IsParent: true, Mode: fs.ModeDir}}
				}
			} else {
				path, _ := filepath.Abs(msg.Path)
				p.path = filepath.Clean(path)
				if !fsys.IsRoot(p.path) {
					p.entries = []fsys.Entry{{Name: "..", Path: fsys.Parent(p.path), IsDir: true, IsParent: true, Mode: fs.ModeDir}}
				}
			}
			p.listing = fsys.Listing{Path: p.path}
			p.detach()
			p.cursor, p.offset = 0, 0
			return msg.Warn
		}
	}
	p.arc = msg.Arc
	p.rem = msg.Rem
	if msg.Rem == nil && msg.Arc == nil {
		p.lastLocal = msg.Listing.Path
	}

	prevName := ""
	if cur, ok := p.Current(); ok {
		prevName = cur.Name
	}
	prevEntries, prevCursor := p.entries, p.cursor
	prevRow := p.cursor - p.offset // screen row of the cursor before the reload
	samePath := msg.Listing.Path == p.path

	p.err = nil
	p.path = msg.Listing.Path
	p.listing = msg.Listing
	p.entries = msg.Listing.Entries

	if msg.Mode == LoadRefresh && samePath {
		// keep the selection for entries that are still listed (in place:
		// a shared selection stays shared). With a shared selection, names
		// this panel hides (dot-files) may be listed by the other panel and
		// are kept.
		listed := make(map[string]bool, len(p.entries))
		for _, e := range p.entries {
			listed[e.Name] = true
		}
		for name := range p.sel.m {
			if listed[name] || (p.sel.refs > 1 && !p.opts.ShowHidden && strings.HasPrefix(name, ".")) {
				continue
			}
			delete(p.sel.m, name)
		}
		if msg.Focus == "" {
			msg.Focus = prevName
		}
	} else {
		p.detach()
		if !samePath {
			p.offset = 0
		}
	}

	p.cursor = 0
	if msg.Focus != "" {
		if i := p.indexOf(msg.Focus); i >= 0 {
			p.cursor = i
		} else if msg.Mode == LoadRefresh && samePath {
			// The focused entry is gone (deleted, or hidden by the filter):
			// land on its nearest surviving neighbour instead of the top.
			p.cursor = p.nearestSurvivor(prevEntries, prevCursor)
		}
	}
	if msg.Mode == LoadRefresh && samePath {
		// Keep the cursor on the same screen row where possible, so that
		// refreshing, re-sorting or toggling hidden files does not make the
		// list jump under the user's eyes.
		p.offset = p.cursor - prevRow
	}
	p.clamp()
	return msg.Warn
}

// nearestSurvivor returns the new index of the first entry at or after old
// position i of the previous listing that still exists (else the closest
// one before it), or 0.
func (p *Panel) nearestSurvivor(prev []fsys.Entry, i int) int {
	for j := i; j < len(prev); j++ {
		if k := p.indexOf(prev[j].Name); k >= 0 {
			return k
		}
	}
	for j := i - 1; j >= 0 && j < len(prev); j-- {
		if k := p.indexOf(prev[j].Name); k >= 0 {
			return k
		}
	}
	return 0
}

func (p *Panel) indexOf(name string) int {
	for i, e := range p.entries {
		if e.Name == name {
			return i
		}
	}
	return -1
}

// ---- navigation ------------------------------------------------------------

// Open enters the directory under the cursor. It returns nil when the entry
// is not a directory (opening files arrives in a later phase).
func (p *Panel) Open() tea.Cmd {
	e, ok := p.Current()
	if !ok || !e.IsDir {
		return nil
	}
	if e.IsParent {
		return p.Parent()
	}
	return p.Load(e.Path, "", LoadNavigate)
}

// Parent navigates to the parent directory, placing the cursor on the
// directory we came from.
func (p *Panel) Parent() tea.Cmd {
	if p.rem != nil {
		loc := p.Location()
		if loc.IsRoot() {
			return nil
		}
		return p.Load(loc.Dir().Arg(), loc.Base(), LoadNavigate)
	}
	if p.path == "" || fsys.IsRoot(p.path) {
		return nil
	}
	return p.Load(fsys.Parent(p.path), filepath.Base(p.path), LoadNavigate)
}

// Refresh re-reads the current directory.
func (p *Panel) Refresh() tea.Cmd { return p.Load(p.path, "", LoadRefresh) }

// RefreshFocus re-reads the directory (keeping the selection of entries that
// still exist) and places the cursor on focus if it exists.
func (p *Panel) RefreshFocus(focus string) tea.Cmd { return p.Load(p.path, focus, LoadRefresh) }

// ChangeDir navigates to an arbitrary directory.
func (p *Panel) ChangeDir(path string) tea.Cmd { return p.Load(path, "", LoadNavigate) }

// ToggleHidden flips dot-file visibility and reloads.
func (p *Panel) ToggleHidden() tea.Cmd { return p.SetShowHidden(!p.opts.ShowHidden) }

// SetShowHidden sets dot-file visibility and reloads the directory, keeping
// the cursor entry (or its nearest neighbour) and its screen row. The UI uses
// it to apply one global setting to both panels.
func (p *Panel) SetShowHidden(show bool) tea.Cmd {
	p.opts.ShowHidden = show
	if p.path == "" {
		return nil
	}
	return p.Refresh()
}

// CycleSort switches to the next sort key in its natural direction (see
// fsys.SortKey.DefaultDesc) and reloads; the cursor stays on the same entry.
func (p *Panel) CycleSort() tea.Cmd {
	p.opts.Sort = p.opts.Sort.Next()
	p.opts.Desc = p.opts.Sort.DefaultDesc()
	return p.Refresh()
}

// ReverseSort flips between ascending and descending order and reloads; the
// cursor stays on the same entry.
func (p *Panel) ReverseSort() tea.Cmd {
	p.opts.Desc = !p.opts.Desc
	return p.Refresh()
}

func (p *Panel) MoveBy(n int) { p.cursor += n; p.clamp() }
func (p *Panel) Up()          { p.MoveBy(-1) }
func (p *Panel) Down()        { p.MoveBy(1) }
func (p *Panel) PageUp()      { p.MoveBy(-p.pageSize()) }
func (p *Panel) PageDown()    { p.MoveBy(p.pageSize()) }
func (p *Panel) Top()         { p.cursor = 0; p.clamp() }
func (p *Panel) Bottom()      { p.cursor = len(p.entries) - 1; p.clamp() }

func (p *Panel) pageSize() int {
	if n := p.listRows() - 1; n > 0 {
		return n
	}
	return 1
}

// ToggleSelect toggles selection of the current entry and advances the cursor.
func (p *Panel) ToggleSelect() {
	e, ok := p.Current()
	if !ok {
		return
	}
	if !e.IsParent {
		if p.IsSelected(e.Name) {
			delete(p.sel.m, e.Name)
		} else {
			p.sel.m[e.Name] = struct{}{}
		}
	}
	p.Down()
}

// SelectAll selects every entry; if all are already selected it clears them.
func (p *Panel) SelectAll() {
	total, sel := 0, 0
	for _, e := range p.entries {
		if !e.IsParent {
			total++
			if p.IsSelected(e.Name) {
				sel++
			}
		}
	}
	if total > 0 && sel == total {
		p.ClearSelection()
		return
	}
	for _, e := range p.entries {
		if !e.IsParent {
			p.sel.m[e.Name] = struct{}{}
		}
	}
}

// ClearSelection removes all selections.
func (p *Panel) ClearSelection() { clear(p.sel.m) }

// ScrollBy scrolls the viewport by n rows (negative = up) without moving the
// cursor more than necessary: the cursor is pulled along only when it would
// leave the visible rows. Used for mouse-wheel and touch-swipe scrolling.
func (p *Panel) ScrollBy(n int) {
	p.setOffset(p.offset + n)
}

// ScrollTo sets the first visible row (clamped) and keeps the cursor visible.
func (p *Panel) ScrollTo(offset int) { p.setOffset(offset) }

func (p *Panel) setOffset(off int) {
	rows := p.listRows()
	if rows < 1 {
		rows = 1
	}
	n := len(p.entries)
	if maxOff := n - rows; off > maxOff {
		off = maxOff
	}
	if off < 0 {
		off = 0
	}
	p.offset = off
	if p.cursor < p.offset {
		p.cursor = p.offset
	}
	if p.cursor >= p.offset+rows {
		p.cursor = p.offset + rows - 1
	}
	p.clamp()
}

// SetCursor moves the cursor to entry i (clamped), scrolling only if needed.
func (p *Panel) SetCursor(i int) { p.cursor = i; p.clamp() }

// ScrollInfo reports the list geometry used by the scroll indicator: the
// number of entries, the number of list rows on screen and the index of the
// first visible entry.
func (p *Panel) ScrollInfo() (total, visible, offset int) {
	return len(p.entries), p.listRows(), p.offset
}

// clamp keeps cursor and scroll offset within bounds.
func (p *Panel) clamp() {
	n := len(p.entries)
	if p.cursor >= n {
		p.cursor = n - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	rows := p.listRows()
	if rows < 1 {
		rows = 1
	}
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
	if maxOff := n - rows; p.offset > maxOff {
		p.offset = maxOff
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

// SetSize sets the outer dimensions (including border) of the panel.
func (p *Panel) SetSize(w, h int) {
	p.width, p.height = w, h
	p.clamp()
}

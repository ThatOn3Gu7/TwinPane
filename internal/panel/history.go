package panel

import (
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/archive"
	"github.com/ThatOn3Gu7/TwinPane/internal/cloud"
)

// Per-panel navigation history (back / forward).
//
// Every completed navigation records where the panel came from, so each panel
// has its own session-local stack — nothing is shared between the two panels
// and nothing is written to disk (bookmarks are a separate, persistent
// feature). A location is recorded in the panel's own typed terms (local
// folder, archive file + folder inside it, or rclone source + folder), never
// as a bare path: a remote history entry keeps the source it belonged to, so
// it still resolves after the panel (or the other panel) switched sources,
// and a local folder is never mistaken for an rclone argument or an archive
// path.

// NavKind is the kind of a recorded location.
type NavKind int

const (
	NavLocal NavKind = iota
	NavArchive
	NavRemote
)

// navLimit caps each stack: the oldest entry is dropped beyond it.
const navLimit = 100

// NavLocation is one history entry: the location plus how the panel was
// showing it (cursor entry and scroll offset), so going back restores both.
type NavLocation struct {
	Kind   NavKind
	Path   string // local folder, archive file, or remote folder (rclone argument)
	Inner  string // folder inside the archive ("" = the archive root)
	Remote string // remote name, for NavRemote
	Source *RemoteSource
	Focus  string // entry the cursor was on ("" = none, e.g. "..")
	Offset int    // first visible row
}

// key identifies the location itself (without cursor/scroll state).
func (l NavLocation) key() string {
	switch l.Kind {
	case NavArchive:
		return "archive\x00" + l.Path + "\x00" + l.Inner
	case NavRemote:
		return "remote\x00" + l.Remote + "\x00" + l.Path
	default:
		return "local\x00" + l.Path
	}
}

// Label describes the location for the status line.
func (l NavLocation) Label() string {
	name := filepath.Base(l.Path)
	switch l.Kind {
	case NavArchive:
		display := name + " (archive)"
		if l.Inner != "" {
			display = name + ": /" + l.Inner
		}
		return display
	case NavRemote:
		loc, ok := cloud.Parse(l.Path)
		if !ok {
			return l.Remote + ": " + l.Path
		}
		return loc.Display()
	default:
		return l.Path
	}
}

type navHistory struct {
	back, forward []NavLocation
	pending       *navPending // the back/forward move waiting for its load
}

// navPending ties a history move to the directory load it started, so a
// failed load can put the entry back where it came from.
type navPending struct {
	seq     uint64
	target  NavLocation
	backing bool // the move was Back (false = Forward)
}

func (p *Panel) pushBack(l NavLocation) []NavLocation {
	out := append(p.hist.back, l)
	if len(out) > navLimit {
		out = out[len(out)-navLimit:]
	}
	return out
}

func (p *Panel) pushForward(l NavLocation) []NavLocation {
	out := append(p.hist.forward, l)
	if len(out) > navLimit {
		out = out[len(out)-navLimit:]
	}
	return out
}

// navState captures where the panel is and how it is showing it.
func (p *Panel) navState() NavLocation {
	loc := NavLocation{Offset: p.offset}
	switch {
	case p.rem != nil:
		loc.Kind = NavRemote
		loc.Remote = p.rem.Remote.Name
		loc.Source = p.rem
		loc.Path = p.path
	case p.arc != nil:
		loc.Kind = NavArchive
		loc.Path = p.arc.Path
		loc.Inner = p.ArchiveInner()
	default:
		loc.Kind = NavLocal
		loc.Path = p.path
	}
	if e, ok := p.Current(); ok && !e.IsParent && e.Name != ".." {
		loc.Focus = e.Name
	}
	return loc
}

// record adds before to the back stack when the panel really moved, and drops
// the forward stack (a new navigation makes it meaningless).
func (p *Panel) record(before NavLocation, hadBefore bool, after NavLocation) {
	if !hadBefore || before.key() == after.key() {
		return
	}
	// the cursor/scroll of the place we left is what going back restores
	p.hist.back = p.pushBack(before)
	p.hist.forward = nil
}

// CanBack and CanForward report whether a history move is available.
func (p *Panel) CanBack() bool    { return len(p.hist.back) > 0 }
func (p *Panel) CanForward() bool { return len(p.hist.forward) > 0 }

// NavDepth returns the number of entries behind and ahead (for help/tests).
func (p *Panel) NavDepth() (back, forward int) {
	return len(p.hist.back), len(p.hist.forward)
}

// NavMove is the outcome of a back/forward request: the load to run (nil
// when there is nothing to do) and, when nothing could be loaded, why.
type NavMove struct {
	Cmd   tea.Cmd
	Note  string // why nothing (or not everything) could be restored
	Label string // the location that is being opened
	OK    bool
}

// available reports whether a recorded location can still be loaded. Local
// folders and archive files are checked here (cheap and synchronous); remote
// folders are checked by rclone, so they can only be rejected when their
// source is gone.
func (p *Panel) available(l NavLocation) bool {
	switch l.Kind {
	case NavRemote:
		return l.Source != nil && l.Source.Client != nil && l.Remote != "" && l.Source.Remote.Name == l.Remote
	case NavArchive:
		return l.Path != "" && navExists(l.Path)
	default:
		return l.Path != "" && navExists(l.Path)
	}
}

// navExists reports whether a path exists at all. It looks at files too: an
// archive history entry points at the archive file, not at a folder.
func navExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Back goes to the previous location of this panel.
func (p *Panel) Back() NavMove { return p.navMove(true) }

// Forward goes to the next location again, after going back.
func (p *Panel) Forward() NavMove { return p.navMove(false) }

func (p *Panel) navMove(backing bool) NavMove {
	var note string
	for {
		var target NavLocation
		if backing {
			if len(p.hist.back) == 0 {
				if note == "" {
					note = "nothing to go back to"
				}
				return NavMove{Note: note}
			}
			target = p.hist.back[len(p.hist.back)-1]
			p.hist.back = p.hist.back[:len(p.hist.back)-1]
		} else {
			if len(p.hist.forward) == 0 {
				if note == "" {
					note = "nothing to go forward to"
				}
				return NavMove{Note: note}
			}
			target = p.hist.forward[len(p.hist.forward)-1]
			p.hist.forward = p.hist.forward[:len(p.hist.forward)-1]
		}
		if !p.available(target) {
			// The place is gone (deleted folder, removed remote, deleted
			// archive): drop the entry and keep looking, so history never
			// leaves the panel stuck on a dead location.
			note = "skipped " + target.Label() + ": no longer available"
			continue
		}
		cmd := p.restore(target)
		if cmd == nil {
			note = "cannot return to " + target.Label()
			continue
		}
		current := p.navState()
		if backing {
			p.hist.forward = p.pushForward(current)
		} else {
			p.hist.back = p.pushBack(current)
		}
		p.hist.pending = &navPending{seq: p.seq, target: target, backing: backing}
		return NavMove{Cmd: cmd, Note: note, Label: target.Label(), OK: true}
	}
}

// restore loads the recorded location again. The cursor lands on the entry
// the panel was on when it left, and the scroll offset is restored once the
// listing arrives.
func (p *Panel) restore(l NavLocation) tea.Cmd {
	switch l.Kind {
	case NavRemote:
		return p.OpenRemoteAt(l.Source, cloud.Path{Remote: l.Remote, Path: l.Path}, l.Focus)
	case NavArchive:
		return p.openArchiveAt(l.Path, l.Inner, l.Focus)
	default:
		return p.Load(l.Path, l.Focus, LoadNavigate)
	}
}

// settleNav is called by Apply once a load has been integrated: it restores
// the scroll offset of a completed history move, or puts the entry back when
// the location could not be shown.
func (p *Panel) settleNav(msg LoadedMsg, ok bool) {
	pend := p.hist.pending
	if pend == nil || pend.seq != msg.Seq {
		return
	}
	p.hist.pending = nil
	if !ok {
		// The load failed: undo the history move so the entry is not lost.
		if pend.backing {
			if n := len(p.hist.forward); n > 0 {
				p.hist.forward = p.hist.forward[:n-1]
			}
			p.hist.back = p.pushBack(pend.target)
		} else {
			if n := len(p.hist.back); n > 0 {
				p.hist.back = p.hist.back[:n-1]
			}
			p.hist.forward = p.pushForward(pend.target)
		}
		return
	}
	// Keep the row it was scrolled to, when the listing allows it.
	p.offset = pend.target.Offset
	p.clamp()
}

// clearPendingForget drops a pending move that a newer load superseded.
func (p *Panel) clearPendingForget() {
	if p.hist.pending != nil && p.hist.pending.seq != p.seq {
		p.hist.pending = nil
	}
}

// openArchiveAt indexes the archive at path and shows folder inner of it.
// It is OpenArchive plus a starting folder, used when returning to a place
// inside an archive from the history.
func (p *Panel) openArchiveAt(path, inner, focus string) tea.Cmd {
	p.CancelLoad()
	p.seq++
	p.beginLoad(filepath.Base(path))
	seq, id, opts := p.seq, p.id, p.opts
	return func() tea.Msg {
		arc, err := archive.Open(path)
		if err != nil {
			return LoadedMsg{PanelID: id, Seq: seq, Path: path, Mode: LoadNavigate, Err: err}
		}
		if inner != "" {
			if _, ok := arc.Lookup(inner); !ok {
				inner = "" // the folder is gone from this version of the archive
			}
		}
		l, err := arc.List(inner, opts)
		return LoadedMsg{PanelID: id, Seq: seq, Path: arc.VirtualPath(inner), Focus: focus, Mode: LoadNavigate, Listing: l, Err: err, Arc: arc}
	}
}

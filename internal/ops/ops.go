// Package ops implements TwinPane's file operations (copy, move, rename,
// delete/trash). It has no UI dependencies: callers drive it through Hooks
// (progress reporting and conflict resolution) and a context for cancellation,
// which keeps the logic fully testable without a terminal.
//
// Safety rules enforced here (not in the UI):
//   - an existing destination is never replaced without an explicit Overwrite
//     decision from Hooks.Conflict (nil hook == Skip);
//   - files are written to a temporary name and atomically renamed into place,
//     so an interrupted or failed copy never leaves a truncated destination
//     and never destroys the file it was meant to replace;
//   - symlinks are copied as links and never followed, so link loops cannot
//     cause infinite recursion and writes cannot escape through a link;
//   - a directory can't be copied/moved into itself or a descendant;
//   - a move only removes a source file after its copy is verified, and
//     only removes a source directory with os.Remove once it is empty.
package ops

import (
	"fmt"
	"io/fs"
	"strings"
)

// Kind selects copy or move semantics.
type Kind int

const (
	Copy Kind = iota
	Move
)

func (k Kind) String() string {
	if k == Move {
		return "move"
	}
	return "copy"
}

// Verb returns a capitalised present participle ("Copying").
func (k Kind) Verb() string {
	if k == Move {
		return "Moving"
	}
	return "Copying"
}

// Past returns a capitalised past tense ("Copied").
func (k Kind) Past() string {
	if k == Move {
		return "Moved"
	}
	return "Copied"
}

// Request describes a copy or move of Sources into DestDir.
type Request struct {
	Kind    Kind
	Sources []string // absolute paths
	DestDir string   // absolute path of an existing directory

	// BeforeTransfer, when non-nil, is called before resolving a destination
	// conflict, before starting a transfer, and again immediately before
	// placing the result. Reviewed higher-level plans use it to fail closed if
	// either path changed after approval. It must not modify the filesystem.
	BeforeTransfer func(src, dst string) error
}

// Action is the answer to a destination conflict.
type Action int

const (
	Skip Action = iota // zero value: the safe default
	Overwrite
	Cancel
)

// Decision is returned by Hooks.Conflict.
type Decision struct {
	Action   Action
	ApplyAll bool // use the same action for all remaining conflicts
}

// Conflict describes a destination that already exists.
type Conflict struct {
	Src, Dst         string
	SrcInfo, DstInfo fs.FileInfo
	IsDir            bool // both are directories: Overwrite means merge
}

// Progress is reported periodically while an operation runs.
type Progress struct {
	Phase      string // "scanning", "copying", "moving", "verifying", "deleting", "trashing"
	Current    string // path currently being processed
	DoneItems  int
	TotalItems int
	DoneBytes  int64
	TotalBytes int64
	Errors     int
	LastError  string
	// ByteOnly marks progress known only by bytes (an external program
	// reports a percentage): DoneItems is not counted.
	ByteOnly bool
}

// Fraction returns completion in [0,1], by bytes when known, else by items.
func (p Progress) Fraction() float64 {
	var f float64
	switch {
	case p.TotalBytes > 0:
		f = float64(p.DoneBytes) / float64(p.TotalBytes)
	case p.TotalItems > 0:
		f = float64(p.DoneItems) / float64(p.TotalItems)
	}
	if f > 1 {
		f = 1
	}
	if f < 0 {
		f = 0
	}
	return f
}

// Hooks connect an operation to its caller. All fields are optional.
type Hooks struct {
	// Progress receives throttled progress updates.
	Progress func(Progress)
	// Conflict is asked what to do when a destination exists. A nil hook
	// skips every conflict (never overwrites).
	Conflict func(Conflict) Decision
	// LowSpace is asked whether to continue when the destination appears to
	// have less free space than the data to be written. Nil == abort.
	LowSpace func(need, free uint64) bool
}

// ItemError records a failure for one path.
type ItemError struct {
	Path string
	Op   string // short description, e.g. "copy", "remove source"
	Err  error
}

func (e ItemError) Error() string {
	return fmt.Sprintf("%s %s: %v", e.Op, e.Path, e.Err)
}

func (e ItemError) Unwrap() error { return e.Err }

// Result summarises a finished copy/move.
type Result struct {
	Kind    Kind
	Files   int   // files and symlinks written to the destination
	Dirs    int   // directories created (or merged into, for moves via rename: renamed)
	Bytes   int64 // bytes written
	Skipped int   // conflicts the user chose to skip
	Errors  []ItemError
	// Leftovers lists sources that were copied and verified but could not
	// be removed during a move. The data exists in BOTH places.
	Leftovers []ItemError
	Canceled  bool
	Fatal     error // the operation stopped early (invalid request, disk full, ...)
}

// OK reports a complete, error-free run (skips are not errors).
func (r Result) OK() bool {
	return !r.Canceled && r.Fatal == nil && len(r.Errors) == 0 && len(r.Leftovers) == 0
}

// Summary returns a one-line human-readable description.
func (r Result) Summary() string {
	var parts []string
	n := r.Files
	items := plural(n, "file")
	if r.Dirs > 0 {
		items += ", " + plural(r.Dirs, "folder")
	}
	parts = append(parts, fmt.Sprintf("%s %s", r.Kind.Past(), items))
	if r.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("skipped %d", r.Skipped))
	}
	if len(r.Errors) > 0 {
		parts = append(parts, plural(len(r.Errors), "error"))
	}
	if len(r.Leftovers) > 0 {
		parts = append(parts, fmt.Sprintf("%d copied but source NOT removed", len(r.Leftovers)))
	}
	s := strings.Join(parts, ", ")
	switch {
	case r.Fatal != nil:
		s = fmt.Sprintf("%s stopped: %v (%s)", capitalize(r.Kind.String()), r.Fatal, s)
	case r.Canceled:
		s = fmt.Sprintf("%s cancelled (%s; remaining items untouched)", capitalize(r.Kind.String()), s)
	}
	return s
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Package history keeps the record of file operations TwinPane performed,
// grouped by application run, and persists it in the configuration directory
// so it survives restarts.
//
// The store is deliberately independent of the UI: it knows operation types,
// statuses, paths, timings and results, and nothing about panels, keys or
// dialogs. Append operations are cheap (bounded in memory, one atomic file
// replace) and safe from concurrent goroutines (one mutex guards both the
// in-memory sessions and the file).
//
// Only real file operations are recorded — never navigation, dialogs or other
// UI interactions. An operation is recorded when it starts and updated when it
// finishes, so an operation that is interrupted by TwinPane exiting stays in
// the file as unfinished; the next run marks it as interrupted instead of
// claiming it succeeded.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/bookmarks"
)

const (
	// FileName is the history file in TwinPane's configuration directory.
	FileName = "history.json"

	// formatVersion is the on-disk format. A file written by a newer TwinPane
	// is never overwritten: the store then becomes read-only.
	formatVersion = 1

	// DefaultSessions is the retention limit used when none is configured.
	DefaultSessions = 20
	// MinSessions and MaxSessions bound the configurable retention limit.
	MinSessions = 1
	MaxSessions = 200

	// MaxOperationsPerSession bounds one session's records; the oldest
	// finished records are dropped first.
	MaxOperationsPerSession = 200

	// MaxFileBytes is the largest history file the store reads or writes.
	MaxFileBytes = 4 << 20

	// Caps that keep one record small.
	maxSources = 32
	maxItems   = 64
	maxErrors  = 16
	maxDetails = 32
	maxText    = 512
)

// Status is the final (or current) state of an operation.
type Status string

const (
	// StatusRunning marks an operation that had not finished when the record
	// was last written.
	StatusRunning Status = "running"
	// StatusSuccess means the operation completed as requested.
	StatusSuccess Status = "success"
	// StatusPartial means it finished incompletely (some items failed or a
	// safety prompt stopped it after work had been done).
	StatusPartial Status = "partial"
	// StatusFailed means it did not complete; see the record's errors.
	StatusFailed Status = "failed"
	// StatusCanceled means the user stopped it.
	StatusCanceled Status = "canceled"
	// StatusInterrupted means TwinPane stopped (crash, kill, power loss)
	// before the operation reported an outcome. Its result is unknown.
	StatusInterrupted Status = "interrupted"
)

// Label is the short upper-case name shown in the UI.
func (s Status) Label() string {
	switch s {
	case StatusRunning:
		return "RUNNING"
	case StatusSuccess:
		return "DONE"
	case StatusPartial:
		return "PARTIAL"
	case StatusFailed:
		return "FAILED"
	case StatusCanceled:
		return "CANCELED"
	case StatusInterrupted:
		return "INTERRUPTED"
	}
	return "UNKNOWN"
}

// Glyph is a one-character marker for compact lists.
func (s Status) Glyph() string {
	switch s {
	case StatusSuccess:
		return "✓"
	case StatusFailed:
		return "✗"
	case StatusPartial:
		return "!"
	case StatusCanceled:
		return "⊘"
	case StatusInterrupted:
		return "?"
	case StatusRunning:
		return "…"
	}
	return "·"
}

// Final reports whether the outcome is known (nothing will change it later).
func (s Status) Final() bool {
	return s != StatusRunning
}

// Valid reports whether s is a status the store knows.
func (s Status) Valid() bool {
	switch s {
	case StatusRunning, StatusSuccess, StatusPartial, StatusFailed, StatusCanceled, StatusInterrupted:
		return true
	}
	return false
}

// Record is one file operation.
type Record struct {
	ID       string    `json:"id"`                 // unique within its session
	Op       string    `json:"op"`                 // "Copy", "Delete", "New file", …
	Status   Status    `json:"status"`             // see Status
	Started  time.Time `json:"started"`            // when it began
	Finished time.Time `json:"finished,omitempty"` // zero while running
	Sources  []string  `json:"sources,omitempty"`  // source paths (or the archive/remote)
	Dest     string    `json:"dest,omitempty"`     // destination path / folder / trash
	Items    []string  `json:"items,omitempty"`    // affected top-level names
	Summary  string    `json:"summary,omitempty"`  // one-line outcome, as shown to the user
	Result   string    `json:"result,omitempty"`   // tool result, e.g. "rclone exit code 3"
	Errors   []string  `json:"errors,omitempty"`   // full, human-readable error lines
	Details  []string  `json:"details,omitempty"`  // extra lines (skips, leftovers, notes)
	Files    int       `json:"files,omitempty"`    // counts, when the operation reports them
	Dirs     int       `json:"dirs,omitempty"`
	Links    int       `json:"links,omitempty"`
	Bytes    int64     `json:"bytes,omitempty"`
}

// Duration is the time the operation took (zero while running or unknown).
func (r Record) Duration() time.Duration {
	if r.Started.IsZero() || r.Finished.IsZero() || r.Finished.Before(r.Started) {
		return 0
	}
	return r.Finished.Sub(r.Started)
}

// Session is one TwinPane run: the operations it performed, newest first.
type Session struct {
	ID      string    `json:"id"`      // derived from Started, e.g. 20261027T101500.123456789
	Started time.Time `json:"started"` // when this TwinPane run began
	Version string    `json:"version,omitempty"`
	Ops     []Record  `json:"operations"` // newest first; see Store
}

// Operations returns the session's records, newest first.
func (s Session) Operations() []Record { return s.Ops }

type file struct {
	Version  int       `json:"version"`
	Sessions []Session `json:"sessions"`
}

// CorruptError reports a history file that could not be used. The damaged
// file is renamed aside (never silently deleted) before a fresh one is
// started.
type CorruptError struct {
	Path   string
	Backup string
	Err    error
}

func (e *CorruptError) Error() string {
	msg := fmt.Sprintf("history file %s is damaged: %v", e.Path, e.Err)
	if e.Backup != "" {
		msg += " (kept as " + filepath.Base(e.Backup) + ")"
	}
	return msg
}

func (e *CorruptError) Unwrap() error { return e.Err }

// ReadOnlyError reports a history file that must not be replaced, because a
// newer TwinPane wrote it.
type ReadOnlyError struct {
	Path string
	Err  error
}

func (e *ReadOnlyError) Error() string {
	return fmt.Sprintf("history file %s was not changed: %v", e.Path, e.Err)
}

func (e *ReadOnlyError) Unwrap() error { return e.Err }

// Options configures a store.
type Options struct {
	// MaxSessions is the retention limit (0 = DefaultSessions, clamped to
	// [MinSessions, MaxSessions]).
	MaxSessions int
	// Version is the TwinPane version recorded with each session.
	Version string
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Store is the in-memory history and its file. Every exported method is safe
// for concurrent use; all file access happens under one mutex, so concurrent
// background tasks can neither interleave writes nor corrupt the file.
type Store struct {
	mu       sync.Mutex
	path     string
	max      int
	now      func() time.Time
	version  string
	sessions []Session // newest session first
	current  int       // index of this run's session in sessions (-1 = none)
	seq      int       // record sequence of the current session
	err      error     // last load or save problem (nil when fine)
	readOnly bool      // a newer file: never replaced
	lastSize int64     // size of our last write, to detect outside changes
	lastMod  time.Time // modification time of our last write
	rev      uint64    // bumped whenever the recorded sessions change
}

// DefaultPath returns the history file path in the shared TwinPane
// configuration directory (TWINPANE_CONFIG_DIR, the OS user config directory,
// then ~/.twinpane — the same policy as settings and bookmarks).
func DefaultPath() (string, error) {
	dir, err := bookmarks.ConfigDir(os.Getenv, os.UserConfigDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// ClampSessions keeps a configured retention limit within the supported
// range; zero (or a negative value) means "not configured" and becomes the
// default.
func ClampSessions(n int) int {
	if n <= 0 {
		return DefaultSessions
	}
	return ClampLimit(n)
}

// ClampLimit keeps a retention limit within [MinSessions, MaxSessions]
// without substituting the default, so stepping down from the minimum stays
// at the minimum.
func ClampLimit(n int) int {
	if n < MinSessions {
		return MinSessions
	}
	if n > MaxSessions {
		return MaxSessions
	}
	return n
}

// Memory returns a store that keeps the history in memory only. It behaves
// like a file store for this run and is never persisted.
func Memory(opts Options) (*Store, error) {
	dir, err := os.MkdirTemp("", "twinpane-history-")
	if err != nil {
		return nil, err
	}
	return Open(filepath.Join(dir, FileName), opts)
}

// Open loads the history at path and starts a new session for this run. A
// missing or empty file is an empty history. A damaged file is moved aside
// and reported (the returned store is still usable and starts a fresh file);
// a file from a newer TwinPane makes the store read-only and is reported.
func Open(path string, opts Options) (*Store, error) {
	if path == "" {
		return nil, errors.New("history path is empty")
	}
	s := &Store{
		path:    path,
		max:     ClampSessions(opts.MaxSessions),
		now:     opts.Now,
		version: opts.Version,
		current: -1,
	}
	if s.now == nil {
		s.now = time.Now
	}
	loadErr := s.load()
	if loadErr != nil {
		s.err = loadErr
	}
	if s.readOnly {
		// Never touch the newer file: no session is started for this run, so
		// nothing can be written.
		return s, loadErr
	}
	s.startSession()
	if n := s.pruneLocked(); n > 0 {
		s.persistLocked()
	}
	return s, loadErr
}

// Close is a no-op kept for symmetry: every change is already on disk.
func (s *Store) Close() error { return nil }

// Revision changes whenever the recorded sessions change: a record starts,
// updates or finishes, a foreground operation is added, the retention limit
// prunes sessions or a file written by another instance is merged. The UI uses
// it to rebuild the operations it shows only when there is something new,
// instead of copying the whole history on every frame.
func (s *Store) Revision() uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rev
}

// Path returns the history file path ("" when the store has none).
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Err returns the last load or save problem, if any.
func (s *Store) Err() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// ReadOnly reports whether the store refuses to write (newer file).
func (s *Store) ReadOnly() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readOnly
}

// Limit returns the current retention limit in sessions.
func (s *Store) Limit() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.max
}

// SetLimit changes the retention limit, prunes older sessions and saves.
func (s *Store) SetLimit(n int) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.max = ClampSessions(n)
	s.pruneLocked()
	return s.persistLocked()
}

// CurrentID returns the ID of this run's session ("" when read-only).
func (s *Store) CurrentID() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current < 0 || s.current >= len(s.sessions) {
		return ""
	}
	return s.sessions[s.current].ID
}

// Sessions returns a copy of every session, newest first, with the operations
// inside each session newest first.
func (s *Store) Sessions() []Session {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, len(s.sessions))
	for i, sess := range s.sessions {
		out[i] = sess
		out[i].Ops = append([]Record(nil), sess.Ops...)
		for j := range out[i].Ops {
			r := &out[i].Ops[j]
			r.Sources = append([]string(nil), r.Sources...)
			r.Items = append([]string(nil), r.Items...)
			r.Errors = append([]string(nil), r.Errors...)
			r.Details = append([]string(nil), r.Details...)
		}
	}
	return out
}

// Start records the beginning of an operation and returns its record ID.
// The record is written to disk at once, so an operation that is interrupted
// by TwinPane exiting is still visible (as interrupted) after a restart.
func (s *Store) Start(r Record) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly || s.current < 0 || s.current >= len(s.sessions) {
		return ""
	}
	sess := &s.sessions[s.current]
	s.seq++
	if r.ID == "" {
		r.ID = fmt.Sprintf("%d", s.seq)
	}
	if r.Started.IsZero() {
		r.Started = s.now()
	}
	if r.Op == "" {
		r.Op = "Operation"
	}
	if !r.Status.Valid() {
		r.Status = StatusRunning
	}
	r.Finished = time.Time{}
	r = sanitizeRecord(r)
	if len(sess.Ops) >= MaxOperationsPerSession {
		s.dropOldestLocked(sess)
	}
	sess.Ops = append([]Record{r}, sess.Ops...) // newest first
	s.persistLocked()
	return r.ID
}

// Finish completes the record with the given ID (an unknown ID is ignored).
func (s *Store) Finish(id string, f Finish) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly || s.current < 0 || s.current >= len(s.sessions) {
		return
	}
	sess := &s.sessions[s.current]
	idx := -1
	for i := range sess.Ops {
		if sess.Ops[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	r := &sess.Ops[idx]
	if f.Status.Valid() {
		r.Status = f.Status
	}
	if f.Finished.IsZero() {
		f.Finished = s.now()
	}
	if r.Started.IsZero() || f.Finished.After(r.Started) || f.Finished.Equal(r.Started) {
		r.Finished = f.Finished
	} else {
		r.Finished = r.Started
	}
	if f.Summary != "" {
		r.Summary = f.Summary
	}
	if f.Result != "" {
		r.Result = f.Result
	}
	if f.Op != "" {
		r.Op = f.Op
	}
	if len(f.Sources) > 0 {
		r.Sources = append([]string(nil), f.Sources...)
	}
	if f.Dest != "" {
		r.Dest = f.Dest
	}
	if len(f.Items) > 0 {
		r.Items = append([]string(nil), f.Items...)
	}
	r.Errors = append([]string(nil), f.Errors...)
	r.Details = append([]string(nil), f.Details...)
	r.Files, r.Dirs, r.Links, r.Bytes = f.Files, f.Dirs, f.Links, f.Bytes
	*r = sanitizeRecord(*r)
	s.persistLocked()
}

// Update changes the descriptive fields of a record that is still running, so
// the file shows what the operation is doing while it does it (the status and
// the times are untouched).
func (s *Store) Update(id string, u Update) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly || s.current < 0 || s.current >= len(s.sessions) {
		return
	}
	sess := &s.sessions[s.current]
	for i := range sess.Ops {
		r := &sess.Ops[i]
		if r.ID != id {
			continue
		}
		if u.Op != "" {
			r.Op = clip(u.Op)
		}
		if u.Dest != "" {
			r.Dest = clip(u.Dest)
		}
		if len(u.Sources) > 0 {
			r.Sources = clipList(u.Sources, maxSources)
		}
		if len(u.Items) > 0 {
			r.Items = clipList(u.Items, maxItems)
		}
		s.persistLocked()
		return
	}
}

// Update carries the fields of a running record that may change.
type Update struct {
	Op      string
	Dest    string
	Sources []string
	Items   []string
}

// Add records an operation that is already finished (a quick foreground
// operation), newest first, and returns its ID.
func (s *Store) Add(r Record) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly || s.current < 0 || s.current >= len(s.sessions) {
		return ""
	}
	sess := &s.sessions[s.current]
	s.seq++
	if r.ID == "" {
		r.ID = fmt.Sprintf("%d", s.seq)
	}
	if r.Started.IsZero() {
		r.Started = s.now()
	}
	if r.Finished.IsZero() {
		r.Finished = r.Started
	}
	if r.Op == "" {
		r.Op = "Operation"
	}
	if !r.Status.Valid() {
		r.Status = StatusSuccess
	}
	r = sanitizeRecord(r)
	if len(sess.Ops) >= MaxOperationsPerSession {
		s.dropOldestLocked(sess)
	}
	sess.Ops = append([]Record{r}, sess.Ops...)
	s.persistLocked()
	return r.ID
}

// Finish carries the outcome of an operation.
type Finish struct {
	Status Status
	// Op replaces the operation type recorded at the start, when the task
	// names the work only once it knows what it is doing.
	Op       string
	Finished time.Time
	Summary  string
	Result   string
	Sources  []string
	Dest     string
	Items    []string
	Errors   []string
	Details  []string
	Files    int
	Dirs     int
	Links    int
	Bytes    int64
}

// ---- internals -------------------------------------------------------------------

func (s *Store) startSession() {
	started := s.now()
	id := started.Format("20060102T150405.000000000")
	for _, sess := range s.sessions { // never reuse an ID (equal clocks in tests)
		if sess.ID == id {
			id += "-" + fmt.Sprintf("%d", len(s.sessions)+1)
			break
		}
	}
	sess := Session{ID: id, Started: started, Version: s.version, Ops: []Record{}}
	s.sessions = append([]Session{sess}, s.sessions...)
	s.current = 0
	s.seq = 0
}

// dropOldestLocked removes the oldest finished record of sess (records are
// stored newest first, so that is the last non-running one).
func (s *Store) dropOldestLocked(sess *Session) {
	for i := len(sess.Ops) - 1; i >= 0; i-- {
		if sess.Ops[i].Status.Final() {
			sess.Ops = append(sess.Ops[:i], sess.Ops[i+1:]...)
			return
		}
	}
	// Only running records: keep them, they are still being written.
}

// pruneLocked applies the retention limit and the per-session cap. The
// running session is never pruned, even if it is the oldest (retention applies
// to finished runs).
func (s *Store) pruneLocked() int {
	changed := 0
	curID := s.currentIDLocked()
	if len(s.sessions) > s.max {
		kept := make([]Session, 0, s.max)
		for _, sess := range s.sessions {
			if len(kept) < s.max || sess.ID == curID {
				kept = append(kept, sess)
			} else {
				changed++
			}
		}
		s.sessions = kept
	}
	if curID != "" {
		s.current = -1
		for i, sess := range s.sessions {
			if sess.ID == curID {
				s.current = i
				break
			}
		}
	}
	for i := range s.sessions {
		for len(s.sessions[i].Ops) > MaxOperationsPerSession {
			before := len(s.sessions[i].Ops)
			s.dropOldestLocked(&s.sessions[i])
			if len(s.sessions[i].Ops) == before {
				break
			}
			changed++
		}
	}
	changed += s.fitFileLocked()
	return changed
}

func (s *Store) currentIDLocked() string {
	if s.current >= 0 && s.current < len(s.sessions) {
		return s.sessions[s.current].ID
	}
	return ""
}

// fitFileLocked drops the oldest data until the encoded history fits
// MaxFileBytes.
func (s *Store) fitFileLocked() int {
	changed := 0
	for len(s.sessions) > 1 {
		data, err := json.Marshal(file{Version: formatVersion, Sessions: s.sessions})
		if err == nil && len(data) <= MaxFileBytes {
			return changed
		}
		idx := len(s.sessions) - 1
		if idx == s.current {
			idx--
		}
		if idx < 0 {
			return changed
		}
		s.sessions = append(s.sessions[:idx], s.sessions[idx+1:]...)
		if idx < s.current {
			s.current--
		}
		changed++
	}
	for {
		data, err := json.Marshal(file{Version: formatVersion, Sessions: s.sessions})
		if err != nil || len(data) <= MaxFileBytes || s.current < 0 {
			return changed
		}
		before := len(s.sessions[s.current].Ops)
		s.dropOldestLocked(&s.sessions[s.current])
		if len(s.sessions[s.current].Ops) == before {
			return changed
		}
		changed++
	}
}

// sanitizeRecord bounds every field so one record cannot grow without limit.
func sanitizeRecord(r Record) Record {
	r.Op = clip(r.Op)
	r.Summary = clip(r.Summary)
	r.Result = clip(r.Result)
	r.Dest = clip(r.Dest)
	r.Sources = clipList(r.Sources, maxSources)
	r.Items = clipList(r.Items, maxItems)
	r.Errors = clipList(r.Errors, maxErrors)
	r.Details = clipList(r.Details, maxDetails)
	if r.Files < 0 {
		r.Files = 0
	}
	if r.Dirs < 0 {
		r.Dirs = 0
	}
	if r.Links < 0 {
		r.Links = 0
	}
	if r.Bytes < 0 {
		r.Bytes = 0
	}
	return r
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxText {
		cut := maxText - len("…") // leave room for the ellipsis
		for cut > 0 && !utf8Start(s[cut]) {
			cut--
		}
		return s[:cut] + "…"
	}
	return s
}

// utf8Start reports whether b begins a UTF-8 sequence (so clip never cuts a
// character in half).
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func clipList(in []string, max int) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, min(len(in), max))
	for i, s := range in {
		if i == max {
			out = append(out, fmt.Sprintf("… %d more", len(in)-max))
			break
		}
		out = append(out, clip(s))
	}
	return out
}

// load reads and validates the file, marking operations that never finished
// in an earlier run as interrupted.
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read history %s: %w", s.path, err)
	}
	if len(data) > MaxFileBytes {
		return s.quarantine(fmt.Errorf("file is larger than %d bytes", MaxFileBytes))
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil // partially written (crash before rename): start empty
	}
	var saved file
	if err := json.Unmarshal(data, &saved); err != nil {
		return s.quarantine(err)
	}
	if saved.Version < 1 {
		return s.quarantine(fmt.Errorf("unknown format version %d", saved.Version))
	}
	newer := saved.Version > formatVersion
	if newer {
		s.readOnly = true // read it, never write it
	}
	s.sessions = make([]Session, 0, len(saved.Sessions))
	for _, sess := range saved.Sessions {
		if sess.ID == "" || sess.Started.IsZero() {
			continue // unusable entry: skip it, keep the rest
		}
		ops := make([]Record, 0, len(sess.Ops))
		for _, r := range sess.Ops {
			if r.Op == "" || r.Started.IsZero() {
				continue
			}
			if !r.Status.Valid() {
				// Unknown state: never claim success.
				r.Status = StatusInterrupted
			}
			if r.Status == StatusRunning {
				r.Status = StatusInterrupted
				r.Details = append(clipList(r.Details, maxDetails-1),
					"TwinPane stopped before this operation finished; its outcome is unknown")
			}
			ops = append(ops, sanitizeRecord(r))
		}
		sess.Ops = ops
		s.sessions = append(s.sessions, sess)
	}
	sort.SliceStable(s.sessions, func(i, j int) bool {
		return s.sessions[i].Started.After(s.sessions[j].Started)
	})
	if newer {
		return &ReadOnlyError{Path: s.path, Err: fmt.Errorf("written by a newer TwinPane (format %d)", saved.Version)}
	}
	if st, err := os.Stat(s.path); err == nil {
		s.lastSize, s.lastMod = st.Size(), st.ModTime()
	}
	s.rev++
	return nil
}

// quarantine moves an unusable file aside so its data is not lost and a fresh
// history can be written.
func (s *Store) quarantine(cause error) error {
	backup := s.path + ".corrupt-" + s.now().Format("20060102T150405")
	if err := os.Rename(s.path, backup); err != nil {
		s.readOnly = true // cannot move it: never overwrite it either
		return &CorruptError{Path: s.path, Err: fmt.Errorf("%v (and it could not be moved aside: %v)", cause, err)}
	}
	s.sessions = nil
	s.err = nil
	return &CorruptError{Path: s.path, Backup: backup, Err: cause}
}

// mergeExternalLocked picks up sessions another TwinPane instance wrote since
// our last save, so running two instances does not lose the other's records.
// Our own current session always wins.
func (s *Store) mergeExternalLocked() {
	st, err := os.Stat(s.path)
	if err != nil {
		return
	}
	if st.Size() == s.lastSize && st.ModTime().Equal(s.lastMod) {
		return // our own last write
	}
	data, err := os.ReadFile(s.path)
	if err != nil || len(data) > MaxFileBytes {
		return
	}
	var saved file
	if err := json.Unmarshal(data, &saved); err != nil || saved.Version > formatVersion {
		return
	}
	known := make(map[string]bool, len(s.sessions))
	curID := s.currentIDLocked()
	for _, sess := range s.sessions {
		known[sess.ID] = true
	}
	added := 0
	for _, sess := range saved.Sessions {
		if sess.ID == "" || known[sess.ID] || sess.ID == curID {
			continue
		}
		s.sessions = append(s.sessions, sess)
		known[sess.ID] = true
		added++
	}
	if added == 0 {
		return
	}
	sort.SliceStable(s.sessions, func(i, j int) bool {
		return s.sessions[i].Started.After(s.sessions[j].Started)
	})
	for i, sess := range s.sessions {
		if sess.ID == curID {
			s.current = i
			break
		}
	}
}

// persistLocked writes the whole history atomically (temp file, fsync,
// rename), with private permissions. Failures are remembered, never fatal.
func (s *Store) persistLocked() error {
	if s.readOnly {
		return s.err
	}
	s.rev++
	s.mergeExternalLocked()
	s.pruneLocked()
	data, err := json.MarshalIndent(file{Version: formatVersion, Sessions: s.sessions}, "", "  ")
	if err != nil {
		s.err = err
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.err = fmt.Errorf("cannot create history directory %s: %w", dir, err)
		return s.err
	}
	tmp, err := os.CreateTemp(dir, ".history-*.tmp")
	if err != nil {
		s.err = fmt.Errorf("cannot write history in %s: %w", dir, err)
		return s.err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		s.err = err
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		s.err = err
		return err
	}
	if err := tmp.Sync(); err != nil {
		s.err = err
		return err
	}
	if err := tmp.Close(); err != nil {
		s.err = err
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		s.err = err
		return err
	}
	ok = true
	s.err = nil
	if d, err := os.Open(dir); err == nil {
		d.Sync() // durable rename where the platform supports it
		d.Close()
	}
	if st, err := os.Stat(s.path); err == nil {
		s.lastSize, s.lastMod = st.Size(), st.ModTime()
	}
	return nil
}

// SessionSummary is a short description of a session for lists.
func SessionSummary(s Session) string {
	n := len(s.Ops)
	done := 0
	for _, r := range s.Ops {
		if r.Status == StatusSuccess {
			done++
		}
	}
	when := s.Started.Local().Format("2006-01-02 15:04")
	switch {
	case n == 0:
		return when + " · no operations"
	case done == n:
		return fmt.Sprintf("%s · %s", when, plural(n, "operation"))
	}
	return fmt.Sprintf("%s · %s (%d successful)", when, plural(n, "operation"), done)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if strings.HasSuffix(word, "y") {
		return fmt.Sprintf("%d %s", n, strings.TrimSuffix(word, "y")+"ies")
	}
	return fmt.Sprintf("%d %ss", n, word)
}

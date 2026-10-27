package activity

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/bookmarks"
)

// CorruptError reports a log file that could not be read at all. Unlike the
// operation history, a damaged log is never a reason to move anything: the
// store keeps appending (the damaged file is simply not rotated into the way)
// and the viewer reports the skipped lines.
type CorruptError struct {
	Path string
	Err  error
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("activity log %s is unreadable: %v", e.Path, e.Err)
}

func (e *CorruptError) Unwrap() error { return e.Err }

// Options configures a store.
type Options struct {
	// Mode is how much to record (zero value is ModeOff, so a caller that
	// forgets to ask for logging gets none).
	Mode Mode
	// MaxBytes is the rotation threshold (0 = DefaultMaxBytes).
	MaxBytes int64
	// Keep is how many rotated files to keep (0 = DefaultKeep).
	Keep int
	// KeepDays removes rotated files older than this at start (0 =
	// DefaultKeepDays, negative = never).
	KeepDays int
	// Run identifies this application run; the UI passes the operation
	// history's session ID so the two records can be correlated.
	Run string
	// Version is the TwinPane version, recorded once at start.
	Version string
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Store is the activity log and its file. Every exported method is safe for
// concurrent use: commands finish on worker goroutines and log from there,
// while the interface logs from the Bubble Tea goroutine.
type Store struct {
	mu       sync.Mutex
	path     string
	mode     Mode
	maxBytes int64
	keep     int
	keepDays int
	run      string
	version  string
	now      func() time.Time

	// tempDir is set for a Memory store and removed by Close.
	tempDir string

	file *os.File // nil until the first entry
	size int64    // bytes in the current file
	seq  uint64   // sequence inside this run

	written int   // entries written since Open
	dropped int   // entries not written because of the level
	skipped int   // malformed lines ignored by the last read
	err     error // last write problem (nil when fine)
	rev     uint64
}

// DefaultPath returns the log file in the shared TwinPane configuration
// directory (the same policy as settings, bookmarks and history).
func DefaultPath() (string, error) {
	dir, err := bookmarks.ConfigDir(os.Getenv, os.UserConfigDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// ClampMaxBytes and ClampKeep bound the configurable retention.
func ClampMaxBytes(n int64) int64 {
	if n <= 0 {
		return DefaultMaxBytes
	}
	if n < MinMaxBytes {
		return MinMaxBytes
	}
	if n > MaxMaxBytes {
		return MaxMaxBytes
	}
	return n
}

// ClampKeep bounds the number of rotated files.
func ClampKeep(n int) int {
	if n <= 0 {
		return DefaultKeep
	}
	if n > KeepLimit {
		return KeepLimit
	}
	return n
}

// ClampKeepDays bounds the age limit.
func ClampKeepDays(n int) int {
	if n < 0 {
		return 0
	}
	if n == 0 {
		return DefaultKeepDays
	}
	if n > 365 {
		return 365
	}
	return n
}

// Open prepares the log at path. A missing file is not an error. Rotated files
// older than the age limit are removed. When Mode is ModeOff nothing is
// written and the store is inert.
func Open(path string, opts Options) (*Store, error) {
	if path == "" {
		return nil, errors.New("activity log path is empty")
	}
	s := &Store{
		path:     path,
		mode:     opts.Mode,
		maxBytes: ClampMaxBytes(opts.MaxBytes),
		keep:     ClampKeep(opts.Keep),
		keepDays: opts.KeepDays,
		run:      opts.Run,
		version:  opts.Version,
		now:      opts.Now,
	}
	if s.keepDays == 0 {
		s.keepDays = DefaultKeepDays
	}
	if s.keepDays < 0 {
		s.keepDays = 0
	}
	if s.now == nil {
		s.now = time.Now
	}
	if st, err := os.Stat(path); err == nil {
		s.size = st.Size()
	}
	s.pruneByAge()
	return s, nil
}

// Memory returns a store in a private temporary directory. It behaves like a
// file store and is removed by Close.
func Memory(opts Options) (*Store, error) {
	dir, err := os.MkdirTemp("", "twinpane-activity-")
	if err != nil {
		return nil, err
	}
	s, err := Open(filepath.Join(dir, FileName), opts)
	if err != nil {
		return nil, err
	}
	s.tempDir = dir
	return s, nil
}

// Path is the file the store writes.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Mode is how much is being recorded.
func (s *Store) Mode() Mode {
	if s == nil {
		return ModeOff
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// SetMode changes how much is recorded from now on. Lowering it never rewrites
// what is already on disk.
func (s *Store) SetMode(m Mode) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = m
}

// Enabled reports whether anything would be written.
func (s *Store) Enabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.mode.minLevel()
	return ok
}

// Err is the last write problem, or nil.
func (s *Store) Err() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Revision changes whenever an entry was written, so the viewer knows when its
// snapshot is stale.
func (s *Store) Revision() uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rev
}

// Written reports how many entries this run has written.
func (s *Store) Written() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written
}

// Close flushes and closes the file. It is safe to call twice.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked()
}

func (s *Store) closeLocked() error {
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	if s.tempDir != "" {
		_ = os.RemoveAll(s.tempDir)
	}
	return err
}

// Log writes one entry. Fields the caller leaves empty are filled in: version,
// time, run and sequence. The entry is filtered by the current mode, sanitized
// and redacted here, so nothing reaches the file unredacted.
func (s *Store) Log(e Entry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	min, ok := s.mode.minLevel()
	if !ok {
		s.dropped++
		return
	}
	if e.Level == "" {
		e.Level = LevelInfo.String()
	}
	if e.LevelOf() < min {
		s.dropped++
		return
	}
	if e.Time.IsZero() {
		e.Time = s.now()
	}
	if e.Run == "" {
		e.Run = s.run
	}
	s.seq++
	e.Seq = s.seq
	e = sanitizeEntry(e)
	line, err := json.Marshal(e)
	if err != nil {
		s.err = err
		return
	}
	if len(line)+1 > MaxEntryBytes {
		// Only the free-form text can be that long; drop the extras.
		e.Data = nil
		e.Err = clip(e.Err, maxErr/2)
		e.Msg = clip(e.Msg, maxMsg/2)
		if line, err = json.Marshal(e); err != nil {
			s.err = err
			return
		}
	}
	line = append(line, '\n')
	if err := s.appendLocked(line); err != nil {
		s.err = err
		return
	}
	s.size += int64(len(line))
	s.written++
	s.rev++
	s.err = nil
	if s.size >= s.maxBytes {
		s.rotateLocked()
	}
}

// Logf is Log with a formatted message.
func (s *Store) Logf(l Level, cat Category, ev, format string, args ...any) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	s.Log(Entry{Level: l.String(), Cat: cat, Ev: ev, Msg: msg})
}

// appendLocked opens the file on first use and appends one complete line with
// a single write, which keeps entries from interleaving when two instances
// share the file.
func (s *Store) appendLocked(line []byte) error {
	if s.file == nil {
		dir := filepath.Dir(s.path)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("cannot create log directory %s: %w", dir, err)
		}
		f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("cannot open activity log %s: %w", s.path, err)
		}
		s.file = f
		if st, err := f.Stat(); err == nil {
			s.size = st.Size()
		}
	}
	_, err := s.file.Write(line)
	return err
}

// rotateLocked moves the current file aside and starts a new one. Two
// instances racing here can at worst rotate twice, losing nothing but a
// filename: the content stays whole in the renamed file.
func (s *Store) rotateLocked() {
	if s.file == nil {
		return
	}
	unlock := lockFile(s.path + ".lock")
	defer unlock()
	_ = s.file.Close()
	s.file = nil
	// Shift .N-1 → .N, dropping the oldest.
	for i := s.keep; i >= 2; i-- {
		from, to := s.rotatedPath(i-1), s.rotatedPath(i)
		if i-1 == 0 {
			from = s.path
		}
		if _, err := os.Stat(from); err != nil {
			continue
		}
		_ = os.Remove(to)
		_ = os.Rename(from, to)
	}
	if _, err := os.Stat(s.path); err == nil {
		_ = os.Remove(s.rotatedPath(1))
		_ = os.Rename(s.path, s.rotatedPath(1))
	}
	s.size = 0
}

// rotatedPath is the file for rotation step i (1 = newest rotated file).
func (s *Store) rotatedPath(i int) string {
	if i <= 0 {
		return s.path
	}
	ext := filepath.Ext(s.path)
	return strings.TrimSuffix(s.path, ext) + fmt.Sprintf(".%d", i) + ext
}

// pruneByAge removes rotated files older than keepDays.
func (s *Store) pruneByAge() {
	if s.keepDays <= 0 {
		return
	}
	cutoff := s.now().Add(-time.Duration(s.keepDays) * 24 * time.Hour)
	for i := 1; i <= KeepLimit; i++ {
		p := s.rotatedPath(i)
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st.ModTime().Before(cutoff) {
			_ = os.Remove(p)
		}
	}
}

// ReadResult is what a snapshot found.
type ReadResult struct {
	Entries []Entry // newest first
	Stat    Stat
}

// Read loads the current file and its rotated neighbours, newest first, up to
// max entries. Malformed lines are skipped and counted; an unreadable file is
// reported but never stops the caller.
func Read(path string, max int) ReadResult {
	var out ReadResult
	if path == "" {
		out.Stat.Missing = true
		return out
	}
	if max <= 0 {
		max = maxEvents
	}
	files := []string{path}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; i <= KeepLimit; i++ {
		p := fmt.Sprintf("%s.%d%s", base, i, ext)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		files = append(files, p)
	}
	seenRuns := map[string]bool{}
	out.Stat.Missing = true
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		out.Stat.Missing = false
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var e Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil || e.Ev == "" {
				out.Stat.Skipped++
				continue
			}
			out.Entries = append(out.Entries, e)
		}
		// Older files are only needed when the newest one was short.
		if len(out.Entries) >= max {
			break
		}
	}
	sort.SliceStable(out.Entries, func(i, j int) bool {
		if out.Entries[i].Time.Equal(out.Entries[j].Time) {
			return out.Entries[i].Seq > out.Entries[j].Seq
		}
		return out.Entries[i].Time.After(out.Entries[j].Time)
	})
	if len(out.Entries) > max {
		out.Entries = out.Entries[:max]
	}
	out.Stat.Entries = len(out.Entries)
	for _, e := range out.Entries {
		switch e.LevelOf() {
		case LevelError:
			out.Stat.Errors++
		case LevelWarn:
			out.Stat.Warns++
		}
		if e.Run != "" {
			seenRuns[e.Run] = true
		}
	}
	out.Stat.Runs = len(seenRuns)
	return out
}

// Package search finds files by name below a directory. It is independent of
// the UI: Run walks the tree in the caller's goroutine and reports results in
// batches over a channel, so a UI can stay responsive and cancel the search
// through the context.
package search

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Query is a parsed search pattern. Matching is case-insensitive and looks
// at names only, never at file contents:
//
//   - plain text matches anywhere in the name ("rep" finds "Report.pdf");
//   - text containing * ? or [ is a wildcard pattern matched against the
//     whole name ("*.go", "img_??.jpg");
//   - text containing a / is matched against the path relative to the
//     search root instead of the name ("src/ui", "docs/*.md").
type Query struct {
	raw      string
	pattern  string // lower-cased, slash-separated
	glob     bool
	wantPath bool
}

// ParseQuery validates s.
func ParseQuery(s string) (Query, error) {
	if strings.TrimSpace(s) == "" {
		return Query{}, errors.New("type part of a file name to search for")
	}
	q := Query{raw: s, pattern: strings.ToLower(filepath.ToSlash(s))}
	q.glob = strings.ContainsAny(s, "*?[")
	q.wantPath = strings.Contains(q.pattern, "/")
	if q.wantPath {
		q.pattern = strings.Trim(q.pattern, "/")
		if q.pattern == "" {
			return Query{}, errors.New("the pattern must contain more than slashes")
		}
	}
	if q.glob {
		if _, err := filepath.Match(q.pattern, ""); err != nil {
			return Query{}, fmt.Errorf("invalid wildcard pattern %q", s)
		}
	}
	return q, nil
}

func (q Query) String() string { return q.raw }

// Match reports whether an entry matches. rel is the slash-separated path
// relative to the search root, name the base name.
func (q Query) Match(rel, name string) bool {
	return q.matchLower(strings.ToLower(rel), strings.ToLower(name))
}

// matchLower is Match for already lower-cased rel and name.
func (q Query) matchLower(rel, name string) bool {
	subject := name
	if q.wantPath {
		subject = rel
	}
	if q.glob {
		if q.wantPath {
			// "docs/*.md" should also match "a/docs/x.md": try every suffix
			// that starts at a path component.
			for {
				if ok, _ := filepath.Match(q.pattern, subject); ok {
					return true
				}
				i := strings.IndexByte(subject, '/')
				if i < 0 {
					return false
				}
				subject = subject[i+1:]
			}
		}
		ok, _ := filepath.Match(q.pattern, subject)
		return ok
	}
	return strings.Contains(subject, q.pattern)
}

// Options control a search.
type Options struct {
	ShowHidden bool          // descend into / report dot-files
	MaxResults int           // stop after this many matches (0 = DefaultMaxResults)
	Interval   time.Duration // batch interval (0 = DefaultInterval)
}

const (
	DefaultMaxResults = 10000
	DefaultInterval   = 100 * time.Millisecond
	maxBatch          = 1000
)

// Result is one match.
type Result struct {
	Path   string // absolute path
	Rel    string // relative to the search root (OS separators)
	IsDir  bool
	IsLink bool
}

// Update is sent periodically while the search runs; the final one has
// Done set. Counters are cumulative, Results only holds the new matches.
type Update struct {
	Results   []Result
	Scanned   int    // entries examined
	Errors    int    // directories that could not be read
	LastError string // most recent of those errors
	Done      bool
	Truncated bool  // stopped at MaxResults
	Canceled  bool  // stopped through the context
	Err       error // the root itself could not be read
}

// Run searches root and sends updates to out until done or canceled. It
// returns when the final update has been delivered (or ctx is canceled and
// nobody is listening). Symbolic links are reported but never followed, so
// link loops cannot make the search run forever; unreadable directories are
// counted and skipped.
func Run(ctx context.Context, root string, q Query, opts Options, out chan<- Update) {
	if opts.MaxResults <= 0 {
		opts.MaxResults = DefaultMaxResults
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	var (
		batch    []Result
		total    Update // counters
		found    int
		lastSend = time.Now()
	)
	send := func(u Update) bool {
		select {
		case out <- u:
			return true
		case <-ctx.Done():
			return false
		}
	}
	flush := func(final bool) bool {
		u := total
		u.Results, batch = batch, nil
		u.Done = final
		lastSend = time.Now()
		return send(u)
	}

	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if err != nil {
			if p == root {
				return err
			}
			// Unreadable directory (WalkDir reports it a second time, after
			// it was passed to us once without error) or vanished entry.
			total.Errors++
			total.LastError = err.Error()
			return nil
		}
		if p == root {
			return nil
		}
		name := d.Name()
		if !opts.ShowHidden && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		total.Scanned++
		rel, _ := filepath.Rel(root, p)
		if q.Match(filepath.ToSlash(rel), name) {
			r := Result{Path: p, Rel: rel, IsDir: d.IsDir(), IsLink: d.Type()&fs.ModeSymlink != 0}
			if r.IsLink {
				if fi, err := os.Stat(p); err == nil && fi.IsDir() {
					r.IsDir = true // shown as a folder, but not descended into
				}
			}
			batch = append(batch, r)
			found++
			if found >= opts.MaxResults {
				total.Truncated = true
				return filepath.SkipAll
			}
		}
		if len(batch) >= maxBatch || time.Since(lastSend) >= opts.Interval {
			if !flush(false) {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
		total.Err = walkErr
	}
	if ctx.Err() != nil {
		total.Canceled = true
		// Deliver what we have if someone is still listening, but never
		// block on a canceled search.
		u := total
		u.Results, u.Done = batch, true
		select {
		case out <- u:
		default:
		}
		return
	}
	flush(true)
}

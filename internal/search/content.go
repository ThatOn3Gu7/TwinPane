// Package search includes the asynchronous content-search engine used by
// TwinPane's native Advanced Search view. Content traversal is bounded,
// cancellable, and independent of terminal/UI code.
package search

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
)

// ContentOptions bounds a recursive content search. A zero value uses the
// documented defaults. MaxEntries counts visited non-root paths, including
// directories; MaxFiles caps regular-file scan attempts. Exact .git
// directories are skipped unless IncludeGit is explicitly set; other
// dot-files and dot-directories remain searchable.
type ContentOptions struct {
	IncludeGit        bool
	MaxEntries        int // total non-root paths visited, including directories
	MaxFiles          int
	MaxResults        int
	MaxBytesPerFile   int64
	MaxBytesTotal     int64
	MaxMatchesPerFile int
	Interval          time.Duration
}

const (
	DefaultContentMaxEntries      = 500_000
	DefaultContentMaxFiles        = 200_000
	DefaultContentMaxResults      = 10_000
	DefaultContentMaxBytesPerFile = int64(1 << 20)
	DefaultContentMaxBytesTotal   = int64(512 << 20)
	DefaultContentMaxMatches      = 5
	contentMaxLines               = 100_000
	contentMaxLineBytes           = 16 << 10
	contentBatchSize              = 32
)

// ContentMatch is a bounded line excerpt from a matching file. Line is
// one-based, and Text contains no line ending.
type ContentMatch struct {
	Line int
	Text string
}

// ContentResult is one file containing at least one query match.
type ContentResult struct {
	Path    string
	Rel     string
	Matches []ContentMatch
}

// ContentUpdate is cumulative progress plus only the newly found Results.
// The final update has Done set. Errors includes unreadable paths; Binary is
// the number of binary/non-text files skipped; TruncatedFiles counts files
// for which only the configured prefix could be examined.
type ContentUpdate struct {
	Results        []ContentResult
	ScannedFiles   int
	ScannedDirs    int
	BytesRead      int64
	Errors         int
	LastError      string
	Binary         int
	TruncatedFiles int
	Done           bool
	Truncated      bool
	Canceled       bool
	Err            error
}

// RunContent searches text files below root for query and streams progress
// to out. Matching is smart-case (lowercase queries ignore case; a query
// containing capitals is case-sensitive). Symbolic links and special files
// are never followed or opened. Exact .git directory names are pruned at any
// depth by default; names such as .gitignore and .github are not special.
func RunContent(ctx context.Context, root, query string, opt ContentOptions, out chan<- ContentUpdate) {
	if opt.MaxEntries <= 0 {
		opt.MaxEntries = DefaultContentMaxEntries
	}
	if opt.MaxFiles <= 0 {
		opt.MaxFiles = DefaultContentMaxFiles
	}
	if opt.MaxResults <= 0 {
		opt.MaxResults = DefaultContentMaxResults
	}
	if opt.MaxBytesPerFile <= 0 {
		opt.MaxBytesPerFile = DefaultContentMaxBytesPerFile
	}
	if opt.MaxBytesTotal <= 0 {
		opt.MaxBytesTotal = DefaultContentMaxBytesTotal
	}
	if opt.MaxMatchesPerFile <= 0 {
		opt.MaxMatchesPerFile = DefaultContentMaxMatches
	}
	if opt.Interval <= 0 {
		opt.Interval = DefaultInterval
	}

	var (
		total    ContentUpdate
		batch    []ContentResult
		found    int
		lastSend = time.Now()
	)
	send := func(final bool) bool {
		u := total
		u.Results, batch = batch, nil
		u.Done = final
		lastSend = time.Now()
		select {
		case out <- u:
			return true
		case <-ctx.Done():
			return false
		}
	}
	flushIfDue := func() bool {
		if len(batch) >= contentBatchSize || time.Since(lastSend) >= opt.Interval {
			return send(false)
		}
		return true
	}

	if ctx.Err() != nil {
		total.Canceled, total.Done = true, true
		select {
		case out <- total:
		default:
		}
		return
	}
	if strings.TrimSpace(query) == "" {
		send(true)
		return
	}
	rootPath := filepath.Clean(root)
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		total.Err = err
		send(true)
		return
	}
	if !rootInfo.IsDir() {
		total.Err = errors.New("search root is not a directory")
		send(true)
		return
	}
	walkRoot := startRoot(rootPath) // also descends into a symlinked root, without following child links
	seenEntries := 0

	walkErr := filepath.WalkDir(walkRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		isRoot := filepath.Clean(path) == rootPath
		if walkErr != nil {
			if isRoot {
				total.Err = walkErr
				return walkErr
			}
			total.Errors++
			total.LastError = walkErr.Error()
			return flushIfDueOrContinue(flushIfDue)
		}
		if entry.IsDir() && !opt.IncludeGit && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if isRoot {
			return nil
		}
		if seenEntries >= opt.MaxEntries {
			total.Truncated = true
			return filepath.SkipAll
		}
		seenEntries++
		if entry.IsDir() {
			total.ScannedDirs++
			return flushIfDueOrContinue(flushIfDue)
		}

		info, infoErr := entry.Info()
		if infoErr != nil {
			total.Errors++
			total.LastError = infoErr.Error()
			return flushIfDueOrContinue(flushIfDue)
		}
		if !info.Mode().IsRegular() {
			return nil // includes symlinks and all special files
		}
		if total.ScannedFiles >= opt.MaxFiles {
			total.Truncated = true
			return filepath.SkipAll
		}
		remaining := opt.MaxBytesTotal - total.BytesRead
		if remaining <= 0 {
			total.Truncated = true
			return filepath.SkipAll
		}

		total.ScannedFiles++
		f, fileInfo, openErr := openRegularContentFile(path)
		if openErr != nil {
			total.Errors++
			total.LastError = openErr.Error()
			return flushIfDueOrContinue(flushIfDue)
		}
		maxBytes := minInt64(opt.MaxBytesPerFile, remaining)
		doc, loadErr := textview.Load(f, fileInfo.Size(), textview.Options{
			MaxBytes: maxBytes, MaxLines: contentMaxLines, MaxLineBytes: contentMaxLineBytes,
		})
		closeErr := f.Close()
		if loadErr != nil {
			total.Errors++
			total.LastError = loadErr.Error()
			return flushIfDueOrContinue(flushIfDue)
		}
		if closeErr != nil {
			total.Errors++
			total.LastError = closeErr.Error()
		}
		total.BytesRead += doc.Read
		if doc.Binary {
			total.Binary++
		} else {
			lines := textview.Find(doc.Lines, query, opt.MaxMatchesPerFile)
			if len(lines) > 0 {
				result := ContentResult{Path: path}
				result.Rel, _ = filepath.Rel(rootPath, path)
				result.Matches = make([]ContentMatch, 0, len(lines))
				for _, line := range lines {
					result.Matches = append(result.Matches, ContentMatch{Line: line + 1, Text: doc.Lines[line]})
				}
				batch = append(batch, result)
				found++
			}
			if doc.Truncated {
				total.TruncatedFiles++
			}
		}
		if found >= opt.MaxResults {
			total.Truncated = true
			return filepath.SkipAll
		}
		return flushIfDueOrContinue(flushIfDue)
	})
	if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) && total.Err == nil {
		total.Err = walkErr
	}

	if ctx.Err() != nil {
		total.Canceled = true
		u := total
		u.Results, u.Done = batch, true
		select {
		case out <- u:
		default:
		}
		return
	}
	send(true)
}

// flushIfDueOrContinue converts an output back-pressure cancellation into a
// clean WalkDir stop. It is kept small so every progress/error path is
// cancellation-aware.
func flushIfDueOrContinue(flush func() bool) error {
	if !flush() {
		return filepath.SkipAll
	}
	return nil
}

// minInt64 returns the smaller non-negative limit.
func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

package ui

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/cloud"
	"github.com/ThatOn3Gu7/TwinPane/internal/fileinfo"
	"github.com/ThatOn3Gu7/TwinPane/internal/history"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// This file records file operations in the persistent operation history. Every
// background task is recorded by newSession (start) and finishTask (outcome);
// operations that run synchronously record themselves with recordOperation.
// Nothing else is recorded: navigation, dialogs and other UI interactions are
// not operations.

// historyStatus maps a task's state to a stored status. States that are still
// in progress are stored as running and become "interrupted" if TwinPane exits
// before the outcome is known.
func historyStatus(state taskState) history.Status {
	switch state {
	case taskCompleted:
		return history.StatusSuccess
	case taskCanceled:
		return history.StatusCanceled
	case taskPartial:
		return history.StatusPartial
	case taskFailed:
		return history.StatusFailed
	}
	return history.StatusRunning
}

// opHistory is the history data a background task carries until it finishes.
type opHistory struct {
	id     string   // record ID in the store ("" = not recorded)
	op     string   // operation type, e.g. "Copy"
	result string   // tool result: exit codes, plan results
	errors []string // the failures, kept separate from details
	files  int      // counts, when the operation reports them
	dirs   int      //
	links  int      //
	bytes  int64    //
}

// cloudResultText describes an rclone outcome: its exit code, or "" when the
// error did not come from rclone.
func cloudResultText(err error) string {
	var ce *cloud.Error
	if !errors.As(err, &ce) || ce == nil || ce.Code == 0 {
		return ""
	}
	return fmt.Sprintf("rclone exit code %d", ce.Code)
}

// opTitleNames normalises the task titles used by the different operation
// flows into one operation type.
var opTitleNames = map[string]string{
	"copy": "Copy", "copying": "Copy",
	"move": "Move", "moving": "Move",
	"delete": "Delete", "deleting": "Delete",
	"moving to trash": "Delete (trash)",
	"renaming":        "Rename",
	"extracting":      "Extract",
	"creating folder": "New folder",
	"downloading":     "Download",
	"uploading":       "Upload",
}

// opTitle is the operation type shown in the history. Tasks start as
// "Checking copy" and are renamed to "Copying" once the work begins, so both
// become "Copy".
func opTitle(title string) string {
	t := strings.TrimSpace(title)
	t = strings.TrimSpace(strings.TrimPrefix(t, "Checking "))
	if name, ok := opTitleNames[strings.ToLower(t)]; ok {
		return name
	}
	if t == "" {
		return "Operation"
	}
	return capitalize(t)
}

// historyBegin records the start of a background task. A failure to record
// never affects the operation: the store reports its own problems.
func (a *App) historyBegin(s *opSession) {
	if a == nil || a.history == nil || s == nil {
		return
	}
	s.hist.op = opTitle(s.title)
	s.hist.id = a.history.Start(history.Record{
		Op:      s.hist.op,
		Status:  history.StatusRunning,
		Started: s.started,
		Sources: append([]string(nil), s.from...),
	})
}

// historyTouch keeps a running record's description current once the task
// knows what it is doing (the transfer title and destination are only set
// after newSession). An operation killed mid-flight therefore still shows its
// destination.
func (a *App) historyTouch(s *opSession) {
	if a == nil || a.history == nil || s == nil || s.hist.id == "" {
		return
	}
	a.history.Update(s.hist.id, history.Update{
		Op:      opTitle(s.title),
		Dest:    s.to,
		Sources: append([]string(nil), s.from...),
		Items:   sourceNames(s.from),
	})
}

// historyComplete stores the outcome of a background task. It is called from
// finishTask, so every terminal path (success, failure, cancellation) is
// recorded exactly once.
func (a *App) historyComplete(s *opSession) {
	if a == nil || a.history == nil || s == nil || s.hist.id == "" {
		return
	}
	st := historyStatus(s.state)
	rec := history.Finish{
		Status:   st,
		Op:       opTitle(s.title),
		Finished: s.finished,
		Summary:  s.summary,
		Result:   s.hist.result,
		Sources:  append([]string(nil), s.from...),
		Dest:     s.to,
		Items:    sourceNames(s.from),
		Errors:   append([]string(nil), s.hist.errors...),
		Details:  historyDetails(s.summary, s.details),
		Files:    s.hist.files,
		Dirs:     s.hist.dirs,
		Links:    s.hist.links,
		Bytes:    s.hist.bytes,
	}
	if len(rec.Errors) == 0 && (st == history.StatusFailed || st == history.StatusPartial) {
		// No structured error was captured: keep the task's explanation so a
		// failure is never recorded without a reason.
		rec.Errors = append(rec.Errors, s.details...)
	}
	a.history.Finish(s.hist.id, rec)
}

// recordOperation stores an operation that already finished (foreground work
// such as rename or a new folder).
func (a *App) recordOperation(r history.Record) {
	if a == nil || a.history == nil {
		return
	}
	if r.Status == "" {
		r.Status = history.StatusSuccess
	}
	a.history.Add(r)
}

// recordRename stores a rename (or a permission change) performed in the
// foreground.
func (a *App) recordRename(op string, started time.Time, src, dst string, err error, notes ...string) {
	rec := history.Record{
		Op:      op,
		Started: started,
		Sources: []string{src},
		Dest:    dst,
	}
	if dst != "" {
		rec.Items = []string{filepath.Base(dst)}
	} else {
		rec.Items = []string{filepath.Base(src)}
	}
	if err != nil {
		rec.Status = history.StatusFailed
		rec.Summary = op + " failed: " + errText(err)
		rec.Errors = []string{errText(err)}
	} else {
		rec.Status = history.StatusSuccess
		rec.Summary = op + " " + quotedBase(src) + successArrow(dst)
	}
	rec.Details = notes
	a.recordOperation(rec)
}

// historyStatusFor maps a foreground outcome (nil = success) to a status.
func historyStatusFor(err error) history.Status {
	if err != nil {
		return history.StatusFailed
	}
	return history.StatusSuccess
}

// historyErrors wraps a foreground error for a record.
func historyErrors(err error) []string {
	if err == nil {
		return nil
	}
	return []string{errText(err)}
}

// permSummary describes a chmod outcome.
func permSummary(path string, perm fs.FileMode, err error) string {
	name := fmt.Sprintf("%q", filepath.Base(path))
	if err != nil {
		return "Permissions not changed on " + name + ": " + errText(err)
	}
	return "Permissions of " + name + " set to " + fileinfo.Symbolic(perm) + " (" + fileinfo.Octal(perm) + ")"
}

// recordNewItem stores a newly created file or folder.
func (a *App) recordNewItem(op string, started time.Time, path string, err error) {
	rec := history.Record{
		Op:      op,
		Started: started,
		Dest:    filepath.Dir(path),
		Items:   []string{filepath.Base(path)},
	}
	if err != nil {
		rec.Status = history.StatusFailed
		rec.Summary = op + " failed: " + filepath.Base(path) + ": " + errText(err)
		rec.Errors = []string{errText(err)}
	} else {
		rec.Status = history.StatusSuccess
		rec.Summary = op + " " + quotedBase(path)
	}
	a.recordOperation(rec)
}

// recordBulkRename stores the outcome of a batch rename as one operation.
func (a *App) recordBulkRename(started time.Time, dir string, res ops.BatchResult, err error) {
	rec := history.Record{
		Op:      "Bulk rename",
		Started: started,
		Sources: []string{dir},
		Dest:    dir,
	}
	if err != nil {
		rec.Status = history.StatusFailed
		rec.Summary = "Bulk rename refused: " + err.Error()
		rec.Errors = []string{err.Error()}
		a.recordOperation(rec)
		return
	}
	rec.Files = res.Renamed
	for _, item := range res.Items {
		switch item.Outcome {
		case ops.OutcomeRenamed:
			rec.Items = append(rec.Items, item.Old+" → "+item.New)
		case ops.OutcomeFailed:
			rec.Errors = append(rec.Errors, fmt.Sprintf("%s → %s: %s", item.Old, item.New, errText(item.Err)))
		case ops.OutcomeBlocked:
			rec.Errors = append(rec.Errors, fmt.Sprintf("%s → %s: blocked by an item that could not be renamed", item.Old, item.New))
		case ops.OutcomeCanceled:
			rec.Errors = append(rec.Errors, fmt.Sprintf("%s → %s: canceled", item.Old, item.New))
		}
		if item.Stranded != "" {
			rec.Details = append(rec.Details,
				fmt.Sprintf("%s is now named %q (its original name was taken)", item.Old, item.Stranded))
		}
	}
	switch {
	case len(res.Items) == 0:
		rec.Status = history.StatusSuccess
		rec.Summary = "Bulk rename: nothing to rename"
	case res.Failed() == 0:
		rec.Status = history.StatusSuccess
		rec.Summary = fmt.Sprintf("Bulk rename: renamed %s", plural(res.Renamed, "item"))
	case res.Renamed > 0:
		rec.Status = history.StatusPartial
		rec.Summary = fmt.Sprintf("Bulk rename: %d renamed, %d failed", res.Renamed, res.Failed())
	default:
		rec.Status = history.StatusFailed
		rec.Summary = fmt.Sprintf("Bulk rename: %d failed", res.Failed())
	}
	a.recordOperation(rec)
}

func quotedBase(p string) string { return fmt.Sprintf("%q", filepath.Base(p)) }

func successArrow(dst string) string {
	if dst == "" {
		return ""
	}
	return " → " + dst
}

// sourceNames returns the top-level names of the given paths, in order.
func sourceNames(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		out = append(out, filepath.Base(filepath.Clean(p)))
	}
	return out
}

// historyDetails drops detail lines that only repeat the summary, so one
// message is not stored twice.
func historyDetails(summary string, details []string) []string {
	out := make([]string, 0, len(details))
	for _, d := range details {
		if strings.TrimSpace(d) == "" || d == summary {
			continue
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// itemErrorTexts renders operation item errors for the history record.
func itemErrorTexts(items []ops.ItemError) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Error())
	}
	return out
}

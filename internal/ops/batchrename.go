package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Batch renaming: several entries of one directory get new names at once.
// It is not a loop over Rename: names may swap (a→b, b→a), form chains
// (a→b, b→c), or change only in case on a case-insensitive filesystem, and
// all names are validated together before anything is touched.
//
//   - PlanBatchRename validates a proposal: invalid names, duplicates within
//     the batch, collisions with entries outside the batch or with batch
//     entries that keep their name, and sources that have vanished.
//   - ApplyBatchRename executes a valid plan in dependency order. Cycles and
//     case-only renames go through a temporary name in the same directory.
//     Every step is a no-replace rename, so an existing file is never
//     overwritten, even if it appears while the batch runs. Failures do not
//     stop independent renames; renames that depended on a failed one are
//     reported as blocked, and temporary names are restored when possible.

// RenameStatus classifies one proposed rename.
type RenameStatus int

const (
	RenameUnchanged RenameStatus = iota // new name == old name: left alone
	RenameOK                            // will be renamed
	RenameInvalid                       // the new name is not allowed
	RenameDuplicate                     // another batch entry gets the same name
	RenameExists                        // an entry outside the batch (or one keeping its name) has it
	RenameMissing                       // the source no longer exists
)

// IsError reports whether the status prevents applying the plan.
func (s RenameStatus) IsError() bool { return s >= RenameInvalid }

// PlanItem is one entry of a batch.
type PlanItem struct {
	Old, New string
	Status   RenameStatus
	Reason   string // for errors: why
}

// RenamePlan is a validated batch proposal.
type RenamePlan struct {
	Dir             string
	Items           []PlanItem
	CaseInsensitive bool // the directory's filesystem folds case
	Changes         int  // items with status RenameOK
	Errors          int  // items with an error status
}

// Ready reports whether the plan can be applied.
func (p RenamePlan) Ready() bool { return p.Changes > 0 && p.Errors == 0 }

// PlanBatchRename validates renaming olds[i] to news[i] inside dir.
func PlanBatchRename(dir string, olds, news []string) RenamePlan {
	plan := RenamePlan{Dir: dir, Items: make([]PlanItem, len(olds))}
	plan.CaseInsensitive = caseInsensitiveDir(dir, olds)
	key := func(s string) string {
		if plan.CaseInsensitive {
			return foldName(s)
		}
		return s
	}

	// Final name of every entry after the batch (unchanged entries keep theirs).
	finalCount := map[string]int{} // by comparison key
	exactCount := map[string]int{} // by exact name
	keeps := map[string]string{}   // key -> name of an unchanged entry holding it
	for i, old := range olds {
		n := old
		if i < len(news) {
			n = news[i]
		}
		plan.Items[i] = PlanItem{Old: old, New: n}
		finalCount[key(n)]++
		exactCount[n]++
		if n == old {
			keeps[key(old)] = old
		}
	}
	// Names currently held by batch entries that are being renamed: such a
	// target becomes free during the batch.
	leaving := map[string]bool{}
	for _, it := range plan.Items {
		if it.New != it.Old {
			leaving[key(it.Old)] = true
		}
	}

	for i := range plan.Items {
		it := &plan.Items[i]
		if it.New == it.Old {
			continue // RenameUnchanged
		}
		invalid := ValidateName(it.New)
		switch {
		case !pathExists(filepath.Join(dir, it.Old)):
			it.Status, it.Reason = RenameMissing, "no longer exists"
		case invalid != nil:
			it.Status, it.Reason = RenameInvalid, invalid.Error()
		case keeps[key(it.New)] != "" && key(it.New) != key(it.Old):
			it.Status = RenameExists
			it.Reason = fmt.Sprintf("%q keeps this name", keeps[key(it.New)])
		case finalCount[key(it.New)] > 1:
			it.Status, it.Reason = RenameDuplicate, "another item gets the same name"
			if exactCount[it.New] <= 1 {
				it.Reason = "another item gets the same name (names differing only in case clash on this filesystem)"
			}
		case key(it.New) != key(it.Old) && !leaving[key(it.New)] && pathExists(filepath.Join(dir, it.New)):
			it.Status, it.Reason = RenameExists, "an item with this name already exists"
		default:
			it.Status = RenameOK
		}
	}
	for _, it := range plan.Items {
		switch {
		case it.Status == RenameOK:
			plan.Changes++
		case it.Status.IsError():
			plan.Errors++
		}
	}
	return plan
}

func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// foldName is the comparison key on case-insensitive filesystems.
func foldName(s string) string { return strings.ToLower(s) }

// caseInsensitiveDir probes whether dir folds case, by looking up a
// case-swapped variant of an existing entry and checking that it resolves
// to the same file.
func caseInsensitiveDir(dir string, names []string) bool {
	for _, n := range names {
		swapped := swapCase(n)
		if swapped == n {
			continue // no cased letters
		}
		fi, err := os.Lstat(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		si, err := os.Lstat(filepath.Join(dir, swapped))
		return err == nil && os.SameFile(fi, si)
	}
	return false
}

func swapCase(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsUpper(r):
			return unicode.ToLower(r)
		case unicode.IsLower(r):
			return unicode.ToUpper(r)
		}
		return r
	}, s)
}

// BatchOutcome is the result for one item of an applied plan.
type BatchOutcome int

const (
	OutcomeRenamed  BatchOutcome = iota
	OutcomeFailed                // the rename itself failed
	OutcomeBlocked               // not attempted: its target is still held by an item that failed
	OutcomeCanceled              // not attempted: canceled
)

// BatchItemResult reports one item.
type BatchItemResult struct {
	Old, New string
	Outcome  BatchOutcome
	Err      error
	// Stranded is set when the item was left under a temporary name that
	// could not be changed back (its original name was taken meanwhile).
	Stranded string
}

// BatchResult summarizes ApplyBatchRename.
type BatchResult struct {
	Items   []BatchItemResult // one per changed item, in plan order
	Renamed int
}

// Failed counts items that were not renamed.
func (r BatchResult) Failed() int { return len(r.Items) - r.Renamed }

// batchRenameFn performs each step; tests substitute failures.
var batchRenameFn = renameNoReplace

// tempPrefix starts the temporary names used for cycles and case changes.
const tempPrefix = ".twinpane-rename-"

// ApplyBatchRename executes a plan built by PlanBatchRename. Only items with
// status RenameOK are touched; a plan with errors is refused.
func ApplyBatchRename(ctx context.Context, plan RenamePlan) (BatchResult, error) {
	if plan.Errors > 0 {
		return BatchResult{}, errors.New("the rename plan has problems: fix them first")
	}
	key := func(s string) string {
		if plan.CaseInsensitive {
			return foldName(s)
		}
		return s
	}

	type job struct {
		res     BatchItemResult
		cur     string // current name on disk
		done    bool   // finished (renamed, failed, blocked or canceled)
		viaTemp bool   // must step through a temporary name (case-only change)
	}
	var jobs []*job
	for _, it := range plan.Items {
		if it.Status == RenameOK {
			j := &job{res: BatchItemResult{Old: it.Old, New: it.New}, cur: it.Old}
			// Case-only change on a case-insensitive filesystem: the target
			// "exists" (it is the item itself). Going through a temporary
			// name works everywhere and never depends on the filesystem
			// treating the rename specially.
			j.viaTemp = plan.CaseInsensitive && key(it.New) == key(it.Old)
			jobs = append(jobs, j)
		}
	}
	// holder returns the pending job currently occupying name n (other than self).
	holder := func(n string, self *job) *job {
		for _, j := range jobs {
			if j != self && !j.done && key(j.cur) == key(n) {
				return j
			}
		}
		return nil
	}
	// A job whose name is held by a failed or blocked item can never proceed.
	heldByStuck := func(n string, self *job) bool {
		for _, j := range jobs {
			if j != self && j.done && j.res.Outcome != OutcomeRenamed && key(j.cur) == key(n) {
				return true
			}
		}
		return false
	}
	tmpSeq := 0
	newTemp := func() string {
		for {
			tmpSeq++
			n := fmt.Sprintf("%s%d-%d", tempPrefix, os.Getpid(), tmpSeq)
			if !pathExists(filepath.Join(plan.Dir, n)) {
				return n
			}
		}
	}
	move := func(j *job, to string) error {
		err := batchRenameFn(filepath.Join(plan.Dir, j.cur), filepath.Join(plan.Dir, to))
		if err == nil {
			j.cur = to
		}
		return err
	}
	finish := func(j *job, o BatchOutcome, err error) {
		j.done, j.res.Outcome, j.res.Err = true, o, err
	}

	for {
		pending := 0
		progressed := false
		for _, j := range jobs {
			if j.done {
				continue
			}
			pending++
			if ctx.Err() != nil {
				finish(j, OutcomeCanceled, ctx.Err())
				progressed = true
				continue
			}
			if j.viaTemp && j.cur == j.res.Old {
				tmp := newTemp()
				if err := move(j, tmp); err != nil {
					finish(j, OutcomeFailed, err)
				}
				progressed = true
				continue
			}
			if heldByStuck(j.res.New, j) {
				finish(j, OutcomeBlocked, nil)
				progressed = true
				continue
			}
			if holder(j.res.New, j) != nil {
				continue // wait until the holder has moved away
			}
			if err := move(j, j.res.New); err != nil {
				finish(j, OutcomeFailed, err)
			} else {
				finish(j, OutcomeRenamed, nil)
			}
			progressed = true
		}
		if pending == 0 {
			break
		}
		if !progressed {
			// Every pending job waits for another one: a cycle (a→b, b→a).
			// Break it by parking one member under a temporary name.
			for _, j := range jobs {
				if !j.done {
					if err := move(j, newTemp()); err != nil {
						finish(j, OutcomeFailed, err)
					}
					break
				}
			}
		}
	}

	// Items left under a temporary name (their final rename failed or was
	// blocked/canceled): try to restore the original name.
	var res BatchResult
	for _, j := range jobs {
		if j.res.Outcome != OutcomeRenamed && j.cur != j.res.Old {
			if err := move(j, j.res.Old); err != nil {
				j.res.Stranded = j.cur
			}
		}
		if j.res.Outcome == OutcomeRenamed {
			res.Renamed++
		}
		res.Items = append(res.Items, j.res)
	}
	return res, nil
}

// IsBatchTemp reports whether name is one of the temporary names used by
// ApplyBatchRename (left behind only if the process died mid-batch).
func IsBatchTemp(name string) bool { return strings.HasPrefix(name, tempPrefix) }

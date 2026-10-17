package ops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// homeDirFn is the home directory protected from deletion (test seam).
var homeDirFn = func() string {
	h, _ := os.UserHomeDir()
	return h
}

// DeletePlan is the result of inspecting what a deletion would affect. The
// UI shows it to the user before anything is removed.
type DeletePlan struct {
	Paths      []string
	TopDirs    int   // top-level folders (not symlinks)
	TopFiles   int   // top-level files, symlinks and other entries
	Files      int   // all non-directories that would be removed (recursive)
	Dirs       int   // all directories that would be removed (recursive, incl. top-level)
	Bytes      int64 // total size of regular files
	Recursive  bool  // at least one top-level folder has contents
	Incomplete bool  // some folders couldn't be read: counts are minimums
	UseTrash   bool  // items will be moved to the trash (recoverable)
	TrashName  string
	TrashNote  string // why the trash can't be used (when !UseTrash)
}

// CheckDeletable rejects paths that must never be deleted: relative paths,
// "." / "..", filesystem roots, the home directory and any folder that
// contains it.
func CheckDeletable(p string) error {
	if p == "" || !filepath.IsAbs(p) {
		return fmt.Errorf("refusing to delete %q: not an absolute path", p)
	}
	clean := filepath.Clean(p)
	base := filepath.Base(clean)
	if filepath.Dir(clean) == clean {
		return fmt.Errorf("refusing to delete the filesystem root %s", clean)
	}
	if base == "." || base == ".." {
		return fmt.Errorf("refusing to delete %q", p)
	}
	fi, err := os.Lstat(clean)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if home := homeDirFn(); home != "" {
			if inside, _ := isInside(home, fi); inside {
				return fmt.Errorf("refusing to delete %s: it is (or contains) your home directory", clean)
			}
		}
	}
	return nil
}

// PlanDelete validates paths and measures what deleting them would remove.
// trash may be nil (permanent deletion only).
func PlanDelete(ctx context.Context, paths []string, trash Trasher) (DeletePlan, error) {
	plan := DeletePlan{}
	if len(paths) == 0 {
		return plan, errors.New("nothing to delete")
	}
	for _, p := range paths {
		if err := CheckDeletable(p); err != nil {
			return plan, err
		}
		p = filepath.Clean(p)
		plan.Paths = append(plan.Paths, p)
		fi, _ := os.Lstat(p)
		if fi.IsDir() {
			plan.TopDirs++
		} else {
			plan.TopFiles++
		}
		err := filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				plan.Incomplete = true
				return nil
			}
			if d.IsDir() {
				plan.Dirs++
				if q != p {
					plan.Recursive = true
				}
			} else {
				plan.Files++
				if q != p {
					plan.Recursive = true
				}
				if d.Type().IsRegular() {
					if info, err := d.Info(); err == nil {
						plan.Bytes += info.Size()
					}
				}
			}
			return nil
		})
		if err != nil {
			return plan, err
		}
	}
	if trash == nil {
		plan.TrashNote = "no trash available"
		return plan, nil
	}
	plan.TrashName = trash.Name()
	plan.UseTrash = true
	for _, p := range plan.Paths {
		if ok, why := trash.Available(p); !ok {
			plan.UseTrash = false
			plan.TrashNote = why
			break
		}
	}
	return plan, nil
}

// DeleteResult reports what a deletion actually did.
type DeleteResult struct {
	UseTrash bool
	Removed  int // top-level items completely removed (or trashed)
	Files    int // non-directories removed (permanent mode)
	Dirs     int // directories removed (permanent mode)
	Failed   []ItemError
	Canceled bool
}

// OK reports whether every item was removed.
func (r DeleteResult) OK() bool { return !r.Canceled && len(r.Failed) == 0 }

// Delete executes a plan produced by PlanDelete. Every path is re-checked
// with CheckDeletable immediately before it is touched. In trash mode a
// failure to trash an item is reported; it is never silently deleted
// permanently instead.
func Delete(ctx context.Context, plan DeletePlan, trash Trasher, hooks Hooks) DeleteResult {
	res := DeleteResult{UseTrash: plan.UseTrash && trash != nil}
	prog := Progress{Phase: "deleting", TotalItems: plan.Files + plan.Dirs}
	if res.UseTrash {
		prog.Phase = "trashing"
		prog.TotalItems = len(plan.Paths)
	}
	var last time.Time
	report := func(force bool) {
		if hooks.Progress != nil && (force || time.Since(last) >= reportInterval) {
			last = time.Now()
			hooks.Progress(prog)
		}
	}
	fail := func(p, op string, err error) {
		ie := ItemError{Path: p, Op: op, Err: err}
		res.Failed = append(res.Failed, ie)
		prog.Errors++
		prog.LastError = ie.Error()
	}

	var removeTree func(p string) bool
	removeTree = func(p string) bool {
		if ctx.Err() != nil {
			res.Canceled = true
			return false
		}
		prog.Current = p
		report(false)
		fi, err := os.Lstat(p)
		if err != nil {
			fail(p, "delete", err)
			return false
		}
		if fi.IsDir() { // never true for symlinks: links are removed, not followed
			entries, err := os.ReadDir(p)
			if err != nil {
				fail(p, "read folder", err)
				return false
			}
			all := true
			for _, ent := range entries {
				if !removeTree(filepath.Join(p, ent.Name())) {
					all = false
				}
				if res.Canceled {
					return false
				}
			}
			if !all {
				return false // folder kept because something inside it remains
			}
			if err := os.Remove(p); err != nil {
				fail(p, "delete folder", err)
				return false
			}
			res.Dirs++
			prog.DoneItems++
			return true
		}
		if err := os.Remove(p); err != nil {
			fail(p, "delete", err)
			return false
		}
		res.Files++
		prog.DoneItems++
		return true
	}

	for _, p := range plan.Paths {
		if ctx.Err() != nil {
			res.Canceled = true
			break
		}
		if err := CheckDeletable(p); err != nil {
			fail(p, "delete", err)
			continue
		}
		if res.UseTrash {
			prog.Current = p
			report(false)
			if err := trash.Trash(p); err != nil {
				fail(p, "move to trash", err)
			} else {
				res.Removed++
			}
			prog.DoneItems++
			continue
		}
		if removeTree(p) {
			res.Removed++
		}
	}
	prog.Current = ""
	report(true)
	return res
}

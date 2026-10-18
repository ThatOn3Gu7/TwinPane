package dircompare

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// Direction identifies the source side for a reviewed copy action.
type Direction uint8

const (
	LeftToRight Direction = iota + 1
	RightToLeft
)

func (d Direction) String() string {
	if d == RightToLeft {
		return "right → left"
	}
	return "left → right"
}

// Choice is one user-selected copy proposal from a comparison row.
type Choice struct {
	Rel       string
	Direction Direction
}

// ActionKind is one step in an immutable reviewed plan.
type ActionKind uint8

const (
	ActionCreateDirectory ActionKind = iota + 1
	ActionCopyEntry
)

func (k ActionKind) String() string {
	if k == ActionCreateDirectory {
		return "create directory"
	}
	return "copy entry"
}

// Action is a reviewed operation with the exact source and destination state
// observed by Compare. Destination is nil only when the target entry was
// absent at review time. Fields are read-only by convention; Plan.Actions
// returns detached copies.
type Action struct {
	Kind        ActionKind
	Rel         string
	Direction   Direction
	Source      Snapshot
	Destination *Snapshot
}

// PlanTotals is the concise summary shown before execution.
type PlanTotals struct {
	Actions     int
	Files       int
	Directories int
	Overwrites  int
	Bytes       int64
}

// Plan is a reviewed set of path-level operations. Its state and root
// identities are private; callers receive detached action copies for display.
type Plan struct {
	leftRoot, rightRoot string
	leftInfo, rightInfo fs.FileInfo
	actions             []Action
	totals              PlanTotals
}

// Actions returns detached action values so a UI cannot mutate the plan that
// will later be executed.
func (p Plan) Actions() []Action {
	out := make([]Action, len(p.actions))
	for i, action := range p.actions {
		out[i] = cloneAction(action)
	}
	return out
}

// Roots returns the canonical local roots captured by Compare.
func (p Plan) Roots() (left, right string) { return p.leftRoot, p.rightRoot }

// Totals returns the operation counts displayed in plan review.
func (p Plan) Totals() PlanTotals { return p.totals }

// Empty reports whether the plan contains operations.
func (p Plan) Empty() bool { return len(p.actions) == 0 }

// ErrChangedSinceReview identifies a path or root that no longer matches its
// reviewed snapshot. Execution stops at the first such change.
var ErrChangedSinceReview = errors.New("filesystem changed since plan review")

// CanCopy reports whether a comparison row can safely propose a copy in the
// chosen direction. Type conflicts and special files require manual handling.
func CanCopy(result Result, rel string, direction Direction) error {
	if !result.Complete {
		return errors.New("comparison is incomplete; rescan before planning")
	}
	if !result.CanSync {
		if result.SyncNote != "" {
			return errors.New(result.SyncNote)
		}
		return errors.New("selected directories overlap")
	}
	if direction != LeftToRight && direction != RightToLeft {
		return errors.New("invalid copy direction")
	}
	if err := validateRel(rel); err != nil {
		return err
	}
	entry, ok := findEntry(result, rel)
	if !ok {
		return fmt.Errorf("%s is not in this comparison", rel)
	}
	return canCopyEntry(entry, direction)
}

func canCopyEntry(entry Entry, direction Direction) error {
	if direction != LeftToRight && direction != RightToLeft {
		return errors.New("invalid copy direction")
	}
	if err := validateRel(entry.Rel); err != nil {
		return err
	}
	if entry.Status == StatusError {
		return fmt.Errorf("%s has a read error and cannot be planned", entry.Rel)
	}
	if entry.Status == StatusTypeConflict {
		return fmt.Errorf("%s has different types on the two sides; resolve it manually", entry.Rel)
	}
	source, destination := sides(entry, direction)
	if source.Snapshot == nil {
		return fmt.Errorf("%s is missing from the selected source side", entry.Rel)
	}
	if source.Snapshot.Kind == KindSpecial || source.Snapshot.Kind == KindUnknown {
		return fmt.Errorf("%s is an unsupported %s", entry.Rel, source.Snapshot.Kind)
	}
	if source.Snapshot.Kind == KindDirectory {
		if destination.Snapshot != nil {
			return fmt.Errorf("%s is already a directory on the destination side; choose individual changed entries", entry.Rel)
		}
		return nil
	}
	if destination.Snapshot != nil && destination.Snapshot.Kind != source.Snapshot.Kind {
		return fmt.Errorf("%s would replace a different entry type; resolve it manually", entry.Rel)
	}
	return nil
}

// BuildPlan creates a deterministic plan from the current comparison and
// explicit user choices. Selecting a one-sided directory expands it into
// individually reviewed directory-creation and leaf-copy actions.
func BuildPlan(result Result, choices []Choice) (Plan, error) {
	var plan Plan
	if !result.Complete {
		return plan, errors.New("comparison is incomplete; rescan before planning")
	}
	if !result.CanSync {
		if result.SyncNote != "" {
			return plan, errors.New(result.SyncNote)
		}
		return plan, errors.New("selected directories overlap")
	}
	if len(choices) == 0 {
		return plan, errors.New("choose at least one copy action")
	}
	plan.leftRoot, plan.rightRoot = result.LeftRoot, result.RightRoot
	plan.leftInfo, plan.rightInfo = result.leftRootInfo, result.rightRootInfo
	if plan.leftInfo == nil || plan.rightInfo == nil {
		return Plan{}, errors.New("comparison is missing root identities; run it again")
	}

	entries := make(map[string]Entry, len(result.Entries))
	for _, entry := range result.Entries {
		entries[entry.Rel] = entry
	}
	selected := make(map[string]Direction, len(choices))
	for _, choice := range choices {
		if err := validateRel(choice.Rel); err != nil {
			return Plan{}, err
		}
		entry, exists := entries[choice.Rel]
		if !exists {
			return Plan{}, fmt.Errorf("%s is not in this comparison", choice.Rel)
		}
		if err := canCopyEntry(entry, choice.Direction); err != nil {
			return Plan{}, err
		}
		if old, exists := selected[choice.Rel]; exists && old != choice.Direction {
			return Plan{}, fmt.Errorf("%s is selected in both directions", choice.Rel)
		}
		selected[choice.Rel] = choice.Direction
	}

	byPath := make(map[string]Action, len(choices))
	add := func(action Action) error {
		if old, exists := byPath[action.Rel]; exists {
			if old.Direction != action.Direction {
				return fmt.Errorf("%s is selected in both directions", action.Rel)
			}
			if old.Kind == action.Kind {
				return nil
			}
			return fmt.Errorf("conflicting plan actions for %s", action.Rel)
		}
		byPath[action.Rel] = cloneAction(action)
		return nil
	}

	var ensureParentDirs func(string, Direction) error
	ensureParentDirs = func(rel string, direction Direction) error {
		parents := parentPaths(rel)
		for _, parent := range parents {
			entry, ok := entries[parent]
			if !ok {
				return fmt.Errorf("parent directory %s is absent from the comparison", parent)
			}
			source, destination := sides(entry, direction)
			if source.Snapshot == nil || source.Snapshot.Kind != KindDirectory {
				return fmt.Errorf("source parent %s is not a real directory", parent)
			}
			if destination.Snapshot != nil {
				if destination.Snapshot.Kind != KindDirectory {
					return fmt.Errorf("destination parent %s is not a real directory", parent)
				}
				continue
			}
			if err := add(Action{Kind: ActionCreateDirectory, Rel: parent, Direction: direction, Source: *source.Snapshot}); err != nil {
				return err
			}
		}
		return nil
	}

	var addSelected func(Entry, Direction, bool) error
	addSelected = func(entry Entry, direction Direction, expand bool) error {
		if err := canCopyEntry(entry, direction); err != nil {
			return err
		}
		source, destination := sides(entry, direction)
		if source.Snapshot.Kind == KindDirectory {
			if err := ensureParentDirs(entry.Rel, direction); err != nil {
				return err
			}
			if err := add(Action{Kind: ActionCreateDirectory, Rel: entry.Rel, Direction: direction, Source: *source.Snapshot}); err != nil {
				return err
			}
			if expand {
				prefix := entry.Rel + "/"
				start := sort.Search(len(result.Entries), func(i int) bool { return result.Entries[i].Rel >= prefix })
				for i := start; i < len(result.Entries); i++ {
					child := result.Entries[i]
					if !strings.HasPrefix(child.Rel, prefix) {
						break
					}
					childSource, _ := sides(child, direction)
					if childSource.Snapshot == nil {
						return fmt.Errorf("%s changed during comparison", child.Rel)
					}
					if err := addSelected(child, direction, false); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := ensureParentDirs(entry.Rel, direction); err != nil {
			return err
		}
		var destinationSnapshot *Snapshot
		if destination.Snapshot != nil {
			copyOfDestination := *destination.Snapshot
			destinationSnapshot = &copyOfDestination
		}
		return add(Action{Kind: ActionCopyEntry, Rel: entry.Rel, Direction: direction, Source: *source.Snapshot, Destination: destinationSnapshot})
	}

	// Walk choices in relative-path order so the resulting plan is stable
	// even when callers supply selections in arbitrary UI order.
	choicePaths := make([]string, 0, len(selected))
	for rel := range selected {
		choicePaths = append(choicePaths, rel)
	}
	sort.Strings(choicePaths)
	for _, rel := range choicePaths {
		direction := selected[rel]
		coveredByDirectory := false
		for _, parent := range parentPaths(rel) {
			if selected[parent] != direction {
				continue
			}
			parentSource, _ := sides(entries[parent], direction)
			if parentSource.Snapshot != nil && parentSource.Snapshot.Kind == KindDirectory {
				coveredByDirectory = true
				break
			}
		}
		if coveredByDirectory {
			continue
		}
		entry := entries[rel]
		expand := false
		if source, _ := sides(entry, direction); source.Snapshot.Kind == KindDirectory {
			expand = true
		}
		if err := addSelected(entry, direction, expand); err != nil {
			return Plan{}, err
		}
	}

	for _, action := range byPath {
		plan.actions = append(plan.actions, cloneAction(action))
	}
	sort.Slice(plan.actions, func(i, j int) bool {
		a, b := plan.actions[i], plan.actions[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		ad, bd := strings.Count(a.Rel, "/"), strings.Count(b.Rel, "/")
		if ad != bd {
			return ad < bd
		}
		return a.Rel < b.Rel
	})
	if len(plan.actions) == 0 {
		return Plan{}, errors.New("the selected entries do not require any copy actions")
	}
	plan.totals.Actions = len(plan.actions)
	for _, action := range plan.actions {
		switch action.Kind {
		case ActionCreateDirectory:
			plan.totals.Directories++
		case ActionCopyEntry:
			plan.totals.Files++
			if action.Source.Kind == KindRegular {
				plan.totals.Bytes += action.Source.Size
			}
			if action.Destination != nil {
				plan.totals.Overwrites++
			}
		}
	}
	return plan, nil
}

func sides(entry Entry, direction Direction) (source, destination Side) {
	if direction == LeftToRight {
		return entry.Left, entry.Right
	}
	return entry.Right, entry.Left
}

func findEntry(result Result, rel string) (Entry, bool) {
	for _, entry := range result.Entries {
		if entry.Rel == rel {
			return entry, true
		}
	}
	return Entry{}, false
}

func parentPaths(rel string) []string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return nil
	}
	out := make([]string, 0, len(parts)-1)
	for i := 1; i < len(parts); i++ {
		out = append(out, strings.Join(parts[:i], "/"))
	}
	return out
}

func cloneAction(action Action) Action {
	if action.Destination != nil {
		destination := *action.Destination
		action.Destination = &destination
	}
	return action
}

// ExecutionHooks connect the plan executor to background progress and
// conflict dialogs. Overwrites are never implicit: a conflict callback must
// return ops.Overwrite for the reviewed destination to be replaced.
type ExecutionHooks struct {
	Operation ops.Hooks
	Progress  func(Action, ops.Progress)
}

// Execution reports exactly how much of a reviewed plan ran. Execution stops
// at the first changed path, conflict skip, cancellation, or failed operation;
// remaining actions are never reinterpreted or attempted.
type Execution struct {
	Completed   []string
	CreatedDirs int
	CopiedFiles int
	Bytes       int64
	Canceled    bool
	Changed     error
	Errors      []error
}

// Execute runs the exact operations captured in plan. It validates both roots
// and each source/destination immediately before an action; ops.Run validates
// again immediately before the atomic file placement. A changed snapshot
// aborts the plan without substituting a different action.
func Execute(ctx context.Context, plan Plan, hooks ExecutionHooks) Execution {
	result := Execution{}
	if len(plan.actions) == 0 {
		result.Errors = append(result.Errors, errors.New("empty synchronization plan"))
		return result
	}
	created := make([]createdDirectory, 0)
	for _, action := range plan.actions {
		if err := ctx.Err(); err != nil {
			result.Canceled = true
			break
		}
		if err := validatePlanAction(ctx, plan, action); err != nil {
			if ctx.Err() != nil {
				result.Canceled = true
			} else if errors.Is(err, ErrChangedSinceReview) {
				result.Changed = err
			} else {
				result.Errors = append(result.Errors, err)
			}
			break
		}
		if action.Kind == ActionCreateDirectory {
			path, err := pathFor(actionRoot(plan, action.Direction, false), action.Rel)
			if err != nil {
				result.Errors = append(result.Errors, err)
				break
			}
			if err := ctx.Err(); err != nil {
				result.Canceled = true
				break
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				if errors.Is(err, fs.ErrExist) {
					result.Changed = fmt.Errorf("%w: destination %s appeared after review", ErrChangedSinceReview, action.Rel)
				} else {
					result.Errors = append(result.Errors, fmt.Errorf("create directory %s: %w", action.Rel, err))
				}
				break
			}
			info, err := lstatPath(path)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("inspect created directory %s: %w", action.Rel, err))
				break
			}
			created = append(created, createdDirectory{action: cloneAction(action), path: path, info: info})
			result.CreatedDirs++
			result.Completed = append(result.Completed, action.Rel)
			continue
		}

		sourceRoot := actionRoot(plan, action.Direction, true)
		destinationRoot := actionRoot(plan, action.Direction, false)
		sourcePath, err := pathFor(sourceRoot, action.Rel)
		if err != nil {
			result.Errors = append(result.Errors, err)
			break
		}
		destinationPath, err := pathFor(destinationRoot, action.Rel)
		if err != nil {
			result.Errors = append(result.Errors, err)
			break
		}
		request := ops.Request{
			Kind:    ops.Copy,
			Sources: []string{sourcePath},
			DestDir: filepath.Dir(destinationPath),
			BeforeTransfer: func(_, _ string) error {
				if err := validatePlanAction(ctx, plan, action); err != nil {
					return fmt.Errorf("%w: %v", ErrChangedSinceReview, err)
				}
				return nil
			},
		}
		operationHooks := hooks.Operation
		oldConflict := operationHooks.Conflict
		operationHooks.Conflict = func(conflict ops.Conflict) ops.Decision {
			if oldConflict != nil {
				return oldConflict(conflict)
			}
			return ops.Decision{Action: ops.Cancel}
		}
		if hooks.Progress != nil {
			oldProgress := operationHooks.Progress
			operationHooks.Progress = func(progress ops.Progress) {
				if oldProgress != nil {
					oldProgress(progress)
				}
				hooks.Progress(action, progress)
			}
		}
		operation := ops.Run(ctx, request, operationHooks)
		if operation.Canceled {
			result.Canceled = true
			break
		}
		if operation.Fatal != nil {
			if errors.Is(operation.Fatal, ErrChangedSinceReview) {
				result.Changed = operation.Fatal
			} else {
				result.Errors = append(result.Errors, operation.Fatal)
			}
			break
		}
		if len(operation.Errors) > 0 {
			result.Errors = append(result.Errors, operation.Errors[0])
			break
		}
		if operation.Skipped > 0 || operation.Files != 1 {
			result.Errors = append(result.Errors, fmt.Errorf("copy of %s was not completed", action.Rel))
			break
		}
		result.CopiedFiles += operation.Files
		result.Bytes += operation.Bytes
		result.Completed = append(result.Completed, action.Rel)
	}

	if len(result.Errors) == 0 && result.Changed == nil && !result.Canceled {
		for i := len(created) - 1; i >= 0; i-- {
			if err := finalizeDirectory(ctx, plan, created[i]); err != nil {
				if errors.Is(err, context.Canceled) {
					result.Canceled = true
				} else if errors.Is(err, ErrChangedSinceReview) {
					result.Changed = err
				} else {
					result.Errors = append(result.Errors, err)
				}
				break
			}
		}
	}
	return result
}

type createdDirectory struct {
	action Action
	path   string
	info   fs.FileInfo
}

func finalizeDirectory(ctx context.Context, plan Plan, created createdDirectory) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePlanRoots(plan); err != nil {
		return fmt.Errorf("%w: %v", ErrChangedSinceReview, err)
	}
	sourceRoot := actionRoot(plan, created.action.Direction, true)
	if err := ValidateSnapshotContext(ctx, sourceRoot, created.action.Rel, created.action.Source); err != nil {
		return fmt.Errorf("%w: %v", ErrChangedSinceReview, err)
	}
	current, err := lstatPath(created.path)
	if err != nil || !current.IsDir() || current.Mode()&fs.ModeSymlink != 0 || !os.SameFile(current, created.info) {
		if err == nil {
			err = errors.New("created directory was replaced")
		}
		return fmt.Errorf("%w: %v", ErrChangedSinceReview, err)
	}
	if err := os.Chmod(created.path, created.action.Source.Mode.Perm()); err != nil {
		return fmt.Errorf("set directory permissions %s: %w", created.action.Rel, err)
	}
	if err := os.Chtimes(created.path, created.action.Source.ModTime, created.action.Source.ModTime); err != nil {
		return fmt.Errorf("set directory time %s: %w", created.action.Rel, err)
	}
	return nil
}

func validatePlanAction(ctx context.Context, plan Plan, action Action) error {
	if err := validatePlanRoots(plan); err != nil {
		return err
	}
	sourceRoot := actionRoot(plan, action.Direction, true)
	destinationRoot := actionRoot(plan, action.Direction, false)
	if err := ValidateSnapshotContext(ctx, sourceRoot, action.Rel, action.Source); err != nil {
		return err
	}
	if action.Destination == nil {
		if err := validateAncestors(destinationRoot, action.Rel); err != nil {
			if errors.Is(err, fs.ErrNotExist) || strings.Contains(err.Error(), "not a real directory") {
				return fmt.Errorf("%w: destination parent for %s changed: %v", ErrChangedSinceReview, action.Rel, err)
			}
			return err
		}
		destinationPath, err := pathFor(destinationRoot, action.Rel)
		if err != nil {
			return err
		}
		if _, err := lstatPath(destinationPath); err == nil {
			return fmt.Errorf("%w: destination %s appeared after review", ErrChangedSinceReview, action.Rel)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	} else if err := ValidateSnapshotContext(ctx, destinationRoot, action.Rel, *action.Destination); err != nil {
		return err
	}
	return nil
}

func validatePlanRoots(plan Plan) error {
	if err := validateRootIdentity(plan.leftRoot, plan.leftInfo); err != nil {
		return err
	}
	if err := validateRootIdentity(plan.rightRoot, plan.rightInfo); err != nil {
		return err
	}
	return nil
}

func actionRoot(plan Plan, direction Direction, source bool) string {
	if direction == LeftToRight {
		if source {
			return plan.leftRoot
		}
		return plan.rightRoot
	}
	if source {
		return plan.rightRoot
	}
	return plan.leftRoot
}

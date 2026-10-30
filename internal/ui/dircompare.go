package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ThatOn3Gu7/TwinPane/internal/dircompare"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
)

type comparisonView struct {
	id                    int
	leftPath              string
	rightPath             string
	cancel                context.CancelFunc
	loading               bool
	hashing               bool
	cancelling            bool
	err                   error
	result                *dircompare.Result
	includeGitDirectories bool
	differencesOnly       bool

	cursor, offset int
	choices        map[string]dircompare.Direction
	reviewing      bool
	plan           dircompare.Plan
	planCursor     int
	planOffset     int
}

var comparisonHelp = []Binding{
	{Help: "Workflow", Desc: "D scan; l/r propose; a add one-sided; p review; Enter confirms; overwrites still ask"},
	{Help: "f", Desc: "show differences and proposed actions only; hide matching rows"},
	{Help: "g", Desc: "include/exclude exact .git directories; .gitignore/.github remain; changing scope clears proposals"},
	{Help: "h", Desc: "SHA-256 checks regular files whose size/time match; metadata alone does not prove equal bytes"},
	{Help: "LEFT/RIGHT ONLY", Desc: "entry exists on just one side; choose the copy direction"},
	{Help: "SAME META", Desc: "same type/mode/time; size for files, target for links, directory size ignored; file contents may differ"},
	{Help: "META DIFF", Desc: "one or more compared metadata fields differ"},
	{Help: "CONTENT DIFF", Desc: "SHA-256 differs; size and modification time matched"},
	{Help: "IDENTICAL", Desc: "metadata and SHA-256 match"},
	{Help: "SAME CONTENT", Desc: "SHA-256 matches but metadata differs"},
	{Help: "TYPE CONFLICT", Desc: "different object types; resolve manually"},
	{Help: "READ ERROR", Desc: "entry unreadable or changed; rescan before planning"},
}

// visibleComparisonEntries applies the clutter filter without hiding any
// outstanding proposal. Metadata matches (and proven identical files after a
// hash scan) are the only rows removed by differences-only mode; conflicts and
// errors remain visible for review.
func visibleComparisonEntries(c *comparisonView) []dircompare.Entry {
	if c == nil || c.result == nil {
		return nil
	}
	if !c.differencesOnly {
		return c.result.Entries
	}
	out := make([]dircompare.Entry, 0, len(c.result.Entries))
	for _, entry := range c.result.Entries {
		if !comparisonEntryMatches(entry) {
			out = append(out, entry)
			continue
		}
		if _, proposed := c.choices[entry.Rel]; proposed {
			out = append(out, entry)
		}
	}
	return out
}

func comparisonEntryMatches(entry dircompare.Entry) bool {
	return entry.Status == dircompare.StatusMetadataEqual || entry.Status == dircompare.StatusIdentical
}

func (a *App) toggleComparisonDifferences() {
	c := a.comparison
	if c == nil || c.result == nil {
		return
	}
	before := visibleComparisonEntries(c)
	oldCursor := c.cursor
	currentRel := ""
	if oldCursor >= 0 && oldCursor < len(before) {
		currentRel = before[oldCursor].Rel
	}
	c.differencesOnly = !c.differencesOnly
	after := visibleComparisonEntries(c)
	c.cursor = min(oldCursor, max(0, len(after)-1))
	for i, entry := range after {
		if entry.Rel == currentRel {
			c.cursor = i
			break
		}
	}
	c.offset = 0
	a.keepComparisonCursorVisible()
	if c.differencesOnly {
		a.setStatusLevel(fmt.Sprintf("Differences-only filter: %d rows; proposed matches stay visible", len(after)), lvlInfo)
	} else {
		a.setStatusLevel(fmt.Sprintf("Showing all %d comparison rows", len(after)), lvlInfo)
	}
}

type compareDoneMsg struct {
	id     int
	result dircompare.Result
	err    error
}

type syncDoneMsg struct {
	id     int
	result dircompare.Execution
	plan   dircompare.Plan
}

// compareEligibility is deliberately panel-based: archives and cloud sources
// may expose paths that resemble local paths, but this workflow only supports
// the two local directories currently visible in the panels.
func (a *App) compareEligibility() string {
	for i, p := range a.panels {
		side := "left"
		if i == 1 {
			side = "right"
		}
		switch {
		case p.InArchive():
			return "Directory comparison is local-only; leave the archive in the " + side + " panel first"
		case p.InRemote():
			return "Directory comparison is local-only; leave the cloud source in the " + side + " panel first"
		case p.Loading():
			return "Wait for the " + side + " panel to finish loading before comparing"
		case p.Err() != nil:
			return "The " + side + " panel has a listing error; refresh it before comparing"
		case p.Path() == "":
			return "The " + side + " panel has no local directory to compare"
		}
	}
	return ""
}

func (a *App) canComparePanels() bool { return a.compareEligibility() == "" && !a.busy() }

func (a *App) beginDirectoryCompare() tea.Cmd {
	if reason := a.compareEligibility(); reason != "" {
		a.setStatusLevel(reason, lvlWarn)
		return nil
	}
	if a.busy() {
		a.setStatusLevel("Wait for active file operations to finish before comparing directories", lvlWarn)
		return nil
	}
	left, right := a.panels[0].Path(), a.panels[1].Path()
	a.comparison = &comparisonView{leftPath: left, rightPath: right, choices: map[string]dircompare.Direction{}}
	a.tasks.showing = false
	a.showHelp = false
	a.setStatusLevel("Comparing local directories…", lvlInfo)
	return a.startComparisonScan(false)
}

func (a *App) startComparisonScan(hash bool) tea.Cmd {
	c := a.comparison
	if c == nil {
		return nil
	}
	if c.cancel != nil {
		c.cancel()
	}
	a.comparisonSeq++
	id := a.comparisonSeq
	ctx, cancel := context.WithCancel(context.Background())
	c.id, c.cancel = id, cancel
	c.loading, c.hashing, c.cancelling = true, hash, false
	c.err = nil
	if hash {
		a.setStatusLevel("Checking file contents with SHA-256…", lvlInfo)
	} else {
		a.setStatusLevel("Comparing local directories…", lvlInfo)
	}
	left, right := c.leftPath, c.rightPath
	includeGitDirectories := c.includeGitDirectories
	return func() tea.Msg {
		result, err := dircompare.Compare(ctx, left, right, dircompare.Options{
			HashMatchingFiles: hash, IncludeGitDirectories: includeGitDirectories,
		})
		return compareDoneMsg{id: id, result: result, err: err}
	}
}

func (a *App) comparisonDone(msg compareDoneMsg) {
	c := a.comparison
	if c == nil || c.id != msg.id {
		return
	}
	c.loading, c.hashing, c.cancelling = false, false, false
	c.cancel = nil
	if msg.err != nil {
		c.err = msg.err
		if errors.Is(msg.err, context.Canceled) {
			c.err = nil
			a.setStatusLevel("Content comparison cancelled; the metadata comparison is unchanged", lvlWarn)
		} else {
			a.setStatusLevel("Cannot compare directories: "+errText(msg.err), lvlErr)
		}
		return
	}
	result := msg.result
	c.result = &result
	c.err = nil
	entries := visibleComparisonEntries(c)
	if c.cursor >= len(entries) {
		c.cursor = max(0, len(entries)-1)
	}
	a.keepComparisonCursorVisible()
	if !result.Complete {
		a.setStatusLevel("Comparison is incomplete; synchronization planning is disabled", lvlWarn)
	} else if !result.CanSync {
		a.setStatusLevel(result.SyncNote, lvlWarn)
	} else if result.ContentChecked {
		a.setStatusLevel("Content comparison complete", lvlOK)
	} else {
		a.setStatusLevel("Metadata comparison complete; press h to hash matching files", lvlOK)
	}
}

func (a *App) comparisonKey(msg tea.KeyMsg) tea.Cmd {
	c := a.comparison
	if c == nil {
		return nil
	}
	s := msg.String()
	if c.loading {
		if s == "esc" || s == "ctrl+c" {
			if c.cancel != nil {
				c.cancel()
			}
			if c.result == nil || !c.hashing {
				a.comparison = nil
				a.setStatusLevel("Directory comparison cancelled", lvlWarn)
			} else {
				c.cancelling = true
				a.setStatusLevel("Stopping content comparison…", lvlWarn)
			}
		}
		return nil
	}
	if c.reviewing {
		return a.comparisonPlanKey(msg)
	}
	if s == "esc" || s == "ctrl+c" || s == "q" {
		a.comparison = nil
		a.setStatus("", false)
		return nil
	}
	if c.err != nil {
		switch s {
		case "r":
			return a.startComparisonScan(false)
		case "h":
			return a.startComparisonScan(true)
		default:
			return nil
		}
	}
	if c.result == nil {
		if s == "r" {
			return a.startComparisonScan(false)
		}
		return nil
	}
	switch s {
	case "?":
		a.openHelp()
		return nil
	case "f":
		a.toggleComparisonDifferences()
		return nil
	case "g":
		hash := c.result.ContentChecked
		c.includeGitDirectories = !c.includeGitDirectories
		c.choices = map[string]dircompare.Direction{}
		c.cursor, c.offset = 0, 0
		c.result = nil // Never leave old-scope rows visible if this scan is cancelled.
		return a.startComparisonScan(hash)
	}
	entries := visibleComparisonEntries(c)
	if len(entries) > 0 {
		switch {
		case a.keys.Up.Matches(msg):
			c.cursor = max(0, c.cursor-1)
		case a.keys.Down.Matches(msg):
			c.cursor = min(len(entries)-1, c.cursor+1)
		case a.keys.PageUp.Matches(msg):
			c.cursor = max(0, c.cursor-max(1, a.comparisonListRows(a.height-3)))
		case a.keys.PageDown.Matches(msg):
			c.cursor = min(len(entries)-1, c.cursor+max(1, a.comparisonListRows(a.height-3)))
		case a.keys.Top.Matches(msg):
			c.cursor = 0
		case a.keys.Bottom.Matches(msg):
			c.cursor = len(entries) - 1
		case a.keys.Select.Matches(msg):
			a.toggleComparisonChoice(entries[c.cursor])
		case s == "l":
			a.proposeComparisonCopy(entries[c.cursor], dircompare.LeftToRight)
		case s == "r":
			a.proposeComparisonCopy(entries[c.cursor], dircompare.RightToLeft)
		}
	}
	switch s {
	case "a":
		a.proposeOneSidedEntries()
	case "u":
		c.choices = map[string]dircompare.Direction{}
		a.setStatusLevel("Cleared proposed copy actions", lvlInfo)
	case "h":
		return a.startComparisonScan(true)
	case "p":
		a.reviewComparisonPlan()
	}
	a.keepComparisonCursorVisible()
	return nil
}

func (a *App) toggleComparisonChoice(entry dircompare.Entry) {
	c := a.comparison
	if _, exists := c.choices[entry.Rel]; exists {
		delete(c.choices, entry.Rel)
		a.setStatusLevel("Removed proposal for "+fsys.SanitizeName(entry.Rel), lvlInfo)
		return
	}
	switch entry.Status {
	case dircompare.StatusLeftOnly:
		a.proposeComparisonCopy(entry, dircompare.LeftToRight)
	case dircompare.StatusRightOnly:
		a.proposeComparisonCopy(entry, dircompare.RightToLeft)
	default:
		a.setStatusLevel("Choose l (left → right) or r (right → left) for this entry", lvlWarn)
	}
}

func (a *App) proposeComparisonCopy(entry dircompare.Entry, direction dircompare.Direction) {
	c := a.comparison
	if c == nil || c.result == nil {
		return
	}
	if err := dircompare.CanCopy(*c.result, entry.Rel, direction); err != nil {
		a.setStatusLevel(errText(err), lvlWarn)
		return
	}
	if old, exists := c.choices[entry.Rel]; exists && old == direction {
		delete(c.choices, entry.Rel)
		a.setStatusLevel("Removed proposal for "+fsys.SanitizeName(entry.Rel), lvlInfo)
		return
	}
	c.choices[entry.Rel] = direction
	a.setStatusLevel(fmt.Sprintf("Proposed copy %s: %s", direction, fsys.SanitizeName(entry.Rel)), lvlOK)
}

func (a *App) proposeOneSidedEntries() {
	c := a.comparison
	if c == nil || c.result == nil {
		return
	}
	if !c.result.Complete || !c.result.CanSync {
		a.setStatusLevel("A complete comparison of non-overlapping local directories is required before planning", lvlWarn)
		return
	}
	added := 0
	for _, entry := range c.result.Entries {
		var direction dircompare.Direction
		var source *dircompare.Snapshot
		switch entry.Status {
		case dircompare.StatusLeftOnly:
			direction, source = dircompare.LeftToRight, entry.Left.Snapshot
		case dircompare.StatusRightOnly:
			direction, source = dircompare.RightToLeft, entry.Right.Snapshot
		default:
			continue
		}
		if source != nil && (source.Kind == dircompare.KindDirectory || source.Kind == dircompare.KindRegular || source.Kind == dircompare.KindSymlink) {
			c.choices[entry.Rel] = direction
			added++
		}
	}
	a.setStatusLevel(fmt.Sprintf("Proposed %d one-sided entries; review all actions before execution", added), lvlInfo)
}

func (a *App) reviewComparisonPlan() {
	c := a.comparison
	if c == nil || c.result == nil {
		return
	}
	choices := make([]dircompare.Choice, 0, len(c.choices))
	for rel, direction := range c.choices {
		choices = append(choices, dircompare.Choice{Rel: rel, Direction: direction})
	}
	plan, err := dircompare.BuildPlan(*c.result, choices)
	if err != nil {
		a.setStatusLevel(errText(err), lvlWarn)
		return
	}
	c.plan, c.reviewing = plan, true
	c.planCursor, c.planOffset = 0, 0
	a.setStatusLevel("Review the exact actions and overwrite count before execution", lvlInfo)
}

func (a *App) comparisonPlanKey(msg tea.KeyMsg) tea.Cmd {
	c := a.comparison
	if c == nil {
		return nil
	}
	s := msg.String()
	if s == "esc" || s == "backspace" {
		c.reviewing = false
		c.plan = dircompare.Plan{}
		return nil
	}
	actions := c.plan.Actions()
	if len(actions) == 0 {
		c.reviewing = false
		return nil
	}
	switch {
	case a.keys.Up.Matches(msg):
		c.planCursor = max(0, c.planCursor-1)
	case a.keys.Down.Matches(msg):
		c.planCursor = min(len(actions)-1, c.planCursor+1)
	case a.keys.PageUp.Matches(msg):
		c.planCursor = max(0, c.planCursor-max(1, a.comparisonPlanRows(a.height-3)))
	case a.keys.PageDown.Matches(msg):
		c.planCursor = min(len(actions)-1, c.planCursor+max(1, a.comparisonPlanRows(a.height-3)))
	case s == "enter" || s == "e":
		return a.confirmComparisonPlan()
	}
	a.keepPlanCursorVisible()
	return nil
}

func (a *App) confirmComparisonPlan() tea.Cmd {
	c := a.comparison
	if c == nil || c.plan.Empty() {
		return nil
	}
	plan := c.plan
	totals := plan.Totals()
	label := "Execute reviewed plan (Enter)"
	if totals.Overwrites > 0 {
		label = fmt.Sprintf("Approve %d overwrite(s) (Enter)", totals.Overwrites)
	}
	d := &dialog{kind: dlgSyncConfirm, title: "Execute this reviewed plan?", danger: totals.Overwrites > 0, width: 60}
	left, right := plan.Roots()
	d.body = []dline{
		text(fmt.Sprintf("%d actions · %d files/links · %d directories", totals.Actions, totals.Files, totals.Directories)),
		text(fmt.Sprintf("%d existing destination(s) will be overwritten", totals.Overwrites)),
		text("Data to copy: " + fsys.HumanSize(totals.Bytes)),
		blank(), text("Left:"), path(left), text("Right:"), path(right), blank(),
		warn("Each action is revalidated against the reviewed state. A change or failure stops all remaining actions."),
	}
	cancel := func(a *App) tea.Cmd {
		a.dialog = nil
		a.setStatusLevel("Reviewed plan not executed", lvlWarn)
		return nil
	}
	d.buttons = []button{
		{label: label, danger: totals.Overwrites > 0, run: func(a *App) tea.Cmd {
			a.dialog = nil
			return a.executeComparisonPlan(plan)
		}},
		{label: "Cancel (Esc)", run: cancel},
	}
	d.onEsc = cancel
	d.footer = "Enter approve · Esc back to plan · ←/→ choose"
	a.dialog = d
	return nil
}

func (a *App) executeComparisonPlan(plan dircompare.Plan) tea.Cmd {
	if plan.Empty() {
		return nil
	}
	left, right := plan.Roots()
	var sources []string
	for _, action := range plan.Actions() {
		sourceRoot := left
		if action.Direction == dircompare.RightToLeft {
			sourceRoot = right
		}
		sources = append(sources, filepath.Join(sourceRoot, filepath.FromSlash(action.Rel)))
	}
	s, ctx := a.newSession("Synchronizing directories", -1, sources)
	s.title = "Synchronizing directories"
	s.to = "Reviewed local plan"
	a.historyTouch(s)
	s.kind = ops.Copy
	s.prog = ops.Progress{Phase: "validating", TotalItems: plan.Totals().Actions}
	a.comparison = nil
	a.tasks.showing = true
	a.setStatusLevel("Executing the reviewed directory plan…", lvlInfo)
	hooks := a.hooks(ctx, s)
	go func() {
		result := dircompare.Execute(ctx, plan, dircompare.ExecutionHooks{Operation: hooks})
		s.events <- syncDoneMsg{id: s.id, result: result, plan: plan}
	}()
	return a.startCmds(s)
}

func (a *App) finishSync(s *opSession, execution dircompare.Execution, plan dircompare.Plan) tea.Cmd {
	if s == nil || !s.active() {
		return nil
	}
	completed := len(execution.Completed)
	partial := completed > 0
	state := taskCompleted
	summary := fmt.Sprintf("Synchronization complete · %d action(s)", completed)
	level := lvlOK
	heading := "Sync completed"
	var details []string
	details = append(details, fmt.Sprintf("Completed %d of %d reviewed actions", completed, plan.Totals().Actions))
	switch {
	case execution.Changed != nil:
		state, level = taskFailed, lvlErr
		heading = operationHeading("Sync", true, false, partial)
		if partial {
			state = taskPartial
		}
		summary = "Synchronization stopped — a path changed since plan review"
		details = append(details, errText(execution.Changed))
	case execution.Canceled:
		level = lvlWarn
		heading = operationHeading("Sync", false, true, partial)
		if partial {
			state, summary = taskPartial, "Synchronization stopped after cancellation"
		} else {
			state, summary = taskCanceled, "Synchronization cancelled — no actions completed"
		}
	case len(execution.Errors) > 0:
		level = lvlErr
		heading = operationHeading("Sync", true, false, partial)
		state, summary = taskFailed, "Synchronization stopped after a failure"
		if partial {
			state, summary = taskPartial, "Synchronization partially completed before a failure"
		}
		details = append(details, errText(execution.Errors[0]))
	}
	if execution.CopiedFiles > 0 {
		details = append(details, fmt.Sprintf("Copied %d file/link(s), %s", execution.CopiedFiles, fsys.HumanSize(execution.Bytes)))
	}
	if execution.CreatedDirs > 0 {
		details = append(details, fmt.Sprintf("Created %d directory(ies)", execution.CreatedDirs))
	}
	s.hist.files, s.hist.dirs, s.hist.bytes = execution.CopiedFiles, execution.CreatedDirs, execution.Bytes
	s.hist.result = fmt.Sprintf("%d of %d reviewed actions completed", completed, plan.Totals().Actions)
	for _, err := range execution.Errors {
		s.hist.errors = append(s.hist.errors, errText(err))
	}
	if execution.Changed != nil {
		s.hist.errors = append(s.hist.errors, errText(execution.Changed))
	}
	a.finishTask(s, state, summary, details...)
	a.taskResult(s, level, heading, summary, detailsBody(details)...)
	a.setStatus("", false)

	left, right := plan.Roots()
	var cmds []tea.Cmd
	for _, p := range a.panels {
		real := canonicalPanelPath(p)
		if real == left || real == right {
			cmds = append(cmds, p.Refresh())
		}
	}
	return tea.Batch(cmds...)
}

func (a *App) keepComparisonCursorVisible() {
	c := a.comparison
	if c == nil || c.result == nil {
		return
	}
	entries := visibleComparisonEntries(c)
	if len(entries) == 0 {
		c.cursor, c.offset = 0, 0
		return
	}
	visible := max(1, a.comparisonListRows(a.height-3))
	if c.cursor < c.offset {
		c.offset = c.cursor
	}
	if c.cursor >= c.offset+visible {
		c.offset = c.cursor - visible + 1
	}
	maxOffset := max(0, len(entries)-visible)
	c.offset = max(0, min(c.offset, maxOffset))
}

func (a *App) keepPlanCursorVisible() {
	c := a.comparison
	if c == nil {
		return
	}
	visible := max(1, a.comparisonPlanRows(a.height-3))
	actions := c.plan.Actions()
	if c.planCursor < c.planOffset {
		c.planOffset = c.planCursor
	}
	if c.planCursor >= c.planOffset+visible {
		c.planOffset = c.planCursor - visible + 1
	}
	maxOffset := max(0, len(actions)-visible)
	c.planOffset = max(0, min(c.planOffset, maxOffset))
}

func comparisonTitle(c *comparisonView, width int) string {
	if c == nil {
		return "Directory comparison"
	}
	filter := "all"
	if c.differencesOnly {
		filter = "diff"
	}
	git := "off"
	if c.includeGitDirectories {
		git = "on"
	}
	switch {
	case width >= 60:
		if c.includeGitDirectories {
			return fmt.Sprintf("Directory comparison · f: %s · g: .git included", filter)
		}
		return fmt.Sprintf("Directory comparison · f: %s · g: .git excluded", filter)
	case width >= 40:
		return fmt.Sprintf("Directory compare · f:%s · g:.git %s", filter, git)
	default:
		return fmt.Sprintf("Compare D:scan f:%s g:%s", filter, git)
	}
}

func (a *App) comparisonListRows(height int) int {
	header := 6
	if height <= 6 {
		header = 3
	}
	return max(0, height-header)
}

func (a *App) comparisonPlanRows(height int) int {
	header := 4
	if height <= 5 {
		header = 3
	}
	return max(0, height-header)
}

func (a *App) comparisonViewRender(height int) string {
	if height <= 0 {
		return ""
	}
	c := a.comparison
	if c == nil {
		return strings.Repeat("\n", height-1)
	}
	if c.reviewing {
		return a.comparisonPlanRender(height)
	}
	width := a.width
	compact := height <= 6
	lines := []string{a.theme.HelpTitle.Render(fitWidth(comparisonTitle(c, width), width))}
	if compact {
		roots := "L " + fsys.SanitizeName(shortPath(c.leftPath)) + " ⇄ R " + fsys.SanitizeName(shortPath(c.rightPath))
		lines = append(lines, a.theme.DialogHint.Render(fitWidth(roots, width)))
	} else {
		lines = append(lines, a.theme.DialogHint.Render(fitWidth("Left: "+fsys.SanitizeName(shortPath(c.leftPath)), width)))
		lines = append(lines, a.theme.DialogHint.Render(fitWidth("Right: "+fsys.SanitizeName(shortPath(c.rightPath)), width)))
	}
	if c.loading {
		message := "Scanning without following symlinks…"
		if c.hashing {
			message = "Comparing matching file contents with SHA-256…"
		}
		if c.cancelling {
			message = "Stopping content comparison…"
		}
		lines = append(lines, a.theme.TitleText.Render(fitWidth(message, width)))
		if !compact {
			lines = append(lines, a.theme.DialogHint.Render(fitWidth("Esc cancels", width)))
		}
		return fixedBody(lines, height, width)
	}
	if c.err != nil {
		lines = append(lines, a.theme.StatusErr.Render(fitWidth("Comparison failed: "+errText(c.err), width)))
		return fixedBody(lines, height, width)
	}
	if c.result == nil {
		lines = append(lines, a.theme.Empty.Render(fitWidth("No comparison results", width)))
		return fixedBody(lines, height, width)
	}
	result := c.result
	entries := visibleComparisonEntries(c)
	t := result.Totals
	matchLabel := fmt.Sprintf("metadata match %d", t.MetadataEqual)
	if result.ContentChecked {
		matchLabel = fmt.Sprintf("identical %d · same bytes %d", t.Identical, t.SameContent)
	}
	entryLabel := fmt.Sprintf("%d entries", t.Entries)
	if c.differencesOnly {
		entryLabel = fmt.Sprintf("%d/%d shown", len(entries), t.Entries)
	}
	totalLine := fmt.Sprintf("%s · L-only %d · R-only %d · %s · changed %d · type %d · errors %d",
		entryLabel, t.LeftOnly, t.RightOnly, matchLabel, t.MetadataDifferent+t.ContentDifferent, t.TypeConflicts, t.Errors)
	lines = append(lines, a.theme.DialogText.Render(fitWidth(totalLine, width)))
	if !compact {
		switch {
		case !result.Complete:
			lines = append(lines, a.theme.StatusWarn.Render(fitWidth("Incomplete scan: actions disabled until a clean rescan", width)))
		case !result.CanSync:
			lines = append(lines, a.theme.StatusWarn.Render(fitWidth(result.SyncNote, width)))
		case result.ContentChecked:
			lines = append(lines, a.theme.DialogHint.Render(fitWidth(fmt.Sprintf("SHA-256 checked matching files · L %s · R %s", fsys.HumanSize(t.LeftBytes), fsys.HumanSize(t.RightBytes)), width)))
		default:
			lines = append(lines, a.theme.DialogHint.Render(fitWidth(fmt.Sprintf("SAME META is metadata, not bytes; h checks SHA-256 · L %s · R %s", fsys.HumanSize(t.LeftBytes), fsys.HumanSize(t.RightBytes)), width)))
		}
		workflow := "Workflow: D scan → l/r propose · a one-sided → p review → Enter approve"
		if width < 60 {
			workflow = "Flow: D scan → l/r copy → p review → Enter approve"
		}
		if width < 50 {
			workflow = "D scan→l/r copy→p plan→Enter"
		}
		lines = append(lines, a.theme.DialogHint.Render(fitWidth(workflow, width)))
	}
	if len(result.Entries) == 0 {
		lines = append(lines, a.theme.Empty.Render(fitWidth("No entries in either directory", width)))
		return fixedBody(lines, height, width)
	}
	if len(entries) == 0 {
		lines = append(lines, a.theme.Empty.Render(fitWidth("No differences or proposals; press f to show matches", width)))
		return fixedBody(lines, height, width)
	}
	rows := a.comparisonListRows(height)
	if rows == 0 {
		return fixedBody(lines, height, width)
	}
	start := min(c.offset, max(0, len(entries)-rows))
	end := min(len(entries), start+rows)
	for i := start; i < end; i++ {
		entry := entries[i]
		marker := "  "
		if i == c.cursor {
			marker = "> "
		}
		var row string
		if width < 60 {
			choice := "---"
			if direction, ok := c.choices[entry.Rel]; ok {
				if direction == dircompare.LeftToRight {
					choice = "L>R"
				} else {
					choice = "R>L"
				}
			}
			row = fmt.Sprintf("%s%-3s %-8s %-4s %s", marker, choice, compactComparisonStatus(entry.Status), comparisonEntryType(entry), entry.Rel)
		} else {
			choice := "      "
			if direction, ok := c.choices[entry.Rel]; ok {
				if direction == dircompare.LeftToRight {
					choice = "L→R   "
				} else {
					choice = "R→L   "
				}
			}
			row = fmt.Sprintf("%s%-6s %-15s %-4s %s", marker, choice, comparisonStatusLabel(entry.Status), comparisonEntryType(entry), entry.Rel)
		}
		style := comparisonStatusStyle(a, entry.Status)
		if i == c.cursor {
			style = a.theme.CursorActive
		}
		lines = append(lines, style.Render(fitWidth(fsys.SanitizeName(row), width)))
	}
	return fixedBody(lines, height, width)
}

func comparisonEntryType(entry dircompare.Entry) string {
	kind := func(snapshot *dircompare.Snapshot) string {
		if snapshot == nil {
			return ""
		}
		switch snapshot.Kind {
		case dircompare.KindDirectory:
			return "DIR"
		case dircompare.KindSymlink:
			return "LINK"
		case dircompare.KindRegular:
			return "FILE"
		case dircompare.KindSpecial:
			return "SPEC"
		default:
			return "?"
		}
	}
	left, right := kind(entry.Left.Snapshot), kind(entry.Right.Snapshot)
	if left != "" && right != "" && left != right {
		return left + "/" + right
	}
	if left != "" {
		return left
	}
	if right != "" {
		return right
	}
	return "?"
}

func compactComparisonStatus(status dircompare.Status) string {
	switch status {
	case dircompare.StatusLeftOnly:
		return "LEFT"
	case dircompare.StatusRightOnly:
		return "RIGHT"
	case dircompare.StatusMetadataEqual:
		return "SAME"
	case dircompare.StatusMetadataDifferent:
		return "META!"
	case dircompare.StatusContentDifferent:
		return "DIFF"
	case dircompare.StatusIdentical:
		return "IDENT"
	case dircompare.StatusSameContent:
		return "BYTES"
	case dircompare.StatusTypeConflict:
		return "TYPE!"
	case dircompare.StatusError:
		return "ERROR"
	default:
		return "?"
	}
}

func comparisonStatusLabel(status dircompare.Status) string {
	switch status {
	case dircompare.StatusLeftOnly:
		return "LEFT ONLY"
	case dircompare.StatusRightOnly:
		return "RIGHT ONLY"
	case dircompare.StatusMetadataEqual:
		return "SAME META"
	case dircompare.StatusMetadataDifferent:
		return "META DIFF"
	case dircompare.StatusContentDifferent:
		return "CONTENT DIFF"
	case dircompare.StatusIdentical:
		return "IDENTICAL"
	case dircompare.StatusSameContent:
		return "SAME CONTENT"
	case dircompare.StatusTypeConflict:
		return "TYPE CONFLICT"
	case dircompare.StatusError:
		return "READ ERROR"
	default:
		return "UNKNOWN"
	}
}

func comparisonStatusStyle(a *App, status dircompare.Status) lipgloss.Style {
	switch status {
	case dircompare.StatusLeftOnly, dircompare.StatusRightOnly:
		return a.theme.StatusInfo
	case dircompare.StatusMetadataDifferent, dircompare.StatusContentDifferent, dircompare.StatusSameContent, dircompare.StatusTypeConflict:
		return a.theme.StatusWarn
	case dircompare.StatusError:
		return a.theme.StatusErr
	case dircompare.StatusIdentical:
		return a.theme.StatusOK
	default:
		return a.theme.TitleText
	}
}

func (a *App) comparisonPlanRender(height int) string {
	if height <= 0 || a.comparison == nil {
		return ""
	}
	c := a.comparison
	t := c.plan.Totals()
	compact := height <= 5
	lines := []string{a.theme.HelpTitle.Render("Review sync plan")}
	lines = append(lines, a.theme.DialogText.Render(fitWidth(fmt.Sprintf("%d actions · %d files/links · %d dirs · %d overwrites",
		t.Actions, t.Files, t.Directories, t.Overwrites), a.width)))
	if !compact {
		lines = append(lines, a.theme.DialogHint.Render(fitWidth("L: "+fsys.SanitizeName(shortPath(c.leftPath))+"  ⇄  R: "+fsys.SanitizeName(shortPath(c.rightPath)), a.width)))
	}
	if t.Overwrites > 0 {
		lines = append(lines, a.theme.StatusWarn.Render(fitWidth("Existing files will be overwritten only after approval.", a.width)))
	} else {
		lines = append(lines, a.theme.DialogHint.Render(fitWidth("No existing destination files are overwritten.", a.width)))
	}
	actions := c.plan.Actions()
	rows := a.comparisonPlanRows(height)
	start := min(c.planOffset, max(0, len(actions)-rows))
	end := min(len(actions), start+rows)
	for i := start; i < end; i++ {
		action := actions[i]
		marker := "  "
		if i == c.planCursor {
			marker = "> "
		}
		direction := "L→R"
		if action.Direction == dircompare.RightToLeft {
			direction = "R→L"
		}
		label := "COPY"
		if action.Kind == dircompare.ActionCreateDirectory {
			label = "MKDIR"
		} else if action.Destination != nil {
			label = "OVERWRITE"
		}
		path := action.Rel
		if action.Kind == dircompare.ActionCreateDirectory {
			path += "/"
		}
		row := fmt.Sprintf("%s%-3s %-9s %s", marker, direction, label, path)
		style := a.theme.TitleText
		if action.Destination != nil {
			style = a.theme.StatusWarn
		}
		if i == c.planCursor {
			style = a.theme.CursorActive
		}
		lines = append(lines, style.Render(fitWidth(fsys.SanitizeName(row), a.width)))
	}
	return fixedBody(lines, height, a.width)
}

func (a *App) comparisonHints() []barItem {
	c := a.comparison
	if c == nil {
		return nil
	}
	if c.loading {
		return []barItem{a.bind("compare.close", "cancel")}
	}
	if c.reviewing {
		return []barItem{a.bind("comparePlan.confirm", "approve plan…"), item("↑↓", "review", ""), a.bind("comparePlan.back", "back")}
	}
	if c.err != nil && c.result != nil {
		return []barItem{a.bind("compare.retry", "retry scan"), a.bind("compare.hash", "retry content check"), a.bind("compare.help", "keys/statuses"), a.bind("compare.close", "close")}
	}
	if c.result == nil {
		return []barItem{a.bind("compare.retry", "retry comparison"), a.bind("compare.help", "keys/statuses"), a.bind("compare.close", "close")}
	}
	filterAction := "show differences"
	if c.differencesOnly {
		filterAction = "show all"
	}
	gitAction := "include .git"
	if c.includeGitDirectories {
		gitAction = "exclude .git"
	}
	if a.width < 40 {
		return []barItem{
			item("l/r", "copy", ""), a.bind("compare.plan", "plan"), a.bind("compare.help", "help"),
			a.bind("compare.auto", "one-sided"), a.bind("compare.differences", filterAction), a.bind("compare.git", gitAction),
			a.bind("compare.close", "close"),
		}
	}
	out := []barItem{
		item("l/r", "copy", ""), a.bind("compare.plan", "review plan"), a.bind("compare.auto", "one-sided"),
		a.bind("compare.differences", filterAction), a.bind("compare.git", gitAction), item("↑↓", "move", ""), a.bind("compare.select", "toggle choice"),
	}
	if !c.result.ContentChecked || !c.result.Complete {
		out = append(out, a.bind("compare.hash", "SHA-256 check"))
	}
	out = append(out, a.bind("compare.help", "keys/statuses"), a.bind("compare.close", "close"))
	return out
}

func fixedBody(lines []string, height, width int) string {
	if height <= 0 {
		return ""
	}
	out := make([]string, 0, height)
	for _, line := range lines {
		if len(out) >= height {
			break
		}
		out = append(out, fitWidth(line, width))
	}
	for len(out) < height {
		out = append(out, strings.Repeat(" ", max(0, width)))
	}
	return strings.Join(out, "\n")
}

// canonicalPanelPath is used after a plan completes so only panels still
// showing one of the reviewed directories are refreshed.
func canonicalPanelPath(p *panel.Panel) string {
	if p == nil || p.InArchive() || p.InRemote() || p.Path() == "" {
		return ""
	}
	real, err := filepath.EvalSymlinks(p.Path())
	if err != nil {
		return ""
	}
	return filepath.Clean(real)
}

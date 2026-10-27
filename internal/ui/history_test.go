package ui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/archive"
	"github.com/ThatOn3Gu7/TwinPane/internal/cloud"
	"github.com/ThatOn3Gu7/TwinPane/internal/compress"
	"github.com/ThatOn3Gu7/TwinPane/internal/history"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/settings"
)

// Persistent operation history: recording, browsing and retention.

// configPaths returns the preferences and history paths used by the harness.
func configPaths(root string) (string, string) {
	return filepath.Join(root, "config", "settings.json"), filepath.Join(root, "config", "history.json")
}

// allRecords flattens the recorded history, newest session first.
func allRecords(st *history.Store) []history.Record {
	var out []history.Record
	for _, sess := range st.Sessions() {
		out = append(out, sess.Operations()...)
	}
	return out
}

func onlyRecord(t *testing.T, st *history.Store) history.Record {
	t.Helper()
	recs := allRecords(st)
	if len(recs) != 1 {
		t.Fatalf("history has %d records, want 1: %+v", len(recs), recs)
	}
	return recs[0]
}

// newHistoryApp builds an app whose history is written to path.
func newHistoryApp(t *testing.T, root, startDir, historyPath string) (*harness, *App) {
	t.Helper()
	settingsPath, _ := configPaths(root)
	a := New(Options{
		Version:      "test",
		StartDir:     startDir,
		SettingsPath: settingsPath,
		HistoryPath:  historyPath,
	})
	h := &harness{t: t, a: a, msgs: make(chan tea.Msg, 256)}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.exec(a.Init())
	h.until("panels", func() bool { return !a.Panel(0).Loading() && !a.Panel(1).Loading() })
	return h, a
}

// ---- recording -------------------------------------------------------------------

func TestCopyRecordsBackgroundOperation(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("a.txt")
	h.press("c")
	h.waitDialog(dlgConfirm)
	h.press("enter")
	h.until("copy", func() bool { return !h.a.busy() })

	rec := onlyRecord(t, h.a.History())
	if rec.Op != "Copy" {
		t.Fatalf("op = %q, want Copy", rec.Op)
	}
	if rec.Status != history.StatusSuccess {
		t.Fatalf("status = %q, want success", rec.Status)
	}
	if rec.Dest != env.dst {
		t.Fatalf("dest = %q, want %q", rec.Dest, env.dst)
	}
	if len(rec.Sources) != 1 || rec.Sources[0] != filepath.Join(env.src, "a.txt") {
		t.Fatalf("sources = %v", rec.Sources)
	}
	if rec.Files != 1 || rec.Bytes == 0 {
		t.Fatalf("counts = %d files, %d bytes", rec.Files, rec.Bytes)
	}
	if rec.Started.IsZero() || rec.Finished.IsZero() {
		t.Fatalf("times missing: %+v", rec)
	}
	if rec.Summary == "" {
		t.Fatal("no summary recorded")
	}
	// The record is on disk immediately (a crash keeps it).
	path := h.a.History().Path()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("history file: %v", err)
	}
	var saved struct {
		Version  int `json:"version"`
		Sessions []struct {
			Operations []history.Record `json:"operations"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("history file is not valid JSON: %v", err)
	}
	if saved.Version != 1 || len(saved.Sessions) != 1 || len(saved.Sessions[0].Operations) != 1 {
		t.Fatalf("saved history = %+v", saved)
	}
}

func TestMoveDeleteAndArchiveAreRecorded(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	// Move a file to the other panel.
	h.focus("b.txt")
	h.press("m")
	h.waitDialog(dlgConfirm)
	h.press("enter")
	h.until("move", func() bool { return !h.a.busy() })
	// Delete a file in the source panel (it goes to the trash).
	h.focus("a.txt")
	h.press("d")
	h.waitDialog(dlgDelete)
	h.press("y")
	h.until("delete", func() bool { return !h.a.busy() })
	h.settle()

	recs := allRecords(h.a.History())
	if len(recs) != 2 {
		t.Fatalf("recorded %d operations, want 2: %+v", len(recs), recs)
	}
	if recs[0].Op != "Delete (trash)" || recs[1].Op != "Move" { // newest first
		t.Fatalf("operations = %q, %q", recs[0].Op, recs[1].Op)
	}
	if recs[0].Status != history.StatusSuccess || len(recs[0].Items) != 1 || recs[0].Items[0] != "a.txt" {
		t.Fatalf("delete record = %+v", recs[0])
	}
	if !strings.Contains(recs[0].Summary, "trash") || recs[0].Result != "moved to the trash" {
		t.Fatalf("delete result = %q / %q", recs[0].Summary, recs[0].Result)
	}
	if !fileExists(filepath.Join(env.dst, "b.txt")) {
		t.Fatal("move did not transfer the file")
	}
}

func TestFailureCancelAndInterruptionAreRecorded(t *testing.T) {
	root := t.TempDir()
	_, historyPath := configPaths(root)
	_, a := newHistoryApp(t, root, root, historyPath)

	// A background task that fails.
	s, _ := a.newSession("Copy", 0, []string{filepath.Join(root, "x.bin")})
	s.to = root
	a.finishTask(s, taskFailed, "Copy failed", "x.bin: permission denied")
	// A background task the user cancels.
	c, _ := a.newSession("Delete", 0, []string{filepath.Join(root, "y.bin")})
	a.finishTask(c, taskCanceled, "Delete cancelled — nothing was deleted")
	// A task that is still running when TwinPane is killed is covered by the
	// next test: here the store keeps it running.
	r, _ := a.newSession("Move", 0, []string{filepath.Join(root, "z.bin")})
	_ = r

	recs := allRecords(a.History())
	if len(recs) != 3 {
		t.Fatalf("recorded %d operations, want 3", len(recs))
	}
	if recs[2].Status != history.StatusFailed {
		t.Fatalf("failure status = %q", recs[2].Status)
	}
	if len(recs[2].Errors) == 0 || !strings.Contains(recs[2].Errors[0], "permission denied") {
		t.Fatalf("failure errors = %v", recs[2].Errors)
	}
	if recs[1].Status != history.StatusCanceled {
		t.Fatalf("cancel status = %q", recs[1].Status)
	}
	if recs[0].Status != history.StatusRunning {
		t.Fatalf("running status = %q", recs[0].Status)
	}
	if !recs[0].Finished.IsZero() {
		t.Fatalf("a running record must not have a finish time: %+v", recs[0])
	}
}

func TestInterruptedOperationNeverLooksSuccessful(t *testing.T) {
	root := t.TempDir()
	settingsPath, historyPath := configPaths(root)
	// A run that started a move and was killed before it finished.
	first := New(Options{Version: "test", StartDir: root, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(first, first.Init())
	s, _ := first.newSession("Move", 0, []string{filepath.Join(root, "big.iso")})
	_ = s
	// Nothing finishes this task: the app "exits" here.

	second := New(Options{Version: "test", StartDir: root, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(second, second.Init())
	second.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	recs := allRecords(second.History())
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1", len(recs))
	}
	rec := recs[0]
	if rec.Status != history.StatusInterrupted {
		t.Fatalf("status = %q, want interrupted", rec.Status)
	}
	if rec.Status == history.StatusSuccess || rec.Summary != "" {
		t.Fatalf("interrupted operation reports a result: %+v", rec)
	}
	if rec.Duration() != 0 {
		t.Fatalf("interrupted operation has a duration: %v", rec.Duration())
	}
	// Selecting it in the Tasks screen shows the unknown outcome.
	press(second, "t", "h")
	if v := second.View(); !strings.Contains(v, "INTERRUPTED") {
		t.Fatalf("history view does not show the interruption:\n%s", v)
	}
	if v := second.View(); !strings.Contains(v, "outcome is unknown") {
		t.Fatalf("details do not explain the interruption:\n%s", v)
	}
}

func TestForegroundRenameNewFileAndNewFolderAreRecorded(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	// Rename
	h.focus("a.txt")
	h.press("r")
	if h.dialog() == nil {
		t.Fatalf("r must open the rename dialog:\n%s", h.view())
	}
	if !strings.Contains(h.view(), "Rename") {
		t.Fatalf("rename dialog missing:\n%s", h.view())
	}
	h.press("ctrl+u") // the extension stays: typing a stem keeps ".txt"
	h.typeText("renamed", false)
	h.press("enter")
	h.idle()
	// New file (n → file)
	h.press("n", "f")
	h.typeText("fresh.md", false)
	h.press("enter")
	h.idle()
	// New folder (F7)
	h.press("f7")
	h.typeText("newdir", false)
	h.press("enter")
	h.idle()

	recs := allRecords(h.a.History())
	if len(recs) != 3 {
		t.Fatalf("recorded %d foreground operations, want 3: %+v", len(recs), recs)
	}
	if recs[2].Op != "Rename" || recs[2].Status != history.StatusSuccess {
		t.Fatalf("rename record = %+v", recs[2])
	}
	if recs[2].Dest != filepath.Join(env.src, "renamed.txt") {
		t.Fatalf("rename dest = %q", recs[2].Dest)
	}
	if recs[1].Op != "New file" || recs[1].Status != history.StatusSuccess {
		t.Fatalf("new file record = %+v", recs[1])
	}
	if recs[0].Op != "New folder" || recs[0].Dest != env.src || recs[0].Items[0] != "newdir" {
		t.Fatalf("new folder record = %+v", recs[0])
	}
	if !fileExists(filepath.Join(env.src, "newdir")) || !fileExists(filepath.Join(env.src, "fresh.md")) {
		t.Fatal("foreground operations did not happen")
	}
}

func TestFailedForegroundRenameIsRecorded(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("a.txt")
	h.press("r")
	if h.dialog() == nil {
		t.Fatalf("the rename dialog did not open:\n%s", h.view())
	}
	h.press("ctrl+u")
	h.typeText("b", false) // b.txt exists: rename must refuse
	h.press("enter")
	if h.dialog() == nil {
		t.Fatal("the dialog should stay open after a failed rename")
	}
	rec := onlyRecord(t, h.a.History())
	if rec.Status != history.StatusFailed {
		t.Fatalf("status = %q, want failed", rec.Status)
	}
	if len(rec.Errors) == 0 {
		t.Fatalf("failed rename has no error: %+v", rec)
	}
	if !fileExists(filepath.Join(env.src, "a.txt")) {
		t.Fatal("the file was changed by a failed rename")
	}
}

func TestArchiveOperationsAreRecorded(t *testing.T) {
	root := t.TempDir()
	_, historyPath := configPaths(root)
	_, a := newHistoryApp(t, root, root, historyPath)

	// Compress: a finished archive with counts.
	zip, _ := a.newSession("Creating ZIP", 0, []string{filepath.Join(root, "a.txt"), filepath.Join(root, "sub")})
	zip.to = filepath.Join(root, "out.zip")
	a.finishZip(zip, compress.Result{Output: filepath.Join(root, "out.zip"), Files: 2, Dirs: 1, Links: 0, Bytes: 4096})

	// Extract: into a folder, with an unsafe entry refused.
	ext, _ := a.newSession("Extracting", 0, []string{filepath.Join(root, "out.zip")})
	ext.to = filepath.Join(root, "out")
	a.finishExtract(ext, archive.ExtractResult{Dest: filepath.Join(root, "out"), Files: 2, Dirs: 1, Bytes: 4096, Top: []string{"a.txt"}, Unsafe: 1}, "", "")

	// Compress failure: never recorded as success.
	bad, _ := a.newSession("Creating ZIP", 0, []string{filepath.Join(root, "big.bin")})
	bad.to = filepath.Join(root, "bad.zip")
	a.finishZip(bad, compress.Result{
		Output:   filepath.Join(root, "bad.zip"),
		Canceled: true,
		Errors:   []ops.ItemError{{Path: "big.bin", Op: "add", Err: errors.New("disk full")}},
	})

	recs := allRecords(a.History())
	if len(recs) != 3 {
		t.Fatalf("recorded %d archive operations, want 3: %+v", len(recs), recs)
	}
	if recs[2].Op != "Archive" && recs[2].Op != "Creating ZIP" {
		t.Fatalf("compress op = %q", recs[2].Op)
	}
	if recs[2].Status != history.StatusSuccess || recs[2].Files != 2 || recs[2].Dirs != 1 || recs[2].Bytes != 4096 {
		t.Fatalf("compress record = %+v", recs[2])
	}
	if recs[1].Op != "Extract" || recs[1].Status != history.StatusSuccess || recs[1].Files != 2 {
		t.Fatalf("extract record = %+v", recs[1])
	}
	if !strings.Contains(recs[1].Result, "unsafe") {
		t.Fatalf("extract result = %q", recs[1].Result)
	}
	// Canceled after writing part of the archive: partial, never success.
	if recs[0].Status != history.StatusPartial {
		t.Fatalf("canceled compress status = %q", recs[0].Status)
	}
	if len(recs[0].Errors) == 0 || !strings.Contains(recs[0].Errors[0], "disk full") {
		t.Fatalf("failed compress errors = %v", recs[0].Errors)
	}
}

func TestBulkRenameIsRecorded(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.a.recordBulkRename(time.Now(), env.src, ops.BatchResult{
		Renamed: 1,
		Items: []ops.BatchItemResult{
			{Old: "a.txt", New: "a1.txt", Outcome: ops.OutcomeRenamed},
			{Old: "b.txt", New: "a1.txt", Outcome: ops.OutcomeFailed, Err: errors.New("already exists")},
		},
	}, nil)
	rec := onlyRecord(t, h.a.History())
	if rec.Op != "Bulk rename" || rec.Status != history.StatusPartial {
		t.Fatalf("bulk rename record = %+v", rec)
	}
	if rec.Files != 1 || len(rec.Errors) != 1 || !strings.Contains(rec.Errors[0], "already exists") {
		t.Fatalf("bulk rename details = %+v", rec)
	}
	if len(rec.Items) != 1 || !strings.Contains(rec.Items[0], "a1.txt") {
		t.Fatalf("bulk rename items = %v", rec.Items)
	}
}

func TestPermissionsChangeIsRecorded(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "a.txt")
	h.a.recordOperation(history.Record{
		Op:      "Permissions",
		Status:  history.StatusSuccess,
		Sources: []string{path},
		Result:  "-rw-r----- (640)",
		Summary: "Permissions set",
	})
	rec := onlyRecord(t, h.a.History())
	if rec.Op != "Permissions" || rec.Result != "-rw-r----- (640)" {
		t.Fatalf("permissions record = %+v", rec)
	}
}

func TestCloudExitCodeIsRecorded(t *testing.T) {
	root := t.TempDir()
	_, historyPath := configPaths(root)
	_, a := newHistoryApp(t, root, root, historyPath)
	s, _ := a.newSession("Downloading", 0, nil)
	s.hist.result = cloudResultText(&cloud.Error{Op: "copy", Code: 9, Msg: "directory not found"})
	a.finishTask(s, taskFailed, "failed: directory not found")
	rec := onlyRecord(t, a.History())
	if rec.Result != "rclone exit code 9" {
		t.Fatalf("result = %q, want the rclone exit code", rec.Result)
	}
	if rec.Status != history.StatusFailed {
		t.Fatalf("status = %q", rec.Status)
	}
}

// ---- persistence, ordering and retention -----------------------------------------

func TestHistorySurvivesRestartAndListsSessionsNewestFirst(t *testing.T) {
	root := t.TempDir()
	settingsPath, historyPath := configPaths(root)
	startDir := filepath.Join(root, "files")
	os.MkdirAll(startDir, 0o755)

	first := New(Options{Version: "run-1", StartDir: startDir, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(first, first.Init())
	first.recordOperation(history.Record{Op: "New folder", Status: history.StatusSuccess, Dest: startDir, Items: []string{"one"}})

	second := New(Options{Version: "run-2", StartDir: startDir, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(second, second.Init())
	second.recordOperation(history.Record{Op: "New folder", Status: history.StatusSuccess, Dest: startDir, Items: []string{"two"}})

	// A third run (the browsing session) sees both earlier runs, newest first.
	third := New(Options{Version: "run-3", StartDir: startDir, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(third, third.Init())
	third.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	sessions := third.History().Sessions()
	if len(sessions) != 3 {
		t.Fatalf("sessions = %d, want 3", len(sessions))
	}
	if len(sessions[0].Operations()) != 0 {
		t.Fatalf("the running session already has operations: %+v", sessions[0])
	}
	if sessions[1].Version != "run-2" || sessions[2].Version != "run-1" {
		t.Fatalf("order = %q, %q", sessions[1].Version, sessions[2].Version)
	}
	if got := sessions[2].Operations()[0].Items[0]; got != "one" {
		t.Fatalf("oldest run lost its record: %q", got)
	}
	// The Tasks screen shows past runs.
	press(third, "t", "h")
	if v := third.View(); !strings.Contains(v, "Operation history") {
		t.Fatalf("history view missing:\n%s", v)
	}
}

func TestHistoryRetentionComesFromSettings(t *testing.T) {
	root := t.TempDir()
	settingsPath, historyPath := configPaths(root)
	os.MkdirAll(filepath.Dir(settingsPath), 0o700)
	if err := os.WriteFile(settingsPath,
		[]byte(`{"version":1,"showHidden":false,"historySessions":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	startDir := filepath.Join(root, "files")
	os.MkdirAll(startDir, 0o755)
	for i := 0; i < 5; i++ {
		a := New(Options{Version: "test", StartDir: startDir, SettingsPath: settingsPath, HistoryPath: historyPath})
		drain(a, a.Init())
		if got := a.historyRetention(); got != 2 {
			t.Fatalf("run %d retention = %d, want 2", i, got)
		}
		a.recordOperation(history.Record{Op: "Copy", Status: history.StatusSuccess})
	}
	last := New(Options{Version: "test", StartDir: startDir, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(last, last.Init())
	if got := len(last.History().Sessions()); got != 2 {
		t.Fatalf("kept %d sessions, want 2", got)
	}
}

func TestRetentionKeyChangesLimitAndKeepsCurrentRun(t *testing.T) {
	root := t.TempDir()
	settingsPath, historyPath := configPaths(root)
	os.MkdirAll(filepath.Dir(settingsPath), 0o700)
	os.WriteFile(settingsPath, []byte(`{"version":1,"historySessions":5}`), 0o600)
	a := New(Options{Version: "test", StartDir: root, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(a, a.Init())
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	a.recordOperation(history.Record{Op: "Copy", Status: history.StatusSuccess})
	cur := a.History().CurrentID()
	press(a, "t", "h")
	for i := 0; i < 10; i++ { // 5 -> 1 is the floor
		press(a, "-")
	}
	if got := a.historyRetention(); got != 1 {
		t.Fatalf("retention after lowering = %d, want 1", got)
	}
	if !strings.Contains(a.View(), "keeping 1") {
		t.Fatalf("header does not show the new limit:\n%s", a.View())
	}
	sessions := a.History().Sessions()
	if len(sessions) != 1 || sessions[0].ID != cur {
		t.Fatalf("the running session was pruned: %+v", sessions)
	}
	// The preference is saved for the next run.
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"historySessions": 1`) {
		t.Fatalf("settings not saved: %s", data)
	}
	for i := 0; i < 3; i++ {
		press(a, "+")
	}
	if got := a.historyRetention(); got != 4 {
		t.Fatalf("retention after raising = %d, want 4", got)
	}
	next := New(Options{Version: "test", StartDir: root, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(next, next.Init())
	if got := next.historyRetention(); got != 4 {
		t.Fatalf("next run reads retention %d, want 4", got)
	}
}

// TestSettingsScreenEditsHistoryRetention covers the Settings screen's half of
// the retention preference: the row exists, its arrows change the limit and the
// file, and an unrelated preference change keeps the saved limit intact (the
// whole preference set is written, never just one field).
func TestSettingsScreenEditsHistoryRetention(t *testing.T) {
	root := t.TempDir()
	settingsPath, historyPath := configPaths(root)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	a := New(Options{Version: "test", StartDir: root, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(a, a.Init())
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	want := settings.DefaultHistorySessions
	if got := a.historyRetention(); got != want {
		t.Fatalf("default retention = %d, want %d", got, want)
	}

	press(a, "f9")
	for a.settingsScr.sel > int(rowHistorySessions) {
		press(a, "up")
	}
	for a.settingsScr.sel < int(rowHistorySessions) {
		press(a, "down")
	}
	if got := a.settingsRows()[rowHistorySessions].value; got != "20 runs (1–200)" {
		t.Fatalf("row value = %q", got)
	}
	// ← (h) lowers the limit by one and prunes nothing yet.
	press(a, "h")
	if got := a.historyRetention(); got != want-1 {
		t.Fatalf("retention after ← = %d, want %d", got, want-1)
	}
	if data, err := os.ReadFile(settingsPath); err != nil || !strings.Contains(string(data), `"historySessions": 19`) {
		t.Fatalf("retention not saved: %s (%v)", data, err)
	}

	// Flipping an unrelated preference must not reset the retention.
	for a.settingsScr.sel > int(rowShowHidden) {
		press(a, "up")
	}
	press(a, "space") // show hidden files
	prefs, err := settings.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !prefs.ShowHidden {
		t.Fatal("the toggle did not reach the file")
	}
	if prefs.HistorySessions != want-1 {
		t.Fatalf("saving another preference changed the retention to %d", prefs.HistorySessions)
	}

	// The floor and the ceiling hold, and r brings the default back.
	for a.settingsScr.sel < int(rowHistorySessions) {
		press(a, "down")
	}
	for i := 0; i < 40; i++ {
		press(a, "h")
	}
	if got := a.historyRetention(); got != history.MinSessions {
		t.Fatalf("retention floor = %d, want %d", got, history.MinSessions)
	}
	press(a, "r")
	press(a, "y") // the reset dialog's explicit action
	if got := a.historyRetention(); got != want {
		t.Fatalf("retention after reset = %d, want %d", got, want)
	}
	press(a, "esc")
}

// ---- browsing the history in the Tasks screen ------------------------------------

func seedHistory(t *testing.T, path string, sessions int, opsPer int) {
	t.Helper()
	clock := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	for s := 0; s < sessions; s++ {
		st, err := history.Open(path, history.Options{
			MaxSessions: sessions + 2,
			Version:     "seed",
			Now:         func() time.Time { clock = clock.Add(time.Second); return clock },
		})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < opsPer; i++ {
			status := history.StatusSuccess
			if i%2 == 1 {
				status = history.StatusFailed
			}
			st.Add(history.Record{
				Op:      []string{"Copy", "Move", "Delete"}[i%3],
				Status:  status,
				Dest:    filepath.Join("/data", []string{"alpha", "beta", "gamma"}[i%3]),
				Sources: []string{filepath.Join("/src", "file.txt")},
				Summary: "recorded",
			})
		}
	}
}

// TestHistoryViewSwallowsTaskActions: the keys of the live task actions (x
// clears finished tasks, c cancels one) must not fire while the history view
// is on screen — the user is reading past runs, not driving this run.
func TestHistoryViewSwallowsTaskActions(t *testing.T) {
	root := t.TempDir()
	settingsPath, historyPath := configPaths(root)
	os.MkdirAll(filepath.Dir(historyPath), 0o700)
	seedHistory(t, historyPath, 2, 1)
	a := New(Options{Version: "test", StartDir: root, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(a, a.Init())
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// A finished task in this run's list, so "x" would have something to clear.
	press(a, "t")
	task, _ := a.newSession("Copying", 0, nil)
	a.finishTask(task, taskCompleted, "Copied 1 file")
	before := len(a.tasks.order)
	if before == 0 {
		t.Fatal("the test needs a finished task in the list")
	}

	press(a, "h") // into the history view
	if !a.tasks.histShowing {
		t.Fatal("h must switch to the history view")
	}
	press(a, "x")
	if got := len(a.tasks.order); got != before {
		t.Fatalf("x cleared tasks from the history view: %d -> %d", before, got)
	}
	press(a, "c")
	if !a.tasks.showing || !strings.Contains(a.View(), "Operation history") {
		t.Fatal("c must not act on the task list from the history view")
	}
	press(a, "h") // back to the tasks
	if a.tasks.histShowing {
		t.Fatal("h must return to the task list")
	}
	press(a, "t")
}

func TestHistoryViewBrowseAndDetails(t *testing.T) {
	root := t.TempDir()
	settingsPath, historyPath := configPaths(root)
	os.MkdirAll(filepath.Dir(historyPath), 0o700)
	seedHistory(t, historyPath, 3, 3)
	a := New(Options{Version: "test", StartDir: root, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(a, a.Init())
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	drain(a, a.Init())

	press(a, "t")
	if v := a.View(); !strings.Contains(v, "Background tasks") {
		t.Fatalf("t must open the task list:\n%s", v)
	}
	press(a, "h")
	v := a.View()
	for _, want := range []string{"Operation history", "keeping", "seed", "DONE", "FAILED"} {
		if !strings.Contains(v, want) {
			t.Fatalf("history view misses %q:\n%s", want, v)
		}
	}
	if !strings.Contains(v, "Details ·") {
		t.Fatalf("history view has no details block:\n%s", v)
	}
	// The newest run with operations is selected first (this run has none
	// yet), and its newest operation.
	if a.tasks.histSession != 1 || a.tasks.histOp != 0 {
		t.Fatalf("initial selection = run %d op %d", a.tasks.histSession, a.tasks.histOp)
	}
	before := a.View()
	press(a, "down")
	if a.tasks.histOp != 1 {
		t.Fatalf("down did not move the operation selection: %d", a.tasks.histOp)
	}
	if a.View() == before {
		t.Fatal("the details did not change with the selection")
	}
	press(a, "down", "down", "down") // cannot pass the last operation
	if a.tasks.histOp != 2 {
		t.Fatalf("selection left the run: %d", a.tasks.histOp)
	}
	// Older runs
	press(a, "[")
	if a.tasks.histSession != 2 || a.tasks.histOp != 0 {
		t.Fatalf("'[' selection = run %d op %d", a.tasks.histSession, a.tasks.histOp)
	}
	press(a, "[", "[", "[")
	if last := len(a.historySessions()) - 1; a.tasks.histSession != last {
		t.Fatalf("selection passed the oldest run: %d", a.tasks.histSession)
	}
	press(a, "]")
	if a.tasks.histSession != len(a.historySessions())-2 {
		t.Fatalf("']' = run %d", a.tasks.histSession)
	}
	// Details scroll
	press(a, "pgdown")
	if a.tasks.histOffset == 0 {
		t.Fatal("PgDn did not scroll the details")
	}
	press(a, "pgup", "pgup")
	if a.tasks.histOffset != 0 {
		t.Fatalf("PgUp did not scroll back: %d", a.tasks.histOffset)
	}
	// Back to the live view, then close
	press(a, "h")
	if v := a.View(); !strings.Contains(v, "Background tasks") {
		t.Fatalf("h did not return to the live task list:\n%s", v)
	}
	press(a, "t")
	if a.tasks.showing {
		t.Fatal("t did not close the tasks screen")
	}
}

func TestHistoryViewWorksOnANarrowTerminal(t *testing.T) {
	root := t.TempDir()
	settingsPath, historyPath := configPaths(root)
	os.MkdirAll(filepath.Dir(historyPath), 0o700)
	seedHistory(t, historyPath, 4, 2)
	a := New(Options{Version: "test", StartDir: root, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(a, a.Init())
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	drain(a, a.Init())
	press(a, "t", "h")
	v := a.View()
	lines := strings.Split(v, "\n")
	if len(lines) > 12 {
		t.Fatalf("view is %d lines tall on a 12-line terminal", len(lines))
	}
	for _, line := range lines {
		if w := ansi.StringWidth(ansi.Strip(line)); w > 40 {
			t.Fatalf("line wider than the terminal (%d > 40): %q", w, line)
		}
	}
	if !strings.Contains(v, "Operation history") {
		t.Fatalf("narrow history view lost its header:\n%s", v)
	}
	// Keys still work with almost no room for the detail block.
	press(a, "[", "down", "pgdown", "]", "h")
	if a.tasks.histShowing {
		t.Fatal("h did not leave the history view")
	}
}

// ---- corrupted and read-only files ------------------------------------------------

func TestDamagedHistoryFileKeepsRecording(t *testing.T) {
	root := t.TempDir()
	settingsPath, historyPath := configPaths(root)
	os.MkdirAll(filepath.Dir(historyPath), 0o700)
	if err := os.WriteFile(historyPath, []byte(`{"version":1,"sessions":[{"id"`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(Options{Version: "test", StartDir: root, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(a, a.Init())
	st, _ := a.Status()
	if !strings.Contains(st, "history") {
		t.Fatalf("the damaged file was not reported: %q", st)
	}
	a.recordOperation(history.Record{Op: "Copy", Status: history.StatusSuccess})
	if len(allRecords(a.History())) != 1 {
		t.Fatal("recording stopped after a damaged file")
	}
	// The damaged file was kept aside, not deleted.
	entries, err := os.ReadDir(filepath.Dir(historyPath))
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, e := range entries {
		if strings.Contains(e.Name(), "history.json.corrupt-") {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("damaged history was not preserved: %v", entries)
	}
}

func TestNewerHistoryFileIsNotOverwritten(t *testing.T) {
	root := t.TempDir()
	settingsPath, historyPath := configPaths(root)
	os.MkdirAll(filepath.Dir(historyPath), 0o700)
	body := `{"version":99,"sessions":[]}`
	if err := os.WriteFile(historyPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(Options{Version: "test", StartDir: root, SettingsPath: settingsPath, HistoryPath: historyPath})
	drain(a, a.Init())
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	a.recordOperation(history.Record{Op: "Copy", Status: history.StatusSuccess})
	press(a, "t", "h")
	if a.History().ReadOnly() != true {
		t.Fatal("a newer history file must be read-only")
	}
	if v := a.View(); !strings.Contains(v, "read-only") {
		t.Fatalf("the history view does not mention read-only:\n%s", v)
	}
	if data, err := os.ReadFile(historyPath); err != nil || string(data) != body {
		t.Fatalf("the newer file was changed: %q (%v)", data, err)
	}
}

func TestProgressUpdatesDoNotCreateRecords(t *testing.T) {
	// Recording happens once per operation (start and finish), not per item.
	root := t.TempDir()
	_, historyPath := configPaths(root)
	a := New(Options{Version: "test", StartDir: root, HistoryPath: historyPath})
	drain(a, a.Init())
	h := &harness{t: t, a: a, msgs: make(chan tea.Msg, 16)}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	s, _ := a.newSession("Copy", 0, []string{root})
	for i := 0; i < 200; i++ { // many progress updates
		h.send(opProgressMsg{id: s.id, p: ops.Progress{Phase: "copying", DoneItems: i, TotalItems: 200}})
	}
	a.finishTask(s, taskCompleted, "Copied 200 files")
	rec := onlyRecord(t, a.History())
	if rec.Status != history.StatusSuccess {
		t.Fatalf("status = %q", rec.Status)
	}
	if len(a.History().Sessions()) != 1 {
		t.Fatal("progress updates created extra history entries")
	}
}

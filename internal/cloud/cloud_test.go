package cloud

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

// The tests in this file never use a real cloud provider. Two kinds of
// stand-in are used:
//
//   - a fake rclone: the test binary itself, re-started with
//     TWINPANE_FAKE_RCLONE set (see TestMain), which simulates failures that
//     cannot be produced locally (expired logins, hanging servers, programs
//     that ignore interrupts, unreadable output);
//   - the real rclone, when it is installed, with remotes of the "local" and
//     "alias" types in a temporary configuration (rclone_test.go): real
//     rclone behaviour, but no network and no credentials.

func TestMain(m *testing.M) {
	if mode := os.Getenv("TWINPANE_FAKE_RCLONE"); mode != "" {
		os.Exit(fakeRclone(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeRclone behaves like rclone in the given mode.
func fakeRclone(mode string, args []string) int {
	if log := os.Getenv("FAKE_LOG"); log != "" {
		f, _ := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		fmt.Fprintln(f, strings.Join(args, "\x1f"))
		f.Close()
	}
	switch mode {
	case "remotes":
		fmt.Println("gdrive:      drive")
		fmt.Println("my photos:   onedrive")
		fmt.Println("box:         alias")
		return 0
	case "none":
		return 0
	case "auth":
		fmt.Fprintln(os.Stderr, `2026/10/17 12:12:51 CRITICAL: Failed to create file system for "gd:": couldn't find root directory ID: couldn't fetch token: invalid_grant: maybe token expired? - try refreshing with "rclone config reconnect gd:"`)
		return 1
	case "offline":
		fmt.Fprintln(os.Stderr, `2026/10/17 12:00:00 CRITICAL: Failed to create file system for "gd:": Get "https://www.googleapis.com/drive/v3/files": dial tcp: lookup www.googleapis.com: no such host`)
		return 1
	case "garbage":
		fmt.Println("this is not json")
		return 0
	case "list":
		fmt.Println(`[{"Path":"b dir","Name":"b dir","Size":-1,"ModTime":"2026-01-02T03:04:05Z","IsDir":true},` +
			`{"Path":"a #1.txt","Name":"a #1.txt","Size":12,"ModTime":"2026-01-02T03:04:05Z","IsDir":false},` +
			`{"Path":".hidden","Name":".hidden","Size":1,"ModTime":"2026-01-02T03:04:05Z","IsDir":false},` +
			`{"Path":"x/y","Name":"x/y","Size":1,"ModTime":"2026-01-02T03:04:05Z","IsDir":false}]`)
		return 0
	case "hang", "stubborn":
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		if f := os.Getenv("FAKE_READY"); f != "" {
			os.WriteFile(f, []byte("ready"), 0o600)
		}
		for {
			select {
			case <-ch:
				if f := os.Getenv("FAKE_SIGNALED"); f != "" {
					os.WriteFile(f, []byte("interrupted"), 0o600)
				}
				if mode == "hang" {
					return 130 // like rclone: clean up and exit
				}
			case <-time.After(30 * time.Second):
				return 0
			}
		}
	}
	fmt.Fprintln(os.Stderr, "fake rclone: unknown mode", mode)
	return 2
}

func fakeClient(t *testing.T, mode string, env ...string) *Client {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake rclone relies on POSIX signals")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &Client{Bin: exe, Env: append([]string{"TWINPANE_FAKE_RCLONE=" + mode}, env...), Grace: 300 * time.Millisecond}
}

// ---- paths ---------------------------------------------------------------

func TestParseAndPaths(t *testing.T) {
	cases := []struct {
		in     string
		ok     bool
		remote string
		path   string
	}{
		{"drive:", true, "drive", ""},
		{"drive:Backups/2026", true, "drive", "Backups/2026"},
		{"my photos:a:b", true, "my photos", "a:b"},
		{"disk:/", true, "disk", "/"},
		{"/sdcard/a:b", false, "", ""},
		{"relative/x:y", false, "", ""},
		{":memory:", false, "", ""},
		{"-x:y", false, "", ""},
		{"plain", false, "", ""},
	}
	for _, c := range cases {
		p, ok := Parse(c.in)
		if ok != c.ok || (ok && (p.Remote != c.remote || p.Path != c.path)) {
			t.Errorf("Parse(%q) = %+v, %v; want %q %q %v", c.in, p, ok, c.remote, c.path, c.ok)
		}
		if ok && p.Arg() != c.in {
			t.Errorf("Parse(%q).Arg() = %q", c.in, p.Arg())
		}
	}

	root := On("drive", "")
	if !root.IsRoot() || root.Display() != "drive: /" || root.Dir() != root {
		t.Errorf("root: %+v %q", root, root.Display())
	}
	sub := root.Join("Backups").Join("a b #1")
	if sub.Arg() != "drive:Backups/a b #1" || sub.Base() != "a b #1" || sub.Display() != "drive: /Backups/a b #1" {
		t.Errorf("join: %q %q %q", sub.Arg(), sub.Base(), sub.Display())
	}
	if sub.Dir().Arg() != "drive:Backups" || sub.Dir().Dir() != root {
		t.Errorf("dir: %q %q", sub.Dir().Arg(), sub.Dir().Dir().Arg())
	}
	lr := Remote{Name: "disk", Type: "local"}.Root()
	if lr.Arg() != "disk:/" || !lr.IsRoot() || lr.Join("tmp").Arg() != "disk:/tmp" || lr.Join("tmp").Dir() != lr {
		t.Errorf("local-type remote root: %q %q", lr.Arg(), lr.Join("tmp").Dir().Arg())
	}
	if !sub.Within(root) || !sub.Within(sub) || root.Within(sub) || sub.Within(On("other", "")) {
		t.Error("Within")
	}
	if On("d", "ab").Within(On("d", "a")) {
		t.Error("ab is not within a")
	}
}

func TestParseRemotes(t *testing.T) {
	got := parseRemotes("mydisk:   local  \nmy alias: alias  \nmem:      memory \n\n")
	want := []Remote{{"mydisk", "local"}, {"my alias", "alias"}, {"mem", "memory"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCheckName(t *testing.T) {
	for _, bad := range []string{"", "  ", ".", "..", "a/b", "a\x00"} {
		if CheckName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, good := range []string{"a", "-rf", "a b", "#1 [x] *?", `back\slash`, "über"} {
		if err := CheckName(good); err != nil {
			t.Errorf("%q rejected: %v", good, err)
		}
	}
}

// ---- error classification (messages taken from rclone 1.75) -----------------

func TestExplain(t *testing.T) {
	cases := []struct {
		stderr string
		code   int
		kind   ErrKind
		hint   string
	}{
		{`2026/10/17 12:12:50 CRITICAL: Failed to load config file "/x/enc.conf": unable to decrypt configuration and not allowed to ask for password - set RCLONE_CONFIG_PASS to your configuration password`, 1, KindConfigLocked, "RCLONE_CONFIG_PASS"},
		{`2026/10/17 12:11:59 CRITICAL: Failed to create file system for "zzz:": didn't find section in config file ("zzz")`, 1, KindNoRemote, "rclone config"},
		{`CRITICAL: Failed to create file system for "gd:": couldn't fetch token: invalid_grant: maybe token expired?`, 1, KindAuth, "rclone config reconnect gd:"},
		{`CRITICAL: Failed to create file system for "gd:": dial tcp: lookup x: no such host`, 1, KindNetwork, "check the connection"},
		{"2026/10/17 12:11:59 ERROR : error listing: directory not found\n2026/10/17 12:11:59 NOTICE: Failed to lsjson with 2 errors: last error was: error in ListJSON: directory not found", 3, KindNotFound, ""},
		{`{"level":"error","msg":"Failed to copy: open /x/ro/x.partial: permission denied","object":"n"}` + "\n" + `{"level":"error","msg":"Attempt 1/3 failed with 1 errors and: open /x: permission denied"}`, 1, KindPermission, ""},
		{`ERROR : Attempt 3/3 failed: googleapi: Error 403: The user's Drive storage quota has been exceeded., storageQuotaExceeded`, 1, KindQuota, "quota"},
		{`NOTICE: Failed to purge: can't purge this remote`, 1, KindUnsupported, "does not support"},
		{`something odd`, 2, KindOther, ""},
	}
	for _, c := range cases {
		e := explain("list", "gd", proc.Status{Code: c.code}, c.stderr)
		if e.Kind != c.kind {
			t.Errorf("%q: kind %d want %d (msg %q)", c.stderr, e.Kind, c.kind, e.Msg)
		}
		if c.hint != "" && !strings.Contains(e.Error(), c.hint) {
			t.Errorf("%q: %q lacks %q", c.stderr, e.Error(), c.hint)
		}
		if strings.Contains(e.Msg, "2026/10/17") || strings.Contains(e.Msg, "Attempt ") {
			t.Errorf("message not cleaned: %q", e.Msg)
		}
	}
	if e := explain("list", "gd", proc.Status{Code: 3}, "2026/10/17 12:11:59 NOTICE: Failed to lsjson: error in ListJSON: directory not found"); e.Msg != "directory not found" {
		t.Errorf("msg %q", e.Msg)
	}
	if !KindAuth.Fatal() || !KindNetwork.Fatal() || KindNotFound.Fatal() || KindPermission.Fatal() {
		t.Error("Fatal kinds")
	}
}

// ---- detection and the fake rclone ---------------------------------------------

func TestDetect(t *testing.T) {
	if _, err := Detect(func(string) (string, error) { return "", errors.New("no") }); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("missing rclone: %v", err)
	}
	c, err := Detect(func(n string) (string, error) { return "/opt/bin/" + n, nil })
	if err != nil || c.Bin != "/opt/bin/rclone" {
		t.Fatalf("%v %v", c, err)
	}
	var nilClient *Client
	if _, err := nilClient.Remotes(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("nil client: %v", err)
	}
	for _, c := range []struct {
		termux bool
		goos   string
		want   string
	}{{true, "android", "pkg install rclone"}, {false, "darwin", "brew"}, {false, "linux", "rclone.org"}, {false, "windows", "winget"}} {
		if h := InstallHint(c.termux, c.goos); !strings.Contains(h, c.want) {
			t.Errorf("%v %s: %q", c.termux, c.goos, h)
		}
	}
}

func TestFakeRemotesAndArgs(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	c := fakeClient(t, "remotes", "FAKE_LOG="+log)
	rs, err := c.Remotes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 || rs[1] != (Remote{"my photos", "onedrive"}) {
		t.Fatalf("%v", rs)
	}
	b, _ := os.ReadFile(log)
	args := strings.Split(strings.TrimSpace(string(b)), "\x1f")
	if args[0] != "listremotes" || !contains(args, "--ask-password=false") || !contains(args, "--long") {
		t.Fatalf("args %q", args)
	}
	if rs, err := fakeClient(t, "none").Remotes(context.Background()); err != nil || len(rs) != 0 {
		t.Fatalf("no remotes: %v %v", rs, err)
	}
}

func TestFakeListParsesAndFilters(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	c := fakeClient(t, "list", "FAKE_LOG="+log)
	l, err := c.List(context.Background(), On("gd", "Docs/-x"), fsysOpts(false))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range l.Entries {
		names = append(names, e.Name)
	}
	// ".." first, folders before files, hidden filtered, "x/y" (invalid) dropped
	if strings.Join(names, "|") != "..|b dir|a #1.txt" || l.Hidden != 1 || l.Dirs != 1 || l.Files != 1 {
		t.Fatalf("names %q hidden %d dirs %d files %d", names, l.Hidden, l.Dirs, l.Files)
	}
	if l.Entries[0].Path != "gd:Docs" || l.Entries[2].Path != "gd:Docs/-x/a #1.txt" || l.Path != "gd:Docs/-x" {
		t.Fatalf("paths %q %q %q", l.Entries[0].Path, l.Entries[2].Path, l.Path)
	}
	if l.Entries[1].Size != 0 || !l.Entries[1].IsDir {
		t.Fatalf("dir entry %+v", l.Entries[1])
	}
	b, _ := os.ReadFile(log)
	args := strings.Split(strings.TrimSpace(string(b)), "\x1f")
	// the path comes after "--", as one argument
	if args[len(args)-2] != "--" || args[len(args)-1] != "gd:Docs/-x" {
		t.Fatalf("args %q", args)
	}
	l, err = c.List(context.Background(), On("gd", ""), fsysOpts(true))
	if err != nil || l.Entries[0].IsParent || len(l.Entries) != 3 {
		t.Fatalf("root listing: %+v %v", l.Entries, err)
	}
}

func TestFakeFailures(t *testing.T) {
	ctx := context.Background()
	_, err := fakeClient(t, "auth").List(ctx, On("gd", ""), fsysOpts(false))
	if KindOf(err) != KindAuth || !strings.Contains(err.Error(), "rclone config reconnect gd:") {
		t.Fatalf("auth: %v", err)
	}
	_, err = fakeClient(t, "offline").List(ctx, On("gd", ""), fsysOpts(false))
	if KindOf(err) != KindNetwork {
		t.Fatalf("offline: %v", err)
	}
	_, err = fakeClient(t, "garbage").List(ctx, On("gd", ""), fsysOpts(false))
	if err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("garbage: %v", err)
	}
	_, err = (&Client{Bin: filepath.Join(t.TempDir(), "missing-rclone")}).List(ctx, On("gd", ""), fsysOpts(false))
	if err == nil || !strings.Contains(err.Error(), "cannot start rclone") {
		t.Fatalf("missing binary: %v", err)
	}
}

func waitFile(t *testing.T, p string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(p); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", p)
}

func TestCancelInterruptsRclone(t *testing.T) {
	dir := t.TempDir()
	ready, signaled := filepath.Join(dir, "ready"), filepath.Join(dir, "sig")
	c := fakeClient(t, "hang", "FAKE_READY="+ready, "FAKE_SIGNALED="+signaled)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.List(ctx, On("gd", ""), fsysOpts(false)); done <- err }()
	waitFile(t, ready)
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if KindOf(err) != KindCanceled || !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop rclone")
	}
	if _, err := os.Stat(signaled); err != nil {
		t.Fatal("rclone was not interrupted with SIGINT first")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

func TestCancelKillsStubbornRclone(t *testing.T) {
	dir := t.TempDir()
	ready, signaled := filepath.Join(dir, "ready"), filepath.Join(dir, "sig")
	c := fakeClient(t, "stubborn", "FAKE_READY="+ready, "FAKE_SIGNALED="+signaled)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Remotes(ctx); done <- err }()
	waitFile(t, ready)
	cancel()
	select {
	case err := <-done:
		if KindOf(err) != KindCanceled {
			t.Fatalf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a program ignoring SIGINT was not killed after the grace period")
	}
	waitFile(t, signaled)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

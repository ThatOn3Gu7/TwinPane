package editor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func fakeEnv(vars map[string]string, have ...string) Env {
	return Env{
		Getenv: func(k string) string { return vars[k] },
		LookPath: func(name string) (string, error) {
			for _, h := range have {
				if h == name {
					return "/usr/bin/" + name, nil
				}
			}
			return "", exec.ErrNotFound
		},
		GOOS: "linux",
	}
}

func TestResolveOrder(t *testing.T) {
	cases := []struct {
		vars    map[string]string
		have    []string
		path    string
		args    []string
		source  string
		noteHas string
		errHas  string
	}{
		{map[string]string{"VISUAL": "code --wait", "EDITOR": "vim"}, []string{"code", "vim"}, "/usr/bin/code", []string{"--wait"}, "$VISUAL", "", ""},
		{map[string]string{"EDITOR": "vim"}, []string{"vim", "nano"}, "/usr/bin/vim", nil, "$EDITOR", "", ""},
		{map[string]string{"VISUAL": "  ", "EDITOR": "micro"}, []string{"micro"}, "/usr/bin/micro", nil, "$EDITOR", "", ""},
		// unset: the first detected editor in KnownEditors order, silently
		{nil, []string{"vi", "vim"}, "/usr/bin/vim", nil, "detected", "", ""},
		{nil, []string{"nano", "vi"}, "/usr/bin/nano", nil, "detected", "", ""},
		{nil, []string{"vi", "nvim", "nano"}, "/usr/bin/nvim", nil, "detected", "", ""},
		// set but unusable: fall back to a detected editor and say why
		{map[string]string{"EDITOR": "missing-ed"}, []string{"nano"}, "/usr/bin/nano", nil, "detected", "$EDITOR was not found", ""},
		{map[string]string{"VISUAL": "\"unterminated"}, []string{"nano"}, "/usr/bin/nano", nil, "detected", "cannot be parsed", ""},
		{map[string]string{"VISUAL": "missing-ed", "EDITOR": "vim"}, []string{"vim"}, "/usr/bin/vim", nil, "$EDITOR", "$VISUAL was not found", ""},
		// graphical editors are never picked silently
		{map[string]string{"DISPLAY": ":0"}, []string{"code", "nano"}, "/usr/bin/nano", nil, "detected", "", ""},
		{map[string]string{"EDITOR": "missing-ed"}, nil, "", nil, "", "", "and no other editor was found"},
		{nil, nil, "", nil, "", "", "no editor found"},
	}
	for i, c := range cases {
		ed, err := Resolve(fakeEnv(c.vars, c.have...))
		if c.errHas != "" {
			if err == nil || !strings.Contains(err.Error(), c.errHas) {
				t.Errorf("case %d: err=%v want %q", i, err, c.errHas)
			}
			continue
		}
		if err != nil || ed.Path != c.path || ed.Source != c.source || len(ed.Args) != len(c.args) {
			t.Errorf("case %d: %+v %v", i, ed, err)
		}
		if (c.noteHas == "") != (ed.Note == "") || !strings.Contains(ed.Note, c.noteHas) {
			t.Errorf("case %d: note %q, want %q", i, ed.Note, c.noteHas)
		}
	}
	if fb := Fallbacks("windows"); !strings.Contains(strings.Join(fb, " "), "notepad") {
		t.Fatal(fb)
	}
}

func TestDetect(t *testing.T) {
	links := map[string]string{"/usr/bin/vi": "/usr/bin/nvim", "/usr/bin/editor": "/usr/bin/nano"}
	env := fakeEnv(map[string]string{"EDITOR": "nvim", "VISUAL": "micro -x"}, "nvim", "vi", "nano", "editor", "micro", "code", "mcedit")
	env.EvalSymlinks = func(p string) (string, error) {
		if r, ok := links[p]; ok {
			return r, nil
		}
		return p, nil
	}
	var got []string
	for _, ed := range Detect(env) {
		got = append(got, ed.Display()+"|"+ed.Source)
	}
	// $VISUAL and $EDITOR first; micro without -x is a different command
	// and stays; vi -> nvim and editor -> nano are duplicates; code is
	// graphical (no DISPLAY): not listed.
	want := []string{"micro|$VISUAL", "nvim|$EDITOR", "nano|detected", "micro|detected", "mcedit|detected"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	for _, ed := range Detect(env) {
		if ed.Display() == "mcedit" && !ed.Single {
			t.Fatal("mcedit must be marked single-file")
		}
	}
	env.Getenv = func(k string) string { return map[string]string{"DISPLAY": ":0"}[k] }
	found := false
	for _, ed := range Detect(env) {
		if ed.Display() == "code" {
			found = reflect.DeepEqual(ed.Args, []string{"--wait"})
		}
	}
	if !found {
		t.Fatal("code --wait should be offered in a graphical session")
	}
	// Android never has a graphical session for terminal programs
	env.GOOS = "android"
	for _, ed := range Detect(env) {
		if ed.Display() == "code" {
			t.Fatal("graphical editor offered on android")
		}
	}
}

func TestCommandHasNoLookup(t *testing.T) {
	ed := Editor{Path: "/opt/ed/bin/nvim", Args: []string{"-p"}}
	c := ed.Command("a.txt", "b.txt")
	if c.Path != "/opt/ed/bin/nvim" || c.Err != nil || !reflect.DeepEqual(c.Args, []string{"/opt/ed/bin/nvim", "-p", "a.txt", "b.txt"}) {
		t.Fatalf("%+v", c)
	}
	if !ed.VimLike() || (Editor{Path: "/usr/bin/nano"}).VimLike() {
		t.Fatal("VimLike")
	}
}

func TestSplitArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("backslash handling differs")
	}
	got, err := SplitArgs(`"my editor" -n 'a b' c\ d`)
	if err != nil || !reflect.DeepEqual(got, []string{"my editor", "-n", "a b", "c d"}) {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := SplitArgs(`'open`); err == nil {
		t.Fatal("expected error")
	}
}

func TestCommand(t *testing.T) {
	ed := Editor{Path: "/usr/bin/code", Args: []string{"--wait"}}
	cmd := ed.Command("/tmp/x y.txt")
	if !reflect.DeepEqual(cmd.Args, []string{"/usr/bin/code", "--wait", "/tmp/x y.txt"}) {
		t.Fatal(cmd.Args)
	}
	if len(ed.Args) != 1 {
		t.Fatal("Command must not mutate Args")
	}
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	text := filepath.Join(dir, "notes.md")
	os.WriteFile(text, []byte("# hello\nwörld\ttab\n"), 0o644)
	bin := filepath.Join(dir, "prog")
	os.WriteFile(bin, []byte{0x7f, 'E', 'L', 'F', 0, 0, 1}, 0o644)
	empty := filepath.Join(dir, "empty")
	os.WriteFile(empty, nil, 0o644)

	if fi, err := Check(text); err != nil || fi.Binary {
		t.Fatalf("text: %+v %v", fi, err)
	}
	if fi, err := Check(empty); err != nil || fi.Binary {
		t.Fatalf("empty: %+v %v", fi, err)
	}
	if fi, err := Check(bin); err != nil || !fi.Binary {
		t.Fatalf("binary: %+v %v", fi, err)
	}
	if _, err := Check(dir); !errors.Is(err, ErrIsDir) {
		t.Fatalf("dir: %v", err)
	}
	if _, err := Check(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "broken")); err == nil {
		if _, err := Check(filepath.Join(dir, "broken")); err == nil || !strings.Contains(err.Error(), "broken link") {
			t.Fatalf("broken link: %v", err)
		}
	}
	if runtime.GOOS != "windows" {
		if os.Geteuid() != 0 {
			locked := filepath.Join(dir, "locked")
			os.WriteFile(locked, []byte("x"), 0)
			if _, err := Check(locked); !errors.Is(err, os.ErrPermission) {
				t.Fatalf("unreadable: %v", err)
			}
		}
	}
}

func TestLooksBinary(t *testing.T) {
	if LooksBinary([]byte("plain ascii\n")) || LooksBinary([]byte("日本語テキスト\n")) || LooksBinary([]byte("\x1b[31mred\x1b[0m")) {
		t.Fatal("text flagged as binary")
	}
	if !LooksBinary([]byte("a\x00b")) || !LooksBinary([]byte{0xff, 0xfe, 0xfd, 0xfc, 0xfb, 0xfa, 'a', 'b'}) {
		t.Fatal("binary not detected")
	}
	// multi-byte rune cut at the end of the sample is fine
	s := []byte(strings.Repeat("a", 20) + "é")
	if LooksBinary(s[:len(s)-1]) {
		t.Fatal("truncated rune treated as binary")
	}
}

func TestDescribeExit(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 3").Run()
	if runtime.GOOS != "windows" && DescribeExit("vim", err) != "editor vim exited with status 3: changes may not have been saved" {
		t.Fatal(DescribeExit("vim", err))
	}
	if !strings.Contains(DescribeExit("x", exec.ErrNotFound), "could not run editor x") {
		t.Fatal()
	}
	if DescribeExit("x", nil) != "" {
		t.Fatal()
	}
}

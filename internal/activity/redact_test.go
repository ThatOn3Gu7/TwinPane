package activity

import (
	"strings"
	"testing"
)

// TestRedactRemovesCredentials covers the shapes a credential can take when it
// reaches the log: a flag, a key/value pair, JSON, a query string, a URL, a
// header, and real rclone output.
func TestRedactRemovesCredentials(t *testing.T) {
	cases := []struct {
		name string
		in   string
		gone []string // must not survive
		kept []string // must still be there
	}{
		{"password flag", "--password hunter2", []string{"hunter2"}, []string{"--password"}},
		{"password equals", "--password=hunter2", []string{"hunter2"}, []string{"--password"}},
		{"pass flag", "--pass s3cret", []string{"s3cret"}, []string{"--pass"}},
		{"token flag", "rclone --token abc123def list", []string{"abc123def"}, []string{"rclone", "list"}},
		{"kv equals", "token=abc123", []string{"abc123"}, []string{"token="}},
		{"kv colon", "password: hunter2", []string{"hunter2"}, []string{"password:"}},
		{"json body", `{"access_token":"ya29.a0AfH6","expires_in":3599}`, []string{"ya29.a0AfH6"}, []string{"expires_in"}},
		{"query string", "https://api.example.com/x?access_token=ya29abc&page=2", []string{"ya29abc"}, []string{"page=2"}},
		{"bearer header", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9", []string{"eyJhbGciOiJIUzI1NiJ9"}, []string{"Authorization"}},
		{"basic auth header", "Authorization: Basic YWxpY2U6aHVudGVyMg==", []string{"YWxpY2U6aHVudGVyMg=="}, nil},
		{"url userinfo", "https://alice:hunter2@example.com/folder: 401 Unauthorized", []string{"hunter2"}, []string{"example.com", "401"}},
		{"secret key", "secret_key=AKIAIOSFODNN7EXAMPLE", []string{"AKIAIOSFODNN7EXAMPLE"}, nil},
		{"client secret", `client_secret: "s3cr3t-value"`, []string{"s3cr3t-value"}, nil},
		{"sevenzip password", "7z a -phunter2 -mx=5 out.7z src", []string{"hunter2"}, []string{"7z", "out.7z"}},
		{"rclone config pass", "remote,pass=obscured123:/path", []string{"obscured123"}, []string{"remote"}},
		{"empty stays empty", "", nil, nil},
		// Nothing secret: the line must come through untouched.
		{"plain message", "copy failed: permission denied", nil, []string{"copy failed: permission denied"}},
		{"plain path", "/home/user/Documents/report.pdf", nil, []string{"/home/user/Documents/report.pdf"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Redact(c.in)
			for _, g := range c.gone {
				if strings.Contains(got, g) {
					t.Errorf("Redact(%q) = %q, still contains %q", c.in, got, g)
				}
			}
			for _, k := range c.kept {
				if !strings.Contains(got, k) {
					t.Errorf("Redact(%q) = %q, lost %q", c.in, got, k)
				}
			}
		})
	}
}

// TestRedactIsIdempotent: running it twice must not reveal anything and must
// not keep mangling the text (the viewer re-renders stored entries).
func TestRedactIsIdempotent(t *testing.T) {
	samples := []string{
		"--password hunter2", "Authorization: Bearer abc12345678",
		"https://alice:hunter2@example.com/x", `{"token":"abc"}`,
		"7z a -phunter2 out.7z", "copy /a to /b",
	}
	for _, s := range samples {
		once := Redact(s)
		if twice := Redact(once); twice != once {
			t.Errorf("not idempotent: %q -> %q -> %q", s, once, twice)
		}
		if ContainsSecret(once) {
			t.Errorf("still looks secret after redaction: %q -> %q", s, once)
		}
	}
}

// TestRedactRealRcloneStderr uses the shapes rclone actually writes: its
// errors can echo the account, a URL and an OAuth body.
func TestRedactRealRcloneStderr(t *testing.T) {
	lines := []string{
		`2026/10/27 09:40:00 ERROR : gdrive:/x: error reading source root directory: failed to configure: oauth2: cannot fetch token: 401 Unauthorized` +
			"\n" + `Response: {"error":"invalid_client","error_description":"Unauthorized","access_token":"ya29.A0AfH6SAMPLE"}`,
		`2026/10/27 09:40:01 ERROR : webdav: https://alice:hunter2@dav.example.com/remote.php: 401 Unauthorized`,
		`2026/10/27 09:40:02 NOTICE: Failed to copy with 2 errors: last error was: sftp: unable to authenticate, attempted methods [none password]`,
	}
	for _, line := range lines {
		got := Redact(line)
		for _, bad := range []string{"ya29.A0AfH6SAMPLE", "hunter2"} {
			if strings.Contains(got, bad) {
				t.Errorf("rclone output leaked %q:\n%s", bad, got)
			}
		}
		// The diagnostic value survives: the rclone level and error text stay.
		if !strings.Contains(got, "NOTICE") && !strings.Contains(got, "ERROR") {
			t.Errorf("redaction removed the rclone level:\n%s", got)
		}
	}
}

// TestRedactArgsKeepsExecutable: the executable and subcommand are what make a
// command entry useful, so they must survive while secret flags do not.
func TestRedactArgsKeepsExecutable(t *testing.T) {
	args := []string{"/usr/bin/rclone", "moveto", "--password", "hunter2", "--", "gdrive:/a", "/home/u/b"}
	got := RedactArgs(args)
	joined := strings.Join(got, " ")
	for _, want := range []string{"/usr/bin/rclone", "moveto", "gdrive:/a", "/home/u/b"} {
		if !strings.Contains(joined, want) {
			t.Errorf("lost %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "hunter2") {
		t.Errorf("argument list leaked the password: %q", joined)
	}
	if got[0] != args[0] {
		t.Errorf("the executable changed: %q -> %q", args[0], got[0])
	}
}

// TestRedactionCannotBeBypassedByData proves the store sanitizes fields the
// caller passes, not just the message.
func TestRedactionCannotBeBypassedByData(t *testing.T) {
	s := newTestStore(t, Options{Mode: ModeVerbose})
	s.Log(Entry{
		Level: LevelWarn.String(), Cat: CatCloud, Ev: "cloud.fail",
		Msg:  "failed with token=abc123",
		Err:  "password: hunter2",
		Data: map[string]string{"access_key": "AKIAIOSFODNN7EXAMPLE", "url": "https://a:b@host/x", "src": "/srv/data"},
	})
	s.Close()
	res := Read(s.Path(), 100)
	if len(res.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(res.Entries))
	}
	e := res.Entries[0]
	all := e.Msg + " " + e.Err
	for _, v := range e.Data {
		all += " " + v
	}
	for _, bad := range []string{"abc123", "hunter2", "AKIAIOSFODNN7EXAMPLE"} {
		if strings.Contains(all, bad) {
			t.Fatalf("stored entry leaked %q: %+v", bad, e)
		}
	}
	if strings.Contains(all, "b@host") {
		t.Fatalf("url credentials leaked: %+v", e.Data)
	}
	// The useful path is still there.
	if !strings.Contains(e.Data["src"], "/srv/data") {
		t.Fatalf("ordinary data was lost: %+v", e.Data)
	}
}

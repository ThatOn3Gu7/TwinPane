// Package cloud lets TwinPane's panels browse and change rclone remotes
// (Google Drive, Dropbox, OneDrive, SFTP, S3, … — whatever the user has set
// up with `rclone config`).
//
// TwinPane never talks to a cloud provider itself and never sees or stores
// credentials: every operation runs the user's own rclone program, which
// reads its own configuration. Rules followed here:
//
//   - rclone is started directly (proc.Start, no shell), with every path as
//     a separate argument after "--", so names are never interpreted;
//   - stdin is /dev/null and --ask-password=false is passed, so rclone can
//     never wait for input that would freeze the interface (an encrypted
//     configuration is reported, with a hint, instead of prompting);
//   - every call takes a context: cancelling it interrupts rclone (SIGINT,
//     which lets it delete partially written files) and kills it if it has
//     not exited after a grace period;
//   - an existing destination is never replaced without an explicit
//     Overwrite decision (see Transfer).
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

// ErrNotInstalled is returned by Detect when no rclone program is found.
var ErrNotInstalled = errors.New("rclone is not installed")

// Client runs one rclone program.
type Client struct {
	// Bin is the absolute path of the rclone executable.
	Bin string
	// Env is added to TwinPane's environment for every rclone call (tests
	// use it for RCLONE_CONFIG; nil in normal use).
	Env []string
	// Grace is how long rclone may take to exit after an interrupt before
	// it is killed (0 = 5 seconds).
	Grace time.Duration
}

// Detect finds rclone with lookPath (proc.LookPath when nil).
func Detect(lookPath func(string) (string, error)) (*Client, error) {
	if lookPath == nil {
		lookPath = proc.LookPath
	}
	p, err := lookPath("rclone")
	if err != nil || p == "" {
		return nil, ErrNotInstalled
	}
	return &Client{Bin: p}, nil
}

// InstallHint tells the user how to install rclone on their platform.
func InstallHint(termux bool, goos string) string {
	switch {
	case termux:
		return "pkg install rclone"
	case goos == "darwin":
		return "brew install rclone (or see https://rclone.org/install/)"
	case goos == "windows":
		return "winget install Rclone.Rclone (or see https://rclone.org/install/)"
	default:
		return "install the rclone package of your distribution, or see https://rclone.org/install/"
	}
}

// ---- locations ---------------------------------------------------------------

// Path is a place rclone can read or write: a local absolute path (Remote
// is "") or a path on a configured remote. Remote paths are kept exactly as
// rclone uses them: "" is the remote's root, and "local"-type remotes use
// absolute paths ("/" is their root).
type Path struct {
	Remote string
	Path   string
}

// Local returns the Path of a local absolute path.
func Local(p string) Path { return Path{Path: p} }

// On returns the Path of p on remote.
func On(remote, p string) Path { return Path{Remote: remote, Path: p} }

// IsLocal reports a local path.
func (p Path) IsLocal() bool { return p.Remote == "" }

// Arg is the argument rclone expects: "remote:path" or the local path.
func (p Path) Arg() string {
	if p.IsLocal() {
		return p.Path
	}
	return p.Remote + ":" + p.Path
}

// IsRoot reports the root of a remote.
func (p Path) IsRoot() bool { return !p.IsLocal() && (p.Path == "" || p.Path == "/") }

// Join returns the path of name inside p.
func (p Path) Join(name string) Path {
	switch {
	case p.IsLocal():
		return Path{Path: joinLocal(p.Path, name)}
	case p.Path == "" || strings.HasSuffix(p.Path, "/"):
		return Path{Remote: p.Remote, Path: p.Path + name}
	default:
		return Path{Remote: p.Remote, Path: p.Path + "/" + name}
	}
}

// Base returns the last element ("" at a remote's root).
func (p Path) Base() string {
	s := strings.TrimRight(p.Path, "/")
	if i := strings.LastIndexAny(s, sepChars(p)); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Dir returns the parent (the root is its own parent).
func (p Path) Dir() Path {
	if p.IsRoot() {
		return p
	}
	s := strings.TrimRight(p.Path, "/")
	i := strings.LastIndexAny(s, sepChars(p))
	switch {
	case i < 0:
		s = ""
	case i == 0:
		s = s[:1]
	default:
		s = s[:i]
	}
	return Path{Remote: p.Remote, Path: s}
}

// Display is the label shown to the user: "drive: /Backups".
func (p Path) Display() string {
	if p.IsLocal() {
		return p.Path
	}
	return p.Remote + ": /" + strings.TrimPrefix(p.Path, "/")
}

// Within reports whether p is q or inside q (same remote).
func (p Path) Within(q Path) bool {
	if p.Remote != q.Remote {
		return false
	}
	cut := "/"
	if p.IsLocal() {
		cut += string(os.PathSeparator)
	}
	a, b := strings.TrimRight(p.Path, cut), strings.TrimRight(q.Path, cut)
	if a == b || b == "" { // the same, or q is a root
		return true
	}
	return strings.HasPrefix(a, b+"/") || (p.IsLocal() && strings.HasPrefix(a, b+string(os.PathSeparator)))
}

// Parse splits "remote:path". Remote names cannot contain ':' or '/', so
// a local path such as "/sdcard/a:b" is not mistaken for one.
func Parse(spec string) (Path, bool) {
	i := strings.IndexByte(spec, ':')
	if i <= 0 {
		return Path{}, false
	}
	name := spec[:i]
	if strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, "-") || strings.TrimSpace(name) != name {
		return Path{}, false
	}
	if len(name) == 1 && os.PathSeparator == '\\' {
		return Path{}, false // a Windows drive letter
	}
	return Path{Remote: name, Path: spec[i+1:]}, true
}

func sepChars(p Path) string {
	if p.IsLocal() && os.PathSeparator == '\\' {
		return `/\`
	}
	return "/"
}

func joinLocal(dir, name string) string {
	if strings.HasSuffix(dir, string(os.PathSeparator)) {
		return dir + name
	}
	return dir + string(os.PathSeparator) + name
}

// ---- remotes -----------------------------------------------------------------

// Remote is one configured rclone remote.
type Remote struct {
	Name string // without the colon
	Type string // backend, e.g. "drive", "onedrive", "sftp"
}

// Root is the remote's root folder ("/" for local-type remotes, whose
// paths are absolute).
func (r Remote) Root() Path {
	if r.Type == "local" {
		return On(r.Name, "/")
	}
	return On(r.Name, "")
}

// Remotes lists the configured remotes (rclone listremotes --long). The
// configuration file is read by rclone; TwinPane only sees names and types.
func (c *Client) Remotes(ctx context.Context) ([]Remote, error) {
	out, err := c.output(ctx, "list remotes", []string{"listremotes", "--long"}, nil)
	if err != nil {
		return nil, err
	}
	return parseRemotes(string(out)), nil
}

func parseRemotes(out string) []Remote {
	var rs []Remote
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimRight(l, " \t\r")
		i := strings.LastIndex(l, ":")
		if i <= 0 {
			continue
		}
		name := strings.TrimSpace(l[:i])
		typ := strings.TrimSpace(l[i+1:])
		if name == "" {
			continue
		}
		rs = append(rs, Remote{Name: name, Type: typ})
	}
	return rs
}

// Version returns the first line of `rclone version`.
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.output(ctx, "version", []string{"version"}, nil)
	if err != nil {
		return "", err
	}
	l, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return l, nil
}

// ---- running rclone ------------------------------------------------------------

// commonFlags are passed to every call: never ask for a password, and give
// up on unreachable servers after a reasonable time.
var commonFlags = []string{"--ask-password=false", "--contimeout=20s"}

// listFlags make listings fail fast instead of retrying for minutes.
var listFlags = []string{"--timeout=60s", "--low-level-retries=2", "--retries=1"}

// run starts rclone with args (subcommand first) and waits for it.
// onLine, if set, receives every line rclone writes to stderr. The
// returned stderr keeps the last 64 KiB.
func (c *Client) run(ctx context.Context, args []string, stdout io.Writer, onLine func(string)) (stderr string, st proc.Status, err error) {
	if c == nil || c.Bin == "" {
		return "", proc.Status{}, ErrNotInstalled
	}
	if err := ctx.Err(); err != nil {
		return "", proc.Status{}, err
	}
	argv := append([]string{c.Bin, args[0]}, commonFlags...)
	argv = append(argv, args[1:]...)
	env := os.Environ()
	env = append(env, c.Env...)

	var outR, outW *os.File
	if stdout != nil {
		if outR, outW, err = os.Pipe(); err != nil {
			return "", proc.Status{}, err
		}
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		if outR != nil {
			outR.Close()
			outW.Close()
		}
		return "", proc.Status{}, err
	}
	child, err := proc.Start(proc.Spec{Path: c.Bin, Args: argv, Env: env, Files: []*os.File{nil, outW, errW}, Sys: proc.NewGroup()})
	if outW != nil {
		outW.Close()
	}
	errW.Close()
	if err != nil {
		if outR != nil {
			outR.Close()
		}
		errR.Close()
		return "", proc.Status{}, fmt.Errorf("cannot start rclone: %w", err)
	}

	var wg sync.WaitGroup
	if outR != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			io.Copy(stdout, outR)
			outR.Close()
		}()
	}
	tail := &tailBuffer{max: 64 << 10}
	wg.Add(1)
	go func() {
		defer wg.Done()
		readLines(errR, func(l string) {
			tail.add(l)
			if onLine != nil {
				onLine(l)
			}
		})
		errR.Close()
	}()

	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			child.InterruptGroup()
			grace := c.Grace
			if grace <= 0 {
				grace = 5 * time.Second
			}
			select {
			case <-time.After(grace):
				child.KillGroup()
			case <-stop:
			}
		case <-stop:
		}
	}()
	st, werr := child.Wait()
	close(stop)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		// a process rclone started still holds the pipes open
		if outR != nil {
			outR.Close()
		}
		errR.Close()
		<-done
	}
	if werr != nil {
		return tail.String(), st, werr
	}
	if ctx.Err() != nil {
		return tail.String(), st, context.Canceled
	}
	return tail.String(), st, nil
}

// output runs a command and returns its stdout, or an *Error.
func (c *Client) output(ctx context.Context, op string, args []string, remote *Path) ([]byte, error) {
	var out limitedBuffer
	out.max = 256 << 20
	stderr, st, err := c.run(ctx, args, &out, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, &Error{Op: op, Kind: KindCanceled, Msg: "cancelled"}
		}
		if errors.Is(err, ErrNotInstalled) {
			return nil, err
		}
		return nil, &Error{Op: op, Kind: KindOther, Msg: err.Error()}
	}
	if !st.Success() {
		return nil, explain(op, remoteName(remote), st, stderr)
	}
	if out.over {
		return nil, &Error{Op: op, Kind: KindOther, Msg: "rclone's answer is too large"}
	}
	return out.Bytes(), nil
}

func remoteName(p *Path) string {
	if p == nil {
		return ""
	}
	return p.Remote
}

type limitedBuffer struct {
	bytes.Buffer
	max  int
	over bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		b.over = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

type tailBuffer struct {
	mu    sync.Mutex
	lines []string
	size  int
	max   int
}

func (t *tailBuffer) add(l string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, l)
	t.size += len(l) + 1
	for t.size > t.max && len(t.lines) > 1 {
		t.size -= len(t.lines[0]) + 1
		t.lines = t.lines[1:]
	}
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

// readLines calls f for every line of r (\r and \n both end a line; very
// long lines are cut).
func readLines(r io.Reader, f func(string)) {
	buf := make([]byte, 32<<10)
	var line []byte
	for {
		n, err := r.Read(buf)
		for _, b := range buf[:n] {
			if b == '\n' || b == '\r' {
				if len(line) > 0 {
					f(string(line))
					line = line[:0]
				}
				continue
			}
			if len(line) < 1<<20 {
				line = append(line, b)
			}
		}
		if err != nil {
			if len(line) > 0 {
				f(string(line))
			}
			return
		}
	}
}

// ---- errors ------------------------------------------------------------------

// ErrKind classifies rclone failures so the interface can explain them.
type ErrKind int

const (
	KindOther ErrKind = iota
	KindCanceled
	KindNotFound     // the folder or file does not exist (exit 3 / 4)
	KindNoRemote     // the remote is not in rclone's configuration
	KindConfigLocked // the configuration is encrypted and no password is set
	KindAuth         // login expired or refused
	KindNetwork      // server unreachable
	KindPermission   // access denied
	KindQuota        // out of space / quota
	KindUnsupported  // the backend cannot do this
	KindExists       // the destination already exists (TwinPane's own check)
	KindInvalid      // invalid request (TwinPane's own check)
)

// Error is a failed rclone operation.
type Error struct {
	Op     string // what was attempted ("list", "copy", …)
	Kind   ErrKind
	Msg    string // rclone's message, shortened
	Hint   string // what the user can do ("" = nothing specific)
	Remote string
	Code   int // rclone's exit status (0 = not from rclone)
}

func (e *Error) Error() string {
	s := e.Msg
	if e.Hint != "" {
		s += " — " + e.Hint
	}
	return s
}

// Is lets errors.Is(err, context.Canceled) recognise cancellation.
func (e *Error) Is(target error) bool { return target == context.Canceled && e.Kind == KindCanceled }

// KindOf returns the kind of err (KindOther when it is not an *Error).
func KindOf(err error) ErrKind {
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Kind
	}
	if errors.Is(err, context.Canceled) {
		return KindCanceled
	}
	return KindOther
}

// Fatal reports kinds that will make every further call fail as well
// (a transfer of several items stops instead of repeating the error).
func (k ErrKind) Fatal() bool {
	switch k {
	case KindNoRemote, KindConfigLocked, KindAuth, KindNetwork, KindCanceled:
		return true
	}
	return false
}

// explain turns rclone's exit status and messages into an *Error.
func explain(op, remote string, st proc.Status, stderr string) *Error {
	msgs := messages(stderr)
	all := strings.ToLower(strings.Join(msgs, "\n"))
	e := &Error{Op: op, Remote: remote, Code: st.Code, Msg: shorten(lastMessage(msgs))}
	if e.Msg == "" {
		e.Msg = "rclone failed (" + st.String() + ")"
	}
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(all, w) {
				return true
			}
		}
		return false
	}
	reconnect := "rclone config reconnect " + remote + ":"
	if remote == "" {
		reconnect = "rclone config reconnect REMOTE:"
	}
	switch {
	case st.Signal != 0:
		e.Kind = KindCanceled
		e.Msg = "rclone was stopped (" + st.String() + ")"
	case has("unable to decrypt configuration", "rclone_config_pass", "couldn't decrypt configuration"):
		e.Kind = KindConfigLocked
		e.Msg = "rclone's configuration is encrypted"
		e.Hint = "start TwinPane with RCLONE_CONFIG_PASS set in the environment (TwinPane never asks for or stores the password)"
	case has("didn't find section in config file", "couldn't find section in config file", "not found in config file"):
		e.Kind = KindNoRemote
		e.Msg = fmt.Sprintf("the remote %q is not in rclone's configuration", remote)
		e.Hint = "run `rclone config` to add it, then press Ctrl+R"
	case has("invalid_grant", "couldn't fetch token", "token expired", "oauth2: ", "unauthorized", "unauthenticated",
		"401 ", "status code 401", "invalid_client", "authentication failed", "login required", "authorization failed",
		"permission denied (publickey"):
		e.Kind = KindAuth
		if i := strings.Index(e.Msg, " - try refreshing with"); i > 0 {
			e.Msg = e.Msg[:i] // the hint below says the same
		}
		e.Hint = "the login has expired or was refused: run `" + reconnect + "` in a terminal, then try again"
	case has("no such host", "dial tcp", "network is unreachable", "connection refused", "i/o timeout",
		"tls handshake timeout", "connection reset", "context deadline exceeded", "no route to host",
		"temporary failure in name resolution", "server misbehaving"):
		e.Kind = KindNetwork
		e.Hint = "the server could not be reached: check the connection, then try again"
	case has("quota", "storagequotaexceeded", "insufficient storage", "no space left", "insufficientfilepermissions: storage"):
		e.Kind = KindQuota
		e.Hint = "the destination is out of space or over its quota"
	case has("permission denied", "access denied", "forbidden", "insufficientpermissions", "403 ", "status code 403", "read-only file system"):
		e.Kind = KindPermission
	case has("not supported", "can't purge", "can't move", "can't copy", "doesn't support", "not implemented", "operation not permitted on this remote"):
		e.Kind = KindUnsupported
		e.Hint = "this remote does not support the operation"
	case st.Code == 3 || st.Code == 4 || has("directory not found", "object not found", "file not found", "doesn't exist"):
		e.Kind = KindNotFound
	}
	return e
}

// messages extracts rclone's messages from its stderr: the "msg" of JSON
// log lines, or plain log lines without the time stamp. Retry notices
// ("Attempt 1/3 failed …") and statistics are dropped.
func messages(stderr string) []string {
	var out, retries []string
	for _, l := range strings.Split(stderr, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "{") {
			var j struct {
				Level string          `json:"level"`
				Msg   string          `json:"msg"`
				Stats json.RawMessage `json:"stats"`
			}
			if json.Unmarshal([]byte(l), &j) == nil {
				if j.Stats != nil || j.Msg == "" || j.Level == "info" || j.Level == "debug" {
					continue
				}
				l = j.Msg
			}
		} else {
			l = stripLogPrefix(l)
			if strings.HasPrefix(l, "INFO") || strings.HasPrefix(l, "DEBUG") || strings.HasPrefix(l, "Transferred:") ||
				strings.HasPrefix(l, "Elapsed time:") || strings.HasPrefix(l, "Checks:") || strings.HasPrefix(l, "Errors:") {
				continue
			}
			for _, lvl := range []string{"CRITICAL: ", "ERROR : ", "ERROR: ", "NOTICE: ", "Failed to "} {
				if i := strings.Index(l, lvl); i >= 0 && lvl != "Failed to " {
					l = l[i+len(lvl):]
				}
			}
		}
		if strings.HasPrefix(l, "Attempt ") {
			// "Attempt 3/3 failed with 1 errors and: <message>"
			if _, rest, ok := strings.Cut(l, ": "); ok {
				l = rest
			}
			retries = append(retries, l)
			continue
		}
		if strings.Contains(l, "Config file") && strings.Contains(l, "not found - using defaults") {
			continue
		}
		if strings.Contains(l, "shared Google Drive client_id") {
			continue // advice, not the error
		}
		out = append(out, l)
	}
	if len(out) == 0 {
		return retries // the retry notices are all there is
	}
	return out
}

func stripLogPrefix(l string) string {
	// "2026/10/17 12:12:50 CRITICAL: …"
	if len(l) > 20 && l[4] == '/' && l[7] == '/' && l[10] == ' ' && l[13] == ':' {
		return strings.TrimSpace(l[20:])
	}
	return l
}

// lastMessage picks the most useful message: the last "Failed to …" /
// critical one, else the last message.
func lastMessage(msgs []string) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if strings.HasPrefix(m, "Failed to ") {
			// "Failed to lsjson: error in ListJSON: directory not found"
			if _, rest, ok := strings.Cut(m, ": "); ok {
				return cleanMsg(rest)
			}
			return cleanMsg(m)
		}
	}
	if len(msgs) > 0 {
		return cleanMsg(msgs[len(msgs)-1])
	}
	return ""
}

func cleanMsg(m string) string {
	for _, p := range []string{"error in ListJSON: ", "error listing: "} {
		m = strings.TrimPrefix(m, p)
	}
	if strings.HasPrefix(m, "Failed to create file system for ") {
		if _, rest, ok := strings.Cut(m, "\": "); ok {
			m = rest
		}
	}
	return m
}

func shorten(s string) string {
	const max = 240
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

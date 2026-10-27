package activity

import (
	"regexp"
	"strings"
)

// Redaction.
//
// The log must never persist a credential, a token or a secret, whatever route
// it took to get there: a command-line flag, a path that happens to look like
// a query string, a URL with a password in it, an Authorization header, a JSON
// body in an error message, or a line of tool output. Everything written goes
// through Redact (see sanitizeEntry), and command arguments go through
// RedactArgs.
//
// The rules are deliberately blunt: over-redacting a harmless word costs a
// little diagnostic detail, while leaking a token cannot be undone. TwinPane
// itself never passes credentials on a command line (rclone reads its own
// configuration; the environment is never logged), so these rules are defence
// in depth for future flags, unusual paths and tool output.

const redacted = "***"

// secretNameSrc matches the names that mean a credential, with either case and
// with "-" or "_" between words (access_token, access-token, AccessToken).
const secretNameSrc = `pass(?:word|wd|phrase)?|passwd|tokens?|access[_-]?token|refresh[_-]?token|id[_-]?token|client[_-]?secret|secret[_-]?key|access[_-]?key|api[_-]?key|apikey|private[_-]?key|credentials?|authorization|auth[_-]?key|session[_-]?id|cookie|signature|sig|secret|pw`

var (
	// (a) bearer-style credentials: "Bearer eyJ...", "Basic YWxpY2U6..."
	bearerRe = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)("[^"]*"|'[^']*'|\S+)`)

	// (b) key = value and key: value for names that mean a secret, including
	// quoted JSON names ("access_token":"…"). The value ends at a delimiter
	// that commonly follows it: whitespace, a quote, a comma in a JSON body,
	// & or ? in a query string, a bracket.
	kvRe = regexp.MustCompile(`(?i)(^|[\s"'{\[,;&?|=])(` + secretNameSrc + `)["']?(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s"',;&)\]}]+)`)

	// (c) secret command-line flags with their value in the same string:
	// "--password hunter2", "--password=hunter2", "--api-key y".
	flagRe = regexp.MustCompile(`(?i)(^|[\s=])(--?[a-z0-9]*(?:pass(?:word|wd)?|token|secret|credential|apikey|api-key|auth|key)[a-z0-9]*)(=|\s+)("[^"]*"|'[^']*'|[^\s"']+)`)

	// (d) a short -p / -P password flag (7-Zip, sshpass, mysql) with its value
	// attached or separate.
	shortFlagRe = regexp.MustCompile(`(^|[\s])(-[pP])([^\s"'-][^\s"']*)`)

	// (e) secret command-line flag names, for the argument-pair pass.
	secretFlagRe = regexp.MustCompile(`(?i)^--?[a-z0-9]*(?:pass(?:word|wd)?|token|secret|credential|apikey|api-key|auth|key)[a-z0-9]*$`)
	shortPassRe  = regexp.MustCompile(`^(-[pP])(.+)$`)
	shortPass    = regexp.MustCompile(`^(-[pP])$`)

	// (f) a data key that names a credential, so its value is masked even when
	// the value alone looks harmless.
	keyRe = regexp.MustCompile(`(?i)^(?:` + secretNameSrc + `)$`)

	// (e) URL userinfo: https://alice:hunter2@example.com/path
	urlRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)([^/\s@]+)@`)

	// (f) an rclone-style remote with an embedded password, as in
	// "remote,pass=obscured:/path".
	remoteOptRe = regexp.MustCompile(`(?i)(,[a-z_]*pass[a-z_]*=)([^,:]+)`)
)

// Redact replaces credentials in s with "***". It never returns an error and
// never panics; an empty string stays empty.
func Redact(s string) string {
	if s == "" || !mayContainSecret(s) {
		return s
	}
	out := bearerRe.ReplaceAllString(s, "$1$2"+redacted)
	out = kvRe.ReplaceAllString(out, "$1$2$3"+redacted)
	out = flagRe.ReplaceAllString(out, "$1$2$3"+redacted)
	out = shortFlagRe.ReplaceAllString(out, "$1$2"+redacted)
	out = urlRe.ReplaceAllString(out, "$1"+redacted+"@")
	out = remoteOptRe.ReplaceAllString(out, "$1"+redacted)
	return out
}

// RedactArgs redacts a command's argument list. A secret flag that takes its
// value in the next argument is handled as a pair, because redacting each
// argument on its own would leave that value in place. The executable
// (argv[0]) is kept: it is what makes the entry useful.
func RedactArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = Redact(a)
	}
	for i, a := range args {
		if name, ok := flagName(a); ok && isSecretFlag(name) {
			// "--flag=value" or "-pVALUE": the value is inside this argument.
			if strings.Contains(a, "=") || shortPassRe.MatchString(a) {
				out[i] = flagWithMask(a)
			} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				// "--flag value": the value is the next argument.
				out[i+1] = redacted
			}
			continue
		}
		if shortPass.MatchString(a) && i+1 < len(args) {
			out[i+1] = redacted
		}
	}
	return out
}

// flagName splits a "--flag=value" argument into its name, and reports whether
// the argument looks like a long flag at all.
func flagName(a string) (string, bool) {
	if len(a) < 3 || a[0] != '-' || a[1] != '-' {
		return "", false
	}
	name := a
	if i := strings.IndexByte(name, '='); i >= 0 {
		name = name[:i]
	}
	return name, true
}

// isSecretFlag reports whether a long flag name means a credential.
func isSecretFlag(name string) bool {
	return secretFlagRe.MatchString(name)
}

// flagWithMask replaces the value attached to a secret flag.
func flagWithMask(a string) string {
	if i := strings.IndexByte(a, '='); i >= 0 {
		return a[:i+1] + redacted
	}
	if m := shortPassRe.FindStringSubmatch(a); m != nil {
		return m[1] + redacted
	}
	return redacted
}

// IsSecretKeyName reports whether a data key names a credential, so its value
// is masked even when the value alone looks harmless (an account ID, say).
func IsSecretKeyName(k string) bool {
	return k != "" && keyRe.MatchString(k)
}

// mayContainSecret is a cheap pre-check so ordinary messages (the vast
// majority) do not run four regexes.
func mayContainSecret(s string) bool {
	if len(s) < 5 {
		return false
	}
	lower := strings.ToLower(s)
	for _, mark := range secretMarks {
		if strings.Contains(lower, mark) {
			return true
		}
	}
	return strings.Contains(s, "://") && strings.Contains(s, "@")
}

// secretMarks are the substrings that make a full redaction pass worthwhile.
var secretMarks = []string{
	"pass", "token", "secret", "key", "auth", "credential", "cookie",
	"sig", "session", "bearer", "basic ", "://", "pw", " -p", "\t-p",
}

// ContainsSecret reports whether s holds something the redactor would have to
// remove, i.e. whether Redact would change it. The tests use it to prove a
// string is clean after redaction; callers may use it to drop a line they
// cannot safely store.
func ContainsSecret(s string) bool {
	if s == "" {
		return false
	}
	return Redact(s) != s
}

#!/usr/bin/env python3
"""ptyterm.py - drive a TUI on a raw pseudo-terminal like a real terminal emulator.

Unlike tmux, which answers terminal queries itself, this harness answers the
queries a modern terminal (xterm/kitty/foot style) answers - DA1/DA2, XTVERSION,
OSC 10/11 colour queries, XTGETTCAP, DECRQM, DECRQSS, CPR, kitty keyboard
query, window-size reports - optionally after an artificial latency (to mimic
SSH or a slow terminal). It runs an interactive bash with job control, so if
the program under test is stopped or dies without restoring the terminal you
see exactly what a user would: the shell prompt on top of the TUI.

Usage:
  ptyterm.py [--latency MS] [--cols N] [--rows N] [--log FILE] [--env K=V ...] -- STEP ...
Steps (executed in order):
  wait:SECONDS        sleep while pumping output
  type:TEXT           send TEXT (escapes: \\r \\n \\e \\t \\xHH)
  screen:LABEL        print the emulated screen under a label
  state:LABEL         print shell/child state (process tree, tty foreground group, termios flags)
  expect:REGEX        fail (exit 1) unless the screen matches REGEX
  reject:REGEX        fail (exit 1) if the screen matches REGEX
  sh:COMMAND          run a shell command outside the terminal (e.g. kill the editor)
Needs pyte (pip install pyte).
"""
import argparse, codecs, fcntl, os, pty, re, select, signal, struct, subprocess, sys, termios, time

try:
    import pyte
except ImportError:
    sys.exit("ptyterm.py needs pyte: pip install pyte")


class Term:
    def __init__(self, cols, rows, latency, log, env):
        self.cols, self.rows, self.latency = cols, rows, latency / 1000.0
        self.screen = pyte.Screen(cols, rows)
        self.stream = pyte.ByteStream(self.screen)
        self.log = open(log, "wb") if log else None
        self.pending = []  # (due_time, bytes)
        self.buf = b""     # unparsed tail for query detection
        self.queries = []
        self.t0 = time.time()
        pid, fd = pty.fork()
        if pid == 0:
            os.environ.clear()
            os.environ.update(env)
            os.execvp("bash", ["bash", "--norc", "--noprofile", "-i"])
        self.pid, self.fd = pid, fd
        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))

    # ---- query responder -------------------------------------------------
    QUERY = re.compile(
        rb"\x1b\[(?P<da>[>=]?0?)c"                      # DA1/DA2/DA3
        rb"|\x1b\[>0?q"                                   # XTVERSION
        rb"|\x1b\](?P<osc>1[01]|4;\d+);\?(?:\x07|\x1b\\)"  # OSC 10/11/4 colour query
        rb"|\x1bP\+q(?P<cap>[0-9A-Fa-f;]*)\x1b\\"          # XTGETTCAP
        rb"|\x1bP\$q(?P<rqss>[^\x1b]*)\x1b\\"              # DECRQSS
        rb"|\x1b\[\?(?P<rqm>\d+)\$p"                      # DECRQM (private)
        rb"|\x1b\[6n"                                     # CPR
        rb"|\x1b\[\?u"                                    # kitty keyboard query
        rb"|\x1b\[(?P<wt>1[468])t"                        # window size reports
    )

    def respond(self, m):
        g = m.group(0)
        if m.group("da") is not None:
            d = m.group("da")
            return b"\x1b[>41;390;0c" if d.startswith(b">") else (b"" if d.startswith(b"=") else b"\x1b[?62;22;52c")
        if g.startswith(b"\x1b[>") and g.endswith(b"q"):
            return b"\x1bP>|xterm(390)\x1b\\"
        if m.group("osc") is not None:
            o = m.group("osc")
            term = b"\x07" if g.endswith(b"\x07") else b"\x1b\\"  # xterm echoes the query's terminator
            return b"\x1b]" + o + (b";rgb:1e1e/1e1e/2e2e" if o == b"11" else b";rgb:dcdc/dcdc/dcdc") + term
        if m.group("cap") is not None:
            out = b""
            for h in m.group("cap").split(b";"):
                name = bytes.fromhex(h.decode()) if h else b""
                vals = {b"TN": b"xterm-256color", b"Co": b"256", b"RGB": b"8/8/8", b"Tc": b""}
                if name in vals:
                    out += b"\x1bP1+r" + h + b"=" + vals[name].hex().encode() + b"\x1b\\"
                else:
                    out += b"\x1bP0+r" + h + b"\x1b\\"
            return out
        if m.group("rqss") is not None:
            return b"\x1bP1$r0m\x1b\\" if m.group("rqss") == b"m" else b"\x1bP0$r\x1b\\"
        if m.group("rqm") is not None:
            return b"\x1b[?" + m.group("rqm") + b";2$y"
        if g == b"\x1b[6n":
            y, x = self.screen.cursor.y + 1, self.screen.cursor.x + 1
            return b"\x1b[%d;%dR" % (y, x)
        if g == b"\x1b[?u":
            return b"\x1b[?0u"
        if m.group("wt") is not None:
            w = m.group("wt")
            if w == b"18":
                return b"\x1b[8;%d;%dt" % (self.rows, self.cols)
            if w == b"14":
                return b"\x1b[4;%d;%dt" % (self.rows * 20, self.cols * 10)
            return b"\x1b[6;20;10t"
        return b""

    def feed(self, data):
        if self.log:
            self.log.write(b"\n<<%.3f OUT>>" % (time.time() - self.t0) + data)
        self.stream.feed(data)
        self.buf = (self.buf + data)[-4096:]
        pos = 0
        for m in self.QUERY.finditer(self.buf):
            r = self.respond(m)
            self.queries.append(m.group(0))
            if r:
                self.pending.append((time.time() + self.latency, r))
            pos = m.end()
        if pos:
            self.buf = self.buf[pos:]

    def pump(self, seconds):
        end = time.time() + seconds
        while True:
            now = time.time()
            for item in [p for p in self.pending if p[0] <= now]:
                self.pending.remove(item)
                self.write(item[1], tag=b"RESP")
            timeout = max(0.0, min([end - now] + [p[0] - now for p in self.pending]))
            if now >= end and not self.pending:
                return
            if now >= end:
                timeout = min(0.01, timeout)
            r, _, _ = select.select([self.fd], [], [], timeout)
            if r:
                try:
                    data = os.read(self.fd, 65536)
                except OSError:
                    return
                if not data:
                    return
                self.feed(data)
            elif now >= end:
                return

    def write(self, data, tag=b"IN"):
        if self.log:
            self.log.write(b"\n<<%.3f %s>>" % (time.time() - self.t0, tag) + data)
        os.write(self.fd, data)

    def find(self, needle):
        """Column and row (0-based) of the first occurrence of needle.
        Built from pyte's cell buffer so that the index of a match is its
        screen column even after wide characters (which take two cells)."""
        for row in range(self.screen.lines):
            line = self.screen.buffer[row]
            cells = "".join((line[x].data or "\0") for x in range(self.screen.columns))
            col = cells.find(needle)
            if col >= 0:
                return col, row
        return None

    def text(self):
        return "\n".join(line.rstrip() for line in self.screen.display)

    def state(self):
        out = []
        try:
            fg = os.tcgetpgrp(self.fd)
        except OSError as e:
            fg = "?(%s)" % e
        attrs = termios.tcgetattr(self.fd)
        lflag = attrs[3]
        flags = [n for n, b in (("ICANON", termios.ICANON), ("ECHO", termios.ECHO), ("ISIG", termios.ISIG)) if lflag & b]
        out.append("tty foreground pgrp=%s  lflag=%s" % (fg, "|".join(flags) or "raw"))
        ps = subprocess.run(["ps", "-o", "pid,pgid,stat,comm", "--ppid", str(self.pid)], capture_output=True, text=True).stdout
        for line in ps.splitlines():
            out.append("  " + line)
            parts = line.split()
            if parts and parts[0].isdigit():
                sub = subprocess.run(["ps", "-o", "pid,pgid,stat,comm", "--no-headers", "--ppid", parts[0]], capture_output=True, text=True).stdout
                for s in sub.splitlines():
                    out.append("      " + s)
        return "\n".join(out)


def unescape(s):
    return codecs.decode(s.replace("\\e", "\\x1b"), "unicode_escape").encode("latin-1")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--latency", type=float, default=0)
    ap.add_argument("--cols", type=int, default=100)
    ap.add_argument("--rows", type=int, default=30)
    ap.add_argument("--log")
    ap.add_argument("--env", action="append", default=[])
    ap.add_argument("steps", nargs="+")
    a = ap.parse_args()
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "TERM": "xterm-256color", "HOME": os.environ.get("HOME", "/tmp"),
           "PS1": "SHELL$ ", "LANG": "C.UTF-8"}
    for kv in a.env:
        k, _, v = kv.partition("=")
        env[k] = v
    t = Term(a.cols, a.rows, a.latency, a.log, env)
    t.pump(0.5)
    rc = 0
    for step in a.steps:
        kind, _, arg = step.partition(":")
        if kind == "wait":
            t.pump(float(arg))
        elif kind == "type":
            t.write(unescape(arg))
            t.pump(0.05)
        elif kind == "screen":
            print("=== screen: %s ===\n%s\n=== end ===" % (arg, t.text()))
        elif kind == "state":
            print("=== state: %s ===\n%s" % (arg, t.state()))
        elif kind == "expect":
            if not re.search(arg, t.text(), re.M):
                print("EXPECT FAILED: %r" % arg); rc = 1
        elif kind == "reject":
            if re.search(arg, t.text(), re.M):
                print("REJECT FAILED: %r" % arg); rc = 1
        elif kind in ("click", "dclick", "wheel"):
            # Mouse/touch input as Termux sends it with mouse reporting on
            # (SGR encoding): a tap is a left press + release at the cell,
            # a swipe is wheel events (64 up / 65 down) at its start cell.
            # The target is the first occurrence of TEXT on the screen.
            n = 1
            text = arg
            if kind == "wheel":
                num, _, text = arg.partition(":")
                n = int(num)
            pos = t.find(text)
            if pos is None:
                print("CLICK TARGET NOT FOUND: %r" % text); rc = 1
                continue
            x, y = pos[0] + 1 + min(len(text) - 1, 1), pos[1] + 1
            if kind == "wheel":
                code = 65 if n > 0 else 64
                for _ in range(abs(n)):
                    t.write(b"\x1b[<%d;%d;%dM" % (code, x, y), b"MOUSE")
                    t.pump(0.02)
            else:
                for i in range(2 if kind == "dclick" else 1):
                    t.write(b"\x1b[<0;%d;%dM\x1b[<0;%d;%dm" % (x, y, x, y), b"MOUSE")
                    t.pump(0.12)
        elif kind == "clickat":
            # tap at 1-based column,row (for targets without unique text)
            x, y = (int(v) for v in arg.split(","))
            t.write(b"\x1b[<0;%d;%dM\x1b[<0;%d;%dm" % (x, y, x, y), b"MOUSE")
            t.pump(0.12)
        elif kind in ("mode", "nomode"):
            # DEC private modes as tracked by pyte (stored shifted by 5)
            on = (int(arg) << 5) in t.screen.mode
            if on != (kind == "mode"):
                print("MODE CHECK FAILED: %s %s (set: %s)" % (kind, arg, on)); rc = 1
        elif kind == "sh":
            subprocess.run(arg, shell=True)
            t.pump(0.05)
        else:
            sys.exit("unknown step " + step)
    print("queries answered: %d (%s)" % (len(t.queries), ", ".join(sorted({repr(q[:12]) for q in t.queries}))[:600]))
    try:
        os.kill(t.pid, signal.SIGKILL)
    except OSError:
        pass
    sys.exit(rc)


if __name__ == "__main__":
    main()

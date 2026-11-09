#!/usr/bin/env python3
"""Record the README demo: run TwinPane in a pty, drive it with a script,
and render the captured screen to an animated GIF.

The demo is generated from the real binary against a throwaway home directory,
so it shows the program as it actually behaves. Nothing here is part of the
build; it only needs Python 3 with `pyte` and `Pillow`:

    pip install pyte pillow
    ./scripts/demo/record_demo.py            # writes assets/demo.gif
    ./scripts/demo/record_demo.py --keep     # keep the captured frames too

The scene is a list of steps: send some keys, wait, and (optionally) show a
caption while the screen settles. Frames are captured on a fixed tick and
identical frames are coalesced, so the GIF stays small.
"""

import argparse
import fcntl
import json
import os
import pty
import select
import shlex
import shutil
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time

import pyte
from PIL import Image, ImageDraw, ImageFont

# ---- look ---------------------------------------------------------------------

COLS, ROWS = 100, 30
FONT_SIZE = 16
CELL_W, CELL_H = 10, 20
PAD = 16          # space around the window
BAR_H = 36        # title bar
CAPTION_H = 40    # caption strip under the terminal

BG = (13, 17, 23)          # page background
WIN_BG = (22, 27, 34)      # window background (terminal default bg)
BAR_BG = (30, 36, 45)
CAPTION_BG = (17, 22, 29)
FG_DEFAULT = (201, 209, 217)
MUTED = (139, 148, 158)
DOT_COLORS = [(255, 95, 86), (255, 189, 46), (39, 201, 63)]

FONT_PATH = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"
FONT_BOLD_PATH = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf"
UI_FONT_PATH = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
UI_FONT_BOLD_PATH = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"

# xterm palette for the 256 colour codes pyte reports.
BASE16 = [
    (0, 0, 0), (205, 49, 49), (13, 188, 121), (229, 229, 16),
    (36, 114, 200), (188, 63, 188), (17, 168, 205), (229, 229, 229),
    (102, 102, 102), (241, 76, 76), (35, 209, 139), (245, 245, 67),
    (59, 142, 234), (214, 112, 214), (41, 184, 219), (255, 255, 255),
]


def palette():
    cols = [(0, 0, 0)] * 256
    for i, c in enumerate(BASE16):
        cols[i] = c
    steps = [0, 95, 135, 175, 215, 255]
    n = 16
    for r in steps:
        for g in steps:
            for b in steps:
                cols[n] = (r, g, b)
                n += 1
    for i in range(24):
        v = 8 + i * 10
        cols[232 + i] = (v, v, v)
    return cols


PALETTE = palette()


def to_rgb(colour, default=FG_DEFAULT):
    """pyte reports "default", a 256-colour index, or truecolor as RRGGBB."""
    if colour is None or colour == "default":
        return default
    if colour.startswith("#"):
        return tuple(int(colour[i:i + 2], 16) for i in (1, 3, 5))
    if len(colour) == 6 and all(c in "0123456789abcdefABCDEF" for c in colour):
        return tuple(int(colour[i:i + 2], 16) for i in (0, 2, 4))
    try:
        return PALETTE[int(colour) % len(PALETTE)]
    except ValueError:
        return default


# ---- the fixture the demo browses ---------------------------------------------


def write_settings(home):
    """Start with the demo project on the left and its parent on the right."""
    cfg = os.path.join(home, ".config", "twinpane")
    os.makedirs(cfg, exist_ok=True)
    with open(os.path.join(cfg, "settings.json"), "w") as f:
        json.dump({
            "startup": {"left": "~/projects/aurora", "right": "~/projects"},
            "sort": {"key": "name", "dirsFirst": True},
            "notice": {"corner": "br", "duration": 3},
            "activity": {"mode": "off"},
        }, f, indent=2)


def build_home(root):
    """A small, believable project tree. Everything the demo touches is here."""

    def write(rel, body=""):
        path = os.path.join(root, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            f.write(body)
        return path

    work = os.path.join(root, "projects", "aurora")

    def filler(rel, head, lines, size=None):
        body = head * 1
        while size and len(body) < size:
            body += lines[len(body) % len(lines)]
        return write(rel, body[:size] if size else body)

    write("projects/aurora/README.md",
          "# aurora\n\nA tiny static-site generator: markdown in, pages out.\n\n"
          "## Build\n\n    go build ./cmd/aurora\n    ./aurora -src content -out public\n\n"
          "## Files\n\n- `content/` — pages in markdown\n- `themes/` — templates\n"
          "- `public/` — the built site (never edit by hand)\n")
    write("projects/aurora/LICENSE",
          "MIT License\n\nCopyright (c) 2026 Aurora contributors\n\n"
          "Permission is hereby granted, free of charge, to any person obtaining a copy\n"
          "of this software and associated documentation files (the \"Software\"), to deal\n"
          "in the Software without restriction...\n")
    write("projects/aurora/notes.txt",
          "todo\n- fade the nav on scroll\n- cache the markdown renderer\n"
          "- ask jo about the feed layout\n- bump the theme to 0.4\n")
    filler("projects/aurora/build.log", "ok  ", ["build ok  ", "wrote 12 pages  "], size=1200)
    write("projects/aurora/screenshot.png", "\x89PNG\r\n\x1a\n" + "\0" * 900)
    write("projects/aurora/.gitignore", "public/\n*.log\n.DS_Store\n")
    write("projects/aurora/.env.example", "AURORA_URL=https://example.com\n")
    write("projects/aurora/src/main.go",
          "package main\n\nimport \"os\"\n\nfunc main() {\n\tif err := run(os.Args[1:]); err != nil {\n"
          "\t\tprintln(err.Error())\n\t\tos.Exit(1)\n\t}\n}\n")
    write("projects/aurora/src/site.go",
          "package main\n\ntype Site struct {\n\tTitle string\n\tPages []Page\n}\n")
    write("projects/aurora/src/render_test.go",
          "package main\n\nimport \"testing\"\n\nfunc TestRender(t *testing.T) {\n\t_ = t\n}\n")
    write("projects/aurora/docs/guide.md", "# guide\n\nHow to add a page.\n")
    write("projects/aurora/docs/changelog.md",
          "# changes\n\n## 0.4.1\n- fixed feed dates\n\n## 0.4.0\n- new theme\n")
    write("projects/backup-2026-10/README.md", "old copy of the site\n")
    write("projects/backup-2026-10/index.html", "<!doctype html>\n<title>aurora</title>\n")

    # Spread the timestamps over a few weeks so the Modified column looks used.
    stamps = {
        "projects/aurora/README.md": (10, 4, 12),
        "projects/aurora/LICENSE": (3, 1, 9),
        "projects/aurora/notes.txt": (0, 6, 21),
        "projects/aurora/build.log": (0, 1, 48),
        "projects/aurora/screenshot.png": (2, 3, 30),
        "projects/aurora/.gitignore": (6, 2, 15),
        "projects/aurora/.env.example": (12, 0, 42),
        "projects/aurora/src": (1, 5, 8),
        "projects/aurora/docs": (8, 2, 19),
        "projects/backup-2026-10": (26, 0, 2),
    }
    now = time.time()
    for rel, (days, hours, mins) in stamps.items():
        when = now - days * 86400 - hours * 3600 - mins * 60
        os.utime(os.path.join(root, rel), (when, when))

    os.makedirs(os.path.join(root, "Downloads"), exist_ok=True)
    return work


# ---- capture -------------------------------------------------------------------


class Session:
    """TwinPane in a pseudo-terminal, with the screen rendered by pyte."""

    def __init__(self, binary, cwd, home, cols=COLS, rows=ROWS):
        self.screen = pyte.Screen(cols, rows)
        self.stream = pyte.ByteStream(self.screen)
        self.cols, self.rows = cols, rows
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        env = {
            "HOME": home,
            "TERM": "xterm-256color",
            "LANG": "C.UTF-8",
            "LC_ALL": "C.UTF-8",
            "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
            "TWINPANE_CONFIG_DIR": os.path.join(home, ".config", "twinpane"),
            "COLORTERM": "truecolor",
            "TZ": "UTC",
        }
        self.proc = subprocess.Popen(
            [binary], cwd=cwd, stdin=slave, stdout=slave, stderr=slave,
            env=env, preexec_fn=os.setsid, close_fds=True,
        )
        os.close(slave)
        self.master = master
        self.buf = b""
        self.frames = []  # (raw screen, caption or None)

    def _read(self, seconds):
        """Read whatever the program writes for `seconds`."""
        end = time.time() + seconds
        while True:
            left = end - time.time()
            if left <= 0:
                return
            r, _, _ = select.select([self.master], [], [], min(left, 0.02))
            if not r:
                continue
            try:
                chunk = os.read(self.master, 65536)
            except OSError:
                return
            if not chunk:
                return
            self.stream.feed(chunk)

    def tick(self, seconds, caption=None, every=0.06):
        """Advance time in small steps, recording a frame on each tick."""
        end = time.time() + seconds
        while True:
            self._read(min(every, max(0.0, end - time.time())))
            self.frames.append((self.snapshot(), caption))
            if time.time() >= end:
                return

    def keys(self, text, wait=0.28, caption=None):
        """Send keys one at a time: TwinPane treats a burst as one command."""
        for ch in text:
            os.write(self.master, ch.encode())
            self.tick(0.09, caption, every=0.09)
        self.tick(wait, caption)

    def wait_for(self, needle, timeout=6.0, caption=None):
        """Keep capturing until the screen contains `needle`."""
        end = time.time() + timeout
        while time.time() < end:
            self.tick(0.06, caption, every=0.06)
            text = "\n".join(self.screen.display)
            if needle in text:
                return True
        return False

    def snapshot(self):
        return [[(c.data, c.fg, c.bg, c.bold, c.reverse) for c in
                 self.screen.buffer[y].values()] for y in range(self.rows)]

    def close(self):
        try:
            os.write(self.master, b"q")
            time.sleep(0.2)
        except OSError:
            pass
        try:
            os.killpg(os.getpgid(self.proc.pid), signal.SIGTERM)
        except OSError:
            pass
        try:
            self.proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            self.proc.kill()
        os.close(self.master)


# ---- rendering -----------------------------------------------------------------


class Renderer:
    def __init__(self):
        self.mono = ImageFont.truetype(FONT_PATH, FONT_SIZE)
        self.mono_bold = ImageFont.truetype(FONT_BOLD_PATH, FONT_SIZE)
        self.ui = ImageFont.truetype(UI_FONT_PATH, 15)
        self.ui_bold = ImageFont.truetype(UI_FONT_BOLD_PATH, 15)
        self.ui_small = ImageFont.truetype(UI_FONT_PATH, 12)
        self.width = COLS * CELL_W + PAD * 2
        self.height = BAR_H + ROWS * CELL_H + CAPTION_H + PAD * 2
        self.cache = {}

    def text(self, colour):
        return self.cache.setdefault(colour, colour)

    def frame(self, cells, caption):
        img = Image.new("RGB", (self.width, self.height), BG)
        d = ImageDraw.Draw(img)
        # window
        x0, y0 = PAD, PAD
        w = COLS * CELL_W
        h = BAR_H + ROWS * CELL_H + CAPTION_H
        d.rounded_rectangle([x0, y0, x0 + w, y0 + h], radius=10, fill=WIN_BG)
        # title bar
        d.rounded_rectangle([x0, y0, x0 + w, y0 + BAR_H], radius=10, fill=BAR_BG)
        d.rectangle([x0, y0 + BAR_H - 10, x0 + w, y0 + BAR_H], fill=BAR_BG)
        for i, col in enumerate(DOT_COLORS):
            cx = x0 + 20 + i * 20
            d.ellipse([cx - 6, y0 + BAR_H // 2 - 6, cx + 6, y0 + BAR_H // 2 + 6], fill=col)
        title = "twinpane — ~/projects/aurora"
        tw = d.textlength(title, font=self.ui)
        d.text((x0 + w / 2 - tw / 2, y0 + BAR_H / 2 - 8), title, font=self.ui,
               fill=MUTED)

        # terminal grid
        top = y0 + BAR_H
        for row, line in enumerate(cells):
            y = top + row * CELL_H
            x = x0
            run_start = 0
            run_key = (line[0][1], line[0][2], line[0][3], line[0][4])
            runs = []
            for col in range(1, COLS):
                key = (line[col][1], line[col][2], line[col][3], line[col][4])
                if key != run_key:
                    runs.append((run_start, col, run_key))
                    run_start, run_key = col, key
            runs.append((run_start, COLS, run_key))
            for start, end, (fg, bg, bold, reverse) in runs:
                fg_rgb = to_rgb(fg)
                bg_rgb = to_rgb(bg, default=WIN_BG)
                if reverse:
                    fg_rgb, bg_rgb = bg_rgb if bg != "default" else WIN_BG, fg_rgb
                if bg_rgb != WIN_BG:
                    d.rectangle([x + start * CELL_W, y,
                                 x + end * CELL_W - 1, y + CELL_H - 1], fill=bg_rgb)
                text = "".join(cells[row][c][0] for c in range(start, end))
                if text.strip():
                    d.text((x + start * CELL_W, y + 1), text,
                           font=self.mono_bold if bold else self.mono, fill=fg_rgb)

        # caption strip
        cy = y0 + BAR_H + ROWS * CELL_H
        d.rectangle([x0 + 1, cy, x0 + w - 1, cy + CAPTION_H - 1], fill=CAPTION_BG)
        label = caption or ""
        if label:
            d.text((x0 + 22, cy + CAPTION_H / 2 - 8), label, font=self.ui, fill=(170, 185, 200))
        # a small mark on the right, so the strip does not look accidental
        d.text((x0 + w - 92, cy + CAPTION_H / 2 - 8), "twinpane", font=self.ui_bold,
               fill=(70, 85, 105))
        return img


def build_gif(frames, renderer, path, tick):
    """Coalesce identical frames, render them, and save a looping GIF."""
    out = []
    last = None
    for cells, caption in frames:
        key = (tuple(tuple(c[0] for c in row) for row in cells),
               tuple(tuple(c[1:] for c in row) for row in cells),
               caption)
        if out and out[-1][0] == key:
            out[-1][1] += tick
            continue
        out.append([key, tick, cells, caption])
    # drop frames shorter than a blink, merging them into the next
    merged = []
    for key, dur, cells, caption in out:
        if merged and dur < 0.05:
            merged[-1][1] += dur
            continue
        merged.append([key, dur, cells, caption])

    images, durations = [], []
    for _, dur, cells, caption in merged:
        images.append(renderer.frame(cells, caption))
        durations.append(max(20, int(dur * 1000)))
    # split the palette per frame but keep a fixed one so colours do not shift
    palette_img = images[0].quantize(colors=128, method=Image.MEDIANCUT, dither=Image.NONE)
    quantized = [im.quantize(palette=palette_img, dither=Image.NONE) for im in images]
    quantized[0].save(path, save_all=True, append_images=quantized[1:], loop=0,
                      duration=durations, optimize=True, disposal=1)
    return len(quantized), sum(durations) / 1000.0


# ---- the scene -----------------------------------------------------------------


def scene(s):
    """Drive the demo. Each step is timed so the GIF reads well.

    The right panel starts in ~/projects (the parent of the left panel's
    folder), so a copy has somewhere real to go.
    """

    def show(text):
        return text

    # 1. two panels
    s.tick(0.9, show("Two panels, one cursor. Tab switches sides."))
    s.keys("j", 0.25, show("Two panels, one cursor. Tab switches sides."))
    s.keys("j", 0.25)
    s.keys("j", 0.35, show("Arrow keys or j/k move; the footer follows the cursor."))
    s.tick(0.5, show("Arrow keys or j/k move; the footer follows the cursor."))

    # 2. sort, to show an instant change of view
    s.keys("s", 0.5, show("s cycles the sort: name, size, time, type."))
    s.keys("s", 0.9, show("s cycles the sort: name, size, time, type."))
    s.keys("s", 0.6)
    s.keys("s", 0.5)

    # 3. select and copy into the other panel
    s.keys(" ", 0.3, show("Space marks items; c copies them to the other panel."))
    s.keys(" ", 0.45)
    s.keys("c", 0.5, show("Space marks items; c copies them to the other panel."))
    s.tick(0.5, show("It asks first — nothing happens by surprise."))
    s.keys("\r", 0.3, show("It asks first — nothing happens by surprise."))
    s.tick(1.0, show("Progress runs in the background; Esc would cancel it."))
    s.tick(0.9, show("One outcome, one notification."))

    # 4. delete to the trash, then put it back
    s.keys("k", 0.3, show("Delete moves the item into TwinPane's own trash."))
    s.keys("d", 0.6, show("Delete moves the item into TwinPane's own trash."))
    s.keys("y", 0.45, show("Recoverable: it is moved, not erased."))
    s.tick(0.9, show("Recoverable: it is moved, not erased."))
    s.keys("T", 0.9, show("T opens the trash: restore, delete for good, or empty."))
    s.keys("r", 0.7, show("r puts it back exactly where it came from."))
    s.tick(0.9, show("r puts it back exactly where it came from."))
    s.keys("\x1b", 0.35)

    # 5. hidden files
    s.keys("h", 0.8, show("h shows hidden files; the header says which mode you are in."))
    s.tick(0.7, show("h shows hidden files; the header says which mode you are in."))
    s.keys("h", 0.5)

    # 6. live search
    s.keys("/", 0.45, show("/ finds files as you type, inside the whole folder."))
    for ch in "src":
        s.keys(ch, 0.25)
    s.tick(1.1, show("/ finds files as you type, inside the whole folder."))
    s.keys("\x1b", 0.5)

    # 7. close on a calm frame
    s.tick(1.4, show("Every key is listed in the help (F1) and can be rebound."))


def inside_namespace():
    """Re-run inside a mount namespace with /home bind-mounted to a temp dir.

    The demo then looks like a real home folder ("~/projects/aurora", and
    notifications naming /home/me/...), which a fresh mkdtemp path does not.
    Only Linux with user namespaces can do this; everywhere else the demo runs
    with an ordinary temporary HOME.
    """
    if os.environ.get("TWINPANE_DEMO_NS") == "1":
        return False
    if not shutil.which("unshare"):
        return False
    try:
        subprocess.run(["unshare", "-m", "-r", "true"], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except (subprocess.CalledProcessError, OSError):
        return False
    base = tempfile.mkdtemp(prefix="twinpane-demo-")
    os.makedirs(os.path.join(base, "me"), exist_ok=True)
    args = " ".join(shlex.quote(a) for a in sys.argv[1:])
    # /home disappears behind the mount, so run a copy of this script from /tmp.
    script = os.path.join(tempfile.mkdtemp(prefix="twinpane-demo-run-"), "record_demo.py")
    shutil.copyfile(os.path.abspath(__file__), script)
    inner = (f"mkdir -p /home && mount --bind {shlex.quote(base)} /home && "
             f"TWINPANE_DEMO_NS=1 HOME=/home/me exec python3 "
             f"{shlex.quote(script)} {args}")
    env = dict(os.environ, TWINPANE_DEMO_HOME=os.path.join(base, "me"))
    rc = subprocess.call(["unshare", "-m", "-r", "--", "bash", "-c", inner], env=env)
    shutil.rmtree(base, ignore_errors=True)
    sys.exit(rc)


def main():
    inside_namespace()
    ap = argparse.ArgumentParser()
    ap.add_argument("--bin", default="./twinpane")
    ap.add_argument("--out", default="assets/demo.gif")
    ap.add_argument("--keep", action="store_true", help="keep the captured frames")
    ap.add_argument("--tick", type=float, default=0.06)
    args = ap.parse_args()

    binary = os.path.abspath(args.bin)
    if not os.path.exists(binary):
        sys.exit(f"build it first: make build ({binary} not found)")

    if os.environ.get("TWINPANE_DEMO_NS") == "1" and os.environ.get("HOME"):
        home = os.environ["HOME"]
    else:
        home = tempfile.mkdtemp(prefix="twinpane-demo-")
    try:
        work = build_home(home)
        write_settings(home)
        s = Session(binary, work, home)
        # startup: both panels open, left in the project, right in the parent
        s.tick(1.6, "TwinPane: a dual-pane file manager for the terminal.")
        try:
            scene(s)
        finally:
            s.close()
        renderer = Renderer()
        os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
        n, secs = build_gif(s.frames, renderer, args.out, args.tick)
        size = os.path.getsize(args.out) / 1e6
        print(f"{args.out}: {n} frames, {secs:.1f}s, {size:.2f} MB, "
              f"{renderer.width}x{renderer.height}")
        if args.keep:
            keep = os.path.join(os.path.dirname(os.path.abspath(args.out)), "demo-frames")
            shutil.rmtree(keep, ignore_errors=True)
            os.makedirs(keep)
            step = max(1, len(s.frames) // 40)
            for i, (cells, caption) in enumerate(s.frames[::step]):
                renderer.frame(cells, caption).save(os.path.join(keep, f"{i:03d}.png"))
            print(f"frames in {keep}")
    finally:
        if os.environ.get("TWINPANE_DEMO_NS") != "1":
            shutil.rmtree(home, ignore_errors=True)


if __name__ == "__main__":
    main()

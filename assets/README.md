# TwinPane assets

Two side-by-side panels, the left one active. Only the README uses these
files; the program doesn't need them.

| File | Use |
|---|---|
| `twinpane-icon.svg` | app icon: panels on a navy rounded square (vector source) |
| `twinpane-icon-1024.png`, `-512.png`, `-256.png` | icon exports |
| `twinpane-mark.svg`, `twinpane-mark-512.png` | panels only, transparent background, for light and dark backgrounds |
| `twinpane-wordmark-light.svg` / `.png` | mark + "TwinPane" for **light** backgrounds (dark text), transparent |
| `twinpane-wordmark-dark.svg` / `.png` | mark + "TwinPane" for **dark** backgrounds (light text), transparent |
| `demo.gif` | the demo at the top of the README, recorded from the real binary |

Colours: navy `#0B1D30`, cyan `#22D3EE`, blue `#3B82F6`. In the wordmark,
"Pane" is `#0284C7` on light backgrounds and `#22D3EE` on dark ones.

The wordmark is set in Inter Bold (SIL Open Font License), converted to
outlines, so the SVGs don't need the font. `make_assets.py` regenerates all
files; it is only needed when the design changes (see the script header for
its requirements).

`demo.gif` is not drawn by hand: `scripts/demo/record_demo.py` runs the built
binary in a pseudo-terminal against a throwaway home folder, captures the
screen and writes the GIF (`--keep` also saves the frames). Re-record it after
a visible change with:

    pip install pyte pillow
    go build -o /tmp/twinpane ./cmd/twinpane
    python3 scripts/demo/record_demo.py --bin /tmp/twinpane --out assets/demo.gif

The logo is part of TwinPane and covered by the project's
[MIT License](../LICENSE).

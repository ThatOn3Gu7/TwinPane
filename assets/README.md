# TwinPane logo

Two side-by-side panels, the left one active. Only the README uses these
files; the program doesn't need them.

| File | Use |
|---|---|
| `twinpane-icon.svg` | app icon: panels on a navy rounded square (vector source) |
| `twinpane-icon-1024.png`, `-512.png`, `-256.png` | icon exports |
| `twinpane-mark.svg`, `twinpane-mark-512.png` | panels only, transparent background, for light and dark backgrounds |
| `twinpane-wordmark-light.svg` / `.png` | mark + "TwinPane" for **light** backgrounds (dark text), transparent |
| `twinpane-wordmark-dark.svg` / `.png` | mark + "TwinPane" for **dark** backgrounds (light text), transparent |

Colours: navy `#0B1D30`, cyan `#22D3EE`, blue `#3B82F6`. In the wordmark,
"Pane" is `#0284C7` on light backgrounds and `#22D3EE` on dark ones.

The wordmark is set in Inter Bold (SIL Open Font License), converted to
outlines, so the SVGs don't need the font. `make_assets.py` regenerates all
files; it is only needed when the design changes (see the script header for
its requirements).

The logo is part of TwinPane and covered by the project's
[MIT License](../LICENSE).

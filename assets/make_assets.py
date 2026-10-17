#!/usr/bin/env python3
"""Regenerate the TwinPane logo files in assets/.

The SVG files in assets/ are the vector sources; this script writes them and
renders the PNG exports. It is only needed when the design changes; nothing in
the TwinPane program uses these files.

Needs: python3 with fonttools and uharfbuzz (pip install fonttools uharfbuzz),
rsvg-convert (librsvg), and the Inter font (SIL Open Font License, not
included here):

    python3 assets/make_assets.py --font /path/to/Inter-Bold.ttf

The wordmark text is converted to outlines, so the SVGs need no font to view.
"""
import argparse
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

# Palette: restrained blue/cyan that works on light and dark pages.
NAVY = "#0B1D30"  # tile and file rows
EDGE = "#24476B"  # tile outline (keeps the tile visible on dark pages)
CYAN = "#22D3EE"  # active (left) panel and prompt
BLUE = "#3B82F6"  # passive (right) panel
INK_LIGHT_BG = "#0B1D30"  # wordmark text on light pages
INK_DARK_BG = "#F1F5F9"  # wordmark text on dark pages
ACCENT_LIGHT_BG = "#0284C7"  # "Pane" on light pages
ACCENT_DARK_BG = "#22D3EE"  # "Pane" on dark pages


def panels(tile=True):
    """The icon artwork on a 512x512 grid, as a list of SVG elements."""
    out = []
    if tile:
        out.append(f'<rect x="16" y="16" width="480" height="480" rx="112" fill="{NAVY}"/>')
        out.append(f'<rect x="18" y="18" width="476" height="476" rx="110" fill="none" '
                   f'stroke="{EDGE}" stroke-width="4"/>')
    # Two side-by-side panels. The left one is the active panel: it carries
    # the cursor row; the right one is the passive panel.
    out.append(f'<rect x="88" y="96" width="160" height="256" rx="24" fill="{CYAN}"/>')
    out.append(f'<rect x="264" y="96" width="160" height="256" rx="24" fill="{BLUE}"/>')
    # Title bars (path line) in each panel.
    for x in (88, 264):
        out.append(f'<path d="M{x + 24} 96h112a24 24 0 0 1 24 24v20h-160v-20a24 24 0 0 1 24-24z" '
                   f'fill="{NAVY}" fill-opacity="0.22"/>')
    # File rows: left panel with the selected (cursor) row as a full-width bar.
    rows_left = [(168, 96), (200, 128, True), (232, 80), (264, 112), (296, 64)]
    for r in rows_left:
        y, w = r[0], r[1]
        if len(r) == 3:
            out.append(f'<rect x="100" y="{y - 6}" width="136" height="28" rx="8" fill="{NAVY}"/>')
            out.append(f'<rect x="112" y="{y + 4}" width="88" height="8" rx="4" fill="{CYAN}"/>')
        else:
            out.append(f'<rect x="112" y="{y + 4}" width="{w}" height="8" rx="4" fill="{NAVY}"/>')
    for y, w in [(168, 72), (200, 112), (232, 96), (264, 56)]:
        out.append(f'<rect x="288" y="{y + 4}" width="{w}" height="8" rx="4" fill="{NAVY}"/>')
    # Command line under the panels: prompt chevron and block cursor.
    color = CYAN if tile else BLUE
    out.append(f'<path d="M96 380l28 22-28 22" fill="none" stroke="{color}" stroke-width="14" '
               f'stroke-linecap="round" stroke-linejoin="round"/>')
    out.append(f'<rect x="144" y="392" width="40" height="20" rx="4" fill="{color}"/>')
    return out


def svg(width, height, body, title):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
            f'viewBox="0 0 {width} {height}" role="img" aria-label="{title}">\n'
            f'<title>{title}</title>\n' + "\n".join(body) + "\n</svg>\n")


def text_paths(font_path, text, size):
    """Outline `text` (kerned by HarfBuzz). Returns ([(path_d, x_end)], width, ascent, descent)."""
    import uharfbuzz as hb
    from fontTools.pens.svgPathPen import SVGPathPen
    from fontTools.pens.transformPen import TransformPen
    from fontTools.ttLib import TTFont

    blob = hb.Blob.from_file_path(font_path)
    face = hb.Face(blob)
    font = hb.Font(face)
    buf = hb.Buffer()
    buf.add_str(text)
    buf.guess_segment_properties()
    hb.shape(font, buf, {"kern": True, "liga": False})
    tt = TTFont(font_path)
    upem = tt["head"].unitsPerEm
    scale = size / upem
    glyphs = tt.getGlyphSet()
    order = tt.getGlyphOrder()
    x = 0
    parts = []
    for info, pos in zip(buf.glyph_infos, buf.glyph_positions):
        name = order[info.codepoint]
        pen = SVGPathPen(glyphs)
        # Font units are y-up; SVG is y-down. Baseline at y=0.
        tp = TransformPen(pen, (scale, 0, 0, -scale, (x + pos.x_offset) * scale, -pos.y_offset * scale))
        glyphs[name].draw(tp)
        parts.append(pen.getCommands())
        x += pos.x_advance
    hhea = tt["OS/2"]
    return parts, x * scale, hhea.sCapHeight * scale


def wordmark(font_path, ink, accent, tile=True):
    """Icon on the left, "TwinPane" on the right; "Twin" in ink, "Pane" in accent."""
    size = 168
    parts, width, cap = text_paths(font_path, "TwinPane", size)
    icon = 256  # icon drawn at half scale
    gap = 40
    pad = 16
    h = icon + 2 * pad
    baseline = pad + icon / 2 + cap / 2
    w = int(pad + icon + gap + width + pad + 0.5)
    body = [f'<g transform="translate({pad} {pad}) scale(0.5)">'] + panels(tile) + ["</g>"]
    tx = pad + icon + gap
    body.append(f'<g transform="translate({tx:.2f} {baseline:.2f})">')
    body.append(f'<path fill="{ink}" d="{"".join(parts[:4])}"/>')
    body.append(f'<path fill="{accent}" d="{"".join(parts[4:])}"/>')
    body.append("</g>")
    return w, h, body


def render(svg_path, png_path, width, height=None):
    cmd = ["rsvg-convert", "-w", str(width)]
    if height:
        cmd += ["-h", str(height)]
    cmd += ["-o", png_path, svg_path]
    subprocess.run(cmd, check=True)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--font", required=True, help="path to Inter-Bold.ttf (or another bold sans)")
    args = ap.parse_args()
    if not os.path.isfile(args.font):
        sys.exit(f"font not found: {args.font}")

    def write(name, content):
        with open(os.path.join(HERE, name), "w", encoding="utf-8") as f:
            f.write(content)

    write("twinpane-icon.svg", svg(512, 512, panels(True), "TwinPane"))
    write("twinpane-mark.svg", svg(512, 512, panels(False), "TwinPane"))
    for variant, ink, accent in (("light", INK_LIGHT_BG, ACCENT_LIGHT_BG), ("dark", INK_DARK_BG, ACCENT_DARK_BG)):
        w, h, body = wordmark(args.font, ink, accent)
        write(f"twinpane-wordmark-{variant}.svg", svg(w, h, body, "TwinPane"))

    p = lambda n: os.path.join(HERE, n)  # noqa: E731
    render(p("twinpane-icon.svg"), p("twinpane-icon-1024.png"), 1024)
    render(p("twinpane-icon.svg"), p("twinpane-icon-512.png"), 512)
    render(p("twinpane-icon.svg"), p("twinpane-icon-256.png"), 256)
    render(p("twinpane-mark.svg"), p("twinpane-mark-512.png"), 512)
    for variant in ("light", "dark"):
        render(p(f"twinpane-wordmark-{variant}.svg"), p(f"twinpane-wordmark-{variant}.png"), 1200)
    print("assets written to", HERE)


if __name__ == "__main__":
    main()

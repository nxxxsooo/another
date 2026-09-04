#!/usr/bin/env python3
"""Render the TUI goodbye animation to a GIF for the README.

The letterforms, the tear geometry, the caption, and the animation beats are
all read out of internal/tui/logo_face.go and internal/tui/logo.go rather
than restated, so the asset cannot drift from what the binary prints. Only
the two palette colours are named here, and a test in the tui package pins
them to Charmtone.

Requires Pillow. Writes docs/assets/tui-goodbye.gif and, for readers who ask
for reduced motion, the settled frame as docs/assets/tui-goodbye-static.png.
"""

from __future__ import annotations

import re
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
FACE_GO = ROOT / "internal" / "tui" / "logo_face.go"
LOGO_GO = ROOT / "internal" / "tui" / "logo.go"
OUTPUT = ROOT / "docs" / "assets" / "tui-goodbye.gif"
STILL = ROOT / "docs" / "assets" / "tui-goodbye-static.png"

CELL_W, CELL_H, PAD = 8, 16, 18
SURFACE = (10, 10, 10)
CHARPLE = (107, 80, 255)   # in register
DOLLY = (255, 96, 255)     # displaced
MUTED = (126, 126, 143)

SHADES = ["█", "▓", "▒", "░", " "]
COVER = {"█": 1.0, "▓": 0.66, "▒": 0.40, "░": 0.20}

VERSION = "v0.4.2"


def load_face() -> tuple[list[str], int]:
    src = FACE_GO.read_text(encoding="utf8")
    rows = re.findall(r'^\t"(.*)",$', src, re.MULTILINE)
    width = int(re.search(r"const markWidth = (\d+)", src).group(1))
    if not rows:
        raise SystemExit("no wordmark rows in logo_face.go")
    return rows, width


def load_design() -> dict:
    """Read the tear geometry, caption, timing, and beats out of logo.go."""
    src = LOGO_GO.read_text(encoding="utf8")

    def const_int(name: str) -> int:
        return int(re.search(rf"\b{name}\s*=\s*(\d+)", src).group(1))

    # Skip past the anonymous struct's field list to the literal's body.
    block = re.search(r"var goodbyeScript = \[\]struct \{.*?\}\{\n(.*?)\n\}", src, re.S).group(1)
    beats = [(int(s), float(t)) for s, t in re.findall(r"\{(\d+),\s*([0-9.]+)\}", block)]
    if not beats:
        raise SystemExit("could not read goodbyeScript from logo.go")
    return {
        "seam": const_int("tearSeam"),
        "shift": const_int("tearShift"),
        "tagline": re.search(r'logoTagline\s*=\s*"(.*?)"', src).group(1),
        "frame_ms": const_int("frameDelay"),
        "beats": beats,
    }


def ink_rule(width: int, reverse: bool) -> str:
    row = [SHADES[i * len(SHADES) // width] for i in range(width)]
    return "".join(row[::-1] if reverse else row)


def lerp(a, b, t):
    return tuple(round(a[i] + (b[i] - a[i]) * t) for i in range(3))


def frame(rows, width, slip, tension, font, design):
    displaced = lerp(CHARPLE, DOLLY, tension)
    lines = [(ink_rule(width, False), displaced)]
    for r, row in enumerate(rows):
        if r < design["seam"]:
            lines.append((" " * slip + row, displaced))
        else:
            lines.append((row, CHARPLE))
    lines.append((ink_rule(width, True), CHARPLE))

    img = Image.new("RGB", (PAD * 2 + width * CELL_W,
                            PAD * 2 + (len(lines) + 1) * CELL_H), SURFACE)
    d = ImageDraw.Draw(img)
    for y, (text, color) in enumerate(lines):
        for x, ch in enumerate(text):
            if ch == " " or x >= width:
                continue
            x0, y0 = PAD + x * CELL_W, PAD + y * CELL_H
            half = CELL_H // 2
            if ch in COVER:
                c = COVER[ch]
                fill = tuple(round(SURFACE[k] + (color[k] - SURFACE[k]) * c) for k in range(3))
                d.rectangle([x0, y0, x0 + CELL_W - 1, y0 + CELL_H - 1], fill=fill)
            elif ch == "▀":
                d.rectangle([x0, y0, x0 + CELL_W - 1, y0 + half - 1], fill=color)
            elif ch == "▄":
                d.rectangle([x0, y0 + half, x0 + CELL_W - 1, y0 + CELL_H - 1], fill=color)
            else:  # full block
                d.rectangle([x0, y0, x0 + CELL_W - 1, y0 + CELL_H - 1], fill=color)
    cy = PAD + len(lines) * CELL_H + 2
    d.text((PAD, cy), design["tagline"], font=font, fill=MUTED)
    d.text((PAD + width * CELL_W - d.textlength(VERSION, font=font), cy),
           VERSION, font=font, fill=MUTED)
    return img


def main() -> None:
    rows, width = load_face()
    design = load_design()
    total = width + design["shift"]
    font = ImageFont.truetype("/System/Library/Fonts/Menlo.ttc", 13)
    frames = [frame(rows, total, slip, tension, font, design)
              for slip, tension in design["beats"]]
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    frames[0].save(OUTPUT, save_all=True, append_images=frames[1:],
                   duration=design["frame_ms"], loop=0, optimize=True)
    # The reduced-motion still is the settled frame, the same one the terminal
    # keeps in its scrollback.
    frames[-1].save(STILL)
    print(f"{OUTPUT.relative_to(ROOT)}  {frames[0].size[0]}x{frames[0].size[1]}  "
          f"{len(frames)} frames @ {design['frame_ms']}ms  "
          f"{OUTPUT.stat().st_size // 1024} KB")
    print(f"{STILL.relative_to(ROOT)}  {STILL.stat().st_size // 1024} KB")


if __name__ == "__main__":
    main()

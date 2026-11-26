#!/usr/bin/env python3
"""Render the TUI goodbye animation to a GIF for the README.

The letterforms, the ghost geometry, the caption, and the animation beats are
all read out of internal/tui/logo_face.go and internal/tui/logo.go rather than
restated. A test in the tui package pins the brand colours to Charmtone.

Requires Pillow. Writes docs/assets/tui-goodbye.gif and, for readers who ask
for reduced motion, the settled frame as docs/assets/tui-goodbye-static.png.
"""

from __future__ import annotations

import re
import subprocess
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
FACE_GO = ROOT / "internal" / "tui" / "logo_face.go"
LOGO_GO = ROOT / "internal" / "tui" / "logo.go"
OUTPUT = ROOT / "docs" / "assets" / "tui-goodbye.gif"
STILL = ROOT / "docs" / "assets" / "tui-goodbye-static.png"

CELL_W, CELL_H, PAD = 8, 16, 18
# How long the looping GIF rests on the final lockup before starting over.
END_HOLD_MS = 1200
# GIF stores a frame delay in centiseconds, and browsers clamp a delay of one
# centisecond to 100ms. A faster script would play correctly in the terminal
# and ten times too slow in the README, so fail loudly instead of shipping it.
MIN_FRAME_MS = 20
SURFACE = (10, 10, 10)
CHARPLE = (107, 80, 255)   # the agent the session came from
JULEP = (0, 255, 178)      # the agent it went to
MUTED = (126, 126, 143)


def load_face() -> tuple[list[str], int]:
    src = FACE_GO.read_text(encoding="utf8")
    rows = re.findall(r'^\t"(.*)",$', src, re.MULTILINE)
    width = int(re.search(r"markWidth\s*=\s*(\d+)", src).group(1))
    if not rows:
        raise SystemExit("no face bitmap in logo_face.go")
    if any(len(r) != width for r in rows):
        raise SystemExit("face bitmap is not rectangular")
    return rows, width


def load_design() -> dict:
    """Read the ghost geometry, caption, timing, and beats out of logo.go."""
    src = LOGO_GO.read_text(encoding="utf8")

    def const_int(name: str) -> int:
        return int(re.search(rf"\b{name}\s*=\s*(\d+)", src).group(1))

    consts = {name: const_int(name) for name in ("ghostShift", "restShift", "restDrop")}

    bands = re.search(r"var tearBands = \[\.\.\.\]int\{(.*?)\}", src).group(1)
    tear_bands = [int(v) for v in re.findall(r"[+-]?\d+", bands)]

    # Skip past the anonymous struct's field list to the literal's body.
    block = re.search(r"var goodbyeScript = \[\]struct \{.*?\}\{\n(.*?)\n\}", src, re.S).group(1)
    beats = []
    for fields in re.findall(r"\{(\w+),\s*(\w+),\s*(\w+)\}", block):
        beats.append(tuple(consts[v] if v in consts else int(v) for v in fields))
    if not beats:
        raise SystemExit("could not read goodbyeScript from logo.go")

    return {
        "shift": consts["ghostShift"],
        "rest_shift": consts["restShift"],
        "rest_drop": consts["restDrop"],
        "tear_bands": tear_bands,
        "tagline": re.search(r'logoTagline\s*=\s*"(.*?)"', src).group(1),
        "frame_ms": const_int("frameDelay"),
        "beats": beats,
    }


def project_version() -> str:
    """Ask git for the version rather than pinning one that goes stale."""
    try:
        tag = subprocess.run(
            ["git", "-C", str(ROOT), "describe", "--tags", "--abbrev=0"],
            capture_output=True, text=True, check=True,
        ).stdout.strip()
    except (subprocess.CalledProcessError, FileNotFoundError):
        return ""
    return tag if tag.startswith("v") else f"v{tag}"


def pixel_at(rows, width, sub_row, col, gap, drop):
    """Mirror stateAt in logo.go, with the violet face above the mint copy."""
    def ink(y, x):
        return 0 <= y < len(rows) and 0 <= x < width and rows[y][x] == "#"

    if ink(sub_row, col):
        return CHARPLE
    if ink(sub_row - drop, col - gap):
        return JULEP
    return SURFACE


def row_gap(cell_row, gap, tear, design):
    """Mirrors rowGap in logo.go: the tear pulls each row off the frame's gap."""
    bands = design["tear_bands"]
    return max(design["rest_shift"],
               min(gap + tear * bands[cell_row % len(bands)], design["shift"]))


def frame(rows, face_width, total, gap, drop, tear, font, design, version):
    cell_rows = (len(rows) + design["rest_drop"] + 1) // 2

    img = Image.new("RGB", (PAD * 2 + total * CELL_W,
                            PAD * 2 + (cell_rows + 1) * CELL_H), SURFACE)
    d = ImageDraw.Draw(img)
    half = CELL_H // 2

    for sub_row in range(2 * cell_rows):
        g = row_gap(sub_row // 2, gap, tear, design)
        for col in range(total):
            color = pixel_at(rows, face_width, sub_row, col, g, drop)
            if color != SURFACE:
                x0, y0 = PAD + col * CELL_W, PAD + sub_row * half
                d.rectangle([x0, y0, x0 + CELL_W - 1, y0 + half - 1], fill=color)

    cy = PAD + cell_rows * CELL_H + 2
    d.text((PAD, cy), design["tagline"], font=font, fill=MUTED)
    if version:
        d.text((PAD + total * CELL_W - d.textlength(version, font=font), cy),
               version, font=font, fill=MUTED)
    return img


def main() -> None:
    rows, face_width = load_face()
    design = load_design()
    total = face_width + design["shift"]
    version = project_version()
    font = ImageFont.truetype("/System/Library/Fonts/Menlo.ttc", 13)
    frames = [frame(rows, face_width, total, gap, drop, tear, font, design, version)
              for gap, drop, tear in design["beats"]]
    # The script holds nothing at the end, because in a terminal the last frame
    # simply stays there. A GIF loops instead, so the settled mark needs a hold
    # of its own or the thing the animation is about flashes past.
    if design["frame_ms"] < MIN_FRAME_MS:
        raise SystemExit(
            f"frameDelay is {design['frame_ms']}ms; below {MIN_FRAME_MS}ms a GIF "
            "frame rounds to one centisecond and browsers clamp it to 100ms"
        )
    durations = [design["frame_ms"]] * len(frames)
    durations[-1] = END_HOLD_MS
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    frames[0].save(OUTPUT, save_all=True, append_images=frames[1:],
                   duration=durations, loop=0, optimize=True)
    # The reduced-motion still is the settled frame, the same one the terminal
    # keeps in its scrollback.
    frames[-1].save(STILL)
    print(f"{OUTPUT.relative_to(ROOT)}  {frames[0].size[0]}x{frames[0].size[1]}  "
          f"{len(frames)} frames @ {design['frame_ms']}ms "
          f"(+{END_HOLD_MS}ms hold), motion {sum(durations[:-1])}ms, "
          f"{OUTPUT.stat().st_size // 1024} KB")
    print(f"{STILL.relative_to(ROOT)}  {STILL.stat().st_size // 1024} KB")


if __name__ == "__main__":
    main()

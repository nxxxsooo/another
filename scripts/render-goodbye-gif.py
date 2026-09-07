#!/usr/bin/env python3
"""Render the TUI goodbye animation to a GIF for the README.

The letterforms, the ghost geometry, the caption, and the animation beats are
all read out of internal/tui/logo_face.go and internal/tui/logo.go rather than
restated, so the asset cannot drift from what the binary prints. Only the three
palette colours are named here, and a test in the tui package pins the two
brand ones to Charmtone.

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
# How long the looping GIF rests on the merged mark before starting over.
END_HOLD_MS = 1200
# GIF stores a frame delay in centiseconds, and browsers clamp a delay of one
# centisecond to 100ms. A faster script would play correctly in the terminal
# and ten times too slow in the README, so fail loudly instead of shipping it.
MIN_FRAME_MS = 20
SURFACE = (10, 10, 10)
CHARPLE = (107, 80, 255)   # the agent the session came from
JULEP = (0, 255, 178)      # the agent it went to
TEXT = (244, 244, 245)     # the session itself, held by both
MUTED = (126, 126, 143)

# Sub-pixel ownership, mirroring the inkState constants in logo.go.
BARE, SOURCE_ONLY, TARGET_ONLY, SHARED = range(4)


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

    consts = {name: const_int(name) for name in ("ghostShift", "ghostClose")}

    bands = re.search(r"var tearBands = \[\.\.\.\]int\{(.*?)\}", src).group(1)
    tear_bands = [int(v) for v in re.findall(r"[+-]?\d+", bands)]

    # Skip past the anonymous struct's field list to the literal's body.
    block = re.search(r"var goodbyeScript = \[\]struct \{.*?\}\{\n(.*?)\n\}", src, re.S).group(1)
    beats = []
    for gap, merge, tear in re.findall(r"\{(\w+),\s*([0-9.]+),\s*(\d+)\}", block):
        # A beat may name a constant instead of spelling out the number.
        beats.append((consts[gap] if gap in consts else int(gap), float(merge), int(tear)))
    if not beats:
        raise SystemExit("could not read goodbyeScript from logo.go")

    return {
        "shift": consts["ghostShift"],
        "close": consts["ghostClose"],
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


def lerp(a, b, t):
    return tuple(round(a[i] + (b[i] - a[i]) * t) for i in range(3))


def state_at(rows, width, sub_row, col, gap, reserve):
    """Overlay the two copies gap cells apart, centred in the reserved width.

    Mirrors pairOffsets and stateAt in logo.go: the copies close on each other
    symmetrically, so neither one is the stationary reference.
    """
    left = (reserve - gap) // 2

    def ink(c):
        return 0 <= c < width and rows[sub_row][c] == "#"

    src, dst = ink(col - left), ink(col - left - gap)
    if src and dst:
        return SHARED
    if src:
        return SOURCE_ONLY
    if dst:
        return TARGET_ONLY
    return BARE


def row_gap(cell_row, gap, tear, design):
    """Mirrors rowGap in logo.go: the tear pulls each row off the frame's gap."""
    if tear == 0:
        return gap
    bands = design["tear_bands"]
    return max(0, min(gap + tear * bands[cell_row % len(bands)], design["shift"]))


def frame(rows, face_width, total, gap, merge, tear, font, design, version):
    # Mirrors renderFrame in logo.go: the overlap is the session and never
    # moves, while merge walks each agent's own colour toward it.
    palette = {
        SOURCE_ONLY: lerp(CHARPLE, TEXT, merge),
        TARGET_ONLY: lerp(JULEP, TEXT, merge),
        SHARED: TEXT,
    }
    cell_rows = len(rows) // 2

    img = Image.new("RGB", (PAD * 2 + total * CELL_W,
                            PAD * 2 + (cell_rows + 1) * CELL_H), SURFACE)
    d = ImageDraw.Draw(img)
    half = CELL_H // 2

    for row in range(cell_rows):
        g = row_gap(row, gap, tear, design)
        for col in range(total):
            top = state_at(rows, face_width, 2 * row, col, g, design["shift"])
            bottom = state_at(rows, face_width, 2 * row + 1, col, g, design["shift"])
            x0, y0 = PAD + col * CELL_W, PAD + row * CELL_H
            if top != BARE:
                d.rectangle([x0, y0, x0 + CELL_W - 1, y0 + half - 1], fill=palette[top])
            if bottom != BARE:
                d.rectangle([x0, y0 + half, x0 + CELL_W - 1, y0 + CELL_H - 1],
                            fill=palette[bottom])

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
    frames = [frame(rows, face_width, total, gap, merge, tear, font, design, version)
              for gap, merge, tear in design["beats"]]
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

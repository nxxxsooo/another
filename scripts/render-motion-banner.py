#!/usr/bin/env python3
"""Render the animated README banner from the approved static composition.

The static composition and purple/green mark are checked-in masters. This
script only authors the short neon rupture, so reruns never re-typeset or
redesign the approved identity.

Requires Pillow and ffmpeg.
"""

from __future__ import annotations

import argparse
import math
import random
import shutil
import subprocess
import tempfile
from pathlib import Path

from PIL import Image, ImageDraw, ImageEnhance, ImageFilter

ROOT = Path(__file__).resolve().parents[1]
ASSETS = ROOT / "docs" / "assets"
MARK_SOURCE = ASSETS / "another-mark-master.png"
STATIC_SOURCE = ASSETS / "another-motion-static.jpg"
DEFAULT_OUTPUT = ASSETS / "another-motion.gif"

WIDTH, HEIGHT = 1200, 360
FPS = 25
FRAME_COUNT = 60
PURPLE = (107, 80, 255, 255)
GREEN = (0, 245, 180, 255)
PINK = (255, 74, 203, 255)
BLUE = (64, 180, 255, 255)


def prepare_mark() -> Image.Image:
    mark = Image.open(MARK_SOURCE).convert("RGB").resize((286, 286), Image.Resampling.LANCZOS)
    alpha = Image.new("L", mark.size, 255)
    draw = ImageDraw.Draw(alpha)
    for inset in range(18):
        opacity = int(255 * (inset / 18) ** 1.8)
        draw.rectangle((inset, inset, 285 - inset, 285 - inset), outline=opacity, width=1)
    mark.putalpha(alpha)
    return mark


def stable_frame() -> Image.Image:
    frame = Image.open(STATIC_SOURCE).convert("RGBA")
    if frame.size != (WIDTH, HEIGHT):
        raise SystemExit(f"Expected {WIDTH}x{HEIGHT} static master, got {frame.size}")
    return frame


def colorize_strip(crop: Image.Image, color: tuple[int, int, int, int]) -> Image.Image:
    gray = crop.convert("L")
    tint = Image.new("RGBA", crop.size, color)
    tint.putalpha(gray.point(lambda pixel: int(pixel * 0.65)))
    return Image.blend(crop, tint, 0.42)


def render_frame(index: int, base: Image.Image, mark: Image.Image) -> Image.Image:
    elapsed = index / FPS
    frame = base.copy()

    # One authored rupture only. The identity is stable before and after it.
    if not 1.18 <= elapsed <= 1.72:
        return frame.convert("RGB")

    phase = (elapsed - 1.18) / 0.54
    envelope = math.sin(math.pi * phase)
    rng = random.Random(9300 + index)
    source = base.copy()

    # A few deliberate horizontal tears, rather than continuous full-frame jitter.
    for y, height in [(64, 12), (96, 7), (129, 16), (170, 9), (207, 13), (245, 6), (284, 11)]:
        if rng.random() > 0.22 + envelope * 0.7:
            continue
        offset = int((8 + 34 * envelope) * rng.choice([-1, 1]))
        strip = source.crop((18, y, WIDTH - 18, y + height))
        if rng.random() < 0.65:
            strip = colorize_strip(strip, rng.choice([PINK, BLUE, GREEN, PURPLE]))
        frame.alpha_composite(strip, (18 + offset, y))
        ImageDraw.Draw(frame).line(
            (18, y, WIDTH - 18, y),
            fill=(5, 5, 8, 210),
            width=rng.choice([1, 2]),
        )

    # The “extra presence” appears for only three frames.
    if 1.39 <= elapsed <= 1.52:
        ghost = ImageEnhance.Brightness(mark.copy()).enhance(1.14)
        ghost.putalpha(ghost.getchannel("A").point(lambda pixel: int(pixel * 0.22 * envelope)))
        frame.alpha_composite(ghost, (76 + int(18 * envelope), 37 - int(5 * envelope)))

    # Pink and blue are transient split channels, never stable brand-mark colors.
    if index % 3:
        for x, width, color in [(618, 5, PINK), (806, 3, BLUE)]:
            strip = colorize_strip(source.crop((x, 98, x + width, 241)), color)
            frame.alpha_composite(strip, (x + int(10 * math.sin(index * 2.1)), 98))

    if 0.45 < phase < 0.58:
        flash = Image.new("RGBA", (WIDTH, HEIGHT), (0, 0, 0, 0))
        ImageDraw.Draw(flash).rectangle(
            (0, 176, WIDTH, 182),
            fill=(225, 232, 255, int(125 * envelope)),
        )
        frame = Image.alpha_composite(frame, flash.filter(ImageFilter.GaussianBlur(3)))

    return frame.convert("RGB")


def render(output: Path) -> None:
    for source in (MARK_SOURCE, STATIC_SOURCE):
        if not source.exists():
            raise SystemExit(f"Missing source asset: {source}")
    if not shutil.which("ffmpeg"):
        raise SystemExit("ffmpeg is required to optimize the animated GIF")

    output.parent.mkdir(parents=True, exist_ok=True)
    base = stable_frame()
    mark = prepare_mark()
    frames = [render_frame(index, base, mark) for index in range(FRAME_COUNT)]

    with tempfile.TemporaryDirectory(prefix="another-motion-") as temp_dir:
        raw = Path(temp_dir) / "raw.gif"
        frames[0].save(
            raw,
            save_all=True,
            append_images=frames[1:],
            duration=int(1000 / FPS),
            loop=0,
            optimize=False,
            disposal=2,
        )
        filter_graph = (
            "fps=20,scale=1200:-1:flags=lanczos,split[s0][s1];"
            "[s0]palettegen=max_colors=192:stats_mode=diff[p];"
            "[s1][p]paletteuse=dither=sierra2_4a:diff_mode=rectangle"
        )
        subprocess.run(
            [
                "ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
                "-i", str(raw), "-vf", filter_graph, str(output),
            ],
            check=True,
        )

    print(output)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    render(args.output)


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Cut the approved mark off its plate into a transparent variant.

`docs/assets/another-mark-master.png` is the approved master and stays exactly
as it is: opaque, 1254x1254, on its dark plate. Some surfaces cannot use a
plated square — a site hero over an arbitrary background, a favicon, anything
that has to sit on both light and dark. This derives that variant from the
master so the cutout has a reproducible source instead of being a one-off.

What the key is, and why it is not the obvious one:

  * Not luminance. The plate is dark, but so are the outer edges of the glyph.
    A luminance key eats the strokes and leaves a washed halo.

  * Not HSV saturation either, which is the trap. The plate is (15, 16, 25), a
    dark navy, and HSV reports that as S=112 of 255 — *more* saturated than
    parts of the mark. HSV saturation is relative to value, so anything dark
    and faintly tinted reads as saturated.

  * Absolute chroma, max(R,G,B) - min(R,G,B), is what actually separates them:
    11 on the plate against 155 on the cyan, independent of how dark a pixel is.

  * Chroma alone is still not enough, because the intersection bloom is
    cyan-*white*, and near-white has almost no chroma. Keying on chroma alone
    punches holes through the brightest part of the mark. So a pixel is
    foreground where it is colourful OR bright, and background only where it is
    both neutral and dark — which is what the plate uniquely is.

Two passes then clean it up:

  * The plate carries the master's halftone grain, and grain dots clear the
    floors on their own. They are high-frequency while the phosphor bloom is
    low-frequency, so a median filter on the alpha channel drops the speckle
    and keeps the bloom. It runs on alpha only, so the grain *inside* the mark,
    which is part of the artwork, is untouched.

  * Partial-alpha pixels still carry the plate's darkness. Un-compositing them,
    solving C = (C' - plate*(1-a))/a for the colour the pixel had before it was
    laid over the plate, keeps the edges from dragging a dark fringe onto
    whatever they land on.

One honest limitation: the bloom in the master is light thrown *onto* the
plate. Removing the plate necessarily dims it. This is a derived asset for
plateless surfaces, not a replacement for the master, and DESIGN.md's
requirement to preserve the bloom applies to the master.

Requires Pillow.

    python3 scripts/render-mark-transparent.py
"""

from __future__ import annotations

import argparse
from pathlib import Path

from PIL import Image, ImageChops, ImageFilter, ImageMath

ROOT = Path(__file__).resolve().parents[1]
MASTER = ROOT / "docs" / "assets" / "another-mark-master.png"
OUTPUT = ROOT / "docs" / "assets" / "another-mark-transparent.png"

# Where the key ramps, in the two units above, measured off the master: the
# plate sits at chroma 11 and value 25, the mark's body well past both. The
# floors clear the plate's grain; the ceilings are where a pixel counts as
# fully the mark.
CHROMA_FLOOR, CHROMA_CEIL = 24, 72
VALUE_FLOOR, VALUE_CEIL = 44, 104

# Odd window, in pixels, for the speckle-removing median pass on alpha.
MEDIAN_WINDOW = 7

# Border inset used to sample the plate colour.
PLATE_INSET = 6


def plate_colour(img: Image.Image) -> tuple[int, int, int]:
    """Median of the border pixels: the plate, sampled rather than assumed."""
    w, h = img.size
    px = img.load()
    edge = []
    for x in range(0, w, max(1, w // 256)):
        edge.append(px[x, PLATE_INSET])
        edge.append(px[x, h - 1 - PLATE_INSET])
    for y in range(0, h, max(1, h // 256)):
        edge.append(px[PLATE_INSET, y])
        edge.append(px[w - 1 - PLATE_INSET, y])
    return tuple(sorted(c[i] for c in edge)[len(edge) // 2] for i in range(3))


def ramp_lut(floor: int, ceil: int):
    """A 0..255 lookup that fades in between floor and ceil."""
    span = max(1, ceil - floor)
    return [0 if v <= floor else 255 if v >= ceil else round(255 * (v - floor) / span)
            for v in range(256)]


def build_alpha(img: Image.Image) -> Image.Image:
    r, g, b = img.split()
    high = ImageChops.lighter(ImageChops.lighter(r, g), b)
    low = ImageChops.darker(ImageChops.darker(r, g), b)
    chroma = ImageChops.subtract(high, low)

    alpha = ImageChops.lighter(
        chroma.point(ramp_lut(CHROMA_FLOOR, CHROMA_CEIL)),
        high.point(ramp_lut(VALUE_FLOOR, VALUE_CEIL)),
    )
    return alpha.filter(ImageFilter.MedianFilter(MEDIAN_WINDOW))


def uncomposite(channel: Image.Image, alpha: Image.Image, base: int) -> Image.Image:
    """Recover the channel's colour before it was laid over the plate."""
    size = channel.size
    # Alpha is a divisor, so zero has to become one; those pixels are fully
    # transparent and their colour is never seen.
    safe = alpha.point(lambda v: v if v else 1)
    return ImageMath.lambda_eval(
        lambda a: a["convert"](
            a["min"](a["max"]((a["c"] * 255 - base * (255 - a["al"])) / a["s"], a["lo"]), a["hi"]),
            "L",
        ),
        c=channel,
        al=alpha,
        s=safe,
        lo=Image.new("L", size, 0),
        hi=Image.new("L", size, 255),
    )


def cut(img: Image.Image) -> Image.Image:
    plate = plate_colour(img)
    alpha = build_alpha(img)
    channels = [uncomposite(ch, alpha, base) for ch, base in zip(img.split(), plate)]
    return Image.merge("RGBA", (*channels, alpha))


def main() -> None:
    ap = argparse.ArgumentParser(description="Cut the approved mark off its plate.")
    ap.add_argument("--size", type=int, default=0,
                    help="optional square resize; default keeps the master's native size")
    ap.add_argument("--output", type=Path, default=OUTPUT)
    args = ap.parse_args()

    master = Image.open(MASTER).convert("RGB")
    cutout = cut(master)
    if args.size:
        cutout = cutout.resize((args.size, args.size), Image.Resampling.LANCZOS)

    args.output.parent.mkdir(parents=True, exist_ok=True)
    # The halftone grain is high-entropy, so this only buys a few percent, but
    # it costs nothing and keeps the asset from growing past the master.
    cutout.save(args.output, optimize=True)

    hist = cutout.getchannel("A").histogram()
    total = cutout.width * cutout.height
    clear, opaque = hist[0], hist[255]
    try:
        shown = args.output.relative_to(ROOT)
    except ValueError:
        # --output may point outside the repo, which is fine.
        shown = args.output
    print(f"{shown}  {cutout.width}x{cutout.height}  "
          f"{args.output.stat().st_size // 1024} KB")
    print(f"plate sampled at {plate_colour(master)}  "
          f"opaque {100 * opaque / total:.1f}%  clear {100 * clear / total:.1f}%  "
          f"soft {100 * (total - opaque - clear) / total:.1f}%")


if __name__ == "__main__":
    main()

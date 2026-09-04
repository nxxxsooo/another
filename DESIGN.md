# Design

## Identity

`another` uses two tightly overlapping lowercase `a` forms. Violet and mint green remain near-coincident rather than separating into a source and destination: they represent one conversational identity present in two native agent forms, with no declared original.

The approved master is `docs/assets/another-mark-master.png`. Preserve its geometry, offset, cyan-white intersection bloom, halftone grain, and screen-print texture. Do not redraw it as a clean vector, two unrelated letters, arrows, nodes, portals, mascots, or anime imagery.

## Visual World

The world is dark terminal punk with controlled analog imperfection:

- near-black surfaces and sparse structural lines;
- violet and mint as stable identity colors;
- cyan-white only where the two forms intersect;
- tactile halftone, print grain, scanlines, and restrained phosphor bloom;
- high-saturation accents used as signals, never as a full rainbow surface.

The TUI remains operational and scan-first. Expressive texture belongs to public identity surfaces, not dense session lists.

## Motion

Motion has one authored event: a stable identity briefly ruptures, reveals an extra presence, and restores itself.

`docs/assets/another-motion.gif` is the reference implementation:

- 2.4-second seamless loop;
- stable opening and closing holds;
- one short horizontal tear sequence;
- pink and blue appear only as transient split channels;
- one extra ghost exists for three frames;
- no continuous shaking, random glitch filter, or perpetual RGB separation.

Use `docs/assets/another-motion-static.jpg` when motion is unsupported or reduced motion is requested. Regenerate the GIF with `scripts/render-motion-banner.py`.

## Color Roles

| Role | Value | Use |
|---|---:|---|
| Ground | `#0A0A0A` | Primary dark field |
| Panel | `#141414` | Terminal and banner structure |
| Rule | `#2D2D38` | Borders, dividers, and selected neutral actions |
| Foreground | `#F4F4F5` | Primary text; dense content stays neutral |
| Muted | `#7E7E8F` | Context and supporting copy |
| Source violet | `#6B50FF` | Brand, current state, and source selection |
| Target mint | `#00FFB2` | Destination, migration, and success |
| Intersection | `#68FFD6` | Current row and the meeting of source and target |
| Magenta | `#FF60FF` | Transient campaign motion only; never stable TUI focus |
| Blue | `#62D8FF` | Information and transient motion channel |
| Coral | `#FF6B6B` | Destructive or warning signal |

The TUI uses the violet–mint pair as directional grammar, not decorative color blocking. Source overlays and controls are violet; destination overlays and controls are mint; preview, rename, setup, and ordinary confirmation surfaces stay neutral. Provider colors appear only on their short labels, while titles remain foreground or intersection ink for fast scanning.

## Typography

Public identity artwork pairs a large italic grotesk wordmark with quiet monospace supporting text. Exact claims, commands, and body copy stay live in Markdown rather than being baked into additional images. TUI typography follows the terminal's configured monospace face.

## GitHub Surface

The README first viewport has three jobs:

1. establish identity with the animated banner;
2. state the literal native-session promise in live text;
3. prove the product with the deterministic TUI preview.

Badges remain secondary. Installation follows immediately after the feature summary. The animation must retain its static reduced-motion source through `<picture>`.

## Boundaries

- No direct visual adaptation of P.A.WORKS' *Another*: no characters, eyepatch, doll, logo, poster composition, or artwork.
- No generic AI gradient blobs, sparkles, glass cards, or decorative terminal cosplay.
- No new stable logo colors without an explicit identity decision. Purple/pink, monochrome violet, bone-white, and low-glow versions are campaign extensions only.
- Do not mistake visual loudness for motion quality. Recognition before rupture, restoration after rupture.

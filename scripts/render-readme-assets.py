#!/usr/bin/env python3
"""Render deterministic README assets from another's Charmtone UI language."""
from pathlib import Path
from xml.sax.saxutils import escape

ROOT = Path(__file__).resolve().parents[1]
ASSETS = ROOT / "docs" / "assets"
ASSETS.mkdir(parents=True, exist_ok=True)

C = {
    "bg": "#0A0A0A", "panel": "#141414", "line": "#2D2D38",
    "text": "#F4F4F5", "muted": "#7E7E8F", "subtle": "#A1A1B3",
    "purple": "#6B50FF", "pink": "#FF60FF", "green": "#00FFB2",
    "intersection": "#68FFD6", "coral": "#FF6B6B", "cyan": "#62D8FF", "orange": "#FF985A",
}
FONT = "'SFMono-Regular','JetBrains Mono','IBM Plex Mono',Menlo,Consolas,monospace"

def write(name: str, body: str) -> None:
    (ASSETS / name).write_text(body)

rows = [
    ("just now", "pi", "Fix production deploy without restarting", "~/work/ship", "42", C["pink"]),
    ("3m ago", "Codex", "Review auth boundary and write the tests", "~/code/api", "18", C["green"]),
    ("12m ago", "Claude Code", "Trace the regression to the native session format", "~/code/cli", "76", C["coral"]),
    ("1h ago", "OpenCode", "Release notes for the latest package", "~/tools/release", "12", C["purple"]),
    ("2h ago", "OpenCode 2", "Prototype the new terminal workflow", "~/labs/tui", "9", C["pink"]),
]
row_svg = []
for i, (when, agent, title, path, count, color) in enumerate(rows):
    y = 202 + i * 52
    marker = "›" if i == 0 else ""
    selected = C["intersection"] if i == 0 else C["text"]
    row_svg.append(f'<text x="54" y="{y}" fill="{selected}" font-family="{FONT}" font-size="17" font-weight="{700 if i == 0 else 400}">{marker}</text>')
    row_svg.append(f'<text x="82" y="{y}" fill="{C["muted"]}" font-family="{FONT}" font-size="16">{escape(when)}</text>')
    row_svg.append(f'<text x="210" y="{y}" fill="{color}" font-family="{FONT}" font-size="16" font-weight="650">{escape(agent)}</text>')
    row_svg.append(f'<text x="390" y="{y}" fill="{selected}" font-family="{FONT}" font-size="16">{escape(title)}</text>')
    row_svg.append(f'<text x="930" y="{y}" fill="{C["muted"]}" font-family="{FONT}" font-size="15">{escape(path)}</text>')
    row_svg.append(f'<text x="1128" y="{y}" fill="{C["muted"]}" text-anchor="end" font-family="{FONT}" font-size="15">{count} msgs</text>')

preview = f'''<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="560" viewBox="0 0 1200 560" role="img" aria-label="another terminal session manager preview">
<rect width="1200" height="560" rx="20" fill="{C['bg']}"/>
<rect x="28" y="28" width="1144" height="504" rx="14" fill="{C['panel']}" stroke="{C['line']}" stroke-width="2"/>
<circle cx="54" cy="53" r="6" fill="{C['coral']}"/><circle cx="74" cy="53" r="6" fill="#FFD166"/><circle cx="94" cy="53" r="6" fill="{C['green']}"/>
<text x="52" y="105" fill="{C['purple']}" font-family="{FONT}" font-size="20" font-weight="700">another</text>
<text x="185" y="105" fill="{C['muted']}" font-family="{FONT}" font-size="16">← source</text>
<rect x="286" y="78" width="90" height="38" rx="6" fill="{C['purple']}"/><text x="331" y="103" text-anchor="middle" fill="{C['text']}" font-family="{FONT}" font-size="16" font-weight="700">all</text>
<text x="406" y="105" fill="{C['subtle']}" font-family="{FONT}" font-size="16">157 sessions</text>
<rect x="1042" y="78" width="104" height="38" rx="6" fill="{C['green']}"/><text x="1094" y="103" text-anchor="middle" fill="{C['bg']}" font-family="{FONT}" font-size="16" font-weight="700">target →</text>
<line x1="52" y1="130" x2="1148" y2="130" stroke="{C['line']}" stroke-width="2"/>
{''.join(row_svg)}
<rect x="668" y="154" width="420" height="254" rx="8" fill="{C['bg']}" stroke="{C['green']}" stroke-width="3"/>
<text x="704" y="198" fill="{C['green']}" font-family="{FONT}" font-size="19" font-weight="700">Choose target</text>
<text x="704" y="226" fill="{C['muted']}" font-family="{FONT}" font-size="15">Carry this session to another agent.</text>
<text x="708" y="276" fill="{C['intersection']}" font-family="{FONT}" font-size="17" font-weight="700">› Claude Code</text>
<text x="708" y="312" fill="{C['green']}" font-family="{FONT}" font-size="17">  Codex</text>
<text x="708" y="348" fill="{C['purple']}" font-family="{FONT}" font-size="17">  OpenCode</text>
<text x="708" y="384" fill="{C['pink']}" font-family="{FONT}" font-size="17">  OpenCode 2</text>
<line x1="52" y1="466" x2="1148" y2="466" stroke="{C['line']}" stroke-width="2"/>
<text x="52" y="500" fill="{C['muted']}" font-family="{FONT}" font-size="14">← source · ↑↓ session · enter resume · → migrate · space preview · ctrl+r rename · A archive · ctrl+d delete</text>
</svg>'''
write("tui-preview.svg", preview)
print(ASSETS / "tui-preview.svg")

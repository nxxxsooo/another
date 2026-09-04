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
    y = 180 + i * 60
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
<rect x="390" y="140" width="420" height="250" rx="8" fill="{C['bg']}" stroke="{C['green']}" stroke-width="3"/>
<text x="426" y="184" fill="{C['green']}" font-family="{FONT}" font-size="19" font-weight="700">Choose target</text>
<text x="426" y="212" fill="{C['muted']}" font-family="{FONT}" font-size="15">Carry this session to another agent.</text>
<text x="430" y="262" fill="{C['intersection']}" font-family="{FONT}" font-size="17" font-weight="700">› Claude Code</text>
<text x="430" y="298" fill="{C['green']}" font-family="{FONT}" font-size="17">  Codex</text>
<text x="430" y="334" fill="{C['purple']}" font-family="{FONT}" font-size="17">  OpenCode</text>
<text x="430" y="370" fill="{C['pink']}" font-family="{FONT}" font-size="17">  OpenCode 2</text>
<line x1="52" y1="466" x2="1148" y2="466" stroke="{C['line']}" stroke-width="2"/>
<text x="52" y="500" fill="{C['muted']}" font-family="{FONT}" font-size="14">← source · ↑↓ session · enter resume · → migrate · space preview · ctrl+r rename · x mark · ctrl+t batch · A archive · ctrl+d delete · / search · r refresh</text>
</svg>'''
write("tui-preview.svg", preview)
print(ASSETS / "tui-preview.svg")

# Batch review overlay: changed rows as 原名 → 新名, everything else folded
# into counts. Sample rows mirror the contract format the live batch produces.
changed = [
    ("昨晚改了一半的登录页", "0904｜功能｜登录页重构"),
    ("修一下支付回调超时", "0904｜修复｜支付回调超时"),
    ("新人 onboarding 文档", "0904｜文档｜新人上手指南"),
    ("首页加载太慢了", "0904｜优化｜首页加载提速"),
    ("准备 v2.0 发版", "0904｜发布｜发版前检查"),
]
dim_rows = [
    ("2m ago", "pi", "0903｜探索｜AI 搜索方案对比", "cyan"),
    ("2h ago", "Codex", "0903｜设计｜深色模式配色", "green"),
    ("3h ago", "Claude Code", "0903｜文档｜接口契约整理", "coral"),
]
batch_svg = []
for i, (when, agent, title, color) in enumerate(dim_rows):
    y = 202 + i * 52
    batch_svg.append(f'<text x="54" y="{y}" fill="{C["intersection"]}" font-family="{FONT}" font-size="16" opacity="0.8">✓</text>')
    batch_svg.append(f'<text x="82" y="{y}" fill="{C["muted"]}" font-family="{FONT}" font-size="16" opacity="0.45">{escape(when)}</text>')
    batch_svg.append(f'<text x="210" y="{y}" fill="{C[color]}" font-family="{FONT}" font-size="16" font-weight="650" opacity="0.7">{escape(agent)}</text>')
    batch_svg.append(f'<text x="390" y="{y}" fill="{C["muted"]}" font-family="{FONT}" font-size="16" opacity="0.45">{escape(title)}</text>')

# Keep the provider column visible behind the review. That makes the core
# promise legible at a glance: one batch can span several native agents.
modal_x, modal_w = 350, 770
row_svg = []
for i, (old, new) in enumerate(changed):
    y = 300 + i * 34
    row_svg.append(
        f'<text x="{modal_x + 40}" y="{y}" font-family="{FONT}" font-size="16" xml:space="preserve">'
        f'<tspan fill="{C["muted"]}">{escape(old)}</tspan>'
        f'<tspan fill="{C["subtle"]}">  →  </tspan>'
        f'<tspan fill="{C["intersection"]}" font-weight="700">{escape(new)}</tspan></text>'
    )

batch = f'''<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="640" viewBox="0 0 1200 640" role="img" aria-label="another batch rename review preview">
<rect width="1200" height="640" rx="20" fill="{C['bg']}"/>
<rect x="28" y="28" width="1144" height="584" rx="14" fill="{C['panel']}" stroke="{C['line']}" stroke-width="2"/>
<circle cx="54" cy="53" r="6" fill="{C['coral']}"/><circle cx="74" cy="53" r="6" fill="#FFD166"/><circle cx="94" cy="53" r="6" fill="{C['green']}"/>
<text x="52" y="105" fill="{C['purple']}" font-family="{FONT}" font-size="20" font-weight="700">another</text>
<text x="185" y="105" fill="{C['muted']}" font-family="{FONT}" font-size="16">← source</text>
<rect x="286" y="78" width="90" height="38" rx="6" fill="{C['purple']}"/><text x="331" y="103" text-anchor="middle" fill="{C['text']}" font-family="{FONT}" font-size="16" font-weight="700">all</text>
<text x="406" y="105" fill="{C['subtle']}" font-family="{FONT}" font-size="16">10 sessions</text>
<rect x="1042" y="78" width="104" height="38" rx="6" fill="{C['green']}"/><text x="1094" y="103" text-anchor="middle" fill="{C['bg']}" font-family="{FONT}" font-size="16" font-weight="700">target →</text>
<line x1="52" y1="130" x2="1148" y2="130" stroke="{C['line']}" stroke-width="2"/>
{''.join(batch_svg)}
<rect x="{modal_x}" y="196" width="{modal_w}" height="330" rx="10" fill="{C['bg']}" stroke="{C['line']}" stroke-width="2"/>
<text x="{modal_x + 40}" y="238" fill="{C['text']}" font-family="{FONT}" font-size="19" font-weight="700">批量命名会话</text>
<text x="{modal_x + 40}" y="266" fill="{C['muted']}" font-family="{FONT}" font-size="15">可应用 5 条 · 冻结 2 · 失败 0 · 无变化 1</text>
{''.join(row_svg)}
<text x="{modal_x + 40}" y="486" fill="{C['muted']}" font-family="{FONT}" font-size="14">e 展开其余行</text>
<line x1="52" y1="556" x2="1148" y2="556" stroke="{C['line']}" stroke-width="2"/>
<text x="52" y="590" fill="{C['muted']}" font-family="{FONT}" font-size="14">enter 应用变更 · e 展开其余 · esc 关闭</text>
</svg>'''
write("tui-batch.svg", batch)
print(ASSETS / "tui-batch.svg")

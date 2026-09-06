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

# Agents are drawn as chips, exactly as the TUI draws them: one equal-sized
# block per agent, identified by color and a three-letter code. Names differ in
# length by up to nine characters, and set as words they leave a ragged column
# in which a short agent looks like a lesser one.
AGENTS = {
    "pi": ("PI", "#0ADCD9"),
    "codex": ("CDX", "#BFBCC8"),
    "claude-code": ("CLA", "#FF985A"),
    "cursor": ("CUR", "#4776FF"),
    "opencode": ("OPC", "#FF4FBF"),
    "opencode2": ("OC2", "#FF79D0"),
    "qwen": ("QWN", "#D46EFF"),
    "agy": ("AGY", "#F5EF34"),
}
CHIP_W, CHIP_H = 52, 26
# The block is tinted, not filled: a column of saturated blocks would take the
# row away from the titles, which are the only thing that identifies a session.
CHIP_TINT = 0.18
MIN_CONTRAST = 4.5


def luminance(hex_color: str) -> float:
    def channel(v: float) -> float:
        v /= 255
        return v / 12.92 if v <= 0.03928 else ((v + 0.055) / 1.055) ** 2.4
    r, g, b = (int(hex_color[i:i + 2], 16) for i in (1, 3, 5))
    return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)


def blend(front: str, back: str, strength: float) -> str:
    f = [int(front[i:i + 2], 16) for i in (1, 3, 5)]
    b = [int(back[i:i + 2], 16) for i in (1, 3, 5)]
    return "#" + "".join(f"{round(x * strength + y * (1 - strength)):02X}" for x, y in zip(f, b))


def contrast(a: str, b: str) -> float:
    high, low = sorted((luminance(a), luminance(b)), reverse=True)
    return (high + 0.05) / (low + 0.05)


def chip_colors(color: str) -> tuple[str, str]:
    """Tint the block and lift the code until it clears the contrast floor."""
    tint, ink = blend(color, C["bg"], CHIP_TINT), color
    while contrast(ink, tint) < MIN_CONTRAST:
        ink = blend(C["text"], ink, 0.25)
    return ink, tint


def agent_chip(agent: str, x: float, baseline: float) -> str:
    code, color = AGENTS[agent]
    ink, tint = chip_colors(color)
    top = baseline - 18
    return (
        f'<rect x="{x}" y="{top}" width="{CHIP_W}" height="{CHIP_H}" rx="6" fill="{tint}"/>'
        f'<text x="{x + CHIP_W / 2}" y="{baseline}" text-anchor="middle" fill="{ink}" '
        f'font-family="{FONT}" font-size="15" font-weight="700" letter-spacing="0.5">{code}</text>'
    )


def write(name: str, body: str) -> None:
    (ASSETS / name).write_text(body)

rows = [
    ("just now", "pi", "Fix production deploy without restarting", "~/work/ship", "42"),
    ("3m ago", "codex", "Review auth boundary and write the tests", "~/code/api", "18"),
    ("12m ago", "claude-code", "Trace the regression to the native session format", "~/code/cli", "76"),
    ("1h ago", "opencode", "Release notes for the latest package", "~/tools/pkg", "12"),
    ("2h ago", "opencode2", "Prototype the new terminal workflow", "~/labs/tui", "9"),
]
row_svg = []
for i, (when, agent, title, path, count) in enumerate(rows):
    y = 180 + i * 60
    marker = "›" if i == 0 else ""
    selected = C["intersection"] if i == 0 else C["text"]
    row_svg.append(f'<text x="54" y="{y}" fill="{selected}" font-family="{FONT}" font-size="17" font-weight="{700 if i == 0 else 400}">{marker}</text>')
    row_svg.append(f'<text x="82" y="{y}" fill="{C["muted"]}" font-family="{FONT}" font-size="16">{escape(when)}</text>')
    row_svg.append(agent_chip(agent, 210, y))
    row_svg.append(f'<text x="288" y="{y}" fill="{selected}" font-family="{FONT}" font-size="16">{escape(title)}</text>')
    row_svg.append(f'<text x="930" y="{y}" fill="{C["muted"]}" font-family="{FONT}" font-size="15">{escape(path)}</text>')
    row_svg.append(f'<text x="1128" y="{y}" fill="{C["muted"]}" text-anchor="end" font-family="{FONT}" font-size="15">{count} msgs</text>')

# The picker is where a chip and the name it stands for are seen together, so
# the codes on the rows behind it can be read at all.
targets = [("claude-code", "Claude Code"), ("codex", "Codex"), ("opencode", "OpenCode"), ("opencode2", "OpenCode 2")]
target_svg = []
for i, (agent, name) in enumerate(targets):
    y = 262 + i * 36
    ink = C["intersection"] if i == 0 else C["text"]
    if i == 0:
        target_svg.append(f'<text x="430" y="{y}" fill="{ink}" font-family="{FONT}" font-size="17" font-weight="700">›</text>')
    target_svg.append(agent_chip(agent, 450, y))
    target_svg.append(f'<text x="516" y="{y}" fill="{ink}" font-family="{FONT}" font-size="17" font-weight="{700 if i == 0 else 400}">{escape(name)}</text>')
target_svg = "".join(target_svg)

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
{target_svg}
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
    ("2m ago", "pi", "0903｜探索｜AI 搜索方案对比"),
    ("2h ago", "codex", "0903｜设计｜深色模式配色"),
    ("3h ago", "claude-code", "0903｜文档｜接口契约整理"),
]
batch_svg = []
for i, (when, agent, title) in enumerate(dim_rows):
    y = 202 + i * 52
    batch_svg.append(f'<text x="54" y="{y}" fill="{C["intersection"]}" font-family="{FONT}" font-size="16" opacity="0.8">✓</text>')
    batch_svg.append(f'<text x="82" y="{y}" fill="{C["muted"]}" font-family="{FONT}" font-size="16" opacity="0.45">{escape(when)}</text>')
    batch_svg.append(f'<g opacity="0.7">{agent_chip(agent, 210, y)}</g>')
    batch_svg.append(f'<text x="288" y="{y}" fill="{C["muted"]}" font-family="{FONT}" font-size="16" opacity="0.45">{escape(title)}</text>')

# Keep the provider column visible behind the review. That makes the core
# promise legible at a glance: one batch can span several native agents.
modal_x, modal_w = 350, 770
row_svg = []
for i, (old, new) in enumerate(changed):
    y = 312 + i * 34
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
<text x="{modal_x + 40}" y="264" fill="{C['muted']}" font-family="{FONT}" font-size="15" xml:space="preserve">模型来源  <tspan fill="{C['text']}">pi</tspan> · <tspan fill="{C['text']}">claude-sonnet-4-5</tspan><tspan fill="{C['subtle']}">  本次临时</tspan></text>
<text x="{modal_x + 40}" y="290" fill="{C['muted']}" font-family="{FONT}" font-size="15">可应用 5 条 · 冻结 2 · 失败 0 · 无变化 1</text>
{''.join(row_svg)}
<text x="{modal_x + 40}" y="486" fill="{C['muted']}" font-family="{FONT}" font-size="14">e 展开其余行</text>
<line x1="52" y1="556" x2="1148" y2="556" stroke="{C['line']}" stroke-width="2"/>
<text x="52" y="590" fill="{C['muted']}" font-family="{FONT}" font-size="14">enter 应用变更 · r 重试失败 · m 换模型 · e 展开其余 · esc 关闭</text>
</svg>'''
write("tui-batch.svg", batch)
print(ASSETS / "tui-batch.svg")

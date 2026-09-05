<p align="center">
  <picture>
    <source media="(prefers-reduced-motion: reduce)" srcset="docs/assets/another-motion-static.jpg">
    <img src="docs/assets/another-motion.gif" width="100%" alt="another 原生 coding agent 会话管理器；紫绿标志短暂分裂出霓虹色通道，随后恢复原状">
  </picture>
</p>

<p align="center">
  <strong>简体中文</strong> · <a href="README.en.md">English</a>
</p>

<p align="center">
  <a href="https://github.com/nxxxsooo/another/releases"><img src="https://img.shields.io/github/v/release/nxxxsooo/another?style=flat-square&color=6B50FF" alt="最新版本"></a>
  <a href="https://github.com/nxxxsooo/another/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/nxxxsooo/another/ci.yml?branch=main&style=flat-square&label=build&color=29D398" alt="构建状态"></a>
</p>

<p align="center">
  在多个 coding agent 之间浏览、管理、迁移并继续真实会话，无需粘贴摘要。
</p>

<p align="center">
  <img src="docs/assets/tui-preview.svg" width="100%" alt="another TUI 展示多个 coding agent 的会话列表和目标 agent 选择器">
</p>

模型用完额度，或者当前工作更适合另一个模型时，通常只能总结已有内容，再粘贴到另一端重新解释。`another` 迁移的是会话本身：它把对话写入目标 agent 的原生存储，随后可以在那里直接打开并继续。

## 功能

- **原生会话**：在目标 agent 中按其原生格式恢复，不是粘贴一份摘要。
- **十个 agent**：Pi、Codex、Claude Code、Cursor、OpenCode、OpenCode 2、CommandCode、Hermes、Qwen Code 和 Antigravity。
- **一个界面**：直接浏览、搜索、预览、重命名、归档、删除或迁移会话。
- **迁移后校验**：重新读取每次写入，比较内容摘要；不一致时回滚，来源会话始终保持原样。
- **本地运行**：读取各 agent 的本地原生存储；`~/.cache/another/` 下的私有 SQLite 索引会在重新扫描时跳过未变会话。

## 安装

macOS 和 Linux：

```bash
# Homebrew
brew trust nxxxsooo/tap
brew install nxxxsooo/tap/another

# 安装脚本
curl -fsSL https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.sh | bash
```

Homebrew 6 要求先信任第三方 tap，否则会拒绝安装。旧版 Homebrew 不认识 `brew trust`，可跳过这一行，直接安装。

<details>
<summary><strong>从源码安装</strong></summary>

需要 Go 1.24 或更高版本：

```bash
go install github.com/nxxxsooo/another/cmd/another@latest
```

</details>

<details>
<summary><strong>手动下载</strong></summary>

从 [Releases](https://github.com/nxxxsooo/another/releases) 下载对应 `darwin`／`linux`、`amd64`／`arm64` 的压缩包，用 `checksums.txt` 校验后，把二进制文件放到 `PATH`：

```bash
shasum -a 256 -c checksums.txt --ignore-missing
tar -xzf another_*_darwin_arm64.tar.gz
install -m 755 another ~/.local/bin/another
```

</details>

确认 `~/.local/bin` 或 Go bin 目录在 `PATH` 中，然后运行：

```bash
another
```

首次运行会打开 Charmtone 配置界面。按 `↑↓` 移动，按 `Space` 开关 agent，按 `Shift+↑↓` 调整它们在来源、去向和 `providers` 中的顺序，再按 `Enter` 继续。第二页可以选择一个已安装的 agent，用于生成 AI 标题建议，默认关闭。之后可随时运行 `another setup` 修改配置。

### 更新

```bash
brew upgrade another                     # Homebrew
curl -fsSL https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.sh | bash   # 安装脚本
go install github.com/nxxxsooo/another/cmd/another@latest                                      # 源码安装
```

Homebrew 之外的安装方式需要手动更新；已安装的二进制不会自动跟随仓库变化。

## 使用 TUI

```text
Enter     用原生 agent 恢复选中的会话
→         把选中的会话迁移到另一个 agent
←         选择来源 agent
↑ / ↓     在会话或选择器条目间移动
Space     预览会话
Ctrl+R    在来源 agent 的原生标题存储中重命名
Tab       已配置 AI 标题且建议到达时，接受建议
A         归档；再次按 A 可撤销上一步归档
Ctrl+D    明确确认后永久删除
/         搜索标题和标准化后的会话正文
r         刷新本地索引
Esc       关闭选择器或临时状态
q         退出
```

迁移完成后，界面会先显示准确的恢复命令。按 `Enter` 把终端交给目标 agent，按 `c` 复制命令，或按 `Esc` 留在会话列表。

## 支持的 agent

OpenCode 与 OpenCode 2 是两个独立 provider。它们使用不同的命令、数据库、schema 和服务生命周期。

| Agent | Provider ID | 原生恢复命令 | 重命名 | 归档 | 删除 |
|---|---|---|:---:|:---:|:---:|
| Pi | `pi` | `pi --session <file>` | ✓ | — | ✓ |
| Codex | `codex` | `codex resume <id>` | ✓ | ✓ | ✓ |
| Claude Code | `claude-code` | `claude --resume <id>` | ✓ | — | ✓ |
| Cursor | `cursor` | `cursor-agent --resume <id>` | — | — | ✓ |
| OpenCode | `opencode` | `opencode --session <id>` | ✓ | ✓ | ✓ |
| OpenCode 2 | `opencode2` | `opencode2 --session <id>` | ✓ | — | ✓ |
| CommandCode | `commandcode` | `commandcode --resume <id>` | — | — | ✓ |
| Hermes | `hermes` | `hermes --resume <id>` | — | ✓ | ✓ |
| Qwen Code | `qwen` | `qwen --resume <id>` | ✓ | — | — |
| Antigravity | `agy` | `agy --conversation <id>` | ✓ | — | — |

`—` 表示这个 agent 没有经过验证的原生操作契约。`another` 会直接提示限制，不会维护一份刷新后消失的私有状态。

检查本机安装状态：

```bash
another providers
another providers doctor
```

## AI 标题建议

如果 setup 中指定了 agent，按 `Ctrl+R` 会以原始标题打开重命名框，同时在后台请求该 agent 生成一个 `MMDD｜类型｜主题` 格式的标题，规则来自 `title-formatter` skill。日期取自索引中的创建时间，并转换到 `Asia/Shanghai`，不会交给模型猜测。

不符合格式的建议、关闭输入框后才到达的建议和失败请求都会被丢弃，不会覆盖已输入的文字。agent 在临时目录中运行，因此当前项目的指令不会进入标题请求。

## CLI

```bash
# 浏览和搜索
another list [--provider ID] [--project PATH] [--cwd] [--limit N] [--json] [--refresh]
another search "query" [--provider ID] [--project PATH] [--cwd] [--limit N] [--json]
another show <session-id> [--provider ID] [--limit N]

# 迁移会话
another migrate <session-id> --to <provider> [--from ID] [-y]
another migrate <session-id> --to codex --context full -y
another resume <session-id> --to <provider> [--from ID]

# 可移植备份
another export <session-id> -o session.another.json
another import session.another.json --to <provider> [--context MODE] [--dry-run] -y

# 配置和索引
another setup
another index update
another index rebuild
```

`list` 和 `search` 加上 `--json` 后会输出机器可读记录。子 agent 会话默认隐藏；加上 `--include-subagents` 可显示。

上下文模式：

- `auto`：清理后的所有轮次能放下时全部保留，否则选择近期工作上下文；
- `full`：保留清理后的全部用户和 assistant 轮次，即使目标端可能压缩或拒绝；
- `recent`：始终生成有长度上限的近期上下文。

## 迁移哪些内容

`another` 会保留按顺序排列的用户和 assistant 文本、目标格式支持的时间戳、项目目录、标题，以及用于去重和校验的迁移标记。

各 provider 特有的 reasoning signature、tool call、tool result、图片和 system record 没有可移植的对等格式，因此不会迁移。来源会话不会被修改。

OpenCode 和 OpenCode 2 通过各自的官方导入／API 接口写入。Codex Desktop 标题来自 GUI 标题索引，不从注入消息中猜测。Pi 写入完整的 assistant 记录，并为重建历史显式设置传输元数据和零值 usage。

## 安全边界

- 每个迁移目标都会重新加载并校验内容，然后才报告成功。
- 校验失败时，只删除本次迁移创建的产物。
- `Ctrl+D` 默认选择 **Cancel**，确认框会显示 provider、标题、项目目录和完整 session ID。
- 能被准确识别的活动会话禁止重命名、归档和删除。
- 配置目录权限为 `0700`，配置文件和 SQLite 索引权限为 `0600`。
- 在 setup 中停用 agent 只会移除本地索引记录，不会删除原生会话。
- 标题建议复用你已认证的 agent CLI；`another` 自身不保存 API key。
- 标题建议只会显示在重命名字段旁，仍需按 `Tab` 接受，再按 `Enter` 提交。

## 开发

```bash
git clone https://github.com/nxxxsooo/another.git
cd another
make build
go test ./...
go test -race ./...
go vet ./...
```

重新生成视觉资产。栅格化字标已经提交到仓库，因此普通构建不需要安装字体。

```bash
./scripts/render-readme-assets.py                                  # TUI 预览 SVG
python3 ./scripts/render-motion-banner.py                          # 品牌横幅，需要 Pillow 和 ffmpeg
python3 ./scripts/render-goodbye-gif.py                            # 退出动效，需要 Pillow
python3 ./scripts/render-logo-face.py > internal/tui/logo_face.go  # 字标，需要 Pillow 和 JetBrains Mono ExtraBold
```

## 名字

字面含义优先：保留当前会话，在 **another** agent 中继续。

这个名字也轻轻借用了 [《Another》](https://www.pa-works.jp/works/another/) 的概念。这部 2012 年悬疑动画由 P.A.WORKS 制作，改编自绫辻行人的小说。作品中那个难以分辨身份的额外存在，给迁移会话提供了第二层隐喻：同一个对话身份会在另一个位置，以另一种原生形式出现。

这里只借用抽象概念，不代表任何关联，也不改编其视觉。项目没有使用动画的 logo、角色或原画。

## 致谢

`another` 起源于 [CyrusSE/agenthop](https://github.com/CyrusSE/agenthop) 的 fork，并按 MIT License 分发。目前已使用独立的 module path、provider 契约、TUI、配置流程和发布体系。

## 许可证

[MIT](LICENSE)

<p align="center">
  <picture>
    <source media="(prefers-reduced-motion: reduce)" srcset="docs/assets/tui-goodbye-static.png">
    <img src="docs/assets/tui-goodbye.gif" width="500" alt="another 退出时打印的字标；上半部分错开两个字符并变成洋红色，随后恢复紫色并重新对齐">
  </picture>
</p>

<p align="center">
  <sub>退出时，字标会留在终端回滚区；把终端交给另一个 agent 时不会显示。<br>
  设置 <code>ANOTHER_NO_MOTION=1</code>、<code>NO_COLOR</code>、<code>CI</code> 或通过 pipe 输出时，使用静止帧。</sub>
</p>

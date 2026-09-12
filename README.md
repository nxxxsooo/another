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
- **十一个 agent**：Pi、Codex、Claude Code、Cursor、OpenCode、OpenCode 2、CommandCode、CodeM、Hermes、Qwen Code 和 Antigravity。
- **一个界面**：直接浏览、搜索、预览、重命名、归档、删除、换目录或迁移会话。
- **项目聚合**：默认只看当前 Git 项目，并把主工作区与所有已登记 worktree 的会话放在一起；按 `f` 可切换到全部项目，按 `g` 可按日期或 worktree 分组。
- **迁移后校验**：重新读取每次写入，比较内容摘要；不一致时回滚，来源会话始终保持原样。
- **中英双语界面**：默认跟随终端 locale，也可以在 setup 里固定为 English 或中文；与标题语言各自独立。
- **本地运行**：读取各 agent 的本地原生存储；私有 SQLite 索引（`~/.cache/another/`，Windows 下为 `%LOCALAPPDATA%\another`）会在重新扫描时跳过未变会话。

## 安装

macOS 和 Linux：

```bash
# Homebrew
brew trust nxxxsooo/tap
brew install nxxxsooo/tap/another

# 安装脚本
curl -fsSL https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.sh | bash && export PATH="$HOME/.local/bin:$PATH"
```

Windows（PowerShell）：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.ps1 | iex"
```

Windows 脚本把 `another.exe` 装到 `%LOCALAPPDATA%\another`，并加入用户 `PATH`；装完重开一个终端即可。

Homebrew 6 要求先信任第三方 tap，否则会拒绝安装。旧版 Homebrew 不认识 `brew trust`，可跳过这一行，直接安装。

安装脚本会在默认目录不在 `PATH` 时，把它持久化到当前 shell 的启动文件（zsh 为 `~/.zshrc`）。命令末尾的 `export` 让当前终端也立即生效；这一步必须由启动安装脚本的 shell 执行，脚本的子进程无法修改父 shell，SSH 与本机终端在这里没有区别。

<details>
<summary><strong>安装报 <code>undefined method 'run'</code></strong></summary>

Cask 用 `postflight_steps` 去掉下载后的 quarantine 属性，这个写法需要 Homebrew 6.0.16 或更高版本。看到

```text
Error: Cask 'another' definition is invalid: undefined method 'run' for an instance of Homebrew::InstallSteps::DSL
```

说明本机 Homebrew 太旧。通常不是没升级，而是升级被卡住了——`brew update` 如果打印过 `Could not 'git stash' in /opt/homebrew`，它抓完新版本就中止了合并，源码一直停在旧版；此时官方 cask 也会成片报 `undefined method 'command_wrapper'`。先确认 `/opt/homebrew`（Intel 机器为 `/usr/local/Homebrew`）里没有要保留的本地改动，然后重置并升级：

```bash
cd /opt/homebrew
git status
git reset --hard origin/HEAD
brew update
brew install nxxxsooo/tap/another
```

已经信任过的 tap 不需要再 `brew trust` 一次。

</details>

<details>
<summary><strong>从源码安装</strong></summary>

需要 Go 1.24 或更高版本：

```bash
go install github.com/nxxxsooo/another/cmd/another@latest
```

</details>

<details>
<summary><strong>手动下载</strong></summary>

从 [Releases](https://github.com/nxxxsooo/another/releases) 下载对应 `darwin`／`linux`、`amd64`／`arm64` 的压缩包（Windows 为 `windows` 的 zip 包），用 `checksums.txt` 校验后，把二进制文件放到 `PATH`：

```bash
shasum -a 256 -c checksums.txt --ignore-missing
tar -xzf another_*_darwin_arm64.tar.gz
install -m 755 another ~/.local/bin/another
```

Windows 上解压 `another_*_windows_amd64.zip`，把 `another.exe` 放到 `PATH` 中的目录（安装脚本默认用 `%LOCALAPPDATA%\another`）。

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

首次运行会打开 Charmtone 配置界面。第一页顶部用 `←→` 选界面语言：**Auto**（默认，跟随终端 locale）、**English**、**中文**；按下即时重绘，选错当场就能看见。按 `↑↓` 移动，按 `Space` 开关 agent，按 `Shift+↑↓` 调整它们在来源、去向和 `providers` 中的顺序，再按 `Enter` 继续。页面默认只列持续实测的六个 agent，五个兼容适配折在末尾一行里，光标移到那行按 `Space` 展开；如果配置里已经启用了其中某个，这行开局就是展开的——看不见的设置没法关掉。第二页可以选择一个已安装的 agent，用于生成 AI 标题建议，默认关闭。启用了 OpenCode 2 时，这一页还有一行 `OpenCode 2 标题插件`：按 `t` 打开，another 才会把插件写进 OpenCode 2 的配置目录，那行同时写明将写到哪个目录、目录里现在是什么。之后可随时运行 `another setup` 修改配置。

通过 SSH 使用 Ghostty 时，终端通常只转发 `TERM=xterm-ghostty`，不会转发 `COLORTERM`。`v0.13.3` 起 another 会直接识别这种环境并保留完整配色，同时仍遵守 `NO_COLOR`。

### 更新

```bash
brew upgrade another                     # Homebrew
curl -fsSL https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.sh | bash && export PATH="$HOME/.local/bin:$PATH"  # 安装脚本
go install github.com/nxxxsooo/another/cmd/another@latest                                      # 源码安装
```

Windows 下安装脚本的等价命令（`another update` 能判断出安装来源时会替你执行）：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.ps1 | iex"

Homebrew 之外的安装方式需要手动更新；已安装的二进制不会自动跟随仓库变化。

## 使用 TUI

```text
Enter     用原生 agent 恢复选中的会话
→         把选中的会话迁移到另一个 agent
←         选择来源 agent
↑ / ↓     在会话或选择器条目间移动
Space     预览会话
f         在当前项目和全部项目之间切换
Ctrl+R    在来源 agent 的原生标题存储中重命名
Tab       已配置 AI 标题且建议到达时，接受建议
m         把会话复制或搬到另一个项目目录
a         归档；再次按 a 可撤销上一步归档
x / X     标记光标所在会话 / 整页全选或全清
Ctrl+D    明确确认后删除
u         撤销这次删除；仅限会话就是一个文件、Another 能原样放回的 agent
/         搜索标题和标准化后的会话正文
r         刷新本地索引
Esc       关闭选择器或临时状态
q         退出
```

界面语言与标题语言是两个独立设置，`auto` 的含义也不同：界面的 `auto` 读终端 locale（`LC_ALL` → `LC_MESSAGES` → `LANG`），`zh` 开头走中文，其余一律英文；标题的 `auto` 看会话内容。CLI 的 `--help`、参数说明和错误信息始终是英文。迁移到 Pi 时补写的那句衔接语（原会话以 assistant 结尾时才写）跟随界面语言，两种写法都能在读回时识别并剥离。

TUI 运行期间会把终端标题设为 `another`（setup 页为 `another setup`），退出时改回当前目录，把终端交给目标 agent 时改成那个 agent 的名字——标签页上写的始终是此刻真正在跑的东西。

在 macOS 上，TUI 打开时会临时切到当前可用的英文键盘布局，让字母快捷键不受中文拼音输入法拦截；退出或进入目标 agent 前会恢复打开 TUI 前的输入源。Linux 不修改系统输入源。

迁移完成后，界面会先显示准确的恢复命令。按 `Enter` 把终端交给目标 agent，按 `c` 复制命令，或按 `Esc` 留在会话列表。

TUI 默认按当前项目过滤。Git 仓库的主工作区、所有已登记 worktree 及其子目录视为同一项目；非 Git 目录按当前目录精确匹配。顶部始终显示当前范围，搜索也沿用该范围。当前项目没有会话时不会自动跳到全局，按 `f` 即可查看全部。同一窗口宽度下，切换范围保持去向按钮的位置固定，会话计数在预留区域内居中。

一个项目不等于一个目录。当列出的会话确实分布在多个目录时——worktree、monorepo 的子树——行内会出现目录列：和 agent 一样是一枚按路径着色的标签，写着相对于项目根的那一段（`.worktrees/delete-undo`、`packages/api`，位于项目根的会话写项目根自己的名字），共有的前缀不会占掉标题的宽度。列宽只由终端宽度决定，不随载入的内容变：切换范围、过滤 agent、翻页时，每一列都留在原地，放不下的标题和路径以 `…` 截断。目录已不存在时保留路径、去掉颜色。所有会话都在同一个目录时，这一列不出现。

按 `g` 在三种视图之间循环：不分组、按日期、按 worktree，底部会写明当前落在哪一种。分组只是对已加载页面换一种看法，不会重新查询，光标下的会话也仍然在光标下；光标不会停在分组带上。整份列表只有一组时不分组——一条盖住全部的分组带，说的是每一行都已经共有的东西。

按日期分组把列表切成今天、昨天、本周早些时候、本月早些时候和更早。分组带按日历天划分，不是按 24 小时：昨晚 23:50 和今早 00:10 相隔二十分钟，却分属两天，而这正是「昨天」这个词要回答的问题。分组带说了是哪一天之后，时间列只回答它没说的部分——今天给时长（`7m`、`13h`），昨天给时钟（`22:17`），再往前给日期（`Aug 1`）——于是这一列从十二格缩到五格，省下的宽度归还给标题和目录。

按 worktree 分组时，每棵树有一条自己颜色的分组带，写着树名和会话数，它的会话排在带下面；记录在子目录里的会话算进所属的那棵树，不会单独成组。树按各自最新的会话排序，刚离开的活儿仍然在最上面，组内依旧按时间倒序。这时目录列只留分组带没说的部分，比如会话真正跑在的 `internal/tui`；日期分组不动这一列，因为分组带说的是时间，不是位置。

底部只列每条会话都用得上的那几个键，最后一个是 `?`。按下去是完整键表，按当前 agent 真正支持的操作过滤——不支持归档的 agent 不会列出归档。终端放不下整张表时它会滚动，而不是把键截掉。

## 换目录

同一个 agent，换一个工作目录继续：新开的 worktree、搬过位置的仓库、或者本来就该在隔壁项目里做的事。按 `m` 输入目标目录，`Tab` 在两种语义之间切换：

- **复制**（默认）：原会话留在原地，目标目录里多出一份可以继续的会话；
- **移动**：会话本身换目录，原目录不再有它。

这不是迁移。迁移会把对话经可移植格式重写一遍，工具调用和 reasoning 会在这一步丢掉；换目录走的是各 agent 自己的原生操作，内容原样保留：OpenCode 2 调用官方的 `fork` 和 `move` 接口，Pi 逐行复制自己的会话文件、只改写文件头里的 `id` 和 `cwd`。没有经过验证的原生契约的 agent 会如实报告不支持，而不是用重写冒充搬家。

每次换目录都会回读校验后才报告成功：OpenCode 2 比对会话行里的目录，Pi 比对内容摘要。移动模式只有在新文件校验通过之后才删除原文件；复制模式如果目标建不起来，不会在源目录留下半个副本。目标目录必须真实存在——写错一个路径应该当场失败，而不是生成一个指向空处的会话。

## 支持的 agent

OpenCode 与 OpenCode 2 是两个独立 provider。它们使用不同的命令、数据库、schema 和服务生命周期。

持续实测范围是 **Pi、OpenCode 2、Claude Code、Codex、Antigravity 和 Qwen Code**；这六个进入每次发布的回归检查。Cursor、OpenCode、CommandCode、CodeM 和 Hermes 保留兼容适配，但不承诺每个版本都在维护者环境完成端到端实测。首次 setup 不会根据本机残留数据自动全选，必须由用户手动选择要索引和展示的 agent。

会话列表用等宽色块标记 agent：名字长短差着九个字符，排成文字会让短名字看起来是个更小的 agent，也会把标题挤到每行不同的位置。来源和去向选择器里色块和全名同时出现，那里就是这张对照表。

| Agent | Provider ID | 列表标记 | 原生恢复命令 | 重命名 | 归档 | 换目录 | 删除 |
|---|---|:---:|---|:---:|:---:|:---:|:---:|
| Pi | `pi` | `PI` | `pi --session <file>` | ✓ | — | ✓ | ✓ |
| Codex | `codex` | `CDX` | `codex resume <id>` | ✓ | ✓ | — | ✓ |
| Claude Code | `claude-code` | `CLA` | `claude --resume <id>` | ✓ | — | — | ✓ |
| Cursor | `cursor` | `CUR` | `cursor-agent --resume <id>` | — | — | — | ✓ |
| OpenCode | `opencode` | `OPC` | `opencode --session <id>` | ✓ | ✓ | — | ✓ |
| OpenCode 2 | `opencode2` | `OC2` | `opencode2 --session <id>` | ✓ | — | ✓ | ✓ |
| CommandCode | `commandcode` | `CMD` | `commandcode --resume <id>` | — | — | — | ✓ |
| CodeM | `codem` | `CDM` | `codem --resume <id>` | ✓ | ✓ | — | ✓ |
| Hermes | `hermes` | `HRM` | `hermes --resume <id>` | — | ✓ | — | ✓ |
| Qwen Code | `qwen` | `QWN` | `qwen --resume <id>` | ✓ | ✓ | — | ✓ |
| Antigravity | `agy` | `AGY` | `agy --conversation <id>` | ✓ | — | — | ✓ |

`—` 表示这个 agent 没有经过验证的原生操作契约。重命名、归档、换目录和删除都直接修改对应 agent 的原生状态，不是 Another 私有标记；Another 只展示当前 agent 真正支持的操作，不会维护一份刷新后消失的私有状态。

CodeM 按工作目录字面路径的哈希查找会话。目录搬家或旧路径变成符号链接后，Another 会通过 CodeM 自己的 `LINCO_SESSIONS_ROOT` 给它一个只指向原项目哈希目录的恢复桥接：不复制、不移动 JSONL，也不会把另一个项目里的同名 ID 暴露给 CodeM。

各 agent 值得先了解的具体行为——哪些删除可以撤销、Codex 的三处标题与子 agent 会话、子会话、目录已不存在的会话——见 [`docs/providers.zh.md`](docs/providers.zh.md)。

检查本机安装状态：

```bash
another providers
another providers doctor
```

## AI 标题建议

如果 setup 中指定了 agent，按 `Ctrl+R` 会以原始标题打开重命名框，同时在后台请求该 agent 生成标题。规则由 another 自己执行，不依赖任何 Skill：中文为 `MMDD｜类型｜主题`，英文为 `MMDD｜Type｜Topic`；日期取自索引中的创建时间并转换到 `Asia/Shanghai`，不会交给模型猜测。

setup 第二页选 agent 和语言，按 `Enter` 进第三页选模型：模型列表由该 agent 的 CLI 自己给出（`pi --list-models`、`agy models`、`opencode models`、`opencode2 models`），输入任意字符即时过滤，第一行「默认模型」表示交给 CLI 自己决定，最后一行可以手输一个列表里还没有的模型名。Claude Code 和 Qwen Code 没有列模型的子命令，但各自的 headless 控制协议能返回当前配置可用的模型目录；another 只发控制请求，不发提示词，也不产生模型调用。Codex 没有可问的接口，直接进手输，页面会说明原因——列一份猜出来的模型 ID 只会让 `--model` 在重命名时才报错。

setup 第二页用 `←→` 选标题语言（与第一页的界面语言互不影响）：**Auto**（默认）、**English**、**中文**。Auto 看第一条有效用户消息：含汉字就用中文，否则用英文。日期和 `｜` 分隔符在三种语言下都不变；八类语义一一对应：功能／Feature、设计／Design、修复／Fix、优化／Optimize、发布／Release、探索／Explore、文档／Docs、研究／Research。

不符合格式的建议、关闭输入框后才到达的建议和失败请求都会被丢弃，不会覆盖已输入的文字。agent 在临时目录中运行，因此当前项目的指令不会进入标题请求。

需要一次整理多个标题时，用 `x` 标记会话（`X` 切换整页），再按 `Ctrl+T` 批量生成：建议并发产生并实时显示进度，`esc` 取消剩余任务。确认页只列出会变更的行，冻结、失败和无变化折成计数（`e` 展开）；按 `Enter` 应用全部变更，每行都会回读验证。

失败的行会自动重试一次（间隔 2 秒），只针对超时、限流、CLI 崩溃这类瞬时失败；CLI 未安装、该 agent 不能生成标题、缺少创建时间这些重试也不会变的失败直接落到确认页。确认页上按 `r` 重跑仍然失败的行和被 `esc` 中断的行，已经拿到的建议原样保留，不会重新花一次模型调用、也不会换一个新标题。应用阶段失败的行会保留标记，`Ctrl+T` 即可只重试它们。

批量页顶部标明这次用的 agent、模型和语言。生成结束或取消后按 `m` 打开和 setup 同一个模型选择器（同样是 CLI 自己给出的列表，输入过滤，最后一行可手输），`Enter` 用新模型重跑当前这批会话。这个模型不会写回配置：给几十条旧会话选一个便宜的模型，不该变成下次单条重命名的默认。

生成标题时，Codex、Antigravity、OpenCode 这些 CLI 会为每次无头调用留下一条自己的会话，而且没有关掉的开关。Claude Code 和 CodeM 有开关，another 就用它们：`--no-session-persistence` 和 `--no-session` 让这次运行根本不落盘，比事后认出来再清掉更干净（Claude Code 这一路同时带上 `--disable-slash-commands`，会话内容是不可信输入，不该由它触发命令或 Skill）。另一侧 another 会认出剩下这些残留并挡在索引之外，旧版本 Claude Code 留下的也一并清掉：提示词第一行是固定标记，运行目录是 `another-titler-*` 临时目录，两者任一命中即不入库。Antigravity 两条都躲得掉——它按模型答案给这次运行命名，于是残留顶着一个符合命名规则的标题，而且根本不记录运行目录——所以短到只可能是残留的会话会被打开，直接比对里面的提示词本身；长会话不会被读取。之前版本已经入库的残留会在下次刷新时清掉。清的只是 another 自己的索引，agent 自己的会话文件仍在它自己的目录里。

<img src="docs/assets/tui-batch.svg" width="100%" alt="another 批量命名确认页：原名到新名的对照表与折叠计数">

OpenCode 2 可以在首次自动命名时直接执行同一规则，而不额外调用一次模型。这个适配器随 another 二进制分发：在 `another setup` 第二页把 `OpenCode 2 标题插件` 那行打开，或运行 `another integrations install`，another 会把插件写进 OpenCode 2 的 `plugins/another-title-policy/` 并记下自己写了什么。它只写这一个目录，从不改你的 `opencode.json(c)`——OpenCode 2 自己就会发现该目录。升级 another 之后 `another integrations status` 会说明插件是否落后，`another setup` 再跑一次即可对齐；被你手工改过的文件不会被覆盖。源码与细节见 [`integrations/opencode2-title-policy/`](integrations/opencode2-title-policy/)。Pi 走同一条路：another 自带一个扩展，在 `another setup` 第二页打开 `Pi 标题扩展` 那行，或运行 `another integrations install pi`，它会被写进 Pi 的 `extensions/another-title-policy/`——Pi 自己会发现该目录，another 不改你的 `settings.json`。扩展本身不生成标题：一轮结束（`agent_settled`）时调用 `another rename --auto`，策略、语言、标题 agent 都留在二进制里。源码见 [`integrations/pi-title-policy/`](integrations/pi-title-policy/)。Claude Code 没有可覆盖的标题 agent，也没有"会话已命名"事件，只能在会话结束之后改名：[`integrations/claude-code-title-hook/`](integrations/claude-code-title-hook/) 里的 SessionEnd 钩子调用 `another rename --auto`，写的是 Claude Code 自己的 `custom-title` 记录，所以它自己的列表里也认。已经手工命名过的会话会跳过，手动标题不会被覆盖；需要手工装。Codex 暂无稳定的原生标题策略接口，仍由 another 整理。

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

# 换目录（同一个 agent，另一个项目目录）
another relocate <session-id> --to-dir <path> [--from ID] [--dry-run] [-y]
another relocate <session-id> --to-dir ../feature-worktree --move -y

# 改名（写进该 agent 自己的标题存储）
another rename <session-id> --title "0908｜功能｜标题策略" [--from ID]
another rename <session-id> --auto [--dry-run]   # 交给已配置的标题 agent

# 可移植备份
another export <session-id> -o session.another.json
another import session.another.json --to <provider> [--context MODE] [--dry-run] -y

# 配置和索引
another setup
another integrations                      # 适配器状态
another integrations install [agent...]  # 安装或更新适配器，不写 agent 就是全部
another integrations remove [agent...]
another index update
another index rebuild

# 项目换了目录
another paths
another paths link <旧目录> <新目录>
another paths unlink <旧目录>
```

会话属于它**开始**的目录。agent 每一轮都会记录当前工作目录，中途 `cd` 进子目录、临时目录或另一个仓库都不改变归属；按项目过滤时，一个目录连同其子目录（Git 仓库则连同全部注册 worktree）算作同一个项目。

重命名或移动项目后，早期会话仍指向 agent 当时记录的旧目录。`another paths` 列出这些已不存在的目录，并给出可核对的候选（同一 provider 存储目录中出现的新路径，或结尾路径段相同的现有目录）；`another paths link` 记录你的决定并重建索引投影。agent 写下的原始目录始终保留，`another paths unlink` 可随时还原。

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
- 确认框会说明该 agent 的删除能否撤销；只有能把同一个会话原样放回时才提供撤销，绝不用重新渲染的副本冒充。
- 换目录每次都从「复制」开始，「移动」需要显式切换；移动只在新位置校验通过之后才删除原文件。
- 能被准确识别的活动会话禁止重命名、归档、换目录和删除。
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
golangci-lint run ./...   # brew install golangci-lint
```

参与开发的文档：

- [`CONTRIBUTING.md`](CONTRIBUTING.md)：开发流程、提交前必须通过的检查、PR 需要说明什么。
- [`docs/architecture.md`](docs/architecture.md)：目录结构、数据流、索引 schema、摘要与安全规则。
- [`docs/adding-a-provider.md`](docs/adding-a-provider.md)：如何接入一个新的 agent。
- [`docs/providers.zh.md`](docs/providers.zh.md)：能力表背后各 agent 的具体行为。

重新生成视觉资产。栅格化字标已经提交到仓库，因此普通构建不需要安装字体。

```bash
./scripts/render-readme-assets.py                                  # TUI 预览 SVG
python3 ./scripts/render-motion-banner.py                          # 品牌横幅，需要 Pillow 和 ffmpeg
python3 ./scripts/render-goodbye-gif.py                            # 退出动效，需要 Pillow
python3 ./scripts/render-logo-face.py > internal/tui/logo_face.go  # 字标，需要 Pillow 和 JetBrains Mono ExtraBold
python3 ./scripts/render-mark-transparent.py                       # 标志透明版，从母版派生，需要 Pillow
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
    <img src="docs/assets/tui-goodbye.gif" width="500" alt="another 退出时打印的字标；一份紫色、一份薄荷绿的两份字标分列两侧，同时向中间靠拢，途中字标沿横向撕成错位的几条，两种颜色褪成两边共有的交叠青绿，最后重合成一个完整的青绿色字标">
  </picture>
</p>

<p align="center">
  <sub>退出时，字标会留在终端回滚区；把终端交给另一个 agent 时不会显示。<br>
  设置 <code>ANOTHER_NO_MOTION=1</code>、<code>NO_COLOR</code>、<code>CI</code> 或通过 pipe 输出时，使用静止帧。</sub>
</p>

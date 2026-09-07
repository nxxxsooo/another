package tui

import (
	"errors"
	"fmt"

	"github.com/nxxxsooo/another/internal/i18n"
	"github.com/nxxxsooo/another/internal/titler"
)

// uiText is every word another puts on its own screens. It is one flat struct
// rather than a key lookup so that adding a line is a compile-time obligation
// in both languages, and so a typo in a name cannot degrade quietly into a
// missing string at render time.
//
// Fields ending in Fmt are format strings; their verbs and order must match
// across languages. A few fields carry column widths instead of words, because
// a translated label that no longer fits its column is not translated, it is
// truncated.
type uiText struct {
	// Session rows.
	untitled          string
	messageCountFmt   string
	messageCountWidth int

	// Header and scope.
	sourceArrow    string
	targetArrow    string
	headerCountFmt string
	scopeProject   string
	scopeThis      string
	scopeAll       string

	// Empty states.
	emptySearch  string
	emptyProject string
	emptyAll     string

	// Footer.
	working         string
	indexing        string
	sessionCountFmt string
	markedFmt       string

	// Status and errors.
	cwdUnreadable        string
	archivedPrefix       string
	undoHint             string
	unarchivedPrefix     string
	renamedPrefix        string
	deletedPrefix        string
	copyHint             string
	migratedPrefix       string
	alreadyPrefix        string
	warningsFmt          string
	titleEmpty           string
	noSessionSelected    string
	noSuggestion         string
	resumeUnsupportedFmt string
	noResumeCommandFmt   string
	projectUnknown       string
	resumeCopied         string
	cannotArchiveRunning string
	archiveUnsupportedFm string
	cannotRenameRunning  string
	renameUnsupportedFmt string
	cannotDeleteRunning  string
	deleteUnsupportedFmt string
	terminalTooSmall     string

	// Modals.
	sourceModalTitle   string
	sourceModalHint    string
	targetModalTitle   string
	targetModalHint    string
	renameModalTitle   string
	renameModalHint    string
	renamePlaceholder  string
	suggestionLoading  string
	suggestionPrefix   string
	suggestionAccept   string
	suggestionFailed   string
	deleteConfirmTitle string
	deleteConfirmBody  string
	fieldSource        string
	fieldTitle         string
	fieldDirectory     string
	choiceCancel       string
	choiceDelete       string

	// Batch rename.
	batchNeedsModel      string
	batchNeedsMarks      string
	batchMarksMissing    string
	batchCancelling      string
	batchNothingToRetry  string
	batchCancelFirst     string
	batchNoChanges       string
	batchRetryMissing    string
	batchModelUnchanged  string
	batchPartialFmt      string
	batchRenamedFmt      string
	batchAllFailedFmt    string
	batchNoneApplied     string
	someRowsFailed       string
	allRowsFailed        string
	rowNotInList         string
	rowRenameUnsupported string
	rowTitleMismatch     string
	batchTitle           string
	batchModelLabel      string
	batchModelHelp       string
	batchProgressFmt     string
	batchCancelProgress  string
	batchConcurrencyNote string
	batchCountsFmt       string
	batchMoreRowsFmt     string
	batchExpandHint      string
	batchRetryHintFmt    string
	batchModelSource     string
	batchModelTemporary  string
	rowFailed            string
	rowFrozen            string
	rowUnchanged         string
	defaultModel         string

	// Freeze reasons, keyed by the engine's identifier.
	freezeMissingCreatedAt   string
	freezeCancelled          string
	freezeDuplicateTitle     string
	freezeNotIndexed         string
	freezeCurrentSession     string
	freezeRenameUnsupported  string
	freezeSuggestUnsupported string

	// The footer key hints. The list hint is assembled from a base, one
	// fragment per action the selected provider actually supports, and a
	// tail, so an unsupported action is never advertised.
	helpSource           string
	helpTarget           string
	helpPreview          string
	helpDelete           string
	helpRenameSuggestion string
	helpRename           string
	helpBatchModelList   string
	helpBatchModelTyped  string
	helpBatchRunning     string
	helpBatchReview      string
	helpSearch           string
	helpResume           string
	helpArchived         string
	helpListBase         string
	helpListRename       string
	helpListArchive      string
	helpListDelete       string
	helpListTail         string

	// Setup, page one.
	setupAgentsTitle  string
	setupAgentsHint   string
	setupCLIFound     string
	setupCLIMissing   string
	setupCLIWidth     int
	setupSessionsFmt  string
	setupNoData       string
	setupNameWidth    int
	setupUnavailable  string
	setupPickOne      string
	setupAgentsHelp   string
	setupFoldExpand   string
	setupFoldCollapse string
	setupFoldLabelFmt string
	// setupFoldLabelOneFmt is the same line when the fold holds exactly
	// one agent. English needs it; Chinese repeats the plural form.
	setupFoldLabelOneFmt string
	setupFoldHintFmt     string
	setupInterface       string

	// Setup, page two.
	setupTitleTitle     string
	setupTitleHint      string
	setupTitleNone      string
	setupTitleNoneHelp  string
	setupTitleOff       string
	setupTitleLanguage  string
	setupTitlePolicy    string
	setupTitleHelpModel string
	setupTitleHelpSave  string

	// Setup, page three, shared with the batch model picker.
	setupModelTitle       string
	setupModelHintFmt     string
	setupModelLoadingFmt  string
	setupModelBack        string
	setupModelLabel       string
	setupModelHelpBack    string
	setupModelHelpList    string
	setupModelMoreFmt     string
	setupModelCustom      string
	setupModelFilter      string
	setupModelHelp        string
	modelListUnsupported  string
	modelListNoTitles     string
	modelListNotInstalled string
	modelListTimedOut     string
	modelListEmpty        string
	modelListFailedFmt    string
	suggestNoCreatedAt    string
	suggestNoTitles       string
	suggestNotInstalled   string
	suggestTimedOut       string
	suggestFailedFmt      string
	modelPlaceholder      string
}

// englishText is the default. It is also the reference: a new line is written
// here first, and the Chinese entry is a translation of it rather than the
// other way around, so the two never drift into different features.
var englishText = uiText{
	untitled:          "(untitled)",
	messageCountFmt:   "%d msg",
	messageCountWidth: 9,

	sourceArrow:    "← source ",
	targetArrow:    "target →",
	headerCountFmt: "   │   %d sessions   │   ",
	scopeProject:   "project",
	scopeThis:      "this project",
	scopeAll:       "all",

	emptySearch:  "\n  No session matches",
	emptyProject: "\n  No sessions in this project\n  Press f to see all",
	emptyAll:     "\n  No sessions",

	working:         " working…",
	indexing:        "indexing…",
	sessionCountFmt: "%d sessions",
	markedFmt:       "%d marked  ·  x mark · X all · ctrl+t batch",

	cwdUnreadable:        "Could not read the current directory, showing every session: ",
	archivedPrefix:       "Archived ",
	undoHint:             "  ·  a undo",
	unarchivedPrefix:     "Unarchived ",
	renamedPrefix:        "Renamed to ",
	deletedPrefix:        "Deleted ",
	copyHint:             "  ·  c copy",
	migratedPrefix:       "Migrated to ",
	alreadyPrefix:        "Already on ",
	warningsFmt:          "  ·  %d warning(s)",
	titleEmpty:           "A title cannot be empty",
	noSessionSelected:    "No session selected",
	noSuggestion:         "No suggestion available",
	resumeUnsupportedFmt: "%s cannot be opened directly",
	noResumeCommandFmt:   "%s has no resume command",
	projectUnknown:       "Could not determine the current project",
	resumeCopied:         "Resume command copied",
	cannotArchiveRunning: "The session running right now cannot be archived",
	archiveUnsupportedFm: "%s does not support archiving",
	cannotRenameRunning:  "The session running right now cannot be renamed",
	renameUnsupportedFmt: "%s does not support renaming",
	cannotDeleteRunning:  "The session running right now cannot be deleted",
	deleteUnsupportedFmt: "%s does not support deleting",
	terminalTooSmall:     "Terminal too small — resize to at least 48x20",

	sourceModalTitle:   "Source",
	sourceModalHint:    "Which agent is this session from?",
	targetModalTitle:   "Target",
	targetModalHint:    "Which agent should carry this session?",
	renameModalTitle:   "Rename session",
	renameModalHint:    "Written back as the source agent's own native title",
	renamePlaceholder:  "New session title",
	suggestionLoading:  "asking for a title…",
	suggestionPrefix:   "suggested ",
	suggestionAccept:   "  · tab accepts",
	suggestionFailed:   "no suggestion: ",
	deleteConfirmTitle: "Delete session?",
	deleteConfirmBody:  "This deletes the original session inside the source agent. It cannot be undone.",
	fieldSource:        "Source",
	fieldTitle:         "Title",
	fieldDirectory:     "Directory",
	choiceCancel:       "Cancel",
	choiceDelete:       "Delete",

	batchNeedsModel:      "Configure a title model in setup before renaming in bulk",
	batchNeedsMarks:      "Mark sessions with x, then press ctrl+t",
	batchMarksMissing:    "None of the marked sessions are in this list",
	batchCancelling:      "Cancelling the remaining rows…",
	batchNothingToRetry:  "No row is worth retrying",
	batchCancelFirst:     "Press esc to stop generating, then change the model",
	batchNoChanges:       "No title change to apply",
	batchRetryMissing:    "The rows to retry are no longer in this batch",
	batchModelUnchanged:  "Same model — keeping the current results",
	batchPartialFmt:      "Renamed %d, failed %d: %s · failed rows stay marked, ctrl+t retries",
	batchRenamedFmt:      "Renamed %d",
	batchAllFailedFmt:    "Batch rename failed on %d: %s · marks kept, ctrl+t retries",
	batchNoneApplied:     "No title change was applied",
	someRowsFailed:       "some rows failed",
	allRowsFailed:        "every row failed",
	rowNotInList:         "session is no longer in the list",
	rowRenameUnsupported: "source does not support renaming",
	rowTitleMismatch:     "title read back did not match",
	batchTitle:           "Rename sessions in bulk",
	batchModelLabel:      "Model",
	batchModelHelp:       "enter reruns on this model  ·  esc cancel",
	batchProgressFmt:     "generating %d/%d · esc cancels",
	batchCancelProgress:  "cancelling %d/%d · waiting for the rest to stop",
	batchConcurrencyNote: "4 at a time · every row is its own agent call, so slow is normal.",
	batchCountsFmt:       "%d to apply · %d frozen · %d failed · %d unchanged",
	batchMoreRowsFmt:     "+ %d more",
	batchExpandHint:      "e expands the rest",
	batchRetryHintFmt:    "r retries %d",
	batchModelSource:     "Written by",
	batchModelTemporary:  "  this run only",
	rowFailed:            "failed ",
	rowFrozen:            "frozen ",
	rowUnchanged:         "unchanged ",
	defaultModel:         "default model",

	freezeMissingCreatedAt:   "no creation time",
	freezeCancelled:          "cancelled",
	freezeDuplicateTitle:     "duplicate title in this batch",
	freezeNotIndexed:         "no longer in the index",
	freezeCurrentSession:     "this session is not renamed",
	freezeRenameUnsupported:  "source does not support renaming",
	freezeSuggestUnsupported: "agent cannot suggest titles",

	helpSource:           " ↑↓ source · →/enter apply · esc cancel",
	helpTarget:           " ↑↓ target · enter migrate · esc cancel",
	helpPreview:          " ↑↓ scroll · esc close",
	helpDelete:           " ←→ choose · enter confirm · esc cancel",
	helpRenameSuggestion: " type a title · tab uses the suggestion · enter save · esc cancel",
	helpRename:           " type a title · enter save · esc cancel",
	helpBatchModelList:   " ↑↓ model · type to filter · enter rerun · esc cancel",
	helpBatchModelTyped:  " type a model · enter rerun · esc cancel",
	helpBatchRunning:     " generating · esc stops the rest",
	helpBatchReview:      " enter apply · r retry failures · m change model · e expand rest · esc close",
	helpSearch:           " enter search · esc cancel",
	helpResume:           " enter open that agent · c copy command · esc keep browsing · q quit",
	helpArchived:         " a undo archive · esc keep it · ↑↓ keep browsing",
	helpListBase:         " ← source · ↑↓ session · enter open · → other agent · space preview · f scope",
	helpListRename:       " · ctrl+r rename",
	helpListArchive:      " · a archive",
	helpListDelete:       " · ctrl+d delete",
	helpListTail:         " · x mark · X all · ctrl+t batch · / search · r refresh",

	setupAgentsTitle:     "Choose your agents",
	setupAgentsHint:      "Space toggles an agent; Shift+↑↓ reorders them.",
	setupCLIFound:        "CLI found",
	setupCLIMissing:      "no CLI",
	setupCLIWidth:        11,
	setupSessionsFmt:     "%d sessions",
	setupNoData:          "no session data",
	setupNameWidth:       16,
	setupUnavailable:     "%s: no CLI and no session data found",
	setupPickOne:         "Choose at least one agent",
	setupAgentsHelp:      "↑↓ move  ·  space toggle  ·  enter next  ·  esc cancel",
	setupFoldExpand:      "expand",
	setupFoldCollapse:    "collapse",
	setupFoldLabelFmt:    "%s %d adapters, not tested each release",
	setupFoldLabelOneFmt: "%s %d adapter, not tested each release",
	setupFoldHintFmt:     "  ·  space %s",
	setupInterface:       "Interface",

	setupTitleTitle:     "AI title suggestions",
	setupTitleHint:      "Which installed agent writes a title when you press ctrl+r.",
	setupTitleNone:      "None of the selected agents has a CLI that can write titles, so this stays off.",
	setupTitleNoneHelp:  "enter save  ·  esc back",
	setupTitleOff:       "Off",
	setupTitleLanguage:  "Title language",
	setupTitlePolicy:    "Suggestions are off; the language is still shared with O2 and Pi native naming.",
	setupTitleHelpModel: "↑↓ agent  ·  ←→ language  ·  enter model  ·  esc back",
	setupTitleHelpSave:  "↑↓ agent  ·  ←→ language  ·  enter save  ·  esc back",

	setupModelTitle:       "Which model writes the titles",
	setupModelHintFmt:     "The models %s reports; the default leaves the choice to that CLI.",
	setupModelLoadingFmt:  "asking %s for its models…",
	setupModelBack:        "esc back",
	setupModelLabel:       "Model",
	setupModelHelpBack:    "enter save  ·  esc back one step",
	setupModelHelpList:    "enter save  ·  esc back to the list",
	setupModelMoreFmt:     "+ %d more, keep typing to filter",
	setupModelCustom:      "a model name you type",
	setupModelFilter:      "filter: ",
	setupModelHelp:        "↑↓ model  ·  type to filter  ·  enter save  ·  esc back",
	modelListUnsupported:  "%s cannot list its models",
	modelListNoTitles:     "%s cannot write titles",
	modelListNotInstalled: "%s is not installed",
	modelListTimedOut:     "%s took too long to answer",
	modelListEmpty:        "%s named no models",
	modelListFailedFmt:    "%s: %s",
	suggestNoCreatedAt:    "the session has no creation time",
	suggestNoTitles:       "%s cannot write titles",
	suggestNotInstalled:   "%s is not installed",
	suggestTimedOut:       "%s took too long to answer",
	suggestFailedFmt:      "%s: %s",
	modelPlaceholder:      "empty uses that CLI's default model",
}

// chineseText is the interface another shipped with before it had a second
// language. Full-width punctuation is deliberate: these are Chinese sentences,
// not English sentences with Chinese words in them.
var chineseText = uiText{
	untitled:          "(未命名)",
	messageCountFmt:   "%d条",
	messageCountWidth: 7,

	sourceArrow:    "← 来源 ",
	targetArrow:    "去向 →",
	headerCountFmt: "   │   %d 个会话   │   ",
	scopeProject:   "项目",
	scopeThis:      "当前项目",
	scopeAll:       "全部",

	emptySearch:  "\n  没有匹配的会话",
	emptyProject: "\n  当前项目没有会话\n  按 f 查看全部",
	emptyAll:     "\n  没有会话",

	working:         " 处理中…",
	indexing:        "正在建立索引…",
	sessionCountFmt: "%d 个会话",
	markedFmt:       "已标记 %d 个会话  ·  x 标记 · X 全选 · ctrl+t 批量命名",

	cwdUnreadable:        "无法读取当前目录，已显示全部会话：",
	archivedPrefix:       "已归档 ",
	undoHint:             "  ·  a 撤销",
	unarchivedPrefix:     "已取消归档 ",
	renamedPrefix:        "已重命名为 ",
	deletedPrefix:        "已删除 ",
	copyHint:             "  ·  c 复制",
	migratedPrefix:       "已迁移到 ",
	alreadyPrefix:        "已存在于 ",
	warningsFmt:          "  ·  %d 条警告",
	titleEmpty:           "标题不能为空",
	noSessionSelected:    "没有选中的会话",
	noSuggestion:         "没有可用建议",
	resumeUnsupportedFmt: "%s 不支持直接进入",
	noResumeCommandFmt:   "%s 没有可用的 resume 命令",
	projectUnknown:       "无法确定当前项目",
	resumeCopied:         "已复制 resume 命令",
	cannotArchiveRunning: "不能归档当前正在运行的会话",
	archiveUnsupportedFm: "%s 不支持归档",
	cannotRenameRunning:  "不能重命名当前正在运行的会话",
	renameUnsupportedFmt: "%s 不支持重命名",
	cannotDeleteRunning:  "不能删除当前正在运行的会话",
	deleteUnsupportedFmt: "%s 不支持删除",
	terminalTooSmall:     "终端太小 — 请调整到至少 48x20",

	sourceModalTitle:   "选择来源",
	sourceModalHint:    "会话来自哪个 agent？",
	targetModalTitle:   "选择去向",
	targetModalHint:    "把这条会话带到哪个 agent？",
	renameModalTitle:   "重命名会话",
	renameModalHint:    "写回来源 agent 的原生标题",
	renamePlaceholder:  "新的会话标题",
	suggestionLoading:  "AI 建议生成中…",
	suggestionPrefix:   "建议 ",
	suggestionAccept:   "  · tab 接受",
	suggestionFailed:   "建议不可用：",
	deleteConfirmTitle: "删除会话？",
	deleteConfirmBody:  "该操作会删除来源 agent 中的原始会话，无法撤销。",
	fieldSource:        "来源",
	fieldTitle:         "标题",
	fieldDirectory:     "目录",
	choiceCancel:       "取消",
	choiceDelete:       "删除",

	batchNeedsModel:      "先在设置里配置标题模型，才能批量命名",
	batchNeedsMarks:      "x 标记会话后，再用 ctrl+t 批量命名",
	batchMarksMissing:    "标记的会话都不在当前列表中",
	batchCancelling:      "正在取消剩余任务…",
	batchNothingToRetry:  "没有可重试的行",
	batchCancelFirst:     "先 esc 取消生成，再换模型",
	batchNoChanges:       "没有可应用的标题变更",
	batchRetryMissing:    "重试的会话都不在这批里了",
	batchModelUnchanged:  "模型未变，保留现有结果",
	batchPartialFmt:      "已重命名 %d 条，失败 %d 条：%s · 失败行仍有标记，ctrl+t 重试",
	batchRenamedFmt:      "已重命名 %d 条",
	batchAllFailedFmt:    "批量重命名失败 %d 条：%s · 标记保留，ctrl+t 重试",
	batchNoneApplied:     "没有应用任何标题变更",
	someRowsFailed:       "部分行失败",
	allRowsFailed:        "全部失败",
	rowNotInList:         "会话已不在列表中",
	rowRenameUnsupported: "该来源不支持重命名",
	rowTitleMismatch:     "回读标题不一致",
	batchTitle:           "批量命名会话",
	batchModelLabel:      "模型",
	batchModelHelp:       "enter 换模型并重跑  ·  esc 取消",
	batchProgressFmt:     "正在生成 %d/%d · esc 取消",
	batchCancelProgress:  "正在取消 %d/%d · 等待剩余任务退出",
	batchConcurrencyNote: "4 路并发 · 每条都是独立的 agent 调用，慢是正常的。",
	batchCountsFmt:       "可应用 %d 条 · 冻结 %d · 失败 %d · 无变化 %d",
	batchMoreRowsFmt:     "+ 还有 %d 条",
	batchExpandHint:      "e 展开其余行",
	batchRetryHintFmt:    "r 重试 %d 行",
	batchModelSource:     "模型来源",
	batchModelTemporary:  "  本次临时",
	rowFailed:            "失败 ",
	rowFrozen:            "冻结 ",
	rowUnchanged:         "无变化 ",
	defaultModel:         "默认模型",

	freezeMissingCreatedAt:   "缺少创建时间",
	freezeCancelled:          "已取消",
	freezeDuplicateTitle:     "批内标题重复",
	freezeNotIndexed:         "会话已不在索引中",
	freezeCurrentSession:     "当前会话不改名",
	freezeRenameUnsupported:  "该来源不支持重命名",
	freezeSuggestUnsupported: "该 agent 不支持标题建议",

	helpSource:           " ↑↓ 选来源 · →/enter 应用 · esc 取消",
	helpTarget:           " ↑↓ 选去向 · enter 迁移 · esc 取消",
	helpPreview:          " ↑↓ 滚动 · esc 关闭",
	helpDelete:           " ←→ 选择 · enter 确认 · esc 取消",
	helpRenameSuggestion: " 输入新标题 · tab 用建议 · enter 保存 · esc 取消",
	helpRename:           " 输入新标题 · enter 保存 · esc 取消",
	helpBatchModelList:   " ↑↓ 选模型 · 输入过滤 · enter 换模型重跑 · esc 取消",
	helpBatchModelTyped:  " 输入模型名 · enter 换模型重跑 · esc 取消",
	helpBatchRunning:     " 生成中 · esc 取消剩余任务",
	helpBatchReview:      " enter 应用变更 · r 重试失败 · m 换模型 · e 展开其余 · esc 关闭",
	helpSearch:           " enter 搜索 · esc 取消",
	helpResume:           " enter 进入该 agent · c 复制命令 · esc 继续浏览 · q 退出",
	helpArchived:         " a 撤销归档 · esc 放弃撤销 · ↑↓ 继续浏览",
	helpListBase:         " ← 来源 · ↑↓ 选会话 · enter 进入 · → 跨 agent · space 预览 · f 范围",
	helpListRename:       " · ctrl+r 重命名",
	helpListArchive:      " · a 归档",
	helpListDelete:       " · ctrl+d 删除",
	helpListTail:         " · x 标记 · X 全选 · ctrl+t 批量 · / 搜索 · r 刷新",

	setupAgentsTitle:     "选择你使用的 agent",
	setupAgentsHint:      "Space 开关 agent；Shift+↑↓ 调整显示顺序。",
	setupCLIFound:        "CLI 已安装",
	setupCLIMissing:      "CLI 未安装",
	setupCLIWidth:        12,
	setupSessionsFmt:     "%d 个会话",
	setupNoData:          "无会话数据",
	setupNameWidth:       16,
	setupUnavailable:     "%s 未检测到 CLI 或会话数据",
	setupPickOne:         "至少选择一个 agent",
	setupAgentsHelp:      "↑↓ 移动  ·  space 开关  ·  enter 下一步  ·  esc 取消",
	setupFoldExpand:      "展开",
	setupFoldCollapse:    "收起",
	setupFoldLabelFmt:    "%s 其他 %d 个兼容适配（非每次发布实测）",
	setupFoldLabelOneFmt: "%s 其他 %d 个兼容适配（非每次发布实测）",
	setupFoldHintFmt:     "  ·  space %s",
	setupInterface:       "界面语言",

	setupTitleTitle:     "重命名时的 AI 标题建议",
	setupTitleHint:      "按 ctrl+r 时调用哪个已装 agent 生成候选标题。",
	setupTitleNone:      "已选的 agent 里没有能生成标题的 CLI，此功能保持关闭。",
	setupTitleNoneHelp:  "enter 保存  ·  esc 返回",
	setupTitleOff:       "不启用",
	setupTitleLanguage:  "标题语言",
	setupTitlePolicy:    "建议模型关闭；语言仍供 O2／Pi 原生命名共用。",
	setupTitleHelpModel: "↑↓ 选 agent  ·  ←→ 选语言  ·  enter 选模型  ·  esc 返回",
	setupTitleHelpSave:  "↑↓ 选 agent  ·  ←→ 选语言  ·  enter 保存  ·  esc 返回",

	setupModelTitle:       "用哪个模型写标题",
	setupModelHintFmt:     "%s 报告的可用模型，留在默认即由该 CLI 自己决定。",
	setupModelLoadingFmt:  "正在向 %s 获取模型列表…",
	setupModelBack:        "esc 返回",
	setupModelLabel:       "模型",
	setupModelHelpBack:    "enter 保存  ·  esc 返回上一步",
	setupModelHelpList:    "enter 保存  ·  esc 回到列表",
	setupModelMoreFmt:     "+ 还有 %d 个，继续输入可过滤",
	setupModelCustom:      "自定义模型名",
	setupModelFilter:      "过滤：",
	setupModelHelp:        "↑↓ 选模型  ·  输入过滤  ·  enter 保存  ·  esc 返回",
	modelListUnsupported:  "%s 不支持列出模型",
	modelListNoTitles:     "%s 不能生成标题",
	modelListNotInstalled: "%s 未安装",
	modelListTimedOut:     "%s 获取模型超时",
	modelListEmpty:        "%s 没有返回可用模型",
	modelListFailedFmt:    "%s：%s",
	suggestNoCreatedAt:    "会话缺少创建时间",
	suggestNoTitles:       "%s 不能生成标题",
	suggestNotInstalled:   "%s 未安装",
	suggestTimedOut:       "%s 生成超时",
	suggestFailedFmt:      "%s：%s",
	modelPlaceholder:      "留空用该 CLI 的默认模型",
}

// txt is what every screen reads. It is a package-level value for the same
// reason the resolved language is: the words are needed in delegates, free
// functions, and two separate Bubble Tea programs, and a parameter threaded
// through all of them would only restate what one process-wide preference
// already decided.
var txt = &englishText

// SetLanguage resolves the stored preference into the language every screen
// and the Pi bridge turn will speak. It is called once, on startup, with
// whatever configuration held — including nothing at all, which is auto.
func SetLanguage(preference string) {
	applyLanguage(i18n.Lang(preference))
}

// applyLanguage points the interface at one of the catalogs. It returns the
// previous language so a caller that switches temporarily — a test rendering
// both, mostly — can put it back.
func applyLanguage(l i18n.Lang) i18n.Lang {
	previous := i18n.SetCurrent(l)
	switch i18n.Current() {
	case i18n.LangChinese:
		txt = &chineseText
	default:
		txt = &englishText
	}
	return previous
}

// listErrorText says why a model listing failed, in the current language. An
// error from anywhere else is shown as itself: the picker's own failures are
// the ones another wrote, and a CLI's message is the CLI's to word.
func listErrorText(err error) string {
	var listErr *titler.ListError
	if !errors.As(err, &listErr) {
		return err.Error()
	}
	switch listErr.Reason {
	case titler.ListUnsupported:
		return fmt.Sprintf(txt.modelListUnsupported, listErr.Command)
	case titler.ListNoTitles:
		return fmt.Sprintf(txt.modelListNoTitles, listErr.Command)
	case titler.ListNotInstalled:
		return fmt.Sprintf(txt.modelListNotInstalled, listErr.Command)
	case titler.ListTimedOut:
		return fmt.Sprintf(txt.modelListTimedOut, listErr.Command)
	case titler.ListEmpty:
		return fmt.Sprintf(txt.modelListEmpty, listErr.Command)
	default:
		return fmt.Sprintf(txt.modelListFailedFmt, listErr.Command, listErr.Detail)
	}
}

// suggestErrorText says why one title suggestion failed, in the current
// language. It is shown under the rename box and beside a failed batch row.
func suggestErrorText(err error) string {
	var suggestErr *titler.SuggestError
	if !errors.As(err, &suggestErr) {
		return err.Error()
	}
	switch suggestErr.Reason {
	case titler.SuggestNoCreatedAt:
		return txt.suggestNoCreatedAt
	case titler.SuggestNoTitles:
		return fmt.Sprintf(txt.suggestNoTitles, suggestErr.Command)
	case titler.SuggestNotInstalled:
		return fmt.Sprintf(txt.suggestNotInstalled, suggestErr.Command)
	case titler.SuggestTimedOut:
		return fmt.Sprintf(txt.suggestTimedOut, suggestErr.Command)
	default:
		return fmt.Sprintf(txt.suggestFailedFmt, suggestErr.Command, suggestErr.Detail)
	}
}

// freezeText says why a row was refused, in the current language. An
// identifier with no translation is shown as itself rather than dropped: a row
// with a blank reason would look like a bug in the batch rather than in this
// table.
func freezeText(reason titler.FreezeReason) string {
	switch reason {
	case titler.FreezeMissingCreatedAt:
		return txt.freezeMissingCreatedAt
	case titler.FreezeCancelled:
		return txt.freezeCancelled
	case titler.FreezeDuplicateTitle:
		return txt.freezeDuplicateTitle
	case titler.FreezeNotIndexed:
		return txt.freezeNotIndexed
	case titler.FreezeCurrentSession:
		return txt.freezeCurrentSession
	case titler.FreezeRenameUnsupported:
		return txt.freezeRenameUnsupported
	case titler.FreezeSuggestUnsupported:
		return txt.freezeSuggestUnsupported
	default:
		return string(reason)
	}
}

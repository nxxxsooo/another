package util

import "strings"

const defaultTitleLen = 80

// TitlePicker picks a session title from user messages; plain text beats slash commands.
type TitlePicker struct {
	plain, slash string
	max          int
}

func NewTitlePicker(maxLen int) *TitlePicker {
	if maxLen <= 0 {
		maxLen = defaultTitleLen
	}
	return &TitlePicker{max: maxLen}
}

func (p *TitlePicker) Note(text string) {
	t, ok := UserTitleFromText(text)
	if !ok {
		return
	}
	if strings.HasPrefix(strings.TrimSpace(t), "/") {
		if p.slash == "" {
			p.slash = t
		}
		return
	}
	if p.plain == "" {
		p.plain = t
	}
}

func (p *TitlePicker) Title() string {
	if p.plain != "" {
		return p.plain
	}
	return p.slash
}

func (p *TitlePicker) TitleOr(fallback string) string {
	if t := p.Title(); t != "" {
		return t
	}
	return fallback
}

// UserTitleFromText extracts a display title from one user message.
func UserTitleFromText(text string) (string, bool) {
	text, ok := NormalizeUserText(text)
	if !ok {
		return "", false
	}
	if name := extractXMLTag(text, "command-name"); name != "" {
		name = strings.TrimSpace(name)
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		if IsThinSlashTitle(name) {
			return "", false
		}
		return FirstUserSnippet(name, defaultTitleLen), true
	}
	if msg := extractXMLTag(text, "command-message"); msg != "" {
		cmd := "/" + strings.TrimSpace(msg)
		if IsThinSlashTitle(cmd) {
			return "", false
		}
		return FirstUserSnippet(cmd, defaultTitleLen), true
	}
	if strings.HasPrefix(text, "/") {
		if IsThinSlashTitle(text) {
			return "", false
		}
		return FirstUserSnippet(text, defaultTitleLen), true
	}
	return FirstUserSnippet(text, defaultTitleLen), true
}

// NormalizeUserText removes provider transport wrappers from a real prompt.
func NormalizeUserText(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<timestamp>") {
		if end := strings.Index(text, "</timestamp>"); end >= 0 {
			text = strings.TrimSpace(text[end+len("</timestamp>"):])
		}
	}
	if query := extractXMLTag(text, "user_query"); query != "" {
		text = strings.TrimSpace(query)
	}
	return text, text != "" && !SkipUserMessage(text)
}

// DisplayUserText removes provider transport wrappers without changing stored content.
func DisplayUserText(text string) string {
	if normalized, ok := NormalizeUserText(text); ok {
		return normalized
	}
	return text
}

// SkipUserMessage reports agent-injected user lines that are not real prompts.
func SkipUserMessage(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	for _, prefix := range []string{
		"[Request interrupted by user",
		"<local-command-caveat>",
		"<local-command-stdout>",
		"<task-notification>",
		"<user_query>",
		"<system_reminder>",
		"<agent_transcripts>",
		"<environment_context>",
		"<skill>",
		"# AGENTS.md",
		"<INSTRUCTIONS>",
		"<permissions instructions>",
		"<collaboration_mode>",
		"<skills_instructions>",
		"<manually_attached_skills>",
		"<image name=",
		"Read HEARTBEAT.md",
		"You are being used as the model planner",
		"Sender (untrusted metadata)",
		"The following is the Codex agent history",
	} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// IsThinSlashTitle is true for Claude/Cursor UI setup commands, not session topics.
func IsThinSlashTitle(cmd string) bool {
	cmd = strings.ToLower(strings.Fields(strings.TrimSpace(cmd))[0])
	switch cmd {
	case "/model", "/login", "/effort", "/copy":
		return true
	default:
		return false
	}
}

// IsWeakStoredTitle is true when a DB-stored title should be replaced from messages.
func IsWeakStoredTitle(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return true
	}
	if IsThinSlashTitle(title) {
		return true
	}
	if strings.HasPrefix(title, "<") {
		return true
	}
	return false
}

// PickStoredOrMessages uses a DB title when strong, else scans user message lines.
func PickStoredOrMessages(stored string, userLines []string) string {
	if stored != "" && !IsWeakStoredTitle(stored) {
		if t, ok := UserTitleFromText(stored); ok {
			return t
		}
		return FirstUserSnippet(stored, defaultTitleLen)
	}
	picker := NewTitlePicker(defaultTitleLen)
	for _, line := range userLines {
		picker.Note(line)
	}
	if t := picker.Title(); t != "" {
		return t
	}
	if stored != "" {
		return FirstUserSnippet(stored, defaultTitleLen)
	}
	return ""
}

func extractXMLTag(s, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	i += len(open)
	j := strings.Index(s[i:], close)
	if j < 0 {
		return ""
	}
	return s[i : i+j]
}

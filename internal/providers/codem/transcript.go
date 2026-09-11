package codem

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/util"
)

// The record types another reads. A CodeM transcript also carries thinking,
// tool calls and results, per-turn requests and responses, token usage, hook
// runs, todos and checkpoints; none of that is portable conversation, so it is
// read past rather than projected into a message.
const (
	recordHeader        = "header"
	recordUserMessage   = "user_message"
	recordAssistantText = "assistant_text"
)

// writeFile is replaced in tests that need a write to fail.
var writeFile = util.WriteFileAtomic

type record struct {
	Type      string `json:"type"`
	At        string `json:"at"`
	SessionID string `json:"session_id"`
	StartedAt string `json:"started_at"`
	CWD       string `json:"cwd"`
	Content   string `json:"content"`
	Text      string `json:"text"`
	NewTitle  string `json:"new_title"`
	RecordSeq uint64 `json:"record_seq"`
}

type transcript struct {
	id        string
	project   string
	title     string
	startedAt time.Time
	firstAt   time.Time
	lastAt    time.Time
	messages  []model.Message
	migration *model.MigrationMeta
	recordSeq uint64
}

// created prefers the header's own start over the first message that survived
// the read, because a preview window may have dropped that message and because
// a session can be opened well before anything is said in it.
func (t transcript) created(info os.FileInfo) time.Time {
	if !t.startedAt.IsZero() {
		return t.startedAt
	}
	if !t.firstAt.IsZero() {
		return t.firstAt
	}
	if info != nil {
		return info.ModTime()
	}
	return time.Time{}
}

func (t transcript) updated(info os.FileInfo) time.Time {
	if !t.lastAt.IsZero() {
		return t.lastAt
	}
	if info != nil {
		return info.ModTime()
	}
	return time.Time{}
}

// scan reads one transcript. keepLast, when positive, bounds how many messages
// are held: older ones are dropped as the file is read, so a preview of a long
// session does not materialize the whole conversation.
func scan(ctx context.Context, path string, keepLast int) (transcript, error) {
	out := transcript{id: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	origin := util.NewOriginDirectory("")
	picker := util.NewTitlePicker(80)
	err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if meta, ok := model.ParseMigrationMeta(line); ok {
			out.migration = meta
		}
		var row record
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if row.RecordSeq > out.recordSeq {
			out.recordSeq = row.RecordSeq
		}
		switch row.Type {
		case recordHeader:
			// The id comes from the file name rather than from this record:
			// `codem --resume` resolves a session by looking for that name in
			// the current directory's hash, so the name is the id that works.
			// Resuming also appends a second header for the same session, so
			// the earliest start wins and the later ones only confirm it.
			origin.Note(row.CWD)
			if started := util.ParseTime(row.StartedAt); !started.IsZero() {
				if out.startedAt.IsZero() || started.Before(out.startedAt) {
					out.startedAt = started
				}
			}
		case recordUserMessage:
			text := strings.TrimSpace(row.Content)
			if text == "" || skipUserMessage(text) {
				return nil
			}
			picker.Note(text)
			out.append(model.RoleUser, text, util.ParseTime(row.At), keepLast)
		case recordAssistantText:
			// One assistant turn can emit text, call a tool, and speak again.
			// Each of those is its own record and stays its own message: the
			// tool call between them is real, and merging across it would
			// invent a paragraph the agent never wrote in one breath.
			text := strings.TrimSpace(row.Text)
			if text == "" {
				return nil
			}
			out.append(model.RoleAssistant, text, util.ParseTime(row.At), keepLast)
		case recordSessionRenamed:
			if title := strings.TrimSpace(row.NewTitle); title != "" {
				out.title = title
			}
		}
		return nil
	})
	if err != nil {
		return transcript{}, err
	}
	out.project = origin.Path()
	if out.title == "" {
		out.title = picker.TitleOr("(codem session)")
	}
	return out, nil
}

func (t *transcript) append(role model.Role, text string, ts time.Time, keepLast int) {
	if !ts.IsZero() {
		if t.firstAt.IsZero() {
			t.firstAt = ts
		}
		t.lastAt = ts
	}
	t.messages = append(t.messages, model.Message{Role: role, Content: text, Timestamp: ts})
	if keepLast > 0 && len(t.messages) > keepLast {
		t.messages = t.messages[len(t.messages)-keepLast:]
	}
}

// skipUserMessage drops the user turns CodeM writes on the user's behalf. A
// loaded skill or a reminder arrives as a whole user message wrapped in
// <system-reminder>; no real prompt follows the closing tag, so the record is
// dropped rather than trimmed.
func skipUserMessage(text string) bool {
	return util.SkipUserMessage(text) || strings.HasPrefix(text, "<system-reminder>")
}

// Relocation is absent by contract, not by omission. The
// directory a session belongs to is the hash its transcript sits under, and
// CodeM has no operation that moves a session between them.
var (
	_ provider.Provider        = (*Provider)(nil)
	_ provider.PreviewLoader   = (*Provider)(nil)
	_ provider.WriteCleaner    = (*Provider)(nil)
	_ provider.SessionDeleter  = (*Provider)(nil)
	_ provider.SessionRenamer  = (*Provider)(nil)
	_ provider.SessionArchiver = (*Provider)(nil)
)

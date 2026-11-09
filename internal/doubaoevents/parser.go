package doubaoevents

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// parse recognizes only observed native envelopes and explicit successful root
// completions. Desktop's logifier embeds raw JSON inside a quoted log message.
func parse(line []byte, source Source) (Event, bool) {
	var marker string
	switch source {
	case Desktop:
		marker = `[office-api-logger] "[webview][unknown][flow-web][logifier-report]: `
	case CLI:
		marker = `[info] [cli-chat] `
	default:
		return Event{}, false
	}
	i := bytes.Index(line, []byte(marker))
	if i < 0 {
		return Event{}, false
	}
	payload := line[i+len(marker):]
	if source == Desktop {
		const suffix = `", source:  (0)`
		payload = bytes.TrimSuffix(payload, []byte("\r"))
		if !bytes.HasSuffix(payload, []byte(suffix)) {
			return Event{}, false
		}
		payload = payload[:len(payload)-len(suffix)]
	}
	var record struct {
		EventName string          `json:"eventName"`
		Type      string          `json:"type"`
		Level     string          `json:"level"`
		Meta      json.RawMessage `json:"meta"`
	}
	if json.Unmarshal(payload, &record) != nil {
		return Event{}, false
	}
	e := Event{Source: source}
	if source == Desktop {
		if record.EventName != "on_message_session_end" || record.Type != "event" || record.Level != "info" {
			return Event{}, false
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(record.Meta, &m) != nil {
			return Event{}, false
		}
		get := func(k string) string {
			var s string
			if len(m[k]) == 0 || m[k][0] != '"' || json.Unmarshal(m[k], &s) != nil {
				return "\x00"
			}
			return s
		}
		// A first turn from the work start page is reported with
		// conversation_type 5 and chat_scene generic_start_page..., while later
		// turns use 3 and normal_conversation. The desktop app also repeats the
		// same completion as resumed:"1"; TurnID is shared, so the ledger drops
		// the repeat without treating either shape as a separate turn.
		scene := get("chat_scene")
		if scene != "normal_conversation" && scene != "generic_start_page_new_conversation" {
			return Event{}, false
		}
		for k, v := range map[string]string{"session_success": "1", "receive_success": "1", "agent_error": "0", "general_agent": "1", "is_async_task": "0"} {
			if get(k) != v {
				return Event{}, false
			}
		}
		if ctype := get("conversation_type"); ctype != "3" && ctype != "5" {
			return Event{}, false
		}
		for _, k := range []string{"session_err", "receive_err", "cancel_source"} {
			if _, ok := m[k]; !ok || get(k) != "" {
				return Event{}, false
			}
		}
		e.ConversationID, e.TurnID = get("conversation_id"), get("receive_message_id")
	} else {
		if record.EventName != "im_task_finished" || record.Level != "info" {
			return Event{}, false
		}
		var m struct {
			Scope              string `json:"scope"`
			AgentType          string `json:"agent_type"`
			ConversationID     string `json:"conversation_id"`
			RootConversationID string `json:"root_conversation_id"`
			SessionID          string `json:"session_id"`
			RootSessionID      string `json:"root_session_id"`
			HasError           *bool  `json:"has_error"`
			Aborted            *bool  `json:"aborted"`
		}
		if json.Unmarshal(record.Meta, &m) != nil || m.Scope != "agent" || m.AgentType != "main" || m.HasError == nil || *m.HasError || m.Aborted == nil || *m.Aborted || m.ConversationID != m.RootConversationID || m.SessionID != m.RootSessionID {
			return Event{}, false
		}
		e.ConversationID, e.TurnID = m.ConversationID, m.SessionID
	}
	if !validID(e.ConversationID) || !validID(e.TurnID) {
		return Event{}, false
	}
	return e, true
}

func validID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '-' && r != '_'
	}) < 0
}

type tail struct {
	f       *os.File
	offset  int64
	partial []byte
	discard bool
	anchor  []byte
}

func (t *tail) baseline() error {
	s, err := t.f.Stat()
	if err != nil {
		return err
	}
	t.offset = s.Size()
	if t.offset > 0 {
		var last [1]byte
		if _, err := t.f.ReadAt(last[:], t.offset-1); err != nil {
			return err
		}
		t.discard = last[0] != '\n'
	}
	return t.rememberAnchor()
}

// A boundary fingerprint also detects truncate-and-regrow between two kernel
// notifications when the new size has already exceeded the previous offset.
func (t *tail) rememberAnchor() error {
	n := min(t.offset, 64)
	t.anchor = make([]byte, n)
	if n == 0 {
		return nil
	}
	_, err := t.f.ReadAt(t.anchor, t.offset-n)
	if err == io.EOF {
		// A concurrent truncate is handled on the next vnode notification.
		t.anchor = nil
		return nil
	}
	return err
}

func (t *tail) read(emit func([]byte) error) error {
	return t.readChecked(emit, func() error { return nil })
}

func (t *tail) readChecked(emit func([]byte) error, check func() error) error {
	s, err := t.f.Stat()
	if err != nil {
		return err
	}
	changed := s.Size() < t.offset
	if !changed && len(t.anchor) > 0 {
		b := make([]byte, len(t.anchor))
		if _, err := t.f.ReadAt(b, t.offset-int64(len(b))); err != nil {
			return err
		}
		changed = !bytes.Equal(b, t.anchor)
	}
	if changed {
		t.offset = 0
		t.partial = nil
		t.discard = false
	}
	var buf [32 * 1024]byte
	for {
		if err := check(); err != nil {
			return err
		}
		// Bound each drain to its initial size so a continuously writing process
		// cannot starve cancellation or notifications for other files.
		if t.offset >= s.Size() {
			return t.rememberAnchor()
		}
		n, err := t.f.ReadAt(buf[:min(int64(len(buf)), s.Size()-t.offset)], t.offset)
		t.offset += int64(n)
		for _, b := range buf[:n] {
			if b == '\n' {
				if !t.discard {
					if err := emit(t.partial); err != nil {
						return err
					}
				}
				t.partial = t.partial[:0]
				t.discard = false
			} else if !t.discard {
				if len(t.partial) == maxLine {
					t.partial = nil
					t.discard = true
				} else {
					t.partial = append(t.partial, b)
				}
			}
		}
		if err == io.EOF {
			return t.rememberAnchor()
		}
		if err != nil {
			return err
		}
	}
}

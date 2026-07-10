package codex

import (
	"strings"
	"time"

	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/google/uuid"
)

const (
	codexRestoredUserPrompt   = "[Session restored via agenthop — continuing prior work]"
	codexDefaultContextWindow = 258400
)

func codexV2MessageID(prefix string) string {
	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New()
	}
	return prefix + id.String() + "_0"
}

func codexV2UserLines(ts, text string) ([]string, error) {
	var out []string
	if line, err := codexV2Line(ts, "event_msg", map[string]any{
		"type": "user_message", "message": text, "images": []any{}, "local_images": []any{},
	}); err != nil {
		return nil, err
	} else {
		out = append(out, line)
	}
	if line, err := codexV2Line(ts, "response_item", map[string]any{
		"type": "message", "id": codexV2MessageID("msg_user_"), "role": "user",
		"content": []any{map[string]any{"type": "input_text", "text": text}},
	}); err != nil {
		return nil, err
	} else {
		out = append(out, line)
	}
	return out, nil
}

func codexV2AssistantLines(ts, text string) ([]string, error) {
	var out []string
	if line, err := codexV2Line(ts, "event_msg", map[string]any{
		"type": "agent_message", "message": text,
	}); err != nil {
		return nil, err
	} else {
		out = append(out, line)
	}
	if line, err := codexV2Line(ts, "response_item", map[string]any{
		"type": "message", "id": codexV2MessageID("msg_resp_"), "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": text}},
	}); err != nil {
		return nil, err
	} else {
		out = append(out, line)
	}
	return out, nil
}

func codexV2TaskStarted(ts, turnID string, started time.Time) (string, error) {
	return codexV2Line(ts, "event_msg", map[string]any{
		"type":                    "task_started",
		"turn_id":                 turnID,
		"started_at":              started.Unix(),
		"model_context_window":    codexDefaultContextWindow,
		"collaboration_mode_kind": "default",
	})
}

func codexV2TurnContext(ts, turnID, project string, now time.Time) (string, error) {
	if project == "" {
		project = "/"
	}
	return codexV2Line(ts, "turn_context", map[string]any{
		"turn_id":         turnID,
		"cwd":             project,
		"workspace_roots": []string{project},
		"current_date":    now.Format("2006-01-02"),
		"approval_policy": "never",
		"sandbox_policy":  map[string]any{"type": "danger-full-access"},
	})
}

func codexV2TaskComplete(ts, turnID, lastAssistant string) (string, error) {
	return codexV2Line(ts, "event_msg", map[string]any{
		"type":               "task_complete",
		"turn_id":            turnID,
		"last_agent_message": lastAssistant,
	})
}

func codexPrepareMessages(msgs []model.Message) []model.Message {
	out := make([]model.Message, 0, len(msgs)+1)
	for _, m := range msgs {
		text := strings.TrimSpace(m.PlainText())
		if text == "" || m.Role == model.RoleTool {
			continue
		}
		if m.Role == model.RoleUser && codexSkipUserText(text) {
			continue
		}
		if m.Role == model.RoleAssistant && strings.HasPrefix(text, "[CONTEXT COMPACTION") {
			continue
		}
		out = append(out, m)
	}
	if len(out) > 0 && out[0].Role == model.RoleAssistant {
		out = append([]model.Message{{Role: model.RoleUser, Content: codexRestoredUserPrompt}}, out...)
	}
	return out
}

func codexAppendTurn(lines *[]string, turnID, project string, turnStart time.Time, user model.Message, assistants []model.Message) error {
	ts := codexV2Timestamp(turnStart)
	started, err := codexV2TaskStarted(ts, turnID, turnStart)
	if err != nil {
		return err
	}
	*lines = append(*lines, started)
	ctx, err := codexV2TurnContext(ts, turnID, project, turnStart)
	if err != nil {
		return err
	}
	*lines = append(*lines, ctx)

	userText := strings.TrimSpace(user.PlainText())
	userLines, err := codexV2UserLines(codexV2Timestamp(user.Timestamp), userText)
	if err != nil {
		return err
	}
	*lines = append(*lines, userLines...)

	lastAssistant := ""
	for _, m := range assistants {
		text := strings.TrimSpace(m.PlainText())
		if text == "" {
			continue
		}
		assistLines, err := codexV2AssistantLines(codexV2Timestamp(m.Timestamp), text)
		if err != nil {
			return err
		}
		*lines = append(*lines, assistLines...)
		lastAssistant = text
	}
	if lastAssistant != "" {
		done, err := codexV2TaskComplete(codexV2Timestamp(turnStart), turnID, lastAssistant)
		if err != nil {
			return err
		}
		*lines = append(*lines, done)
	}
	return nil
}

func codexBuildTurnLines(msgs []model.Message, project string, now time.Time) ([]string, error) {
	msgs = codexPrepareMessages(msgs)
	if len(msgs) == 0 {
		return nil, nil
	}
	var lines []string
	var assistants []model.Message
	var currentUser *model.Message
	turnStart := now
	flush := func() error {
		if currentUser == nil {
			return nil
		}
		turnID, err := uuid.NewV7()
		if err != nil {
			turnID = uuid.New()
		}
		if err := codexAppendTurn(&lines, turnID.String(), project, turnStart, *currentUser, assistants); err != nil {
			return err
		}
		currentUser = nil
		assistants = nil
		turnStart = turnStart.Add(time.Millisecond)
		return nil
	}
	for _, m := range msgs {
		switch m.Role {
		case model.RoleUser:
			if err := flush(); err != nil {
				return nil, err
			}
			msg := m
			currentUser = &msg
		case model.RoleAssistant:
			if currentUser == nil {
				msg := model.Message{Role: model.RoleUser, Content: codexRestoredUserPrompt, Timestamp: m.Timestamp}
				currentUser = &msg
			}
			assistants = append(assistants, m)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return lines, nil
}

package cli

import (
	"encoding/json"
	"fmt"
)

// parseHookPayload reads the JSON document an agent CLI pipes to a lifecycle
// hook and pulls out the one thing another needs: the session's own id, and
// whether the agent is done. The payload shapes differ per agent — agy spells
// the key conversationId and adds fullyIdle, claude-code spells session_id —
// so the keys are tried in order and the first hit wins.
//
// A missing fullyIdle reads as idle: an older CLI that never heard of the
// field still ended its turn, and refusing every one of those would make the
// hook useless. Only an explicit false says the agent is still working.
func parseHookPayload(data []byte) (id string, idle bool, err error) {
	idle = true
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", false, fmt.Errorf("hook payload is not a JSON object: %w", err)
	}
	for _, key := range []string{"conversation_id", "conversationId", "session_id", "sessionId"} {
		if s, ok := payload[key].(string); ok && s != "" {
			id = s
			break
		}
	}
	if id == "" {
		return "", false, fmt.Errorf("hook payload carries no session id")
	}
	if b, ok := payload["fullyIdle"].(bool); ok {
		idle = b
	}
	return id, idle, nil
}

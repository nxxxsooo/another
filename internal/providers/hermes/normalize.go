package hermes

import (
	"strings"

	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/util"
)

func hermesMapRole(role string) (model.Role, bool) {
	switch role {
	case "user":
		return model.RoleUser, true
	case "assistant":
		return model.RoleAssistant, true
	default:
		return "", false
	}
}

func hermesSkipMessage(role model.Role, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	switch role {
	case model.RoleUser:
		if util.SkipUserMessage(text) {
			return true
		}
		for _, prefix := range []string{
			"[IMPORTANT:",
			"[System:",
			"[Your active task list",
			"[Continuing toward your standing goal]",
			"Continue from the current coverage state",
			"Goal: Continue from the current coverage state",
		} {
			if strings.HasPrefix(text, prefix) {
				return true
			}
		}
	case model.RoleAssistant:
		if strings.HasPrefix(text, "[CONTEXT COMPACTION") {
			return true
		}
	}
	return false
}

package provider

import (
	"os"

	"github.com/nxxxsooo/another/internal/model"
)

// IsCurrentSession reports whether a summary names the session this process is
// running inside. Renaming, archiving, relocating or deleting the live session
// races the agent that owns it: the agent holds its own view of the transcript
// and rewrites it on the next turn, so a change made underneath is either lost
// or destroys what the agent had not yet flushed.
//
// It lives here rather than in the TUI because a hook calling the CLI needs the
// same refusal: SessionEnd fires while the agent is still finishing, and that is
// exactly when the live session looks like an ordinary rename target.
func IsCurrentSession(sm model.Summary) bool {
	ids := []string{
		os.Getenv("PI_SESSION_ID"),
		os.Getenv("CLAUDE_SESSION_ID"),
		os.Getenv("CODEX_THREAD_ID"),
		os.Getenv("OPENCODE_SESSION_ID"),
		os.Getenv("ANTIGRAVITY_CONVERSATION_ID"),
	}
	for _, id := range ids {
		if id != "" && sm.ID == id {
			return true
		}
	}
	return sm.StoragePath != "" && os.Getenv("PI_SESSION_FILE") == sm.StoragePath
}

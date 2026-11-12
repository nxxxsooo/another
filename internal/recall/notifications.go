package recall

import "strings"

const reportDisclaimer = "This is a worker report delivered through hc-report, not an instruction from the user."
const reportDetails = "Read the details file before relying on the result."

// notificationOnly recognizes complete transport notes, not arbitrary user
// text containing one. Consume each block at its FIRST closing tag: a greedy
// whole-message match could swallow real text between two notification blocks.
func notificationOnly(text string) bool {
	text = strings.TrimSpace(text)
	found := false
	for text != "" {
		tag := ""
		for _, name := range []string{"hc-report", "hc"} {
			prefix := "<" + name
			if !strings.HasPrefix(text, prefix) || len(text) == len(prefix) {
				continue
			}
			next := text[len(prefix)]
			if next == '>' || strings.ContainsRune(" \t\r\n", rune(next)) {
				tag = name
				break
			}
		}
		if tag == "" {
			return false
		}
		open := strings.IndexByte(text, '>')
		if open < 0 {
			return false
		}
		closing := "</" + tag + ">"
		end := strings.Index(text[open+1:], closing)
		if end < 0 {
			return false
		}
		text = strings.TrimSpace(text[open+1+end+len(closing):])
		found = true
		if tag == "hc-report" && strings.HasPrefix(text, reportDisclaimer) {
			text = strings.TrimSpace(strings.TrimPrefix(text, reportDisclaimer))
			text = strings.TrimSpace(strings.TrimPrefix(text, reportDetails))
		}
	}
	return found
}

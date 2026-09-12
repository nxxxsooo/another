package pi

import (
	"testing"
)

// pi replaces :, /, and \ unconditionally (upstream getDefaultSessionDirPath),
// so another does the same: for a colon path the old colon-keeping form never
// matched what pi itself wrote, and on Windows the colon is additionally
// illegal in a file name. The Windows drive-letter half is covered by the
// write and relocate tests on Windows CI, where every temp path carries one.
func TestEncodeProjectDirMatchesPiForColons(t *testing.T) {
	if got := encodeProjectDir("/x/a:b/c"); got != "--x-a-b-c--" {
		t.Fatalf("colon encoding diverged from pi: %q", got)
	}
}

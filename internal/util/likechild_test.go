package util_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/util"
)

// A child-prefix pattern must keep its wildcard live on both platforms. On
// Windows the separator is a backslash, and appending it bare in front of %
// would escape the wildcard into a literal percent sign — matching nothing.
// The previously failing Windows index tests (search filters, project roots,
// titler prune) exercise the SQL side; this pins the string side.
func TestEscapeLikeChildPrefix(t *testing.T) {
	got := util.EscapeLikeChildPrefix(`C:\proj`)
	var want string
	if runtime.GOOS == "windows" {
		want = `C:\\proj\\%`
	} else {
		want = `C:\\proj/%`
	}
	if got != want {
		t.Fatalf("EscapeLikeChildPrefix = %q, want %q", got, want)
	}
	if strings.HasSuffix(got, `\%`) && !strings.HasSuffix(got, `\\%`) {
		t.Fatalf("wildcard escaped into a literal percent: %q", got)
	}
}

package titler

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"time"
)

// disposeOpenCodeLocation uses the same CLI's service discovery and auth as
// `run`. Never use location.reload: it rebuilds every location in the service.
// The deepObject query must be in the URL; `api --param` does not encode this
// object on OpenCode 2.2.7 and an omitted location targets the server default.
func disposeOpenCodeLocation(bin, dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	query := url.Values{"location[directory]": {dir}}
	cmd := exec.CommandContext(ctx, bin, "api", "DELETE", "/api/debug/location?"+query.Encode())
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("dispose OpenCode titler location: %w", ctx.Err())
		}
		return fmt.Errorf("dispose OpenCode titler location: %s", failureReason(string(out), "", err))
	}
	return nil
}

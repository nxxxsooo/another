package recall

import (
	"fmt"
	"net/url"
)

// SessionURI is shared by context headers and MCP resources. It lives here
// because MCP rendering depends on recall, not the other way around.
func SessionURI(providerID, id string) string {
	return fmt.Sprintf("another://session/%s/%s", url.PathEscape(providerID), url.PathEscape(id))
}

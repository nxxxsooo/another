package util

// OriginDirectory resolves the one directory a session belongs to.
//
// Agents record their working directory on every turn, not once per session.
// A session that cd's into a subdirectory, a temporary checkout, or a second
// repository therefore carries several directories, and taking the last one
// filed real sessions under /private/tmp or under whichever repository the
// agent happened to visit last. another attributes a session to the first
// directory it recorded, and keeps a guess decoded from the storage path only
// until that evidence arrives.
//
// Merging a subdirectory into its project is the query layer's job: list and
// search match a session at or below each root of the active project scope.
type OriginDirectory struct {
	path     string
	recorded bool
}

// NewOriginDirectory starts from a fallback such as a decoded storage
// directory name. The fallback is lossy for paths containing the separator a
// provider encodes with, so any recorded directory replaces it.
func NewOriginDirectory(fallback string) OriginDirectory {
	return OriginDirectory{path: fallback}
}

// Note keeps the first non-empty directory a session recorded and ignores
// every later one.
func (o *OriginDirectory) Note(dir string) {
	if o.recorded || dir == "" {
		return
	}
	o.path = dir
	o.recorded = true
}

// Path is the directory the session is attributed to.
func (o OriginDirectory) Path() string { return o.path }

// Recorded reports whether the session itself supplied a directory, as
// opposed to the fallback decoded from its storage path.
func (o OriginDirectory) Recorded() bool { return o.recorded }

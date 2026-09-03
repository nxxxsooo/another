package migrate

import (
	"context"
	"errors"
	"os"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/util"
)

// DedupIndex is satisfied by index.Store for migration deduplication.
type DedupIndex interface {
	FindMigration(providerID, originDigest string) (sessionID, storagePath string, ok bool, err error)
	FindLegacyMigrationByOrigin(providerID, originID, originSource string, originMessageCount int) (sessionID, storagePath string, ok bool, err error)
}

// FindExistingMigration checks one known JSONL target. Broad storage scans are
// deliberately left to the incremental index.
func FindExistingMigration(storagePath, originDigest string) (string, bool) {
	if storagePath == "" || originDigest == "" {
		return "", false
	}
	return scanJSONLForDigest(storagePath, originDigest)
}

var errDigestFound = errors.New("migration digest found")

func scanJSONLForDigest(path, originDigest string) (string, bool) {
	match := func(line []byte) bool { return migrationDigest(line) == originDigest }
	if util.ScanJSONLEdges(path, 25, 64*1024, match) {
		return path, true
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() > 512*1024 {
		return "", false
	}
	var found string
	_ = util.ReadJSONLLines(path, 0, func(line []byte) error {
		if match(line) {
			found = path
			return errDigestFound
		}
		return nil
	})
	return found, found != ""
}

func migrationDigest(line []byte) string {
	meta, ok := model.ParseMigrationMeta(line)
	if !ok {
		return ""
	}
	return meta.OriginDigest
}

// FindDuplicate searches the index and target provider storage for an existing migration of conv.
func FindDuplicate(idx DedupIndex, dst provider.Provider, conv *model.Conversation) (*provider.WriteResult, bool) {
	result, ok, _ := FindDuplicateE(idx, dst, conv)
	return result, ok
}

// FindDuplicateE is the error-reporting variant used by migrations, where an
// index failure must not silently create another target.
func FindDuplicateE(idx DedupIndex, dst provider.Provider, conv *model.Conversation) (*provider.WriteResult, bool, error) {
	if conv.ID == "" || conv.Provider == "" {
		return nil, false, nil
	}
	digest := model.NewMigrationMeta(conv).OriginDigest
	if idx != nil {
		digests := []string{digest}
		if legacy := model.LegacyOriginDigest(conv); legacy != digest {
			digests = append(digests, legacy)
		}
		for _, candidate := range digests {
			sid, path, ok, err := idx.FindMigration(dst.ID(), candidate)
			if err != nil {
				return nil, false, err
			}
			if ok {
				result := &provider.WriteResult{
					SessionID:     sid,
					StoragePath:   path,
					AlreadyExists: true,
				}
				if migrationTargetMatches(dst, *result, conv, candidate, false) {
					return result, true, nil
				}
			}
		}
		if conv.ID != "" {
			sid, path, ok, err := idx.FindLegacyMigrationByOrigin(dst.ID(), conv.ID, conv.Provider, len(conv.Messages))
			if err != nil {
				return nil, false, err
			}
			if ok {
				result := &provider.WriteResult{
					SessionID:     sid,
					StoragePath:   path,
					AlreadyExists: true,
				}
				if migrationTargetMatches(dst, *result, conv, "", true) {
					return result, true, nil
				}
			}
		}
	}
	return nil, false, nil
}

// migrationTargetMatches validates the exact target and its embedded origin
// marker, rather than treating any loadable row/file as proof of a migration.
func migrationTargetMatches(dst provider.Provider, result provider.WriteResult, source *model.Conversation, digest string, legacy bool) bool {
	if result.SessionID == "" || result.StoragePath == "" {
		return false
	}
	loaded, err := dst.Load(context.Background(), provider.SessionRef{
		ID: result.SessionID, Provider: dst.ID(), StoragePath: result.StoragePath,
		ProjectPath: result.ProjectPath,
	})
	if err != nil || loaded.Migration == nil {
		return false
	}
	meta := loaded.Migration
	if meta.OriginID != source.ID || meta.OriginSource != source.Provider {
		return false
	}
	// Older digest-bearing targets copied too much provider state into active
	// context. Recreate them using the current resumable projection format.
	if meta.OriginDigest != "" && (meta.TargetFormatVersion == nil || *meta.TargetFormatVersion < model.MigrationTargetFormatVersion) {
		return false
	}
	if legacy && meta.OriginDigest == "" {
		return meta.OriginMessageCount == len(source.Messages)
	}
	return meta.OriginDigest != "" && meta.OriginDigest == digest
}

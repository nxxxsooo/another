package migrate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/util"
)

// DedupIndex is satisfied by index.Store for migration deduplication.
type DedupIndex interface {
	FindMigration(providerID, originDigest string) (sessionID, storagePath string, ok bool, err error)
	FindMigrationByOrigin(providerID, originID, originSource string) (sessionID, storagePath string, ok bool, err error)
}

// FindExistingMigration scans JSONL storage for agenthop_migration metadata matching origin digest.
func FindExistingMigration(storagePath, originDigest string) (string, bool) {
	if storagePath == "" || originDigest == "" {
		return "", false
	}
	if strings.HasSuffix(storagePath, ".jsonl") {
		if path, ok := scanJSONLForDigest(storagePath, originDigest); ok {
			return path, true
		}
	}
	dir := storagePath
	if strings.HasSuffix(storagePath, ".jsonl") {
		dir = filepath.Dir(storagePath)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if p == storagePath {
			continue
		}
		if path, ok := scanJSONLForDigest(p, originDigest); ok {
			return path, true
		}
	}
	return "", false
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

func migrationMeta(line []byte) (digest, originID, originSource string) {
	var row map[string]any
	if json.Unmarshal(line, &row) != nil {
		return "", "", ""
	}
	var data map[string]any
	if t, _ := row["type"].(string); t == model.MigrationType {
		data, _ = row["data"].(map[string]any)
	} else if d, ok := row["data"].(map[string]any); ok {
		if t, _ := d["type"].(string); t == model.MigrationType {
			data = d
		}
	}
	if data == nil {
		return "", "", ""
	}
	digest, _ = data["originDigest"].(string)
	originID, _ = data["originId"].(string)
	originSource, _ = data["originSource"].(string)
	return digest, originID, originSource
}

func migrationDigest(line []byte) string {
	digest, _, _ := migrationMeta(line)
	return digest
}

func migrationOrigin(line []byte) (originID, originSource string) {
	_, originID, originSource = migrationMeta(line)
	return originID, originSource
}

// FindDuplicate searches the index and target provider storage for an existing migration of conv.
func FindDuplicate(idx DedupIndex, dst provider.Provider, conv *model.Conversation) (*provider.WriteResult, bool) {
	digest := model.OriginDigest(conv)
	if digest == "" && conv.ID == "" {
		return nil, false
	}
	if idx != nil {
		if digest != "" {
			sid, path, ok, err := idx.FindMigration(dst.ID(), digest)
			if err != nil {
				return nil, false
			}
			if ok {
				return &provider.WriteResult{
					SessionID:     sid,
					StoragePath:   path,
					AlreadyExists: true,
				}, true
			}
		}
		if conv.ID != "" {
			sid, path, ok, err := idx.FindMigrationByOrigin(dst.ID(), conv.ID, conv.Provider)
			if err != nil {
				return nil, false
			}
			if ok {
				return &provider.WriteResult{
					SessionID:     sid,
					StoragePath:   path,
					AlreadyExists: true,
				}, true
			}
		}
	}
	for _, ps := range dst.DefaultPaths() {
		root := ps.Path
		if root == "" {
			continue
		}
		if digest != "" {
			if path, ok := walkForDigest(root, digest); ok {
				return writeResultFromPath(path), true
			}
		}
		if conv.ID != "" {
			if path, ok := walkForOrigin(root, conv.ID, conv.Provider); ok {
				return writeResultFromPath(path), true
			}
		}
	}
	return nil, false
}

func walkForDigest(root, digest string) (string, bool) {
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if p, ok := scanJSONLForDigest(path, digest); ok {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found, found != ""
}

func walkForOrigin(root, originID, originSource string) (string, bool) {
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if p, ok := scanJSONLForOrigin(path, originID, originSource); ok {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found, found != ""
}

func scanJSONLForOrigin(path, originID, originSource string) (string, bool) {
	if originID == "" {
		return "", false
	}
	match := func(line []byte) bool {
		id, src := migrationOrigin(line)
		if id != originID {
			return false
		}
		if originSource != "" && src != "" && src != originSource {
			return false
		}
		return true
	}
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

func writeResultFromPath(path string) *provider.WriteResult {
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	base := filepath.Base(path)
	if strings.HasPrefix(base, "rollout-") {
		parts := strings.Split(strings.TrimSuffix(base, ".jsonl"), "-")
		if len(parts) >= 2 {
			id = parts[len(parts)-1]
		}
	}
	_ = util.ReadJSONLLines(path, 3, func(line []byte) error {
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			return nil
		}
		if sid, _ := row["session_id"].(string); sid != "" {
			id = sid
		}
		if payload, ok := row["payload"].(map[string]any); ok {
			if sid, _ := payload["id"].(string); sid != "" {
				id = sid
			} else if sid, _ := payload["session_id"].(string); sid != "" {
				id = sid
			}
		}
		if sid, _ := row["sessionId"].(string); sid != "" {
			id = sid
		}
		return nil
	})
	return &provider.WriteResult{
		SessionID:     id,
		StoragePath:   path,
		AlreadyExists: true,
	}
}

package integrations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const annotatedOpenCode = `{
  // keep this comment
  "$schema": "https://opencode.ai/config.json",
  "theme": "system", // trailing note
  "mcp": {
    "servers": {
      "fit": {
        "type": "remote",
        "url": "https://example.com/mcp", // remote
      },
    },
  },
}
`

func TestOpenCodeRecallPatchesJSONCInPlaceAndRemovesExactly(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(file, []byte(annotatedOpenCode), 0o600); err != nil {
		t.Fatal(err)
	}
	status := OpenCodeRecallStatus(dir)
	if status.State != StateMissing || status.Dir != file {
		t.Fatalf("status = %+v", status)
	}
	installed, err := InstallOpenCodeRecall(status, "dev", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if installed.State != StateCurrent {
		t.Fatalf("after install = %s", installed.State)
	}
	data, _ := os.ReadFile(file)
	for _, keep := range []string{"// keep this comment", "// trailing note", "// remote", `"fit"`} {
		if !strings.Contains(string(data), keep) {
			t.Errorf("install lost %q:\n%s", keep, data)
		}
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Errorf("mode = %v, want 0600 kept", info.Mode().Perm())
	}
	if again, err := InstallOpenCodeRecall(installed, "dev", "", false); err != nil || again.State != StateCurrent {
		t.Fatalf("reinstall = %+v, %v", again, err)
	}

	if err := RemoveOpenCodeRecall(OpenCodeRecallStatus(dir), false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(file)
	if string(data) != annotatedOpenCode {
		t.Fatalf("remove did not restore the file byte for byte:\n%s", data)
	}
}

// Every shape a person's file comes in round-trips: no mcp block yet, plain
// JSON without trailing commas, Windows line endings. The added entry reads
// like its neighbours.
func TestOpenCodeRecallRoundTripsEveryStyle(t *testing.T) {
	for name, tc := range map[string]struct{ body, wantAdded string }{
		"no mcp block": {
			body:      "{\n  // c\n  \"theme\": \"x\"\n}\n",
			wantAdded: "  \"theme\": \"x\",\n  \"mcp\": {\n    \"servers\": {\n      \"another\": {\n        \"type\": \"local\",",
		},
		"plain json": {
			body:      "{\n    \"mcp\": {\n        \"servers\": {\n            \"fit\": {\"type\": \"remote\"}\n        }\n    }\n}\n",
			wantAdded: "            \"fit\": {\"type\": \"remote\"},\n            \"another\": {\n                \"type\": \"local\",",
		},
		"crlf": {
			body:      "{\r\n  \"mcp\": {\r\n    \"servers\": {\r\n      \"fit\": {},\r\n    },\r\n  },\r\n}\r\n",
			wantAdded: "      \"fit\": {},\r\n      \"another\": {\r\n        \"type\": \"local\",",
		},
		"empty file object": {
			body:      "{}\n",
			wantAdded: "{\n  \"mcp\": {\n    \"servers\": {\n      \"another\": {\n        \"type\": \"local\",",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "opencode.jsonc")
			if err := os.WriteFile(file, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			installed, err := InstallOpenCodeRecall(OpenCodeRecallStatus(dir), "dev", "", false)
			if err != nil || installed.State != StateCurrent {
				t.Fatalf("install = %+v, %v", installed, err)
			}
			data, _ := os.ReadFile(file)
			if !strings.Contains(string(data), tc.wantAdded) {
				t.Fatalf("added entry not shaped like its neighbours:\n%s", data)
			}
			if err := RemoveOpenCodeRecall(OpenCodeRecallStatus(dir), false); err != nil {
				t.Fatal(err)
			}
			if data, _ := os.ReadFile(file); string(data) != tc.body {
				t.Fatalf("round trip changed the file:\n%q\nwant\n%q", data, tc.body)
			}
		})
	}
}

func TestOpenCodeRecallCreatesConfigAndRefusesForeignEntries(t *testing.T) {
	dir := t.TempDir()
	created, err := InstallOpenCodeRecall(OpenCodeRecallStatus(dir), "dev", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if created.State != StateCurrent || filepath.Base(created.Dir) != "opencode.json" {
		t.Fatalf("created = %+v", created)
	}
	var doc map[string]map[string]map[string]openCodeMCP
	data, _ := os.ReadFile(created.Dir)
	if err := json.Unmarshal(data, &doc); err != nil || !slices.Equal(doc["mcp"]["servers"][MCPServerName].Command, MCPCommand()) {
		t.Fatalf("written = %s, %v", data, err)
	}

	foreign := t.TempDir()
	body := `{"mcp":{"servers":{"another":{"type":"local","command":["node","someone-else.js"]}}}}`
	if err := os.WriteFile(filepath.Join(foreign, "opencode.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	status := OpenCodeRecallStatus(foreign)
	if status.State != StateForeign {
		t.Fatalf("foreign state = %s", status.State)
	}
	if _, err := InstallOpenCodeRecall(status, "dev", "", false); err == nil {
		t.Fatal("install over a foreign entry should need --force")
	}
	if err := RemoveOpenCodeRecall(status, false); err == nil {
		t.Fatal("remove of a foreign entry should need --force")
	}
	if forced, err := InstallOpenCodeRecall(status, "dev", "", true); err != nil || forced.State != StateCurrent {
		t.Fatalf("forced = %+v, %v", forced, err)
	}

	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "opencode.json"), []byte(`{"mcp": {`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallOpenCodeRecall(OpenCodeRecallStatus(broken), "dev", "", true); err == nil {
		t.Fatal("an unparseable file must not be rewritten")
	}
}

// A server another registered in the deprecated V1 form is still another's:
// installing moves it to mcp.servers, and removing takes either form back.
func TestOpenCodeRecallMovesALegacyEntry(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "opencode.json")
	legacy := `{"mcp":{"another":{"type":"local","command":["another-old","mcp"]},"fit":{"type":"remote","url":"u"}}}`
	if err := os.WriteFile(file, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	status := OpenCodeRecallStatus(dir)
	if status.State != StateOutdated {
		t.Fatalf("legacy entry = %s, want outdated", status.State)
	}
	if got, err := InstallOpenCodeRecall(status, "dev", "", false); err != nil || got.State != StateCurrent {
		t.Fatalf("install = %+v, %v", got, err)
	}
	var doc struct {
		MCP map[string]json.RawMessage `json:"mcp"`
	}
	data, _ := os.ReadFile(file)
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.MCP["another"]; ok {
		t.Fatalf("legacy entry left behind: %s", data)
	}
	if _, ok := doc.MCP["fit"]; !ok {
		t.Fatalf("another server lost: %s", data)
	}
}

// Qwen Code's settings.json holds every setting the person has, so the server
// is added and taken back without changing a byte of anything else.
func TestQwenRecallRoundTripsSettings(t *testing.T) {
	dir := t.TempDir()
	file := QwenSettingsFile(dir)
	body := "{\n  \"$version\": 3,\n  // models\n  \"model\": {\"name\": \"qwen3\"},\n  \"mcpServers\": {\n    \"hindsight\": {\n      \"command\": \"node\",\n      \"args\": [\"x.js\"]\n    }\n  }\n}\n"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := QwenRecallStatus(dir).State; got != StateMissing {
		t.Fatalf("initial = %s", got)
	}
	installed, err := InstallQwenRecall(QwenRecallStatus(dir), "dev", "", false)
	if err != nil || installed.State != StateCurrent {
		t.Fatalf("install = %+v, %v", installed, err)
	}
	data, _ := os.ReadFile(file)
	want := "    },\n    \"another\": {\n      \"command\": \"" + MCPCommand()[0] + "\",\n      \"args\": [\n        \"mcp\"\n      ]\n    }\n  }\n}\n"
	if !strings.HasSuffix(string(data), want) || !strings.Contains(string(data), "// models") {
		t.Fatalf("installed file:\n%s", data)
	}
	if err := RemoveQwenRecall(QwenRecallStatus(dir), false); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(file); string(data) != body {
		t.Fatalf("round trip changed settings.json:\n%s", data)
	}

	foreign := t.TempDir()
	if err := os.WriteFile(QwenSettingsFile(foreign), []byte(`{"mcpServers":{"another":{"command":"npx","args":["other"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallQwenRecall(QwenRecallStatus(foreign), "dev", "", false); err == nil {
		t.Fatal("install over a foreign server should need --force")
	}
}

func TestRecallStateRecognizesAnothersOwnBinaries(t *testing.T) {
	if got := recallState(MCPCommand()); got != StateCurrent {
		t.Fatalf("own command = %s", got)
	}
	if got := recallState([]string{"another-dev", "mcp"}); got != StateOutdated && MCPCommand()[0] != "another-dev" {
		t.Fatalf("dev build = %s, want outdated", got)
	}
	if got := recallState([]string{"npx", "other-mcp"}); got != StateForeign {
		t.Fatalf("foreign = %s", got)
	}
}

// Claude Code's .claude.json is rewritten by every running session, so the
// write goes through its CLI; the fake stands in for that CLI and edits the
// same file the real one would.
func TestClaudeRecallGoesThroughTheCLI(t *testing.T) {
	dir := t.TempDir()
	file := ClaudeStateFile(dir)
	if err := os.WriteFile(file, []byte(`{"numStartups":3,"mcpServers":{"fit":{"type":"http","url":"https://example.com"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	old := runClaude
	t.Cleanup(func() { runClaude = old })
	runClaude = func(configDir string, args ...string) error {
		if configDir != dir {
			t.Fatalf("configDir = %q", configDir)
		}
		calls = append(calls, strings.Join(args, " "))
		var doc map[string]any
		data, _ := os.ReadFile(file)
		_ = json.Unmarshal(data, &doc)
		servers := doc["mcpServers"].(map[string]any)
		switch args[1] {
		case "add":
			sep := slices.Index(args, "--")
			servers[args[sep-1]] = map[string]any{"type": "stdio", "command": args[sep+1], "args": args[sep+2:], "env": map[string]any{}}
		case "remove":
			delete(servers, args[len(args)-1])
		}
		out, _ := json.Marshal(doc)
		return os.WriteFile(file, out, 0o600)
	}

	status := ClaudeRecallStatus(dir)
	if status.State != StateMissing {
		t.Fatalf("initial = %s", status.State)
	}
	installed, err := InstallClaudeRecall(status, "dev", "", false)
	if err != nil || installed.State != StateCurrent {
		t.Fatalf("install = %+v, %v", installed, err)
	}
	want := "mcp add --scope user another -- " + strings.Join(MCPCommand(), " ")
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("calls = %q, want %q", calls, want)
	}
	if _, err := InstallClaudeRecall(installed, "dev", "", false); err != nil || len(calls) != 1 {
		t.Fatalf("a current registration should not be rewritten: %q %v", calls, err)
	}
	if err := RemoveClaudeRecall(ClaudeRecallStatus(dir), false); err != nil {
		t.Fatal(err)
	}
	if got := ClaudeRecallStatus(dir).State; got != StateMissing {
		t.Fatalf("after remove = %s", got)
	}
	data, _ := os.ReadFile(file)
	if !strings.Contains(string(data), `"fit"`) {
		t.Fatalf("remove touched another server: %s", data)
	}
}

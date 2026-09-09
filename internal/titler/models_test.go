package titler

import (
	"strings"
	"testing"
)

// Each parser is fed the shape its CLI actually prints, decorations included.
func TestModelParsersKeepOnlyUsableIdentifiers(t *testing.T) {
	cases := []struct {
		name  string
		parse func(string) []string
		raw   string
		want  []string
	}{
		{
			"pi table rejoins provider and model",
			parseTableModels,
			"provider          model                context  max-out\n" +
				"anthropic         claude-sonnet-4-5    1M       64K\n" +
				"google            gemini-3-pro         1M       64K\n",
			[]string{"anthropic/claude-sonnet-4-5", "google/gemini-3-pro"},
		},
		{
			"agy drops the progress line",
			parseTabbedModels,
			"Fetching available models...\ngemini-3.8-flash-high\tGemini 3.8 Flash (High)\ngemini-3.1-pro-low\tGemini 3.1 Pro (Low)\n",
			[]string{"gemini-3.8-flash-high", "gemini-3.1-pro-low"},
		},
		{
			"opencode keeps bare identifiers",
			parsePlainModels,
			"anthropic/claude-opus-5\n\nopenai/gpt-5.3-codex\nTotal: 2 models\n",
			[]string{"anthropic/claude-opus-5", "openai/gpt-5.3-codex"},
		},
		{
			"ansi colour is stripped",
			parsePlainModels,
			"\x1b[32manthropic/claude-opus-5\x1b[0m\n",
			[]string{"anthropic/claude-opus-5"},
		},
		{
			"claude keeps the --model values and drops its default row",
			parseClaudeModels,
			`{"type":"system","subtype":"hook_started","hook_name":"SessionStart:startup"}` + "\n" +
				`{"type":"control_response","response":{"subtype":"success","request_id":"another-list-models","response":{"models":[` +
				`{"value":"default","resolvedModel":"claude-opus-5[1m]","displayName":"Default (recommended)"},` +
				`{"value":"opus[1m]","resolvedModel":"claude-opus-5[1m]","displayName":"Opus (1M context)"},` +
				`{"value":"sonnet","resolvedModel":"claude-sonnet-5","displayName":"Sonnet"},` +
				`{"value":"haiku","resolvedModel":"claude-haiku-4-5","displayName":"Haiku"}]}}}` + "\n",
			[]string{"opus[1m]", "sonnet", "haiku"},
		},
		{
			"claude ignores a failed or unrelated control response",
			parseClaudeModels,
			`{"type":"control_response","response":{"subtype":"error","request_id":"another-list-models","error":"unknown subtype"}}` + "\n" +
				`not json at all` + "\n" +
				`{"type":"result","subtype":"success","result":"hi"}` + "\n",
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.parse(tc.raw)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("parsed %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDedupeCapsAndPreservesOrder(t *testing.T) {
	got := dedupe([]string{"b", "a", "b", "c"})
	if strings.Join(got, ",") != "b,a,c" {
		t.Fatalf("dedupe = %v", got)
	}
	long := make([]string, maxModels+50)
	for i := range long {
		long[i] = string(rune('a'+i%26)) + strings.Repeat("x", i%7) + string(rune(i))
	}
	if n := len(dedupe(long)); n > maxModels {
		t.Fatalf("dedupe returned %d models, cap is %d", n, maxModels)
	}
}

// A picker is only offered for CLIs that can really list; the rest must fall
// back to typing rather than showing IDs their --model flag would reject.
func TestCanListModelsMatchesTheListers(t *testing.T) {
	for _, id := range []string{"pi", "agy", "opencode", "opencode2", "OPENCODE", "claude-code", "claude"} {
		if !CanListModels(id) {
			t.Fatalf("%s should support listing", id)
		}
	}
	for _, id := range []string{"codex", "qwen", "cursor", ""} {
		if CanListModels(id) {
			t.Fatalf("%s must not claim listing support", id)
		}
	}
	for id := range modelListers {
		if _, ok := launchers[id]; !ok {
			t.Fatalf("%s can list models but cannot generate titles", id)
		}
	}
}

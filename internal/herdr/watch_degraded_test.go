package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestWatchStatusDegradesWithoutLosingPositiveIdentities(t *testing.T) {
	f := watchFixture()
	f.responses["hc ls --json"] = strings.Replace(f.responses["hc ls --json"], `]}`, `,{"address":"t3","agent":"opencode","state":"live"}]}`, 1)
	f.responses["hc get t3 --json"] = `{"address":"t3","state":"live","status":{"app":"opencode"}}`
	c := Client{Run: f.run}
	s, err := c.Status(context.Background())
	if err != nil || len(s.Live) != 3 || len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], "terminal t3") || len(s.Groups["ses_super"]) != 1 {
		t.Fatalf("status=%+v err=%v", s, err)
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(out, &shape); err != nil || len(shape) != 3 || shape["warnings"] == nil {
		t.Fatalf("JSON=%s err=%v", out, err)
	}
	for _, add := range []bool{true, false} {
		value := "ses_member"
		if !add {
			value = ""
		}
		f.responses["hc label t1 another.watch.ses_super="+value] = `{}`
		if add {
			err = c.Watch(context.Background(), "ses_super", "ses_member")
		} else {
			err = c.Unwatch(context.Background(), "ses_super", "ses_member")
		}
		if err != nil {
			t.Fatalf("unrelated missing records blocked write: %v", err)
		}
	}
	// The duplicated ID alone is excluded. The supervisor's identity survives.
	f.responses["hc get t3 --json"] = `{"address":"t3","state":"live","status":{"app":"opencode"},"records":{"ses_member":{"app":"opencode","state":"idle"}}}`
	s, err = c.Status(context.Background())
	if err != nil || len(s.Live) != 2 || s.Live["ses_super"].Terminal != "t1" || len(s.Warnings) != 1 {
		t.Fatalf("ambiguous status=%+v err=%v", s, err)
	}
	if _, ok := s.Live["ses_member"]; ok {
		t.Fatal("ambiguous identity remained live")
	}
	if err := c.Watch(context.Background(), "ses_super", "ses_member"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("write error=%v", err)
	}
}

func TestMalformedWatchLabelsOnlySkipTheirGroup(t *testing.T) {
	for _, label := range []string{`"ses_super"`, `"ses_member,"`, `42`, `null`, `["ses_member"]`} {
		f := watchFixture()
		f.responses["hc get t1 --json"] = strings.Replace(f.responses["hc get t1 --json"], `"another.watch.ses_super":"ses_member"`, `"another.watch.ses_super":`+label, 1)
		f.responses["hc get t2 --json"] = strings.Replace(memberDetail("working"), `"records":`, `"labels":{"another.watch.ses_member":"ses_super"},"records":`, 1)
		s, err := (Client{Run: f.run}).Status(context.Background())
		if err != nil || len(s.Warnings) != 1 || len(s.Groups) != 1 || len(s.Groups["ses_member"]) != 1 {
			t.Fatalf("label=%s status=%+v err=%v", label, s, err)
		}
	}
}

func TestWatchStatusFatalCoreAndInvalidResponses(t *testing.T) {
	for _, key := range []string{"hc server status --json", "hc ls --json", "hc get t2 --json"} {
		f := watchFixture()
		f.responses[key] = `{"bad json"`
		if _, err := (Client{Run: f.run}).Status(context.Background()); err == nil {
			t.Fatalf("accepted invalid %s", key)
		}
	}
	f := watchFixture()
	f.fail["hc server status --json"] = errors.New("core unreachable")
	if _, err := (Client{Run: f.run}).Status(context.Background()); err == nil {
		t.Fatal("accepted unreachable core")
	}
}

func TestFollowFiltersEventsAndSurvivesTransientDiscovery(t *testing.T) {
	f := watchFixture()
	original := f.responses["hc get t1 --json"]
	var baselineCalls int
	events := &eventSteps{terminals: []string{"", "unrelated", "unrelated", "t2", "t1", "t2", "t1", "t1", "t1", "t2", "t1"}, steps: []func(){
		func() {},
		func() { baselineCalls = len(f.calls); f.responses["hc get t2 --json"] = memberDetail("idle") },
		func() {
			if len(f.calls) != baselineCalls {
				t.Fatal("unrelated terminal triggered discovery")
			}
		},
		func() {
			if len(f.calls) != baselineCalls {
				t.Fatal("unrelated churn triggered discovery")
			}
		},
		func() { f.fail["hc server status --json"] = errors.New("transient core failure") },
		func() {
			delete(f.fail, "hc server status --json")
			f.responses["hc get t2 --json"] = memberDetail("working")
		},
		func() {
			f.responses["hc get t1 --json"] = strings.Replace(original, `"another.watch.ses_super":"ses_member"`, `"another.watch.ses_super":"ses_super"`, 1)
		},
		func() { f.responses["hc get t1 --json"] = original },
		func() {
			f.responses["hc get t1 --json"] = `{"address":"t1","state":"live","status":{"app":"opencode"}}`
		},
		func() {
			f.responses["hc get t1 --json"] = original
			f.responses["hc get t2 --json"] = memberDetail("done")
		},
		func() {
			f.responses["hc get t1 --json"] = strings.Replace(original, `"another.watch.ses_super":"ses_member"`, `"another.watch.ses_super":""`, 1)
		},
	}}
	var ends []string
	err := (Client{Run: f.run}).Follow(context.Background(), "ses_super", events, func(end TurnEnd) error { ends = append(ends, end.State); return nil })
	if err != nil || strings.Join(ends, ",") != "idle,done" || events.index != len(events.steps) {
		t.Fatalf("ends=%v steps=%d err=%v", ends, events.index, err)
	}
}

func TestFollowSupervisorLabelsDiscoverNewMemberTerminals(t *testing.T) {
	f := watchFixture()
	original := f.responses["hc get t1 --json"]
	events := &eventSteps{terminals: []string{"", "t1", "t3", "t1"}, steps: []func(){
		func() {},
		func() {
			f.responses["hc ls --json"] = strings.Replace(f.responses["hc ls --json"], `]}`, `,{"address":"t3","agent":"opencode","state":"live"}]}`, 1)
			f.responses["hc get t3 --json"] = `{"address":"t3","state":"live","status":{"app":"opencode"},"records":{"ses_third":{"app":"opencode","state":"working"}}}`
			f.responses["hc get t1 --json"] = strings.Replace(original, `"another.watch.ses_super":"ses_member"`, `"another.watch.ses_super":"ses_member,ses_third"`, 1)
		},
		func() {
			f.responses["hc get t3 --json"] = strings.ReplaceAll(f.responses["hc get t3 --json"], `"working"`, `"idle"`)
		},
		func() {
			f.responses["hc get t1 --json"] = strings.Replace(original, `"another.watch.ses_super":"ses_member"`, `"another.watch.ses_super":""`, 1)
		},
	}}
	var members []string
	err := (Client{Run: f.run}).Follow(context.Background(), "ses_super", events, func(end TurnEnd) error { members = append(members, end.Member); return nil })
	if err != nil || strings.Join(members, ",") != "ses_third" {
		t.Fatalf("members=%v err=%v", members, err)
	}
}

func TestFollowInitiallyMalformedGroupStillFiltersUnrelatedChurn(t *testing.T) {
	f := watchFixture()
	original := f.responses["hc get t1 --json"]
	f.responses["hc get t1 --json"] = strings.Replace(original, `"another.watch.ses_super":"ses_member"`, `"another.watch.ses_super":"ses_super"`, 1)
	var calls int
	events := &eventSteps{terminals: []string{"", "unrelated", "t1", "t1"}, steps: []func(){
		func() {},
		func() { calls = len(f.calls) },
		func() {
			if len(f.calls) != calls {
				t.Fatal("malformed group allowed unrelated churn to probe all terminals")
			}
			f.responses["hc get t1 --json"] = original
		},
		func() {
			f.responses["hc get t1 --json"] = strings.Replace(original, `"another.watch.ses_super":"ses_member"`, `"another.watch.ses_super":""`, 1)
		},
	}}
	if err := (Client{Run: f.run}).Follow(context.Background(), "ses_super", events, func(TurnEnd) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if events.index != len(events.steps) {
		t.Fatal("malformed label prematurely ended follower")
	}
}

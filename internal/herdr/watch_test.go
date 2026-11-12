package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func watchFixture() *fake {
	f := fixture()
	f.responses["hc ls --json"] = `{"terminals":[{"address":"t1","agent":"opencode","state":"live"},{"address":"t2","agent":"opencode","state":"live"}]}`
	f.responses["hc get t1 --json"] = `{"address":"t1","state":"live","status":{"app":"opencode"},"labels":{"another.watch.ses_super":"ses_member"},"records":{"ses_super":{"app":"opencode","state":"idle","title":"Supervisor"}}}`
	f.responses["hc get t2 --json"] = memberDetail("working")
	return f
}

func memberDetail(state string) string {
	return `{"address":"t2","state":"live","status":{"app":"opencode"},"records":{"ses_member":{"app":"opencode","state":"` + state + `","title":"Member"},"ses_new":{"app":"opencode","state":"` + state + `","title":"New member"}}}`
}

func TestWatchLabelsAndState(t *testing.T) {
	f := watchFixture()
	c := Client{Run: f.run}
	s, err := c.Status(context.Background())
	if err != nil || s.Live["ses_member"] != (LiveSession{State: "working", Terminal: "t2", Title: "Member"}) || !reflect.DeepEqual(s.Groups, map[string][]string{"ses_super": {"ses_member"}}) {
		t.Fatalf("status=%+v err=%v", s, err)
	}
	for _, test := range []struct {
		add           bool
		member, label string
	}{
		{true, "ses_new", "ses_member,ses_new"}, {true, "ses_member", "ses_member"}, {false, "ses_member", ""},
	} {
		key := "hc label t1 another.watch.ses_super=" + test.label
		f.responses[key] = `{}`
		if test.add {
			err = c.Watch(context.Background(), "ses_super", test.member)
		} else {
			err = c.Unwatch(context.Background(), "ses_super", test.member)
		}
		if err != nil || strings.Join(f.calls[len(f.calls)-1], " ") != key {
			t.Fatalf("write=%v err=%v", f.calls, err)
		}
	}
}

func TestWatchRefusesInvalidDiscoveryWithoutWrites(t *testing.T) {
	for _, test := range []struct {
		name, supervisor, member string
		change                   func(*fake)
	}{
		{"self", "ses_super", "ses_super", func(*fake) {}},
		{"invalid", "ses_super", "ses_bad,inject", func(*fake) {}},
		{"missing", "ses_super", "ses_missing", func(*fake) {}},
		{"non-opencode", "ses_super", "ses_member", func(f *fake) {
			f.responses["hc get t2 --json"] = strings.ReplaceAll(memberDetail("working"), "opencode", "pi")
		}},
		{"remote", "ses_super", "ses_member", func(f *fake) {
			f.responses["hc ls --json"] = strings.ReplaceAll(f.responses["hc ls --json"], `"address":"t2"`, `"address":"remote/t2"`)
		}},
		{"incomplete", "ses_super", "ses_member", func(f *fake) { f.fail["hc get t2 --json"] = errors.New("metadata missing") }},
		{"ambiguous", "ses_super", "ses_member", func(f *fake) {
			f.responses["hc get t1 --json"] = strings.ReplaceAll(f.responses["hc get t1 --json"], `"ses_super":`, `"ses_member":{"app":"opencode","state":"idle"},"ses_super":`)
		}},
		{"bad-label", "ses_super", "ses_member", func(f *fake) {
			f.responses["hc get t1 --json"] = strings.ReplaceAll(f.responses["hc get t1 --json"], `"another.watch.ses_super":"ses_member"`, `"another.watch.ses_super":"ses_super"`)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, add := range []bool{true, false} {
				f := watchFixture()
				test.change(f)
				c := Client{Run: f.run}
				var err error
				if add {
					err = c.Watch(context.Background(), test.supervisor, test.member)
				} else {
					err = c.Unwatch(context.Background(), test.supervisor, test.member)
				}
				if err == nil {
					t.Fatal("expected validation error")
				}
				for _, call := range f.calls {
					if len(call) > 1 && call[1] == "label" {
						t.Fatal("wrote after invalid discovery")
					}
				}
			}
		})
	}
}

type eventSteps struct {
	steps []func()
	index int
}

func (r *eventSteps) Read(p []byte) (int, error) {
	if r.index == len(r.steps) {
		return 0, io.EOF
	}
	r.steps[r.index]()
	line := `{"method":"event","params":{"terminal":"t2","type":"record"}}` + "\n"
	if r.index == 0 {
		line = `{"terminals":[]}` + "\n"
	}
	r.index++
	return copy(p, line), nil
}

func TestFollowTransitionsMembershipAndRemoval(t *testing.T) {
	f := watchFixture()
	setState := func(state string) func() { return func() { f.responses["hc get t2 --json"] = memberDetail(state) } }
	setGroup := func(value string) {
		var detail map[string]any
		if err := json.Unmarshal([]byte(f.responses["hc get t1 --json"]), &detail); err != nil {
			t.Fatal(err)
		}
		detail["labels"] = map[string]string{watchPrefix + "ses_super": value}
		out, _ := json.Marshal(detail)
		f.responses["hc get t1 --json"] = string(out)
	}
	events := &eventSteps{steps: []func(){
		func() {}, setState("idle"), setState("idle"), setState("blocked"), setState("blocked"), setState("error"),
		func() { setGroup("ses_member,ses_new"); setState("working")() }, setState("done"),
		func() { setGroup("") },
	}}
	var emitted []TurnEnd
	err := (Client{Run: f.run}).Follow(context.Background(), "ses_super", events, func(end TurnEnd) error { emitted = append(emitted, end); return nil })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ses_member:idle", "ses_member:blocked", "ses_member:error", "ses_member:done", "ses_new:done"}
	var got []string
	for _, end := range emitted {
		if end.Title == "" || end.At.IsZero() {
			t.Fatal("missing report fields")
		}
		got = append(got, end.Member+":"+end.State)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	if events.index != len(events.steps) {
		t.Fatalf("removed group did not exit cleanly: %d", events.index)
	}
}

func TestFollowStreamErrors(t *testing.T) {
	for _, input := range []string{"not json\n", "{}\n", "{\"terminals\":[]}\n"} {
		f := watchFixture()
		if err := (Client{Run: f.run}).Follow(context.Background(), "ses_super", strings.NewReader(input), func(TurnEnd) error { return nil }); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

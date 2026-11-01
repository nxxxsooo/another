package titler

import (
	"strings"
	"time"

	"github.com/nxxxsooo/another/internal/model"
)

// RefreshTurns is how many new user turns make a title another wrote worth
// checking again on the same day. Fewer spends a title call on sessions that
// have not moved; more lets a session drift a long way under an old name.
const RefreshTurns = 3

// Mark is what another remembers about the last title it wrote for a session.
// It is the only way to tell a title another owns from one a person chose:
// both can follow the policy, and only the person's must never be replaced.
type Mark struct {
	Title     string    `json:"title"`
	UserTurns int       `json:"user_turns"`
	At        time.Time `json:"at"`
}

// Plan is what a stale-aware rename should do with one session.
type Plan string

const (
	// PlanSkip leaves the title and the mark alone.
	PlanSkip Plan = "skip"
	// PlanName asks for a first title: the session has none another owns.
	PlanName Plan = "name"
	// PlanRefresh asks again for a title another owns, allowing the model to
	// keep it when the session has not moved.
	PlanRefresh Plan = "refresh"
	// PlanAdopt records the current title as another's without a model call:
	// a policy title written before marks existed, or history that shrank.
	PlanAdopt Plan = "adopt"
)

// Freshness is the input to Decide. Activity is when the session last moved;
// its Shanghai date is the date a refreshed title carries.
type Freshness struct {
	Title     string
	Mark      *Mark
	UserTurns int
	Activity  time.Time
	Language  Language
	// ReplaceNative treats a non-policy title on a session another has never
	// titled as the agent's own automatic name, which is safe to replace.
	// Callers set it only for agents that name sessions themselves; where a
	// name can only come from a person, it stays off.
	ReplaceNative bool
}

// Decision is Decide's verdict. NewDay says the title's date is behind the
// session's latest activity, so even a kept topic must be redated.
type Decision struct {
	Plan   Plan
	Reason string
	NewDay bool
}

// Decide applies the dynamic title rule. A title another owns is refreshed
// when the session moves to a later day or gains RefreshTurns user turns; a
// title someone else set is never replaced, except an agent's own automatic
// name when the caller says the agent produces those.
func Decide(f Freshness) Decision {
	title := strings.TrimSpace(f.Title)
	if title == "" {
		return Decision{Plan: PlanName, Reason: "untitled"}
	}
	conforms := Conforms(f.Language, title)
	switch {
	case f.Mark != nil && f.Mark.Title != title:
		// Agents name a session once, when it starts. A title that changed
		// after another wrote its own came from a person.
		return Decision{Plan: PlanSkip, Reason: "set by hand"}
	case f.Mark == nil && !conforms:
		if f.ReplaceNative {
			return Decision{Plan: PlanName, Reason: "agent title"}
		}
		return Decision{Plan: PlanSkip, Reason: "set by hand"}
	case !conforms:
		// another wrote it, but it no longer passes the policy — the
		// language setting changed since.
		return Decision{Plan: PlanName, Reason: "not a policy title"}
	}
	if day := MMDD(f.Activity); day != "" && day != TitleDay(title) {
		return Decision{Plan: PlanRefresh, Reason: "new day", NewDay: true}
	}
	if f.Mark == nil {
		return Decision{Plan: PlanAdopt, Reason: "first seen"}
	}
	switch moved := f.UserTurns - f.Mark.UserTurns; {
	case moved >= RefreshTurns:
		return Decision{Plan: PlanRefresh, Reason: "topic may have moved"}
	case moved < 0:
		// Compaction or a rewritten history: count from what is there now.
		return Decision{Plan: PlanAdopt, Reason: "history shrank"}
	}
	return Decision{Plan: PlanSkip, Reason: "fresh"}
}

// TitleDay is the MMDD a policy title carries, or "" for any other title.
func TitleDay(title string) string {
	day, _, ok := strings.Cut(strings.TrimSpace(title), Separator)
	if !ok || len(day) != 4 || strings.Trim(day, "0123456789") != "" {
		return ""
	}
	return day
}

// Redate puts day on a policy title, keeping its type and topic. The model is
// told the date, but nothing it returns is trusted to carry it.
func Redate(title string, day time.Time) string {
	title = strings.TrimSpace(title)
	date := MMDD(day)
	if date == "" || TitleDay(title) == "" {
		return title
	}
	return date + title[len(TitleDay(title)):]
}

// Topic is a policy title without its date: the part a refresh may change.
func Topic(title string) string {
	title = strings.TrimSpace(title)
	if day := TitleDay(title); day != "" {
		return strings.TrimPrefix(title[len(day):], Separator)
	}
	return title
}

// UserTurns counts what a person said, the unit the drift rule is measured in.
func UserTurns(messages []model.Message) int {
	n := 0
	for _, m := range messages {
		if m.Role == model.RoleUser && strings.TrimSpace(m.PlainText()) != "" {
			n++
		}
	}
	return n
}

package tui

import (
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/list"
)

// dateBand is how far back a session is, in the units people actually ask the
// question in. It is not a duration: "yesterday" is a calendar day, not
// twenty-four hours, so a session from 23:50 last night and one from 00:10 this
// morning belong to different bands twenty minutes apart.
type dateBand int

const (
	bandToday dateBand = iota
	bandYesterday
	bandThisWeek
	bandThisMonth
	bandOlder
	bandUnknown
)

// dateBandFor places a session against a reference day. The reference is passed
// in rather than read from the clock so the bands can be tested at a boundary
// instead of near one.
func dateBandFor(t, now time.Time) dateBand {
	if t.IsZero() {
		return bandUnknown
	}
	day := func(x time.Time) time.Time {
		return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, x.Location())
	}
	today := day(now)
	at := day(t.In(now.Location()))
	switch {
	case !at.Before(today):
		// Anything dated later than today is still today's work: a clock
		// skewed a few minutes forward must not open a band above the list.
		return bandToday
	case at.Equal(today.AddDate(0, 0, -1)):
		return bandYesterday
	case at.After(today.AddDate(0, 0, -7)):
		return bandThisWeek
	case at.After(today.AddDate(0, 0, -30)):
		return bandThisMonth
	default:
		return bandOlder
	}
}

func (b dateBand) label() string {
	switch b {
	case bandToday:
		return txt.bandToday
	case bandYesterday:
		return txt.bandYesterday
	case bandThisWeek:
		return txt.bandThisWeek
	case bandThisMonth:
		return txt.bandThisMonth
	case bandOlder:
		return txt.bandOlder
	default:
		return txt.bandUnknown
	}
}

// groupSessionsByDate bands the list by when each session was last touched. The
// incoming page is already sorted by recency, so the bands come out newest
// first without being sorted again — and a band is only drawn where the list
// actually crosses into it.
func groupSessionsByDate(items []list.Item, now time.Time) []list.Item {
	return groupSessionsBy(items,
		func(row sessionItem) string {
			return strconv.Itoa(int(dateBandFor(row.summary.UpdatedAt, now)))
		},
		func(key string, count int) groupHeader {
			n, _ := strconv.Atoi(key)
			return groupHeader{label: dateBand(n).label(), count: count}
		})
}

// groupModeStatus names the mode the g key just moved to. Three states cannot
// be read off the screen the way two can — a flat list and a list whose bands
// happen to be off screen look identical — so the key says which one it landed
// on rather than leaving it to be inferred.
func groupModeStatus(mode int) string {
	switch mode {
	case groupTree:
		return mutedStyle.Render(txt.groupByTree)
	case groupDate:
		return mutedStyle.Render(txt.groupByDate)
	default:
		return mutedStyle.Render(txt.groupOff)
	}
}

// compactTime is the time column under date bands. The band already says which
// day the row is from, so the column says only where in it: "7m", "13h", a
// weekday inside the week, and a date past that. It is what lets the column
// shrink from twelve cells to five without the row losing anything the band
// does not already carry.
// It answers the question its own band leaves open, which is a different
// question in each one. Inside today that is how long ago; inside yesterday,
// which is a calendar day and can hold a stamp twenty-three hours old and one
// twenty-five hours old, a duration says nothing the band has not, so it gives
// the clock. Further back the day itself is what is being asked.
func compactTime(t, now time.Time) string {
	switch dateBandFor(t, now) {
	case bandToday:
		d := now.Sub(t)
		switch {
		case d < time.Minute:
			return txt.compactNow
		case d < time.Hour:
			return fmt.Sprintf("%dm", int(d/time.Minute))
		default:
			return fmt.Sprintf("%dh", max(1, int(d/time.Hour)))
		}
	case bandYesterday:
		return t.Format("15:04")
	case bandThisWeek:
		return t.Format("Mon")
	case bandThisMonth:
		return t.Format("Jan 2")
	case bandOlder:
		if t.Year() == now.Year() {
			return t.Format("Jan 2")
		}
		return t.Format("Jan 06")
	default:
		return txt.bandUnknown
	}
}

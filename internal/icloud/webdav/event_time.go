package webdav

import (
	"github.com/aaronfaby/icloud-cli/internal/output"
	"strings"
	"time"
	_ "time/tzdata"
)

// EventTimes builds DATE properties (exclusive end) or UTC DATE-TIME properties.
func EventTimes(start, end string, allDay bool, zone string) ([]string, error) {
	invalid := output.Validation("invalid_event_time", "use ordered dates for all-day events, or RFC3339 times; local times require an IANA time_zone and must be unambiguous", nil)
	if allDay {
		if zone != "" {
			return nil, invalid
		}
		a, e := time.Parse("2006-01-02", start)
		b, f := time.Parse("2006-01-02", end)
		if e != nil || f != nil || !b.After(a) {
			return nil, invalid
		}
		return []string{"DTSTART;VALUE=DATE:" + a.Format("20060102"), "DTEND;VALUE=DATE:" + b.Format("20060102")}, nil
	}
	var loc *time.Location
	if zone != "" {
		var e error
		loc, e = time.LoadLocation(zone)
		if e != nil || zone == "Local" {
			return nil, invalid
		}
	}
	parse := func(s string) (time.Time, error) {
		if t, e := time.Parse(time.RFC3339, s); e == nil {
			if loc != nil {
				_, a := t.Zone()
				_, b := t.In(loc).Zone()
				if a != b {
					return time.Time{}, invalid
				}
			}
			return t, nil
		}
		if loc == nil { // Preserve the existing compact UTC input.
			if t, e := time.Parse("20060102T150405Z", s); e == nil {
				return t, nil
			}
			return time.Time{}, invalid
		}
		layout := "2006-01-02T15:04:05"
		if len(s) == 16 {
			layout = "2006-01-02T15:04"
		}
		wall, e := time.Parse(layout, s)
		if e != nil {
			return time.Time{}, invalid
		}
		// Enumerate the offsets on either side of a transition, including non-hour shifts.
		offsets := map[int]bool{}
		for h := -48; h <= 48; h++ {
			_, off := wall.Add(time.Duration(h) * time.Hour).In(loc).Zone()
			offsets[off] = true
		}
		var found []time.Time
		for off := range offsets {
			candidate := wall.Add(-time.Duration(off) * time.Second)
			if candidate.In(loc).Format(layout) == s {
				found = append(found, candidate)
			}
		}
		if len(found) != 1 {
			return time.Time{}, invalid
		}
		return found[0], nil
	}
	a, e := parse(strings.TrimSpace(start))
	if e != nil {
		return nil, e
	}
	b, e := parse(strings.TrimSpace(end))
	if e != nil {
		return nil, e
	}
	if !b.After(a) || a.Nanosecond() != 0 || b.Nanosecond() != 0 || a.UTC().Year() < 1 || a.UTC().Year() > 9999 || b.UTC().Year() < 1 || b.UTC().Year() > 9999 {
		return nil, invalid
	}
	return []string{"DTSTART:" + a.UTC().Format("20060102T150405Z"), "DTEND:" + b.UTC().Format("20060102T150405Z")}, nil
}

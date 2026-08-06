// Package schedule decides whether the display should be awake at a given
// moment, and drives the display power abstraction accordingly.
//
// All arithmetic is done on wall-clock time in the configured location rather
// than by adding durations to UTC. That distinction is what makes daylight-saving
// transitions correct: on the March transition 02:00-03:00 local does not exist,
// and on the October transition 02:00-03:00 happens twice. Adding
// "8 hours after midnight UTC" silently lands on the wrong wall time on those
// two days each year.
package schedule

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Kind values for a rule.
const (
	KindWeekly = "weekly"
	KindDate   = "date"
)

// Rule is one schedule entry.
type Rule struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Weekday *int   `json:"weekday,omitempty"` // 0=Monday .. 6=Sunday (ISO)
	OnDate  string `json:"on_date,omitempty"` // YYYY-MM-DD
	Enabled bool   `json:"enabled"`
	OnTime  string `json:"on_time,omitempty"`  // HH:MM
	OffTime string `json:"off_time,omitempty"` // HH:MM
	Label   string `json:"label,omitempty"`
}

// Override is a temporary manual wake or sleep.
type Override struct {
	ID        int64     `json:"id"`
	Mode      string    `json:"mode"` // wake | sleep
	StartsAt  time.Time `json:"starts_at"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedBy string    `json:"created_by"`
}

// Decision is the resolved display state at a point in time.
type Decision struct {
	// On is whether the display should be powered.
	On bool `json:"on"`
	// Reason names the rule that decided it, for the dashboard.
	Reason string `json:"reason"`
	// Source is one of: override, date, weekly, default.
	Source string `json:"source"`
	// NextChange is when the state is next expected to flip, or zero if unknown.
	NextChange time.Time `json:"next_change,omitempty"`
}

// MaxLookaheadDays bounds the search for the next transition, so a schedule with
// every day disabled terminates instead of looping.
const MaxLookaheadDays = 8

// ParseHHMM parses "HH:MM" into minutes past midnight.
func ParseHHMM(s string) (int, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("time %q must be in HH:MM format", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("time %q has an invalid hour", s)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("time %q has an invalid minute", s)
	}
	return h*60 + m, nil
}

// FormatHHMM renders minutes past midnight.
func FormatHHMM(mins int) string {
	mins = ((mins % 1440) + 1440) % 1440
	return fmt.Sprintf("%02d:%02d", mins/60, mins%60)
}

// ValidateRule checks a rule is well formed.
func ValidateRule(r Rule) error {
	switch r.Kind {
	case KindWeekly:
		if r.Weekday == nil || *r.Weekday < 0 || *r.Weekday > 6 {
			return errors.New("a weekly rule needs a weekday between 0 (Monday) and 6 (Sunday)")
		}
	case KindDate:
		if _, err := time.Parse("2006-01-02", r.OnDate); err != nil {
			return errors.New("a date rule needs on_date in YYYY-MM-DD format")
		}
	default:
		return fmt.Errorf("unknown rule kind %q", r.Kind)
	}

	// A rule that is enabled but has no times means "off all day", which is a
	// legitimate way to blank the display on a closed day.
	if !r.Enabled || (r.OnTime == "" && r.OffTime == "") {
		return nil
	}
	if r.OnTime == "" || r.OffTime == "" {
		return errors.New("both on_time and off_time are required when either is set")
	}
	if _, err := ParseHHMM(r.OnTime); err != nil {
		return err
	}
	if _, err := ParseHHMM(r.OffTime); err != nil {
		return err
	}
	return nil
}

// ISOWeekday converts a Go weekday to ISO order (Monday = 0).
//
// Go numbers Sunday as 0. Using that value directly against the schema's
// Monday-first weekday column shifts every schedule by one day.
func ISOWeekday(d time.Weekday) int { return (int(d) + 6) % 7 }

// Engine resolves the schedule.
type Engine struct {
	loc *time.Location
}

// NewEngine builds an Engine for a location.
func NewEngine(loc *time.Location) *Engine {
	if loc == nil {
		loc = time.UTC
	}
	return &Engine{loc: loc}
}

// Location returns the engine's timezone.
func (e *Engine) Location() *time.Location { return e.loc }

// Decide resolves the display state at instant t.
//
// Precedence, highest first: an active manual override, a date-specific rule for
// that day, the weekly rule for that weekday, then the default (off).
func (e *Engine) Decide(t time.Time, rules []Rule, overrides []Override) Decision {
	local := t.In(e.loc)

	// 1. Manual override wins outright.
	if o, ok := activeOverride(t, overrides); ok {
		return Decision{
			On:         o.Mode == "wake",
			Reason:     fmt.Sprintf("manual %s until %s", o.Mode, o.ExpiresAt.In(e.loc).Format("15:04")),
			Source:     "override",
			NextChange: o.ExpiresAt,
		}
	}

	// 2. A date rule for today.
	dateStr := local.Format("2006-01-02")
	for _, r := range rules {
		if r.Kind == KindDate && r.OnDate == dateStr {
			return e.decideFromRule(local, r, "date")
		}
	}

	// 3. The weekly rule for today.
	iso := ISOWeekday(local.Weekday())
	for _, r := range rules {
		if r.Kind == KindWeekly && r.Weekday != nil && *r.Weekday == iso {
			return e.decideFromRule(local, r, "weekly")
		}
	}

	// 4. No rule: the display stays off. Defaulting to "on" would leave a screen
	// burning overnight whenever a rule is accidentally deleted.
	return Decision{
		On: false, Reason: "no schedule rule for today", Source: "default",
		NextChange: e.nextTransition(local, rules, false),
	}
}

func (e *Engine) decideFromRule(local time.Time, r Rule, source string) Decision {
	if !r.Enabled || r.OnTime == "" || r.OffTime == "" {
		return Decision{
			On: false, Reason: labelOr(r, "display off all day"), Source: source,
			NextChange: e.nextTransition(local, nil, false),
		}
	}
	on, _ := ParseHHMM(r.OnTime)
	off, _ := ParseHHMM(r.OffTime)
	nowMin := local.Hour()*60 + local.Minute()

	active := withinWindow(nowMin, on, off)
	return Decision{
		On:     active,
		Reason: labelOr(r, fmt.Sprintf("%s-%s", r.OnTime, r.OffTime)),
		Source: source,
	}
}

func labelOr(r Rule, fallback string) string {
	if strings.TrimSpace(r.Label) != "" {
		return r.Label
	}
	return fallback
}

// withinWindow reports whether nowMin falls inside [on, off).
//
// When off <= on the window crosses midnight: 18:00-02:00 is active from 18:00
// through 01:59 the next day. BeerMate runs festival and stadium bars that trade
// past midnight, so an on<off-only reading would be wrong for the company's own
// core use case.
func withinWindow(nowMin, on, off int) bool {
	if on == off {
		// A zero-length window is treated as "always on" rather than "never on":
		// an operator setting both to the same value means a 24 hour display.
		return true
	}
	if on < off {
		return nowMin >= on && nowMin < off
	}
	return nowMin >= on || nowMin < off
}

// CrossesMidnight reports whether a rule's window spans midnight.
func CrossesMidnight(r Rule) bool {
	if r.OnTime == "" || r.OffTime == "" {
		return false
	}
	on, err1 := ParseHHMM(r.OnTime)
	off, err2 := ParseHHMM(r.OffTime)
	if err1 != nil || err2 != nil {
		return false
	}
	return off < on
}

func activeOverride(t time.Time, overrides []Override) (Override, bool) {
	var best Override
	found := false
	for _, o := range overrides {
		if t.Before(o.StartsAt) || !t.Before(o.ExpiresAt) {
			continue
		}
		// The most recently created override wins if several overlap.
		if !found || o.StartsAt.After(best.StartsAt) || (o.StartsAt.Equal(best.StartsAt) && o.ID > best.ID) {
			best, found = o, true
		}
	}
	return best, found
}

// nextTransition finds the next moment the decision flips, searching forward day
// by day. It returns the zero time if nothing changes within MaxLookaheadDays.
func (e *Engine) nextTransition(local time.Time, rules []Rule, currentlyOn bool) time.Time {
	_ = currentlyOn
	if len(rules) == 0 {
		return time.Time{}
	}
	for d := 0; d < MaxLookaheadDays; d++ {
		day := local.AddDate(0, 0, d)
		r, ok := e.ruleFor(day, rules)
		if !ok || !r.Enabled || r.OnTime == "" {
			continue
		}
		on, err := ParseHHMM(r.OnTime)
		if err != nil {
			continue
		}
		// Build the wall-clock instant in the configured zone. time.Date resolves
		// non-existent and ambiguous local times per the zone's rules, which is
		// exactly the DST behaviour we want.
		cand := time.Date(day.Year(), day.Month(), day.Day(), on/60, on%60, 0, 0, e.loc)
		if cand.After(local) {
			return cand
		}
	}
	return time.Time{}
}

func (e *Engine) ruleFor(day time.Time, rules []Rule) (Rule, bool) {
	dateStr := day.Format("2006-01-02")
	for _, r := range rules {
		if r.Kind == KindDate && r.OnDate == dateStr {
			return r, true
		}
	}
	iso := ISOWeekday(day.Weekday())
	for _, r := range rules {
		if r.Kind == KindWeekly && r.Weekday != nil && *r.Weekday == iso {
			return r, true
		}
	}
	return Rule{}, false
}

// NextChange returns the next scheduled state change after t.
func (e *Engine) NextChange(t time.Time, rules []Rule, overrides []Override) time.Time {
	cur := e.Decide(t, rules, overrides)
	if !cur.NextChange.IsZero() {
		return cur.NextChange
	}

	// Step forward in one-minute increments only across the current and next day,
	// bounded so this can never spin. A minute of granularity matches the HH:MM
	// precision the schedule is expressed in.
	local := t.In(e.loc)
	limit := local.AddDate(0, 0, 2)
	for probe := local.Add(time.Minute); probe.Before(limit); probe = probe.Add(time.Minute) {
		if e.Decide(probe, rules, overrides).On != cur.On {
			return probe.Truncate(time.Minute)
		}
	}
	return time.Time{}
}

// SortRules orders rules for stable presentation.
func SortRules(rules []Rule) {
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Kind != rules[j].Kind {
			return rules[i].Kind < rules[j].Kind
		}
		if rules[i].Kind == KindWeekly {
			wi, wj := 0, 0
			if rules[i].Weekday != nil {
				wi = *rules[i].Weekday
			}
			if rules[j].Weekday != nil {
				wj = *rules[j].Weekday
			}
			return wi < wj
		}
		return rules[i].OnDate < rules[j].OnDate
	})
}

// DefaultRules returns the legacy kiosk schedule: 08:00-17:00 every day.
func DefaultRules() []Rule {
	out := make([]Rule, 0, 7)
	for i := 0; i < 7; i++ {
		wd := i
		out = append(out, Rule{
			Kind: KindWeekly, Weekday: &wd, Enabled: true,
			OnTime: "08:00", OffTime: "17:00",
		})
	}
	return out
}

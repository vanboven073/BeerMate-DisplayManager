package schedule

import (
	"testing"
	"time"
	_ "time/tzdata" // guarantee Europe/Amsterdam resolves regardless of host zoneinfo
)

func amsterdam(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatalf("Europe/Amsterdam unavailable: %v (is time/tzdata imported?)", err)
	}
	return loc
}

func wd(n int) *int { return &n }

// office is the legacy kiosk schedule: 08:00-17:00 every day.
func office() []Rule { return DefaultRules() }

func at(t *testing.T, loc *time.Location, s string) time.Time {
	t.Helper()
	ts, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return ts
}

func TestParseAndFormatHHMM(t *testing.T) {
	cases := map[string]int{"00:00": 0, "08:00": 480, "17:00": 1020, "23:59": 1439}
	for s, want := range cases {
		got, err := ParseHHMM(s)
		if err != nil {
			t.Errorf("ParseHHMM(%q): %v", s, err)
			continue
		}
		if got != want {
			t.Errorf("ParseHHMM(%q) = %d, want %d", s, got, want)
		}
		if back := FormatHHMM(got); back != s {
			t.Errorf("FormatHHMM(%d) = %q, want %q", got, back, s)
		}
	}
	for _, bad := range []string{"", "8", "8:00:00", "24:00", "08:60", "-1:00", "aa:bb"} {
		if _, err := ParseHHMM(bad); err == nil {
			t.Errorf("ParseHHMM(%q) = nil error, want failure", bad)
		}
	}
}

// Go numbers Sunday as 0; the schema and UI use Monday-first. A mistake here
// shifts every schedule by exactly one day.
func TestISOWeekday(t *testing.T) {
	cases := []struct {
		in   time.Weekday
		want int
	}{
		{time.Monday, 0}, {time.Tuesday, 1}, {time.Wednesday, 2},
		{time.Thursday, 3}, {time.Friday, 4}, {time.Saturday, 5}, {time.Sunday, 6},
	}
	for _, c := range cases {
		if got := ISOWeekday(c.in); got != c.want {
			t.Errorf("ISOWeekday(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestOfficeHoursBoundaries(t *testing.T) {
	loc := amsterdam(t)
	e := NewEngine(loc)
	rules := office()

	cases := []struct {
		when string
		want bool
		why  string
	}{
		{"2026-07-24 07:59", false, "one minute before opening"},
		{"2026-07-24 08:00", true, "exactly at opening: inclusive"},
		{"2026-07-24 08:01", true, "just after opening"},
		{"2026-07-24 12:00", true, "midday"},
		{"2026-07-24 16:59", true, "one minute before closing"},
		{"2026-07-24 17:00", false, "exactly at closing: exclusive"},
		{"2026-07-24 17:01", false, "just after closing"},
		{"2026-07-24 00:00", false, "midnight"},
		{"2026-07-24 23:59", false, "late evening"},
	}
	for _, c := range cases {
		got := e.Decide(at(t, loc, c.when), rules, nil)
		if got.On != c.want {
			t.Errorf("%s (%s): display on = %v, want %v", c.when, c.why, got.On, c.want)
		}
	}
}

// ---- daylight saving -----------------------------------------------------

// On the March transition the clock jumps 02:00 -> 03:00, so 02:00-02:59 local
// never occurs. An 08:00-17:00 window must still open at 08:00 wall clock, which
// is one hour earlier in UTC than the day before.
func TestSpringForwardKeepsWallClockSchedule(t *testing.T) {
	loc := amsterdam(t)
	e := NewEngine(loc)
	rules := office()

	// 2026-03-29 is the European spring transition.
	transition := "2026-03-29"
	for _, c := range []struct {
		when string
		want bool
	}{
		{transition + " 07:59", false},
		{transition + " 08:00", true},
		{transition + " 16:59", true},
		{transition + " 17:00", false},
	} {
		got := e.Decide(at(t, loc, c.when), rules, nil)
		if got.On != c.want {
			t.Errorf("spring-forward day %s: on = %v, want %v", c.when, got.On, c.want)
		}
	}

	// The UTC offset genuinely changed across the transition, which is the whole
	// point: the same wall time maps to a different instant.
	before := at(t, loc, "2026-03-28 08:00")
	after := at(t, loc, transition+" 08:00")
	_, offBefore := before.Zone()
	_, offAfter := after.Zone()
	if offBefore == offAfter {
		t.Fatalf("expected the UTC offset to change across the spring transition; "+
			"both days report %d seconds - the tzdata may be wrong", offBefore)
	}
	if after.Sub(before) != 23*time.Hour {
		t.Errorf("08:00 to 08:00 across spring-forward = %v, want 23h "+
			"(the day is one hour shorter)", after.Sub(before))
	}
}

// On the October transition 02:00-02:59 local occurs twice. The schedule must
// still be evaluated on wall clock, and the day is 25 hours long.
func TestFallBackKeepsWallClockSchedule(t *testing.T) {
	loc := amsterdam(t)
	e := NewEngine(loc)
	rules := office()

	transition := "2026-10-25" // European autumn transition
	for _, c := range []struct {
		when string
		want bool
	}{
		{transition + " 07:59", false},
		{transition + " 08:00", true},
		{transition + " 16:59", true},
		{transition + " 17:00", false},
	} {
		got := e.Decide(at(t, loc, c.when), rules, nil)
		if got.On != c.want {
			t.Errorf("fall-back day %s: on = %v, want %v", c.when, got.On, c.want)
		}
	}

	before := at(t, loc, "2026-10-24 08:00")
	after := at(t, loc, transition+" 08:00")
	if after.Sub(before) != 25*time.Hour {
		t.Errorf("08:00 to 08:00 across fall-back = %v, want 25h "+
			"(the day is one hour longer)", after.Sub(before))
	}
}

// The ambiguous hour itself must still resolve deterministically rather than
// flapping. Both instants that read 02:30 local are outside office hours.
func TestAmbiguousHourResolvesConsistently(t *testing.T) {
	loc := amsterdam(t)
	e := NewEngine(loc)
	rules := office()

	// 00:30 UTC and 01:30 UTC on the transition day both render as 02:30 local.
	first := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)
	second := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)

	if got := first.In(loc).Format("15:04"); got != "02:30" {
		t.Fatalf("first instant renders as %s, want 02:30", got)
	}
	if got := second.In(loc).Format("15:04"); got != "02:30" {
		t.Fatalf("second instant renders as %s, want 02:30", got)
	}
	d1 := e.Decide(first, rules, nil)
	d2 := e.Decide(second, rules, nil)
	if d1.On != d2.On {
		t.Error("the two occurrences of the ambiguous hour produced different decisions")
	}
	if d1.On {
		t.Error("02:30 is outside 08:00-17:00 and should be off")
	}
}

// ---- midnight-crossing windows -------------------------------------------

// BeerMate runs festival and stadium bars that trade past midnight. A schedule
// model that assumes on < off is silently wrong for the company's core use case.
func TestOvernightWindow(t *testing.T) {
	loc := amsterdam(t)
	e := NewEngine(loc)

	rules := make([]Rule, 0, 7)
	for i := 0; i < 7; i++ {
		w := i
		rules = append(rules, Rule{
			Kind: KindWeekly, Weekday: &w, Enabled: true,
			OnTime: "18:00", OffTime: "02:00",
		})
	}

	cases := []struct {
		when string
		want bool
		why  string
	}{
		{"2026-07-24 17:59", false, "before opening"},
		{"2026-07-24 18:00", true, "at opening"},
		{"2026-07-24 23:59", true, "just before midnight"},
		{"2026-07-25 00:00", true, "just after midnight, still the same session"},
		{"2026-07-25 01:59", true, "one minute before closing"},
		{"2026-07-25 02:00", false, "at closing"},
		{"2026-07-25 12:00", false, "middle of the following day"},
	}
	for _, c := range cases {
		got := e.Decide(at(t, loc, c.when), rules, nil)
		if got.On != c.want {
			t.Errorf("%s (%s): on = %v, want %v", c.when, c.why, got.On, c.want)
		}
	}
}

func TestCrossesMidnightDetection(t *testing.T) {
	if !CrossesMidnight(Rule{OnTime: "18:00", OffTime: "02:00"}) {
		t.Error("18:00-02:00 should be detected as crossing midnight")
	}
	if CrossesMidnight(Rule{OnTime: "08:00", OffTime: "17:00"}) {
		t.Error("08:00-17:00 should not be detected as crossing midnight")
	}
}

func TestWithinWindow(t *testing.T) {
	cases := []struct {
		now, on, off int
		want         bool
	}{
		{480, 480, 1020, true},   // exactly at open
		{1020, 480, 1020, false}, // exactly at close
		{479, 480, 1020, false},
		{0, 1080, 120, true},   // 00:00 inside 18:00-02:00
		{119, 1080, 120, true}, // 01:59 inside
		{120, 1080, 120, false},
		{1079, 1080, 120, false},
		{1080, 1080, 120, true},
		{600, 600, 600, true}, // zero-length window means 24h
	}
	for _, c := range cases {
		if got := withinWindow(c.now, c.on, c.off); got != c.want {
			t.Errorf("withinWindow(%d, %d, %d) = %v, want %v", c.now, c.on, c.off, got, c.want)
		}
	}
}

// ---- precedence ----------------------------------------------------------

func TestOverrideBeatsSchedule(t *testing.T) {
	loc := amsterdam(t)
	e := NewEngine(loc)
	rules := office()

	night := at(t, loc, "2026-07-24 22:00")
	if e.Decide(night, rules, nil).On {
		t.Fatal("22:00 should be outside office hours")
	}

	wake := Override{
		ID: 1, Mode: "wake",
		StartsAt:  night.Add(-time.Hour),
		ExpiresAt: night.Add(time.Hour),
	}
	d := e.Decide(night, rules, []Override{wake})
	if !d.On {
		t.Error("a manual wake override should turn the display on outside hours")
	}
	if d.Source != "override" {
		t.Errorf("decision source = %q, want override", d.Source)
	}

	// It must expire on its own so a screen is never left on overnight.
	after := e.Decide(night.Add(2*time.Hour), rules, []Override{wake})
	if after.On {
		t.Error("the override should have expired")
	}
}

func TestSleepOverrideDuringOfficeHours(t *testing.T) {
	loc := amsterdam(t)
	e := NewEngine(loc)
	rules := office()

	noon := at(t, loc, "2026-07-24 12:00")
	if !e.Decide(noon, rules, nil).On {
		t.Fatal("noon should be inside office hours")
	}
	sleep := Override{
		ID: 1, Mode: "sleep",
		StartsAt: noon.Add(-time.Minute), ExpiresAt: noon.Add(time.Hour),
	}
	if e.Decide(noon, rules, []Override{sleep}).On {
		t.Error("a manual sleep override should blank the display during office hours")
	}
}

func TestDateRuleBeatsWeeklyRule(t *testing.T) {
	loc := amsterdam(t)
	e := NewEngine(loc)

	rules := append(office(), Rule{
		Kind: KindDate, OnDate: "2026-12-25", Enabled: false, Label: "Christmas Day",
	})
	christmasNoon := at(t, loc, "2026-12-25 12:00")
	d := e.Decide(christmasNoon, rules, nil)
	if d.On {
		t.Error("a disabled date rule should override the weekly schedule")
	}
	if d.Source != "date" {
		t.Errorf("decision source = %q, want date", d.Source)
	}

	// The following day reverts to the weekly rule.
	if !e.Decide(at(t, loc, "2026-12-26 12:00"), rules, nil).On {
		t.Error("the day after a holiday should follow the weekly schedule again")
	}
}

// A deleted or missing rule must fail safe to "off". Defaulting to "on" would
// leave a screen burning overnight after a configuration mistake.
func TestNoRuleDefaultsToOff(t *testing.T) {
	loc := amsterdam(t)
	e := NewEngine(loc)
	d := e.Decide(at(t, loc, "2026-07-24 12:00"), nil, nil)
	if d.On {
		t.Error("with no rules the display should default to off")
	}
	if d.Source != "default" {
		t.Errorf("decision source = %q, want default", d.Source)
	}
}

func TestDisabledDayIsOffAllDay(t *testing.T) {
	loc := amsterdam(t)
	e := NewEngine(loc)

	rules := office()
	// Disable Sunday (ISO 6).
	for i := range rules {
		if rules[i].Weekday != nil && *rules[i].Weekday == 6 {
			rules[i].Enabled = false
		}
	}
	// 2026-07-26 is a Sunday.
	for _, hhmm := range []string{"00:00", "08:00", "12:00", "16:59", "23:59"} {
		if e.Decide(at(t, loc, "2026-07-26 "+hhmm), rules, nil).On {
			t.Errorf("Sunday %s: display on, want off all day", hhmm)
		}
	}
	// Monday still works.
	if !e.Decide(at(t, loc, "2026-07-27 12:00"), rules, nil).On {
		t.Error("Monday should be unaffected by disabling Sunday")
	}
}

func TestMostRecentOverrideWins(t *testing.T) {
	loc := amsterdam(t)
	e := NewEngine(loc)
	now := at(t, loc, "2026-07-24 22:00")

	older := Override{ID: 1, Mode: "wake",
		StartsAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(time.Hour)}
	newer := Override{ID: 2, Mode: "sleep",
		StartsAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}

	if e.Decide(now, office(), []Override{older, newer}).On {
		t.Error("the more recently created override should win")
	}
}

// ---- validation ----------------------------------------------------------

func TestValidateRule(t *testing.T) {
	tests := []struct {
		name    string
		rule    Rule
		wantErr bool
	}{
		{"valid weekly", Rule{Kind: KindWeekly, Weekday: wd(0), Enabled: true,
			OnTime: "08:00", OffTime: "17:00"}, false},
		{"valid overnight", Rule{Kind: KindWeekly, Weekday: wd(4), Enabled: true,
			OnTime: "18:00", OffTime: "02:00"}, false},
		{"weekly without weekday", Rule{Kind: KindWeekly, Enabled: true,
			OnTime: "08:00", OffTime: "17:00"}, true},
		{"weekday out of range", Rule{Kind: KindWeekly, Weekday: wd(7), Enabled: true,
			OnTime: "08:00", OffTime: "17:00"}, true},
		{"disabled needs no times", Rule{Kind: KindWeekly, Weekday: wd(0), Enabled: false}, false},
		{"only on_time set", Rule{Kind: KindWeekly, Weekday: wd(0), Enabled: true,
			OnTime: "08:00"}, true},
		{"valid date rule", Rule{Kind: KindDate, OnDate: "2026-12-25", Enabled: false}, false},
		{"bad date format", Rule{Kind: KindDate, OnDate: "25-12-2026", Enabled: false}, true},
		{"unknown kind", Rule{Kind: "monthly", Enabled: true}, true},
		{"bad time", Rule{Kind: KindWeekly, Weekday: wd(0), Enabled: true,
			OnTime: "25:00", OffTime: "17:00"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRule(tt.rule)
			if tt.wantErr && err == nil {
				t.Error("expected an error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// ---- next transition -----------------------------------------------------

func TestNextChange(t *testing.T) {
	loc := amsterdam(t)
	e := NewEngine(loc)
	rules := office()

	// Before opening, the next change is today's 08:00.
	next := e.NextChange(at(t, loc, "2026-07-24 06:00"), rules, nil)
	if next.IsZero() {
		t.Fatal("no next change found before opening")
	}
	if got := next.In(loc).Format("2006-01-02 15:04"); got != "2026-07-24 08:00" {
		t.Errorf("next change = %s, want 2026-07-24 08:00", got)
	}

	// During the day, the next change is 17:00.
	next = e.NextChange(at(t, loc, "2026-07-24 12:00"), rules, nil)
	if got := next.In(loc).Format("2006-01-02 15:04"); got != "2026-07-24 17:00" {
		t.Errorf("next change = %s, want 2026-07-24 17:00", got)
	}
}

// A schedule with every day disabled must terminate rather than search forever.
func TestNextChangeTerminatesWithNoActiveDays(t *testing.T) {
	loc := amsterdam(t)
	e := NewEngine(loc)
	rules := office()
	for i := range rules {
		rules[i].Enabled = false
	}
	done := make(chan time.Time, 1)
	go func() { done <- e.NextChange(at(t, loc, "2026-07-24 12:00"), rules, nil) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("NextChange did not terminate with all days disabled")
	}
}

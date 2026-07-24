package content

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ---- layouts -------------------------------------------------------------

// The specification requires 16 layouts. Losing one silently would remove a
// documented capability, so the count and the required IDs are asserted.
func TestAllRequiredLayoutsPresent(t *testing.T) {
	required := []string{
		LayoutFullscreen, LayoutCols5050, LayoutCols6040, LayoutCols4060,
		LayoutRows5050, LayoutMainRightSide, LayoutMainLeftSide,
		LayoutMainBottomTick, LayoutMainTopBanner, LayoutQuad,
		LayoutLeftTwoRight, LayoutTopTwoBottom, LayoutDashboardSocial,
		LayoutImageCountdown, LayoutWebsiteKPI, LayoutCustomGrid,
	}
	if got := len(Layouts()); got != len(required) {
		t.Errorf("Layouts() returned %d layouts, want %d", got, len(required))
	}
	for _, id := range required {
		if _, ok := LookupLayout(id); !ok {
			t.Errorf("required layout %q is missing", id)
		}
	}
}

// Every fixed layout must tile the screen exactly: no gaps (which show as
// unexplained black bands) and no overlaps (which silently hide content).
func TestFixedLayoutsTileTheScreenExactly(t *testing.T) {
	for _, l := range Layouts() {
		if l.Custom {
			continue
		}
		t.Run(l.ID, func(t *testing.T) {
			if len(l.Slots) == 0 {
				t.Fatal("fixed layout has no slots")
			}
			rects := make(map[string]Rect, len(l.Slots))
			var area float64
			for _, s := range l.Slots {
				rects[s.ID] = s.Rect
				area += s.Rect.W * s.Rect.H
			}
			if err := ValidateGeometry(rects); err != nil {
				t.Errorf("layout geometry invalid: %v", err)
			}
			if diff := area - 100*100; diff > 0.5 || diff < -0.5 {
				t.Errorf("slots cover %.1f%% of the screen, want 100%% "+
					"(gaps appear as black bands on the display)", area/100)
			}
		})
	}
}

func TestLayoutSlotIDsUnique(t *testing.T) {
	for _, l := range Layouts() {
		seen := map[string]bool{}
		for _, s := range l.Slots {
			if seen[s.ID] {
				t.Errorf("layout %q has duplicate slot %q", l.ID, s.ID)
			}
			seen[s.ID] = true
		}
	}
}

func TestValidateGeometry(t *testing.T) {
	tests := []struct {
		name    string
		rects   map[string]Rect
		wantErr bool
	}{
		{"single full", map[string]Rect{"a": {0, 0, 100, 100}}, false},
		{"adjacent halves", map[string]Rect{
			"a": {0, 0, 50, 100}, "b": {50, 0, 50, 100}}, false},
		{"overlapping", map[string]Rect{
			"a": {0, 0, 60, 100}, "b": {50, 0, 50, 100}}, true},
		{"off screen right", map[string]Rect{"a": {60, 0, 50, 100}}, true},
		{"negative origin", map[string]Rect{"a": {-10, 0, 50, 100}}, true},
		{"zero width", map[string]Rect{"a": {0, 0, 0, 100}}, true},
		{"quadrants", map[string]Rect{
			"a": {0, 0, 50, 50}, "b": {50, 0, 50, 50},
			"c": {0, 50, 50, 50}, "d": {50, 50, 50, 50}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGeometry(tt.rects)
			if tt.wantErr && err == nil {
				t.Error("expected an error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// ---- weekday mask --------------------------------------------------------

// Go counts Sunday as weekday 0 while the schema uses ISO order (Monday first).
// Getting this wrong shifts every schedule by a day, which is exactly the kind of
// bug that only shows up in production on the wrong morning.
func TestWeekdayMaskUsesISOOrder(t *testing.T) {
	cases := []struct {
		day time.Weekday
		bit int
	}{
		{time.Monday, 1 << 0},
		{time.Tuesday, 1 << 1},
		{time.Wednesday, 1 << 2},
		{time.Thursday, 1 << 3},
		{time.Friday, 1 << 4},
		{time.Saturday, 1 << 5},
		{time.Sunday, 1 << 6},
	}
	for _, c := range cases {
		if got := WeekdayBit(c.day); got != c.bit {
			t.Errorf("WeekdayBit(%v) = %d, want %d", c.day, got, c.bit)
		}
		if !DayEnabled(AllDaysMask, c.day) {
			t.Errorf("DayEnabled(AllDaysMask, %v) = false", c.day)
		}
		if !DayEnabled(c.bit, c.day) {
			t.Errorf("DayEnabled(%d, %v) = false", c.bit, c.day)
		}
		if DayEnabled(AllDaysMask&^c.bit, c.day) {
			t.Errorf("DayEnabled(mask without %v, %v) = true", c.day, c.day)
		}
	}
}

func TestValidateDaysMask(t *testing.T) {
	if err := ValidateDaysMask(0); err == nil {
		t.Error("an empty mask should be rejected: the scene would never play")
	}
	if err := ValidateDaysMask(AllDaysMask); err != nil {
		t.Errorf("AllDaysMask rejected: %v", err)
	}
	if err := ValidateDaysMask(128); err == nil {
		t.Error("an out-of-range mask should be rejected")
	}
}

// ---- URL validation ------------------------------------------------------

func TestValidateHTTPURL(t *testing.T) {
	tests := []struct {
		url       string
		allowHTTP bool
		wantErr   bool
		why       string
	}{
		{"https://dashboard.beermatecloud.eu/x", false, false, "plain https"},
		{"http://example.com", false, true, "http rejected when not allowed"},
		{"http://example.com", true, false, "http allowed when opted in"},
		{"ftp://example.com", true, true, "unsupported scheme"},
		{"javascript:alert(1)", true, true, "javascript scheme"},
		{"data:text/html,<script>", true, true, "data scheme"},
		{"https://user:pass@example.com", false, true, "embedded credentials"},
		{"https://", false, true, "no host"},
		{"", false, true, "empty"},
		{"not a url at all", false, true, "no scheme"},
		{"https://" + strings.Repeat("a", MaxURLLen), false, true, "too long"},
	}
	for _, tt := range tests {
		err := ValidateHTTPURL(tt.url, tt.allowHTTP)
		if tt.wantErr && err == nil {
			t.Errorf("ValidateHTTPURL(%q, %v) = nil, want error (%s)", tt.url, tt.allowHTTP, tt.why)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("ValidateHTTPURL(%q, %v) = %v, want nil (%s)", tt.url, tt.allowHTTP, err, tt.why)
		}
	}
}

// ---- colour validation ---------------------------------------------------

// Colours are interpolated into inline styles in the player, so anything other
// than a strict hex value is a CSS injection vector.
func TestColourValidationRejectsCSSInjection(t *testing.T) {
	bad := []string{
		"red", "rgb(1,2,3)", "url(https://evil/x)", "#12345",
		"#E86514; background:url(x)", "expression(alert(1))", "#GGGGGG",
		"var(--x)", "#E8651",
	}
	for _, v := range bad {
		if errs := checkColor("background", v); len(errs) == 0 {
			t.Errorf("checkColor(%q) accepted a non-hex value", v)
		}
	}
	for _, v := range []string{"", "#fff", "#E86514", "#0E1821FF", "#abc"} {
		if errs := checkColor("background", v); len(errs) != 0 {
			t.Errorf("checkColor(%q) rejected a valid hex colour: %v", v, errs)
		}
	}
}

// ---- zone validation -----------------------------------------------------

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestValidateZoneImage(t *testing.T) {
	cfg := mustJSON(t, ImageConfig{Fit: "contain", Title: "Roadmap"})
	refs, err := ValidateZone(TypeImage, "42", cfg)
	if err != nil {
		t.Fatalf("valid image zone rejected: %v", err)
	}
	if len(refs.MediaIDs) != 1 || refs.MediaIDs[0] != 42 {
		t.Errorf("media refs = %v, want [42]", refs.MediaIDs)
	}

	if _, err := ValidateZone(TypeImage, "", cfg); err == nil {
		t.Error("image zone without a media reference was accepted")
	}
	if _, err := ValidateZone(TypeImage, "not-a-number", cfg); err == nil {
		t.Error("image zone with a non-numeric reference was accepted")
	}
	if _, err := ValidateZone(TypeImage, "42", `{"fit":"nonsense"}`); err == nil {
		t.Error("invalid fit value was accepted")
	}
}

// A typo in a config key must be reported, not silently ignored: otherwise the
// operator sees a slide that does not match what they configured.
func TestValidateZoneRejectsUnknownConfigFields(t *testing.T) {
	if _, err := ValidateZone(TypeImage, "1", `{"fit":"contain","ft":"cover"}`); err == nil {
		t.Error("unknown config field was silently accepted")
	}
}

func TestValidateZoneCountdown(t *testing.T) {
	good := mustJSON(t, CountdownConfig{
		Title: "BeerMate launch", TargetRFC3339: "2026-08-14T00:00:00+02:00",
		Timezone: "Europe/Amsterdam",
	})
	if _, err := ValidateZone(TypeCountdown, "", good); err != nil {
		t.Errorf("valid countdown rejected: %v", err)
	}

	noTitle := mustJSON(t, CountdownConfig{
		TargetRFC3339: "2026-08-14T00:00:00+02:00", Timezone: "Europe/Amsterdam"})
	if _, err := ValidateZone(TypeCountdown, "", noTitle); err == nil {
		t.Error("countdown without a title was accepted")
	}

	badTarget := mustJSON(t, CountdownConfig{
		Title: "x", TargetRFC3339: "14 August 2026", Timezone: "Europe/Amsterdam"})
	if _, err := ValidateZone(TypeCountdown, "", badTarget); err == nil {
		t.Error("countdown with a non-RFC3339 target was accepted")
	}

	badTZ := mustJSON(t, CountdownConfig{
		Title: "x", TargetRFC3339: "2026-08-14T00:00:00Z", Timezone: "Mars/Olympus"})
	if _, err := ValidateZone(TypeCountdown, "", badTZ); err == nil {
		t.Error("countdown with an unknown timezone was accepted")
	}
}

func TestValidateZoneKPI(t *testing.T) {
	cards := make([]KPICard, MaxKPICards)
	for i := range cards {
		cards[i] = KPICard{Label: "Metric", Value: "1"}
	}
	if _, err := ValidateZone(TypeKPI, "", mustJSON(t, KPIConfig{
		Source: "manual", Cards: cards})); err != nil {
		t.Errorf("KPI zone at the card limit rejected: %v", err)
	}

	if _, err := ValidateZone(TypeKPI, "", mustJSON(t, KPIConfig{
		Source: "manual", Cards: append(cards, KPICard{Label: "x", Value: "1"})})); err == nil {
		t.Errorf("KPI zone with more than %d cards was accepted", MaxKPICards)
	}

	if _, err := ValidateZone(TypeKPI, "", mustJSON(t, KPIConfig{
		Source: "manual", Cards: nil})); err == nil {
		t.Error("KPI zone with no cards was accepted")
	}

	// API-backed KPI must have a valid https URL.
	if _, err := ValidateZone(TypeKPI, "", mustJSON(t, KPIConfig{
		Source: "api", URL: "not-a-url", Cards: cards[:1]})); err == nil {
		t.Error("API KPI with an invalid URL was accepted")
	}

	// Refresh interval must respect upstream rate limits.
	if _, err := ValidateZone(TypeKPI, "", mustJSON(t, KPIConfig{
		Source: "api", URL: "https://api.example.com/k", RefreshSec: 1,
		Cards: cards[:1]})); err == nil {
		t.Error("API KPI with a 1 second refresh was accepted")
	}
}

func TestValidateZoneQR(t *testing.T) {
	if _, err := ValidateZone(TypeQR, "", mustJSON(t, QRConfig{
		Kind: "url", Data: "https://beermate.events", ECLevel: "M"})); err != nil {
		t.Errorf("valid QR zone rejected: %v", err)
	}
	if _, err := ValidateZone(TypeQR, "", mustJSON(t, QRConfig{
		Kind: "url", Data: "javascript:alert(1)"})); err == nil {
		t.Error("QR zone encoding a javascript: URL was accepted")
	}
	if _, err := ValidateZone(TypeQR, "", mustJSON(t, QRConfig{
		Kind: "text", Data: strings.Repeat("x", MaxQRDataLen+1)})); err == nil {
		t.Error("QR zone with unscannable payload length was accepted")
	}
	if _, err := ValidateZone(TypeQR, "", mustJSON(t, QRConfig{
		Kind: "text", Data: ""})); err == nil {
		t.Error("QR zone with no data was accepted")
	}
}

func TestValidateZoneUnknownType(t *testing.T) {
	if _, err := ValidateZone("hologram", "", "{}"); err == nil {
		t.Error("unknown content type was accepted")
	}
}

// ---- scene validation ----------------------------------------------------

func fullscreenScene(t *testing.T) *Scene {
	t.Helper()
	return &Scene{
		Name: "Roadmap", Enabled: true, Layout: LayoutFullscreen,
		DurationMS: 60000, DaysMask: AllDaysMask, Transition: "fade",
		Zones: []Zone{{
			Slot: "a", ContentType: TypeImage, ContentRef: "1",
			Config: `{"fit":"contain"}`,
		}},
	}
}

func TestValidateSceneHappyPath(t *testing.T) {
	sc := fullscreenScene(t)
	refs, err := ValidateScene(sc)
	if err != nil {
		t.Fatalf("valid scene rejected: %v", err)
	}
	if len(refs.MediaIDs) != 1 {
		t.Errorf("refs.MediaIDs = %v, want one entry", refs.MediaIDs)
	}
	// Geometry must be taken from the layout, not from the client.
	if sc.Zones[0].Rect != (Rect{0, 0, 100, 100}) {
		t.Errorf("zone rect = %+v, want the layout's full-screen rect", sc.Zones[0].Rect)
	}
}

// A fixed layout's geometry is authoritative. If a client could supply its own
// rect, a saved scene would drift away from the template it claims to use.
func TestFixedLayoutOverridesClientGeometry(t *testing.T) {
	sc := fullscreenScene(t)
	sc.Zones[0].Rect = Rect{10, 10, 20, 20} // client tries to override
	if _, err := ValidateScene(sc); err != nil {
		t.Fatal(err)
	}
	if sc.Zones[0].Rect != (Rect{0, 0, 100, 100}) {
		t.Errorf("client geometry survived validation: %+v", sc.Zones[0].Rect)
	}
}

func TestValidateSceneRequiresEveryLayoutSlot(t *testing.T) {
	sc := fullscreenScene(t)
	sc.Layout = LayoutCols5050 // needs slots a and b, only a supplied
	err := ValidateScene2Err(t, sc)
	if err == nil {
		t.Fatal("scene missing a required slot was accepted")
	}
	if !strings.Contains(err.Error(), "slot") {
		t.Errorf("error should mention the missing slot, got: %v", err)
	}
}

func ValidateScene2Err(t *testing.T, sc *Scene) error {
	t.Helper()
	_, err := ValidateScene(sc)
	return err
}

func TestValidateSceneRejectsUnknownSlot(t *testing.T) {
	sc := fullscreenScene(t)
	sc.Zones[0].Slot = "zz"
	if _, err := ValidateScene(sc); err == nil {
		t.Error("scene with a slot the layout does not define was accepted")
	}
}

// A scene where every zone is empty renders as a black screen, which from across
// a room is indistinguishable from a crashed player.
func TestValidateSceneRejectsAllEmptyZones(t *testing.T) {
	sc := fullscreenScene(t)
	sc.Zones[0].ContentType = TypeEmpty
	sc.Zones[0].ContentRef = ""
	if _, err := ValidateScene(sc); err == nil {
		t.Error("scene with no content in any zone was accepted")
	}
}

func TestValidateSceneDuration(t *testing.T) {
	sc := fullscreenScene(t)
	sc.DurationMS = 100
	if _, err := ValidateScene(sc); err == nil {
		t.Error("scene with a sub-second duration was accepted")
	}
	sc.DurationMS = MaxDurationMS + 1
	if _, err := ValidateScene(sc); err == nil {
		t.Error("scene with an absurd duration was accepted")
	}
}

func TestValidateSceneActiveWindow(t *testing.T) {
	sc := fullscreenScene(t)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	sc.ActiveFrom, sc.ActiveUntil = &from, &until
	if _, err := ValidateScene(sc); err == nil {
		t.Error("scene whose active_until precedes active_from was accepted")
	}
}

func TestValidateCustomGridZoneCap(t *testing.T) {
	sc := &Scene{
		Name: "Grid", Enabled: true, Layout: LayoutCustomGrid,
		DurationMS: 30000, DaysMask: AllDaysMask,
	}
	// MaxCustomZones + 1 thin horizontal strips: legal geometry, too many zones.
	n := MaxCustomZones + 1
	h := 100.0 / float64(n)
	for i := 0; i < n; i++ {
		sc.Zones = append(sc.Zones, Zone{
			Slot:        string(rune('a' + i)),
			Rect:        Rect{0, float64(i) * h, 100, h},
			ContentType: TypeText,
			Config:      `{"heading":"x"}`,
		})
	}
	if _, err := ValidateScene(sc); err == nil {
		t.Errorf("custom grid with %d zones was accepted, cap is %d", n, MaxCustomZones)
	}
}

func TestValidateCustomGridRejectsOverlap(t *testing.T) {
	sc := &Scene{
		Name: "Grid", Enabled: true, Layout: LayoutCustomGrid,
		DurationMS: 30000, DaysMask: AllDaysMask,
		Zones: []Zone{
			{Slot: "a", Rect: Rect{0, 0, 60, 100}, ContentType: TypeText, Config: `{"heading":"a"}`},
			{Slot: "b", Rect: Rect{50, 0, 50, 100}, ContentType: TypeText, Config: `{"heading":"b"}`},
		},
	}
	if _, err := ValidateScene(sc); err == nil {
		t.Error("custom grid with overlapping zones was accepted; content would be hidden")
	}
}

func TestSceneActiveAt(t *testing.T) {
	base := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC) // a Friday
	sc := fullscreenScene(t)
	sc.Valid = true

	if !sc.ActiveAt(base) {
		t.Error("enabled scene with an all-days mask should be active")
	}

	sc.Enabled = false
	if sc.ActiveAt(base) {
		t.Error("disabled scene should not be active")
	}
	sc.Enabled = true

	sc.Valid = false
	if sc.ActiveAt(base) {
		t.Error("invalid scene should not be active")
	}
	sc.Valid = true

	// Friday excluded from the mask.
	sc.DaysMask = AllDaysMask &^ WeekdayBit(time.Friday)
	if sc.ActiveAt(base) {
		t.Error("scene should not be active on an excluded weekday")
	}
	sc.DaysMask = AllDaysMask

	future := base.Add(48 * time.Hour)
	sc.ActiveFrom = &future
	if sc.ActiveAt(base) {
		t.Error("scene should not be active before active_from")
	}
	sc.ActiveFrom = nil

	past := base.Add(-time.Hour)
	sc.ActiveUntil = &past
	if sc.ActiveAt(base) {
		t.Error("scene should not be active after active_until")
	}
}

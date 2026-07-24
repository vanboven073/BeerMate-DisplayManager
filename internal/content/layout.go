// Package content defines the scene/zone model, the split-screen layouts and
// validation for every content type.
//
// The central idea: every playlist entry is a Scene containing one or more Zones.
// A traditional fullscreen slide is a Scene with a single zone covering 100x100.
// Split-screen therefore needs no special-casing anywhere in scheduling,
// validation, publishing or rendering.
package content

import (
	"fmt"
	"sort"
)

// Rect is zone geometry as percentages of the screen.
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Slot is a named position inside a layout.
type Slot struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Rect  Rect   `json:"rect"`
	// Hint tells the editor which content types suit this slot, so the UI can
	// lead with sensible options rather than presenting all fifteen every time.
	Hint []string `json:"hint,omitempty"`
}

// Layout is a named screen composition.
type Layout struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Slots       []Slot `json:"slots"`
	// Custom marks the free-form grid, whose slots come from the scene itself.
	Custom bool `json:"custom"`
}

// Layout identifiers.
const (
	LayoutFullscreen      = "fullscreen"
	LayoutCols5050        = "cols_50_50"
	LayoutCols6040        = "cols_60_40"
	LayoutCols4060        = "cols_40_60"
	LayoutRows5050        = "rows_50_50"
	LayoutMainRightSide   = "main_right_sidebar"
	LayoutMainLeftSide    = "main_left_sidebar"
	LayoutMainBottomTick  = "main_bottom_ticker"
	LayoutMainTopBanner   = "main_top_banner"
	LayoutQuad            = "quad"
	LayoutLeftTwoRight    = "left_two_right"
	LayoutTopTwoBottom    = "top_two_bottom"
	LayoutDashboardSocial = "dashboard_social"
	LayoutImageCountdown  = "image_countdown"
	LayoutWebsiteKPI      = "website_kpi"
	LayoutCustomGrid      = "custom_grid"
)

// MaxCustomZones caps a custom grid. Every additional zone is another renderer,
// another timer and possibly another network fetch on a 4-core Jetson; beyond
// this the display becomes unreadable long before it becomes slow.
const MaxCustomZones = 8

// Ticker and banner bands are sized so text stays legible at typical viewing
// distance on a 1080p screen: 12% of 1080 is ~130px, comfortable for a ticker.
const (
	tickerBand = 12.0
	bannerBand = 18.0
	sidebarPct = 30.0
)

var layouts = []Layout{
	{
		ID: LayoutFullscreen, Name: "Fullscreen",
		Description: "One zone filling the entire screen.",
		Slots: []Slot{
			{ID: "a", Label: "Full screen", Rect: Rect{0, 0, 100, 100}},
		},
	},
	{
		ID: LayoutCols5050, Name: "Two columns (50/50)",
		Description: "Two equal columns side by side.",
		Slots: []Slot{
			{ID: "a", Label: "Left", Rect: Rect{0, 0, 50, 100}},
			{ID: "b", Label: "Right", Rect: Rect{50, 0, 50, 100}},
		},
	},
	{
		ID: LayoutCols6040, Name: "Two columns (60/40)",
		Description: "A wider left column with a narrower right column.",
		Slots: []Slot{
			{ID: "a", Label: "Left (wide)", Rect: Rect{0, 0, 60, 100}},
			{ID: "b", Label: "Right", Rect: Rect{60, 0, 40, 100}},
		},
	},
	{
		ID: LayoutCols4060, Name: "Two columns (40/60)",
		Description: "A narrower left column with a wider right column.",
		Slots: []Slot{
			{ID: "a", Label: "Left", Rect: Rect{0, 0, 40, 100}},
			{ID: "b", Label: "Right (wide)", Rect: Rect{40, 0, 60, 100}},
		},
	},
	{
		ID: LayoutRows5050, Name: "Two rows (50/50)",
		Description: "Two equal rows stacked vertically.",
		Slots: []Slot{
			{ID: "a", Label: "Top", Rect: Rect{0, 0, 100, 50}},
			{ID: "b", Label: "Bottom", Rect: Rect{0, 50, 100, 50}},
		},
	},
	{
		ID: LayoutMainRightSide, Name: "Main with right sidebar",
		Description: "Primary content with a supporting column on the right.",
		Slots: []Slot{
			{ID: "a", Label: "Main", Rect: Rect{0, 0, 100 - sidebarPct, 100}},
			{ID: "b", Label: "Sidebar", Rect: Rect{100 - sidebarPct, 0, sidebarPct, 100},
				Hint: []string{TypeSocial, TypeKPI, TypeCountdown, TypeClock}},
		},
	},
	{
		ID: LayoutMainLeftSide, Name: "Main with left sidebar",
		Description: "Primary content with a supporting column on the left.",
		Slots: []Slot{
			{ID: "a", Label: "Sidebar", Rect: Rect{0, 0, sidebarPct, 100},
				Hint: []string{TypeSocial, TypeKPI, TypeCountdown, TypeClock}},
			{ID: "b", Label: "Main", Rect: Rect{sidebarPct, 0, 100 - sidebarPct, 100}},
		},
	},
	{
		ID: LayoutMainBottomTick, Name: "Main with bottom ticker",
		Description: "Primary content with a scrolling ticker band along the bottom.",
		Slots: []Slot{
			{ID: "a", Label: "Main", Rect: Rect{0, 0, 100, 100 - tickerBand}},
			{ID: "b", Label: "Ticker", Rect: Rect{0, 100 - tickerBand, 100, tickerBand},
				Hint: []string{TypeTicker, TypeText}},
		},
	},
	{
		ID: LayoutMainTopBanner, Name: "Main with top banner",
		Description: "A header band above the primary content.",
		Slots: []Slot{
			{ID: "a", Label: "Banner", Rect: Rect{0, 0, 100, bannerBand},
				Hint: []string{TypeText, TypeAnnouncement, TypeClock}},
			{ID: "b", Label: "Main", Rect: Rect{0, bannerBand, 100, 100 - bannerBand}},
		},
	},
	{
		ID: LayoutQuad, Name: "Four quadrants",
		Description: "Four equal zones in a 2x2 grid.",
		Slots: []Slot{
			{ID: "a", Label: "Top left", Rect: Rect{0, 0, 50, 50}},
			{ID: "b", Label: "Top right", Rect: Rect{50, 0, 50, 50}},
			{ID: "c", Label: "Bottom left", Rect: Rect{0, 50, 50, 50}},
			{ID: "d", Label: "Bottom right", Rect: Rect{50, 50, 50, 50}},
		},
	},
	{
		ID: LayoutLeftTwoRight, Name: "Large left, two stacked right",
		Description: "A large left zone beside two stacked right zones.",
		Slots: []Slot{
			{ID: "a", Label: "Main (left)", Rect: Rect{0, 0, 60, 100}},
			{ID: "b", Label: "Top right", Rect: Rect{60, 0, 40, 50}},
			{ID: "c", Label: "Bottom right", Rect: Rect{60, 50, 40, 50}},
		},
	},
	{
		ID: LayoutTopTwoBottom, Name: "Large top, two below",
		Description: "A large top zone above two side-by-side zones.",
		Slots: []Slot{
			{ID: "a", Label: "Main (top)", Rect: Rect{0, 0, 100, 60}},
			{ID: "b", Label: "Bottom left", Rect: Rect{0, 60, 50, 40}},
			{ID: "c", Label: "Bottom right", Rect: Rect{50, 60, 50, 40}},
		},
	},
	{
		ID: LayoutDashboardSocial, Name: "Dashboard + social feed",
		Description: "A live dashboard with a social feed column beside it.",
		Slots: []Slot{
			{ID: "a", Label: "Dashboard", Rect: Rect{0, 0, 68, 100},
				Hint: []string{TypeWebsite, TypeKPI}},
			{ID: "b", Label: "Social feed", Rect: Rect{68, 0, 32, 100},
				Hint: []string{TypeSocial}},
		},
	},
	{
		ID: LayoutImageCountdown, Name: "Image + countdown",
		Description: "A hero image with a countdown panel alongside.",
		Slots: []Slot{
			{ID: "a", Label: "Image", Rect: Rect{0, 0, 62, 100},
				Hint: []string{TypeImage, TypeVideo}},
			{ID: "b", Label: "Countdown", Rect: Rect{62, 0, 38, 100},
				Hint: []string{TypeCountdown, TypeEvent}},
		},
	},
	{
		ID: LayoutWebsiteKPI, Name: "Website + KPI panel",
		Description: "An embedded site with a KPI strip below it.",
		Slots: []Slot{
			{ID: "a", Label: "Website", Rect: Rect{0, 0, 100, 72},
				Hint: []string{TypeWebsite}},
			{ID: "b", Label: "KPI panel", Rect: Rect{0, 72, 100, 28},
				Hint: []string{TypeKPI}},
		},
	},
	{
		ID: LayoutCustomGrid, Name: "Custom grid", Custom: true,
		Description: fmt.Sprintf("Freely positioned zones (up to %d).", MaxCustomZones),
		Slots:       nil,
	},
}

var layoutByID = func() map[string]Layout {
	m := make(map[string]Layout, len(layouts))
	for _, l := range layouts {
		m[l.ID] = l
	}
	return m
}()

// Layouts returns every available layout in presentation order.
func Layouts() []Layout {
	out := make([]Layout, len(layouts))
	copy(out, layouts)
	return out
}

// LookupLayout returns a layout by ID.
func LookupLayout(id string) (Layout, bool) {
	l, ok := layoutByID[id]
	return l, ok
}

// SlotsFor returns the slots a layout defines. For the custom grid the caller
// supplies its own zones, so this returns nil.
func SlotsFor(layoutID string) ([]Slot, error) {
	l, ok := layoutByID[layoutID]
	if !ok {
		return nil, fmt.Errorf("unknown layout %q", layoutID)
	}
	out := make([]Slot, len(l.Slots))
	copy(out, l.Slots)
	return out, nil
}

// ValidateGeometry checks that zone rectangles are inside the screen and do not
// overlap. Overlap is rejected rather than z-ordered: on a signage screen an
// accidental overlap silently hides content, and the operator has no way to
// notice from the admin UI that something is being covered.
func ValidateGeometry(rects map[string]Rect) error {
	type named struct {
		id string
		r  Rect
	}
	var list []named
	for id, r := range rects {
		if r.W <= 0 || r.H <= 0 {
			return fmt.Errorf("zone %q has zero or negative size", id)
		}
		if r.X < -0.01 || r.Y < -0.01 || r.X+r.W > 100.01 || r.Y+r.H > 100.01 {
			return fmt.Errorf("zone %q extends outside the screen (x=%.1f y=%.1f w=%.1f h=%.1f)",
				id, r.X, r.Y, r.W, r.H)
		}
		list = append(list, named{id, r})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].id < list[j].id })

	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if overlaps(list[i].r, list[j].r) {
				return fmt.Errorf("zones %q and %q overlap", list[i].id, list[j].id)
			}
		}
	}
	return nil
}

// overlaps reports whether two rectangles intersect by more than a rounding
// epsilon. The tolerance keeps adjacent zones sharing an edge (50 + 50) from
// being reported as overlapping due to float representation.
func overlaps(a, b Rect) bool {
	const eps = 0.05
	return a.X+eps < b.X+b.W &&
		b.X+eps < a.X+a.W &&
		a.Y+eps < b.Y+b.H &&
		b.Y+eps < a.Y+a.H
}

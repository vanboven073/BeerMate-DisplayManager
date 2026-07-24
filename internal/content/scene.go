package content

import (
	"fmt"
	"time"
)

// Zone is one region of a scene.
type Zone struct {
	ID          int64  `json:"id"`
	SceneID     int64  `json:"scene_id"`
	Slot        string `json:"slot"`
	Rect        Rect   `json:"rect"`
	Z           int    `json:"z"`
	ContentType string `json:"content_type"`
	ContentRef  string `json:"content_ref"`
	Config      string `json:"config"`
	Style       string `json:"style"`
	Label       string `json:"label"`
}

// ZoneStyle is the presentational wrapper around a zone's content.
type ZoneStyle struct {
	Background   string  `json:"background,omitempty"`
	Padding      float64 `json:"padding,omitempty"`
	BorderWidth  float64 `json:"border_width,omitempty"`
	BorderColor  string  `json:"border_color,omitempty"`
	BorderRadius float64 `json:"border_radius,omitempty"`
	Overflow     string  `json:"overflow,omitempty"` // hidden | visible | scroll
	Fit          string  `json:"fit,omitempty"`
	ShowLabel    bool    `json:"show_label,omitempty"`
}

// Scene is one full-screen composition in the playlist.
type Scene struct {
	ID            int64      `json:"id"`
	RevisionID    int64      `json:"revision_id"`
	StableID      string     `json:"stable_id"`
	Name          string     `json:"name"`
	Position      int        `json:"position"`
	Enabled       bool       `json:"enabled"`
	Layout        string     `json:"layout"`
	LayoutJSON    string     `json:"layout_json"`
	DurationMS    int        `json:"duration_ms"`
	Background    string     `json:"background"`
	Transition    string     `json:"transition"`
	ActiveFrom    *time.Time `json:"active_from,omitempty"`
	ActiveUntil   *time.Time `json:"active_until,omitempty"`
	DaysMask      int        `json:"days_mask"`
	Valid         bool       `json:"valid"`
	ValidationMsg string     `json:"validation_message"`
	LastPlayedAt  *time.Time `json:"last_played_at,omitempty"`
	LastError     string     `json:"last_error"`
	CreatedAt     time.Time  `json:"created_at"`
	CreatedBy     string     `json:"created_by"`
	UpdatedAt     time.Time  `json:"updated_at"`
	UpdatedBy     string     `json:"updated_by"`
	Zones         []Zone     `json:"zones"`
}

// Transitions supported by the player.
var Transitions = []string{"none", "fade", "slide_left", "slide_up", "dissolve"}

// MaxSceneNameLen bounds the internal scene name.
const MaxSceneNameLen = 120

// ValidateScene checks a scene and all its zones, returning the references it
// depends on so the publish path can verify they still exist.
func ValidateScene(s *Scene) (Refs, error) {
	var errs ValidationErrors
	var refs Refs

	if err := checkRequired("name", s.Name, MaxSceneNameLen); err != nil {
		errs = append(errs, err...)
	}
	if err := ValidateDuration(s.DurationMS); err != nil {
		errs = append(errs, ValidationError{"duration_ms", err.Error()})
	}
	if err := ValidateDaysMask(s.DaysMask); err != nil {
		errs = append(errs, ValidationError{"days_mask", err.Error()})
	}
	errs = append(errs, checkEnum("transition", s.Transition, true, Transitions...)...)
	errs = append(errs, checkColor("background", s.Background)...)

	if s.ActiveFrom != nil && s.ActiveUntil != nil && !s.ActiveUntil.After(*s.ActiveFrom) {
		errs = append(errs, ValidationError{"active_until", "must be after active_from"})
	}

	layout, ok := LookupLayout(s.Layout)
	if !ok {
		errs = append(errs, ValidationError{"layout", fmt.Sprintf("unknown layout %q", s.Layout)})
		return refs, errs.OrNil()
	}

	if len(s.Zones) == 0 {
		errs = append(errs, ValidationError{"zones", "a scene must have at least one zone"})
		return refs, errs.OrNil()
	}

	// Slot geometry comes from the layout for fixed layouts, and from the zone
	// itself only for the custom grid. Trusting client-supplied geometry on a
	// fixed layout would let a saved scene drift away from its template.
	rects := make(map[string]Rect, len(s.Zones))
	seen := make(map[string]bool, len(s.Zones))

	if layout.Custom {
		if len(s.Zones) > MaxCustomZones {
			errs = append(errs, ValidationError{"zones",
				fmt.Sprintf("a custom grid supports at most %d zones", MaxCustomZones)})
		}
		for i := range s.Zones {
			z := &s.Zones[i]
			if z.Slot == "" {
				errs = append(errs, ValidationError{fmt.Sprintf("zones[%d].slot", i), "is required"})
				continue
			}
			if seen[z.Slot] {
				errs = append(errs, ValidationError{fmt.Sprintf("zones[%d].slot", i),
					fmt.Sprintf("duplicate slot %q", z.Slot)})
				continue
			}
			seen[z.Slot] = true
			rects[z.Slot] = z.Rect
		}
	} else {
		allowed := make(map[string]Slot, len(layout.Slots))
		for _, sl := range layout.Slots {
			allowed[sl.ID] = sl
		}
		for i := range s.Zones {
			z := &s.Zones[i]
			sl, ok := allowed[z.Slot]
			if !ok {
				errs = append(errs, ValidationError{fmt.Sprintf("zones[%d].slot", i),
					fmt.Sprintf("layout %q has no slot %q", layout.ID, z.Slot)})
				continue
			}
			if seen[z.Slot] {
				errs = append(errs, ValidationError{fmt.Sprintf("zones[%d].slot", i),
					fmt.Sprintf("duplicate slot %q", z.Slot)})
				continue
			}
			seen[z.Slot] = true
			z.Rect = sl.Rect // authoritative: the layout defines the geometry
			rects[z.Slot] = sl.Rect
		}
		for _, sl := range layout.Slots {
			if !seen[sl.ID] {
				errs = append(errs, ValidationError{"zones",
					fmt.Sprintf("layout %q requires a zone for slot %q (%s)", layout.ID, sl.ID, sl.Label)})
			}
		}
	}

	if len(rects) > 0 {
		if err := ValidateGeometry(rects); err != nil {
			errs = append(errs, ValidationError{"zones", err.Error()})
		}
	}

	for i := range s.Zones {
		z := &s.Zones[i]
		zoneRefs, err := ValidateZone(z.ContentType, z.ContentRef, z.Config)
		if err != nil {
			var ve ValidationErrors
			if ok := asValidationErrors(err, &ve); ok {
				for _, e := range ve {
					errs = append(errs, ValidationError{
						Field:   fmt.Sprintf("zones[%s].%s", z.Slot, e.Field),
						Message: e.Message,
					})
				}
			} else {
				errs = append(errs, ValidationError{
					Field: fmt.Sprintf("zones[%s]", z.Slot), Message: err.Error()})
			}
		}
		refs.MediaIDs = append(refs.MediaIDs, zoneRefs.MediaIDs...)
		refs.WebsiteIDs = append(refs.WebsiteIDs, zoneRefs.WebsiteIDs...)
		refs.FeedIDs = append(refs.FeedIDs, zoneRefs.FeedIDs...)
		refs.CredIDs = append(refs.CredIDs, zoneRefs.CredIDs...)

		if z.Style != "" {
			var st ZoneStyle
			if err := decode(z.Style, &st); err != nil {
				errs = append(errs, ValidationError{
					Field: fmt.Sprintf("zones[%s].style", z.Slot), Message: err.Error()})
			} else {
				errs = append(errs, prefixed(fmt.Sprintf("zones[%s].style", z.Slot),
					validateZoneStyle(st))...)
			}
		}
		errs = append(errs, checkLen(fmt.Sprintf("zones[%s].label", z.Slot), z.Label, MaxLabelLen)...)
	}

	// A scene where nothing is configured renders as a blank screen, which looks
	// identical to a crash from across the room. Require at least one real zone.
	allEmpty := true
	for _, z := range s.Zones {
		if z.ContentType != TypeEmpty {
			allEmpty = false
			break
		}
	}
	if allEmpty {
		errs = append(errs, ValidationError{"zones", "at least one zone must have content"})
	}

	return refs, errs.OrNil()
}

func validateZoneStyle(st ZoneStyle) ValidationErrors {
	var errs ValidationErrors
	errs = append(errs, checkColor("background", st.Background)...)
	errs = append(errs, checkColor("border_color", st.BorderColor)...)
	errs = append(errs, checkEnum("overflow", st.Overflow, true, "hidden", "visible", "scroll")...)
	errs = append(errs, checkEnum("fit", st.Fit, true, "contain", "cover", "stretch")...)
	if st.Padding < 0 || st.Padding > 20 {
		errs = append(errs, ValidationError{"padding", "must be between 0 and 20 (percent)"})
	}
	if st.BorderWidth < 0 || st.BorderWidth > 40 {
		errs = append(errs, ValidationError{"border_width", "must be between 0 and 40 px"})
	}
	if st.BorderRadius < 0 || st.BorderRadius > 200 {
		errs = append(errs, ValidationError{"border_radius", "must be between 0 and 200 px"})
	}
	return errs
}

func prefixed(prefix string, errs ValidationErrors) ValidationErrors {
	if len(errs) == 0 {
		return nil
	}
	out := make(ValidationErrors, len(errs))
	for i, e := range errs {
		out[i] = ValidationError{Field: prefix + "." + e.Field, Message: e.Message}
	}
	return out
}

func asValidationErrors(err error, dst *ValidationErrors) bool {
	if ve, ok := err.(ValidationErrors); ok {
		*dst = ve
		return true
	}
	return false
}

// ActiveAt reports whether a scene should play at the given local time.
//
// This is the scene-level window; the global display schedule is applied
// separately by the scheduler.
func (s *Scene) ActiveAt(now time.Time) bool {
	if !s.Enabled || !s.Valid {
		return false
	}
	if s.ActiveFrom != nil && now.Before(*s.ActiveFrom) {
		return false
	}
	if s.ActiveUntil != nil && !now.Before(*s.ActiveUntil) {
		return false
	}
	return DayEnabled(s.DaysMask, now.Weekday())
}

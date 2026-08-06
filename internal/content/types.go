package content

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Zone content types.
const (
	TypeEmpty        = "empty"
	TypeImage        = "image"
	TypeVideo        = "video"
	TypeWebsite      = "website"
	TypeCountdown    = "countdown"
	TypeClock        = "clock"
	TypeKPI          = "kpi"
	TypeQR           = "qr"
	TypeAnnouncement = "announcement"
	TypeImageText    = "image_text"
	TypeSocial       = "social"
	TypeText         = "text"
	TypeTicker       = "ticker"
	TypeEvent        = "event"
	TypeFallback     = "fallback"
)

// AllTypes lists every zone content type.
func AllTypes() []string {
	return []string{
		TypeEmpty, TypeImage, TypeVideo, TypeWebsite, TypeCountdown, TypeClock,
		TypeKPI, TypeQR, TypeAnnouncement, TypeImageText, TypeSocial, TypeText,
		TypeTicker, TypeEvent, TypeFallback,
	}
}

var validType = func() map[string]bool {
	m := map[string]bool{}
	for _, t := range AllTypes() {
		m[t] = true
	}
	return m
}()

// ValidType reports whether t is a known content type.
func ValidType(t string) bool { return validType[t] }

// Field length caps. These bound both storage and render cost: an operator
// pasting a novel into a heading should get a clear error, not an unreadable
// screen or a slow player.
const (
	MaxHeadingLen  = 120
	MaxSubtitleLen = 200
	MaxBodyLen     = 2000
	MaxLabelLen    = 80
	MaxURLLen      = 2048
	MaxQRDataLen   = 1200 // beyond this a QR code stops being scannable on screen
	MaxTickerItems = 20
	MaxKPICards    = 6
)

// Duration bounds for a scene.
const (
	MinDurationMS = 3000
	MaxDurationMS = 24 * 60 * 60 * 1000
)

// ---- per-type configuration -------------------------------------------------

// ImageConfig configures an image zone.
type ImageConfig struct {
	Fit        string `json:"fit"` // contain | cover | stretch
	Background string `json:"background,omitempty"`
	Title      string `json:"title,omitempty"`
	Subtitle   string `json:"subtitle,omitempty"`
	Overlay    string `json:"overlay,omitempty"`
	OverlayPos string `json:"overlay_position,omitempty"` // top | center | bottom
}

// VideoConfig configures a video zone.
type VideoConfig struct {
	Mode     string `json:"mode"` // once | loop | until_complete | duration
	Muted    bool   `json:"muted"`
	PosterID int64  `json:"poster_id,omitempty"`
	Fit      string `json:"fit"`
}

// WebsiteConfig configures a website zone. The heavy settings (render mode,
// credentials, capture interval) live on the website record itself; this is only
// the per-placement override.
type WebsiteConfig struct {
	Zoom          float64 `json:"zoom,omitempty"`
	RefreshOnShow bool    `json:"refresh_on_show"`
}

// CountdownConfig configures a countdown zone.
type CountdownConfig struct {
	Title             string `json:"title"`
	Subtitle          string `json:"subtitle,omitempty"`
	TargetRFC3339     string `json:"target"`
	Timezone          string `json:"timezone"`
	CompletionMessage string `json:"completion_message,omitempty"`
	CompletionImageID int64  `json:"completion_image_id,omitempty"`
	HideZeroUnits     bool   `json:"hide_zero_units"`
	ExpireAfterDone   bool   `json:"expire_after_done"`
	Variant           string `json:"variant,omitempty"`
}

// ClockConfig configures a clock zone.
type ClockConfig struct {
	Timezone     string `json:"timezone"`
	ShowSeconds  bool   `json:"show_seconds"`
	ShowDate     bool   `json:"show_date"`
	Title        string `json:"title,omitempty"`
	Subtitle     string `json:"subtitle,omitempty"`
	Variant      string `json:"variant,omitempty"`
	TwentyFourHr bool   `json:"twenty_four_hour"`
}

// KPICard is one metric tile.
type KPICard struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Unit        string `json:"unit,omitempty"`
	Target      string `json:"target,omitempty"`
	Trend       string `json:"trend,omitempty"`  // up | down | flat
	Status      string `json:"status,omitempty"` // good | warn | bad | neutral
	Icon        string `json:"icon,omitempty"`
	Description string `json:"description,omitempty"`
	// SourcePath is a dotted JSON path used when the card is API-backed.
	SourcePath string `json:"source_path,omitempty"`
}

// KPIConfig configures a KPI zone.
type KPIConfig struct {
	Title  string    `json:"title,omitempty"`
	Cards  []KPICard `json:"cards"`
	Source string    `json:"source"` // manual | api
	// API-backed settings.
	URL           string `json:"url,omitempty"`
	CredentialID  int64  `json:"credential_id,omitempty"`
	RefreshSec    int    `json:"refresh_sec,omitempty"`
	TimeoutSec    int    `json:"timeout_sec,omitempty"`
	FallbackToast string `json:"fallback_message,omitempty"`
}

// QRConfig configures a QR-code zone.
type QRConfig struct {
	Heading     string `json:"heading,omitempty"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind"` // url | text | contact | wifi
	Data        string `json:"data"`
	ECLevel     string `json:"ec_level"` // L | M | Q | H
	WithLogo    bool   `json:"with_logo"`
	Foreground  string `json:"foreground,omitempty"`
	Background  string `json:"background,omitempty"`
}

// AnnouncementConfig configures a generated announcement zone.
type AnnouncementConfig struct {
	Heading    string `json:"heading"`
	Body       string `json:"body,omitempty"`
	ImageID    int64  `json:"image_id,omitempty"`
	ShowLogo   bool   `json:"show_logo"`
	Background string `json:"background,omitempty"`
	Align      string `json:"align,omitempty"` // left | center | right
	TextSize   string `json:"text_size,omitempty"`
	TextColor  string `json:"text_color,omitempty"`
	CTA        string `json:"cta,omitempty"`
	QRData     string `json:"qr_data,omitempty"`
}

// ImageTextConfig configures an image-and-text template zone.
type ImageTextConfig struct {
	Template   string `json:"template"`
	Heading    string `json:"heading,omitempty"`
	Subtitle   string `json:"subtitle,omitempty"`
	Body       string `json:"body,omitempty"`
	ImageID    int64  `json:"image_id,omitempty"`
	ShowLogo   bool   `json:"show_logo"`
	CTA        string `json:"cta,omitempty"`
	QRData     string `json:"qr_data,omitempty"`
	Background string `json:"background,omitempty"`
	Fit        string `json:"fit,omitempty"`
}

// ImageTextTemplates are the supported arrangements.
var ImageTextTemplates = []string{
	"image_left", "image_right", "full_background", "centered_caption",
	"header_image_footer", "logo_title_body",
}

// SocialConfig configures a social feed zone.
type SocialConfig struct {
	FeedID   int64  `json:"feed_id"`
	Template string `json:"template"` // single | cards | vertical | ticker | grid | wall | sidebar | fullscreen | latest
	MaxItems int    `json:"max_items,omitempty"`
	ShowMeta bool   `json:"show_meta"`
}

// SocialTemplates are the supported display templates.
var SocialTemplates = []string{
	"single", "cards", "vertical", "ticker", "grid", "wall", "sidebar",
	"fullscreen", "latest",
}

// TextConfig configures a plain text zone.
type TextConfig struct {
	Heading    string `json:"heading,omitempty"`
	Body       string `json:"body,omitempty"`
	Align      string `json:"align,omitempty"`
	TextSize   string `json:"text_size,omitempty"`
	TextColor  string `json:"text_color,omitempty"`
	Background string `json:"background,omitempty"`
}

// TickerConfig configures a ticker zone.
type TickerConfig struct {
	Source          string   `json:"source"` // manual | feed | emergency
	Messages        []string `json:"messages,omitempty"`
	FeedID          int64    `json:"feed_id,omitempty"`
	Direction       string   `json:"direction,omitempty"` // left | right
	SpeedPxSec      int      `json:"speed_px_sec,omitempty"`
	Separator       string   `json:"separator,omitempty"`
	TextSize        string   `json:"text_size,omitempty"`
	TextColor       string   `json:"text_color,omitempty"`
	Background      string   `json:"background,omitempty"`
	PauseOnPriority bool     `json:"pause_on_priority"`
}

// EventConfig configures an event zone.
type EventConfig struct {
	Title          string `json:"title"`
	Subtitle       string `json:"subtitle,omitempty"`
	StartsRFC3339  string `json:"starts_at,omitempty"`
	Location       string `json:"location,omitempty"`
	ImageID        int64  `json:"image_id,omitempty"`
	ShowLogo       bool   `json:"show_logo"`
	QRData         string `json:"qr_data,omitempty"`
	EmbedCountdown bool   `json:"embed_countdown"`
	Variant        string `json:"variant,omitempty"`
}

// ---- validation --------------------------------------------------------------

// ValidationError describes why a zone or scene is invalid.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

// ValidationErrors is a collection of field errors.
type ValidationErrors []ValidationError

func (v ValidationErrors) Error() string {
	if len(v) == 0 {
		return "no validation errors"
	}
	parts := make([]string, len(v))
	for i, e := range v {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

// OrNil returns nil when empty, so callers can `return errs.OrNil()`.
func (v ValidationErrors) OrNil() error {
	if len(v) == 0 {
		return nil
	}
	return v
}

// Refs are the identifiers a zone points at, returned so the caller can check
// they still exist before publishing.
type Refs struct {
	MediaIDs   []int64
	WebsiteIDs []int64
	FeedIDs    []int64
	CredIDs    []int64
}

// ValidateZone checks a zone's content type, reference and configuration.
//
// It returns the references the zone depends on so the publish path can verify
// they exist. Referential integrity cannot be expressed in the schema here
// because content_ref is deliberately untyped.
func ValidateZone(contentType, contentRef, configJSON string) (Refs, error) {
	var errs ValidationErrors
	var refs Refs

	if !ValidType(contentType) {
		return refs, ValidationErrors{{Field: "content_type",
			Message: fmt.Sprintf("unknown content type %q", contentType)}}
	}
	if contentType == TypeEmpty {
		return refs, nil
	}

	cfg := strings.TrimSpace(configJSON)
	if cfg == "" {
		cfg = "{}"
	}
	if !json.Valid([]byte(cfg)) {
		return refs, ValidationErrors{{Field: "config", Message: "configuration is not valid JSON"}}
	}

	switch contentType {
	case TypeImage:
		id, err := refID(contentRef, "image")
		if err != nil {
			errs = append(errs, ValidationError{"content_ref", err.Error()})
		} else {
			refs.MediaIDs = append(refs.MediaIDs, id)
		}
		var c ImageConfig
		if err := decode(cfg, &c); err != nil {
			errs = append(errs, ValidationError{"config", err.Error()})
			break
		}
		errs = append(errs, checkEnum("fit", c.Fit, true, "contain", "cover", "stretch")...)
		errs = append(errs, checkLen("title", c.Title, MaxHeadingLen)...)
		errs = append(errs, checkLen("subtitle", c.Subtitle, MaxSubtitleLen)...)
		errs = append(errs, checkLen("overlay", c.Overlay, MaxBodyLen)...)
		errs = append(errs, checkColor("background", c.Background)...)

	case TypeVideo:
		id, err := refID(contentRef, "video")
		if err != nil {
			errs = append(errs, ValidationError{"content_ref", err.Error()})
		} else {
			refs.MediaIDs = append(refs.MediaIDs, id)
		}
		var c VideoConfig
		if err := decode(cfg, &c); err != nil {
			errs = append(errs, ValidationError{"config", err.Error()})
			break
		}
		errs = append(errs, checkEnum("mode", c.Mode, true, "once", "loop", "until_complete", "duration")...)
		errs = append(errs, checkEnum("fit", c.Fit, true, "contain", "cover", "stretch")...)
		if c.PosterID > 0 {
			refs.MediaIDs = append(refs.MediaIDs, c.PosterID)
		}

	case TypeWebsite:
		id, err := refID(contentRef, "website")
		if err != nil {
			errs = append(errs, ValidationError{"content_ref", err.Error()})
		} else {
			refs.WebsiteIDs = append(refs.WebsiteIDs, id)
		}
		var c WebsiteConfig
		if err := decode(cfg, &c); err != nil {
			errs = append(errs, ValidationError{"config", err.Error()})
			break
		}
		if c.Zoom != 0 && (c.Zoom < 0.25 || c.Zoom > 4) {
			errs = append(errs, ValidationError{"zoom", "must be between 0.25 and 4"})
		}

	case TypeCountdown:
		var c CountdownConfig
		if err := decode(cfg, &c); err != nil {
			errs = append(errs, ValidationError{"config", err.Error()})
			break
		}
		errs = append(errs, checkRequired("title", c.Title, MaxHeadingLen)...)
		errs = append(errs, checkLen("subtitle", c.Subtitle, MaxSubtitleLen)...)
		errs = append(errs, checkLen("completion_message", c.CompletionMessage, MaxBodyLen)...)
		if _, err := time.Parse(time.RFC3339, c.TargetRFC3339); err != nil {
			errs = append(errs, ValidationError{"target", "must be an RFC3339 timestamp"})
		}
		errs = append(errs, checkTimezone("timezone", c.Timezone)...)
		if c.CompletionImageID > 0 {
			refs.MediaIDs = append(refs.MediaIDs, c.CompletionImageID)
		}

	case TypeClock:
		var c ClockConfig
		if err := decode(cfg, &c); err != nil {
			errs = append(errs, ValidationError{"config", err.Error()})
			break
		}
		errs = append(errs, checkTimezone("timezone", c.Timezone)...)
		errs = append(errs, checkLen("title", c.Title, MaxHeadingLen)...)
		errs = append(errs, checkLen("subtitle", c.Subtitle, MaxSubtitleLen)...)

	case TypeKPI:
		var c KPIConfig
		if err := decode(cfg, &c); err != nil {
			errs = append(errs, ValidationError{"config", err.Error()})
			break
		}
		errs = append(errs, checkEnum("source", c.Source, false, "manual", "api")...)
		if len(c.Cards) == 0 {
			errs = append(errs, ValidationError{"cards", "at least one KPI card is required"})
		}
		if len(c.Cards) > MaxKPICards {
			errs = append(errs, ValidationError{"cards",
				fmt.Sprintf("at most %d KPI cards are supported", MaxKPICards)})
		}
		for i, card := range c.Cards {
			p := fmt.Sprintf("cards[%d]", i)
			errs = append(errs, checkRequired(p+".label", card.Label, MaxLabelLen)...)
			errs = append(errs, checkLen(p+".unit", card.Unit, 24)...)
			errs = append(errs, checkLen(p+".description", card.Description, MaxSubtitleLen)...)
			errs = append(errs, checkEnum(p+".trend", card.Trend, true, "up", "down", "flat")...)
			errs = append(errs, checkEnum(p+".status", card.Status, true, "good", "warn", "bad", "neutral")...)
		}
		if c.Source == "api" {
			if err := ValidateHTTPURL(c.URL, false); err != nil {
				errs = append(errs, ValidationError{"url", err.Error()})
			}
			if c.RefreshSec != 0 && (c.RefreshSec < 30 || c.RefreshSec > 86400) {
				errs = append(errs, ValidationError{"refresh_sec",
					"must be between 30 and 86400 seconds to respect upstream rate limits"})
			}
			if c.CredentialID > 0 {
				refs.CredIDs = append(refs.CredIDs, c.CredentialID)
			}
		}

	case TypeQR:
		var c QRConfig
		if err := decode(cfg, &c); err != nil {
			errs = append(errs, ValidationError{"config", err.Error()})
			break
		}
		errs = append(errs, checkEnum("kind", c.Kind, false, "url", "text", "contact", "wifi")...)
		errs = append(errs, checkEnum("ec_level", c.ECLevel, true, "L", "M", "Q", "H")...)
		errs = append(errs, checkLen("heading", c.Heading, MaxHeadingLen)...)
		errs = append(errs, checkLen("description", c.Description, MaxSubtitleLen)...)
		if strings.TrimSpace(c.Data) == "" {
			errs = append(errs, ValidationError{"data", "QR content is required"})
		} else if utf8.RuneCountInString(c.Data) > MaxQRDataLen {
			errs = append(errs, ValidationError{"data",
				fmt.Sprintf("at most %d characters; longer codes become unscannable on screen", MaxQRDataLen)})
		}
		if c.Kind == "url" && strings.TrimSpace(c.Data) != "" {
			if err := ValidateHTTPURL(c.Data, true); err != nil {
				errs = append(errs, ValidationError{"data", err.Error()})
			}
		}
		errs = append(errs, checkColor("foreground", c.Foreground)...)
		errs = append(errs, checkColor("background", c.Background)...)

	case TypeAnnouncement:
		var c AnnouncementConfig
		if err := decode(cfg, &c); err != nil {
			errs = append(errs, ValidationError{"config", err.Error()})
			break
		}
		errs = append(errs, checkRequired("heading", c.Heading, MaxHeadingLen)...)
		errs = append(errs, checkLen("body", c.Body, MaxBodyLen)...)
		errs = append(errs, checkLen("cta", c.CTA, MaxLabelLen)...)
		errs = append(errs, checkEnum("align", c.Align, true, "left", "center", "right")...)
		errs = append(errs, checkEnum("text_size", c.TextSize, true, "s", "m", "l", "xl")...)
		errs = append(errs, checkColor("text_color", c.TextColor)...)
		errs = append(errs, checkColor("background", c.Background)...)
		if c.ImageID > 0 {
			refs.MediaIDs = append(refs.MediaIDs, c.ImageID)
		}

	case TypeImageText:
		var c ImageTextConfig
		if err := decode(cfg, &c); err != nil {
			errs = append(errs, ValidationError{"config", err.Error()})
			break
		}
		errs = append(errs, checkEnum("template", c.Template, false, ImageTextTemplates...)...)
		errs = append(errs, checkLen("heading", c.Heading, MaxHeadingLen)...)
		errs = append(errs, checkLen("subtitle", c.Subtitle, MaxSubtitleLen)...)
		errs = append(errs, checkLen("body", c.Body, MaxBodyLen)...)
		errs = append(errs, checkLen("cta", c.CTA, MaxLabelLen)...)
		if c.ImageID > 0 {
			refs.MediaIDs = append(refs.MediaIDs, c.ImageID)
		}

	case TypeSocial:
		var c SocialConfig
		if err := decode(cfg, &c); err != nil {
			errs = append(errs, ValidationError{"config", err.Error()})
			break
		}
		if c.FeedID <= 0 {
			errs = append(errs, ValidationError{"feed_id", "a social feed must be selected"})
		} else {
			refs.FeedIDs = append(refs.FeedIDs, c.FeedID)
		}
		errs = append(errs, checkEnum("template", c.Template, false, SocialTemplates...)...)
		if c.MaxItems < 0 || c.MaxItems > 50 {
			errs = append(errs, ValidationError{"max_items", "must be between 0 and 50"})
		}

	case TypeText:
		var c TextConfig
		if err := decode(cfg, &c); err != nil {
			errs = append(errs, ValidationError{"config", err.Error()})
			break
		}
		if strings.TrimSpace(c.Heading) == "" && strings.TrimSpace(c.Body) == "" {
			errs = append(errs, ValidationError{"body", "a heading or body is required"})
		}
		errs = append(errs, checkLen("heading", c.Heading, MaxHeadingLen)...)
		errs = append(errs, checkLen("body", c.Body, MaxBodyLen)...)
		errs = append(errs, checkEnum("align", c.Align, true, "left", "center", "right")...)
		errs = append(errs, checkColor("text_color", c.TextColor)...)
		errs = append(errs, checkColor("background", c.Background)...)

	case TypeTicker:
		var c TickerConfig
		if err := decode(cfg, &c); err != nil {
			errs = append(errs, ValidationError{"config", err.Error()})
			break
		}
		errs = append(errs, checkEnum("source", c.Source, false, "manual", "feed", "emergency")...)
		switch c.Source {
		case "manual":
			if len(c.Messages) == 0 {
				errs = append(errs, ValidationError{"messages", "at least one message is required"})
			}
			if len(c.Messages) > MaxTickerItems {
				errs = append(errs, ValidationError{"messages",
					fmt.Sprintf("at most %d messages", MaxTickerItems)})
			}
			for i, m := range c.Messages {
				errs = append(errs, checkLen(fmt.Sprintf("messages[%d]", i), m, MaxBodyLen)...)
			}
		case "feed":
			if c.FeedID <= 0 {
				errs = append(errs, ValidationError{"feed_id", "a feed must be selected"})
			} else {
				refs.FeedIDs = append(refs.FeedIDs, c.FeedID)
			}
		}
		errs = append(errs, checkEnum("direction", c.Direction, true, "left", "right")...)
		if c.SpeedPxSec != 0 && (c.SpeedPxSec < 10 || c.SpeedPxSec > 400) {
			errs = append(errs, ValidationError{"speed_px_sec", "must be between 10 and 400"})
		}

	case TypeEvent:
		var c EventConfig
		if err := decode(cfg, &c); err != nil {
			errs = append(errs, ValidationError{"config", err.Error()})
			break
		}
		errs = append(errs, checkRequired("title", c.Title, MaxHeadingLen)...)
		errs = append(errs, checkLen("subtitle", c.Subtitle, MaxSubtitleLen)...)
		errs = append(errs, checkLen("location", c.Location, MaxLabelLen)...)
		if c.StartsRFC3339 != "" {
			if _, err := time.Parse(time.RFC3339, c.StartsRFC3339); err != nil {
				errs = append(errs, ValidationError{"starts_at", "must be an RFC3339 timestamp"})
			}
		}
		if c.ImageID > 0 {
			refs.MediaIDs = append(refs.MediaIDs, c.ImageID)
		}

	case TypeFallback:
		// The branded fallback needs no configuration.
	}

	return refs, errs.OrNil()
}

// ---- helpers ----------------------------------------------------------------

// decode unmarshals strictly so a typo in a field name is reported rather than
// silently ignored, which would leave an operator staring at a slide that does
// not reflect what they configured.
func decode(s string, dst any) error {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid configuration: %v", err)
	}
	return nil
}

func refID(ref, what string) (int64, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, fmt.Errorf("a %s must be selected", what)
	}
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid %s reference", what)
	}
	return id, nil
}

func checkLen(field, v string, max int) ValidationErrors {
	if utf8.RuneCountInString(v) > max {
		return ValidationErrors{{field, fmt.Sprintf("must be at most %d characters", max)}}
	}
	return nil
}

func checkRequired(field, v string, max int) ValidationErrors {
	if strings.TrimSpace(v) == "" {
		return ValidationErrors{{field, "is required"}}
	}
	return checkLen(field, v, max)
}

func checkEnum(field, v string, allowEmpty bool, allowed ...string) ValidationErrors {
	if v == "" {
		if allowEmpty {
			return nil
		}
		return ValidationErrors{{field, "is required"}}
	}
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return ValidationErrors{{field, fmt.Sprintf("must be one of: %s", strings.Join(allowed, ", "))}}
}

// checkColor accepts only #rgb / #rrggbb / #rrggbbaa.
//
// Colours are interpolated into inline styles in the player. Restricting them to
// a strict hex pattern means an operator cannot inject arbitrary CSS (or a
// url(...) that would make the player fetch something) through a colour field.
func checkColor(field, v string) ValidationErrors {
	if v == "" {
		return nil
	}
	if !strings.HasPrefix(v, "#") {
		return ValidationErrors{{field, "must be a hex colour such as #E86514"}}
	}
	hex := v[1:]
	if len(hex) != 3 && len(hex) != 6 && len(hex) != 8 {
		return ValidationErrors{{field, "must be a hex colour such as #E86514"}}
	}
	for _, r := range hex {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return ValidationErrors{{field, "must be a hex colour such as #E86514"}}
		}
	}
	return nil
}

func checkTimezone(field, tz string) ValidationErrors {
	if tz == "" {
		return nil // the server default applies
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return ValidationErrors{{field, fmt.Sprintf("unknown timezone %q", tz)}}
	}
	return nil
}

// ValidateHTTPURL checks a URL is a well-formed absolute http(s) URL.
//
// It rejects credentials embedded in the URL and, unless allowPlainHTTP, requires
// HTTPS. Note this is a *format* check: SSRF protection for URLs the server
// itself fetches is enforced separately at request time, because DNS can resolve
// to a private address after this check passes.
func ValidateHTTPURL(raw string, allowPlainHTTP bool) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("a URL is required")
	}
	if len(raw) > MaxURLLen {
		return fmt.Errorf("URL must be at most %d characters", MaxURLLen)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("is not a valid URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !allowPlainHTTP {
			return errors.New("must use https")
		}
	default:
		return fmt.Errorf("unsupported scheme %q; only http and https are allowed", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("must include a host")
	}
	if u.User != nil {
		return errors.New("must not embed credentials; use a stored credential instead")
	}
	return nil
}

// ValidateDuration checks a scene duration.
func ValidateDuration(ms int) error {
	if ms < MinDurationMS {
		return fmt.Errorf("duration must be at least %d ms", MinDurationMS)
	}
	if ms > MaxDurationMS {
		return fmt.Errorf("duration must be at most %d ms", MaxDurationMS)
	}
	return nil
}

// DaysMask helpers. Bit 0 is Monday through bit 6 Sunday (ISO order).
//
// Go's time.Weekday counts Sunday as 0, so converting through this helper rather
// than using the value directly is what keeps the two conventions from silently
// drifting one day apart.
const AllDaysMask = 127

// WeekdayBit returns the mask bit for a Go weekday.
func WeekdayBit(d time.Weekday) int {
	iso := (int(d) + 6) % 7 // Sunday(0) -> 6, Monday(1) -> 0
	return 1 << iso
}

// DayEnabled reports whether mask includes the given weekday.
func DayEnabled(mask int, d time.Weekday) bool {
	return mask&WeekdayBit(d) != 0
}

// ValidateDaysMask checks the mask is in range and not empty.
func ValidateDaysMask(mask int) error {
	if mask < 0 || mask > AllDaysMask {
		return fmt.Errorf("days mask must be between 0 and %d", AllDaysMask)
	}
	if mask == 0 {
		return errors.New("at least one day must be selected")
	}
	return nil
}

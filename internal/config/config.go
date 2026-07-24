// Package config loads and validates runtime configuration.
//
// Precedence (later wins): built-in defaults -> config file -> environment.
// The production config file lives at /etc/beermate-display-manager/config.json.
// Secrets never live in the config file; the encryption key is a separate
// 0600 file so the config can be readable for troubleshooting.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Default filesystem locations on the Jetson.
const (
	DefaultConfigDir = "/etc/beermate-display-manager"
	DefaultDataDir   = "/var/lib/beermate-display-manager"
	DefaultAppDir    = "/opt/BeerMateDisplayManager"

	ConfigFileName = "config.json"
	SecretFileName = "secret.key"
)

// Config is the fully resolved application configuration.
type Config struct {
	// ---- HTTP ----------------------------------------------------------
	// ListenAddr is the bind address. Production is 0.0.0.0:8080 so the admin
	// UI is reachable over the Tailscale interface; there is no public exposure
	// because the Jetson sits behind the RUT router with no port forwarding.
	ListenAddr string `json:"listen_addr"`
	// TrustProxyHeaders enables X-Forwarded-For parsing. Off by default: with a
	// direct Tailscale connection, trusting these headers would let a client
	// spoof its IP and defeat login rate limiting.
	TrustProxyHeaders bool `json:"trust_proxy_headers"`
	// CookieSecure forces the Secure flag. Auto-detected per-request when unset,
	// but can be pinned when terminating TLS upstream.
	CookieSecure *bool `json:"cookie_secure,omitempty"`

	// ---- Paths ---------------------------------------------------------
	DataDir   string `json:"data_dir"`
	ConfigDir string `json:"config_dir"`

	// ---- Locale --------------------------------------------------------
	Timezone string `json:"timezone"`

	// ---- Logging -------------------------------------------------------
	LogLevel  string `json:"log_level"`
	LogFormat string `json:"log_format"` // "json" or "text"

	// ---- Sessions ------------------------------------------------------
	SessionIdleTimeout time.Duration `json:"-"`
	SessionMaxLifetime time.Duration `json:"-"`

	// ---- Uploads -------------------------------------------------------
	MaxImageBytes int64 `json:"max_image_bytes"`
	MaxVideoBytes int64 `json:"max_video_bytes"`
	MaxPDFBytes   int64 `json:"max_pdf_bytes"`
	MaxPDFPages   int   `json:"max_pdf_pages"`

	// ---- Managed browser ------------------------------------------------
	// BrowserEnabled turns on the CDP-driven Chromium used for non-embeddable
	// and authenticated websites.
	BrowserEnabled bool `json:"browser_enabled"`
	// BrowserDebugAddr MUST stay on loopback. CDP is a full remote-control
	// interface with access to every stored cookie; binding it to 0.0.0.0 would
	// expose every authenticated website session to the whole tailnet.
	BrowserDebugAddr string `json:"browser_debug_addr"`
	BrowserBinary    string `json:"browser_binary"`
	BrowserXvfbDisp  string `json:"browser_xvfb_display"`
	BrowserRealDisp  string `json:"browser_real_display"`
	BrowserWidth     int    `json:"browser_width"`
	BrowserHeight    int    `json:"browser_height"`

	// ---- Display power --------------------------------------------------
	// DPMSEnabled controls whether the scheduler drives the physical display.
	DPMSEnabled bool   `json:"dpms_enabled"`
	DPMSDisplay string `json:"dpms_display"`
	// DPMSDriver selects the implementation: "xset" on the Jetson, "mock" in dev.
	DPMSDriver string `json:"dpms_driver"`

	// ---- External tools --------------------------------------------------
	PDFToPPMPath string `json:"pdftoppm_path"`
	FFmpegPath   string `json:"ffmpeg_path"`

	// ---- Limits ----------------------------------------------------------
	BackupRetention    int   `json:"backup_retention"`
	SocialCacheMaxPost int   `json:"social_cache_max_posts"`
	LowDiskWarnBytes   int64 `json:"low_disk_warn_bytes"`

	// ---- Derived (not serialised) ---------------------------------------
	Location  *time.Location `json:"-"`
	SecretKey []byte         `json:"-"`
	Dev       bool           `json:"-"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		ListenAddr:         "0.0.0.0:8080",
		TrustProxyHeaders:  false,
		DataDir:            DefaultDataDir,
		ConfigDir:          DefaultConfigDir,
		Timezone:           "Europe/Amsterdam",
		LogLevel:           "info",
		LogFormat:          "json",
		SessionIdleTimeout: 12 * time.Hour,
		SessionMaxLifetime: 7 * 24 * time.Hour,
		MaxImageBytes:      25 << 20,  // 25 MiB
		MaxVideoBytes:      512 << 20, // 512 MiB
		MaxPDFBytes:        64 << 20,  // 64 MiB
		MaxPDFPages:        50,
		BrowserEnabled:     true,
		BrowserDebugAddr:   "127.0.0.1:9222",
		BrowserBinary:      "",     // auto-detected
		BrowserXvfbDisp:    ":99",  // virtual display for capture
		BrowserRealDisp:    ":0",   // physical display for interactive login
		BrowserWidth:       1920,
		BrowserHeight:      1080,
		DPMSEnabled:        true,
		DPMSDisplay:        ":0",
		DPMSDriver:         "xset",
		PDFToPPMPath:       "pdftoppm",
		FFmpegPath:         "ffmpeg",
		BackupRetention:    10,
		SocialCacheMaxPost: 200,
		LowDiskWarnBytes:   1 << 30, // 1 GiB
	}
}

// fileShape mirrors Config but with durations as strings, which is friendlier to
// hand-edit in /etc than nanosecond integers.
type fileShape struct {
	Config
	SessionIdleTimeout string `json:"session_idle_timeout,omitempty"`
	SessionMaxLifetime string `json:"session_max_lifetime,omitempty"`
}

// Load resolves configuration from defaults, an optional file, and the environment.
//
// configPath may be empty, in which case $CONFIG_DIR/config.json is used when it
// exists. A missing config file is not an error: the defaults are a valid
// production configuration.
func Load(configPath string) (Config, error) {
	c := Default()

	// Environment can relocate the config directory before we look for the file.
	if v := os.Getenv("BEERMATE_CONFIG_DIR"); v != "" {
		c.ConfigDir = v
	}
	if configPath == "" {
		configPath = filepath.Join(c.ConfigDir, ConfigFileName)
	}

	if raw, err := os.ReadFile(configPath); err == nil {
		var fs fileShape
		fs.Config = c
		if err := json.Unmarshal(raw, &fs); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", configPath, err)
		}
		c = fs.Config
		if fs.SessionIdleTimeout != "" {
			d, err := time.ParseDuration(fs.SessionIdleTimeout)
			if err != nil {
				return Config{}, fmt.Errorf("session_idle_timeout: %w", err)
			}
			c.SessionIdleTimeout = d
		}
		if fs.SessionMaxLifetime != "" {
			d, err := time.ParseDuration(fs.SessionMaxLifetime)
			if err != nil {
				return Config{}, fmt.Errorf("session_max_lifetime: %w", err)
			}
			c.SessionMaxLifetime = d
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read %s: %w", configPath, err)
	}

	applyEnv(&c)

	if err := c.finalise(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func applyEnv(c *Config) {
	str := func(env string, dst *string) {
		if v := os.Getenv(env); v != "" {
			*dst = v
		}
	}
	boolean := func(env string, dst *bool) {
		if v := os.Getenv(env); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				*dst = b
			}
		}
	}
	i64 := func(env string, dst *int64) {
		if v := os.Getenv(env); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				*dst = n
			}
		}
	}
	integer := func(env string, dst *int) {
		if v := os.Getenv(env); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			}
		}
	}
	dur := func(env string, dst *time.Duration) {
		if v := os.Getenv(env); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				*dst = d
			}
		}
	}

	str("BEERMATE_LISTEN_ADDR", &c.ListenAddr)
	str("BEERMATE_DATA_DIR", &c.DataDir)
	str("BEERMATE_CONFIG_DIR", &c.ConfigDir)
	str("BEERMATE_TIMEZONE", &c.Timezone)
	str("BEERMATE_LOG_LEVEL", &c.LogLevel)
	str("BEERMATE_LOG_FORMAT", &c.LogFormat)
	boolean("BEERMATE_TRUST_PROXY_HEADERS", &c.TrustProxyHeaders)
	dur("BEERMATE_SESSION_IDLE_TIMEOUT", &c.SessionIdleTimeout)
	dur("BEERMATE_SESSION_MAX_LIFETIME", &c.SessionMaxLifetime)
	i64("BEERMATE_MAX_IMAGE_BYTES", &c.MaxImageBytes)
	i64("BEERMATE_MAX_VIDEO_BYTES", &c.MaxVideoBytes)
	i64("BEERMATE_MAX_PDF_BYTES", &c.MaxPDFBytes)
	integer("BEERMATE_MAX_PDF_PAGES", &c.MaxPDFPages)
	boolean("BEERMATE_BROWSER_ENABLED", &c.BrowserEnabled)
	str("BEERMATE_BROWSER_DEBUG_ADDR", &c.BrowserDebugAddr)
	str("BEERMATE_BROWSER_BINARY", &c.BrowserBinary)
	str("BEERMATE_BROWSER_XVFB_DISPLAY", &c.BrowserXvfbDisp)
	str("BEERMATE_BROWSER_REAL_DISPLAY", &c.BrowserRealDisp)
	integer("BEERMATE_BROWSER_WIDTH", &c.BrowserWidth)
	integer("BEERMATE_BROWSER_HEIGHT", &c.BrowserHeight)
	boolean("BEERMATE_DPMS_ENABLED", &c.DPMSEnabled)
	str("BEERMATE_DPMS_DISPLAY", &c.DPMSDisplay)
	str("BEERMATE_DPMS_DRIVER", &c.DPMSDriver)
	str("BEERMATE_PDFTOPPM_PATH", &c.PDFToPPMPath)
	str("BEERMATE_FFMPEG_PATH", &c.FFmpegPath)
	integer("BEERMATE_BACKUP_RETENTION", &c.BackupRetention)
	integer("BEERMATE_SOCIAL_CACHE_MAX_POSTS", &c.SocialCacheMaxPost)
	i64("BEERMATE_LOW_DISK_WARN_BYTES", &c.LowDiskWarnBytes)
	boolean("BEERMATE_DEV", &c.Dev)

	if v := os.Getenv("BEERMATE_COOKIE_SECURE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.CookieSecure = &b
		}
	}
}

// finalise validates the configuration and computes derived fields.
func (c *Config) finalise() error {
	if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		return fmt.Errorf("listen_addr %q: %w", c.ListenAddr, err)
	}
	if c.DataDir == "" {
		return errors.New("data_dir must not be empty")
	}
	if !filepath.IsAbs(c.DataDir) {
		abs, err := filepath.Abs(c.DataDir)
		if err != nil {
			return fmt.Errorf("data_dir: %w", err)
		}
		c.DataDir = abs
	}

	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return fmt.Errorf("timezone %q: %w (is time/tzdata imported?)", c.Timezone, err)
	}
	c.Location = loc

	// Refuse to run with a non-loopback CDP address. This is a hard failure and
	// not a warning: an exposed DevTools port hands over every stored website
	// session cookie to anyone on the tailnet.
	if c.BrowserEnabled {
		if err := validateLoopback(c.BrowserDebugAddr); err != nil {
			return fmt.Errorf("browser_debug_addr: %w", err)
		}
	}

	switch c.DPMSDriver {
	case "xset", "mock", "noop":
	default:
		return fmt.Errorf("dpms_driver %q: must be xset, mock or noop", c.DPMSDriver)
	}
	switch strings.ToLower(c.LogFormat) {
	case "json", "text":
	default:
		return fmt.Errorf("log_format %q: must be json or text", c.LogFormat)
	}

	if c.MaxPDFPages < 1 {
		return errors.New("max_pdf_pages must be >= 1")
	}
	if c.BackupRetention < 1 {
		return errors.New("backup_retention must be >= 1")
	}
	if c.SocialCacheMaxPost < 1 {
		return errors.New("social_cache_max_posts must be >= 1")
	}
	if c.BrowserWidth < 320 || c.BrowserHeight < 240 {
		return errors.New("browser viewport too small")
	}
	return nil
}

// validateLoopback rejects any address that is not bound to the loopback interface.
func validateLoopback(addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q is not host:port: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%q has an invalid port", addr)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%q is not a literal IP or localhost", host)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("%q must be a loopback address; the DevTools port grants full "+
			"browser control including stored session cookies and must never be reachable "+
			"from the network", host)
	}
	return nil
}

// ValidateLoopback is exported for tests and startup self-checks.
func ValidateLoopback(addr string) error { return validateLoopback(addr) }

// Subdirectories of DataDir.
func (c Config) DatabaseDir() string   { return filepath.Join(c.DataDir, "database") }
func (c Config) UploadsDir() string    { return filepath.Join(c.DataDir, "uploads") }
func (c Config) ThumbnailsDir() string { return filepath.Join(c.DataDir, "thumbnails") }
func (c Config) ProfilesDir() string   { return filepath.Join(c.DataDir, "browser-profiles") }
func (c Config) BackupsDir() string    { return filepath.Join(c.DataDir, "backups") }
func (c Config) CacheDir() string      { return filepath.Join(c.DataDir, "cache") }
func (c Config) SocialCacheDir() string { return filepath.Join(c.DataDir, "social-cache") }
func (c Config) RuntimeDir() string    { return filepath.Join(c.DataDir, "runtime") }
func (c Config) DatabasePath() string  { return filepath.Join(c.DatabaseDir(), "beermate.db") }
func (c Config) SecretPath() string    { return filepath.Join(c.ConfigDir, SecretFileName) }

// DataDirs lists every directory that must exist at startup.
func (c Config) DataDirs() []string {
	return []string{
		c.DataDir, c.DatabaseDir(), c.UploadsDir(), c.ThumbnailsDir(),
		c.ProfilesDir(), c.BackupsDir(), c.CacheDir(), c.SocialCacheDir(), c.RuntimeDir(),
	}
}

// EnsureDirs creates the data directory tree with restrictive permissions.
func (c Config) EnsureDirs() error {
	for _, d := range c.DataDirs() {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

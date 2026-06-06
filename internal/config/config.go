package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds all monloader configuration. The override tables back the
// per-site tag and rating settings.
type Config struct {
	Server          ServerConfig     `toml:"server"`
	Monbooru        MonbooruConfig   `toml:"monbooru"`
	Downloader      DownloaderConfig `toml:"downloader"`
	GalleryDL       GalleryDLConfig  `toml:"gallerydl"`
	Auth            AuthConfig       `toml:"auth"`
	Log             LogConfig        `toml:"log"`
	Sites           []Site           `toml:"sites"`
	TagOverrides    []TagOverride    `toml:"tag_overrides"`
	RatingOverrides []RatingOverride `toml:"rating_overrides"`
}

type ServerConfig struct {
	BindAddress string `toml:"bind_address"`
	BaseURL     string `toml:"base_url"`
	// CustomCSS is an optional path to a stylesheet served at /custom.css
	// and linked after the bundled main.css, so the palette can be retuned
	// without a rebuild. Same shape as monbooru's knob.
	CustomCSS string `toml:"custom_css"`
	// BooruName overrides the brand shown in every page <title>, the topbar
	// wordmark, and the login heading. Empty resolves to "monloader" at
	// render time so an existing install upgrades without a config edit.
	BooruName string `toml:"name"`
	// BooruLogo is an optional path to a logo / favicon image served at
	// /custom.logo. When set it replaces both the favicon link and the
	// topbar logo on every page. Same shape as the custom_css knob.
	BooruLogo string `toml:"logo"`
}

// MonbooruConfig points at the monbooru instance the downloader pushes to.
type MonbooruConfig struct {
	APIURL   string `toml:"api_url"`
	APIToken string `toml:"api_token"`
	// WebURL is monbooru's browser-facing base, used to build the links to
	// pushed images in the queue. Empty falls back to APIURL - set it when
	// monbooru is reached at an internal address but browsed at a different
	// public one.
	WebURL         string `toml:"web_url"`
	DefaultGallery string `toml:"default_gallery"`
}

type DownloaderConfig struct {
	Concurrency    int    `toml:"concurrency"`
	MaxItemsPerJob int    `toml:"max_items_per_job"`
	DefaultFolder  string `toml:"default_folder"`
}

// GalleryDLConfig controls the gallery-dl subprocess. ConfigPath is the
// managed file the app writes (never hand-edited); RawConfig is an
// optional JSON object merged into it.
type GalleryDLConfig struct {
	BinaryPath   string  `toml:"binary_path"`
	ConfigPath   string  `toml:"config_path"`
	ArchivePath  string  `toml:"archive_path"`
	CookiesDir   string  `toml:"cookies_dir"`
	SleepRequest float64 `toml:"sleep_request"`
	RawConfig    string  `toml:"raw_config"`
}

// AuthConfig gates the optional UI password and the downloader's own API
// bearer token. Both are off by default for LAN trust.
type AuthConfig struct {
	EnablePassword      bool   `toml:"enable_password"`
	PasswordHash        string `toml:"password_hash"`
	SessionLifetimeDays int    `toml:"session_lifetime_days"`
	APIToken            string `toml:"api_token"`
}

// LogConfig controls log verbosity: "warn" (default), "info", "debug".
type LogConfig struct {
	Level string `toml:"level"`
}

// Site is one repeatable [[sites]] block: credentials and a per-source
// target gallery, written into the managed gallery-dl config. Name is the
// gallery-dl category (e.g. "gelbooru", "e621").
type Site struct {
	Name     string `toml:"name"`
	Username string `toml:"username"`
	APIKey   string `toml:"api_key"`
	UserID   string `toml:"user_id"`
	Gallery  string `toml:"gallery"`
	Cookies  string `toml:"cookies"`
}

// TagOverride routes a gallery-dl tag-category suffix to a monbooru
// category for one site, winning over the curated profile.
type TagOverride struct {
	Site string `toml:"site"`
	From string `toml:"from"`
	To   string `toml:"to"`
}

// RatingOverride routes a booru rating value to a monbooru rating level
// for one site, winning over the curated profile.
type RatingOverride struct {
	Site string `toml:"site"`
	From string `toml:"from"`
	To   string `toml:"to"`
}

// Default returns a fully populated config with the built-in defaults.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			BindAddress: "0.0.0.0:8081",
			BaseURL:     "http://localhost:8081",
		},
		Monbooru: MonbooruConfig{
			APIURL:         "http://monbooru:8080",
			DefaultGallery: "default",
		},
		Downloader: DownloaderConfig{
			Concurrency:    1,
			MaxItemsPerJob: 200,
			DefaultFolder:  "downloads",
		},
		GalleryDL: GalleryDLConfig{
			BinaryPath:   "gallery-dl",
			ConfigPath:   "/config/gallery-dl.json",
			ArchivePath:  "/config/gallery-dl-archive.sqlite",
			CookiesDir:   "/config/cookies",
			SleepRequest: 1.0,
		},
		Auth: AuthConfig{
			SessionLifetimeDays: 7,
		},
		Log: LogConfig{
			Level: "warn",
		},
	}
}

// Load reads the config (creating it with defaults when absent), applies
// MONLOADER_* env overrides, and validates. The result is the effective
// runtime view. Env overrides are applied before the final validation so a
// MONLOADER_* value is sanity-checked the same way a file value is.
func Load(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if writeErr := Save(Default(), path); writeErr != nil {
			return nil, fmt.Errorf("creating default config: %w", writeErr)
		}
	} else if err != nil {
		return nil, fmt.Errorf("checking config file: %w", err)
	}

	cfg, err := LoadFromFile(path)
	if err != nil {
		return nil, err
	}
	applyEnvOverrides(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadFromFile decodes the config file (or defaults when absent) and validates
// it, without applying MONLOADER_* env overrides. This is the persistence
// view: a settings save writes this layer so an ephemeral env value (e.g. a
// token passed via the container env) is never baked into monloader.toml.
func LoadFromFile(path string) (*Config, error) {
	cfg := Default()
	if _, err := os.Stat(path); err == nil {
		// Null the slices so the file's entries replace the (empty)
		// defaults rather than appending to them.
		cfg.Sites = nil
		cfg.TagOverrides = nil
		cfg.RatingOverrides = nil
		if _, err := toml.DecodeFile(path, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file %q: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("checking config file: %w", err)
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save marshals cfg to TOML and writes atomically to path.
func Save(cfg *Config, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	tmpFile, err := os.CreateTemp(dir, ".monloader.toml.*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	if err := toml.NewEncoder(tmpFile).Encode(cfg); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		return fmt.Errorf("encoding config: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// Clone returns a deep copy safe to mutate without affecting the original, so a
// settings save can build the next snapshot from the current one. The slices
// hold value types, so copying the headers and elements is a full copy.
func (cfg *Config) Clone() *Config {
	cp := *cfg
	cp.Sites = append([]Site(nil), cfg.Sites...)
	cp.TagOverrides = append([]TagOverride(nil), cfg.TagOverrides...)
	cp.RatingOverrides = append([]RatingOverride(nil), cfg.RatingOverrides...)
	return &cp
}

// FindSite returns the per-site block with the given gallery-dl category
// name, or nil.
func (cfg *Config) FindSite(name string) *Site {
	for i := range cfg.Sites {
		if cfg.Sites[i].Name == name {
			return &cfg.Sites[i]
		}
	}
	return nil
}

// ValidateRawConfig rejects a non-empty raw gallery-dl passthrough that is
// not a JSON object. An empty string is valid (no passthrough). The
// settings page calls this before Save so invalid JSON is never persisted.
func ValidateRawConfig(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return fmt.Errorf("raw gallery-dl config must be a JSON object: %w", err)
	}
	return nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("MONLOADER_SERVER_BIND_ADDRESS"); v != "" {
		cfg.Server.BindAddress = v
	}
	if v := os.Getenv("MONLOADER_SERVER_BASE_URL"); v != "" {
		cfg.Server.BaseURL = v
	}
	if v := os.Getenv("MONLOADER_MONBOORU_API_URL"); v != "" {
		cfg.Monbooru.APIURL = v
	}
	if v := os.Getenv("MONLOADER_MONBOORU_API_TOKEN"); v != "" {
		cfg.Monbooru.APIToken = v
	}
	if v := os.Getenv("MONLOADER_MONBOORU_WEB_URL"); v != "" {
		cfg.Monbooru.WebURL = v
	}
	if v := os.Getenv("MONLOADER_MONBOORU_DEFAULT_GALLERY"); v != "" {
		cfg.Monbooru.DefaultGallery = v
	}
	if v := os.Getenv("MONLOADER_DOWNLOADER_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Downloader.Concurrency = n
		}
	}
	if v := os.Getenv("MONLOADER_DOWNLOADER_MAX_ITEMS_PER_JOB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Downloader.MaxItemsPerJob = n
		}
	}
	if v := os.Getenv("MONLOADER_DOWNLOADER_DEFAULT_FOLDER"); v != "" {
		cfg.Downloader.DefaultFolder = v
	}
	if v := os.Getenv("MONLOADER_GALLERYDL_BINARY_PATH"); v != "" {
		cfg.GalleryDL.BinaryPath = v
	}
	if v := os.Getenv("MONLOADER_GALLERYDL_CONFIG_PATH"); v != "" {
		cfg.GalleryDL.ConfigPath = v
	}
	if v := os.Getenv("MONLOADER_GALLERYDL_ARCHIVE_PATH"); v != "" {
		cfg.GalleryDL.ArchivePath = v
	}
	if v := os.Getenv("MONLOADER_GALLERYDL_COOKIES_DIR"); v != "" {
		cfg.GalleryDL.CookiesDir = v
	}
	if v := os.Getenv("MONLOADER_GALLERYDL_SLEEP_REQUEST"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.GalleryDL.SleepRequest = f
		}
	}
	if v := os.Getenv("MONLOADER_AUTH_ENABLE_PASSWORD"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Auth.EnablePassword = b
		}
	}
	if v := os.Getenv("MONLOADER_AUTH_PASSWORD_HASH"); v != "" {
		cfg.Auth.PasswordHash = v
	}
	if v := os.Getenv("MONLOADER_AUTH_API_TOKEN"); v != "" {
		cfg.Auth.APIToken = v
	}
	if v := os.Getenv("MONLOADER_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
}

func validate(cfg *Config) error {
	if cfg.Server.BindAddress == "" {
		return fmt.Errorf("server.bind_address must not be empty")
	}
	if !strings.Contains(cfg.Server.BindAddress, ":") {
		return fmt.Errorf("server.bind_address %q is not a valid host:port", cfg.Server.BindAddress)
	}
	// enable_password=true with an empty hash would let the password-update
	// handler bypass the current-password check, so refuse it at load with
	// the same hint monbooru gives.
	if cfg.Auth.EnablePassword && strings.TrimSpace(cfg.Auth.PasswordHash) == "" {
		return fmt.Errorf("auth.enable_password is true but auth.password_hash is empty - " +
			"run `monloader -hash-password 'your-password'` and paste the result into monloader.toml")
	}
	if cfg.Auth.EnablePassword {
		h := strings.TrimSpace(cfg.Auth.PasswordHash)
		if !strings.HasPrefix(h, "$2a$") && !strings.HasPrefix(h, "$2b$") && !strings.HasPrefix(h, "$2y$") {
			return fmt.Errorf("auth.password_hash does not look like a bcrypt hash - " +
				"run `monloader -hash-password 'your-password'` and paste the result into monloader.toml")
		}
	}
	// A non-positive worker count would stall the queue; snap to one worker
	// rather than fail a user-fixable typo.
	if cfg.Downloader.Concurrency <= 0 {
		cfg.Downloader.Concurrency = 1
	}
	if cfg.Downloader.MaxItemsPerJob <= 0 {
		cfg.Downloader.MaxItemsPerJob = 200
	}
	if cfg.GalleryDL.SleepRequest < 0 {
		cfg.GalleryDL.SleepRequest = 0
	}
	if cfg.Auth.SessionLifetimeDays <= 0 {
		cfg.Auth.SessionLifetimeDays = 7
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "warn"
	}
	return nil
}

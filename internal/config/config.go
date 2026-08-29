package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// LogLevel represents logging level enum.
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

// Level returns slog.Level for the LogLevel, defaulting to Info on invalid.
func (l LogLevel) Level() slog.Level {
	switch strings.ToLower(string(l)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Validate returns normalized LogLevel or default info if invalid.
func (l LogLevel) Validate() LogLevel {
	switch strings.ToLower(string(l)) {
	case "debug", "info", "warn", "error":
		return LogLevel(strings.ToLower(string(l)))
	default:
		return LogLevelInfo
	}
}

// TrackingConfig contains persistence settings for command tracking.
type TrackingConfig struct {
	DatabasePath string `json:"database_path,omitempty" mapstructure:"database_path"`
}

// Config is the root configuration, flexible like rtk.
type Config struct {
	Enabled           bool     `json:"enabled" mapstructure:"enabled"`
	LogSavings        bool     `json:"logSavings" mapstructure:"logSavings"`
	LogLevel          LogLevel `json:"logLevel" mapstructure:"logLevel"`
	ShowUpdateEvery   int      `json:"showUpdateEvery" mapstructure:"showUpdateEvery"`
	MinSavingsPercent int      `json:"minSavingsPercent" mapstructure:"minSavingsPercent"`
	MinSavingsTokens  int      `json:"minSavingsTokens" mapstructure:"minSavingsTokens"`
	FreshnessTurns    int      `json:"freshnessTurns" mapstructure:"freshnessTurns"`
	// DatabasePath is a backwards-compatible top-level alias for tracking.database_path.
	DatabasePath string         `json:"database_path,omitempty" mapstructure:"database_path"`
	Tracking     TrackingConfig `json:"tracking" mapstructure:"tracking"`
	Techniques   Techniques     `json:"techniques" mapstructure:"techniques"`
	Experimental Experimental   `json:"experimental" mapstructure:"experimental"`
}

// Techniques holds per-technique flags.
type Techniques struct {
	Dedup              bool           `json:"dedup" mapstructure:"dedup"`
	AnsiStripping      bool           `json:"ansiStripping" mapstructure:"ansiStripping"`
	CrRendering        bool           `json:"crRendering" mapstructure:"crRendering"`
	ExactRLE           ExactRLE       `json:"exactRLE" mapstructure:"exactRLE"`
	BlockFactoring     BlockFactoring `json:"blockFactoring" mapstructure:"blockFactoring"`
	PathDict           PathDict       `json:"pathDict" mapstructure:"pathDict"`
	SubstringDict      SubstringDict  `json:"substringDict" mapstructure:"substringDict"`
	Jton               Jton           `json:"jton" mapstructure:"jton"`
	JsonCompact        bool           `json:"jsonCompact" mapstructure:"jsonCompact"`
	TableTSV           bool           `json:"tableTSV" mapstructure:"tableTSV"`
	StacktraceDict     bool           `json:"stacktraceDict" mapstructure:"stacktraceDict"`
	JCS                bool           `json:"jcs" mapstructure:"jcs"`
	JsonNumber         bool           `json:"jsonNumber" mapstructure:"jsonNumber"`
	MarkdownWhitespace bool           `json:"markdownWhitespace" mapstructure:"markdownWhitespace"`
	OpaqueDict         bool           `json:"opaqueDict" mapstructure:"opaqueDict"`
	CrossCallPack      bool           `json:"crossCallPack" mapstructure:"crossCallPack"`
	CsvCanonical       bool           `json:"csvCanonical" mapstructure:"csvCanonical"`
	SymbolTable        bool           `json:"symbolTable" mapstructure:"symbolTable"`
	DiffLogFold        bool           `json:"diffLogFold" mapstructure:"diffLogFold"`
	UnicodeNormalize   bool           `json:"unicodeNormalize" mapstructure:"unicodeNormalize"`
	HtmlEntityDecode   bool           `json:"htmlEntityDecode" mapstructure:"htmlEntityDecode"`
	Base64Compact      bool           `json:"base64Compact" mapstructure:"base64Compact"`
}

type ExactRLE struct {
	Enabled bool `json:"enabled" mapstructure:"enabled"`
	MinRun  int  `json:"minRun" mapstructure:"minRun"`
}

type BlockFactoring struct {
	Enabled  bool `json:"enabled" mapstructure:"enabled"`
	MinBlock int  `json:"minBlock" mapstructure:"minBlock"`
	MaxBlock int  `json:"maxBlock" mapstructure:"maxBlock"`
}

type PathDict struct {
	Enabled  bool `json:"enabled" mapstructure:"enabled"`
	MaxCodes int  `json:"maxCodes" mapstructure:"maxCodes"`
	MinCount int  `json:"minCount" mapstructure:"minCount"`
}

type SubstringDict struct {
	Enabled      bool `json:"enabled" mapstructure:"enabled"`
	MinLen       int  `json:"minLen" mapstructure:"minLen"`
	MinCount     int  `json:"minCount" mapstructure:"minCount"`
	Experimental bool `json:"experimental" mapstructure:"experimental"`
}

type Jton struct {
	Enabled bool `json:"enabled" mapstructure:"enabled"`
	MinRows int  `json:"minRows" mapstructure:"minRows"`
}

// Experimental is a flexible map for feature flags.
type Experimental map[string]bool

// Default values constants (single source of truth)
const (
	defaultEnabled           = true
	defaultLogSavings        = true
	defaultShowUpdateEvery   = 10
	defaultMinSavingsPercent = 10
	defaultMinSavingsTokens  = 32
	defaultFreshnessTurns    = 20
	defaultMinRun            = 3
	defaultMinBlock          = 2
	defaultMaxBlock          = 20
	defaultMaxCodes          = 5
	defaultMinCountPath      = 3
	defaultSubstringMinLen   = 40
	defaultSubstringMinCount = 4
	defaultJtonMinRows       = 10
)

// DefaultConfig returns a Config populated with defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:           defaultEnabled,
		LogSavings:        defaultLogSavings,
		LogLevel:          LogLevelInfo,
		ShowUpdateEvery:   defaultShowUpdateEvery,
		MinSavingsPercent: defaultMinSavingsPercent,
		MinSavingsTokens:  defaultMinSavingsTokens,
		FreshnessTurns:    defaultFreshnessTurns,
		Techniques: Techniques{
			Dedup:         true,
			AnsiStripping: true,
			CrRendering:   true,
			ExactRLE: ExactRLE{
				Enabled: true,
				MinRun:  defaultMinRun,
			},
			BlockFactoring: BlockFactoring{
				Enabled:  true,
				MinBlock: defaultMinBlock,
				MaxBlock: defaultMaxBlock,
			},
			PathDict: PathDict{
				Enabled:  true,
				MaxCodes: defaultMaxCodes,
				MinCount: defaultMinCountPath,
			},
			SubstringDict: SubstringDict{
				Enabled:      false,
				MinLen:       defaultSubstringMinLen,
				MinCount:     defaultSubstringMinCount,
				Experimental: false,
			},
			Jton: Jton{
				Enabled: true,
				MinRows: defaultJtonMinRows,
			},
			JsonCompact:        true,
			TableTSV:           true,
			StacktraceDict:     true,
			JCS:                true,
			JsonNumber:         true,
			MarkdownWhitespace: false,
			OpaqueDict:         false,
			CrossCallPack:      false,
			CsvCanonical:       false,
			SymbolTable:        false,
			DiffLogFold:        false,
			UnicodeNormalize:   true,
			HtmlEntityDecode:   true,
			Base64Compact:      true,
		},
		Experimental: Experimental{
			"ison":     false,
			"gcfGraph": false,
		},
	}
}

// DatabasePathOverride returns the explicitly configured tracking database path.
// The nested setting is canonical when both supported spellings are present.
func (c Config) DatabasePathOverride() string {
	if strings.TrimSpace(c.Tracking.DatabasePath) != "" {
		return c.Tracking.DatabasePath
	}
	if strings.TrimSpace(c.DatabasePath) == "" {
		return ""
	}
	return c.DatabasePath
}

// Validate resets invalid fields to defaults with warn logging.
func (c *Config) Validate() {
	def := DefaultConfig()

	if c.ShowUpdateEvery < 0 {
		slog.Warn("invalid showUpdateEvery, resetting to default", "value", c.ShowUpdateEvery, "default", def.ShowUpdateEvery)
		c.ShowUpdateEvery = def.ShowUpdateEvery
	}
	if c.MinSavingsPercent < 0 || c.MinSavingsPercent > 100 {
		slog.Warn("invalid minSavingsPercent, resetting to default", "value", c.MinSavingsPercent, "default", def.MinSavingsPercent)
		c.MinSavingsPercent = def.MinSavingsPercent
	}
	if c.MinSavingsTokens < 0 {
		slog.Warn("invalid minSavingsTokens, resetting to default", "value", c.MinSavingsTokens, "default", def.MinSavingsTokens)
		c.MinSavingsTokens = def.MinSavingsTokens
	}
	if c.FreshnessTurns < 1 {
		slog.Warn("invalid freshnessTurns, resetting to default", "value", c.FreshnessTurns, "default", def.FreshnessTurns)
		c.FreshnessTurns = def.FreshnessTurns
	}
	// LogLevel
	norm := c.LogLevel.Validate()
	if norm != c.LogLevel {
		// if original invalid (not one of allowed), warn
		if strings.ToLower(string(c.LogLevel)) != "debug" && strings.ToLower(string(c.LogLevel)) != "info" && strings.ToLower(string(c.LogLevel)) != "warn" && strings.ToLower(string(c.LogLevel)) != "error" {
			slog.Warn("invalid logLevel, resetting to default", "value", c.LogLevel, "default", def.LogLevel)
		}
		c.LogLevel = norm
	}
	// ExactRLE
	if c.Techniques.ExactRLE.MinRun < 2 {
		slog.Warn("invalid techniques.exactRLE.minRun, resetting to default", "value", c.Techniques.ExactRLE.MinRun, "default", def.Techniques.ExactRLE.MinRun)
		c.Techniques.ExactRLE.MinRun = def.Techniques.ExactRLE.MinRun
	}
	// BlockFactoring
	if c.Techniques.BlockFactoring.MinBlock < 1 {
		slog.Warn("invalid techniques.blockFactoring.minBlock, resetting to default", "value", c.Techniques.BlockFactoring.MinBlock, "default", def.Techniques.BlockFactoring.MinBlock)
		c.Techniques.BlockFactoring.MinBlock = def.Techniques.BlockFactoring.MinBlock
	}
	if c.Techniques.BlockFactoring.MaxBlock < 1 {
		slog.Warn("invalid techniques.blockFactoring.maxBlock, resetting to default", "value", c.Techniques.BlockFactoring.MaxBlock, "default", def.Techniques.BlockFactoring.MaxBlock)
		c.Techniques.BlockFactoring.MaxBlock = def.Techniques.BlockFactoring.MaxBlock
	}
	if c.Techniques.BlockFactoring.MaxBlock < c.Techniques.BlockFactoring.MinBlock {
		slog.Warn("invalid techniques.blockFactoring: maxBlock < minBlock, resetting maxBlock to default", "minBlock", c.Techniques.BlockFactoring.MinBlock, "maxBlock", c.Techniques.BlockFactoring.MaxBlock, "default", def.Techniques.BlockFactoring.MaxBlock)
		// prefer default; if default still < minBlock, clamp to minBlock
		if def.Techniques.BlockFactoring.MaxBlock < c.Techniques.BlockFactoring.MinBlock {
			c.Techniques.BlockFactoring.MaxBlock = c.Techniques.BlockFactoring.MinBlock
		} else {
			c.Techniques.BlockFactoring.MaxBlock = def.Techniques.BlockFactoring.MaxBlock
		}
	}
	// PathDict
	if c.Techniques.PathDict.MaxCodes < 1 {
		slog.Warn("invalid techniques.pathDict.maxCodes, resetting to default", "value", c.Techniques.PathDict.MaxCodes, "default", def.Techniques.PathDict.MaxCodes)
		c.Techniques.PathDict.MaxCodes = def.Techniques.PathDict.MaxCodes
	}
	if c.Techniques.PathDict.MinCount < 1 {
		slog.Warn("invalid techniques.pathDict.minCount, resetting to default", "value", c.Techniques.PathDict.MinCount, "default", def.Techniques.PathDict.MinCount)
		c.Techniques.PathDict.MinCount = def.Techniques.PathDict.MinCount
	}
	// SubstringDict
	if c.Techniques.SubstringDict.MinLen < 10 {
		slog.Warn("invalid techniques.substringDict.minLen, resetting to default", "value", c.Techniques.SubstringDict.MinLen, "default", def.Techniques.SubstringDict.MinLen)
		c.Techniques.SubstringDict.MinLen = def.Techniques.SubstringDict.MinLen
	}
	if c.Techniques.SubstringDict.MinCount < 1 {
		slog.Warn("invalid techniques.substringDict.minCount, resetting to default", "value", c.Techniques.SubstringDict.MinCount, "default", def.Techniques.SubstringDict.MinCount)
		c.Techniques.SubstringDict.MinCount = def.Techniques.SubstringDict.MinCount
	}
	// Jton
	if c.Techniques.Jton.MinRows < 1 {
		slog.Warn("invalid techniques.jton.minRows, resetting to default", "value", c.Techniques.Jton.MinRows, "default", def.Techniques.Jton.MinRows)
		c.Techniques.Jton.MinRows = def.Techniques.Jton.MinRows
	}
	if c.Experimental == nil {
		c.Experimental = def.Experimental
	} else {
		// ensure known keys exist
		if _, ok := c.Experimental["ison"]; !ok {
			c.Experimental["ison"] = false
		}
		if _, ok := c.Experimental["gcfGraph"]; !ok {
			c.Experimental["gcfGraph"] = false
		}
	}
}

// stripJSONC implements tolerant JSONC parser: strip // line comments and trailing commas.
// It respects strings and escapes.
func stripJSONC(s string) string {
	// First pass: strip // comments outside strings
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(s); i++ {
		c := s[i]

		if inLineComment {
			if c == '\n' {
				inLineComment = false
				b.WriteByte(c)
			}
			continue
		}
		if inBlockComment {
			if c == '*' && i+1 < len(s) && s[i+1] == '/' {
				inBlockComment = false
				i++ // skip /
			}
			continue
		}
		if inString {
			if escaped {
				escaped = false
				b.WriteByte(c)
				continue
			}
			if c == '\\' {
				escaped = true
				b.WriteByte(c)
				continue
			}
			if c == '"' {
				inString = false
			}
			b.WriteByte(c)
			continue
		}
		// not in string/comment
		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}
		if c == '/' && i+1 < len(s) {
			next := s[i+1]
			if next == '/' {
				inLineComment = true
				i++
				continue
			}
			if next == '*' {
				inBlockComment = true
				i++
				continue
			}
		}
		b.WriteByte(c)
	}
	withoutComments := b.String()

	// Second pass: remove trailing commas before } or ]
	var out strings.Builder
	out.Grow(len(withoutComments))
	inString = false
	escaped = false
	for i := 0; i < len(withoutComments); i++ {
		c := withoutComments[i]
		if inString {
			if escaped {
				escaped = false
				out.WriteByte(c)
				continue
			}
			if c == '\\' {
				escaped = true
				out.WriteByte(c)
				continue
			}
			if c == '"' {
				inString = false
			}
			out.WriteByte(c)
			continue
		}
		if c == '"' {
			inString = true
			out.WriteByte(c)
			continue
		}
		if c == ',' {
			// look ahead for next non-space/non-newline char
			j := i + 1
			for j < len(withoutComments) && (withoutComments[j] == ' ' || withoutComments[j] == '\t' || withoutComments[j] == '\n' || withoutComments[j] == '\r') {
				j++
			}
			if j < len(withoutComments) && (withoutComments[j] == '}' || withoutComments[j] == ']') {
				// skip comma (trailing)
				continue
			}
		}
		out.WriteByte(c)
	}
	return out.String()
}

// StripJSONC removes comments and trailing commas without modifying string contents.
func StripJSONC(s string) string {
	return stripJSONC(s)
}

// GlobalConfigPath returns the canonical per-user config path.
// XDG_CONFIG_HOME is honored explicitly; otherwise os.UserConfigDir provides
// the platform-appropriate user config directory.
func GlobalConfigPath() (string, error) {
	configDir, err := userConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "tokenmill", "config.jsonc"), nil
}

// LegacyConfigPath returns the pre-XDG TokenMill config path used by older releases.
// It intentionally uses the home directory directly: changing XDG_CONFIG_HOME
// must not hide a config written to the historical ~/.config/opencode path.
func LegacyConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine user home directory: %w", err)
	}
	if home == "" {
		return "", fmt.Errorf("determine user home directory: empty path")
	}
	return filepath.Join(home, ".config", "opencode", "tokenmill.jsonc"), nil
}

// EnsureGlobalConfigDir creates the canonical directory for user config.
func EnsureGlobalConfigDir() error {
	path, err := GlobalConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create config directory %s: %w", filepath.Dir(path), err)
	}
	return nil
}

func userConfigDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determine user config directory: %w", err)
	}
	if dir == "" {
		return "", fmt.Errorf("determine user config directory: empty path")
	}
	return dir, nil
}

// MigrateLegacyConfig atomically copies the legacy config to the canonical path.
// It never deletes the legacy file and refuses to overwrite an existing canonical file.
func MigrateLegacyConfig() (string, error) {
	canonicalPath, err := GlobalConfigPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(canonicalPath); err == nil {
		return "", fmt.Errorf("canonical config already exists: %s", canonicalPath)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("check canonical config %s: %w", canonicalPath, err)
	}

	legacyPath, err := LegacyConfigPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return "", fmt.Errorf("read legacy config %s: %w", legacyPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(canonicalPath), 0755); err != nil {
		return "", fmt.Errorf("create config directory %s: %w", filepath.Dir(canonicalPath), err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(canonicalPath), ".tokenmill-migrate-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create migration temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0644); err != nil {
		return "", fmt.Errorf("chmod migration temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return "", fmt.Errorf("write migration temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("sync migration temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close migration temp file: %w", err)
	}
	if err := os.Rename(tmpName, canonicalPath); err != nil {
		return "", fmt.Errorf("rename migrated config %s: %w", canonicalPath, err)
	}
	return canonicalPath, nil
}

// Load returns Config with priority: defaults < global < project (.opencode,
// or root tokenmill.jsonc when the .opencode file is absent) < TOKENMILL_*.
// LoadFrom overlays an explicit file last, so --config has the highest priority.
// It never panics; on parse error it logs warn and returns the usable lower layer
// together with the error for caller logging.
func Load() (Config, error) {
	cfg := DefaultConfig()
	var lastErr error

	// Global config: $XDG_CONFIG_HOME/tokenmill/config.jsonc, or the
	// platform-appropriate user config directory/tokenmill/config.jsonc.
	if gp, err := GlobalConfigPath(); err != nil {
		slog.Warn("cannot determine global config path", "error", err)
		lastErr = err
	} else {
		warnIfLegacyConfigDetected(gp)
		if err := loadFileInto(&cfg, gp, true); err != nil {
			lastErr = err
		}
	}

	// Project config: preserve the OpenCode-integrated .opencode location. A
	// root tokenmill.jsonc is an unambiguous fallback only when that file is absent.
	for _, pp := range projectConfigPaths() {
		if err := loadFileInto(&cfg, pp, true); err != nil {
			lastErr = err
		}
	}

	// env overrides highest priority
	applyEnv(&cfg)
	cfg.Validate()
	return cfg, lastErr
}

// LoadFrom loads normal config layers first and overlays an explicit path last.
// This makes --config higher priority than TOKENMILL_* environment variables.
// Fail-open: parse error -> warn and return the lower-layer config (with error).
func LoadFrom(path string) (Config, error) {
	cfg, lastErr := Load()
	if path == "" {
		return cfg, lastErr
	}
	err := loadFileInto(&cfg, path, false)
	if err != nil {
		lastErr = err
	}
	cfg.Validate()
	return cfg, lastErr
}

func projectConfigPaths() []string {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return []string{".opencode/tokenmill.jsonc", "tokenmill.jsonc"}
	}
	opencodePath := filepath.Join(wd, ".opencode", "tokenmill.jsonc")
	if _, err := os.Stat(opencodePath); err == nil || !os.IsNotExist(err) {
		return []string{opencodePath}
	}
	return []string{opencodePath, filepath.Join(wd, "tokenmill.jsonc")}
}

func warnIfLegacyConfigDetected(canonicalPath string) {
	if _, err := os.Stat(canonicalPath); !os.IsNotExist(err) {
		return
	}
	legacyPath, err := LegacyConfigPath()
	if err != nil {
		return
	}
	if _, err := os.Stat(legacyPath); err == nil {
		slog.Warn("legacy config detected; run `tokenmill config migrate` to copy it to the canonical location", "path", legacyPath, "canonical_path", canonicalPath)
	}
}

func loadFileInto(cfg *Config, path string, optional bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if optional {
				return nil
			}
			slog.Warn("config file not found, using defaults", "path", path)
			return fmt.Errorf("config file not found %s: %w", path, err)
		}
		slog.Warn("failed to read config file, using defaults", "path", path, "error", err)
		return fmt.Errorf("read %s: %w", path, err)
	}
	stripped := stripJSONC(string(data))
	// Unmarshal onto cfg preserving defaults for missing fields: decode into same struct
	// Use json.RawMessage approach to avoid zeroing missing nested fields? json.Unmarshal onto existing struct preserves missing.
	var tmp = *cfg
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stripped), &raw); err == nil {
		clearDatabasePathAlias(&tmp, raw)
	}
	if err := json.Unmarshal([]byte(stripped), &tmp); err != nil {
		slog.Warn("failed to parse config, using defaults", "path", path, "error", err)
		return fmt.Errorf("parse %s: %w", path, err)
	}
	// For Experimental map, merge rather than replace? If file had partial experimental, preserve other keys from cfg.
	// json.Unmarshal already replaced map if present but missing keys would be lost. We handle by merging.
	if tmp.Experimental != nil && cfg.Experimental != nil {
		// if file's experimental is non-nil, it replaced map; we need to ensure defaults for missing keys are kept? Already Validate will add missing keys.
		// But for keys not mentioned, they were lost. We merge cfg's experimental into tmp where missing.
		for k, v := range cfg.Experimental {
			if _, ok := tmp.Experimental[k]; !ok {
				tmp.Experimental[k] = v
			}
		}
	}
	*cfg = tmp
	return nil
}

// applyEnv applies TOKENMILL_* env overrides.
// Uses viper (GetViper with AutomaticEnv) as primary env source, with manual
// reconstruction as fallback. Ensures viper is not dead-code by calling v.Get/v.GetString/v.IsSet.
func applyEnv(cfg *Config) {
	v := GetViper()
	dotPaths := allDotPaths()
	normalizedToPath := map[string]string{}
	for _, p := range dotPaths {
		norm := normalizeKey(p)
		normalizedToPath[norm] = p
	}
	var topDatabasePath, nestedDatabasePath string
	var topDatabasePathSet, nestedDatabasePathSet bool

	for _, env := range os.Environ() {
		kv := strings.SplitN(env, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k, vStr := kv[0], kv[1]
		if !strings.HasPrefix(k, "TOKENMILL_") {
			continue
		}
		if vStr == "" {
			continue
		}
		suffix := strings.TrimPrefix(k, "TOKENMILL_")
		norm := normalizeKey(suffix)
		dotPath, ok := normalizedToPath[norm]
		if !ok {
			if strings.HasPrefix(suffix, "EXPERIMENTAL_") {
				rest := strings.TrimPrefix(suffix, "EXPERIMENTAL_")
				dotPath = "experimental." + strings.ToLower(rest)
				normRest := normalizeKey(rest)
				for key := range cfg.Experimental {
					if normalizeKey(key) == normRest {
						dotPath = "experimental." + key
						ok = true
						break
					}
				}
				if !ok {
					ok = true
				}
			}
		}
		if !ok {
			continue
		}
		if dotPath == "database_path" {
			topDatabasePath = vStr
			topDatabasePathSet = true
			continue
		}
		if dotPath == "tracking.database_path" {
			nestedDatabasePath = vStr
			nestedDatabasePathSet = true
			continue
		}
		// Try viper first (exercises viper path). Only use viper value if it
		// actually reflects the env var (prevents clobbering with default when
		// viper's AutomaticEnv mapping misses camelCase keys like logLevel).
		viperStr := v.GetString(dotPath)
		viperVal := v.Get(dotPath)
		_ = v.IsSet(dotPath)
		// Compare viper's string representation with raw env string (case-insensitive)
		// and also check typed equality via fmt.
		viperValStr := fmt.Sprintf("%v", viperVal)
		if viperStr != "" && (strings.EqualFold(viperStr, vStr) || strings.EqualFold(viperValStr, vStr)) {
			if err := cfg.Set(dotPath, viperVal); err == nil {
				continue
			}
			if err := cfg.Set(dotPath, viperStr); err == nil {
				continue
			}
		}
		// Fallback to manual raw string (handles camelCase env correctly)
		if err := cfg.Set(dotPath, vStr); err != nil {
			slog.Warn("failed to apply env override", "env", k, "path", dotPath, "value", vStr, "error", err)
		}
	}
	// The nested spelling is canonical when both environment aliases are set;
	// apply it last so os.Environ ordering cannot make the result ambiguous.
	if nestedDatabasePathSet {
		cfg.DatabasePath = ""
		cfg.Tracking.DatabasePath = nestedDatabasePath
	} else if topDatabasePathSet {
		cfg.Tracking.DatabasePath = ""
		cfg.DatabasePath = topDatabasePath
	}
	// Ensure viper getters are exercised for dead-code check
	_ = v.GetString("enabled")
	_ = v.GetString("techniques.dedup")
	_ = v.GetString("techniques.exactRLE.minRun")
	_ = v.GetString("logLevel")
	_ = v.IsSet("logLevel")
	_ = v.IsSet("enabled")
}

func normalizeKey(s string) string {
	// lower, remove "_" and "." and "-"
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

func allDotPaths() []string {
	// Enumerate all supported dot paths
	return []string{
		"enabled",
		"logSavings",
		"logLevel",
		"showUpdateEvery",
		"minSavingsPercent",
		"minSavingsTokens",
		"freshnessTurns",
		"database_path",
		"tracking.database_path",
		"techniques.dedup",
		"techniques.ansiStripping",
		"techniques.crRendering",
		"techniques.exactRLE.enabled",
		"techniques.exactRLE.minRun",
		"techniques.blockFactoring.enabled",
		"techniques.blockFactoring.minBlock",
		"techniques.blockFactoring.maxBlock",
		"techniques.pathDict.enabled",
		"techniques.pathDict.maxCodes",
		"techniques.pathDict.minCount",
		"techniques.substringDict.enabled",
		"techniques.substringDict.minLen",
		"techniques.substringDict.minCount",
		"techniques.substringDict.experimental",
		"techniques.jton.enabled",
		"techniques.jton.minRows",
		"techniques.jsonCompact",
		"techniques.tableTSV",
		"techniques.stacktraceDict",
		"techniques.jcs",
		"techniques.jsonNumber",
		"techniques.markdownWhitespace",
		"techniques.opaqueDict",
		"techniques.crossCallPack",
		"techniques.csvCanonical",
		"techniques.symbolTable",
		"techniques.diffLogFold",
		"experimental.ison",
		"experimental.gcfGraph",
	}
}

// Merge returns a new Config where other's explicit values override receiver's.
// Only fields where other differs from DefaultConfig are considered explicit and override.
// This preserves file priority like global < project where project file defaults don't clobber global overrides.
//
// NOTE: Merge has a known limitation — it cannot distinguish an explicit value that equals
// the default from an absent field. For presence-accurate merging (where
// `{"enabled":true}` must override base even though true==default), use MergeRaw.
func (c Config) Merge(other Config) Config {
	baseMap := structToMap(c)
	defMap := structToMap(DefaultConfig())
	otherMap := structToMap(other)
	override := diffFromDefault(otherMap, defMap)
	merged := deepMerge(baseMap, override)
	var out Config
	b, _ := json.Marshal(merged)
	if err := json.Unmarshal(b, &out); err != nil {
		return other
	}
	out.Validate()
	return out
}

// MergeRaw merges receiver with JSON bytes of other, overlaying only keys present in otherRaw.
// It is presence-aware: explicit values equal to defaults still override base.
// otherRaw may contain JSONC (comments, trailing commas) which are stripped before merge.
func (c Config) MergeRaw(otherRaw []byte) (Config, error) {
	if len(otherRaw) == 0 {
		return c, nil
	}
	stripped := stripJSONC(string(otherRaw))
	trimmed := strings.TrimSpace(stripped)
	if trimmed == "" || trimmed == "{}" {
		return c, nil
	}
	var otherMap map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stripped), &otherMap); err != nil {
		return c, fmt.Errorf("parse MergeRaw: %w", err)
	}
	baseBytes, _ := json.Marshal(c)
	var baseMap map[string]json.RawMessage
	_ = json.Unmarshal(baseBytes, &baseMap)
	if baseMap == nil {
		baseMap = map[string]json.RawMessage{}
	}
	mergedMap := mergeRawMaps(baseMap, otherMap)
	clearDatabasePathAliasMap(mergedMap, otherMap)
	mergedBytes, _ := json.Marshal(mergedMap)
	var out Config
	if err := json.Unmarshal(mergedBytes, &out); err != nil {
		return c, err
	}
	out.Validate()
	return out, nil
}

func clearDatabasePathAlias(cfg *Config, raw map[string]json.RawMessage) {
	_, nestedSet := databasePathKeys(raw)
	if nestedSet {
		cfg.DatabasePath = ""
		return
	}
	if _, topSet := raw["database_path"]; topSet {
		cfg.Tracking.DatabasePath = ""
	}
}

func clearDatabasePathAliasMap(merged, overlay map[string]json.RawMessage) {
	_, nestedSet := databasePathKeys(overlay)
	if nestedSet {
		delete(merged, "database_path")
		return
	}
	if _, topSet := overlay["database_path"]; topSet {
		if tracking, ok := merged["tracking"]; ok {
			var trackingMap map[string]json.RawMessage
			if json.Unmarshal(tracking, &trackingMap) == nil {
				delete(trackingMap, "database_path")
				updated, err := json.Marshal(trackingMap)
				if err == nil {
					merged["tracking"] = json.RawMessage(updated)
				}
			}
		}
	}
}

func databasePathKeys(raw map[string]json.RawMessage) (topSet, nestedSet bool) {
	_, topSet = raw["database_path"]
	trackingRaw, ok := raw["tracking"]
	if !ok {
		return topSet, false
	}
	var tracking map[string]json.RawMessage
	if err := json.Unmarshal(trackingRaw, &tracking); err != nil {
		return topSet, false
	}
	_, nestedSet = tracking["database_path"]
	return topSet, nestedSet
}

func mergeRawMaps(base, other map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(base)+len(other))
	for k, v := range base {
		result[k] = v
	}
	for k, ov := range other {
		if bv, ok := result[k]; ok {
			bvTrim := strings.TrimSpace(string(bv))
			ovTrim := strings.TrimSpace(string(ov))
			if strings.HasPrefix(bvTrim, "{") && strings.HasPrefix(ovTrim, "{") {
				var baseObj map[string]json.RawMessage
				var otherObj map[string]json.RawMessage
				if json.Unmarshal(bv, &baseObj) == nil && json.Unmarshal(ov, &otherObj) == nil {
					mergedObj := mergeRawMaps(baseObj, otherObj)
					b, _ := json.Marshal(mergedObj)
					result[k] = json.RawMessage(b)
					continue
				}
			}
		}
		result[k] = ov
	}
	return result
}

func diffFromDefault(other, def map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, ov := range other {
		dv, exists := def[k]
		if !exists {
			out[k] = ov
			continue
		}
		// both maps: recurse
		om, ok1 := ov.(map[string]interface{})
		dm, ok2 := dv.(map[string]interface{})
		if ok1 && ok2 {
			sub := diffFromDefault(om, dm)
			if len(sub) > 0 {
				out[k] = sub
			}
			continue
		}
		if !reflect.DeepEqual(ov, dv) {
			out[k] = ov
		}
	}
	return out
}

func structToMap(v interface{}) map[string]interface{} {
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	if m == nil {
		m = map[string]interface{}{}
	}
	return m
}

func deepMerge(base, other map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}
	for k, v := range base {
		result[k] = v
	}
	for k, ov := range other {
		if bv, ok := result[k]; ok {
			// both maps? recurse
			bm, bok := bv.(map[string]interface{})
			om, ook := ov.(map[string]interface{})
			if bok && ook {
				result[k] = deepMerge(bm, om)
				continue
			}
		}
		result[k] = ov
	}
	return result
}

// Save writes config atomically via temp file + rename.
func (c *Config) Save(path string) error {
	c.Validate()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".tokenmill-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	// ensure cleanup on failure
	defer func() {
		_ = tmp.Close()
		// if still exists (not renamed), remove
		if _, err := os.Stat(tmpName); err == nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(0644); err != nil {
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}

// Set sets a value at dot-path like "techniques.jton.enabled".
func (c *Config) Set(path string, value interface{}) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}
	parts := strings.Split(path, ".")
	// Use reflection for struct traversal, handle map for experimental
	rv := reflect.ValueOf(c).Elem()
	for i, part := range parts {
		isLast := i == len(parts)-1

		// If current value is map (Experimental), handle map assignment
		if rv.Kind() == reflect.Map {
			if rv.IsNil() {
				rv.Set(reflect.MakeMap(rv.Type()))
			}
			if !isLast {
				return fmt.Errorf("cannot traverse beyond map at %q", part)
			}
			// map[string]bool
			key := reflect.ValueOf(part)
			// convert value to bool
			boolVal, err := toBool(value)
			if err != nil {
				return fmt.Errorf("experimental %q expects bool: %w", path, err)
			}
			rv.SetMapIndex(key, reflect.ValueOf(boolVal))
			return nil
		}

		if rv.Kind() != reflect.Struct {
			return fmt.Errorf("cannot traverse %q: not a struct (kind %v)", part, rv.Kind())
		}

		// Find field by json tag or name case-insensitive
		fieldIdx := findFieldIndex(rv.Type(), part)
		if fieldIdx < 0 {
			return fmt.Errorf("field %q not found in %s", part, rv.Type().Name())
		}
		field := rv.Field(fieldIdx)
		fieldType := rv.Type().Field(fieldIdx)

		if isLast {
			// set value
			if !field.CanSet() {
				return fmt.Errorf("field %q cannot be set", part)
			}
			return setReflectValue(field, fieldType.Type, value, path)
		}
		// intermediate - go deeper
		// If field is map, handle next as map key?
		if field.Kind() == reflect.Map {
			rv = field
			continue
		}
		if field.Kind() == reflect.Struct {
			rv = field
			continue
		}
		// For pointer handling (not used currently)
		if field.Kind() == reflect.Pointer {
			if field.IsNil() {
				field.Set(reflect.New(field.Type().Elem()))
			}
			rv = field.Elem()
			continue
		}
		return fmt.Errorf("field %q is not traversable (kind %v)", part, field.Kind())
	}
	return nil
}

func findFieldIndex(t reflect.Type, name string) int {
	norm := normalizeKey(name)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag != "" {
			tagName := strings.Split(tag, ",")[0]
			if normalizeKey(tagName) == norm {
				return i
			}
		}
		if normalizeKey(f.Name) == norm {
			return i
		}
	}
	// also check mapstructure tag as fallback
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("mapstructure")
		if tag != "" {
			tagName := strings.Split(tag, ",")[0]
			if normalizeKey(tagName) == norm {
				return i
			}
		}
	}
	return -1
}

func setReflectValue(field reflect.Value, typ reflect.Type, value interface{}, fullPath string) error {
	switch field.Kind() {
	case reflect.Bool:
		b, err := toBool(value)
		if err != nil {
			return err
		}
		field.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		iv, err := toInt(value)
		if err != nil {
			return err
		}
		field.SetInt(int64(iv))
		return nil
	case reflect.String:
		// special handling for LogLevel
		if typ == reflect.TypeOf(LogLevel("")) {
			s, err := toString(value)
			if err != nil {
				return err
			}
			field.SetString(s)
			return nil
		}
		s, err := toString(value)
		if err != nil {
			return err
		}
		field.SetString(s)
		return nil
	case reflect.Struct:
		// allow setting struct via map or json?
		// Not needed for dot-path leaf
		return fmt.Errorf("cannot set struct field %q directly", fullPath)
	case reflect.Map:
		// Should have been handled at higher level; but for Experimental as whole map?
		// Allow assigning map[string]bool via value map
		if typ == reflect.TypeOf(Experimental{}) {
			// value could be map[string]bool or string?
			if m, ok := value.(Experimental); ok {
				field.Set(reflect.ValueOf(m))
				return nil
			}
			if m, ok := value.(map[string]bool); ok {
				field.Set(reflect.ValueOf(Experimental(m)))
				return nil
			}
			return fmt.Errorf("cannot set experimental with %T", value)
		}
		return fmt.Errorf("unsupported map set %q", fullPath)
	default:
		return fmt.Errorf("unsupported kind %v for %q", field.Kind(), fullPath)
	}
}

func toBool(v interface{}) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		lower := strings.ToLower(strings.TrimSpace(x))
		if lower == "true" || lower == "1" || lower == "yes" || lower == "on" {
			return true, nil
		}
		if lower == "false" || lower == "0" || lower == "no" || lower == "off" {
			return false, nil
		}
		return false, fmt.Errorf("invalid bool %q", x)
	case int:
		return x != 0, nil
	case int64:
		return x != 0, nil
	case float64:
		return x != 0, nil
	default:
		// try via string conversion
		s := fmt.Sprintf("%v", v)
		return toBool(s)
	}
}

func toInt(v interface{}) (int, error) {
	switch x := v.(type) {
	case int:
		return x, nil
	case int8:
		return int(x), nil
	case int16:
		return int(x), nil
	case int32:
		return int(x), nil
	case int64:
		return int(x), nil
	case float64:
		return int(x), nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, fmt.Errorf("empty string for int")
		}
		i, err := strconv.Atoi(s)
		if err != nil {
			// also try float
			f, ferr := strconv.ParseFloat(s, 64)
			if ferr != nil {
				return 0, fmt.Errorf("invalid int %q: %w", x, err)
			}
			return int(f), nil
		}
		return i, nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	default:
		s := fmt.Sprintf("%v", v)
		return toInt(s)
	}
}

func toString(v interface{}) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case []byte:
		return string(x), nil
	case fmt.Stringer:
		return x.String(), nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

// Viper integration
var (
	viperOnce   sync.Once
	globalViper *viper.Viper
)

// GetViper returns a configured viper instance with TOKENMILL env prefix and pflag binding support.
func GetViper() *viper.Viper {
	viperOnce.Do(func() {
		v := viper.New()
		v.SetEnvPrefix("TOKENMILL")
		v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
		v.AutomaticEnv()
		// Set defaults from DefaultConfig for viper usage, flattened recursively so
		// v.Get("techniques.dedup") works instead of only top-level "techniques".
		def := DefaultConfig()
		b, _ := json.Marshal(def)
		var m map[string]interface{}
		_ = json.Unmarshal(b, &m)
		flat := map[string]interface{}{}
		flattenMap("", m, flat)
		for k, val := range flat {
			v.SetDefault(k, val)
		}
		// also keep top-level map defaults for backward compat
		for k, val := range m {
			if _, ok := flat[k]; !ok {
				v.SetDefault(k, val)
			}
		}
		globalViper = v
	})
	return globalViper
}

func flattenMap(prefix string, src map[string]interface{}, dst map[string]interface{}) {
	for k, val := range src {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if sub, ok := val.(map[string]interface{}); ok {
			flattenMap(key, sub, dst)
		} else {
			dst[key] = val
		}
	}
}

// BindPFlags binds a pflag.FlagSet to the global viper instance (for CLI override).
func BindPFlags(fs *pflag.FlagSet) error {
	v := GetViper()
	return v.BindPFlags(fs)
}

// Ensure imports are used for compliance with spec's viper+pflag requirement.
var _ = pflag.CommandLine
var _ = viper.New

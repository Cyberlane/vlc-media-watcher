package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/Cyberlane/vlc-media-watcher/internal/credentials"
)

const appDirectory = "vlc-media-watcher"

type Config struct {
	Profile     string                   `toml:"profile"`
	Credentials CredentialsConfig        `toml:"credentials"`
	VLC         VLCConfig                `toml:"vlc"`
	Sonarr      MediaManagerConfig       `toml:"sonarr"`
	Radarr      MediaManagerConfig       `toml:"radarr"`
	Trackers    map[string]TrackerConfig `toml:"trackers"`
	Watch       WatchConfig              `toml:"watch"`
	Storage     StorageConfig            `toml:"storage"`
}

// CredentialsConfig owns the provider selected when a user explicitly binds
// an individual credential to the global default. Existing source fields
// remain explicit compatibility bindings and are never rewritten on load.
type CredentialsConfig struct {
	DefaultProvider string `toml:"default_provider"`
}

const (
	DefaultProviderKeychain  = "keyring"
	DefaultProvider1Password = "1password"
	ProviderDefault          = "default"
)

type VLCConfig struct {
	Endpoint        string `toml:"endpoint"`
	SecretSource    string `toml:"secret_source"`
	SecretReference string `toml:"secret_reference"`
	PasswordEnv     string `toml:"password_env"`
}

type MediaManagerConfig struct {
	UnmonitorAfterWatch bool `toml:"unmonitor_after_watch"`
	MetadataLookup      bool `toml:"metadata_lookup"`
	// UpdateMonitored and MonitoredAfterWatch are retained only to read older
	// configuration files. New files use UnmonitorAfterWatch.
	UpdateMonitored     bool   `toml:"update_monitored"`
	MonitoredAfterWatch bool   `toml:"monitored_after_watch"`
	Endpoint            string `toml:"endpoint"`
	SecretSource        string `toml:"secret_source"`
	SecretReference     string `toml:"secret_reference"`
	APIKeyEnv           string `toml:"api_key_env"`
	LocalPathPrefix     string `toml:"local_path_prefix"`
	RemotePathPrefix    string `toml:"remote_path_prefix"`
}

// TrackerConfig holds non-secret OAuth application metadata. The account
// token itself is resolved exactly like other secrets and is never written to
// config.toml or SQLite.
type TrackerConfig struct {
	Enabled               bool   `toml:"enabled"`
	SyncProgress          bool   `toml:"sync_progress"`
	ClientID              string `toml:"client_id"`
	ClientSecretSource    string `toml:"client_secret_source"`
	ClientSecretReference string `toml:"client_secret_reference"`
	ClientSecretEnv       string `toml:"client_secret_env"`
	SecretSource          string `toml:"secret_source"`
	SecretReference       string `toml:"secret_reference"`
	AccessTokenEnv        string `toml:"access_token_env"`
}

// UnmonitoringEnabled reports whether completed media should be unmonitored.
// It also understands the pre-1.0 two-setting configuration for upgrades.
func (c MediaManagerConfig) UnmonitoringEnabled() bool {
	return c.UnmonitorAfterWatch || (c.UpdateMonitored && !c.MonitoredAfterWatch)
}

// LookupEnabled reports whether this service may be read to identify an exact
// watched file for tracker mapping. It never enables a remote library write.
func (c MediaManagerConfig) LookupEnabled() bool { return c.MetadataLookup || c.UnmonitoringEnabled() }

type WatchConfig struct {
	EpisodeThreshold float64       `toml:"episode_threshold"`
	MovieThreshold   float64       `toml:"movie_threshold"`
	PollInterval     time.Duration `toml:"poll_interval"`
}

type StorageConfig struct {
	Path string `toml:"path"`
}

// CredentialRegistry normalizes the existing field-specific configuration
// into provider-neutral stable IDs. It is intentionally read-only: this first
// compatibility slice must not copy values, migrate providers, or rewrite a
// user's configuration file.
func (c Config) CredentialRegistry() (credentials.Registry, error) {
	effective := c.EffectiveCredentialBindings()
	entries := []credentials.Entry{
		{
			Requirement: credentials.Requirement{ID: credentials.VLCPasswordID, Label: "VLC password", Kind: credentials.OpaqueSecret, Ownership: credentials.UserStored, Required: true},
			Binding:     credentialBinding(effective.VLC.SecretSource, effective.VLC.SecretReference, effective.VLC.PasswordEnv),
		},
		{
			Requirement: credentials.Requirement{ID: credentials.SonarrAPIKeyID, Label: "Sonarr API key", Kind: credentials.OpaqueSecret, Ownership: credentials.UserStored},
			Binding:     credentialBinding(effective.Sonarr.SecretSource, effective.Sonarr.SecretReference, effective.Sonarr.APIKeyEnv),
		},
		{
			Requirement: credentials.Requirement{ID: credentials.RadarrAPIKeyID, Label: "Radarr API key", Kind: credentials.OpaqueSecret, Ownership: credentials.UserStored},
			Binding:     credentialBinding(effective.Radarr.SecretSource, effective.Radarr.SecretReference, effective.Radarr.APIKeyEnv),
		},
	}

	for _, name := range []string{"anilist", "myanimelist", "trakt", "simkl"} {
		tracker := effective.Trackers[name]
		entries = append(entries, credentials.Entry{
			Requirement: credentials.Requirement{ID: credentials.TrackerAccessTokenID(name), Label: trackerLabel(name) + " access token", Kind: trackerAccessTokenKind(name), Ownership: credentials.AppWritten},
			Binding:     credentialBinding(tracker.SecretSource, tracker.SecretReference, tracker.AccessTokenEnv),
		})
		if name == "anilist" || name == "trakt" {
			entries = append(entries, credentials.Entry{
				Requirement: credentials.Requirement{ID: credentials.TrackerClientSecretID(name), Label: trackerLabel(name) + " OAuth client secret", Kind: credentials.OpaqueSecret, Ownership: credentials.UserStored},
				Binding:     credentialBinding(tracker.ClientSecretSource, tracker.ClientSecretReference, tracker.ClientSecretEnv),
			})
		}
	}

	return credentials.NewRegistry(entries...)
}

// DefaultCredentialProvider returns the selected interactive provider. An
// empty value is the legacy-safe Keychain default so manually constructed
// configurations preserve historical behavior.
func (c Config) DefaultCredentialProvider() string {
	return EffectiveCredentialProvider(ProviderDefault, c.Credentials.DefaultProvider)
}

// EffectiveCredentialProvider resolves the special default binding without
// modifying persisted configuration. Environment remains a compatibility
// binding only and is intentionally not eligible as a global onboarding
// default.
func EffectiveCredentialProvider(source, defaultProvider string) string {
	if strings.TrimSpace(source) != ProviderDefault {
		return strings.TrimSpace(source)
	}
	switch strings.TrimSpace(defaultProvider) {
	case DefaultProvider1Password:
		return DefaultProvider1Password
	default:
		return DefaultProviderKeychain
	}
}

// EffectiveCredentialBindings returns a copy whose explicit default bindings
// are resolved to the configured provider. It is for runtime consumers only;
// callers must continue saving the original configuration to preserve the
// user's default-versus-override choice.
func (c Config) EffectiveCredentialBindings() Config {
	effective := c
	effective.VLC.SecretSource = EffectiveCredentialProvider(c.VLC.SecretSource, c.Credentials.DefaultProvider)
	effective.Sonarr.SecretSource = EffectiveCredentialProvider(c.Sonarr.SecretSource, c.Credentials.DefaultProvider)
	effective.Radarr.SecretSource = EffectiveCredentialProvider(c.Radarr.SecretSource, c.Credentials.DefaultProvider)
	if c.Trackers != nil {
		effective.Trackers = make(map[string]TrackerConfig, len(c.Trackers))
		for name, trackerConfig := range c.Trackers {
			trackerConfig.SecretSource = EffectiveCredentialProvider(trackerConfig.SecretSource, c.Credentials.DefaultProvider)
			trackerConfig.ClientSecretSource = EffectiveCredentialProvider(trackerConfig.ClientSecretSource, c.Credentials.DefaultProvider)
			effective.Trackers[name] = trackerConfig
		}
	}
	return effective
}

func trackerAccessTokenKind(name string) credentials.Kind {
	switch name {
	case "myanimelist", "trakt":
		return credentials.RenewableToken
	default:
		return credentials.OpaqueSecret
	}
}

func credentialBinding(source, reference, environment string) credentials.Binding {
	return credentials.Binding{
		Provider:    credentials.Provider(source),
		Locator:     reference,
		Environment: environment,
	}
}

func trackerLabel(name string) string {
	switch name {
	case "anilist":
		return "AniList"
	case "myanimelist":
		return "MyAnimeList"
	case "trakt":
		return "Trakt"
	case "simkl":
		return "SIMKL"
	default:
		return name
	}
}

// DefaultPath returns the platform-appropriate path for the application
// configuration file. os.UserConfigDir follows XDG_CONFIG_HOME on Unix,
// Library/Application Support on macOS, and AppData on Windows.
func DefaultPath() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(base, appDirectory, "config.toml")
}

func (c Config) DatabasePath() string {
	if c.Storage.Path != "" {
		return c.Storage.Path
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "watcher.db"
	}
	return filepath.Join(base, appDirectory, "watcher.db")
}

func Load(path string) (*Config, error) {
	var cfg Config
	metadata, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	migrateLegacyMediaManager(&cfg.Sonarr, metadata.IsDefined("sonarr", "unmonitor_after_watch"))
	migrateLegacyMediaManager(&cfg.Radarr, metadata.IsDefined("radarr", "unmonitor_after_watch"))
	if cfg.Credentials.DefaultProvider == "" {
		cfg.Credentials.DefaultProvider = DefaultProviderKeychain
	}
	if cfg.VLC.SecretSource == "" {
		cfg.VLC.SecretSource = "environment"
	}
	applyMediaManagerDefaults(&cfg.Sonarr, cfg.Profile, "sonarr", "http://127.0.0.1:8989", "VLC_MEDIA_WATCHER_SONARR_API_KEY")
	applyMediaManagerDefaults(&cfg.Radarr, cfg.Profile, "radarr", "http://127.0.0.1:7878", "VLC_MEDIA_WATCHER_RADARR_API_KEY")
	applyTrackerDefaults(&cfg)
	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyTrackerDefaults(cfg *Config) {
	if cfg.Trackers == nil {
		cfg.Trackers = make(map[string]TrackerConfig)
	}
	for _, name := range []string{"anilist", "anidb", "myanimelist", "trakt", "simkl"} {
		cfg.Trackers[name] = trackerWithCredentialDefaults(cfg.Profile, name, cfg.Trackers[name])
	}
}

func trackerWithCredentialDefaults(profile, name string, tracker TrackerConfig) TrackerConfig {
	// OAuth callbacks deliver a user access token to this local process. The
	// client writes that token only to the system keychain, so using a selectable
	// 1Password/environment source here was misleading and could leave linking
	// unable to complete. Preserve client-secret flexibility, but migrate account
	// tokens to their one supported durable location.
	if name != "anidb" {
		tracker.SecretSource = "keyring"
		tracker.SecretReference = profile + "/" + name + "-access-token"
		tracker.AccessTokenEnv = "VLC_MEDIA_WATCHER_" + strings.ToUpper(name) + "_ACCESS_TOKEN"
	}
	if tracker.ClientSecretSource == "" {
		tracker.ClientSecretSource = "keyring"
	}
	if tracker.ClientSecretReference == "" {
		tracker.ClientSecretReference = profile + "/" + name + "-client-secret"
	}
	if tracker.ClientSecretEnv == "" {
		tracker.ClientSecretEnv = "VLC_MEDIA_WATCHER_" + strings.ToUpper(name) + "_CLIENT_SECRET"
	}
	if tracker.SecretReference == "" {
		tracker.SecretReference = profile + "/" + name + "-access-token"
	}
	if tracker.AccessTokenEnv == "" {
		tracker.AccessTokenEnv = "VLC_MEDIA_WATCHER_" + strings.ToUpper(name) + "_ACCESS_TOKEN"
	}
	return tracker
}

func migrateLegacyMediaManager(cfg *MediaManagerConfig, hasNewSetting bool) {
	if !hasNewSetting {
		cfg.UnmonitorAfterWatch = cfg.UpdateMonitored && !cfg.MonitoredAfterWatch
	}
}

func applyMediaManagerDefaults(cfg *MediaManagerConfig, profile, name, endpoint, apiKeyEnv string) {
	if cfg.Endpoint == "" {
		cfg.Endpoint = endpoint
	}
	if cfg.SecretSource == "" {
		cfg.SecretSource = "keyring"
	}
	if cfg.SecretReference == "" {
		cfg.SecretReference = profile + "/" + name + "-api-key"
	}
	if cfg.APIKeyEnv == "" {
		cfg.APIKeyEnv = apiKeyEnv
	}
}

func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("configuration is required")
	}
	if cfg.Profile == "" {
		return fmt.Errorf("profile is required")
	}
	if cfg.Credentials.DefaultProvider != "" && cfg.Credentials.DefaultProvider != DefaultProviderKeychain && cfg.Credentials.DefaultProvider != DefaultProvider1Password {
		return fmt.Errorf("credentials.default_provider must be keyring or 1password")
	}
	effective := cfg.EffectiveCredentialBindings()
	if effective.VLC.Endpoint == "" {
		return fmt.Errorf("vlc.endpoint is required")
	}
	switch effective.VLC.SecretSource {
	case "environment":
		if effective.VLC.PasswordEnv == "" {
			return fmt.Errorf("vlc.password_env is required for the environment secret source")
		}
	case "keyring", "1password":
		if effective.VLC.SecretReference == "" {
			return fmt.Errorf("vlc.secret_reference is required for the %s secret source", effective.VLC.SecretSource)
		}
		if effective.VLC.SecretSource == "1password" && !strings.HasPrefix(effective.VLC.SecretReference, "op://") {
			return fmt.Errorf("vlc.secret_reference must be an explicit op:// reference for the 1password secret source")
		}
	default:
		return fmt.Errorf("vlc.secret_source must be default, environment, keyring, or 1password")
	}
	if err := validateMediaManager("sonarr", effective.Sonarr); err != nil {
		return err
	}
	if err := validateMediaManager("radarr", effective.Radarr); err != nil {
		return err
	}
	for name, tracker := range effective.Trackers {
		if err := validateTracker(name, tracker); err != nil {
			return err
		}
	}
	if cfg.Watch.EpisodeThreshold <= 0 || cfg.Watch.EpisodeThreshold > 1 {
		return fmt.Errorf("watch.episode_threshold must be between 0 and 1")
	}
	if cfg.Watch.MovieThreshold <= 0 || cfg.Watch.MovieThreshold > 1 {
		return fmt.Errorf("watch.movie_threshold must be between 0 and 1")
	}
	if cfg.Watch.PollInterval <= 0 {
		return fmt.Errorf("watch.poll_interval must be positive")
	}
	return nil
}

func validateTracker(name string, tracker TrackerConfig) error {
	if tracker.SyncProgress {
		if !tracker.Enabled {
			return fmt.Errorf("trackers.%s.sync_progress requires the tracker to be enabled", name)
		}
		if name != "anilist" {
			return fmt.Errorf("trackers.%s.sync_progress is not supported yet", name)
		}
	}
	if !tracker.Enabled {
		return nil
	}
	// AniDB is a reference and mapping provider. Its public title dump is
	// locally cached and no account credential is required to review or
	// confirm an AID.
	if name == "anidb" {
		return nil
	}
	if strings.TrimSpace(tracker.ClientID) == "" {
		return fmt.Errorf("trackers.%s.client_id is required when the tracker is enabled", name)
	}
	if name == "anilist" && strings.EqualFold(strings.TrimSpace(tracker.ClientID), "VLC_MEDIA_WATCHER_ID") {
		return fmt.Errorf("trackers.anilist.client_id must be the client-ID value, not the VLC_MEDIA_WATCHER_ID field label")
	}
	switch tracker.SecretSource {
	case "environment":
		if strings.TrimSpace(tracker.AccessTokenEnv) == "" {
			return fmt.Errorf("trackers.%s.access_token_env is required for the environment secret source", name)
		}
	case "keyring", "1password":
		if strings.TrimSpace(tracker.SecretReference) == "" {
			return fmt.Errorf("trackers.%s.secret_reference is required for the %s secret source", name, tracker.SecretSource)
		}
		if tracker.SecretSource == "1password" && !strings.HasPrefix(strings.TrimSpace(tracker.SecretReference), "op://") {
			return fmt.Errorf("trackers.%s.secret_reference must be an explicit op:// reference for the 1password secret source", name)
		}
	default:
		return fmt.Errorf("trackers.%s.secret_source must be default, environment, keyring, or 1password", name)
	}
	if name == "anilist" || name == "trakt" {
		switch tracker.ClientSecretSource {
		case "environment":
			if strings.TrimSpace(tracker.ClientSecretEnv) == "" {
				return fmt.Errorf("trackers.%s.client_secret_env is required for the environment secret source", name)
			}
		case "keyring", "1password":
			if strings.TrimSpace(tracker.ClientSecretReference) == "" {
				return fmt.Errorf("trackers.%s.client_secret_reference is required for the %s secret source", name, tracker.ClientSecretSource)
			}
			if tracker.ClientSecretSource == "1password" && !strings.HasPrefix(strings.TrimSpace(tracker.ClientSecretReference), "op://") {
				return fmt.Errorf("trackers.%s.client_secret_reference must be an explicit op:// reference for the 1password secret source", name)
			}
		default:
			return fmt.Errorf("trackers.%s.client_secret_source must be default, environment, keyring, or 1password", name)
		}
	}
	return nil
}

func validateMediaManager(name string, cfg MediaManagerConfig) error {
	if !cfg.LookupEnabled() {
		return nil
	}

	endpoint, err := url.Parse(strings.TrimSpace(cfg.Endpoint))
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return fmt.Errorf("%s.endpoint must be a full http or https URL", name)
	}
	if endpoint.User != nil {
		return fmt.Errorf("%s.endpoint must not include credentials", name)
	}
	if endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("%s.endpoint must not include a query or fragment", name)
	}
	if strings.HasSuffix(strings.ToLower(strings.TrimRight(endpoint.Path, "/")), "/api/v3") {
		return fmt.Errorf("%s.endpoint must not include /api/v3", name)
	}

	switch cfg.SecretSource {
	case "environment":
		if strings.TrimSpace(cfg.APIKeyEnv) == "" {
			return fmt.Errorf("%s.api_key_env is required for the environment secret source", name)
		}
	case "keyring", "1password":
		if strings.TrimSpace(cfg.SecretReference) == "" {
			return fmt.Errorf("%s.secret_reference is required for the %s secret source", name, cfg.SecretSource)
		}
		if cfg.SecretSource == "1password" && !strings.HasPrefix(strings.TrimSpace(cfg.SecretReference), "op://") {
			return fmt.Errorf("%s.secret_reference must be an explicit op:// reference for the 1password secret source", name)
		}
	default:
		return fmt.Errorf("%s.secret_source must be default, environment, keyring, or 1password", name)
	}

	hasLocalPathPrefix := strings.TrimSpace(cfg.LocalPathPrefix) != ""
	hasRemotePathPrefix := strings.TrimSpace(cfg.RemotePathPrefix) != ""
	if hasLocalPathPrefix != hasRemotePathPrefix {
		return fmt.Errorf("%s.local_path_prefix and %s.remote_path_prefix must both be set or both be empty", name, name)
	}

	return nil
}

func Save(path string, cfg *Config) error {
	if cfg != nil {
		cfg.Credentials.DefaultProvider = cfg.DefaultCredentialProvider()
		applyTrackerDefaults(cfg)
	}
	if err := Validate(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	content := fmt.Sprintf(`# VLC Media Watcher configuration. Managed by the terminal UI.
profile = %s

[credentials]
default_provider = %s

[vlc]
endpoint = %s
secret_source = %s
secret_reference = %s
password_env = %s

[sonarr]
unmonitor_after_watch = %t
metadata_lookup = %t
endpoint = %s
secret_source = %s
secret_reference = %s
api_key_env = %s
local_path_prefix = %s
remote_path_prefix = %s

[radarr]
unmonitor_after_watch = %t
metadata_lookup = %t
endpoint = %s
secret_source = %s
secret_reference = %s
api_key_env = %s
local_path_prefix = %s
remote_path_prefix = %s

[trackers.anilist]
enabled = %t
sync_progress = %t
client_id = %s
client_secret_source = %s
client_secret_reference = %s
client_secret_env = %s
secret_source = %s
secret_reference = %s
access_token_env = %s

[trackers.anidb]
enabled = %t
sync_progress = false

[trackers.myanimelist]
enabled = %t
sync_progress = false
client_id = %s
client_secret_source = %s
client_secret_reference = %s
client_secret_env = %s
secret_source = %s
secret_reference = %s
access_token_env = %s

[trackers.trakt]
enabled = %t
sync_progress = false
client_id = %s
client_secret_source = %s
client_secret_reference = %s
client_secret_env = %s
secret_source = %s
secret_reference = %s
access_token_env = %s

[trackers.simkl]
enabled = %t
sync_progress = false
client_id = %s
client_secret_source = %s
client_secret_reference = %s
client_secret_env = %s
secret_source = %s
secret_reference = %s
access_token_env = %s

[watch]
episode_threshold = %.4g
movie_threshold = %.4g
poll_interval = %s

[storage]
path = %s
`, quote(cfg.Profile), quote(cfg.DefaultCredentialProvider()), quote(cfg.VLC.Endpoint), quote(cfg.VLC.SecretSource), quote(cfg.VLC.SecretReference), quote(cfg.VLC.PasswordEnv),
		cfg.Sonarr.UnmonitoringEnabled(), cfg.Sonarr.MetadataLookup, quote(cfg.Sonarr.Endpoint), quote(cfg.Sonarr.SecretSource), quote(cfg.Sonarr.SecretReference), quote(cfg.Sonarr.APIKeyEnv), quote(cfg.Sonarr.LocalPathPrefix), quote(cfg.Sonarr.RemotePathPrefix),
		cfg.Radarr.UnmonitoringEnabled(), cfg.Radarr.MetadataLookup, quote(cfg.Radarr.Endpoint), quote(cfg.Radarr.SecretSource), quote(cfg.Radarr.SecretReference), quote(cfg.Radarr.APIKeyEnv), quote(cfg.Radarr.LocalPathPrefix), quote(cfg.Radarr.RemotePathPrefix),
		trackerForSave(cfg, "anilist").Enabled, trackerForSave(cfg, "anilist").SyncProgress, quote(trackerForSave(cfg, "anilist").ClientID), quote(trackerForSave(cfg, "anilist").ClientSecretSource), quote(trackerForSave(cfg, "anilist").ClientSecretReference), quote(trackerForSave(cfg, "anilist").ClientSecretEnv), quote(trackerForSave(cfg, "anilist").SecretSource), quote(trackerForSave(cfg, "anilist").SecretReference), quote(trackerForSave(cfg, "anilist").AccessTokenEnv),
		trackerForSave(cfg, "anidb").Enabled,
		trackerForSave(cfg, "myanimelist").Enabled, quote(trackerForSave(cfg, "myanimelist").ClientID), quote(trackerForSave(cfg, "myanimelist").ClientSecretSource), quote(trackerForSave(cfg, "myanimelist").ClientSecretReference), quote(trackerForSave(cfg, "myanimelist").ClientSecretEnv), quote(trackerForSave(cfg, "myanimelist").SecretSource), quote(trackerForSave(cfg, "myanimelist").SecretReference), quote(trackerForSave(cfg, "myanimelist").AccessTokenEnv),
		trackerForSave(cfg, "trakt").Enabled, quote(trackerForSave(cfg, "trakt").ClientID), quote(trackerForSave(cfg, "trakt").ClientSecretSource), quote(trackerForSave(cfg, "trakt").ClientSecretReference), quote(trackerForSave(cfg, "trakt").ClientSecretEnv), quote(trackerForSave(cfg, "trakt").SecretSource), quote(trackerForSave(cfg, "trakt").SecretReference), quote(trackerForSave(cfg, "trakt").AccessTokenEnv),
		trackerForSave(cfg, "simkl").Enabled, quote(trackerForSave(cfg, "simkl").ClientID), quote(trackerForSave(cfg, "simkl").ClientSecretSource), quote(trackerForSave(cfg, "simkl").ClientSecretReference), quote(trackerForSave(cfg, "simkl").ClientSecretEnv), quote(trackerForSave(cfg, "simkl").SecretSource), quote(trackerForSave(cfg, "simkl").SecretReference), quote(trackerForSave(cfg, "simkl").AccessTokenEnv),
		cfg.Watch.EpisodeThreshold, cfg.Watch.MovieThreshold, quote(cfg.Watch.PollInterval.String()), quote(cfg.Storage.Path))
	return os.WriteFile(path, []byte(content), 0o600)
}

func trackerForSave(cfg *Config, name string) TrackerConfig {
	tracker := trackerWithCredentialDefaults(cfg.Profile, name, cfg.Trackers[name])
	if name == "anidb" && tracker.SecretSource == "" {
		tracker.SecretSource = "keyring"
	}
	return tracker
}

func quote(value string) string {
	return strconv.Quote(value)
}

const example = `# VLC Media Watcher configuration. Keep credentials out of this file.
profile = "default"

[vlc]
endpoint = "http://127.0.0.1:8080"
secret_source = "keyring"
secret_reference = "default/vlc-http-password"
password_env = "VLC_MEDIA_WATCHER_VLC_PASSWORD"

[sonarr]
# Optional. When enabled, matched episodes are unmonitored after their watch
# threshold is reached. API keys remain outside this file.
unmonitor_after_watch = false
metadata_lookup = false
endpoint = "http://127.0.0.1:8989"
secret_source = "keyring"
secret_reference = "default/sonarr-api-key"
api_key_env = "VLC_MEDIA_WATCHER_SONARR_API_KEY"
# Set both prefixes only when VLC and Sonarr see the same files at different paths.
local_path_prefix = ""
remote_path_prefix = ""

[radarr]
# Optional. When enabled, matched movies are unmonitored after their watch
# threshold is reached. API keys remain outside this file.
unmonitor_after_watch = false
metadata_lookup = false
endpoint = "http://127.0.0.1:7878"
secret_source = "keyring"
secret_reference = "default/radarr-api-key"
api_key_env = "VLC_MEDIA_WATCHER_RADARR_API_KEY"
local_path_prefix = ""
remote_path_prefix = ""

[trackers.anilist]
# Optional. Add the OAuth application client ID in the Tracker TUI tab, then
# store the linked account's access token outside this file.
enabled = false
# Optional. When enabled, completed VLC episodes may advance AniList only
# when the exact next episode can be proven. It never guesses skipped episodes.
sync_progress = false
client_id = ""
client_secret_source = "keyring"
client_secret_reference = "default/anilist-client-secret"
client_secret_env = "VLC_MEDIA_WATCHER_ANILIST_CLIENT_SECRET"
secret_source = "keyring"
secret_reference = "default/anilist-access-token"
access_token_env = "VLC_MEDIA_WATCHER_ANILIST_ACCESS_TOKEN"

[trackers.anidb]
# Optional. AniDB is used only to search and confirm season-level AIDs. It
# does not need an account token or OAuth client.
enabled = false

[trackers.myanimelist]
enabled = false
client_id = ""
client_secret_source = "keyring"
client_secret_reference = "default/myanimelist-client-secret"
client_secret_env = "VLC_MEDIA_WATCHER_MYANIMELIST_CLIENT_SECRET"
secret_source = "keyring"
secret_reference = "default/myanimelist-access-token"
access_token_env = "VLC_MEDIA_WATCHER_MYANIMELIST_ACCESS_TOKEN"

[trackers.trakt]
enabled = false
client_id = ""
client_secret_source = "keyring"
client_secret_reference = "default/trakt-client-secret"
client_secret_env = "VLC_MEDIA_WATCHER_TRAKT_CLIENT_SECRET"
secret_source = "keyring"
secret_reference = "default/trakt-access-token"
access_token_env = "VLC_MEDIA_WATCHER_TRAKT_ACCESS_TOKEN"

[trackers.simkl]
enabled = false
client_id = ""
client_secret_source = "keyring"
client_secret_reference = "default/simkl-client-secret"
client_secret_env = "VLC_MEDIA_WATCHER_SIMKL_CLIENT_SECRET"
secret_source = "keyring"
secret_reference = "default/simkl-access-token"
access_token_env = "VLC_MEDIA_WATCHER_SIMKL_ACCESS_TOKEN"

[watch]
# Advanced settings. Values are fractions: 0.90 means 90% watched.
episode_threshold = 0.90
movie_threshold = 0.85
poll_interval = "2s"

[storage]
# Optional. The default is the operating system's application-data directory.
# path = "/absolute/path/to/watcher.db"
`

func WriteExample(path string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config already exists at %q (use --force to replace it)", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(example), 0o600)
}

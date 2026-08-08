package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Cyberlane/vlc-media-watcher/internal/credentials"
)

func TestWriteExampleAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	if err := WriteExample(path, false); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VLC.Endpoint != "http://127.0.0.1:8080" {
		t.Fatalf("endpoint = %q", cfg.VLC.Endpoint)
	}
	if cfg.Watch.EpisodeThreshold != 0.90 || cfg.Watch.MovieThreshold != 0.85 {
		t.Fatalf("unexpected thresholds: %#v", cfg.Watch)
	}
	if cfg.Sonarr.Endpoint != "http://127.0.0.1:8989" || cfg.Radarr.Endpoint != "http://127.0.0.1:7878" {
		t.Fatalf("unexpected media manager defaults: sonarr=%#v radarr=%#v", cfg.Sonarr, cfg.Radarr)
	}
	if err := WriteExample(path, false); err == nil {
		t.Fatal("expected existing config error")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestAniListProgressSyncRequiresAnEnabledAniListTracker(t *testing.T) {
	cfg := &Config{Trackers: map[string]TrackerConfig{"anilist": {SyncProgress: true}}}
	if err := validateTracker("anilist", cfg.Trackers["anilist"]); err == nil || !strings.Contains(err.Error(), "requires the tracker to be enabled") {
		t.Fatalf("disabled sync error = %v", err)
	}
	if err := validateTracker("myanimelist", TrackerConfig{Enabled: true, SyncProgress: true}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unsupported sync error = %v", err)
	}
}

func TestAniListRejectsClientIDFieldLabel(t *testing.T) {
	err := validateTracker("anilist", TrackerConfig{
		Enabled:               true,
		ClientID:              "VLC_MEDIA_WATCHER_ID",
		SecretSource:          "keyring",
		SecretReference:       "default/anilist-access-token",
		ClientSecretSource:    "keyring",
		ClientSecretReference: "default/anilist-client-secret",
	})
	if err == nil || !strings.Contains(err.Error(), "client-ID value") {
		t.Fatalf("validateTracker() error = %v", err)
	}
}

func TestLoadMigratesOAuthAccountTokensToKeyring(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `profile = "default"

[vlc]
endpoint = "http://127.0.0.1:8080"
secret_source = "environment"
password_env = "VLC_PASSWORD"

[watch]
episode_threshold = 0.9
movie_threshold = 0.85
poll_interval = "2s"

[storage]
path = ""

[trackers.anilist]
enabled = true
client_id = "client"
client_secret_source = "keyring"
secret_source = "1password"
secret_reference = "op://Private/not-an-access-token"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	tracker := cfg.Trackers["anilist"]
	if tracker.SecretSource != "keyring" || tracker.SecretReference != "default/anilist-access-token" {
		t.Fatalf("AniList token storage = %#v", tracker)
	}
}

func TestLoadAppliesMediaManagerDefaultsToLegacyConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	legacy := `profile = "legacy"

[vlc]
endpoint = "http://127.0.0.1:8080"
secret_source = "environment"
password_env = "VLC_MEDIA_WATCHER_VLC_PASSWORD"

[watch]
episode_threshold = 0.90
movie_threshold = 0.85
poll_interval = "2s"

[storage]
path = ""
`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	wantSonarr := MediaManagerConfig{
		Endpoint:        "http://127.0.0.1:8989",
		SecretSource:    "keyring",
		SecretReference: "legacy/sonarr-api-key",
		APIKeyEnv:       "VLC_MEDIA_WATCHER_SONARR_API_KEY",
	}
	wantRadarr := MediaManagerConfig{
		Endpoint:        "http://127.0.0.1:7878",
		SecretSource:    "keyring",
		SecretReference: "legacy/radarr-api-key",
		APIKeyEnv:       "VLC_MEDIA_WATCHER_RADARR_API_KEY",
	}
	if cfg.Sonarr != wantSonarr {
		t.Fatalf("Sonarr = %#v, want %#v", cfg.Sonarr, wantSonarr)
	}
	if cfg.Radarr != wantRadarr {
		t.Fatalf("Radarr = %#v, want %#v", cfg.Radarr, wantRadarr)
	}
}

func TestValidateMediaManagerOnlyWhenEnabled(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*MediaManagerConfig)
		wantError string
	}{
		{
			name: "valid keyring without path mapping",
		},
		{
			name: "valid environment with path mapping",
			configure: func(cfg *MediaManagerConfig) {
				cfg.SecretSource = "environment"
				cfg.SecretReference = ""
				cfg.LocalPathPrefix = "/Volumes/TV"
				cfg.RemotePathPrefix = "/tv"
			},
		},
		{
			name: "disabled ignores incomplete setup",
			configure: func(cfg *MediaManagerConfig) {
				cfg.UnmonitorAfterWatch = false
				cfg.UpdateMonitored = false
				cfg.Endpoint = "not a URL"
				cfg.SecretSource = "pending"
				cfg.SecretReference = ""
				cfg.APIKeyEnv = ""
				cfg.LocalPathPrefix = "/local-only"
			},
		},
		{
			name: "endpoint requires full URL",
			configure: func(cfg *MediaManagerConfig) {
				cfg.Endpoint = "127.0.0.1:8989"
			},
			wantError: "sonarr.endpoint must be a full http or https URL",
		},
		{
			name: "endpoint requires HTTP",
			configure: func(cfg *MediaManagerConfig) {
				cfg.Endpoint = "ftp://sonarr.example.test"
			},
			wantError: "sonarr.endpoint must be a full http or https URL",
		},
		{
			name: "endpoint rejects credentials",
			configure: func(cfg *MediaManagerConfig) {
				cfg.Endpoint = "http://user:secret@sonarr.example.test"
			},
			wantError: "sonarr.endpoint must not include credentials",
		},
		{
			name: "endpoint rejects query",
			configure: func(cfg *MediaManagerConfig) {
				cfg.Endpoint = "http://sonarr.example.test?apikey=secret"
			},
			wantError: "sonarr.endpoint must not include a query or fragment",
		},
		{
			name: "endpoint excludes API path",
			configure: func(cfg *MediaManagerConfig) {
				cfg.Endpoint = "http://sonarr.example.test/sonarr/api/v3/"
			},
			wantError: "sonarr.endpoint must not include /api/v3",
		},
		{
			name: "environment requires variable name",
			configure: func(cfg *MediaManagerConfig) {
				cfg.SecretSource = "environment"
				cfg.APIKeyEnv = ""
			},
			wantError: "sonarr.api_key_env is required",
		},
		{
			name: "keyring requires reference",
			configure: func(cfg *MediaManagerConfig) {
				cfg.SecretReference = ""
			},
			wantError: "sonarr.secret_reference is required",
		},
		{
			name: "1password requires reference",
			configure: func(cfg *MediaManagerConfig) {
				cfg.SecretSource = "1password"
				cfg.SecretReference = ""
			},
			wantError: "sonarr.secret_reference is required",
		},
		{
			name: "secret source is constrained",
			configure: func(cfg *MediaManagerConfig) {
				cfg.SecretSource = "file"
			},
			wantError: "sonarr.secret_source must be default, environment, keyring, or 1password",
		},
		{
			name: "local prefix requires remote prefix",
			configure: func(cfg *MediaManagerConfig) {
				cfg.LocalPathPrefix = "/Volumes/TV"
			},
			wantError: "sonarr.local_path_prefix and sonarr.remote_path_prefix must both be set or both be empty",
		},
		{
			name: "remote prefix requires local prefix",
			configure: func(cfg *MediaManagerConfig) {
				cfg.RemotePathPrefix = "/tv"
			},
			wantError: "sonarr.local_path_prefix and sonarr.remote_path_prefix must both be set or both be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfiguration()
			cfg.Sonarr = validMediaManagerConfiguration()
			if tt.configure != nil {
				tt.configure(&cfg.Sonarr)
			}

			err := Validate(cfg)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestDefaultPathUsesUserConfigDirectory(t *testing.T) {
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "vlc-media-watcher", "config.toml")
	if got := DefaultPath(); got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestSaveRoundTripsConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := &Config{
		Profile: "family",
		VLC: VLCConfig{
			Endpoint:        "http://127.0.0.1:8081",
			SecretSource:    "keyring",
			SecretReference: "family/vlc-http-password",
			PasswordEnv:     "VLC_PASSWORD",
		},
		Sonarr: MediaManagerConfig{
			UnmonitorAfterWatch: true,
			Endpoint:            "https://media.example.test/sonarr",
			SecretSource:        "keyring",
			SecretReference:     "family/sonarr-api-key",
			APIKeyEnv:           "SONARR_API_KEY",
			LocalPathPrefix:     "/Volumes/TV",
			RemotePathPrefix:    "/tv",
		},
		Radarr: MediaManagerConfig{
			UnmonitorAfterWatch: false,
			Endpoint:            "http://radarr.example.test:7878",
			SecretSource:        "environment",
			SecretReference:     "family/radarr-api-key",
			APIKeyEnv:           "RADARR_API_KEY",
			LocalPathPrefix:     "D:\\Movies",
			RemotePathPrefix:    "/movies",
		},
		Watch:   WatchConfig{EpisodeThreshold: .92, MovieThreshold: .87, PollInterval: 3 * time.Second},
		Storage: StorageConfig{Path: filepath.Join(t.TempDir(), "watcher.db")},
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "unmonitor_after_watch") || strings.Contains(string(content), "update_monitored") || strings.Contains(string(content), "monitored_after_watch") {
		t.Fatalf("saved configuration did not use the single unmonitor setting:\n%s", content)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	applyTrackerDefaults(cfg)
	if !reflect.DeepEqual(*loaded, *cfg) {
		t.Fatalf("loaded = %#v, want %#v", loaded, cfg)
	}
}

func TestLegacyMonitoredSettingsMigrateToUnmonitorAfterWatch(t *testing.T) {
	legacyEnabled := MediaManagerConfig{UpdateMonitored: true, MonitoredAfterWatch: false}
	migrateLegacyMediaManager(&legacyEnabled, false)
	if !legacyEnabled.UnmonitorAfterWatch {
		t.Fatal("legacy unmonitor configuration was not migrated")
	}

	legacyDisabled := MediaManagerConfig{UpdateMonitored: true, MonitoredAfterWatch: true}
	migrateLegacyMediaManager(&legacyDisabled, false)
	if legacyDisabled.UnmonitorAfterWatch {
		t.Fatal("legacy monitor-after-watch configuration should be disabled")
	}
}

func TestCredentialRegistryNormalizesExistingFieldsWithoutMutatingConfig(t *testing.T) {
	cfg := Config{
		VLC:    VLCConfig{SecretSource: "environment", PasswordEnv: "VLC_PASSWORD"},
		Sonarr: MediaManagerConfig{SecretSource: "1password", SecretReference: "op://redacted"},
		Radarr: MediaManagerConfig{SecretSource: "keyring", SecretReference: "default/radarr-api-key"},
		Trackers: map[string]TrackerConfig{
			"anilist": {
				SecretSource:          "keyring",
				SecretReference:       "default/anilist-access-token",
				ClientSecretSource:    "1password",
				ClientSecretReference: "op://redacted",
			},
		},
	}
	wantUnchanged := cfg
	wantUnchanged.Trackers = map[string]TrackerConfig{"anilist": cfg.Trackers["anilist"]}

	registry, err := cfg.CredentialRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, wantUnchanged) {
		t.Fatalf("CredentialRegistry() mutated config: %#v", cfg)
	}

	vlc, ok := registry.Entry(credentials.VLCPasswordID)
	if !ok || vlc.Binding.Provider != credentials.ProviderEnvironment || vlc.Binding.Environment != "VLC_PASSWORD" || !vlc.Requirement.Required {
		t.Fatalf("VLC registry entry = %#v", vlc)
	}
	clientSecret, ok := registry.Entry(credentials.TrackerClientSecretID("anilist"))
	if !ok || clientSecret.Requirement.Ownership != credentials.UserStored || clientSecret.Binding.Provider != credentials.Provider1Password {
		t.Fatalf("AniList client-secret registry entry = %#v", clientSecret)
	}
	accessToken, ok := registry.Entry(credentials.TrackerAccessTokenID("anilist"))
	if !ok || accessToken.Requirement.Ownership != credentials.AppWritten || accessToken.Binding.Provider != credentials.ProviderKeychain {
		t.Fatalf("AniList access-token registry entry = %#v", accessToken)
	}
	myAnimeList, ok := registry.Entry(credentials.TrackerAccessTokenID("myanimelist"))
	if !ok || myAnimeList.Requirement.Kind != credentials.RenewableToken || myAnimeList.Requirement.Ownership != credentials.AppWritten {
		t.Fatalf("MyAnimeList access-token registry entry = %#v", myAnimeList)
	}
}

func TestDefaultCredentialBindingResolvesWithoutRewritingItsOverride(t *testing.T) {
	cfg := validConfiguration()
	cfg.Credentials.DefaultProvider = DefaultProvider1Password
	cfg.VLC.SecretSource = ProviderDefault
	cfg.VLC.SecretReference = "op://Private/VLC/password"

	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	registry, err := cfg.CredentialRegistry()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := registry.Entry(credentials.VLCPasswordID)
	if !ok || entry.Binding.Provider != credentials.Provider1Password {
		t.Fatalf("effective VLC binding = %#v", entry)
	}
	if cfg.VLC.SecretSource != ProviderDefault {
		t.Fatalf("registry rewrote explicit default binding to %q", cfg.VLC.SecretSource)
	}
	if effective := cfg.EffectiveCredentialBindings(); effective.VLC.SecretSource != DefaultProvider1Password {
		t.Fatalf("effective VLC source = %q", effective.VLC.SecretSource)
	}
}

func TestOnePasswordBindingsRequireAnExplicitReference(t *testing.T) {
	cfg := validConfiguration()
	cfg.VLC.SecretSource = "1password"
	cfg.VLC.SecretReference = "vlc-password"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "explicit op:// reference") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func validConfiguration() *Config {
	return &Config{
		Profile: "test",
		VLC: VLCConfig{
			Endpoint:     "http://127.0.0.1:8080",
			SecretSource: "environment",
			PasswordEnv:  "VLC_MEDIA_WATCHER_VLC_PASSWORD",
		},
		Watch: WatchConfig{
			EpisodeThreshold: 0.90,
			MovieThreshold:   0.85,
			PollInterval:     2 * time.Second,
		},
	}
}

func validMediaManagerConfiguration() MediaManagerConfig {
	return MediaManagerConfig{
		UnmonitorAfterWatch: true,
		Endpoint:            "http://127.0.0.1:8989",
		SecretSource:        "keyring",
		SecretReference:     "test/sonarr-api-key",
		APIKeyEnv:           "VLC_MEDIA_WATCHER_SONARR_API_KEY",
	}
}

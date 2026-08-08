package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cyberlane/vlc-media-watcher/internal/arr"
	"github.com/Cyberlane/vlc-media-watcher/internal/config"
	"github.com/Cyberlane/vlc-media-watcher/internal/credentials"
	"github.com/Cyberlane/vlc-media-watcher/internal/reconcile"
	"github.com/Cyberlane/vlc-media-watcher/internal/store"
	"github.com/Cyberlane/vlc-media-watcher/internal/watch"
)

func TestRunSetupHonorsExplicitConfigPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom", "config.toml")
	var stdout bytes.Buffer

	if err := runSetup([]string{"--config", path}, &stdout); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("explicit config path was not created: %v", err)
	}
}

func TestRunVersionReportsBuildProvenance(t *testing.T) {
	originalVersion, originalCommit, originalDate, originalBuiltBy := version, commit, date, builtBy
	t.Cleanup(func() {
		version, commit, date, builtBy = originalVersion, originalCommit, originalDate, originalBuiltBy
	})
	version, commit, date, builtBy = "0.1.0", "abc123", "2026-08-01T00:00:00Z", "release-test"
	var stdout bytes.Buffer
	if err := run([]string{"--version"}, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); !strings.Contains(got, "vlc-media-watcher 0.1.0") || !strings.Contains(got, "commit abc123") || !strings.Contains(got, "release-test") {
		t.Fatalf("version output = %q", got)
	}
}

func TestRunEventsShowsAttentionThenUserFacingCompletedState(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "watcher.db")
	configPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := eventTestConfig(databasePath)
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.July, 24, 0, 0, 0, 0, time.UTC)
	completed := watch.Event{MediaPath: "/media/Show S01E04.mkv", Progress: .9, WatchedAt: base, Status: "already-unmonitored", Manager: "sonarr", SourceID: 7, SeasonNumber: 1, EpisodeNumbers: []int{4}}
	failed := watch.Event{MediaPath: "/media/Failed S01E05.mkv", Progress: .9, WatchedAt: base.Add(time.Minute), Status: "failed"}
	for _, event := range []watch.Event{completed, failed} {
		if _, err := db.RecordEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.UpsertTrackerSyncJob(store.TrackerSyncJob{EventPath: completed.MediaPath, Tracker: "anilist", TrackerID: "123", Status: "synced", Detail: "Progress advanced to episode 4."}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := runEvents([]string{"--config", configPath}, &stdout); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, want := range []string{"Needs attention", "Recent completed watches", "Needs attention", "Unmonitored", "AniList: Progress advanced to episode 4."} {
		if !strings.Contains(output, want) {
			t.Fatalf("events output missing %q: %q", want, output)
		}
	}
	if strings.Contains(output, "already-unmonitored") {
		t.Fatalf("events exposed internal status: %q", output)
	}
}

func TestRunEventsPruneIsDryRunFirstAndKeepsUnfinishedTrackerWork(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "watcher.db")
	configPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := eventTestConfig(databasePath)
	cfg.Trackers["anilist"] = config.TrackerConfig{Enabled: true, SyncProgress: true, ClientID: "test-client", SecretSource: "environment", AccessTokenEnv: "TEST_ANILIST_TOKEN", ClientSecretSource: "environment", ClientSecretEnv: "TEST_ANILIST_CLIENT_SECRET"}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	synced := watch.Event{MediaPath: "/media/Synced.mkv", Progress: .9, WatchedAt: old, Status: "unmonitored", Manager: "sonarr", SourceID: 7, SeasonNumber: 1, EpisodeNumbers: []int{4}}
	unsynced := watch.Event{MediaPath: "/media/Unsynced.mkv", Progress: .9, WatchedAt: old, Status: "unmonitored", Manager: "sonarr", SourceID: 7, SeasonNumber: 1, EpisodeNumbers: []int{5}}
	failure := watch.Event{MediaPath: "/media/Failure.mkv", Progress: .9, WatchedAt: old, Status: "failed"}
	for _, event := range []watch.Event{synced, unsynced, failure} {
		if _, err := db.RecordEvent(event); err != nil {
			t.Fatal(err)
		}
		if event.Manager != "" {
			if err := db.UpdateEventResolution(event); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.UpsertTrackerSyncJob(store.TrackerSyncJob{EventPath: synced.MediaPath, Tracker: "anilist", TrackerID: "123", Status: "synced"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := runEvents([]string{"prune", "--older-than", "90d", "--dry-run", "--config", configPath}, &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "1 fully reconciled event") {
		t.Fatalf("dry-run output = %q", stdout.String())
	}
	if err := runEvents([]string{"prune", "--older-than", "90d", "--apply", "--config", configPath}, io.Discard); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	events, err := db.RecentEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].MediaPath == synced.MediaPath || events[1].MediaPath == synced.MediaPath {
		t.Fatalf("events after prune = %#v", events)
	}
}

func eventTestConfig(databasePath string) *config.Config {
	return &config.Config{
		Profile:  "default",
		VLC:      config.VLCConfig{Endpoint: "http://127.0.0.1:8080", SecretSource: "environment", PasswordEnv: "TEST_PASSWORD"},
		Watch:    config.WatchConfig{EpisodeThreshold: .9, MovieThreshold: .85, PollInterval: time.Second},
		Storage:  config.StorageConfig{Path: databasePath},
		Trackers: map[string]config.TrackerConfig{},
	}
}

func TestRunMappingsConfirmRequiresResolvedSeasonUnit(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "watcher.db")
	configPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := &config.Config{
		Profile: "default",
		VLC:     config.VLCConfig{Endpoint: "http://127.0.0.1:8080", SecretSource: "environment", PasswordEnv: "TEST_PASSWORD"},
		Watch:   config.WatchConfig{EpisodeThreshold: .9, MovieThreshold: .85, PollInterval: time.Second},
		Storage: config.StorageConfig{Path: databasePath},
	}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	units, err := db.UpsertIdentity(arr.MediaIdentity{Manager: arr.ManagerSonarr, SourceID: 42, Kind: "series", Title: "Example", SeasonNumber: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = runMappings([]string{"confirm", "anilist", "--manager", "sonarr", "--source-id", "42", "--season", "2", "--id", "999", "--title", "Example Season 2", "--config", configPath}, &stdout)
	if err != nil || !strings.Contains(stdout.String(), "No tracker watch state was changed") {
		t.Fatalf("confirm mapping error=%v output=%q", err, stdout.String())
	}
	db, err = store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mappings, err := db.MappingsForUnit(units[1].ID)
	if err != nil || len(mappings) != 1 || mappings[0].Tracker != "anilist" || mappings[0].TrackerID != "999" {
		t.Fatalf("mappings=%#v error=%v", mappings, err)
	}
}

func TestRunIntegrationsTestsWithoutWriting(t *testing.T) {
	const apiKey = "sonarr-test-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sonarr/api/v3/system/status" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Api-Key"); got != apiKey {
			t.Fatalf("X-Api-Key = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"appName":"Sonarr","instanceName":"Living Room","version":"4.0.15","isLinux":true}`))
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := &config.Config{
		Profile: "default",
		VLC: config.VLCConfig{
			Endpoint:     "http://127.0.0.1:8080",
			SecretSource: "environment",
			PasswordEnv:  "VLC_MEDIA_WATCHER_TEST_PASSWORD",
		},
		Sonarr: config.MediaManagerConfig{
			Endpoint:     server.URL + "/sonarr",
			SecretSource: "environment",
			APIKeyEnv:    "VLC_MEDIA_WATCHER_TEST_SONARR_API_KEY",
		},
		Watch: config.WatchConfig{
			EpisodeThreshold: .9,
			MovieThreshold:   .85,
			PollInterval:     2 * time.Second,
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VLC_MEDIA_WATCHER_TEST_SONARR_API_KEY", apiKey)

	var stdout bytes.Buffer
	if err := runIntegrations([]string{"test", "sonarr", "--config", path}, &stdout); err != nil {
		t.Fatal(err)
	}
	if output := stdout.String(); !strings.Contains(output, "Sonarr connection OK") || !strings.Contains(output, "No library changes were made") || strings.Contains(output, apiKey) {
		t.Fatalf("output = %q", output)
	}
}

func TestRunWatchPersistsEventBeforeRadarrWrite(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "watcher.db")
	var persistedBeforeWrite atomic.Bool
	var writes atomic.Int32

	vlcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/requests/status.json" {
			t.Errorf("unexpected VLC path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"state":"playing","position":0.9,"length":7200,"information":{"category":{"meta":{"uri":"file:///media/Movie.mkv"}}}}`))
	}))
	defer vlcServer.Close()

	radarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Key"); got != "radarr-test-key" {
			t.Errorf("X-Api-Key = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/system/status":
			_, _ = w.Write([]byte(`{"appName":"Radarr","instanceName":"Movies","version":"6.1.1","isLinux":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie":
			_, _ = w.Write([]byte(`[{"id":12,"title":"Movie","path":"/media/Movie","monitored":true,"movieFile":{"id":33,"movieId":12,"path":"/media/Movie.mkv"}}]`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/movie/editor":
			db, err := store.Open(databasePath)
			if err != nil {
				t.Errorf("open event store during Radarr write: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			events, err := db.RecentEvents(10)
			_ = db.Close()
			if err != nil || len(events) != 1 || events[0].MediaPath != "/media/Movie.mkv" || events[0].Status != "pending" {
				t.Errorf("events before Radarr write = %#v, %v", events, err)
			} else {
				persistedBeforeWrite.Store(true)
			}
			writes.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected Radarr request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer radarrServer.Close()

	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := &config.Config{
		Profile: "default",
		VLC: config.VLCConfig{
			Endpoint:     vlcServer.URL,
			SecretSource: "environment",
			PasswordEnv:  "VLC_MEDIA_WATCHER_TEST_PASSWORD",
		},
		Radarr: config.MediaManagerConfig{
			UpdateMonitored:     true,
			MonitoredAfterWatch: false,
			Endpoint:            radarrServer.URL,
			SecretSource:        "environment",
			APIKeyEnv:           "VLC_MEDIA_WATCHER_TEST_RADARR_API_KEY",
		},
		Watch: config.WatchConfig{
			EpisodeThreshold: .9,
			MovieThreshold:   .85,
			PollInterval:     2 * time.Second,
		},
		Storage: config.StorageConfig{Path: databasePath},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VLC_MEDIA_WATCHER_TEST_PASSWORD", "vlc-password")
	t.Setenv("VLC_MEDIA_WATCHER_TEST_RADARR_API_KEY", "radarr-test-key")

	var stdout bytes.Buffer
	if err := runWatch([]string{"--once", "--config", path}, &stdout); err != nil {
		t.Fatal(err)
	}
	if !persistedBeforeWrite.Load() || writes.Load() != 1 {
		t.Fatalf("persistedBeforeWrite=%t writes=%d", persistedBeforeWrite.Load(), writes.Load())
	}
	db, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	events, err := db.RecentEvents(10)
	_ = db.Close()
	if err != nil || len(events) != 1 || events[0].Status != "unmonitored" {
		t.Fatalf("events after Radarr write = %#v, %v", events, err)
	}
	if output := stdout.String(); !strings.Contains(output, "Recorded local watched event") || !strings.Contains(output, "set to unmonitored") {
		t.Fatalf("output = %q", output)
	}
}

func TestRunWatchKeepsDisabledManagersEntirelyLocal(t *testing.T) {
	var managerRequests atomic.Int32
	managerServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		managerRequests.Add(1)
	}))
	defer managerServer.Close()
	vlcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"state":"playing","position":0.9,"length":1200,"information":{"category":{"meta":{"uri":"file:///media/Show.S01E01.mkv"}}}}`))
	}))
	defer vlcServer.Close()

	databasePath := filepath.Join(t.TempDir(), "watcher.db")
	configPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := &config.Config{
		Profile: "default",
		VLC: config.VLCConfig{
			Endpoint:     vlcServer.URL,
			SecretSource: "environment",
			PasswordEnv:  "VLC_MEDIA_WATCHER_TEST_PASSWORD",
		},
		Sonarr: config.MediaManagerConfig{
			UpdateMonitored: false,
			Endpoint:        managerServer.URL,
			SecretSource:    "environment",
			APIKeyEnv:       "UNSET_SONARR_API_KEY",
		},
		Radarr: config.MediaManagerConfig{
			UpdateMonitored: false,
			Endpoint:        managerServer.URL,
			SecretSource:    "environment",
			APIKeyEnv:       "UNSET_RADARR_API_KEY",
		},
		Watch: config.WatchConfig{
			EpisodeThreshold: .9,
			MovieThreshold:   .85,
			PollInterval:     2 * time.Second,
		},
		Storage: config.StorageConfig{Path: databasePath},
	}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VLC_MEDIA_WATCHER_TEST_PASSWORD", "vlc-password")

	if err := runWatch([]string{"--once", "--config", configPath}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if requests := managerRequests.Load(); requests != 0 {
		t.Fatalf("disabled managers made %d requests", requests)
	}
	db, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	events, err := db.RecentEvents(10)
	_ = db.Close()
	if err != nil || len(events) != 1 || events[0].Status != "local" {
		t.Fatalf("events = %#v, %v", events, err)
	}
}

func TestContinuousWatchIsSingleInstancePrivateAndGraceful(t *testing.T) {
	polled := make(chan struct{}, 4)
	vlcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case polled <- struct{}{}:
		default:
		}
		_, _ = w.Write([]byte(`{"state":"playing","position":0.1,"length":1200,"information":{"category":{"meta":{"uri":"file:///Users/example/Private/Secret.Show.S01E01.mkv"}}}}`))
	}))
	defer vlcServer.Close()

	databasePath := filepath.Join(t.TempDir(), "watcher.db")
	configPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := &config.Config{
		Profile: "service-test",
		VLC: config.VLCConfig{
			Endpoint:     vlcServer.URL,
			SecretSource: "environment",
			PasswordEnv:  "VLC_MEDIA_WATCHER_TEST_PASSWORD",
		},
		Watch:   config.WatchConfig{EpisodeThreshold: .9, MovieThreshold: .85, PollInterval: 10 * time.Millisecond},
		Storage: config.StorageConfig{Path: databasePath},
	}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VLC_MEDIA_WATCHER_TEST_PASSWORD", "vlc-password")

	ctx, cancel := context.WithCancel(context.Background())
	var output bytes.Buffer
	firstDone := make(chan error, 1)
	go func() { firstDone <- runWatchContext(ctx, []string{"--config", configPath}, &output) }()
	select {
	case <-polled:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("continuous watcher did not poll VLC")
	}

	secondErr := runWatchContext(context.Background(), []string{"--config", configPath}, io.Discard)
	if secondErr == nil || !strings.Contains(secondErr.Error(), "another continuous watcher") {
		cancel()
		t.Fatalf("second watcher error = %v", secondErr)
	}
	cancel()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("graceful watcher stop = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("continuous watcher did not stop after cancellation")
	}

	logOutput := output.String()
	if !strings.Contains(logOutput, "INFO Watching VLC") || !strings.Contains(logOutput, "Secret.Show.S01E01.mkv") || strings.Contains(logOutput, "/Users/example/Private") || !strings.Contains(logOutput, "Watcher stopped") {
		t.Fatalf("service log output = %q", logOutput)
	}

	db, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if acquired, err := db.AcquireWatcherLease(continuousWatcherLeaseName, "after-stop", time.Now(), continuousWatcherLeaseTTL); err != nil || !acquired {
		t.Fatalf("released watcher lease = %t, %v", acquired, err)
	}
}

func TestContinuousWatchKeepsItsLeaseWhenVLCCredentialNeedsRepair(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "watcher.db")
	configPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := &config.Config{
		Profile: "credential-retry-test",
		VLC: config.VLCConfig{
			Endpoint:        "http://127.0.0.1:8080",
			SecretSource:    "1password",
			SecretReference: "op://private/vlc/password",
		},
		Watch:   config.WatchConfig{EpisodeThreshold: .9, MovieThreshold: .85, PollInterval: time.Second},
		Storage: config.StorageConfig{Path: databasePath},
	}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	firstAttempt := make(chan struct{})
	dependencies := defaultWatchDependencies()
	dependencies.credentialRetryDelays = []time.Duration{100 * time.Millisecond}
	dependencies.resolveVLC = func(context.Context, config.VLCConfig) credentials.Resolution {
		select {
		case <-firstAttempt:
		default:
			close(firstAttempt)
		}
		return credentials.Resolution{State: credentials.StateProviderUnavailable, SafeMessage: "op://private/vlc/password"}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runWatchContextWithDependencies(ctx, []string{"--config", configPath}, &output, dependencies)
	}()
	select {
	case <-firstAttempt:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not attempt the VLC credential")
	}

	secondErr := runWatchContext(context.Background(), []string{"--config", configPath}, io.Discard)
	if secondErr == nil || !strings.Contains(secondErr.Error(), "another continuous watcher") {
		t.Fatalf("second watcher error = %v", secondErr)
	}
	select {
	case err := <-done:
		t.Fatalf("credential-degraded watcher exited early: %v", err)
	default:
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("credential-degraded watcher stop = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("credential-degraded watcher did not stop after cancellation")
	}
	logOutput := output.String()
	if !strings.Contains(logOutput, "VLC credential provider is unavailable") || !strings.Contains(logOutput, "Watching is paused until the VLC credential is repaired") || strings.Contains(logOutput, "op://private") {
		t.Fatalf("credential-degraded log = %q", logOutput)
	}

	db, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if acquired, err := db.AcquireWatcherLease(continuousWatcherLeaseName, "after-degraded-stop", time.Now(), continuousWatcherLeaseTTL); err != nil || !acquired {
		t.Fatalf("released degraded watcher lease = %t, %v", acquired, err)
	}
}

func TestContinuousWatchRecoversVLCCredentialOnceWithoutLeakingProviderMetadata(t *testing.T) {
	polled := make(chan struct{}, 1)
	vlcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case polled <- struct{}{}:
		default:
		}
		_, _ = w.Write([]byte(`{"state":"paused","position":0,"length":0}`))
	}))
	defer vlcServer.Close()

	databasePath := filepath.Join(t.TempDir(), "watcher.db")
	configPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := &config.Config{
		Profile: "credential-recovery-test",
		VLC: config.VLCConfig{
			Endpoint:     vlcServer.URL,
			SecretSource: "environment",
			PasswordEnv:  "VLC_MEDIA_WATCHER_TEST_PASSWORD",
		},
		Watch:   config.WatchConfig{EpisodeThreshold: .9, MovieThreshold: .85, PollInterval: 10 * time.Millisecond},
		Storage: config.StorageConfig{Path: databasePath},
	}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	dependencies := defaultWatchDependencies()
	dependencies.credentialRetryDelays = []time.Duration{time.Millisecond}
	dependencies.resolveVLC = func(context.Context, config.VLCConfig) credentials.Resolution {
		if calls.Add(1) == 1 {
			return credentials.Resolution{State: credentials.StateNeedsUserAction, SafeMessage: "op://private/vlc/password"}
		}
		return credentials.Resolution{State: credentials.StateReady, Value: "test-password", SafeMessage: "Ready"}
	}

	ctx, cancel := context.WithCancel(context.Background())
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runWatchContextWithDependencies(ctx, []string{"--config", configPath}, &output, dependencies)
	}()
	select {
	case <-polled:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("watcher did not resume polling after credential recovery")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("credential-recovered watcher stop = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("credential-recovered watcher did not stop after cancellation")
	}

	logOutput := output.String()
	if strings.Count(logOutput, "Watching is paused until the VLC credential is repaired") != 1 || !strings.Contains(logOutput, "VLC credential repaired; resuming VLC observation.") || strings.Contains(logOutput, "op://private") {
		t.Fatalf("credential-recovery log = %q", logOutput)
	}
}

func TestVLCCredentialResolutionUsesTheApprovedBackgroundTimeoutAndRetrySchedule(t *testing.T) {
	dependencies := defaultWatchDependencies()
	remaining := make(chan time.Duration, 1)
	dependencies.resolveVLC = func(ctx context.Context, _ config.VLCConfig) credentials.Resolution {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("credential resolver context has no deadline")
		}
		remaining <- time.Until(deadline)
		return credentials.Resolution{State: credentials.StateProviderUnavailable, SafeMessage: "VLC credential provider is unavailable."}
	}
	logger := newWatchServiceLogger(io.Discard, false)
	_, err := resolveVLCCredential(context.Background(), config.VLCConfig{}, true, logger, nil, dependencies)
	if err == nil || strings.Contains(err.Error(), "op://") {
		t.Fatalf("one-shot credential resolution error = %v", err)
	}
	if got := <-remaining; got > vlcCredentialResolveTimeout || got < 9*time.Second {
		t.Fatalf("credential resolution timeout remaining = %s, want approximately %s", got, vlcCredentialResolveTimeout)
	}
	for attempt, want := range []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		if got := vlcCredentialRetryDelay(attempt, defaultVLCCredentialRetryDelays); got != want {
			t.Fatalf("retry delay %d = %s, want %s", attempt, got, want)
		}
	}
}

func TestWatchServiceLoggerSuppressesRepeatedWarningsAndReportsRecovery(t *testing.T) {
	var output bytes.Buffer
	logger := newWatchServiceLogger(&output, false)
	now := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	logger.warning(now, "VLC is unavailable")
	logger.warning(now.Add(time.Second), "VLC is unavailable")
	logger.warning(now.Add(2*time.Second), "VLC is unavailable")
	logger.pollRecovered(now.Add(3*time.Second), "VLC is unavailable")
	got := output.String()
	if strings.Count(got, "WARN VLC is unavailable") != 1 || !strings.Contains(got, "recovered after 2 repeated warning(s)") {
		t.Fatalf("warning log = %q", got)
	}
	if media := logger.media(`C:\\Private\\Shows\\Example.S01E01.mkv`); media != "Example.S01E01.mkv" {
		t.Fatalf("redacted media = %q", media)
	}
}

func TestSecureContinuousOutputProtectsRegularLogFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission bits")
	}
	logPath := filepath.Join(t.TempDir(), "watcher.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	if err := os.Chmod(logPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := secureContinuousOutput(logFile); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("log mode = %o, want 600", got)
	}
	if err := secureContinuousOutput(&bytes.Buffer{}); err != nil {
		t.Fatalf("non-file output: %v", err)
	}
}

func TestReconcileEventCreatesOnlyProvisionalLocalTrackerIdentity(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "watcher.db")
	cfg := eventTestConfig(databasePath)
	cfg.Trackers["trakt"] = config.TrackerConfig{Enabled: true}
	db, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	event := watch.Event{MediaPath: "/media/Example.Show.S02E03.mkv", Progress: .95, WatchedAt: time.Now().UTC(), Status: "pending"}
	if created, err := db.RecordEvent(event); err != nil || !created {
		t.Fatalf("RecordEvent() = %t, %v", created, err)
	}

	outcome, err := reconcileEvent(db, cfg, reconcile.New(context.Background(), cfg), event)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != reconcile.StatusLocal || !strings.Contains(strings.Join(outcome.Messages, " "), "provisional local title") {
		t.Fatalf("outcome = %#v", outcome)
	}
	events, err := db.RecentEvents(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Manager != "local" || events[0].SourceID <= 0 || events[0].SeasonNumber != 2 || !reflect.DeepEqual(events[0].EpisodeNumbers, []int{3}) {
		t.Fatalf("stored event = %#v", events)
	}
	units, err := db.RecentMediaUnits(10)
	if err != nil || len(units) != 2 {
		t.Fatalf("units = %#v, %v", units, err)
	}
	mappings, err := db.MappingsForUnit(units[0].ID)
	if err != nil || len(mappings) != 0 {
		t.Fatalf("mappings = %#v, %v", mappings, err)
	}
}

func TestSecretTargetSelectsMediaManagerAPIKeys(t *testing.T) {
	cfg := &config.Config{
		VLC:    config.VLCConfig{SecretSource: "keyring", SecretReference: "vlc-ref"},
		Sonarr: config.MediaManagerConfig{SecretSource: "environment", SecretReference: "sonarr-ref"},
		Radarr: config.MediaManagerConfig{SecretSource: "1password", SecretReference: "radarr-ref"},
		Trackers: map[string]config.TrackerConfig{
			"anilist": {SecretSource: "keyring", SecretReference: "anilist-token-ref", ClientSecretSource: "environment", ClientSecretReference: "anilist-client-secret-ref"},
		},
	}
	tests := []struct {
		target, label, source, reference string
	}{
		{"vlc", "VLC password", "keyring", "vlc-ref"},
		{"sonarr", "Sonarr API key", "environment", "sonarr-ref"},
		{"radarr", "Radarr API key", "1password", "radarr-ref"},
		{"anilist", "AniList access token", "keyring", "anilist-token-ref"},
		{"anilist-client-secret", "AniList OAuth client secret", "environment", "anilist-client-secret-ref"},
	}
	for _, test := range tests {
		label, source, reference, err := secretTarget(cfg, test.target)
		if err != nil || label != test.label || source != test.source || reference != test.reference {
			t.Fatalf("secretTarget(%q) = %q, %q, %q, %v", test.target, label, source, reference, err)
		}
	}
	if _, _, _, err := secretTarget(cfg, "unknown"); err == nil {
		t.Fatal("expected unknown target error")
	}
}

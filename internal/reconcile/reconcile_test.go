package reconcile

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Cyberlane/vlc-media-watcher/internal/arr"
	"github.com/Cyberlane/vlc-media-watcher/internal/config"
	"github.com/Cyberlane/vlc-media-watcher/internal/credentials"
)

func TestDisabledManagersMakeNoCalls(t *testing.T) {
	environment := newFakeEnvironment()
	reconciler := newReconciler(context.Background(), testConfig(), environment.dependencies())

	if reconciler.Active() {
		t.Fatal("Active() = true with both managers disabled")
	}
	if problems := reconciler.Problems(); len(problems) != 0 {
		t.Fatalf("Problems() = %v", problems)
	}
	outcome := reconciler.Process(context.Background(), "/media/example.mkv")
	if outcome.Status != StatusLocal {
		t.Fatalf("Process() status = %q, want %q", outcome.Status, StatusLocal)
	}
	if len(environment.resolveCalls) != 0 || len(environment.factoryCalls) != 0 {
		t.Fatalf("disabled managers made initialization calls: resolves=%v factories=%v", environment.resolveCalls, environment.factoryCalls)
	}
	assertClientCalls(t, environment.clients[arr.ManagerSonarr], 0, 0, 0)
	assertClientCalls(t, environment.clients[arr.ManagerRadarr], 0, 0, 0)
}

func TestInitializationFailureIsStoredAndFailsClosed(t *testing.T) {
	cfg := testConfig()
	cfg.Sonarr.UpdateMonitored = true
	environment := newFakeEnvironment()
	environment.resolveErrors["Sonarr API key"] = errors.New("credential lookup failed")

	reconciler := newReconciler(context.Background(), cfg, environment.dependencies())
	if !reconciler.Active() {
		t.Fatal("Active() = false for an enabled manager with an initialization problem")
	}
	problems := reconciler.Problems()
	if len(problems) != 1 || !strings.Contains(problems[0], "Sonarr") || !strings.Contains(problems[0], "credential lookup failed") {
		t.Fatalf("Problems() = %v", problems)
	}

	outcome := reconciler.Process(context.Background(), "/media/example.mkv")
	if outcome.Status != StatusFailed {
		t.Fatalf("Process() status = %q, want %q", outcome.Status, StatusFailed)
	}
	if strings.Contains(strings.Join(outcome.Messages, " "), "credential lookup failed") {
		t.Fatalf("Process() exposed an initialization error: %v", outcome.Messages)
	}
	if len(environment.factoryCalls) != 0 {
		t.Fatalf("factory calls = %v, want none", environment.factoryCalls)
	}
	assertClientCalls(t, environment.clients[arr.ManagerSonarr], 0, 0, 0)
}

func TestCredentialFailurePausesOnlyTheAffectedManager(t *testing.T) {
	cfg := testConfig()
	cfg.Sonarr.UpdateMonitored = true
	cfg.Radarr.UpdateMonitored = true
	environment := newFakeEnvironment()
	environment.clients[arr.ManagerRadarr].match = exactMatch(arr.ManagerRadarr, true)
	environment.clients[arr.ManagerRadarr].found = true
	dependencies := environment.dependencies()
	dependencies.resolveManagerCredential = func(_ context.Context, manager arr.Manager, _ config.MediaManagerConfig) credentials.Resolution {
		if manager == arr.ManagerSonarr {
			return credentials.Resolution{State: credentials.StateProviderUnavailable, SafeMessage: "op://private/sonarr/api-key"}
		}
		return credentials.Resolution{State: credentials.StateReady, Value: "radarr-api-key", SafeMessage: "Ready"}
	}

	reconciler := newReconciler(context.Background(), cfg, dependencies)
	problems := reconciler.Problems()
	if len(problems) != 1 || !strings.Contains(problems[0], "Sonarr API-key provider is unavailable") || strings.Contains(problems[0], "op://private") {
		t.Fatalf("Problems() = %v", problems)
	}

	outcome := reconciler.Process(context.Background(), "/media/example.mkv")
	if outcome.Status != StatusUnmonitored {
		t.Fatalf("Process() status = %q, want %q (%v)", outcome.Status, StatusUnmonitored, outcome.Messages)
	}
	assertClientCalls(t, environment.clients[arr.ManagerSonarr], 0, 0, 0)
	assertClientCalls(t, environment.clients[arr.ManagerRadarr], 0, 1, 1)
}

func TestNoMatchesChecksEveryActiveManager(t *testing.T) {
	cfg := testConfig()
	cfg.Sonarr.UpdateMonitored = true
	cfg.Sonarr.LocalPathPrefix = "/Volumes/TV"
	cfg.Sonarr.RemotePathPrefix = "/tv"
	cfg.Radarr.UpdateMonitored = true
	environment := newFakeEnvironment()

	reconciler := newReconciler(context.Background(), cfg, environment.dependencies())
	outcome := reconciler.Process(context.Background(), "/Volumes/TV/Show/episode.mkv")
	if outcome.Status != StatusUnmatched {
		t.Fatalf("Process() status = %q, want %q", outcome.Status, StatusUnmatched)
	}
	assertClientCalls(t, environment.clients[arr.ManagerSonarr], 0, 1, 0)
	assertClientCalls(t, environment.clients[arr.ManagerRadarr], 0, 1, 0)
	mapping := environment.clients[arr.ManagerSonarr].findMappings[0]
	if mapping == nil || mapping.LocalPrefix != "/Volumes/TV" || mapping.RemotePrefix != "/tv" {
		t.Fatalf("Sonarr mapping = %#v", mapping)
	}
	if environment.clients[arr.ManagerRadarr].findMappings[0] != nil {
		t.Fatalf("Radarr mapping = %#v, want nil", environment.clients[arr.ManagerRadarr].findMappings[0])
	}
}

func TestResolveForManagerDoesNotRequireOtherEnabledManagers(t *testing.T) {
	cfg := testConfig()
	cfg.Sonarr.MetadataLookup = true
	cfg.Radarr.MetadataLookup = true
	environment := newFakeEnvironment()
	environment.clients[arr.ManagerSonarr].match = exactMatch(arr.ManagerSonarr, true)
	environment.clients[arr.ManagerSonarr].found = true
	environment.clients[arr.ManagerRadarr].findErr = errors.New("Radarr is unavailable")

	reconciler := newReconciler(context.Background(), cfg, environment.dependencies())
	match, err := reconciler.ResolveForManager(context.Background(), "/media/episode.mkv", arr.ManagerSonarr)
	if err != nil {
		t.Fatalf("ResolveForManager() error = %v", err)
	}
	if match.Manager != arr.ManagerSonarr {
		t.Fatalf("ResolveForManager() match manager = %q", match.Manager)
	}
	assertClientCalls(t, environment.clients[arr.ManagerSonarr], 0, 1, 0)
	assertClientCalls(t, environment.clients[arr.ManagerRadarr], 0, 0, 0)
}

func TestNewReconcilerForManagerDoesNotResolveUnrelatedCredentials(t *testing.T) {
	cfg := testConfig()
	cfg.Sonarr.MetadataLookup = true
	cfg.Radarr.MetadataLookup = true
	environment := newFakeEnvironment()

	reconciler := newReconcilerForManager(context.Background(), cfg, environment.dependencies(), arr.ManagerSonarr)
	if problems := reconciler.Problems(); len(problems) != 0 {
		t.Fatalf("Problems() = %v", problems)
	}
	if len(environment.resolveCalls) != 1 || environment.resolveCalls[0].label != "Sonarr API key" {
		t.Fatalf("credential resolution = %#v", environment.resolveCalls)
	}
	if len(environment.factoryCalls) != 1 || environment.factoryCalls[0].manager != arr.ManagerSonarr {
		t.Fatalf("client factories = %#v", environment.factoryCalls)
	}
}

func TestCrossManagerMatchesFailClosed(t *testing.T) {
	cfg := testConfig()
	cfg.Sonarr.UpdateMonitored = true
	cfg.Radarr.UpdateMonitored = true
	environment := newFakeEnvironment()
	environment.clients[arr.ManagerSonarr].match = exactMatch(arr.ManagerSonarr, true)
	environment.clients[arr.ManagerSonarr].found = true
	environment.clients[arr.ManagerRadarr].match = exactMatch(arr.ManagerRadarr, true)
	environment.clients[arr.ManagerRadarr].found = true

	reconciler := newReconciler(context.Background(), cfg, environment.dependencies())
	outcome := reconciler.Process(context.Background(), "/media/shared-file.mkv")
	if outcome.Status != StatusFailed {
		t.Fatalf("Process() status = %q, want %q", outcome.Status, StatusFailed)
	}
	assertClientCalls(t, environment.clients[arr.ManagerSonarr], 0, 1, 0)
	assertClientCalls(t, environment.clients[arr.ManagerRadarr], 0, 1, 0)
}

func TestAmbiguousLookupStillChecksEveryManager(t *testing.T) {
	cfg := testConfig()
	cfg.Sonarr.UpdateMonitored = true
	cfg.Radarr.UpdateMonitored = true
	environment := newFakeEnvironment()
	environment.clients[arr.ManagerSonarr].findErr = fmt.Errorf("find episodes: %w", arr.ErrAmbiguousMatch)

	reconciler := newReconciler(context.Background(), cfg, environment.dependencies())
	outcome := reconciler.Process(context.Background(), "/media/example.mkv")
	if outcome.Status != StatusFailed {
		t.Fatalf("Process() status = %q, want %q", outcome.Status, StatusFailed)
	}
	if !strings.Contains(strings.Join(outcome.Messages, " "), "more than one exact library match") {
		t.Fatalf("Process() messages = %v", outcome.Messages)
	}
	assertClientCalls(t, environment.clients[arr.ManagerSonarr], 0, 1, 0)
	assertClientCalls(t, environment.clients[arr.ManagerRadarr], 0, 1, 0)
}

func TestAnyLookupErrorPreventsAnOtherwiseValidWrite(t *testing.T) {
	cfg := testConfig()
	cfg.Sonarr.UpdateMonitored = true
	cfg.Radarr.UpdateMonitored = true
	environment := newFakeEnvironment()
	environment.clients[arr.ManagerSonarr].match = exactMatch(arr.ManagerSonarr, true)
	environment.clients[arr.ManagerSonarr].found = true
	environment.clients[arr.ManagerRadarr].findErr = errors.New("response included api-key-value")

	reconciler := newReconciler(context.Background(), cfg, environment.dependencies())
	outcome := reconciler.Process(context.Background(), "/media/example.mkv")
	if outcome.Status != StatusFailed {
		t.Fatalf("Process() status = %q, want %q", outcome.Status, StatusFailed)
	}
	if strings.Contains(strings.Join(outcome.Messages, " "), "api-key-value") {
		t.Fatalf("Process() exposed a lookup error: %v", outcome.Messages)
	}
	assertClientCalls(t, environment.clients[arr.ManagerSonarr], 0, 1, 0)
	assertClientCalls(t, environment.clients[arr.ManagerRadarr], 0, 1, 0)
}

func TestAlreadyDesiredStateIsANoOp(t *testing.T) {
	t.Run("unmonitored", func(t *testing.T) {
		cfg := testConfig()
		cfg.Sonarr.UnmonitorAfterWatch = true
		environment := newFakeEnvironment()
		environment.clients[arr.ManagerSonarr].match = arr.Match{
			Manager: arr.ManagerSonarr,
			Targets: []arr.Target{
				{ID: 11, Monitored: false},
				{ID: 12, Monitored: false},
			},
		}
		environment.clients[arr.ManagerSonarr].found = true

		reconciler := newReconciler(context.Background(), cfg, environment.dependencies())
		outcome := reconciler.Process(context.Background(), "/media/example.mkv")
		if outcome.Status != StatusAlreadyUnmonitored {
			t.Fatalf("Process() status = %q, want %q", outcome.Status, StatusAlreadyUnmonitored)
		}
		assertClientCalls(t, environment.clients[arr.ManagerSonarr], 0, 1, 0)
	})
}

func TestSuccessfulWriteReportsDesiredState(t *testing.T) {
	tests := []struct {
		name    string
		manager arr.Manager
	}{
		{name: "unmonitor Sonarr", manager: arr.ManagerSonarr},
		{name: "unmonitor Radarr", manager: arr.ManagerRadarr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			managerConfig := configuredManager(cfg, tt.manager)
			managerConfig.UnmonitorAfterWatch = true
			setConfiguredManager(cfg, tt.manager, managerConfig)
			environment := newFakeEnvironment()
			environment.clients[tt.manager].match = exactMatch(tt.manager, true)
			environment.clients[tt.manager].found = true

			reconciler := newReconciler(context.Background(), cfg, environment.dependencies())
			outcome := reconciler.Process(context.Background(), "/media/example.mkv")
			if outcome.Status != StatusUnmonitored {
				t.Fatalf("Process() status = %q, want %q", outcome.Status, StatusUnmonitored)
			}
			client := environment.clients[tt.manager]
			assertClientCalls(t, client, 0, 1, 1)
			if len(client.setDesired) != 1 || client.setDesired[0] != false {
				t.Fatalf("SetMonitored desired values = %v, want [false]", client.setDesired)
			}
		})
	}
}

func TestWriteFailureIsUserSafe(t *testing.T) {
	cfg := testConfig()
	cfg.Radarr.UpdateMonitored = true
	environment := newFakeEnvironment()
	environment.clients[arr.ManagerRadarr].match = exactMatch(arr.ManagerRadarr, true)
	environment.clients[arr.ManagerRadarr].found = true
	environment.clients[arr.ManagerRadarr].setErr = errors.New("remote echoed api-key-value")

	reconciler := newReconciler(context.Background(), cfg, environment.dependencies())
	outcome := reconciler.Process(context.Background(), "/media/example.mkv")
	if outcome.Status != StatusFailed {
		t.Fatalf("Process() status = %q, want %q", outcome.Status, StatusFailed)
	}
	if strings.Contains(strings.Join(outcome.Messages, " "), "api-key-value") {
		t.Fatalf("Process() exposed a write error: %v", outcome.Messages)
	}
	assertClientCalls(t, environment.clients[arr.ManagerRadarr], 0, 1, 1)
}

func TestInvalidOrCrossManagerMatchDoesNotWrite(t *testing.T) {
	cfg := testConfig()
	cfg.Sonarr.UpdateMonitored = true
	environment := newFakeEnvironment()
	environment.clients[arr.ManagerSonarr].match = exactMatch(arr.ManagerRadarr, true)
	environment.clients[arr.ManagerSonarr].found = true

	reconciler := newReconciler(context.Background(), cfg, environment.dependencies())
	outcome := reconciler.Process(context.Background(), "/media/example.mkv")
	if outcome.Status != StatusFailed {
		t.Fatalf("Process() status = %q, want %q", outcome.Status, StatusFailed)
	}
	assertClientCalls(t, environment.clients[arr.ManagerSonarr], 0, 1, 0)
}

func TestConnectionTestUsesRequestedDisabledService(t *testing.T) {
	cfg := testConfig()
	cfg.Sonarr.Endpoint = "not a URL"
	cfg.Radarr.UpdateMonitored = false
	cfg.Radarr.Endpoint = "https://media.example.test/radarr"
	environment := newFakeEnvironment()
	environment.clients[arr.ManagerRadarr].instance = arr.Instance{AppName: "Radarr", InstanceName: "Movies", Version: "5.1.2"}

	instance, err := testService(context.Background(), cfg, arr.ManagerRadarr, environment.dependencies())
	if err != nil {
		t.Fatal(err)
	}
	if instance.InstanceName != "Movies" {
		t.Fatalf("Test() instance = %#v", instance)
	}
	if len(environment.resolveCalls) != 1 || environment.resolveCalls[0].label != "Radarr API key" || environment.resolveCalls[0].reference != "test/radarr-api-key" {
		t.Fatalf("resolve calls = %#v", environment.resolveCalls)
	}
	if len(environment.factoryCalls) != 1 || environment.factoryCalls[0].manager != arr.ManagerRadarr || environment.factoryCalls[0].endpoint != cfg.Radarr.Endpoint {
		t.Fatalf("factory calls = %#v", environment.factoryCalls)
	}
	assertClientCalls(t, environment.clients[arr.ManagerRadarr], 1, 0, 0)
}

func TestConnectionTestValidatesTargetAndConfiguration(t *testing.T) {
	t.Run("wrong application", func(t *testing.T) {
		cfg := testConfig()
		environment := newFakeEnvironment()
		environment.clients[arr.ManagerRadarr].instance = arr.Instance{AppName: "Sonarr", Version: "4.0"}

		_, err := testService(context.Background(), cfg, arr.ManagerRadarr, environment.dependencies())
		if err == nil || !strings.Contains(err.Error(), "identifies itself as Sonarr") {
			t.Fatalf("Test() error = %v", err)
		}
	})

	t.Run("incomplete path mapping", func(t *testing.T) {
		cfg := testConfig()
		cfg.Sonarr.LocalPathPrefix = "/Volumes/TV"
		environment := newFakeEnvironment()

		_, err := testService(context.Background(), cfg, arr.ManagerSonarr, environment.dependencies())
		if err == nil || !strings.Contains(err.Error(), "must both be set") {
			t.Fatalf("Test() error = %v", err)
		}
		if len(environment.resolveCalls) != 0 || len(environment.factoryCalls) != 0 {
			t.Fatalf("invalid configuration made calls: resolves=%v factories=%v", environment.resolveCalls, environment.factoryCalls)
		}
	})

	t.Run("endpoint excludes API path", func(t *testing.T) {
		cfg := testConfig()
		cfg.Sonarr.Endpoint = "https://media.example.test/sonarr/api/v3"
		environment := newFakeEnvironment()

		_, err := testService(context.Background(), cfg, arr.ManagerSonarr, environment.dependencies())
		if err == nil || !strings.Contains(err.Error(), "must not include /api/v3") {
			t.Fatalf("Test() error = %v", err)
		}
		if len(environment.resolveCalls) != 0 || len(environment.factoryCalls) != 0 {
			t.Fatalf("invalid endpoint made calls: resolves=%v factories=%v", environment.resolveCalls, environment.factoryCalls)
		}
	})

	t.Run("unsupported service", func(t *testing.T) {
		environment := newFakeEnvironment()
		_, err := testService(context.Background(), testConfig(), arr.Manager("lidarr"), environment.dependencies())
		if err == nil || !strings.Contains(err.Error(), "unsupported media manager") {
			t.Fatalf("Test() error = %v", err)
		}
		if len(environment.resolveCalls) != 0 || len(environment.factoryCalls) != 0 {
			t.Fatalf("unsupported service made calls: resolves=%v factories=%v", environment.resolveCalls, environment.factoryCalls)
		}
	})
}

type fakeClient struct {
	instance arr.Instance
	checkErr error
	match    arr.Match
	found    bool
	findErr  error
	setErr   error

	checkCalls   int
	findCalls    int
	setCalls     int
	findPaths    []string
	findMappings []*arr.PathMapping
	setDesired   []bool
}

func (c *fakeClient) Check(context.Context) (arr.Instance, error) {
	c.checkCalls++
	return c.instance, c.checkErr
}

func (c *fakeClient) Find(_ context.Context, mediaPath string, mapping *arr.PathMapping) (arr.Match, bool, error) {
	c.findCalls++
	c.findPaths = append(c.findPaths, mediaPath)
	if mapping == nil {
		c.findMappings = append(c.findMappings, nil)
	} else {
		copy := *mapping
		c.findMappings = append(c.findMappings, &copy)
	}
	return c.match, c.found, c.findErr
}

func (c *fakeClient) SetMonitored(_ context.Context, _ arr.Match, desired bool) error {
	c.setCalls++
	c.setDesired = append(c.setDesired, desired)
	return c.setErr
}

type resolveCall struct {
	label       string
	source      string
	reference   string
	environment string
}

type factoryCall struct {
	manager  arr.Manager
	endpoint string
	apiKey   string
}

type fakeEnvironment struct {
	clients       map[arr.Manager]*fakeClient
	resolveErrors map[string]error
	factoryErrors map[arr.Manager]error
	resolveCalls  []resolveCall
	factoryCalls  []factoryCall
}

func newFakeEnvironment() *fakeEnvironment {
	return &fakeEnvironment{
		clients: map[arr.Manager]*fakeClient{
			arr.ManagerSonarr: {instance: arr.Instance{AppName: "Sonarr", Version: "4.0"}},
			arr.ManagerRadarr: {instance: arr.Instance{AppName: "Radarr", Version: "5.0"}},
		},
		resolveErrors: make(map[string]error),
		factoryErrors: make(map[arr.Manager]error),
	}
}

func (e *fakeEnvironment) dependencies() dependencies {
	return dependencies{
		resolveSecret: func(_ context.Context, label, source, reference, environment string) (string, error) {
			e.resolveCalls = append(e.resolveCalls, resolveCall{label: label, source: source, reference: reference, environment: environment})
			if err := e.resolveErrors[label]; err != nil {
				return "", err
			}
			return "resolved-api-key", nil
		},
		newClient: func(manager arr.Manager, endpoint, apiKey string) (client, error) {
			e.factoryCalls = append(e.factoryCalls, factoryCall{manager: manager, endpoint: endpoint, apiKey: apiKey})
			if err := e.factoryErrors[manager]; err != nil {
				return nil, err
			}
			return e.clients[manager], nil
		},
	}
}

func testConfig() *config.Config {
	return &config.Config{
		Sonarr: config.MediaManagerConfig{
			Endpoint:        "http://127.0.0.1:8989",
			SecretSource:    "keyring",
			SecretReference: "test/sonarr-api-key",
			APIKeyEnv:       "VLC_MEDIA_WATCHER_SONARR_API_KEY",
		},
		Radarr: config.MediaManagerConfig{
			Endpoint:        "http://127.0.0.1:7878",
			SecretSource:    "keyring",
			SecretReference: "test/radarr-api-key",
			APIKeyEnv:       "VLC_MEDIA_WATCHER_RADARR_API_KEY",
		},
	}
}

func exactMatch(manager arr.Manager, monitored bool) arr.Match {
	return arr.Match{
		Manager: manager,
		Targets: []arr.Target{{ID: 42, Monitored: monitored}},
	}
}

func configuredManager(cfg *config.Config, manager arr.Manager) config.MediaManagerConfig {
	if manager == arr.ManagerSonarr {
		return cfg.Sonarr
	}
	return cfg.Radarr
}

func setConfiguredManager(cfg *config.Config, manager arr.Manager, managerConfig config.MediaManagerConfig) {
	if manager == arr.ManagerSonarr {
		cfg.Sonarr = managerConfig
		return
	}
	cfg.Radarr = managerConfig
}

func assertClientCalls(t *testing.T, client *fakeClient, checks, finds, sets int) {
	t.Helper()
	if client.checkCalls != checks || client.findCalls != finds || client.setCalls != sets {
		t.Fatalf("client calls = check:%d find:%d set:%d, want check:%d find:%d set:%d", client.checkCalls, client.findCalls, client.setCalls, checks, finds, sets)
	}
}

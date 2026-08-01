package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Cyberlane/vlc-media-watcher/internal/arr"
	"github.com/Cyberlane/vlc-media-watcher/internal/config"
	"github.com/Cyberlane/vlc-media-watcher/internal/store"
	"github.com/Cyberlane/vlc-media-watcher/internal/tracker"
	"github.com/Cyberlane/vlc-media-watcher/internal/watch"
)

func TestParsePercent(t *testing.T) {
	for _, input := range []string{"90", "90%", "0.9"} {
		value, err := parsePercent(input)
		if err != nil || value != .9 {
			t.Fatalf("parsePercent(%q) = %v, %v", input, value, err)
		}
	}
	if _, err := parsePercent("101"); err == nil {
		t.Fatal("expected invalid percentage error")
	}
}

func TestParseBool(t *testing.T) {
	for _, input := range []string{"true", "YES", "on", "enabled", "1"} {
		value, err := parseBool(input)
		if err != nil || !value {
			t.Fatalf("parseBool(%q) = %v, %v", input, value, err)
		}
	}
	for _, input := range []string{"false", "NO", "off", "disabled", "0"} {
		value, err := parseBool(input)
		if err != nil || value {
			t.Fatalf("parseBool(%q) = %v, %v", input, value, err)
		}
	}
	if _, err := parseBool("sometimes"); err == nil {
		t.Fatal("expected invalid boolean error")
	}
}

func TestApplySettingSavesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	m.view = settingsView
	m.selected, m.input, m.editing = settingIndex(t, m, episodeThresholdSetting), "92", true
	m.applySetting()
	if m.editing || m.config.Watch.EpisodeThreshold != .92 {
		t.Fatalf("model = %#v", m)
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.Watch.EpisodeThreshold != .92 {
		t.Fatalf("loaded = %#v, err = %v", loaded, err)
	}
}

func TestApplyMediaManagerSettingsSavesAllFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)

	m.view = sonarrView
	edits := []struct {
		id    settingID
		value string
	}{
		{sonarrEndpointSetting, "https://media.example/sonarr"},
		{sonarrSecretRefSetting, "family/sonarr-key"},
		{sonarrSecretSourceSetting, "environment"},
		{sonarrAPIKeyEnvSetting, "SONARR_KEY"},
		{sonarrLocalPrefixSetting, "/Volumes/TV"},
		{sonarrRemotePrefixSetting, "/data/tv"},
		{sonarrUnmonitorSetting, "on"},
	}
	for _, edit := range edits {
		m.selected, m.input, m.editing = settingIndex(t, m, edit.id), edit.value, true
		m.applySetting()
		if m.editing {
			t.Fatalf("%s was not saved: %s", edit.id, m.message)
		}
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Sonarr.UnmonitoringEnabled() || loaded.Sonarr.Endpoint != "https://media.example/sonarr" ||
		loaded.Sonarr.SecretSource != "environment" || loaded.Sonarr.SecretReference != "family/sonarr-key" || loaded.Sonarr.APIKeyEnv != "SONARR_KEY" ||
		loaded.Sonarr.LocalPathPrefix != "/Volumes/TV" || loaded.Sonarr.RemotePathPrefix != "/data/tv" {
		t.Fatalf("loaded Sonarr = %#v", loaded.Sonarr)
	}

	m.view = radarrView
	m.selected, m.input, m.editing = settingIndex(t, m, radarrUnmonitorSetting), "yes", true
	m.applySetting()
	if m.editing || !m.config.Radarr.UnmonitoringEnabled() {
		t.Fatalf("Radarr boolean was not saved: %#v (%s)", m.config.Radarr, m.message)
	}
}

func TestUnmonitorSettingTogglesWithEnterAndSpace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	m.view = sonarrView
	m.selected = settingIndex(t, m, sonarrUnmonitorSetting)

	_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.editing || !m.config.Sonarr.UnmonitoringEnabled() || !strings.Contains(m.message, "Saved") {
		t.Fatalf("Enter toggle = %#v (%s)", m.config.Sonarr, m.message)
	}
	if value := m.settings()[m.selected].value; value != "[ ON ]" {
		t.Fatalf("toggle display = %q, want [ ON ]", value)
	}

	_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeySpace})
	if m.editing || m.config.Sonarr.UnmonitoringEnabled() {
		t.Fatalf("Space toggle = %#v (%s)", m.config.Sonarr, m.message)
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.Sonarr.UnmonitoringEnabled() {
		t.Fatalf("saved toggle = %#v, err = %v", loaded, err)
	}
}

func TestTrackerLinkSettingTogglesWithEnterAndSpace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	cfg.Trackers = map[string]config.TrackerConfig{
		"anilist": {ClientID: "client-id", ClientSecretSource: "keyring", SecretSource: "keyring"},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	m.view, m.trackerDetail, m.trackerAdding, m.trackerService = trackersView, true, true, tracker.AniList
	m.selected = settingIndex(t, m, "trackers.anilist.enabled")

	_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.editing || m.selecting || !m.trackerConfig("anilist").Enabled {
		t.Fatalf("Enter must toggle Link tracker, not edit it: editing:%t selecting:%t config:%#v", m.editing, m.selecting, m.trackerConfig("anilist"))
	}
	if value := m.settings()[m.selected].value; value != "[ ON ]" {
		t.Fatalf("Link tracker display = %q, want [ ON ]", value)
	}

	_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeySpace})
	if m.editing || m.selecting || m.trackerConfig("anilist").Enabled {
		t.Fatalf("Space must toggle Link tracker, not edit it: editing:%t selecting:%t config:%#v", m.editing, m.selecting, m.trackerConfig("anilist"))
	}
}

func TestAniListProgressSyncIsAnExplicitToggle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	cfg.Trackers = map[string]config.TrackerConfig{
		"anilist": {Enabled: true, ClientID: "client-id", ClientSecretSource: "keyring", SecretSource: "keyring"},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	m.view, m.trackerDetail, m.trackerService = trackersView, true, tracker.AniList
	m.selected = settingIndex(t, m, "trackers.anilist.sync_progress")
	if value := m.settings()[m.selected].value; value != "[ OFF ]" {
		t.Fatalf("default sync toggle = %q", value)
	}
	_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.trackerConfig("anilist").SyncProgress || m.editing {
		t.Fatalf("sync toggle = %#v editing=%t", m.trackerConfig("anilist"), m.editing)
	}
}

func TestOAuthTrackerSettingsDoNotExposeAConflictingTokenLocation(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "config.toml"), testConfig(t))
	m.config.Trackers = map[string]config.TrackerConfig{
		"anilist": {Enabled: true, ClientID: "client", ClientSecretSource: "1password", ClientSecretReference: "op://Private/client-secret", SecretSource: "1password", SecretReference: "op://Private/wrong-token"},
	}
	m.view, m.trackerDetail, m.trackerService = trackersView, true, tracker.AniList
	for _, item := range m.settings() {
		if item.id == "trackers.anilist.secret_source" || item.id == "trackers.anilist.secret_reference" || item.id == "trackers.anilist.access_token_env" {
			t.Fatalf("account-token setting leaked into OAuth form: %#v", item)
		}
	}
	if rendered := m.View(); !strings.Contains(rendered, "secure keychain after successful OAuth linking") {
		t.Fatalf("missing token-storage explanation:\n%s", rendered)
	}
}

func TestInvalidTrackerToggleNeverOpensTextEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	m.view, m.trackerDetail, m.trackerAdding, m.trackerService = trackersView, true, true, tracker.AniList
	m.selected = settingIndex(t, m, "trackers.anilist.enabled")

	_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.editing || m.selecting {
		t.Fatalf("invalid tracker toggle opened an editor: editing:%t selecting:%t", m.editing, m.selecting)
	}
	if m.trackerConfig("anilist").Enabled || !strings.Contains(m.message, "client_id") {
		t.Fatalf("invalid tracker toggle did not preserve state with a useful error: enabled:%t message:%q", m.trackerConfig("anilist").Enabled, m.message)
	}
}

func TestEveryToggleChangesStateWithoutOpeningAnEditor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	cfg.Trackers = make(map[string]config.TrackerConfig)
	for _, definition := range tracker.All() {
		cfg.Trackers[string(definition.Service)] = config.TrackerConfig{ClientID: "client-id", ClientSecretSource: "keyring", SecretSource: "keyring"}
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	toggle := func(item setting) {
		t.Helper()
		before := item.value
		_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
		if m.editing || m.selecting {
			t.Fatalf("%s opened an editor: editing:%t selecting:%t", item.id, m.editing, m.selecting)
		}
		if after := m.settings()[m.selected].value; after == before {
			t.Fatalf("%s did not change after Enter: %q", item.id, after)
		}
		_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeySpace})
		if m.editing || m.selecting || m.settings()[m.selected].value != before {
			t.Fatalf("%s did not restore after Space: editing:%t selecting:%t value:%q", item.id, m.editing, m.selecting, m.settings()[m.selected].value)
		}
	}

	for _, view := range []int{sonarrView, radarrView} {
		m.view = view
		for index, item := range m.settings() {
			if isBooleanSetting(item.id) {
				m.selected = index
				toggle(item)
			}
		}
	}
	m.view, m.trackerDetail = trackersView, true
	for trackerIndex := range tracker.All() {
		m.trackerAdding = true
		m.trackerSelected = trackerIndex
		m.trackerService = tracker.All()[trackerIndex].Service
		m.selected = settingIndex(t, m, settingID("trackers."+string(m.trackerService)+".enabled"))
		toggle(m.settings()[m.selected])
	}
}

func TestMediaManagerGuidanceAndDashboard(t *testing.T) {
	cfg := testConfig(t)
	cfg.Sonarr.UnmonitorAfterWatch = false
	cfg.Radarr.UnmonitorAfterWatch = true
	m := New(filepath.Join(t.TempDir(), "config.toml"), cfg)
	m.width, m.height = 80, 24
	m.view = sonarrView
	view := m.View()
	for _, want := range []string{
		"Sonarr library",
		"connection, identity, guarded action",
		"Exact path → path mapping → unique bare filename",
		"[ V ] Test connection",
		"After completion: record locally only; no remote write",
		"Path mapping",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("Sonarr view does not contain %q:\n%s", want, view)
		}
	}
	if lines := strings.Count(view, "\n"); lines > 24 {
		t.Fatalf("80x24 Sonarr view uses %d lines:\n%s", lines, view)
	}
	m.message = "Saved."
	if lines := strings.Count(m.View(), "\n"); lines > 24 {
		t.Fatalf("80x24 Sonarr view with a message uses %d lines", lines)
	}
	m.editing, m.input = true, "http://127.0.0.1:8989"
	if lines := strings.Count(m.View(), "\n"); lines > 24 {
		t.Fatalf("80x24 Sonarr edit view uses %d lines", lines)
	}
	m.editing, m.message = false, ""

	m.view = dashboardView
	view = m.View()
	for _, want := range []string{
		"Sonarr:", "not configured",
		"Radarr:", "unmonitors matched movies",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("dashboard does not contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "dry-run mode") || strings.Contains(view, "integration pending") {
		t.Fatalf("dashboard contains stale integration copy:\n%s", view)
	}
}

func TestMediaManagerValidationAction(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "config.toml"), testConfig(t))
	m.view = radarrView
	m.validate = func(_ context.Context, _ *config.Config, manager arr.Manager) (arr.Instance, error) {
		if manager != arr.ManagerRadarr {
			t.Fatalf("manager = %q", manager)
		}
		return arr.Instance{AppName: "Radarr", InstanceName: "Cinema", Version: "5.1"}, nil
	}
	_, cmd := m.updateKey(keyMsg("v"))
	if !m.validating || !strings.Contains(m.managerMessages["radarr"].text, "Validating Radarr") || cmd == nil {
		t.Fatalf("validation did not start: validating=%t message=%#v", m.validating, m.managerMessages["radarr"])
	}
	_, _ = m.Update(cmd())
	status := m.managerMessages["radarr"]
	if m.validating || status.tone != messageToneSuccess || !strings.Contains(status.text, "Radarr connection OK: Radarr 5.1 (Cinema)") || !strings.Contains(status.text, "No library changes") {
		t.Fatalf("validation result = validating:%t message:%#v", m.validating, status)
	}
	m.view = dashboardView
	if strings.Contains(m.View(), "Radarr connection OK") {
		t.Fatal("Radarr validation status appeared outside the Radarr tab")
	}
	m.view = radarrView
	if !strings.Contains(m.View(), "Radarr connection OK") {
		t.Fatal("Radarr validation status did not remain on the Radarr tab")
	}
	_, _ = m.updateKey(keyMsg("v"))
	if m.managerMessages["radarr"].tone != messageToneNeutral {
		t.Fatalf("validation-in-progress tone = %v", m.managerMessages["radarr"].tone)
	}
	_, _ = m.Update(validationResultMsg{service: "radarr", err: errors.New("not reachable")})
	if m.managerMessages["radarr"].tone != messageToneWarning {
		t.Fatalf("validation-failure tone = %v", m.managerMessages["radarr"].tone)
	}
}

func TestSecretSourceUsesSelectorAndAdaptsTheLabels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	m.view = sonarrView
	m.selected = settingIndex(t, m, sonarrSecretSourceSetting)

	_, _ = m.updateKey(keyMsg("enter"))
	if !m.editing || !m.selecting {
		t.Fatalf("selector state = editing:%t selecting:%t", m.editing, m.selecting)
	}
	view := m.View()
	for _, want := range []string{"Choose API key location", "This computer's secure keychain", "1Password", "Environment variable"} {
		if !strings.Contains(view, want) {
			t.Errorf("selector does not contain %q:\n%s", want, view)
		}
	}

	_, _ = m.updateKey(keyMsg("down"))
	_, _ = m.updateKey(keyMsg("enter"))
	if m.editing || m.config.Sonarr.SecretSource != "1password" {
		t.Fatalf("1Password selection was not saved: %#v (%s)", m.config.Sonarr, m.message)
	}
	view = m.View()
	for _, want := range []string{"1Password item", "op://Private/Sonarr/password"} {
		if !strings.Contains(view, want) {
			t.Errorf("1Password settings do not contain %q:\n%s", want, view)
		}
	}
}

func TestFiniteSettingsUseReusableSelectionLists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	m.view = settingsView
	m.selected = settingIndex(t, m, episodeThresholdSetting)
	_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.selecting || len(m.selectionOptions) != len(thresholdOptions()) {
		t.Fatalf("threshold selection = selecting:%t options:%#v", m.selecting, m.selectionOptions)
	}
	_, _ = m.updateKey(keyMsg("down")) // 90% -> 95%
	_, _ = m.updateKey(keyMsg("enter"))
	if m.editing || m.config.Watch.EpisodeThreshold != .95 {
		t.Fatalf("threshold selection did not save: %#v (%s)", m.config.Watch, m.message)
	}

	m.selected = settingIndex(t, m, vlcEndpointSetting)
	_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.selecting || !m.selectionOptions[len(m.selectionOptions)-1].custom {
		t.Fatalf("endpoint selection = %#v", m.selectionOptions)
	}
	m.choice = len(m.selectionOptions) - 1
	_, _ = m.updateKey(keyMsg("enter"))
	if !m.editing || m.selecting {
		t.Fatalf("custom endpoint did not enter text mode: editing:%t selecting:%t", m.editing, m.selecting)
	}

	m.editing = false
	m.selected = settingIndex(t, m, databasePathSetting)
	_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.selecting || len(m.selectionOptions) != 2 || !m.selectionOptions[1].custom {
		t.Fatalf("text setting should offer a keep-or-custom picker: %#v", m.selectionOptions)
	}
}

func TestEveryBooleanSettingRendersAsToggle(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "config.toml"), testConfig(t))
	for _, view := range []int{sonarrView, radarrView, trackersView} {
		m.view = view
		m.trackerDetail = view == trackersView
		for _, item := range m.settings() {
			if !isBooleanSetting(item.id) {
				continue
			}
			if item.value != "[ ON ]" && item.value != "[ OFF ]" {
				t.Fatalf("%s renders %q instead of a toggle", item.id, item.value)
			}
		}
	}
}

func TestEveryNonBooleanSettingStartsWithASelectionList(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "config.toml"), testConfig(t))
	for _, view := range []int{settingsView, sonarrView, radarrView, trackersView} {
		m.view = view
		m.trackerDetail = view == trackersView
		for index, item := range m.settings() {
			if isBooleanSetting(item.id) {
				continue
			}
			m.selected = index
			m.editing = false
			m.selecting = false
			m.selectionOptions = nil
			_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
			if !m.selecting || !m.editing || len(m.selectionOptions) == 0 {
				t.Fatalf("%s did not open a selection list: editing:%t selecting:%t options:%#v", item.id, m.editing, m.selecting, m.selectionOptions)
			}
			_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEsc})
		}
	}
}

func TestCustomSavedValuesDoNotDefaultToTextEntry(t *testing.T) {
	cfg := testConfig(t)
	cfg.Profile = "household"
	cfg.VLC.Endpoint = "https://vlc.example.test"
	cfg.Watch.EpisodeThreshold = .93
	m := New(filepath.Join(t.TempDir(), "config.toml"), cfg)
	m.view = settingsView
	for _, id := range []settingID{profileSetting, vlcEndpointSetting, episodeThresholdSetting} {
		m.selected = settingIndex(t, m, id)
		_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
		if !m.selecting || m.selectionOptions[m.choice].custom {
			t.Fatalf("%s defaults to text entry: selecting:%t choice:%#v", id, m.selecting, m.selectionOptions)
		}
		if !strings.Contains(m.selectionOptions[m.choice].label, "Keep current value") {
			t.Fatalf("%s should preserve its existing custom value: %#v", id, m.selectionOptions)
		}
		_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEsc})
	}
}

func TestOnlyTheActiveSecretLocatorIsEditable(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "config.toml"), testConfig(t))
	m.view = sonarrView
	assertSettingPresent := func(id settingID, want bool) {
		t.Helper()
		found := false
		for _, item := range m.settings() {
			if item.id == id {
				found = true
			}
		}
		if found != want {
			t.Fatalf("%s present=%t, want %t", id, found, want)
		}
	}

	assertSettingPresent(sonarrSecretRefSetting, true)
	assertSettingPresent(sonarrAPIKeyEnvSetting, false)
	m.config.Sonarr.SecretSource = "environment"
	assertSettingPresent(sonarrSecretRefSetting, false)
	assertSettingPresent(sonarrAPIKeyEnvSetting, true)

	m.view, m.trackerDetail = trackersView, true
	m.config.Trackers = map[string]config.TrackerConfig{
		"anilist": {SecretSource: "environment", ClientSecretSource: "keyring"},
	}
	for _, item := range m.settings() {
		if item.id == "trackers.anilist.secret_reference" || item.id == "trackers.anilist.client_secret_env" {
			t.Fatalf("inactive tracker locator leaked into the form: %s", item.id)
		}
	}
}

func TestEventStatusKeepsFailuresVisible(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "config.toml"), testConfig(t))
	for _, status := range []string{"pending", "failed", "unmatched"} {
		rendered := m.eventStatus(watch.Event{Status: status})
		if !strings.Contains(rendered, strings.ToUpper(status)) {
			t.Fatalf("eventStatus(%q) = %q", status, rendered)
		}
	}
}

func TestTrackingDefaultsToActionableItemsAndShowsResolutionEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC()
	for _, event := range []watch.Event{
		{MediaPath: "/media/needs.mkv", Progress: .9, WatchedAt: when, Status: "unmatched"},
		{MediaPath: "/media/done.mkv", Progress: .9, WatchedAt: when.Add(-time.Minute), Status: "unmonitored", Manager: "sonarr", SourceID: 22, SeasonNumber: 1},
	} {
		if _, err := db.RecordEvent(event); err != nil {
			t.Fatal(err)
		}
		if event.SourceID > 0 {
			if err := db.UpdateEventResolution(event); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.RecordWatcherHeartbeat(when); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	m.view = eventsView
	if rendered := m.View(); !strings.Contains(rendered, "Needs action 1") || !strings.Contains(rendered, "needs.mkv") || strings.Contains(rendered, "done.mkv") {
		t.Fatalf("default Tracking view =\n%s", rendered)
	}
	m.setTrackingMode(trackingCompleted)
	if rendered := m.View(); !strings.Contains(rendered, "done.mkv") || !strings.Contains(rendered, "sonarr source #22") {
		t.Fatalf("completed Tracking view =\n%s", rendered)
	}
	_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if rendered := m.View(); !strings.Contains(rendered, "Resolved library identity") || !strings.Contains(rendered, "season 1") {
		t.Fatalf("Tracking detail =\n%s", rendered)
	}
}

func TestTrackersHideDisabledProvidersUntilAddMode(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "config.toml"), testConfig(t))
	m.config.Trackers = make(map[string]config.TrackerConfig)
	m.config.Trackers["anilist"] = config.TrackerConfig{Enabled: true, ClientID: "client", SecretSource: "keyring", ClientSecretSource: "keyring"}
	m.view = trackersView
	if rendered := m.View(); !strings.Contains(rendered, "AniList") || strings.Contains(rendered, "AniDB") || strings.Contains(rendered, "MyAnimeList") {
		t.Fatalf("enabled tracker list =\n%s", rendered)
	}
	_, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if rendered := m.View(); !strings.Contains(rendered, "Add tracker") || !strings.Contains(rendered, "AniDB") {
		t.Fatalf("add tracker list =\n%s", rendered)
	}
}

func TestMatchesRequireAnExplicitTrackerConfirmation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	cfg.Trackers = map[string]config.TrackerConfig{"anilist": {Enabled: true, ClientID: "client", SecretSource: "keyring", SecretReference: "default/anilist-access-token"}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	units, err := db.UpsertIdentity(arr.MediaIdentity{Manager: arr.ManagerSonarr, SourceID: 7, Kind: "series", Title: "Example Anime", SeasonNumber: 2})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	m.view = matchesView
	for index, unit := range m.mediaUnits {
		if unit.Scope == "season" {
			m.selected = index
		}
	}
	_, _ = m.updateKey(keyMsg("enter"))
	if !m.mappingSelecting {
		t.Fatal("expected tracker selector")
	}
	_, _ = m.updateKey(keyMsg("enter"))
	if !m.finding {
		t.Fatal("expected tracker candidate search")
	}
	_, _ = m.Update(candidateResultMsg{})
	if !m.manualIDSelecting {
		t.Fatal("expected manual-ID action picker after no candidates")
	}
	_, _ = m.updateKey(keyMsg("down"))
	_, _ = m.updateKey(keyMsg("enter"))
	if !m.editing {
		t.Fatal("expected tracker ID input only after choosing manual entry")
	}
	m.input = "12345"
	m.confirmMapping()
	if m.editing || !strings.Contains(m.message, "Confirmed locally") {
		t.Fatalf("confirmation state = editing:%t message:%q", m.editing, m.message)
	}
	db, err = store.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mappings, err := db.MappingsForUnit(units[1].ID)
	if err != nil || len(mappings) != 1 || mappings[0].Tracker != "anilist" || mappings[0].TrackerID != "12345" {
		t.Fatalf("mappings = %#v, %v", mappings, err)
	}
}

func TestAniListMappingQueuesCompletedSeasonCatchUpWithoutBlockingTheTUI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	cfg.Trackers = map[string]config.TrackerConfig{"anilist": {Enabled: true, SyncProgress: true, ClientID: "client", SecretSource: "keyring", SecretReference: "default/anilist-access-token"}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	units, err := db.UpsertIdentity(arr.MediaIdentity{Manager: arr.ManagerSonarr, SourceID: 7, Kind: "series", Title: "Example Anime", SeasonNumber: 1})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	event := watch.Event{MediaPath: "example-s01e01.mkv", Progress: .9, WatchedAt: time.Now().UTC(), Status: "unmonitored", Manager: "sonarr", SourceID: 7, SeasonNumber: 1, EpisodeNumbers: []int{1}}
	if _, err := db.RecordEvent(event); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.UpdateEventResolution(event); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	m := New(path, cfg)
	m.view = matchesView
	for index, unit := range m.mediaUnits {
		if unit.ID == units[1].ID {
			m.selected = index
		}
	}
	m.mappingTracker = 0
	m.input = "12345"
	command := m.confirmMapping()
	if command == nil || !m.trackingSyncing || !strings.Contains(m.message, "Queuing completed episodes") {
		t.Fatalf("mapping queue state command:%t syncing:%t message:%q", command != nil, m.trackingSyncing, m.message)
	}
}

func TestMatchesSeparateSourceFactsFromConfirmedTrackerTargetAndOpenIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	cfg.Trackers = map[string]config.TrackerConfig{"anidb": {Enabled: true}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	units, err := db.UpsertIdentity(arr.MediaIdentity{Manager: arr.ManagerSonarr, SourceID: 7, Kind: "series", Title: "Example Anime", SeasonNumber: 2, TVDBID: 99})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.ConfirmMapping(store.TrackerMapping{MediaUnitID: units[1].ID, Tracker: "anidb", TrackerID: "123", TrackerTitle: "AniDB Native Title"}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	m.view = matchesView
	m.setMatchesMode(matchesConfirmed)
	for index, unit := range m.mediaUnits {
		if unit.Scope == "season" {
			m.selected = index
		}
	}
	rendered := m.View()
	for _, want := range []string{"Example Anime", "sonarr source #7", "AniDB", "123", "AniDB Native Title", "https://anidb.net/a123"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Matches view missing %q:\n%s", want, rendered)
		}
	}
	opened := ""
	m.openURL = func(link string) error { opened = link; return nil }
	m.openConfirmedMapping()
	if opened != "https://anidb.net/a123" || !strings.Contains(m.message, "Opened confirmed") {
		t.Fatalf("opened=%q message=%q", opened, m.message)
	}
}

func TestMatchesHideNonActionableSeriesForSeasonOnlyTracker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	cfg.Trackers = map[string]config.TrackerConfig{"anidb": {Enabled: true}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.UpsertIdentity(arr.MediaIdentity{Manager: arr.ManagerSonarr, SourceID: 7, Kind: "series", Title: "Example Anime", SeasonNumber: 2})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	m.view = matchesView
	for _, unit := range m.mediaUnits {
		if unit.Scope == "series" {
			t.Fatalf("non-actionable series unit leaked into default mapping queue: %#v", m.mediaUnits)
		}
	}
}

func TestMatchesDefaultQueueShowsOnlyUnconfirmedEnabledTrackerScopes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	cfg.Trackers = map[string]config.TrackerConfig{"anilist": {Enabled: true, ClientID: "client", SecretSource: "keyring", SecretReference: "token"}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	first, err := db.UpsertIdentity(arr.MediaIdentity{Manager: arr.ManagerSonarr, SourceID: 1, Kind: "series", Title: "Mapped", SeasonNumber: 1})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.ConfirmMapping(store.TrackerMapping{MediaUnitID: first[1].ID, Tracker: "anilist", TrackerID: "100", TrackerTitle: "Mapped Season"}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.UpsertIdentity(arr.MediaIdentity{Manager: arr.ManagerSonarr, SourceID: 2, Kind: "series", Title: "Needs Mapping", SeasonNumber: 2}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	m.view = matchesView
	if len(m.mediaUnits) != 1 || m.mediaUnits[0].Title != "Needs Mapping" || m.mediaUnits[0].Scope != "season" {
		t.Fatalf("default mapping queue = %#v", m.mediaUnits)
	}
	m.setMatchesMode(matchesConfirmed)
	if len(m.mediaUnits) != 1 || m.mediaUnits[0].Title != "Mapped" || m.mediaUnits[0].Scope != "season" {
		t.Fatalf("confirmed view = %#v", m.mediaUnits)
	}
	m.setMatchesMode(matchesRecent)
	if len(m.mediaUnits) != 2 {
		t.Fatalf("recent enabled scopes = %#v", m.mediaUnits)
	}
}

func TestConfirmedMappingActionCanRemoveLocalMapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig(t)
	cfg.Trackers = map[string]config.TrackerConfig{"anidb": {Enabled: true}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	units, err := db.UpsertIdentity(arr.MediaIdentity{Manager: arr.ManagerSonarr, SourceID: 7, Kind: "series", Title: "Example", SeasonNumber: 1})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.ConfirmMapping(store.TrackerMapping{MediaUnitID: units[1].ID, Tracker: "anidb", TrackerID: "10", TrackerTitle: "Example"}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg)
	m.view = matchesView
	m.setMatchesMode(matchesConfirmed)
	_, _ = m.updateKey(keyMsg("enter"))
	if !m.mappingActionSelecting {
		t.Fatal("expected confirmed-mapping action menu")
	}
	_, _ = m.updateKey(keyMsg("down"))
	_, _ = m.updateKey(keyMsg("down"))
	_, _ = m.updateKey(keyMsg("enter"))
	if !strings.Contains(m.message, "Removed local") || len(m.mediaUnits) != 0 {
		t.Fatalf("remove state message=%q units=%#v", m.message, m.mediaUnits)
	}
}

func TestTrackerLinkActionStartsBrowserAuthorization(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "config.toml"), testConfig(t))
	m.config.Trackers = map[string]config.TrackerConfig{
		"anilist": {Enabled: true, ClientID: "client", ClientSecretSource: "keyring", ClientSecretReference: "default/anilist-client-secret", SecretSource: "keyring", SecretReference: "default/anilist-access-token"},
	}
	m.view, m.trackerDetail = trackersView, true
	m.link = func(_ context.Context, service tracker.Service, cfg config.TrackerConfig, _ func(string) error) (tracker.LinkResult, error) {
		if service != tracker.AniList || cfg.ClientID != "client" {
			t.Fatalf("link request = %s %#v", service, cfg)
		}
		return tracker.LinkResult{AccessToken: "token"}, nil
	}
	_, command := m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("L")})
	if !m.linking || command == nil || !strings.Contains(m.message, "Opening the browser") {
		t.Fatalf("link state = linking:%t command:%v message:%q", m.linking, command != nil, m.message)
	}
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	storagePath := filepath.Join(t.TempDir(), "watcher.db")
	return &config.Config{
		Profile: "default",
		VLC: config.VLCConfig{
			Endpoint:        "http://127.0.0.1:8081",
			SecretSource:    "keyring",
			SecretReference: "default/vlc-http-password",
			PasswordEnv:     "VLC_PASSWORD",
		},
		Watch:   config.WatchConfig{EpisodeThreshold: .9, MovieThreshold: .85, PollInterval: 2 * time.Second},
		Storage: config.StorageConfig{Path: storagePath},
		Sonarr: config.MediaManagerConfig{
			Endpoint:        "http://127.0.0.1:8989",
			SecretSource:    "keyring",
			SecretReference: "default/sonarr-api-key",
			APIKeyEnv:       "SONARR_API_KEY",
		},
		Radarr: config.MediaManagerConfig{
			Endpoint:        "http://127.0.0.1:7878",
			SecretSource:    "keyring",
			SecretReference: "default/radarr-api-key",
			APIKeyEnv:       "RADARR_API_KEY",
		},
	}
}

func settingIndex(t *testing.T, m *Model, id settingID) int {
	t.Helper()
	for index, item := range m.settings() {
		if item.id == id {
			return index
		}
	}
	t.Fatalf("setting %q not found in view %d", id, m.view)
	return -1
}

func keyMsg(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}

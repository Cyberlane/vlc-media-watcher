package tui

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Cyberlane/vlc-media-watcher/internal/arr"
	"github.com/Cyberlane/vlc-media-watcher/internal/config"
	"github.com/Cyberlane/vlc-media-watcher/internal/reconcile"
	"github.com/Cyberlane/vlc-media-watcher/internal/secrets"
	"github.com/Cyberlane/vlc-media-watcher/internal/store"
	"github.com/Cyberlane/vlc-media-watcher/internal/tracker"
	"github.com/Cyberlane/vlc-media-watcher/internal/watch"
)

var (
	titleStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	sectionStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	tabStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
	activeTabStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("63")).Padding(0, 1)
	labelStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	valueStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	selectedRowStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24"))
	toggleOnStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("78"))
	toggleOffStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245"))
	hintStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Italic(true)
	infoStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	mutedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	warningStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errorStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	successStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
)

const (
	dashboardView = iota
	eventsView
	settingsView
	sonarrView
	radarrView
	trackersView
	matchesView
	viewCount
)

type matchesMode uint8

const (
	matchesNeedsMapping matchesMode = iota
	matchesConfirmed
	matchesRecent
)

type trackingMode uint8

const (
	trackingNeedsAction trackingMode = iota
	trackingCompleted
	trackingAll
)

type settingID string

const (
	profileSetting            settingID = "profile"
	vlcEndpointSetting        settingID = "vlc.endpoint"
	vlcSecretSourceSetting    settingID = "vlc.secret_source"
	vlcSecretReferenceSetting settingID = "vlc.secret_reference"
	vlcPasswordEnvSetting     settingID = "vlc.password_env"
	episodeThresholdSetting   settingID = "watch.episode_threshold"
	movieThresholdSetting     settingID = "watch.movie_threshold"
	pollIntervalSetting       settingID = "watch.poll_interval"
	databasePathSetting       settingID = "storage.path"
	sonarrUnmonitorSetting    settingID = "sonarr.unmonitor_after_watch"
	sonarrMetadataSetting     settingID = "sonarr.metadata_lookup"
	sonarrEndpointSetting     settingID = "sonarr.endpoint"
	sonarrSecretSourceSetting settingID = "sonarr.secret_source"
	sonarrSecretRefSetting    settingID = "sonarr.secret_reference"
	sonarrAPIKeyEnvSetting    settingID = "sonarr.api_key_env"
	sonarrLocalPrefixSetting  settingID = "sonarr.local_path_prefix"
	sonarrRemotePrefixSetting settingID = "sonarr.remote_path_prefix"
	radarrUnmonitorSetting    settingID = "radarr.unmonitor_after_watch"
	radarrMetadataSetting     settingID = "radarr.metadata_lookup"
	radarrEndpointSetting     settingID = "radarr.endpoint"
	radarrSecretSourceSetting settingID = "radarr.secret_source"
	radarrSecretRefSetting    settingID = "radarr.secret_reference"
	radarrAPIKeyEnvSetting    settingID = "radarr.api_key_env"
	radarrLocalPrefixSetting  settingID = "radarr.local_path_prefix"
	radarrRemotePrefixSetting settingID = "radarr.remote_path_prefix"
)

type Model struct {
	configPath             string
	config                 *config.Config
	allEvents              []watch.Event
	events                 []watch.Event
	trackingMode           trackingMode
	trackingDetail         bool
	trackingRetrying       bool
	trackingSyncing        bool
	heartbeat              time.Time
	heartbeatKnown         bool
	integrationChecks      map[string]store.IntegrationCheck
	syncJobs               map[string]store.TrackerSyncJob
	allMediaUnits          []store.MediaUnit
	mediaUnits             []store.MediaUnit
	mappings               map[int64][]store.TrackerMapping
	matchesMode            matchesMode
	view                   int
	selected               int
	editing                bool
	editingSearch          bool
	selecting              bool
	choice                 int
	selectionOptions       []choiceOption
	selectionTitle         string
	validating             bool
	validate               validateServiceFunc
	input                  string
	message                string
	messageTone            messageTone
	managerMessages        map[string]messageState
	width                  int
	height                 int
	trackerDetail          bool
	managerDiagnostics     bool
	trackerSelected        int
	trackerService         tracker.Service
	trackerAdding          bool
	mappingSelecting       bool
	mappingActionSelecting bool
	mappingAction          int
	activeMapping          store.TrackerMapping
	manualIDSelecting      bool
	manualIDChoice         int
	mappingTracker         int
	finding                bool
	candidatePicking       bool
	candidates             []tracker.Candidate
	candidateSelected      int
	search                 trackerSearchFunc
	openURL                func(string) error
	linking                bool
	link                   trackerLinkFunc
}

type messageTone uint8

const (
	messageToneNeutral messageTone = iota
	messageToneSuccess
	messageToneWarning
)

type messageState struct {
	text string
	tone messageTone
}

type validateServiceFunc func(context.Context, *config.Config, arr.Manager) (arr.Instance, error)

type validationResultMsg struct {
	service  string
	instance arr.Instance
	err      error
}

type trackerSearchFunc func(context.Context, tracker.Service, string, string) ([]tracker.Candidate, error)

type candidateResultMsg struct {
	service    tracker.Service
	candidates []tracker.Candidate
	err        error
}

type trackerLinkFunc func(context.Context, tracker.Service, config.TrackerConfig, func(string) error) (tracker.LinkResult, error)

type trackerLinkResultMsg struct {
	service tracker.Service
	result  tracker.LinkResult
	err     error
}

type trackingRetryResultMsg struct {
	attempted int
	resolved  int
	err       error
}

type trackingSyncResultMsg struct {
	job store.TrackerSyncJob
	err error
}

type mappingSyncResultMsg struct {
	attempted int
	synced    int
	review    int
	err       error
}

type trackingSyncProgressMsg struct {
	detail  string
	updates <-chan tea.Msg
}

func Run(configPath string, input io.Reader, output io.Writer) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	m := New(configPath, cfg)
	_, err = tea.NewProgram(m, tea.WithInput(input), tea.WithOutput(output), tea.WithAltScreen()).Run()
	return err
}

func New(configPath string, cfg *config.Config) *Model {
	m := &Model{configPath: configPath, config: cfg, width: 80, height: 24, validate: reconcile.Test, search: tracker.Search, openURL: tracker.OpenBrowser, link: tracker.Link, managerMessages: make(map[string]messageState), mappings: make(map[int64][]store.TrackerMapping), integrationChecks: make(map[string]store.IntegrationCheck), syncJobs: make(map[string]store.TrackerSyncJob)}
	if definitions := tracker.All(); len(definitions) > 0 {
		m.trackerService = definitions[0].Service
	}
	m.reloadEvents()
	m.reloadMediaUnits()
	return m
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch value := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = value.Width, value.Height
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(value)
	case validationResultMsg:
		m.validating = false
		name := titleService(value.service)
		if value.err != nil {
			m.recordIntegrationCheck(value.service, "failed", value.err.Error())
			m.setManagerMessage(value.service, fmt.Sprintf("%s validation failed: %v", name, value.err), messageToneWarning)
			return m, nil
		}
		instance := strings.TrimSpace(value.instance.InstanceName)
		if instance == "" {
			instance = value.instance.AppName
		}
		detail := fmt.Sprintf("✓ Connected — %s %s", value.instance.AppName, value.instance.Version)
		m.recordIntegrationCheck(value.service, "success", detail)
		m.setManagerMessage(value.service, fmt.Sprintf("%s connection OK: %s %s (%s). No library changes were made.", name, value.instance.AppName, value.instance.Version, instance), messageToneSuccess)
		return m, nil
	case candidateResultMsg:
		m.finding = false
		if value.err != nil {
			m.recordIntegrationCheck(string(value.service), "failed", value.err.Error())
			m.manualIDSelecting = true
			m.manualIDChoice = 0
			m.setMessage("Could not find candidates: "+value.err.Error()+" Choose how to continue.", messageToneWarning)
			return m, nil
		}
		if len(value.candidates) == 0 {
			m.manualIDSelecting = true
			m.manualIDChoice = 0
			m.setMessage("No candidates found. Choose how to continue.", messageToneWarning)
			return m, nil
		}
		m.candidates = value.candidates
		m.candidateSelected = 0
		m.candidatePicking = true
		m.setMessage("Review the candidates; Enter confirms the highlighted exact ID.", messageToneNeutral)
		return m, nil
	case trackerLinkResultMsg:
		m.linking = false
		if value.err != nil {
			m.setMessage("Could not link "+trackerTitle(value.service)+": "+value.err.Error(), messageToneWarning)
			return m, nil
		}
		trackerConfig := m.trackerConfig(string(value.service))
		if err := secrets.StoreInKeyring(trackerConfig.SecretReference, value.result.AccessToken); err != nil {
			m.setMessage("Could not store linked token: "+err.Error(), messageToneWarning)
			return m, nil
		}
		m.recordIntegrationCheck(string(value.service), "linked", "✓ Linked locally")
		m.setMessage(trackerTitle(value.service)+" account linked securely.", messageToneSuccess)
		return m, nil
	case trackingRetryResultMsg:
		m.trackingRetrying = false
		m.reloadEvents()
		m.reloadMediaUnits()
		if value.err != nil {
			m.setMessage("Retry stopped: "+value.err.Error(), messageToneWarning)
			return m, nil
		}
		m.setMessage(fmt.Sprintf("Retried %d watched file(s); %d reached a stable outcome.", value.attempted, value.resolved), messageToneSuccess)
		return m, nil
	case trackingSyncResultMsg:
		m.trackingSyncing = false
		m.reloadEvents()
		if value.err != nil {
			m.setMessage("AniList sync did not complete: "+value.err.Error(), messageToneWarning)
			return m, nil
		}
		m.setMessage("AniList: "+value.job.Detail, messageToneSuccess)
		return m, nil
	case mappingSyncResultMsg:
		m.trackingSyncing = false
		m.reloadEvents()
		if value.err != nil {
			m.setMessage(fmt.Sprintf("AniList catch-up stopped after %d of %d episode(s): %v", value.synced+value.review, value.attempted, value.err), messageToneWarning)
			return m, nil
		}
		if value.attempted == 0 {
			m.setMessage("Confirmed locally. No completed episodes are waiting for AniList.", messageToneSuccess)
			return m, nil
		}
		if value.review > 0 {
			m.setMessage(fmt.Sprintf("AniList catch-up finished: %d synced, %d need review.", value.synced, value.review), messageToneWarning)
			return m, nil
		}
		m.setMessage(fmt.Sprintf("AniList catch-up finished: %d recorded episode(s) synced.", value.synced), messageToneSuccess)
		return m, nil
	case trackingSyncProgressMsg:
		m.setMessage("AniList sync: "+value.detail, messageToneNeutral)
		return m, waitForTrackingSync(value.updates)
	}
	return m, nil
}

func (m *Model) recordIntegrationCheck(service, state, detail string) {
	db, err := store.Open(m.config.DatabasePath())
	if err != nil {
		return
	}
	defer db.Close()
	if err := db.RecordIntegrationCheck(service, state, detail, time.Now()); err == nil {
		if m.integrationChecks == nil {
			m.integrationChecks = make(map[string]store.IntegrationCheck)
		}
		m.integrationChecks[service] = store.IntegrationCheck{Service: service, State: state, Detail: detail, CheckedAt: time.Now().UTC()}
	}
}

func (m *Model) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.candidatePicking {
		switch key.String() {
		case "esc":
			m.candidatePicking = false
			m.candidates = nil
			m.setMessage("Candidate selection cancelled.", messageToneNeutral)
		case "up", "k":
			m.candidateSelected = (m.candidateSelected + len(m.candidates) - 1) % len(m.candidates)
		case "down", "j":
			m.candidateSelected = (m.candidateSelected + 1) % len(m.candidates)
		case "enter", "c":
			return m, m.confirmCandidate()
		case "o":
			m.openSelectedCandidate()
		}
		return m, nil
	}
	if m.mappingActionSelecting {
		switch key.String() {
		case "esc":
			m.mappingActionSelecting = false
			m.setMessage("Mapping action cancelled.", messageToneNeutral)
		case "up", "k", "left", "h":
			m.mappingAction = (m.mappingAction + 2) % 3
		case "down", "j", "right", "l":
			m.mappingAction = (m.mappingAction + 1) % 3
		case "enter":
			switch m.mappingAction {
			case 0:
				m.openConfirmedMapping()
				m.mappingActionSelecting = false
			case 1:
				m.mappingActionSelecting = false
				m.beginReplacement()
			case 2:
				m.mappingActionSelecting = false
				m.deleteActiveMapping()
			}
		}
		return m, nil
	}
	if m.mappingSelecting {
		if m.selected < 0 || m.selected >= len(m.mediaUnits) {
			m.mappingSelecting = false
			m.setMessage("No media item is selected.", messageToneWarning)
			return m, nil
		}
		available := m.eligibleTrackerDefinitions(m.mediaUnits[m.selected])
		if len(available) == 0 {
			m.mappingSelecting = false
			m.setMessage("Link a tracker before confirming an ID.", messageToneWarning)
			return m, nil
		}
		switch key.String() {
		case "esc":
			m.mappingSelecting = false
			m.setMessage("Mapping confirmation cancelled.", messageToneNeutral)
		case "up", "k":
			m.mappingTracker = (m.mappingTracker + len(available) - 1) % len(available)
		case "down", "j":
			m.mappingTracker = (m.mappingTracker + 1) % len(available)
		case "enter":
			m.mappingSelecting = false
			m.finding = true
			m.setMessage("Finding tracker candidates…", messageToneNeutral)
			return m, m.findCandidates(available[m.mappingTracker])
		case "f":
			if m.selected < 0 || m.selected >= len(m.mediaUnits) {
				m.setMessage("No media item is selected.", messageToneWarning)
				return m, nil
			}
			m.mappingSelecting = false
			m.finding = true
			m.setMessage("Finding tracker candidates…", messageToneNeutral)
			return m, m.findCandidates(available[m.mappingTracker])
		case "s":
			m.mappingSelecting = false
			m.editing = true
			m.editingSearch = true
			m.input = m.mediaUnits[m.selected].Title
			m.setMessage("Edit the tracker search title. Searching never confirms a mapping.", messageToneNeutral)
		}
		return m, nil
	}
	if m.manualIDSelecting {
		switch key.String() {
		case "esc":
			m.manualIDSelecting = false
			m.setMessage("Mapping confirmation cancelled.", messageToneNeutral)
		case "up", "k", "left", "h":
			m.manualIDChoice = (m.manualIDChoice + 1) % 2
		case "down", "j", "right", "l":
			m.manualIDChoice = (m.manualIDChoice + 1) % 2
		case "enter":
			if m.manualIDChoice == 0 {
				m.manualIDSelecting = false
				m.mappingSelecting = true
				m.setMessage("Choose a tracker to search again.", messageToneNeutral)
				return m, nil
			}
			m.manualIDSelecting = false
			m.editing = true
			m.input = ""
			m.setMessage("Enter the exact tracker ID. This is the explicit manual-confirmation step.", messageToneNeutral)
		}
		return m, nil
	}
	if m.selecting {
		choices := m.selectionOptions
		switch key.String() {
		case "esc":
			m.editing = false
			m.selecting = false
			m.selectionOptions = nil
			m.setMessage("Selection cancelled.", messageToneNeutral)
		case "up", "k", "left", "h":
			m.choice = (m.choice + len(choices) - 1) % len(choices)
		case "down", "j", "right", "l":
			m.choice = (m.choice + 1) % len(choices)
		case "enter":
			selected := choices[m.choice]
			m.selecting = false
			m.selectionOptions = nil
			if selected.custom {
				m.input = m.settings()[m.selected].value
				m.setMessage("Enter a custom value for "+m.settings()[m.selected].name+". Enter saves; Esc cancels.", messageToneNeutral)
				return m, nil
			}
			m.input = selected.value
			m.applySetting()
			if !m.editing && isSecretSource(m.settings()[m.selected].id) {
				m.selectSecretReference()
				if m.input == "1password" {
					m.setMessage("Saved. Next, enter the 1Password item shown below.", messageToneSuccess)
				}
			}
		}
		return m, nil
	}

	if m.editing {
		switch key.Type {
		case tea.KeyEsc:
			m.editing = false
			m.editingSearch = false
			m.setMessage("Edit cancelled.", messageToneNeutral)
		case tea.KeyEnter:
			if m.view == matchesView {
				if m.editingSearch {
					m.editing = false
					m.editingSearch = false
					if m.selected < 0 || m.selected >= len(m.mediaUnits) {
						m.setMessage("No media item is selected.", messageToneWarning)
						return m, nil
					}
					definitions := m.eligibleTrackerDefinitions(m.mediaUnits[m.selected])
					if m.mappingTracker < 0 || m.mappingTracker >= len(definitions) {
						m.setMessage("No eligible tracker is selected.", messageToneWarning)
						return m, nil
					}
					m.finding = true
					m.setMessage("Finding tracker candidates…", messageToneNeutral)
					return m, m.findCandidatesFor(definitions[m.mappingTracker], m.input)
				}
				return m, m.confirmMapping()
			} else {
				m.applySetting()
			}
		case tea.KeyBackspace, tea.KeyCtrlH:
			if len(m.input) > 0 {
				_, size := utf8.DecodeLastRuneInString(m.input)
				m.input = m.input[:len(m.input)-size]
			}
		case tea.KeyRunes:
			m.input += string(key.Runes)
		}
		return m, nil
	}

	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "1":
		if m.view == matchesView {
			m.setMatchesMode(matchesNeedsMapping)
			return m, nil
		}
		if m.view == eventsView {
			m.setTrackingMode(trackingNeedsAction)
			return m, nil
		}
	case "2":
		if m.view == matchesView {
			m.setMatchesMode(matchesConfirmed)
			return m, nil
		}
		if m.view == eventsView {
			m.setTrackingMode(trackingCompleted)
			return m, nil
		}
	case "3":
		if m.view == matchesView {
			m.setMatchesMode(matchesRecent)
			return m, nil
		}
		if m.view == eventsView {
			m.setTrackingMode(trackingAll)
			return m, nil
		}
	case "tab", "right", "l":
		m.view = (m.view + 1) % viewCount
		m.selected = 0
		m.trackerDetail = false
		m.managerDiagnostics = false
		if m.view == eventsView {
			m.reloadEvents()
		}
		if m.view == matchesView {
			m.reloadMediaUnits()
		}
	case "shift+tab", "left", "h":
		m.view = (m.view + viewCount - 1) % viewCount
		m.selected = 0
		m.trackerDetail = false
		m.managerDiagnostics = false
		if m.view == eventsView {
			m.reloadEvents()
		}
		if m.view == matchesView {
			m.reloadMediaUnits()
		}
	case "r":
		m.reloadEvents()
		m.reloadMediaUnits()
	case "R":
		if m.view == eventsView && !m.trackingRetrying && m.selected >= 0 && m.selected < len(m.events) {
			event := m.events[m.selected]
			if !isRetryableEvent(event) {
				m.setMessage("Only items needing action can be retried. Completed records are never replayed.", messageToneWarning)
				return m, nil
			}
			m.trackingRetrying = true
			m.setMessage("Retrying the selected watched file through the guarded reconciliation path…", messageToneNeutral)
			return m, m.retryTrackingEvents([]watch.Event{event})
		}
	case "A":
		if m.view == eventsView && !m.trackingRetrying {
			events := make([]watch.Event, 0, m.trackingNeedsCount())
			for _, event := range m.allEvents {
				if isRetryableEvent(event) {
					events = append(events, event)
				}
			}
			if len(events) == 0 {
				m.setMessage("There are no watched files that need retrying.", messageToneNeutral)
				return m, nil
			}
			m.trackingRetrying = true
			m.setMessage("Retrying all items that need action through the guarded reconciliation path…", messageToneNeutral)
			return m, m.retryTrackingEvents(events)
		}
	case "S":
		if m.view == eventsView && !m.trackingSyncing && m.selected >= 0 && m.selected < len(m.events) {
			if !m.trackerConfig("anilist").SyncProgress {
				m.setMessage("AniList progress sync is off. Enable Sync watched progress in the AniList tracker first.", messageToneWarning)
				return m, nil
			}
			event := m.events[m.selected]
			m.trackingSyncing = true
			m.setMessage("AniList sync: preparing the selected watched file…", messageToneNeutral)
			return m, m.syncSelectedAniList(event)
		}
	case "a":
		if m.view == trackersView && !m.trackerDetail {
			m.trackerAdding = !m.trackerAdding
			m.trackerSelected = 0
			if definitions := m.trackerListDefinitions(); len(definitions) > 0 {
				m.trackerService = definitions[0].Service
			}
			return m, nil
		}
	case "o":
		if m.view == matchesView && m.selected >= 0 && m.selected < len(m.mediaUnits) {
			m.openConfirmedMapping()
			return m, nil
		}
	case "up", "k":
		if m.view == eventsView && m.selected > 0 {
			m.selected--
			return m, nil
		}
		if m.view == trackersView && !m.trackerDetail && len(m.trackerListDefinitions()) > 0 {
			m.moveTrackerSelection(-1)
			return m, nil
		}
		if m.view == matchesView && m.selected > 0 {
			m.selected--
			return m, nil
		}
		if m.isEditableView() && m.selected > 0 {
			m.selected--
		}
	case "down", "j":
		if m.view == eventsView && m.selected < len(m.events)-1 {
			m.selected++
			return m, nil
		}
		if m.view == trackersView && !m.trackerDetail && len(m.trackerListDefinitions()) > 0 {
			m.moveTrackerSelection(1)
			return m, nil
		}
		if m.view == matchesView && m.selected < len(m.mediaUnits)-1 {
			m.selected++
			return m, nil
		}
		if m.isEditableView() && m.selected < len(m.settings())-1 {
			m.selected++
		}
	case "home", "g":
		if m.isEditableView() {
			m.selected = 0
		}
	case "end", "G":
		if m.isEditableView() {
			m.selected = len(m.settings()) - 1
		}
	case "enter", " ":
		if m.view == dashboardView {
			if m.trackingNeedsCount() > 0 {
				m.view, m.selected = eventsView, 0
				m.setTrackingMode(trackingNeedsAction)
			} else if needs, _, _ := m.matchModeCounts(); needs > 0 {
				m.view, m.selected = matchesView, 0
				m.setMatchesMode(matchesNeedsMapping)
			} else {
				m.setMessage("Nothing needs action. Review Tracking or Matches for history.", messageToneNeutral)
			}
			return m, nil
		}
		if m.view == eventsView && len(m.events) > 0 {
			m.trackingDetail = !m.trackingDetail
			return m, nil
		}
		if m.view == matchesView && len(m.mediaUnits) > 0 {
			if len(m.eligibleTrackerDefinitions(m.mediaUnits[m.selected])) == 0 {
				m.setMessage("No enabled tracker accepts this scope. Select a season for AniList, AniDB, or MyAnimeList.", messageToneWarning)
				return m, nil
			}
			if mappings := m.enabledMappingsForUnit(m.mediaUnits[m.selected]); len(mappings) > 0 {
				m.activeMapping = mappings[0]
				m.mappingAction = 0
				m.mappingActionSelecting = true
				m.setMessage("Manage the confirmed mapping or replace it after review.", messageToneNeutral)
				return m, nil
			}
			m.mappingSelecting = true
			m.mappingTracker = 0
			m.setMessage("Choose the tracker whose exact ID you want to confirm.", messageToneNeutral)
			return m, nil
		}
		if m.view == trackersView && !m.trackerDetail && len(m.trackerListDefinitions()) > 0 {
			m.trackerDetail = true
			m.selected = 0
			m.setMessage("Configure the tracker, then link its account token outside config.toml.", messageToneNeutral)
			return m, nil
		}
		if m.isEditableView() {
			item := m.settings()[m.selected]
			if isBooleanSetting(item.id) {
				m.toggleSetting(item)
				return m, nil
			}
			m.beginSelection(item)
			return m, nil
		}
	case "v":
		if service := m.managerService(); service != "" && !m.validating {
			m.validating = true
			m.setManagerMessage(service, "Validating "+titleService(service)+" connection…", messageToneNeutral)
			return m, m.validateConnection(service)
		}
	case "d":
		if m.managerService() != "" {
			m.managerDiagnostics = !m.managerDiagnostics
			return m, nil
		}
	case "L", "shift+l":
		if m.view == trackersView && m.trackerDetail && !m.linking {
			definition, ok := m.selectedTrackerDefinition()
			if !ok {
				m.setMessage("Choose a tracker first.", messageToneWarning)
				return m, nil
			}
			if definition.Service == tracker.AniDB {
				m.setMessage("AniDB does not need account linking. Enable Use for matching, then confirm a season-level AID in Matches.", messageToneNeutral)
				return m, nil
			}
			trackerConfig := m.trackerConfig(string(definition.Service))
			if !trackerConfig.Enabled {
				m.setMessage("Turn Link tracker on after entering its client ID before authorizing an account.", messageToneWarning)
				return m, nil
			}
			m.linking = true
			m.setMessage("Opening the browser for "+definition.Name+" authorization…", messageToneNeutral)
			return m, m.linkTracker(definition.Service, trackerConfig)
		}
	}
	if key.String() == "esc" && m.view == trackersView && m.trackerDetail {
		m.trackerDetail = false
		m.selected = 0
		m.setMessage("Tracker selection.", messageToneNeutral)
	}
	if key.String() == "esc" && m.view == eventsView && m.trackingDetail {
		m.trackingDetail = false
		m.setMessage("Tracking list.", messageToneNeutral)
	}
	return m, nil
}

func (m *Model) beginSelection(item setting) {
	m.editing = true
	m.selecting = true
	m.selectionOptions = selectionOptions(item)
	m.selectionTitle = "Choose " + item.name
	m.choice = optionIndex(m.selectionOptions, item.value)
	m.setMessage("Choose an action; Enter applies it and Esc cancels.", messageToneNeutral)
}

func (m *Model) setMessage(message string, tone messageTone) {
	m.message = message
	m.messageTone = tone
}

func (m *Model) setManagerMessage(service, message string, tone messageTone) {
	if m.managerMessages == nil {
		m.managerMessages = make(map[string]messageState)
	}
	m.managerMessages[service] = messageState{text: message, tone: tone}
}

func (m *Model) displayedMessage() messageState {
	if service := m.managerService(); service != "" {
		if message, ok := m.managerMessages[service]; ok {
			return message
		}
	}
	return messageState{text: m.message, tone: m.messageTone}
}

func (m *Model) validateConnection(service string) tea.Cmd {
	cfg := *m.config
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), secrets.ForegroundResolveTimeout)
		defer cancel()
		manager := arr.Manager(service)
		instance, err := m.validate(ctx, &cfg, manager)
		return validationResultMsg{service: service, instance: instance, err: err}
	}
}

func (m *Model) isEditableView() bool {
	return m.view == settingsView || m.view == sonarrView || m.view == radarrView || (m.view == trackersView && m.trackerDetail)
}

func (m *Model) managerService() string {
	switch m.view {
	case sonarrView:
		return "sonarr"
	case radarrView:
		return "radarr"
	default:
		return ""
	}
}

func titleService(service string) string {
	if service == "sonarr" {
		return "Sonarr"
	}
	return "Radarr"
}

func trackerTitle(service tracker.Service) string {
	definition, ok := tracker.Lookup(string(service))
	if ok {
		return definition.Name
	}
	return string(service)
}

type setting struct {
	id                settingID
	name, value, hint string
}

type choiceOption struct {
	value, label string
	custom       bool
}

func secretSourceChoices() []choiceOption {
	return []choiceOption{
		{"keyring", "This computer's secure keychain (recommended)", false},
		{"1password", "1Password", false},
		{"environment", "Environment variable (advanced)", false},
	}
}

func optionIndex(options []choiceOption, value string) int {
	for index, option := range options {
		if option.value == value {
			return index
		}
	}
	for index, option := range options {
		if option.custom {
			return index
		}
	}
	return 0
}

func customOption() choiceOption {
	return choiceOption{label: "Enter a different custom value…", custom: true}
}

func textValueOptions(item setting) []choiceOption {
	if item.value == "" {
		return []choiceOption{{value: "", label: "Keep blank", custom: false}, customOption()}
	}
	return []choiceOption{{value: item.value, label: "Keep current value — " + truncate(item.value, 42), custom: false}, customOption()}
}

// keepCurrentOption makes Enter safe. If a configuration file contains a
// valid value outside our usual presets, it is presented as a normal selected
// choice instead of silently focusing the text-entry action.
func keepCurrentOption(options []choiceOption, current string) []choiceOption {
	for _, option := range options {
		if option.value == current {
			return options
		}
	}
	if current == "" {
		return append([]choiceOption{{value: "", label: "Keep blank"}}, options...)
	}
	return append([]choiceOption{{value: current, label: "Keep current value — " + truncate(current, 42)}}, options...)
}

func profileOptions() []choiceOption {
	return []choiceOption{{"default", "Default", false}, customOption()}
}

func vlcEndpointOptions() []choiceOption {
	return []choiceOption{{"http://127.0.0.1:8080", "VLC HTTP — 127.0.0.1:8080", false}, {"http://127.0.0.1:8081", "VLC HTTP — 127.0.0.1:8081", false}, customOption()}
}

func managerEndpointOptions(name, endpoint string) []choiceOption {
	return []choiceOption{{endpoint, name + " local default — " + strings.TrimPrefix(endpoint, "http://"), false}, customOption()}
}

func thresholdOptions() []choiceOption {
	return []choiceOption{{"70", "70%", false}, {"75", "75%", false}, {"80", "80%", false}, {"85", "85%", false}, {"90", "90%", false}, {"95", "95%", false}, {"100", "100%", false}}
}

func pollIntervalOptions() []choiceOption {
	return []choiceOption{{"1s", "Every second", false}, {"2s", "Every 2 seconds", false}, {"5s", "Every 5 seconds", false}, {"10s", "Every 10 seconds", false}, {"15s", "Every 15 seconds", false}, {"30s", "Every 30 seconds", false}, {"1m", "Every minute", false}}
}

// selectionOptions centralizes every finite-value picker. Text entry is kept
// for user-owned values only: paths, custom URLs, secret references, and OAuth
// application identifiers.
func selectionOptions(item setting) []choiceOption {
	if isSecretSource(item.id) {
		return secretSourceChoices()
	}
	var options []choiceOption
	switch item.id {
	case profileSetting:
		options = profileOptions()
	case vlcEndpointSetting:
		options = vlcEndpointOptions()
	case sonarrEndpointSetting:
		options = managerEndpointOptions("Sonarr", "http://127.0.0.1:8989")
	case radarrEndpointSetting:
		options = managerEndpointOptions("Radarr", "http://127.0.0.1:7878")
	case episodeThresholdSetting, movieThresholdSetting:
		options = thresholdOptions()
	case pollIntervalSetting:
		options = pollIntervalOptions()
	default:
		return textValueOptions(item)
	}
	return keepCurrentOption(options, item.value)
}

func isSecretSource(id settingID) bool {
	if strings.HasPrefix(string(id), "trackers.") && (strings.HasSuffix(string(id), ".secret_source") || strings.HasSuffix(string(id), ".client_secret_source")) {
		return true
	}
	switch id {
	case vlcSecretSourceSetting, sonarrSecretSourceSetting, radarrSecretSourceSetting:
		return true
	default:
		return false
	}
}

func isBooleanSetting(id settingID) bool {
	if strings.HasPrefix(string(id), "trackers.") && (strings.HasSuffix(string(id), ".enabled") || strings.HasSuffix(string(id), ".sync_progress")) {
		return true
	}
	switch id {
	case sonarrUnmonitorSetting, radarrUnmonitorSetting, sonarrMetadataSetting, radarrMetadataSetting:
		return true
	default:
		return false
	}
}

func (m *Model) toggleSetting(item setting) {
	current := item.value == "[ ON ]"
	m.input = strconv.FormatBool(!current)
	m.editing = true
	m.applySetting()
	// A toggle is never a text field. Validation can reject a state change
	// (for example, enabling a tracker before its client ID is configured),
	// but it must return to the normal row with an actionable message.
	if m.editing {
		m.editing = false
	}
}

func (m *Model) selectSecretReference() {
	var target settingID
	source := m.input
	switch m.settings()[m.selected].id {
	case vlcSecretSourceSetting:
		target = secretValueSettingID(source, vlcSecretReferenceSetting, vlcPasswordEnvSetting)
	case sonarrSecretSourceSetting:
		target = secretValueSettingID(source, sonarrSecretRefSetting, sonarrAPIKeyEnvSetting)
	case radarrSecretSourceSetting:
		target = secretValueSettingID(source, radarrSecretRefSetting, radarrAPIKeyEnvSetting)
	default:
		if strings.HasPrefix(string(m.settings()[m.selected].id), "trackers.") {
			current := string(m.settings()[m.selected].id)
			if strings.HasSuffix(current, ".client_secret_source") {
				target = secretValueSettingID(source, settingID(strings.TrimSuffix(current, ".client_secret_source")+".client_secret_reference"), settingID(strings.TrimSuffix(current, ".client_secret_source")+".client_secret_env"))
			} else {
				target = secretValueSettingID(source, settingID(strings.TrimSuffix(current, ".secret_source")+".secret_reference"), settingID(strings.TrimSuffix(current, ".secret_source")+".access_token_env"))
			}
		} else {
			return
		}
	}
	for index, item := range m.settings() {
		if item.id == target {
			m.selected = index
			return
		}
	}
}

func secretValueSettingID(source string, referenceID, environmentID settingID) settingID {
	if source == "environment" {
		return environmentID
	}
	return referenceID
}

func secretValueSetting(source string, referenceID settingID, referenceValue, referenceService string, environmentID settingID, environmentValue, environmentExample, environmentLabel string) setting {
	if source == "environment" {
		return setting{environmentID, environmentLabel, environmentValue, environmentHint(source, environmentExample)}
	}
	return setting{referenceID, secretReferenceLabel(source), referenceValue, secretReferenceHint(referenceService, source)}
}

func (m *Model) settings() []setting {
	switch m.view {
	case trackersView:
		if !m.trackerDetail {
			return nil
		}
		definition, ok := m.selectedTrackerDefinition()
		if !ok {
			return nil
		}
		trackerConfig := m.trackerConfig(string(definition.Service))
		if definition.Service == tracker.AniDB {
			return []setting{{settingID("trackers.anidb.enabled"), "Use for matching", formatToggle(trackerConfig.Enabled), "Enables locally cached AniDB title search and season-level AID confirmations; no account link is needed"}}
		}
		prefix := "trackers." + string(definition.Service)
		clientIDHint := "Create a personal OAuth application with this tracker, then enter its public client ID"
		if definition.Service == tracker.AniList {
			clientIDHint = "Enter the numeric value stored in your VLC_MEDIA_WATCHER_ID field, not that field's label or an op:// reference"
		}
		settings := []setting{
			{settingID(prefix + ".client_id"), "Application client ID", trackerConfig.ClientID, clientIDHint},
			{settingID(prefix + ".client_secret_source"), "Client secret location", trackerConfig.ClientSecretSource, "Needed by AniList and Trakt; choose secure keychain, 1Password, or an environment variable"},
			secretValueSetting(trackerConfig.ClientSecretSource, settingID(prefix+".client_secret_reference"), trackerConfig.ClientSecretReference, definition.Name+" client secret", settingID(prefix+".client_secret_env"), trackerConfig.ClientSecretEnv, "VLC_MEDIA_WATCHER_"+strings.ToUpper(string(definition.Service))+"_CLIENT_SECRET", "Client secret variable"),
			{settingID(prefix + ".enabled"), "Link tracker", formatToggle(trackerConfig.Enabled), "Enter or Space toggles it after the application client ID is configured"},
		}
		if definition.Service == tracker.AniList {
			settings = append(settings, setting{settingID(prefix + ".sync_progress"), "Sync watched progress", formatToggle(trackerConfig.SyncProgress), "Off by default. When on, each confirmed Sonarr watch automatically advances AniList through its watched episode; S backfills a historical record"})
		}
		return settings
	case sonarrView:
		settings := []setting{
			{sonarrMetadataSetting, "Use for tracker metadata", formatToggle(m.config.Sonarr.MetadataLookup), "Read exact file metadata to create tracker-match entries; never writes to Sonarr"},
			{sonarrUnmonitorSetting, "Unmonitor after watch", formatToggle(m.config.Sonarr.UnmonitoringEnabled()), "Enter or Space toggles it. On stops future grabs/upgrades for matched episodes"},
			{sonarrEndpointSetting, "Endpoint", m.config.Sonarr.Endpoint, "Choose the local default or enter a custom base URL; do not add /api/v3"},
			{sonarrSecretSourceSetting, "API key location", m.config.Sonarr.SecretSource, "Choose secure keychain, 1Password, or an environment variable"},
			{sonarrLocalPrefixSetting, "VLC's TV folder", m.config.Sonarr.LocalPathPrefix, "Leave blank unless VLC sees the files in a different folder, e.g. /Volumes/Media/TV"},
			{sonarrRemotePrefixSetting, "Sonarr's TV folder", m.config.Sonarr.RemotePathPrefix, "Leave blank unless Sonarr calls that same folder something else, e.g. /tv"},
		}
		credential := secretValueSetting(m.config.Sonarr.SecretSource, sonarrSecretRefSetting, m.config.Sonarr.SecretReference, "Sonarr", sonarrAPIKeyEnvSetting, m.config.Sonarr.APIKeyEnv, "SONARR_API_KEY", "API-key variable")
		return append(settings[:4], append([]setting{credential}, settings[4:]...)...)
	case radarrView:
		settings := []setting{
			{radarrMetadataSetting, "Use for tracker metadata", formatToggle(m.config.Radarr.MetadataLookup), "Read exact file metadata to create tracker-match entries; never writes to Radarr"},
			{radarrUnmonitorSetting, "Unmonitor after watch", formatToggle(m.config.Radarr.UnmonitoringEnabled()), "Enter or Space toggles it. On stops future grabs/upgrades for matched movies"},
			{radarrEndpointSetting, "Endpoint", m.config.Radarr.Endpoint, "Choose the local default or enter a custom base URL; do not add /api/v3"},
			{radarrSecretSourceSetting, "API key location", m.config.Radarr.SecretSource, "Choose secure keychain, 1Password, or an environment variable"},
			{radarrLocalPrefixSetting, "VLC's movie folder", m.config.Radarr.LocalPathPrefix, "Leave blank unless VLC sees the files in a different folder, e.g. /Volumes/Media/Movies"},
			{radarrRemotePrefixSetting, "Radarr's movie folder", m.config.Radarr.RemotePathPrefix, "Leave blank unless Radarr calls that same folder something else, e.g. /movies"},
		}
		credential := secretValueSetting(m.config.Radarr.SecretSource, radarrSecretRefSetting, m.config.Radarr.SecretReference, "Radarr", radarrAPIKeyEnvSetting, m.config.Radarr.APIKeyEnv, "RADARR_API_KEY", "API-key variable")
		return append(settings[:4], append([]setting{credential}, settings[4:]...)...)
	default:
		settings := []setting{
			{profileSetting, "Profile", m.config.Profile, "Choose the default profile or enter a custom local name"},
			{vlcEndpointSetting, "VLC endpoint", m.config.VLC.Endpoint, "Choose a common local endpoint or enter a custom URL"},
			{vlcSecretSourceSetting, "Password location", m.config.VLC.SecretSource, "Choose secure keychain, 1Password, or an environment variable"},
			{episodeThresholdSetting, "Episode threshold", percent(m.config.Watch.EpisodeThreshold), "Choose the percentage watched before recording an episode"},
			{movieThresholdSetting, "Movie threshold", percent(m.config.Watch.MovieThreshold), "Choose the percentage watched before recording a movie"},
			{pollIntervalSetting, "Poll interval", m.config.Watch.PollInterval.String(), "Choose how often VLC is checked"},
			{databasePathSetting, "Database path", m.config.Storage.Path, "Blank uses the operating-system default"},
		}
		credential := secretValueSetting(m.config.VLC.SecretSource, vlcSecretReferenceSetting, m.config.VLC.SecretReference, "VLC", vlcPasswordEnvSetting, m.config.VLC.PasswordEnv, "VLC_PASSWORD", "Password variable")
		return append(settings[:3], append([]setting{credential}, settings[3:]...)...)
	}
}

func secretReferenceLabel(source string) string {
	if source == "1password" {
		return "1Password item"
	}
	return "Saved key name"
}

func secretReferenceHint(service, source string) string {
	switch source {
	case "1password":
		return fmt.Sprintf("Example: op://Private/%s/password", service)
	default:
		return fmt.Sprintf("Leave this name as-is, then run: vlc-media-watcher secret set %s", strings.ToLower(service))
	}
}

func environmentHint(source, example string) string {
	if source == "environment" {
		return fmt.Sprintf("The variable's name, not the key itself; for example %s", example)
	}
	return "Only used when API key location is Environment variable"
}

func percent(value float64) string { return fmt.Sprintf("%.0f", value*100) }

func formatBool(value bool) string { return strconv.FormatBool(value) }

func formatToggle(value bool) string {
	if value {
		return "[ ON ]"
	}
	return "[ OFF ]"
}

func (m *Model) applySetting() {
	value := strings.TrimSpace(m.input)
	candidate := *m.config
	if m.config.Trackers != nil {
		candidate.Trackers = make(map[string]config.TrackerConfig, len(m.config.Trackers))
		for name, trackerConfig := range m.config.Trackers {
			candidate.Trackers[name] = trackerConfig
		}
	}
	var err error
	settings := m.settings()
	if m.selected < 0 || m.selected >= len(settings) {
		m.setMessage("Not saved: no setting is selected", messageToneWarning)
		return
	}
	currentSetting := settings[m.selected].id
	if strings.HasPrefix(string(currentSetting), "trackers.") {
		name, field, ok := splitTrackerSetting(currentSetting)
		if !ok {
			err = fmt.Errorf("unknown tracker setting")
		} else {
			trackerConfig := m.trackerConfig(name)
			switch field {
			case "enabled":
				trackerConfig.Enabled, err = parseBool(value)
			case "sync_progress":
				trackerConfig.SyncProgress, err = parseBool(value)
			case "client_id":
				trackerConfig.ClientID = value
			case "client_secret_source":
				trackerConfig.ClientSecretSource = value
			case "client_secret_reference":
				trackerConfig.ClientSecretReference = value
			case "client_secret_env":
				trackerConfig.ClientSecretEnv = value
			case "secret_source":
				trackerConfig.SecretSource = value
			case "secret_reference":
				trackerConfig.SecretReference = value
			case "access_token_env":
				trackerConfig.AccessTokenEnv = value
			default:
				err = fmt.Errorf("unknown tracker setting")
			}
			if candidate.Trackers == nil {
				candidate.Trackers = make(map[string]config.TrackerConfig)
			}
			candidate.Trackers[name] = trackerConfig
		}
	} else {
		switch currentSetting {
		case profileSetting:
			candidate.Profile = value
		case vlcEndpointSetting:
			candidate.VLC.Endpoint = value
		case vlcSecretSourceSetting:
			candidate.VLC.SecretSource = value
		case vlcSecretReferenceSetting:
			candidate.VLC.SecretReference = value
		case vlcPasswordEnvSetting:
			candidate.VLC.PasswordEnv = value
		case episodeThresholdSetting:
			candidate.Watch.EpisodeThreshold, err = parsePercent(value)
		case movieThresholdSetting:
			candidate.Watch.MovieThreshold, err = parsePercent(value)
		case pollIntervalSetting:
			candidate.Watch.PollInterval, err = time.ParseDuration(value)
		case databasePathSetting:
			candidate.Storage.Path = value
		case sonarrUnmonitorSetting:
			candidate.Sonarr.UnmonitorAfterWatch, err = parseBool(value)
			candidate.Sonarr.UpdateMonitored = false
			candidate.Sonarr.MonitoredAfterWatch = false
		case sonarrMetadataSetting:
			candidate.Sonarr.MetadataLookup, err = parseBool(value)
		case sonarrEndpointSetting:
			candidate.Sonarr.Endpoint = value
		case sonarrSecretSourceSetting:
			candidate.Sonarr.SecretSource = value
		case sonarrSecretRefSetting:
			candidate.Sonarr.SecretReference = value
		case sonarrAPIKeyEnvSetting:
			candidate.Sonarr.APIKeyEnv = value
		case sonarrLocalPrefixSetting:
			candidate.Sonarr.LocalPathPrefix = value
		case sonarrRemotePrefixSetting:
			candidate.Sonarr.RemotePathPrefix = value
		case radarrUnmonitorSetting:
			candidate.Radarr.UnmonitorAfterWatch, err = parseBool(value)
			candidate.Radarr.UpdateMonitored = false
			candidate.Radarr.MonitoredAfterWatch = false
		case radarrMetadataSetting:
			candidate.Radarr.MetadataLookup, err = parseBool(value)
		case radarrEndpointSetting:
			candidate.Radarr.Endpoint = value
		case radarrSecretSourceSetting:
			candidate.Radarr.SecretSource = value
		case radarrSecretRefSetting:
			candidate.Radarr.SecretReference = value
		case radarrAPIKeyEnvSetting:
			candidate.Radarr.APIKeyEnv = value
		case radarrLocalPrefixSetting:
			candidate.Radarr.LocalPathPrefix = value
		case radarrRemotePrefixSetting:
			candidate.Radarr.RemotePathPrefix = value
		}
	}
	if err == nil {
		err = config.Save(m.configPath, &candidate)
	}
	if err != nil {
		m.setMessage("Not saved: "+err.Error(), messageToneWarning)
		return
	}
	m.config = &candidate
	if name, field, ok := splitTrackerSetting(currentSetting); ok && field == "enabled" {
		m.trackerService = tracker.Service(name)
		if m.trackerConfig(name).Enabled {
			m.trackerAdding = false
		}
	}
	m.editing = false
	m.setMessage("Saved. New watcher processes will use this setting.", messageToneSuccess)
	m.reloadEvents()
	m.reloadMediaUnits()
}

func splitTrackerSetting(id settingID) (name, field string, ok bool) {
	parts := strings.Split(string(id), ".")
	if len(parts) != 3 || parts[0] != "trackers" {
		return "", "", false
	}
	if _, found := tracker.Lookup(parts[1]); !found {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func (m *Model) trackerConfig(name string) config.TrackerConfig {
	trackerConfig := m.config.Trackers[name]
	if trackerConfig.SecretSource == "" {
		trackerConfig.SecretSource = "keyring"
	}
	if trackerConfig.ClientSecretSource == "" {
		trackerConfig.ClientSecretSource = "keyring"
	}
	if trackerConfig.ClientSecretReference == "" {
		trackerConfig.ClientSecretReference = m.config.Profile + "/" + name + "-client-secret"
	}
	if trackerConfig.ClientSecretEnv == "" {
		trackerConfig.ClientSecretEnv = "VLC_MEDIA_WATCHER_" + strings.ToUpper(name) + "_CLIENT_SECRET"
	}
	if trackerConfig.SecretReference == "" {
		trackerConfig.SecretReference = m.config.Profile + "/" + name + "-access-token"
	}
	if trackerConfig.AccessTokenEnv == "" {
		trackerConfig.AccessTokenEnv = "VLC_MEDIA_WATCHER_" + strings.ToUpper(name) + "_ACCESS_TOKEN"
	}
	return trackerConfig
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "t", "1", "yes", "y", "on", "enabled", "enable":
		return true, nil
	case "false", "f", "0", "no", "n", "off", "disabled", "disable":
		return false, nil
	default:
		return false, fmt.Errorf("enter true or false (yes/no and on/off also work)")
	}
}

func parsePercent(value string) (float64, error) {
	value = strings.TrimSuffix(strings.TrimSpace(value), "%")
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("enter a percentage from 1 to 100")
	}
	if parsed > 1 {
		parsed /= 100
	}
	if parsed <= 0 || parsed > 1 {
		return 0, fmt.Errorf("enter a percentage from 1 to 100")
	}
	return parsed, nil
}

func (m *Model) reloadEvents() {
	db, err := store.Open(m.config.DatabasePath())
	if err != nil {
		m.setMessage("Could not open event store: "+err.Error(), messageToneWarning)
		return
	}
	defer db.Close()
	m.allEvents, err = db.RecentEvents(100)
	if err != nil {
		m.setMessage("Could not load events: "+err.Error(), messageToneWarning)
		return
	}
	m.heartbeat, m.heartbeatKnown, err = db.LatestWatcherHeartbeat()
	if err != nil {
		m.setMessage("Could not load watcher health: "+err.Error(), messageToneWarning)
	}
	m.integrationChecks = make(map[string]store.IntegrationCheck)
	m.syncJobs = make(map[string]store.TrackerSyncJob)
	for _, event := range m.allEvents {
		job, found, jobErr := db.TrackerSyncJob(event.MediaPath, string(tracker.AniList))
		if jobErr != nil {
			m.setMessage("Could not load AniList sync state: "+jobErr.Error(), messageToneWarning)
			continue
		}
		if found {
			m.syncJobs[event.MediaPath] = job
		}
	}
	for _, service := range []string{"sonarr", "radarr", "anilist", "anidb", "myanimelist", "trakt", "simkl"} {
		check, found, checkErr := db.IntegrationCheck(service)
		if checkErr != nil {
			m.setMessage("Could not load integration health: "+checkErr.Error(), messageToneWarning)
			continue
		}
		if found {
			m.integrationChecks[service] = check
		}
	}
	m.refreshVisibleEvents()
}

func isRetryableEvent(event watch.Event) bool {
	return event.Status == "pending" || event.Status == "unmatched" || event.Status == "failed"
}

func (m *Model) eventNeedsAttention(event watch.Event) bool {
	if isRetryableEvent(event) {
		return true
	}
	if job, found := m.syncJobs[event.MediaPath]; found {
		return job.Status == "failed" || job.Status == "review"
	}
	return false
}

func (m *Model) refreshVisibleEvents() {
	visible := make([]watch.Event, 0, len(m.allEvents))
	for _, event := range m.allEvents {
		switch m.trackingMode {
		case trackingNeedsAction:
			if m.eventNeedsAttention(event) {
				visible = append(visible, event)
			}
		case trackingCompleted:
			if !m.eventNeedsAttention(event) {
				visible = append(visible, event)
			}
		default:
			visible = append(visible, event)
		}
	}
	m.events = visible
	if m.view == eventsView && m.selected >= len(m.events) {
		m.selected = max(0, len(m.events)-1)
	}
}

func (m *Model) setTrackingMode(mode trackingMode) {
	m.trackingMode = mode
	m.trackingDetail = false
	m.selected = 0
	m.refreshVisibleEvents()
}

func (m *Model) retryTrackingEvents(events []watch.Event) tea.Cmd {
	cfg := *m.config
	return func() tea.Msg {
		setupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		reconciler := reconcile.New(setupCtx, &cfg)
		cancel()
		if !reconciler.Active() && !trackerFallbackEnabled(cfg) {
			return trackingRetryResultMsg{err: fmt.Errorf("enable Sonarr/Radarr watched-file resolution or a tracker fallback before retrying")}
		}
		if problems := reconciler.Problems(); len(problems) > 0 {
			return trackingRetryResultMsg{err: fmt.Errorf("cannot retry: %s", problems[0])}
		}
		db, err := store.Open(cfg.DatabasePath())
		if err != nil {
			return trackingRetryResultMsg{err: err}
		}
		defer db.Close()
		resolved := 0
		for _, event := range events {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			outcome := reconciler.Process(ctx, event.MediaPath)
			cancel()
			if outcome.Match != nil && outcome.Match.Identity.SourceID > 0 {
				if _, err := db.UpsertIdentity(outcome.Match.Identity); err != nil {
					return trackingRetryResultMsg{attempted: resolved, resolved: resolved, err: err}
				}
				event.Manager = string(outcome.Match.Identity.Manager)
				event.SourceID = outcome.Match.Identity.SourceID
				event.SeasonNumber = outcome.Match.Identity.SeasonNumber
				event.EpisodeNumbers = append([]int(nil), outcome.Match.Identity.EpisodeNumbers...)
				if err := db.UpdateEventResolution(event); err != nil {
					return trackingRetryResultMsg{attempted: resolved, resolved: resolved, err: err}
				}
			} else if trackerFallbackEnabled(cfg) && (outcome.Status == reconcile.StatusLocal || outcome.Status == reconcile.StatusUnmatched) {
				identity, found, localErr := db.UpsertLocalParsedPath(event.MediaPath)
				if localErr != nil {
					return trackingRetryResultMsg{attempted: resolved, resolved: resolved, err: localErr}
				}
				if found {
					event.Manager = string(identity.Manager)
					event.SourceID = identity.SourceID
					event.SeasonNumber = identity.SeasonNumber
					event.EpisodeNumbers = append([]int(nil), identity.EpisodeNumbers...)
					if err := db.UpdateEventResolution(event); err != nil {
						return trackingRetryResultMsg{attempted: resolved, resolved: resolved, err: err}
					}
				}
			}
			if err := db.UpdateEventStatus(event.MediaPath, string(outcome.Status)); err != nil {
				return trackingRetryResultMsg{attempted: resolved, resolved: resolved, err: err}
			}
			if !isRetryableEvent(watch.Event{Status: string(outcome.Status)}) {
				resolved++
			}
		}
		return trackingRetryResultMsg{attempted: len(events), resolved: resolved}
	}
}

func trackerFallbackEnabled(cfg config.Config) bool {
	for _, trackerConfig := range cfg.Trackers {
		if trackerConfig.Enabled {
			return true
		}
	}
	return false
}

func (m *Model) syncSelectedAniList(event watch.Event) tea.Cmd {
	cfg := *m.config
	updates := make(chan tea.Msg)
	go func() {
		report := func(detail string) {
			updates <- trackingSyncProgressMsg{detail: detail, updates: updates}
		}
		report("Opening the local tracking record…")
		if !cfg.Trackers["anilist"].Enabled || !cfg.Trackers["anilist"].SyncProgress {
			updates <- trackingSyncResultMsg{err: fmt.Errorf("AniList progress sync is not enabled")}
			return
		}
		db, err := store.Open(cfg.DatabasePath())
		if err != nil {
			updates <- trackingSyncResultMsg{err: err}
			return
		}
		defer db.Close()
		if len(event.EpisodeNumbers) == 0 {
			report("Reading exact episode evidence from Sonarr…")
			setupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			reconciler := reconcile.NewForManager(setupCtx, &cfg, arr.Manager(event.Manager))
			cancel()
			if problems := reconciler.Problems(); len(problems) > 0 {
				updates <- trackingSyncResultMsg{err: fmt.Errorf("cannot refresh episode evidence: %s", problems[0])}
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			match, resolveErr := reconciler.ResolveForManager(ctx, event.MediaPath, arr.Manager(event.Manager))
			cancel()
			if resolveErr != nil {
				updates <- trackingSyncResultMsg{err: resolveErr}
				return
			}
			event.Manager = string(match.Identity.Manager)
			event.SourceID = match.Identity.SourceID
			event.SeasonNumber = match.Identity.SeasonNumber
			event.EpisodeNumbers = append([]int(nil), match.Identity.EpisodeNumbers...)
			if err := db.UpdateEventResolution(event); err != nil {
				updates <- trackingSyncResultMsg{err: err}
				return
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		job, err := tracker.SyncAniListEventWithProgress(ctx, cfg.Trackers["anilist"], db, event, report)
		cancel()
		if err != nil {
			updates <- trackingSyncResultMsg{job: job, err: err}
			return
		}
		if job.Status == "" {
			updates <- trackingSyncResultMsg{err: fmt.Errorf("this event has no confirmed AniList season mapping")}
			return
		}
		updates <- trackingSyncResultMsg{job: job}
	}()
	return waitForTrackingSync(updates)
}

func waitForTrackingSync(updates <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-updates
	}
}

func (m *Model) reloadMediaUnits() {
	db, err := store.Open(m.config.DatabasePath())
	if err != nil {
		return
	}
	defer db.Close()
	units, err := db.RecentMediaUnits(100)
	if err == nil {
		m.allMediaUnits = units
		m.mappings = make(map[int64][]store.TrackerMapping, len(units))
		for _, unit := range units {
			mappings, mappingErr := db.MappingsForUnit(unit.ID)
			if mappingErr == nil {
				m.mappings[unit.ID] = mappings
			}
		}
		m.refreshVisibleMatchUnits()
	}
}

func (m *Model) refreshVisibleMatchUnits() {
	visible := make([]store.MediaUnit, 0, len(m.allMediaUnits))
	for _, unit := range m.allMediaUnits {
		eligible := m.eligibleTrackerDefinitions(unit)
		if len(eligible) == 0 {
			continue
		}
		hasConfirmed, hasMissing := false, false
		for _, definition := range eligible {
			if _, found := mappingForTracker(m.mappings[unit.ID], definition.Service); found {
				hasConfirmed = true
			} else {
				hasMissing = true
			}
		}
		switch m.matchesMode {
		case matchesNeedsMapping:
			if hasMissing {
				visible = append(visible, unit)
			}
		case matchesConfirmed:
			if hasConfirmed {
				visible = append(visible, unit)
			}
		case matchesRecent:
			visible = append(visible, unit)
		}
	}
	m.mediaUnits = visible
	if m.view == matchesView && m.selected >= len(m.mediaUnits) {
		m.selected = max(0, len(m.mediaUnits)-1)
	}
}

func (m *Model) setMatchesMode(mode matchesMode) {
	m.matchesMode = mode
	m.selected = 0
	m.refreshVisibleMatchUnits()
}

func (m *Model) enabledTrackerDefinitions() []tracker.Definition {
	definitions := make([]tracker.Definition, 0, len(tracker.All()))
	for _, definition := range tracker.All() {
		if m.trackerConfig(string(definition.Service)).Enabled {
			definitions = append(definitions, definition)
		}
	}
	return definitions
}

func (m *Model) trackerListDefinitions() []tracker.Definition {
	if m.trackerAdding {
		definitions := make([]tracker.Definition, 0, len(tracker.All()))
		for _, definition := range tracker.All() {
			if !m.trackerConfig(string(definition.Service)).Enabled {
				definitions = append(definitions, definition)
			}
		}
		return definitions
	}
	return m.enabledTrackerDefinitions()
}

func (m *Model) selectedTrackerDefinition() (tracker.Definition, bool) {
	definitions := m.trackerListDefinitions()
	for _, definition := range definitions {
		if definition.Service == m.trackerService {
			return definition, true
		}
	}
	if len(definitions) == 0 {
		if m.trackerDetail {
			all := tracker.All()
			if m.trackerSelected >= 0 && m.trackerSelected < len(all) {
				return all[m.trackerSelected], true
			}
		}
		return tracker.Definition{}, false
	}
	m.trackerSelected = 0
	m.trackerService = definitions[0].Service
	return definitions[0], true
}

func (m *Model) moveTrackerSelection(delta int) {
	definitions := m.trackerListDefinitions()
	if len(definitions) == 0 {
		return
	}
	current := 0
	for index, definition := range definitions {
		if definition.Service == m.trackerService {
			current = index
			break
		}
	}
	current = (current + delta + len(definitions)) % len(definitions)
	m.trackerSelected = current
	m.trackerService = definitions[current].Service
}

func (m *Model) trackerMappingCounts(service tracker.Service) (confirmed, missing int) {
	for _, unit := range m.allMediaUnits {
		definition, found := tracker.Lookup(string(service))
		if !found || !definition.SupportsUnit(unit.Scope, unit.Kind) {
			continue
		}
		if _, found := mappingForTracker(m.mappings[unit.ID], service); found {
			confirmed++
		} else {
			missing++
		}
	}
	return
}

func (m *Model) trackerReadiness(definition tracker.Definition) readinessState {
	cfg := m.trackerConfig(string(definition.Service))
	if !cfg.Enabled {
		return readinessState{kind: "muted", label: "Disabled"}
	}
	if definition.Service == tracker.AniDB {
		return readinessState{kind: "info", label: "• Reference-only"}
	}
	if check, found := m.integrationChecks[string(definition.Service)]; found {
		if check.State == "linked" {
			return readinessState{kind: "linked", label: "✓ Linked"}
		}
		if check.State == "failed" {
			return readinessState{kind: "failed", label: "× Needs attention"}
		}
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return readinessState{kind: "action", label: "! Needs setup"}
	}
	return readinessState{kind: "action", label: "! Link account"}
}

func (m *Model) eligibleTrackerDefinitions(unit store.MediaUnit) []tracker.Definition {
	definitions := make([]tracker.Definition, 0, len(tracker.All()))
	for _, definition := range m.enabledTrackerDefinitions() {
		if definition.SupportsUnit(unit.Scope, unit.Kind) {
			definitions = append(definitions, definition)
		}
	}
	return definitions
}

func (m *Model) enabledMappingsForUnit(unit store.MediaUnit) []store.TrackerMapping {
	mappings := make([]store.TrackerMapping, 0, len(m.mappings[unit.ID]))
	for _, definition := range m.eligibleTrackerDefinitions(unit) {
		if mapping, found := mappingForTracker(m.mappings[unit.ID], definition.Service); found {
			mappings = append(mappings, mapping)
		}
	}
	return mappings
}

func (m *Model) beginReplacement() {
	if m.selected < 0 || m.selected >= len(m.mediaUnits) {
		m.setMessage("No media item is selected.", messageToneWarning)
		return
	}
	definitions := m.eligibleTrackerDefinitions(m.mediaUnits[m.selected])
	for index, definition := range definitions {
		if string(definition.Service) == m.activeMapping.Tracker {
			m.mappingTracker = index
			m.mappingSelecting = true
			m.setMessage("Search for a replacement, review it in the browser, then confirm it.", messageToneNeutral)
			return
		}
	}
	m.setMessage("The tracker for this mapping is no longer enabled for this scope.", messageToneWarning)
}

func (m *Model) deleteActiveMapping() {
	if m.selected < 0 || m.selected >= len(m.mediaUnits) {
		m.setMessage("No media item is selected.", messageToneWarning)
		return
	}
	unit := m.mediaUnits[m.selected]
	db, err := store.Open(m.config.DatabasePath())
	if err == nil {
		err = db.DeleteMapping(unit.ID, m.activeMapping.Tracker)
		db.Close()
	}
	if err != nil {
		m.setMessage("Could not remove mapping: "+err.Error(), messageToneWarning)
		return
	}
	m.setMessage("Removed local "+m.activeMapping.Tracker+" mapping. No remote tracker state was changed.", messageToneSuccess)
	m.reloadMediaUnits()
}

func (m *Model) confirmMapping() tea.Cmd {
	if m.selected < 0 || m.selected >= len(m.mediaUnits) {
		m.editing = false
		m.setMessage("No media item is selected.", messageToneWarning)
		return nil
	}
	definitions := m.eligibleTrackerDefinitions(m.mediaUnits[m.selected])
	if len(definitions) == 0 || m.mappingTracker >= len(definitions) {
		m.editing = false
		m.setMessage("No linked tracker is available.", messageToneWarning)
		return nil
	}
	trackerID := strings.TrimSpace(m.input)
	if trackerID == "" {
		m.setMessage("Not confirmed: enter the exact tracker ID.", messageToneWarning)
		return nil
	}
	unit := m.mediaUnits[m.selected]
	db, err := store.Open(m.config.DatabasePath())
	if err == nil {
		err = db.ConfirmMapping(store.TrackerMapping{MediaUnitID: unit.ID, Tracker: string(definitions[m.mappingTracker].Service), TrackerID: trackerID, TrackerTitle: unit.Title})
		db.Close()
	}
	if err != nil {
		m.setMessage("Not confirmed: "+err.Error(), messageToneWarning)
		return nil
	}
	m.editing = false
	m.reloadMediaUnits()
	return m.queueAniListCatchUp(unit, definitions[m.mappingTracker].Service)
}

func (m *Model) findCandidates(definition tracker.Definition) tea.Cmd {
	unit := m.mediaUnits[m.selected]
	return m.findCandidatesFor(definition, unit.Title)
}

func (m *Model) findCandidatesFor(definition tracker.Definition, query string) tea.Cmd {
	trackerConfig := m.trackerConfig(string(definition.Service))
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		candidates, err := m.search(ctx, definition.Service, strings.TrimSpace(query), trackerConfig.ClientID)
		return candidateResultMsg{service: definition.Service, candidates: candidates, err: err}
	}
}

func (m *Model) linkTracker(service tracker.Service, trackerConfig config.TrackerConfig) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		result, err := m.link(ctx, service, trackerConfig, tracker.OpenBrowser)
		return trackerLinkResultMsg{service: service, result: result, err: err}
	}
}

func (m *Model) confirmCandidate() tea.Cmd {
	if m.selected < 0 || m.selected >= len(m.mediaUnits) {
		m.candidatePicking = false
		m.setMessage("No media item is selected.", messageToneWarning)
		return nil
	}
	definitions := m.eligibleTrackerDefinitions(m.mediaUnits[m.selected])
	if len(definitions) == 0 || m.mappingTracker >= len(definitions) || m.candidateSelected >= len(m.candidates) {
		m.candidatePicking = false
		m.setMessage("Candidate selection is no longer available.", messageToneWarning)
		return nil
	}
	if m.selected < 0 || m.selected >= len(m.mediaUnits) {
		m.candidatePicking = false
		m.setMessage("No media item is selected.", messageToneWarning)
		return nil
	}
	unit, candidate := m.mediaUnits[m.selected], m.candidates[m.candidateSelected]
	db, err := store.Open(m.config.DatabasePath())
	if err == nil {
		err = db.ConfirmMapping(store.TrackerMapping{MediaUnitID: unit.ID, Tracker: string(definitions[m.mappingTracker].Service), TrackerID: candidate.ID, TrackerTitle: candidate.Title})
		db.Close()
	}
	if err != nil {
		m.setMessage("Not confirmed: "+err.Error(), messageToneWarning)
		return nil
	}
	m.candidatePicking, m.candidates = false, nil
	m.reloadMediaUnits()
	return m.queueAniListCatchUp(unit, definitions[m.mappingTracker].Service)
}

// queueAniListCatchUp uses Bubble Tea's asynchronous command path, keeping
// mapping confirmation responsive while historical watches are sent in order.
// A mapping is season-scoped, so it never syncs a different season by mistake.
func (m *Model) queueAniListCatchUp(unit store.MediaUnit, service tracker.Service) tea.Cmd {
	cfg := *m.config
	if service != tracker.AniList || !cfg.Trackers["anilist"].Enabled || !cfg.Trackers["anilist"].SyncProgress {
		m.setMessage("Confirmed locally. Future tracker syncs may use this exact ID only.", messageToneSuccess)
		return nil
	}
	m.trackingSyncing = true
	m.setMessage("Confirmed locally. Queuing completed episodes for AniList catch-up…", messageToneNeutral)
	return func() tea.Msg {
		db, err := store.Open(cfg.DatabasePath())
		if err != nil {
			return mappingSyncResultMsg{err: err}
		}
		defer db.Close()
		events, err := db.CompletedEventsForSeason(unit.Manager, unit.SourceID, unit.SeasonNumber)
		if err != nil {
			return mappingSyncResultMsg{err: err}
		}
		result := mappingSyncResultMsg{attempted: len(events)}
		for _, event := range events {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			job, syncErr := tracker.SyncAniListEvent(ctx, cfg.Trackers["anilist"], db, event)
			cancel()
			if syncErr != nil {
				result.err = syncErr
				return result
			}
			switch job.Status {
			case "synced", "already_synced":
				result.synced++
			case "review":
				result.review++
			}
		}
		return result
	}
}

func (m *Model) openSelectedCandidate() {
	if m.selected < 0 || m.selected >= len(m.mediaUnits) || m.candidateSelected < 0 || m.candidateSelected >= len(m.candidates) {
		m.setMessage("No candidate is selected.", messageToneWarning)
		return
	}
	definitions := m.eligibleTrackerDefinitions(m.mediaUnits[m.selected])
	if m.mappingTracker < 0 || m.mappingTracker >= len(definitions) {
		m.setMessage("Candidate tracker is no longer available.", messageToneWarning)
		return
	}
	candidate := m.candidates[m.candidateSelected]
	link, err := tracker.URLForID(definitions[m.mappingTracker].Service, candidate.ID)
	if err != nil {
		m.setMessage("Could not open candidate: "+err.Error(), messageToneWarning)
		return
	}
	if err := m.openURL(link); err != nil {
		m.setMessage("Could not open candidate: "+err.Error(), messageToneWarning)
		return
	}
	m.setMessage("Opened "+definitions[m.mappingTracker].Name+" for review. Press Enter only after verifying the entry.", messageToneSuccess)
}

func (m *Model) openConfirmedMapping() {
	if m.selected < 0 || m.selected >= len(m.mediaUnits) {
		m.setMessage("No media item is selected.", messageToneWarning)
		return
	}
	mappings := m.mappings[m.mediaUnits[m.selected].ID]
	if len(mappings) == 0 {
		m.setMessage("No confirmed tracker mapping is available to open. Search candidates first.", messageToneWarning)
		return
	}
	if len(mappings) > 1 {
		m.setMessage("This unit has multiple tracker mappings; select a tracker and open its candidate from the mapping flow.", messageToneWarning)
		return
	}
	mapping := mappings[0]
	link, err := tracker.URLForID(tracker.Service(mapping.Tracker), mapping.TrackerID)
	if err != nil {
		m.setMessage("Could not open confirmed mapping: "+err.Error(), messageToneWarning)
		return
	}
	if err := m.openURL(link); err != nil {
		m.setMessage("Could not open confirmed mapping: "+err.Error(), messageToneWarning)
		return
	}
	m.setMessage("Opened confirmed "+mapping.Tracker+" mapping for review.", messageToneSuccess)
}

func (m *Model) View() string {
	var b strings.Builder
	displayedMessage := m.displayedMessage()
	b.WriteString(titleStyle.Render("VLC Media Watcher") + "  " + mutedStyle.Render("local-first watch automation") + "\n")
	b.WriteString(m.tabs() + "\n\n")
	switch m.view {
	case dashboardView:
		m.renderDashboard(&b)
	case eventsView:
		m.renderEvents(&b)
	case settingsView:
		m.renderSettings(&b)
	case sonarrView, radarrView:
		m.renderMediaManager(&b)
	case trackersView:
		m.renderTrackers(&b)
	case matchesView:
		m.renderMatches(&b)
	}
	b.WriteString("\n")
	if displayedMessage.text != "" {
		b.WriteString(messageStyle(displayedMessage.tone).Render("• "+displayedMessage.text) + "\n")
	}
	if m.selecting {
		b.WriteString(sectionStyle.Render(m.selectionTitle) + "\n")
		for index, option := range m.selectionOptions {
			row := "  " + option.label
			if index == m.choice {
				b.WriteString(selectedRowStyle.Render("›"+row) + "\n")
			} else {
				b.WriteString(labelStyle.Render(" "+row) + "\n")
			}
		}
		b.WriteString(mutedStyle.Render("↑/↓ choose   Enter apply   Esc cancel") + "\n")
	} else if m.editing {
		label := "Enter custom value"
		if m.view == matchesView {
			if m.editingSearch {
				label = "Search tracker catalog"
			} else {
				label = "Confirm exact tracker ID"
			}
		}
		b.WriteString(sectionStyle.Render(label) + "\n")
		b.WriteString(selectedRowStyle.Render("  "+m.input+"_") + "\n")
		b.WriteString(mutedStyle.Render("Enter save   Esc cancel") + "\n")
	} else {
		if m.candidatePicking {
			b.WriteString(sectionStyle.Render("Pick the exact tracker match") + "\n")
			for index, candidate := range m.candidates {
				details := candidate.Kind
				if candidate.Episodes > 0 {
					details += fmt.Sprintf(" • %d eps", candidate.Episodes)
				}
				row := fmt.Sprintf("  %-8s %-4d %-14s %s", candidate.ID, candidate.Year, details, candidate.Title)
				if index == m.candidateSelected {
					b.WriteString(selectedRowStyle.Render("›"+row) + "\n")
				} else {
					b.WriteString(labelStyle.Render(" "+row) + "\n")
				}
				aliases := strings.Join(candidate.Aliases, " • ")
				if aliases != "" && aliases != candidate.Title {
					b.WriteString(hintStyle.Render("    ↳ "+truncate(aliases, max(12, m.width-8))) + "\n")
				}
			}
			b.WriteString(mutedStyle.Render("↑/↓ choose   o open in browser   c/Enter confirm   Esc cancel") + "\n")
			return b.String()
		}
		if m.mappingActionSelecting {
			b.WriteString(sectionStyle.Render("Manage confirmed "+m.activeMapping.Tracker+" mapping") + "\n")
			b.WriteString(mutedStyle.Render(m.activeMapping.TrackerTitle+" • "+m.activeMapping.TrackerID) + "\n")
			choices := []string{"Open in browser", "Replace after search", "Remove local mapping"}
			for index, choice := range choices {
				row := "  " + choice
				if index == m.mappingAction {
					b.WriteString(selectedRowStyle.Render("›"+row) + "\n")
				} else {
					b.WriteString(labelStyle.Render(" "+row) + "\n")
				}
			}
			b.WriteString(mutedStyle.Render("↑/↓ choose   Enter apply   Esc cancel") + "\n")
			return b.String()
		}
		if m.mappingSelecting {
			available := m.eligibleTrackerDefinitions(m.mediaUnits[m.selected])
			b.WriteString(sectionStyle.Render("Choose tracker to search") + "\n")
			for index, definition := range available {
				row := "  " + definition.Name
				if index == m.mappingTracker {
					b.WriteString(selectedRowStyle.Render("›"+row) + "\n")
				} else {
					b.WriteString(labelStyle.Render(" "+row) + "\n")
				}
			}
			b.WriteString(mutedStyle.Render("↑/↓ choose   Enter/f search source title   s edit search title   Esc cancel") + "\n")
			return b.String()
		}
		if m.manualIDSelecting {
			b.WriteString(sectionStyle.Render("No exact match selected") + "\n")
			choices := []string{"Search tracker candidates again", "Enter an exact tracker ID manually"}
			for index, choice := range choices {
				row := "  " + choice
				if index == m.manualIDChoice {
					b.WriteString(selectedRowStyle.Render("›"+row) + "\n")
				} else {
					b.WriteString(labelStyle.Render(" "+row) + "\n")
				}
			}
			b.WriteString(mutedStyle.Render("↑/↓ choose   Enter select   Esc cancel") + "\n")
			return b.String()
		}
		if displayedMessage.text != "" && m.managerService() != "" {
			// The manager setup guide fills an 80x24 terminal. The message takes
			// the footer's line so save errors and test guidance remain visible.
		} else if m.isEditableView() {
			footer := "Tab/←→ views  ↑/↓ select  Enter choose  Space toggle"
			if m.managerService() != "" {
				footer += "  v validate"
			}
			b.WriteString(mutedStyle.Render(footer + "  q quit"))
		} else {
			b.WriteString(mutedStyle.Render("Tab / ← → switch views   r refresh   q quit"))
		}
		if displayedMessage.text == "" || m.managerService() == "" {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func messageStyle(tone messageTone) lipgloss.Style {
	switch tone {
	case messageToneSuccess:
		return successStyle
	case messageToneWarning:
		return warningStyle
	default:
		return mutedStyle
	}
}

func (m *Model) tabs() string {
	labels := []string{"Dashboard", "Tracking", "Settings", "Sonarr", "Radarr", "Trackers", "Matches"}
	parts := make([]string, len(labels))
	for index, label := range labels {
		if index == m.view {
			parts[index] = activeTabStyle.Render(label)
		} else {
			parts[index] = tabStyle.Render(label)
		}
	}
	return strings.Join(parts, " ")
}

func (m *Model) renderDashboard(b *strings.Builder) {
	needs, confirmed, _ := m.matchModeCounts()
	b.WriteString(sectionStyle.Render("Dashboard") + "  " + mutedStyle.Render("health and next action") + "\n\n")
	m.dashboardField(b, "Watcher", m.watcherHealth())
	if len(m.allEvents) == 0 {
		m.dashboardField(b, "Last completed watch", "No local watch recorded")
	} else {
		e := m.allEvents[0]
		m.dashboardField(b, "Last completed watch", filepath.Base(e.MediaPath)+" • "+m.eventTime(e))
	}
	m.dashboardField(b, "Needs attention", dashboardCount(m.trackingNeedsCount(), "tracked item"))
	m.dashboardField(b, "Matches awaiting review", dashboardCount(needs, "mapping"))
	b.WriteString("\n" + sectionStyle.Render("Media managers") + "\n")
	m.renderDashboardManager(b, "Sonarr", m.config.Sonarr, "episodes")
	m.renderDashboardManager(b, "Radarr", m.config.Radarr, "movies")
	b.WriteString("\n" + sectionStyle.Render("Trackers") + "\n")
	enabled := m.enabledTrackerDefinitions()
	if len(enabled) == 0 {
		b.WriteString(mutedStyle.Render("No tracker is enabled. Open Trackers to add one.") + "\n")
	} else {
		for _, definition := range enabled {
			mapped, missing := m.trackerMappingCounts(definition.Service)
			state := m.trackerReadiness(definition)
			fmt.Fprintf(b, "%s %s\n", labelStyle.Render(definition.Name+":"), stateStyle(state.kind).Render(state.label+fmt.Sprintf(" • %d confirmed • %d need review", mapped, missing)))
		}
	}
	if confirmed > 0 {
		b.WriteString("\n" + mutedStyle.Render("Enter opens the next needed repair or mapping. r refreshes evidence.") + "\n")
	}
}

func (m *Model) renderDashboardManager(b *strings.Builder, name string, cfg config.MediaManagerConfig, media string) {
	if !cfg.LookupEnabled() {
		fmt.Fprintf(b, "%s %s\n", labelStyle.Render(name+":"), mutedStyle.Render("not configured"))
		return
	}
	check, found := m.integrationChecks[strings.ToLower(name)]
	status := "configured; not tested in this TUI session"
	style := mutedStyle
	if found {
		status, style = check.Detail, stateStyle(check.State)
		if status == "" {
			status = check.State
		}
	}
	if cfg.UnmonitoringEnabled() {
		status += "; unmonitors matched " + media
	}
	fmt.Fprintf(b, "%s %s\n", labelStyle.Render(name+":"), style.Render(status))
}

func (m *Model) dashboardField(b *strings.Builder, label, value string) {
	fmt.Fprintf(b, "%s %s\n", labelStyle.Render(fmt.Sprintf("%-16s", label+":")), valueStyle.Render(value))
}

func (m *Model) renderEvents(b *strings.Builder) {
	needs, completed, all := m.trackingModeCounts()
	b.WriteString(sectionStyle.Render("Tracking") + "  " + mutedStyle.Render(fmt.Sprintf("[1] Needs action %d  [2] Completed %d  [3] All tracked %d", needs, completed, all)) + "\n")
	if m.trackingDetail && m.selected >= 0 && m.selected < len(m.events) {
		m.renderTrackingDetail(b, m.events[m.selected])
		return
	}
	b.WriteString(mutedStyle.Render(trackingModeDescription(m.trackingMode)) + "\n\n")
	if len(m.events) == 0 {
		message := "No watched files recorded yet. Run the watcher, then press r."
		if m.trackingMode == trackingNeedsAction {
			message = "Nothing needs action. Open [2] Completed to review tracked files."
		}
		b.WriteString(mutedStyle.Render(message) + "\n")
		return
	}
	for index, event := range m.events {
		title := filepath.Base(event.MediaPath)
		if event.SourceID > 0 {
			title += "  " + eventResolutionLabel(event)
		}
		row := fmt.Sprintf("  %-52s %s", truncate(title, max(18, m.width-28)), m.eventStatus(event))
		if index == m.selected {
			b.WriteString(selectedRowStyle.Render("›"+row) + "\n")
		} else {
			b.WriteString(valueStyle.Render(" "+row) + "\n")
		}
		b.WriteString(hintStyle.Render("    ↳ "+m.eventTime(event)+" • "+trackingOutcome(event)) + "\n")
		if job, found := m.syncJobs[event.MediaPath]; found {
			b.WriteString(hintStyle.Render("      AniList: "+job.Detail) + "\n")
		}
	}
	footer := "↑/↓ select   Enter details   1/2/3 change view   r refresh"
	if m.trackingMode == trackingNeedsAction {
		footer += "   R retry selected   A retry all"
	}
	if m.trackerConfig("anilist").SyncProgress {
		footer += "   S sync selected to AniList"
	}
	b.WriteString("\n" + mutedStyle.Render(footer) + "\n")
}

func (m *Model) eventTime(event watch.Event) string {
	return fmt.Sprintf("%s  %.0f%%", event.WatchedAt.Local().Format("2006-01-02 15:04"), event.Progress*100)
}

func (m *Model) eventStatus(event watch.Event) string {
	switch event.Status {
	case "failed":
		return errorStyle.Render("× FAILED")
	case "pending", "unmatched":
		return warningStyle.Render(strings.ToUpper(event.Status))
	case "local":
		return mutedStyle.Render(strings.ToUpper(event.Status))
	default:
		return successStyle.Render(strings.ToUpper(event.Status))
	}
}

type readinessState struct {
	kind  string
	label string
}

func stateStyle(state string) lipgloss.Style {
	switch state {
	case "ready", "linked", "success":
		return successStyle
	case "failed", "error":
		return errorStyle
	case "action", "warning":
		return warningStyle
	case "info":
		return infoStyle
	default:
		return mutedStyle
	}
}

func dashboardCount(count int, singular string) string {
	if count == 0 {
		return "None"
	}
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %ss", count, singular)
}

func (m *Model) watcherHealth() string {
	if !m.heartbeatKnown {
		return "Unknown — no successful VLC poll recorded"
	}
	age := time.Since(m.heartbeat)
	window := maxDuration(30*time.Second, 2*m.config.Watch.PollInterval)
	if age <= window {
		return "Healthy — last successful VLC poll " + relativeTime(age) + " ago"
	}
	return "No recent watcher heartbeat — last successful VLC poll " + relativeTime(age) + " ago"
}

func maxDuration(left, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}

func relativeTime(value time.Duration) string {
	if value < time.Minute {
		return "just now"
	}
	if value < time.Hour {
		return fmt.Sprintf("%dm", int(value.Round(time.Minute).Minutes()))
	}
	return fmt.Sprintf("%dh", int(value.Round(time.Hour).Hours()))
}

func eventResolutionLabel(event watch.Event) string {
	if event.Manager == "" || event.SourceID <= 0 {
		return ""
	}
	label := event.Manager + fmt.Sprintf(" source #%d", event.SourceID)
	if event.SeasonNumber > 0 {
		label += fmt.Sprintf(" • season %d", event.SeasonNumber)
	}
	return label
}

func trackingOutcome(event watch.Event) string {
	switch event.Status {
	case "unmonitored":
		return "library item was unmonitored after completion"
	case "already-unmonitored":
		return "library item was already unmonitored"
	case "local":
		return "recorded locally; no media-manager write was enabled"
	case "unmatched":
		return "could not identify this watched file"
	case "failed":
		return "a reconciliation attempt failed; inspect and retry from the CLI"
	case "pending":
		return "awaiting deterministic reconciliation"
	default:
		return "completed"
	}
}

func (m *Model) trackingNeedsCount() int {
	count := 0
	for _, event := range m.allEvents {
		if m.eventNeedsAttention(event) {
			count++
		}
	}
	return count
}

func (m *Model) trackingModeCounts() (needs, completed, all int) {
	all = len(m.allEvents)
	needs = m.trackingNeedsCount()
	completed = all - needs
	return
}

func trackingModeDescription(mode trackingMode) string {
	switch mode {
	case trackingCompleted:
		return "Stable outcomes for watched files. This is a durable file record, not a replay history."
	case trackingAll:
		return "Every tracked file, including items that need repair."
	default:
		return "Only watched files that need a deterministic retry or manual investigation."
	}
}

func (m *Model) renderTrackingDetail(b *strings.Builder, event watch.Event) {
	b.WriteString(sectionStyle.Render("Tracked watch") + "\n")
	b.WriteString(valueStyle.Render(filepath.Base(event.MediaPath)) + "  " + m.eventStatus(event) + "\n")
	fmt.Fprintf(b, "%s %s • %.0f%% watched\n", mutedStyle.Render("Completed"), event.WatchedAt.Local().Format("2006-01-02 15:04"), event.Progress*100)
	b.WriteString("\n" + sectionStyle.Render("Outcome") + "\n")
	b.WriteString(valueStyle.Render(trackingOutcome(event)) + "\n")
	if event.SourceID > 0 {
		b.WriteString("\n" + sectionStyle.Render("Resolved library identity") + "\n")
		b.WriteString(valueStyle.Render(eventResolutionLabel(event)) + "\n")
		if unit, found := m.findMediaUnitForEvent(event); found {
			b.WriteString(hintStyle.Render("  "+sourceIdentity(unit)) + "\n")
			for _, mapping := range m.enabledMappingsForUnit(unit) {
				b.WriteString(successStyle.Render("  ✓ "+mapping.Tracker+" "+mapping.TrackerID+" "+mapping.TrackerTitle) + "\n")
			}
		}
	}
	if job, found := m.syncJobs[event.MediaPath]; found {
		b.WriteString("\n" + sectionStyle.Render("AniList progress sync") + "\n")
		b.WriteString(stateStyle(syncJobTone(job.Status)).Render(job.Detail) + "\n")
	}
	b.WriteString("\n" + sectionStyle.Render("Source path") + "\n")
	b.WriteString(mutedStyle.Render(event.MediaPath) + "\n")
	footer := "Esc returns to Tracking"
	if isRetryableEvent(event) {
		footer += "   R retries this item"
	}
	if m.trackerConfig("anilist").SyncProgress {
		footer += "   S syncs after exact read-only episode resolution"
	}
	b.WriteString("\n" + mutedStyle.Render(footer) + "\n")
}

func syncJobTone(status string) string {
	switch status {
	case "synced", "already_synced":
		return "success"
	case "review":
		return "warning"
	case "failed":
		return "failed"
	default:
		return "muted"
	}
}

func (m *Model) findMediaUnitForEvent(event watch.Event) (store.MediaUnit, bool) {
	for _, unit := range m.allMediaUnits {
		if unit.Manager != event.Manager || unit.SourceID != event.SourceID {
			continue
		}
		if event.SeasonNumber > 0 && unit.Scope == "season" && unit.SeasonNumber == event.SeasonNumber {
			return unit, true
		}
		if event.SeasonNumber <= 0 && unit.Scope == "media" {
			return unit, true
		}
	}
	return store.MediaUnit{}, false
}

func (m *Model) renderSettings(b *strings.Builder) {
	b.WriteString(sectionStyle.Render("Settings") + "  " + mutedStyle.Render("durable local preferences; secret values are never shown") + "\n")
	b.WriteString(mutedStyle.Render("Library and tracker connections have their own tabs. Changes save locally when confirmed.") + "\n\n")
	m.renderSettingRows(b)
	b.WriteString("\n" + hintStyle.Render("Advanced diagnostic: "+m.configPath) + "\n")
}

func (m *Model) renderMediaManager(b *strings.Builder) {
	service := m.managerService()
	displayName := strings.ToUpper(service[:1]) + service[1:]
	cfg := m.config.Sonarr
	media := "episodes"
	if service == "radarr" {
		cfg, media = m.config.Radarr, "movies"
	}
	if m.editing {
		b.WriteString(sectionStyle.Render(displayName+" library") + "  " + mutedStyle.Render("editing local configuration") + "\n")
		m.renderSettingRows(b)
		return
	}
	b.WriteString(sectionStyle.Render(displayName+" library") + "  " + mutedStyle.Render("connection, identity, guarded action") + "\n")
	if !cfg.LookupEnabled() {
		b.WriteString(warningStyle.Render("! Not configured for watched-file resolution") + "\n")
	} else if check, found := m.integrationChecks[service]; found {
		b.WriteString(stateStyle(check.State).Render(check.Detail) + "  " + mutedStyle.Render("last checked "+check.CheckedAt.Local().Format("2006-01-02 15:04")) + "\n")
	} else {
		b.WriteString(mutedStyle.Render("Connection has not been tested in this TUI. Press v to run a read-only test.") + "\n")
	}
	if cfg.MetadataLookup {
		b.WriteString(successStyle.Render("✓ Resolve watched files against "+displayName+" — read-only") + "\n")
	} else if cfg.UnmonitoringEnabled() {
		b.WriteString(successStyle.Render("✓ Resolution is active because after-watch actions need an exact library item") + "\n")
	} else {
		b.WriteString(mutedStyle.Render("• Resolution is off; watched files remain local records only") + "\n")
	}
	if cfg.UnmonitoringEnabled() {
		b.WriteString(warningStyle.Render("! After completion: unmonitor matched "+media+"; files are never deleted") + "\n")
	} else {
		b.WriteString(mutedStyle.Render("• After completion: record locally only; no remote write") + "\n")
	}
	if m.managerDiagnostics {
		m.renderManagerDiagnostics(b, service)
		return
	}
	b.WriteString(hintStyle.Render("Exact path → path mapping → unique bare filename; ambiguous names never match.") + "\n")
	b.WriteString(selectedRowStyle.Render(" [ V ] Test connection   [ D ] Diagnostics") + "\n")
	m.renderSettingRows(b)
}

func (m *Model) renderManagerDiagnostics(b *strings.Builder, service string) {
	b.WriteString("\n" + sectionStyle.Render("Diagnostics") + "\n")
	found := false
	for _, event := range m.allEvents {
		if event.Manager != service {
			continue
		}
		found = true
		b.WriteString(valueStyle.Render(filepath.Base(event.MediaPath)) + "\n")
		b.WriteString(hintStyle.Render("  "+eventResolutionLabel(event)+" • "+trackingOutcome(event)) + "\n")
		break
	}
	if !found {
		b.WriteString(mutedStyle.Render("No resolved watched file is available yet. Complete a watch, then inspect Tracking for exact evidence.") + "\n")
	}
	b.WriteString("\n" + mutedStyle.Render("Press d to return to configuration.") + "\n")
}

func (m *Model) renderTrackers(b *strings.Builder) {
	if m.trackerDetail {
		definition, ok := m.selectedTrackerDefinition()
		if !ok {
			b.WriteString(mutedStyle.Render("No tracker is selected. Press Esc, then a to add one.") + "\n")
			return
		}
		b.WriteString(sectionStyle.Render(definition.Name+" tracker") + "\n")
		state := m.trackerReadiness(definition)
		b.WriteString(stateStyle(state.kind).Render(state.label) + mutedStyle.Render(" • "+definition.Media+" • "+definition.MappingScope+" mapping") + "\n")
		b.WriteString(mutedStyle.Render(definition.Notes) + "\n\n")
		if definition.Service == tracker.AniDB {
			b.WriteString(mutedStyle.Render("No account connection. Enable reference matching, then confirm an AniDB AID on a season row in Matches. Esc returns.") + "\n\n")
		} else {
			b.WriteString(mutedStyle.Render("Register "+tracker.CallbackURL+" as the callback URL. Shift+L links the account after setup; Esc returns.") + "\n\n")
			b.WriteString(sectionStyle.Render("Linked account token") + "\n")
			b.WriteString(mutedStyle.Render("Stored in this computer's secure keychain after successful OAuth linking; it is never copied to 1Password or shown here.") + "\n\n")
		}
		m.renderSettingRows(b)
		mapped, missing := m.trackerMappingCounts(definition.Service)
		b.WriteString("\n" + hintStyle.Render(fmt.Sprintf("Match coverage: %d confirmed • %d need review", mapped, missing)) + "\n")
		if definition.Service == tracker.AniList {
			syncState := mutedStyle.Render("Progress sync: off — mappings remain local only.")
			if m.trackerConfig("anilist").SyncProgress {
				syncState = successStyle.Render("Progress sync: on — confirmed watched episodes automatically catch AniList up.")
			}
			b.WriteString(syncState + "\n")
		}
		return
	}
	title := "Trackers"
	description := "enabled connections and local match coverage"
	if m.trackerAdding {
		title, description = "Add tracker", "choose a disabled provider to configure"
	}
	b.WriteString(sectionStyle.Render(title) + "  " + mutedStyle.Render(description) + "\n\n")
	definitions := m.trackerListDefinitions()
	if len(definitions) == 0 {
		if m.trackerAdding {
			b.WriteString(mutedStyle.Render("Every available tracker is already enabled.") + "\n")
		} else {
			b.WriteString(mutedStyle.Render("No tracker is enabled. Press a to add one.") + "\n")
		}
		return
	}
	for _, definition := range definitions {
		state := m.trackerReadiness(definition)
		mapped, missing := m.trackerMappingCounts(definition.Service)
		row := fmt.Sprintf("  %-14s %s", definition.Name, state.label)
		if definition.Service == tracker.AniDB {
			row += " • season reference matching"
		} else if definition.Service == tracker.AniList && m.trackerConfig("anilist").SyncProgress {
			row += fmt.Sprintf(" • %d confirmed • progress sync on", mapped)
		} else {
			row += fmt.Sprintf(" • %d confirmed • %d need review", mapped, missing)
		}
		if definition.Service == m.trackerService {
			b.WriteString(selectedRowStyle.Render("›"+row) + "\n")
			b.WriteString(hintStyle.Render("    ↳ "+definition.Media+" • "+definition.Notes) + "\n")
		} else {
			b.WriteString(labelStyle.Render(" "+fmt.Sprintf("%-14s", definition.Name)) + stateStyle(state.kind).Render(" "+strings.TrimPrefix(row, "  "+definition.Name)) + "\n")
		}
	}
	footer := "↑/↓ select   Enter manage   a add tracker"
	if m.trackerAdding {
		footer = "↑/↓ select   Enter configure   a return to enabled trackers"
	}
	b.WriteString("\n" + mutedStyle.Render(footer) + "\n")
}

func (m *Model) renderMatches(b *strings.Builder) {
	needs, confirmed, recent := m.matchModeCounts()
	b.WriteString(sectionStyle.Render("Matches") + "  " + mutedStyle.Render("[1] Needs mapping "+fmt.Sprint(needs)+"  [2] Confirmed "+fmt.Sprint(confirmed)+"  [3] Recent "+fmt.Sprint(recent)) + "\n")
	b.WriteString(mutedStyle.Render(matchModeDescription(m.matchesMode)) + "\n\n")
	if len(m.mediaUnits) == 0 {
		message := "No resolved media identities yet. Enable Sonarr/Radarr metadata lookup, watch a file, then press r."
		if m.matchesMode == matchesNeedsMapping {
			message = "Nothing needs mapping for the enabled trackers. Open [2] Confirmed to review existing mappings."
		}
		b.WriteString(mutedStyle.Render(message) + "\n")
		return
	}
	groups := groupMatchUnits(m.mediaUnits)
	for _, group := range groups {
		unit := group.source
		ids := sourceIdentity(unit)
		b.WriteString(sectionStyle.Render(unit.Title) + "\n")
		b.WriteString(hintStyle.Render(fmt.Sprintf("  %s source #%d • %s", unit.Manager, unit.SourceID, ids)) + "\n")
		for _, item := range group.units {
			index := item.index
			unit := item.unit
			scope := unit.Scope
			if unit.SeasonNumber > 0 {
				scope += fmt.Sprintf(" %d", unit.SeasonNumber)
			}
			row := fmt.Sprintf("  %-9s", scope)
			if index == m.selected {
				b.WriteString(selectedRowStyle.Render("›"+row) + "\n")
			} else {
				b.WriteString(labelStyle.Render(" "+row) + "\n")
			}
			for _, definition := range m.eligibleTrackerDefinitions(unit) {
				mapping, found := mappingForTracker(m.mappings[unit.ID], definition.Service)
				if m.matchesMode == matchesNeedsMapping && found {
					continue
				}
				if m.matchesMode == matchesConfirmed && !found {
					continue
				}
				if found {
					link, _ := tracker.URLForID(definition.Service, mapping.TrackerID)
					b.WriteString(valueStyle.Render(fmt.Sprintf("    %-9s ✓ %-8s %s", definition.Name, mapping.TrackerID, mapping.TrackerTitle)) + "\n")
					if link != "" {
						b.WriteString(hintStyle.Render("               "+link) + "\n")
					}
					continue
				}
				b.WriteString(mutedStyle.Render(fmt.Sprintf("    %-9s — not confirmed", definition.Name)) + "\n")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString(mutedStyle.Render("↑/↓ select   Enter map or manage   o open one confirmed mapping   1/2/3 change view") + "\n")
}

type matchGroup struct {
	source store.MediaUnit
	units  []matchGroupUnit
}

type matchGroupUnit struct {
	unit  store.MediaUnit
	index int
}

func groupMatchUnits(units []store.MediaUnit) []matchGroup {
	groups := make([]matchGroup, 0, len(units))
	indices := make(map[string]int, len(units))
	for index, unit := range units {
		key := fmt.Sprintf("%s:%d", unit.Manager, unit.SourceID)
		groupIndex, found := indices[key]
		if !found {
			groupIndex = len(groups)
			indices[key] = groupIndex
			groups = append(groups, matchGroup{source: unit})
		}
		groups[groupIndex].units = append(groups[groupIndex].units, matchGroupUnit{unit: unit, index: index})
	}
	return groups
}

func sourceIdentity(unit store.MediaUnit) string {
	ids := make([]string, 0, 3)
	if unit.TVDBID > 0 {
		ids = append(ids, fmt.Sprintf("TVDB %d", unit.TVDBID))
	}
	if unit.TMDBID > 0 {
		ids = append(ids, fmt.Sprintf("TMDB %d", unit.TMDBID))
	}
	if unit.IMDbID != "" {
		ids = append(ids, "IMDb "+unit.IMDbID)
	}
	if len(ids) == 0 {
		return "no external source IDs"
	}
	return strings.Join(ids, " • ")
}

func (m *Model) matchModeCounts() (needs, confirmed, recent int) {
	for _, unit := range m.allMediaUnits {
		eligible := m.eligibleTrackerDefinitions(unit)
		if len(eligible) == 0 {
			continue
		}
		recent++
		for _, definition := range eligible {
			if _, found := mappingForTracker(m.mappings[unit.ID], definition.Service); found {
				confirmed++
			} else {
				needs++
			}
		}
	}
	return needs, confirmed, recent
}

func matchModeDescription(mode matchesMode) string {
	switch mode {
	case matchesConfirmed:
		return "Confirmed mappings for enabled trackers. Open, replace, or remove a selected mapping."
	case matchesRecent:
		return "Recent resolved identities for enabled trackers, whether mapped or not."
	default:
		return "Only items that still need a mapping for an enabled tracker."
	}
}

func mappingForTracker(mappings []store.TrackerMapping, service tracker.Service) (store.TrackerMapping, bool) {
	for _, mapping := range mappings {
		if mapping.Tracker == string(service) {
			return mapping, true
		}
	}
	return store.TrackerMapping{}, false
}

func (m *Model) renderSettingRows(b *strings.Builder) {
	for index, item := range m.settings() {
		if section := m.settingSection(index); section != "" {
			if index > 0 && m.view == settingsView {
				b.WriteString("\n")
			}
			b.WriteString(sectionStyle.Render(section) + "\n")
		}
		value := item.value
		if item.id == databasePathSetting && value == "" {
			value = "(default: " + m.config.DatabasePath() + ")"
		}
		value = truncate(value, max(12, m.width-28))
		row := fmt.Sprintf("  %-23s %s", item.name+":", value)
		if index == m.selected {
			b.WriteString(selectedRowStyle.Render("›"+row) + "\n")
		} else {
			valueStyleForRow := valueStyle
			if isBooleanSetting(item.id) {
				if value == "[ ON ]" {
					valueStyleForRow = toggleOnStyle
				} else {
					valueStyleForRow = toggleOffStyle
				}
			}
			b.WriteString(labelStyle.Render(" "+fmt.Sprintf("%-23s", item.name+":")) + valueStyleForRow.Render(value) + "\n")
		}
		if index == m.selected {
			b.WriteString(hintStyle.Render("    ↳ "+truncate(item.hint, max(12, m.width-8))) + "\n")
		}
	}
}

func (m *Model) settingSection(index int) string {
	switch m.view {
	case settingsView:
		switch index {
		case 0:
			return "General"
		case 1:
			return "VLC connection"
		case 3:
			return "Privacy and storage"
		case 4:
			return "Completion rules"
		case 7:
			return "Advanced"
		}
	case sonarrView, radarrView:
		switch index {
		case 0:
			return "Identity resolution"
		case 1:
			return "After-watch action"
		case 2:
			return "Connection"
		case 5:
			return "Path mapping"
		}
	case trackersView:
		if m.trackerDetail {
			switch index {
			case 0:
				return "Account connection"
			case 3:
				return "Availability"
			}
		}
	}
	return ""
}

func truncate(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

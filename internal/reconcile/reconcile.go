// Package reconcile safely coordinates optional Sonarr and Radarr monitored-
// state updates after a local watch is complete.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Cyberlane/vlc-media-watcher/internal/arr"
	"github.com/Cyberlane/vlc-media-watcher/internal/config"
	"github.com/Cyberlane/vlc-media-watcher/internal/secrets"
)

// Status is the stable, user-facing result of reconciling a watched path.
type Status string

const (
	StatusUnmonitored        Status = "unmonitored"
	StatusAlreadyUnmonitored Status = "already-unmonitored"
	StatusUnmatched          Status = "unmatched"
	StatusFailed             Status = "failed"
	StatusLocal              Status = "local"
)

// Outcome describes what happened without exposing API keys or raw remote
// error responses to the user.
type Outcome struct {
	Status   Status
	Messages []string
	Match    *arr.Match
}

const managerLookupTimeout = 30 * time.Second

type client interface {
	Check(context.Context) (arr.Instance, error)
	Find(context.Context, string, *arr.PathMapping) (arr.Match, bool, error)
	SetMonitored(context.Context, arr.Match, bool) error
}

type resolveSecretFunc func(context.Context, string, string, string, string) (string, error)
type clientFactoryFunc func(arr.Manager, string, string) (client, error)

type dependencies struct {
	resolveSecret resolveSecretFunc
	newClient     clientFactoryFunc
}

type managerState struct {
	manager arr.Manager
	config  config.MediaManagerConfig
	client  client
	initErr error
}

// Reconciler owns the enabled media-manager clients. Construction never
// returns an error: setup and secret-resolution failures are retained per
// manager and surfaced through Problems and Process instead of blocking local
// watch recording.
type Reconciler struct {
	managers       []managerState
	globalProblems []string
}

// New constructs a reconciler and resolves credentials for enabled managers.
// Network connections are checked lazily by Process.
func New(ctx context.Context, cfg *config.Config) *Reconciler {
	return newReconciler(ctx, cfg, productionDependencies())
}

// NewForManager constructs a reconciler for one known source manager. It is
// used by historical tracker sync recovery, where the stored event identity
// already says which library owns the file. Avoiding unrelated setup keeps a
// Sonarr-only operation independent of Radarr credentials and availability.
func NewForManager(ctx context.Context, cfg *config.Config, manager arr.Manager) *Reconciler {
	return newReconcilerForManager(ctx, cfg, productionDependencies(), manager)
}

// SetSonarrFilenameCache attaches an optional persistent cache to the Sonarr
// client. Other managers intentionally do not use it.
func (r *Reconciler) SetSonarrFilenameCache(cache arr.SonarrFilenameCache) {
	if r == nil {
		return
	}
	for _, state := range r.managers {
		if state.manager != arr.ManagerSonarr {
			continue
		}
		if client, ok := state.client.(interface{ SetSonarrFilenameCache(arr.SonarrFilenameCache) }); ok {
			client.SetSonarrFilenameCache(cache)
		}
	}
}

func productionDependencies() dependencies {
	return dependencies{
		resolveSecret: secrets.ResolveValue,
		newClient:     newARRClient,
	}
}

func newARRClient(manager arr.Manager, endpoint, apiKey string) (client, error) {
	switch manager {
	case arr.ManagerSonarr:
		return arr.NewSonarrClient(endpoint, apiKey)
	case arr.ManagerRadarr:
		return arr.NewRadarrClient(endpoint, apiKey)
	default:
		return nil, fmt.Errorf("unsupported media manager %q", manager)
	}
}

func newReconciler(ctx context.Context, cfg *config.Config, deps dependencies) *Reconciler {
	return newReconcilerForManager(ctx, cfg, deps, "")
}

func newReconcilerForManager(ctx context.Context, cfg *config.Config, deps dependencies, onlyManager arr.Manager) *Reconciler {
	reconciler := &Reconciler{}
	if cfg == nil {
		reconciler.globalProblems = append(reconciler.globalProblems, "Media-manager configuration is unavailable.")
		return reconciler
	}
	if ctx == nil {
		ctx = context.Background()
	}

	configured := []struct {
		manager arr.Manager
		config  config.MediaManagerConfig
	}{
		{manager: arr.ManagerSonarr, config: cfg.Sonarr},
		{manager: arr.ManagerRadarr, config: cfg.Radarr},
	}

	for _, item := range configured {
		if onlyManager != "" && item.manager != onlyManager {
			continue
		}
		if !item.config.LookupEnabled() {
			continue
		}

		state := managerState{manager: item.manager, config: item.config}
		if err := validateManagerConfig(item.manager, item.config); err != nil {
			state.initErr = err
			reconciler.managers = append(reconciler.managers, state)
			continue
		}

		apiKey, err := deps.resolveSecret(ctx, apiKeyLabel(item.manager), item.config.SecretSource, item.config.SecretReference, item.config.APIKeyEnv)
		if err != nil {
			state.initErr = fmt.Errorf("resolve API key: %w", err)
			reconciler.managers = append(reconciler.managers, state)
			continue
		}

		state.client, err = deps.newClient(item.manager, item.config.Endpoint, apiKey)
		if err != nil {
			state.initErr = fmt.Errorf("create client: %w", err)
		}
		reconciler.managers = append(reconciler.managers, state)
	}

	return reconciler
}

// Active reports whether at least one media manager is configured to update
// monitored state. A manager remains active when initialization failed so the
// failure is reported rather than silently treating the watch as local-only.
func (r *Reconciler) Active() bool {
	return r != nil && len(r.managers) > 0
}

// Problems returns a stable-order copy of initialization problems. The
// underlying secret resolver is responsible for never including secret values
// in its errors.
func (r *Reconciler) Problems() []string {
	if r == nil {
		return nil
	}
	problems := append([]string(nil), r.globalProblems...)
	for _, state := range r.managers {
		if state.initErr != nil {
			problems = append(problems, fmt.Sprintf("%s setup problem: %v", managerName(state.manager), state.initErr))
		}
	}
	return problems
}

// Process finds exact matches in every active manager before attempting a
// write. Any lookup failure or more than one manager match fails closed.
func (r *Reconciler) Process(ctx context.Context, mediaPath string) Outcome {
	if r == nil {
		return outcome(StatusLocal, "Remote monitored-state updates are unavailable; this watch remains local.")
	}
	if len(r.globalProblems) > 0 {
		return outcome(StatusFailed, "Media-manager setup is unavailable; no remote monitored-state change was made.")
	}
	if !r.Active() {
		return outcome(StatusLocal, "Remote monitored-state updates are disabled; this watch remains local.")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for _, state := range r.managers {
		if state.initErr != nil || state.client == nil {
			return outcome(StatusFailed, fmt.Sprintf("%s is enabled but not ready; review its setup. No remote monitored-state change was made.", managerName(state.manager)))
		}
	}

	type foundMatch struct {
		state managerState
		match arr.Match
	}
	matches := make([]foundMatch, 0, 1)
	lookupFailures := make([]string, 0)

	for _, state := range r.managers {
		lookupCtx, cancelLookup := context.WithTimeout(ctx, managerLookupTimeout)
		match, found, err := state.client.Find(lookupCtx, mediaPath, pathMapping(state.config))
		cancelLookup()
		if err != nil {
			if errors.Is(err, arr.ErrAmbiguousMatch) {
				lookupFailures = append(lookupFailures, fmt.Sprintf("%s found more than one exact library match; no remote monitored-state change was made.", managerName(state.manager)))
			} else {
				lookupFailures = append(lookupFailures, fmt.Sprintf("%s could not be checked; no remote monitored-state change was made.", managerName(state.manager)))
			}
			continue
		}
		if !found {
			continue
		}
		if err := validateMatch(state.manager, match); err != nil {
			lookupFailures = append(lookupFailures, fmt.Sprintf("%s returned an invalid match; no remote monitored-state change was made.", managerName(state.manager)))
			continue
		}
		matches = append(matches, foundMatch{state: state, match: match})
	}

	if len(lookupFailures) > 0 {
		return Outcome{Status: StatusFailed, Messages: lookupFailures}
	}
	if len(matches) == 0 {
		return outcome(StatusUnmatched, "No exact Sonarr or Radarr library match was found; no remote monitored-state change was made.")
	}
	if len(matches) > 1 {
		return outcome(StatusFailed, "More than one media manager matched this file; no remote monitored-state change was made.")
	}

	matched := matches[0]
	match := matched.match
	if !matched.state.config.UnmonitoringEnabled() {
		return Outcome{Status: StatusLocal, Match: &match, Messages: []string{fmt.Sprintf("Resolved exact %s metadata locally; tracker updates still require confirmed mappings.", managerName(matched.state.manager))}}
	}
	if matched.match.AllMonitored(false) {
		return Outcome{Status: StatusAlreadyUnmonitored, Match: &match, Messages: []string{fmt.Sprintf("The matched %s item is already unmonitored.", managerName(matched.state.manager))}}
	}

	if err := matched.state.client.SetMonitored(ctx, matched.match, false); err != nil {
		return outcome(StatusFailed, fmt.Sprintf("%s matched the file, but its monitored state could not be updated; no remote change was confirmed.", managerName(matched.state.manager)))
	}
	return Outcome{Status: StatusUnmonitored, Match: &match, Messages: []string{fmt.Sprintf("The matched %s item was set to unmonitored.", managerName(matched.state.manager))}}
}

// Resolve performs the exact same fail-closed library lookup as Process but
// never changes a manager's monitored state. It is used for explicit tracker
// progress backfills, where a historical local event needs fresh episode
// evidence before any tracker write can be considered.
func (r *Reconciler) Resolve(ctx context.Context, mediaPath string) (arr.Match, error) {
	return r.resolve(ctx, mediaPath, "")
}

// ResolveForManager performs a read-only exact lookup against one known
// source manager. Historical events retain that source identity, so tracker
// progress recovery must not depend on a separate enabled manager such as
// Radarr being reachable.
func (r *Reconciler) ResolveForManager(ctx context.Context, mediaPath string, manager arr.Manager) (arr.Match, error) {
	return r.resolve(ctx, mediaPath, manager)
}

func (r *Reconciler) resolve(ctx context.Context, mediaPath string, onlyManager arr.Manager) (arr.Match, error) {
	if r == nil || len(r.globalProblems) > 0 || !r.Active() {
		return arr.Match{}, fmt.Errorf("media-manager resolution is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	states := make([]managerState, 0, len(r.managers))
	for _, state := range r.managers {
		if onlyManager != "" && state.manager != onlyManager {
			continue
		}
		states = append(states, state)
		if state.initErr != nil || state.client == nil {
			return arr.Match{}, fmt.Errorf("%s is not ready for read-only resolution", managerName(state.manager))
		}
	}
	if len(states) == 0 {
		return arr.Match{}, fmt.Errorf("%s is not enabled for read-only resolution", managerName(onlyManager))
	}
	matches := make([]arr.Match, 0, 1)
	for _, state := range states {
		match, found, err := state.client.Find(ctx, mediaPath, pathMapping(state.config))
		if err != nil {
			if errors.Is(err, arr.ErrAmbiguousMatch) {
				return arr.Match{}, fmt.Errorf("%s found more than one exact library match", managerName(state.manager))
			}
			return arr.Match{}, fmt.Errorf("%s could not be checked", managerName(state.manager))
		}
		if !found {
			continue
		}
		if err := validateMatch(state.manager, match); err != nil {
			return arr.Match{}, fmt.Errorf("%s returned an invalid match", managerName(state.manager))
		}
		matches = append(matches, match)
	}
	if len(matches) == 0 {
		return arr.Match{}, fmt.Errorf("no exact Sonarr or Radarr library match was found")
	}
	if len(matches) > 1 {
		return arr.Match{}, fmt.Errorf("more than one media manager matched this file")
	}
	return matches[0], nil
}

// Test validates, resolves, and checks one configured service regardless of
// whether unmonitor-after-watch is enabled.
func Test(ctx context.Context, cfg *config.Config, service arr.Manager) (arr.Instance, error) {
	return testService(ctx, cfg, service, productionDependencies())
}

func testService(ctx context.Context, cfg *config.Config, service arr.Manager, deps dependencies) (arr.Instance, error) {
	if cfg == nil {
		return arr.Instance{}, fmt.Errorf("configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	managerConfig, err := selectedManagerConfig(cfg, service)
	if err != nil {
		return arr.Instance{}, err
	}
	if err := validateManagerConfig(service, managerConfig); err != nil {
		return arr.Instance{}, err
	}

	apiKey, err := deps.resolveSecret(ctx, apiKeyLabel(service), managerConfig.SecretSource, managerConfig.SecretReference, managerConfig.APIKeyEnv)
	if err != nil {
		return arr.Instance{}, fmt.Errorf("resolve %s API key: %w", managerName(service), err)
	}
	managerClient, err := deps.newClient(service, managerConfig.Endpoint, apiKey)
	if err != nil {
		return arr.Instance{}, fmt.Errorf("create %s client: %w", managerName(service), err)
	}
	instance, err := managerClient.Check(ctx)
	if err != nil {
		return arr.Instance{}, fmt.Errorf("check %s connection: %w", managerName(service), err)
	}
	if !strings.EqualFold(strings.TrimSpace(instance.AppName), string(service)) {
		name := strings.TrimSpace(instance.AppName)
		if name == "" {
			name = "an unknown application"
		}
		return arr.Instance{}, fmt.Errorf("%s endpoint identifies itself as %s", managerName(service), name)
	}
	if strings.TrimSpace(instance.Version) == "" {
		return arr.Instance{}, fmt.Errorf("%s endpoint did not report a version", managerName(service))
	}
	return instance, nil
}

func selectedManagerConfig(cfg *config.Config, service arr.Manager) (config.MediaManagerConfig, error) {
	switch service {
	case arr.ManagerSonarr:
		return cfg.Sonarr, nil
	case arr.ManagerRadarr:
		return cfg.Radarr, nil
	default:
		return config.MediaManagerConfig{}, fmt.Errorf("unsupported media manager %q", service)
	}
}

func validateManagerConfig(manager arr.Manager, cfg config.MediaManagerConfig) error {
	endpoint, err := url.Parse(strings.TrimSpace(cfg.Endpoint))
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return fmt.Errorf("%s.endpoint must be a full http or https URL", manager)
	}
	if endpoint.User != nil {
		return fmt.Errorf("%s.endpoint must not include credentials", manager)
	}
	if endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("%s.endpoint must not include a query or fragment", manager)
	}
	if strings.HasSuffix(strings.ToLower(strings.TrimRight(endpoint.Path, "/")), "/api/v3") {
		return fmt.Errorf("%s.endpoint must not include /api/v3", manager)
	}

	switch cfg.SecretSource {
	case "environment":
		if strings.TrimSpace(cfg.APIKeyEnv) == "" {
			return fmt.Errorf("%s.api_key_env is required for the environment secret source", manager)
		}
	case "keyring", "1password":
		if strings.TrimSpace(cfg.SecretReference) == "" {
			return fmt.Errorf("%s.secret_reference is required for the %s secret source", manager, cfg.SecretSource)
		}
	default:
		return fmt.Errorf("%s.secret_source must be environment, keyring, or 1password", manager)
	}

	hasLocalPrefix := strings.TrimSpace(cfg.LocalPathPrefix) != ""
	hasRemotePrefix := strings.TrimSpace(cfg.RemotePathPrefix) != ""
	if hasLocalPrefix != hasRemotePrefix {
		return fmt.Errorf("%s.local_path_prefix and %s.remote_path_prefix must both be set or both be empty", manager, manager)
	}
	return nil
}

func validateMatch(manager arr.Manager, match arr.Match) error {
	if match.Manager != manager {
		return fmt.Errorf("%s client returned a %s match", manager, match.Manager)
	}
	if len(match.Targets) == 0 {
		return fmt.Errorf("%s match has no targets", manager)
	}
	for _, target := range match.Targets {
		if target.ID <= 0 {
			return fmt.Errorf("%s match has an invalid target", manager)
		}
	}
	return nil
}

func pathMapping(cfg config.MediaManagerConfig) *arr.PathMapping {
	if strings.TrimSpace(cfg.LocalPathPrefix) == "" && strings.TrimSpace(cfg.RemotePathPrefix) == "" {
		return nil
	}
	return &arr.PathMapping{LocalPrefix: cfg.LocalPathPrefix, RemotePrefix: cfg.RemotePathPrefix}
}

func apiKeyLabel(manager arr.Manager) string {
	return managerName(manager) + " API key"
}

func managerName(manager arr.Manager) string {
	switch manager {
	case arr.ManagerSonarr:
		return "Sonarr"
	case arr.ManagerRadarr:
		return "Radarr"
	default:
		return "Media manager"
	}
}

func outcome(status Status, message string) Outcome {
	return Outcome{Status: status, Messages: []string{message}}
}

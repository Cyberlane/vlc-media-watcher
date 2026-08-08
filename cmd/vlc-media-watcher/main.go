package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/Cyberlane/vlc-media-watcher/internal/arr"
	"github.com/Cyberlane/vlc-media-watcher/internal/config"
	"github.com/Cyberlane/vlc-media-watcher/internal/credentials"
	"github.com/Cyberlane/vlc-media-watcher/internal/reconcile"
	"github.com/Cyberlane/vlc-media-watcher/internal/secrets"
	"github.com/Cyberlane/vlc-media-watcher/internal/store"
	"github.com/Cyberlane/vlc-media-watcher/internal/tracker"
	"github.com/Cyberlane/vlc-media-watcher/internal/tui"
	"github.com/Cyberlane/vlc-media-watcher/internal/vlc"
	"github.com/Cyberlane/vlc-media-watcher/internal/watch"
)

const appName = "vlc-media-watcher"

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "source"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return runTUI(nil, stdout)
	}

	switch args[0] {
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	case "version", "--version":
		printVersion(stdout)
		return nil
	case "setup":
		return runSetup(args[1:], stdout)
	case "watch":
		return runWatch(args[1:], stdout)
	case "events":
		return runEvents(args[1:], stdout)
	case "integrations":
		return runIntegrations(args[1:], stdout)
	case "secret":
		return runSecret(args[1:], stdout)
	case "mappings":
		return runMappings(args[1:], stdout)
	case "tui":
		return runTUI(args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q (run %s help)", args[0], appName)
	}
}

func printVersion(w io.Writer) {
	buildVersion, buildCommit, buildDate, builder := buildInformation()
	fmt.Fprintf(w, "%s %s (commit %s, built %s by %s)\n", appName, buildVersion, buildCommit, buildDate, builder)
}

func buildInformation() (string, string, string, string) {
	buildVersion, buildCommit, buildDate, builder := version, commit, date, builtBy
	if info, ok := debug.ReadBuildInfo(); ok {
		if buildVersion == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			buildVersion = strings.TrimPrefix(info.Main.Version, "v")
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if buildCommit == "none" && setting.Value != "" {
					buildCommit = setting.Value
				}
			case "vcs.time":
				if buildDate == "unknown" && setting.Value != "" {
					buildDate = setting.Value
				}
			case "vcs.modified":
				if setting.Value == "true" && buildCommit != "none" {
					buildCommit += "+dirty"
				}
			}
		}
	}
	return buildVersion, buildCommit, buildDate, builder
}

func configFromFlags(args []string, name string) (*config.Config, *flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", config.DefaultPath(), "configuration file")
	if err := fs.Parse(args); err != nil {
		return nil, fs, err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return nil, fs, err
	}
	return cfg, fs, nil
}

func runSetup(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("config", config.DefaultPath(), "configuration file")
	force := fs.Bool("force", false, "replace an existing configuration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := config.WriteExample(*path, *force); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Created %s\n\n", *path)
	fmt.Fprintln(stdout, "Next steps:")
	fmt.Fprintln(stdout, "  1. Enable VLC's HTTP interface and set its password.")
	fmt.Fprintf(stdout, "  2. Run %s secret set to store that password in the system keyring.\n", appName)
	fmt.Fprintf(stdout, "  3. Run %s watch --once to validate the connection.\n", appName)
	fmt.Fprintln(stdout, "  4. Optional: open the Sonarr/Radarr TUI tabs for media-manager setup guidance.")
	return nil
}

func runWatch(args []string, stdout io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWatchContext(ctx, args, stdout)
}

const (
	continuousWatcherLeaseName    = "continuous-watch"
	continuousWatcherLeaseTTL     = 30 * time.Second
	continuousWatcherLeaseRenew   = 10 * time.Second
	repeatedWarningReportInterval = 15 * time.Minute
	vlcCredentialResolveTimeout   = secrets.BackgroundResolveTimeout
)

var defaultVLCCredentialRetryDelays = []time.Duration{
	15 * time.Second,
	30 * time.Second,
	time.Minute,
	2 * time.Minute,
	5 * time.Minute,
}

type watchDependencies struct {
	resolveVLC             func(context.Context, config.VLCConfig) credentials.Resolution
	credentialResolveLimit time.Duration
	credentialRetryDelays  []time.Duration
}

func defaultWatchDependencies() watchDependencies {
	return watchDependencies{
		resolveVLC:             secrets.ResolveVLC,
		credentialResolveLimit: vlcCredentialResolveTimeout,
		credentialRetryDelays:  defaultVLCCredentialRetryDelays,
	}
}

func runWatchContext(ctx context.Context, args []string, stdout io.Writer) error {
	return runWatchContextWithDependencies(ctx, args, stdout, defaultWatchDependencies())
}

func runWatchContextWithDependencies(ctx context.Context, args []string, stdout io.Writer, dependencies watchDependencies) error {
	if dependencies.resolveVLC == nil {
		dependencies.resolveVLC = secrets.ResolveVLC
	}
	if dependencies.credentialResolveLimit <= 0 {
		dependencies.credentialResolveLimit = vlcCredentialResolveTimeout
	}
	if len(dependencies.credentialRetryDelays) == 0 {
		dependencies.credentialRetryDelays = defaultVLCCredentialRetryDelays
	}
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", config.DefaultPath(), "configuration file")
	once := fs.Bool("once", false, "read VLC once and exit")
	verbose := fs.Bool("verbose", false, "include full media paths in continuous watcher logs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*once {
		if err := secureContinuousOutput(stdout); err != nil {
			return err
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer db.Close()

	serviceLogger := newWatchServiceLogger(stdout, *verbose)
	leaseOwner := ""
	if !*once {
		leaseOwner, err = newWatcherLeaseOwner()
		if err != nil {
			return err
		}
		acquired, err := db.AcquireWatcherLease(continuousWatcherLeaseName, leaseOwner, time.Now(), continuousWatcherLeaseTTL)
		if err != nil {
			return err
		}
		if !acquired {
			return fmt.Errorf("another continuous watcher is already running for database %q", cfg.DatabasePath())
		}
		leaseCtx, cancelLease := context.WithCancel(ctx)
		defer cancelLease()
		defer func() {
			if err := db.ReleaseWatcherLease(continuousWatcherLeaseName, leaseOwner); err != nil {
				serviceLogger.warning(time.Now(), "Could not release watcher lease: "+err.Error())
			}
		}()
		ctx = leaseCtx
	}
	var leaseErrors <-chan error
	if !*once {
		leaseErrors = maintainWatcherLease(ctx, db, continuousWatcherLeaseName, leaseOwner, continuousWatcherLeaseRenew, continuousWatcherLeaseTTL)
	}

	integrationSetupCtx, cancelIntegrationSetup := context.WithTimeout(ctx, 15*time.Second)
	mediaManagers := reconcile.New(integrationSetupCtx, cfg)
	cancelIntegrationSetup()
	for _, problem := range mediaManagers.Problems() {
		if *once {
			fmt.Fprintln(stdout, "warning:", problem)
		} else {
			serviceLogger.warning(time.Now(), problem)
		}
	}
	mediaManagers.SetSonarrFilenameCache(db)
	password, err := resolveVLCCredential(ctx, cfg.VLC, *once, serviceLogger, leaseErrors, dependencies)
	if err != nil {
		if !*once && errors.Is(err, context.Canceled) {
			serviceLogger.info(time.Now(), "Watcher stopped.")
			return nil
		}
		return err
	}
	client := vlc.NewClient(cfg.VLC.Endpoint, password)
	processor := watch.NewProcessor(cfg.Watch.EpisodeThreshold, cfg.Watch.MovieThreshold)
	lastObservation := ""
	poll := func(showStatus bool) error {
		status, err := client.Status(ctx)
		if err != nil {
			return fmt.Errorf("VLC connection failed: %w", err)
		}
		if err := db.RecordWatcherHeartbeat(time.Now()); err != nil {
			return err
		}
		observation := status.State + "\x00" + status.MediaPath
		if !showStatus && observation != lastObservation {
			if status.MediaPath == "" {
				serviceLogger.info(time.Now(), fmt.Sprintf("VLC state changed: %s (no active media).", status.State))
			} else {
				serviceLogger.info(time.Now(), fmt.Sprintf("Watching VLC: %s (%s).", serviceLogger.media(status.MediaPath), status.State))
			}
			lastObservation = observation
		}
		event, ok := processor.Process(status, time.Now())
		if !ok {
			if showStatus {
				fmt.Fprintf(stdout, "VLC: state=%s media=%q progress=%.1f%% length=%ds; no completed watch event yet.\n", status.State, status.MediaPath, status.Position*100, status.LengthSeconds)
			}
			return nil
		}
		if !mediaManagers.Active() {
			event.Status = string(reconcile.StatusLocal)
		}
		created, err := db.RecordEvent(event)
		if err != nil {
			return err
		}
		if created {
			if showStatus {
				fmt.Fprintf(stdout, "Recorded local watched event: %s (%.0f%%)\n", event.MediaPath, event.Progress*100)
			} else {
				serviceLogger.info(time.Now(), fmt.Sprintf("Recorded local watched event: %s (%.0f%%)", serviceLogger.media(event.MediaPath), event.Progress*100))
			}
		} else {
			if showStatus {
				fmt.Fprintln(stdout, "Watch event already recorded; the local record was not duplicated.")
			} else {
				serviceLogger.info(time.Now(), "Watch event already recorded; the local record was not duplicated.")
			}
		}

		outcome, err := reconcileEvent(db, cfg, mediaManagers, event)
		if err != nil {
			return err
		}
		for _, message := range outcome.Messages {
			if outcome.Status == reconcile.StatusFailed || outcome.Status == reconcile.StatusUnmatched {
				if showStatus {
					fmt.Fprintln(stdout, "warning:", message)
				} else {
					serviceLogger.warning(time.Now(), message)
				}
			} else {
				if showStatus {
					fmt.Fprintln(stdout, message)
				} else {
					serviceLogger.info(time.Now(), message)
				}
			}
		}
		return nil
	}
	if *once {
		return poll(true)
	}
	serviceLogger.info(time.Now(), fmt.Sprintf("Watching VLC at %s every %s.", cfg.VLC.Endpoint, cfg.Watch.PollInterval))
	ticker := time.NewTicker(cfg.Watch.PollInterval)
	defer ticker.Stop()
	lastPollError := ""
	for {
		if err := poll(false); err != nil {
			lastPollError = err.Error()
			serviceLogger.warning(time.Now(), lastPollError)
		} else if lastPollError != "" {
			serviceLogger.pollRecovered(time.Now(), lastPollError)
			lastPollError = ""
		}
		select {
		case <-ctx.Done():
			serviceLogger.info(time.Now(), "Watcher stopped.")
			return nil
		case err := <-leaseErrors:
			return err
		case <-ticker.C:
		}
	}
}

func resolveVLCCredential(ctx context.Context, cfg config.VLCConfig, once bool, logger *watchServiceLogger, leaseErrors <-chan error, dependencies watchDependencies) (string, error) {
	paused := false
	for attempt := 0; ; attempt++ {
		resolveCtx, cancel := context.WithTimeout(ctx, dependencies.credentialResolveLimit)
		resolution := dependencies.resolveVLC(resolveCtx, cfg)
		cancel()
		if resolution.Ready() {
			if paused {
				logger.credentialRecovered(time.Now())
			}
			return resolution.Value, nil
		}
		if once {
			return "", vlcCredentialResolutionError(resolution)
		}
		if !paused {
			logger.credentialPaused(time.Now(), resolution.State)
			paused = true
		}
		if err := waitForVLCCredentialRetry(ctx, leaseErrors, vlcCredentialRetryDelay(attempt, dependencies.credentialRetryDelays)); err != nil {
			return "", err
		}
	}
}

func vlcCredentialResolutionError(resolution credentials.Resolution) error {
	if strings.TrimSpace(resolution.SafeMessage) != "" {
		return errors.New(resolution.SafeMessage)
	}
	return errors.New("VLC credential could not be resolved.")
}

func vlcCredentialRetryDelay(attempt int, delays []time.Duration) time.Duration {
	if len(delays) == 0 {
		return 5 * time.Minute
	}
	if attempt < len(delays) {
		return delays[attempt]
	}
	return delays[len(delays)-1]
}

func waitForVLCCredentialRetry(ctx context.Context, leaseErrors <-chan error, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-leaseErrors:
		return err
	case <-timer.C:
		return nil
	}
}

func secureContinuousOutput(writer io.Writer) error {
	file, ok := writer.(*os.File)
	if !ok {
		return nil
	}
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect continuous watcher output: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("secure continuous watcher output: %w", err)
	}
	return nil
}

type watchServiceLogger struct {
	writer             io.Writer
	verbose            bool
	lastWarning        string
	lastWarningAt      time.Time
	suppressedWarnings int
}

func newWatchServiceLogger(writer io.Writer, verbose bool) *watchServiceLogger {
	return &watchServiceLogger{writer: writer, verbose: verbose}
}

func (l *watchServiceLogger) info(now time.Time, message string) {
	fmt.Fprintf(l.writer, "%s INFO %s\n", now.UTC().Format(time.RFC3339), message)
}

func (l *watchServiceLogger) warning(now time.Time, message string) {
	if message == l.lastWarning && now.Sub(l.lastWarningAt) < repeatedWarningReportInterval {
		l.suppressedWarnings++
		return
	}
	if l.suppressedWarnings > 0 {
		l.info(now, fmt.Sprintf("Suppressed %d repeated warning(s).", l.suppressedWarnings))
	}
	fmt.Fprintf(l.writer, "%s WARN %s\n", now.UTC().Format(time.RFC3339), message)
	l.lastWarning = message
	l.lastWarningAt = now
	l.suppressedWarnings = 0
}

func (l *watchServiceLogger) pollRecovered(now time.Time, pollWarning string) {
	if l.lastWarning == pollWarning && l.suppressedWarnings > 0 {
		l.info(now, fmt.Sprintf("VLC polling recovered after %d repeated warning(s) were suppressed.", l.suppressedWarnings))
	} else {
		l.info(now, "VLC polling recovered.")
	}
	if l.lastWarning == pollWarning {
		l.lastWarning = ""
		l.lastWarningAt = time.Time{}
		l.suppressedWarnings = 0
	}
}

func (l *watchServiceLogger) credentialPaused(now time.Time, state credentials.State) {
	l.warning(now, vlcCredentialPausedMessage(state))
}

func (l *watchServiceLogger) credentialRecovered(now time.Time) {
	l.info(now, "VLC credential repaired; resuming VLC observation.")
	l.lastWarning = ""
	l.lastWarningAt = time.Time{}
	l.suppressedWarnings = 0
}

func vlcCredentialPausedMessage(state credentials.State) string {
	const paused = "Watching is paused until the VLC credential is repaired; retrying automatically."
	switch state {
	case credentials.StateNotConfigured:
		return "VLC credential is not configured. " + paused
	case credentials.StateProviderUnavailable:
		return "VLC credential provider is unavailable. " + paused
	case credentials.StateNeedsUserAction:
		return "VLC credential needs user action. " + paused
	case credentials.StateCredentialMissing:
		return "VLC credential is missing. " + paused
	case credentials.StateCredentialDenied:
		return "VLC credential access was denied. " + paused
	case credentials.StateCredentialInvalid:
		return "VLC credential is invalid. " + paused
	default:
		return "VLC credential needs repair. " + paused
	}
}

func (l *watchServiceLogger) media(value string) string {
	if l.verbose {
		return value
	}
	name := path.Base(strings.ReplaceAll(value, "\\", "/"))
	if name == "." || name == "/" || name == "" {
		return "(media path hidden)"
	}
	return name
}

func newWatcherLeaseOwner() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("create watcher lease owner: %w", err)
	}
	return hex.EncodeToString(random), nil
}

func maintainWatcherLease(ctx context.Context, db *store.Store, name, owner string, interval, ttl time.Duration) <-chan error {
	errors := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				renewed, err := db.RenewWatcherLease(name, owner, now, ttl)
				if err != nil {
					errors <- err
					return
				}
				if !renewed {
					errors <- fmt.Errorf("continuous watcher lease was lost to another process")
					return
				}
			}
		}
	}()
	return errors
}

func runSecret(args []string, stdout io.Writer) error {
	if len(args) < 1 || args[0] != "set" {
		return fmt.Errorf("usage: %s secret set [vlc|sonarr|radarr|anilist|myanimelist|trakt|simkl|<tracker>-client-secret] [--config <path>]", appName)
	}
	target := "vlc"
	flagArgs := args[1:]
	if len(flagArgs) > 0 && !strings.HasPrefix(flagArgs[0], "-") {
		target = strings.ToLower(flagArgs[0])
		flagArgs = flagArgs[1:]
	}
	fs := flag.NewFlagSet("secret set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", config.DefaultPath(), "configuration file")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: %s secret set [vlc|sonarr|radarr|anilist|myanimelist|trakt|simkl|<tracker>-client-secret] [--config <path>]", appName)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	label, source, reference, err := secretTarget(cfg, target)
	if err != nil {
		return err
	}
	if source != "keyring" {
		return fmt.Errorf("%s uses secret_source = %q; secret set is only for keyring secrets", target, source)
	}
	fmt.Fprintf(stdout, "%s (stored in the system keyring): ", label)
	secret, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(stdout)
	if err != nil {
		return err
	}
	if err := secrets.StoreInKeyring(reference, string(secret)); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Stored %s in the system keyring as %q.\n", label, reference)
	return nil
}

func secretTarget(cfg *config.Config, target string) (label, source, reference string, err error) {
	if trackerName, found := strings.CutSuffix(target, "-client-secret"); found {
		trackerConfig, ok := cfg.Trackers[trackerName]
		if !ok {
			return "", "", "", fmt.Errorf("unknown tracker client secret %q", target)
		}
		return titleTracker(trackerName) + " OAuth client secret", trackerConfig.ClientSecretSource, trackerConfig.ClientSecretReference, nil
	}
	switch target {
	case "vlc":
		return "VLC password", cfg.VLC.SecretSource, cfg.VLC.SecretReference, nil
	case "sonarr":
		return "Sonarr API key", cfg.Sonarr.SecretSource, cfg.Sonarr.SecretReference, nil
	case "radarr":
		return "Radarr API key", cfg.Radarr.SecretSource, cfg.Radarr.SecretReference, nil
	case "anidb":
		return "", "", "", fmt.Errorf("AniDB matching does not use an account secret")
	default:
		trackerConfig, ok := cfg.Trackers[target]
		if !ok {
			return "", "", "", fmt.Errorf("unknown secret target %q (use vlc, sonarr, radarr, anilist, myanimelist, trakt, or simkl)", target)
		}
		return titleTracker(target) + " access token", trackerConfig.SecretSource, trackerConfig.SecretReference, nil
	}
}

func titleTracker(name string) string {
	switch name {
	case "anilist":
		return "AniList"
	case "anidb":
		return "AniDB"
	case "myanimelist":
		return "MyAnimeList"
	case "simkl":
		return "SIMKL"
	default:
		return "Trakt"
	}
}

func runIntegrations(args []string, stdout io.Writer) error {
	if len(args) < 2 || args[0] != "test" {
		return fmt.Errorf("usage: %s integrations test <sonarr|radarr> [--config <path>]", appName)
	}
	manager, err := mediaManager(args[1])
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("integrations test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", config.DefaultPath(), "configuration file")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: %s integrations test <sonarr|radarr> [--config <path>]", appName)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), secrets.ForegroundResolveTimeout)
	defer cancel()
	instance, err := reconcile.Test(ctx, cfg, manager)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(instance.InstanceName)
	if name == "" {
		name = instance.AppName
	}
	fmt.Fprintf(stdout, "%s connection OK: %s %s (instance %q). No library changes were made.\n", titleManager(manager), instance.AppName, instance.Version, name)
	return nil
}

func mediaManager(value string) (arr.Manager, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(arr.ManagerSonarr):
		return arr.ManagerSonarr, nil
	case string(arr.ManagerRadarr):
		return arr.ManagerRadarr, nil
	default:
		return "", fmt.Errorf("unknown media manager %q (use sonarr or radarr)", value)
	}
}

func titleManager(manager arr.Manager) string {
	if manager == arr.ManagerSonarr {
		return "Sonarr"
	}
	return "Radarr"
}

func runEvents(args []string, stdout io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "retry":
			return runRetryEvents(args[1:], stdout)
		case "prune":
			return runPruneEvents(args[1:], stdout)
		case "recent":
			return runEventList(args[1:], stdout, eventListRecent)
		case "history":
			return runEventList(args[1:], stdout, eventListHistory)
		case "needs-attention":
			return runEventList(args[1:], stdout, eventListAttention)
		}
	}
	return runEventList(args, stdout, eventListDefault)
}

type eventListMode uint8

const (
	eventListDefault eventListMode = iota
	eventListRecent
	eventListHistory
	eventListAttention
)

type displayedEvent struct {
	event    watch.Event
	trackers []store.EventTrackerState
	title    string
}

func runEventList(args []string, stdout io.Writer, mode eventListMode) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", config.DefaultPath(), "configuration file")
	trackerFilter := fs.String("tracker", "", "tracker name")
	limit := fs.Int("limit", eventListLimit(mode), "maximum rows")
	verbose := fs.Bool("verbose", false, "show diagnostic state")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *limit <= 0 {
		return fmt.Errorf("usage: %s events [--tracker <name>] [--limit <n>] [--verbose] [--config <path>]", appName)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer db.Close()

	if mode == eventListDefault {
		attentionEvents, err := db.AttentionEvents(*limit)
		if err != nil {
			return err
		}
		attention, err := displayedEventsFor(db, attentionEvents, *trackerFilter)
		if err != nil {
			return err
		}
		recentEvents, err := db.CompletedEvents(5)
		if err != nil {
			return err
		}
		recent, err := displayedEventsFor(db, recentEvents, *trackerFilter)
		if err != nil {
			return err
		}
		if len(attention) == 0 && len(recent) == 0 {
			fmt.Fprintln(stdout, "No watched events recorded.")
			return nil
		}
		if len(attention) > 0 {
			printEventSection(stdout, "Needs attention", attention, *verbose)
		}
		if len(recent) > 0 {
			printEventSection(stdout, "Recent completed watches", recent, *verbose)
		}
		return nil
	}

	var events []watch.Event
	switch mode {
	case eventListRecent:
		events, err = db.CompletedEvents(*limit)
	case eventListHistory:
		events, err = db.RecentEvents(*limit)
	case eventListAttention:
		events, err = db.AttentionEvents(*limit)
	}
	if err != nil {
		return err
	}
	rows, err := displayedEventsFor(db, events, *trackerFilter)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		if mode == eventListAttention {
			fmt.Fprintln(stdout, "No watches need attention.")
		} else {
			fmt.Fprintln(stdout, "No watched events matched.")
		}
		return nil
	}
	printEventSection(stdout, eventSectionTitle(mode), rows, *verbose)
	return nil
}

func eventListLimit(mode eventListMode) int {
	switch mode {
	case eventListDefault:
		return 100
	case eventListHistory:
		return 50
	default:
		return 25
	}
}

func eventSectionTitle(mode eventListMode) string {
	switch mode {
	case eventListRecent:
		return "Recent completed watches"
	case eventListHistory:
		return "Watch history"
	case eventListAttention:
		return "Needs attention"
	default:
		return "Watched events"
	}
}

func displayedEventsFor(db *store.Store, events []watch.Event, trackerFilter string) ([]displayedEvent, error) {
	filter := strings.ToLower(strings.TrimSpace(trackerFilter))
	rows := make([]displayedEvent, 0, len(events))
	for _, event := range events {
		trackers, err := db.EventTrackerStates(event)
		if err != nil {
			return nil, err
		}
		if filter != "" && !eventHasTracker(trackers, filter) {
			continue
		}
		rows = append(rows, displayedEvent{event: event, trackers: trackers, title: eventDisplayTitle(db, event)})
	}
	return rows, nil
}

func eventHasTracker(trackers []store.EventTrackerState, filter string) bool {
	for _, state := range trackers {
		if strings.EqualFold(state.Tracker, filter) {
			return true
		}
	}
	return false
}

func printEventSection(stdout io.Writer, title string, rows []displayedEvent, verbose bool) {
	fmt.Fprintln(stdout, eventColor(stdout, "1;36", title))
	for _, row := range rows {
		printEventRow(stdout, row, verbose)
	}
	fmt.Fprintln(stdout)
}

func printEventRow(stdout io.Writer, row displayedEvent, verbose bool) {
	event := row.event
	status, color := eventDisplayStatus(event)
	when := eventColor(stdout, "2", event.WatchedAt.Local().Format("02 Jan · 15:04"))
	fmt.Fprintf(stdout, "  %s  %s\n", eventColor(stdout, color, status), when)
	fmt.Fprintf(stdout, "     %s\n", row.title)

	metadata := []string{fmt.Sprintf("%.0f%% watched", event.Progress*100)}
	if event.Manager != "" {
		metadata = append(metadata, displayManager(event.Manager))
	}
	fmt.Fprintf(stdout, "     %s\n", eventColor(stdout, "2", strings.Join(metadata, " · ")))
	if len(row.trackers) > 0 {
		fmt.Fprintf(stdout, "     %s\n", eventColor(stdout, "36", eventTrackerSummary(row.trackers)))
	}
	if verbose {
		fmt.Fprintf(stdout, "     %s\n", eventColor(stdout, "2", "diagnostic: status="+event.Status+" · path="+event.MediaPath))
	}
}

func eventDisplayTitle(db *store.Store, event watch.Event) string {
	fallback := filepath.Base(event.MediaPath)
	if event.Manager == "" || event.SourceID <= 0 {
		return fallback
	}
	scope, season := "media", -1
	if event.Manager == "sonarr" {
		scope = "series"
	}
	unit, err := db.MediaUnit(event.Manager, event.SourceID, scope, season)
	if err != nil || unit.Title == "" {
		return fallback
	}
	if event.Manager != "sonarr" || event.SeasonNumber <= 0 || len(event.EpisodeNumbers) == 0 {
		return unit.Title
	}
	episodes := append([]int(nil), event.EpisodeNumbers...)
	sort.Ints(episodes)
	label := fmt.Sprintf("S%02dE%02d", event.SeasonNumber, episodes[0])
	if len(episodes) > 1 {
		label += fmt.Sprintf("–E%02d", episodes[len(episodes)-1])
	}
	return unit.Title + " — " + label
}

func eventDisplayStatus(event watch.Event) (string, string) {
	switch event.Status {
	case "unmonitored", "already-unmonitored":
		return "✓  Unmonitored", "1;32"
	case "local":
		return "✓  Watched locally", "1;32"
	case "pending":
		return "•  Waiting to reconcile", "1;33"
	case "unmatched":
		return "!  Needs library match", "1;33"
	case "failed":
		return "×  Needs attention", "1;31"
	default:
		return "•  Recorded", "2"
	}
}

func displayManager(manager string) string {
	switch manager {
	case "sonarr":
		return "Sonarr"
	case "radarr":
		return "Radarr"
	default:
		return manager
	}
}

func eventTrackerSummary(states []store.EventTrackerState) string {
	labels := make([]string, 0, len(states))
	for _, state := range states {
		name := displayTracker(state.Tracker)
		switch state.Status {
		case "mapped":
			labels = append(labels, name+" mapped")
		case "synced", "already_synced":
			if state.Detail != "" {
				labels = append(labels, name+": "+state.Detail)
			} else {
				labels = append(labels, name+" synced")
			}
		case "failed", "review":
			labels = append(labels, name+" needs attention")
		default:
			labels = append(labels, name+" "+state.Status)
		}
	}
	return strings.Join(labels, " · ")
}

func eventColor(stdout io.Writer, code, value string) string {
	if value == "" || !eventColorEnabled(stdout) {
		return value
	}
	return "\x1b[" + code + "m" + value + "\x1b[0m"
}

func eventColorEnabled(stdout io.Writer) bool {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled {
		return false
	}
	file, ok := stdout.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func displayTracker(name string) string {
	switch strings.ToLower(name) {
	case "anilist":
		return "AniList"
	case "anidb":
		return "AniDB"
	case "myanimelist":
		return "MyAnimeList"
	case "simkl":
		return "SIMKL"
	case "trakt":
		return "Trakt"
	default:
		return name
	}
}

func runPruneEvents(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("events prune", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", config.DefaultPath(), "configuration file")
	olderThan := fs.String("older-than", "", "age such as 90d")
	dryRun := fs.Bool("dry-run", false, "preview eligible events")
	apply := fs.Bool("apply", false, "delete eligible events")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || strings.TrimSpace(*olderThan) == "" || (*dryRun && *apply) {
		return fmt.Errorf("usage: %s events prune --older-than <age> [--dry-run|--apply] [--config <path>]", appName)
	}
	age, err := parseEventAge(*olderThan)
	if err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer db.Close()
	candidates, err := db.PrunableEvents(time.Now().UTC().Add(-age))
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(candidates))
	for _, event := range candidates {
		ready, err := eventReadyForPrune(db, cfg, event)
		if err != nil {
			return err
		}
		if ready {
			paths = append(paths, event.MediaPath)
		}
	}
	if len(paths) == 0 {
		fmt.Fprintln(stdout, "No fully reconciled events are eligible for pruning.")
		return nil
	}
	if !*apply {
		fmt.Fprintf(stdout, "Dry run: %d fully reconciled event(s) older than %s would be removed. Run with --apply to delete them.\n", len(paths), *olderThan)
		return nil
	}
	deleted, err := db.DeleteEvents(paths)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Pruned %d fully reconciled event(s). Confirmed tracker mappings were kept.\n", deleted)
	return nil
}

func parseEventAge(value string) (time.Duration, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("--older-than must be a positive duration such as 90d")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	age, err := time.ParseDuration(value)
	if err != nil || age <= 0 {
		return 0, fmt.Errorf("--older-than must be a positive duration such as 90d")
	}
	return age, nil
}

func eventReadyForPrune(db *store.Store, cfg *config.Config, event watch.Event) (bool, error) {
	aniList := cfg.Trackers["anilist"]
	if !aniList.Enabled || !aniList.SyncProgress || event.Manager != "sonarr" || event.SourceID <= 0 || event.SeasonNumber <= 0 {
		return true, nil
	}
	job, found, err := db.TrackerSyncJob(event.MediaPath, "anilist")
	if err != nil {
		return false, err
	}
	return found && (job.Status == "synced" || job.Status == "already_synced"), nil
}

func reconcileEvent(db *store.Store, cfg *config.Config, mediaManagers *reconcile.Reconciler, event watch.Event) (reconcile.Outcome, error) {
	outcome := mediaManagers.Process(context.Background(), event.MediaPath)
	if outcome.Match != nil && outcome.Match.Identity.SourceID > 0 {
		if _, err := db.UpsertIdentity(outcome.Match.Identity); err != nil {
			return reconcile.Outcome{}, fmt.Errorf("record resolved media identity: %w", err)
		}
		event.Manager = string(outcome.Match.Identity.Manager)
		event.SourceID = outcome.Match.Identity.SourceID
		event.SeasonNumber = outcome.Match.Identity.SeasonNumber
		event.EpisodeNumbers = append([]int(nil), outcome.Match.Identity.EpisodeNumbers...)
		if err := db.UpdateEventResolution(event); err != nil {
			return reconcile.Outcome{}, err
		}
	} else if fallbackTrackerIdentityAllowed(cfg, outcome.Status) {
		identity, found, err := db.UpsertLocalParsedPath(event.MediaPath)
		if err != nil {
			return reconcile.Outcome{}, fmt.Errorf("record parsed local media identity: %w", err)
		}
		if found {
			event.Manager = string(identity.Manager)
			event.SourceID = identity.SourceID
			event.SeasonNumber = identity.SeasonNumber
			event.EpisodeNumbers = append([]int(nil), identity.EpisodeNumbers...)
			if err := db.UpdateEventResolution(event); err != nil {
				return reconcile.Outcome{}, err
			}
			outcome.Messages = append(outcome.Messages, "Created a provisional local title for tracker review; no tracker mapping or watched-state update was inferred.")
		}
	}
	if err := db.UpdateEventStatus(event.MediaPath, string(outcome.Status)); err != nil {
		return reconcile.Outcome{}, err
	}
	if cfg != nil {
		syncCtx, cancelSync := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelSync()
		job, syncErr := tracker.SyncAniListEvent(syncCtx, cfg.Trackers["anilist"], db, event)
		if syncErr != nil {
			outcome.Messages = append(outcome.Messages, "AniList sync was not completed; inspect Tracking.")
		} else if job.Status != "" {
			outcome.Messages = append(outcome.Messages, "AniList: "+job.Detail)
		}
	}
	return outcome, nil
}

func fallbackTrackerIdentityAllowed(cfg *config.Config, status reconcile.Status) bool {
	if cfg == nil || (status != reconcile.StatusLocal && status != reconcile.StatusUnmatched) {
		return false
	}
	for _, trackerConfig := range cfg.Trackers {
		if trackerConfig.Enabled {
			return true
		}
	}
	return false
}

func runRetryEvents(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("events retry", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", config.DefaultPath(), "configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: %s events retry [--config <path>]", appName)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	setupCtx, cancelSetup := context.WithTimeout(context.Background(), 15*time.Second)
	mediaManagers := reconcile.New(setupCtx, cfg)
	cancelSetup()
	if !mediaManagers.Active() && !fallbackTrackerIdentityAllowed(cfg, reconcile.StatusLocal) {
		return fmt.Errorf("no Sonarr/Radarr metadata lookup or enabled tracker fallback is available")
	}
	for _, problem := range mediaManagers.Problems() {
		return fmt.Errorf("cannot retry events: %s", problem)
	}
	db, err := store.Open(cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer db.Close()
	mediaManagers.SetSonarrFilenameCache(db)
	events, err := db.RetryableEvents(500)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		fmt.Fprintln(stdout, "No pending, unmatched, or failed events need retrying.")
		return nil
	}
	for _, event := range events {
		outcome, err := reconcileEvent(db, cfg, mediaManagers, event)
		if err != nil {
			return fmt.Errorf("retry %q: %w", event.MediaPath, err)
		}
		fmt.Fprintf(stdout, "%s  %-18s  %s\n", outcome.Status, event.MediaPath, strings.Join(outcome.Messages, " "))
	}
	return nil
}

func runMappings(args []string, stdout io.Writer) error {
	usage := fmt.Sprintf("usage: %s mappings confirm <tracker> --manager <sonarr|radarr> --source-id <id> [--season <n>] --id <tracker-id> --title <verified title> [--config <path>]", appName)
	if len(args) < 2 || args[0] != "confirm" {
		return fmt.Errorf("%s", usage)
	}
	trackerName := strings.ToLower(strings.TrimSpace(args[1]))
	if trackerName != "anilist" && trackerName != "anidb" && trackerName != "myanimelist" && trackerName != "trakt" && trackerName != "simkl" {
		return fmt.Errorf("unknown tracker %q", trackerName)
	}
	fs := flag.NewFlagSet("mappings confirm", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	managerValue := fs.String("manager", "", "Sonarr or Radarr")
	sourceID := fs.Int("source-id", 0, "manager source ID")
	season := fs.Int("season", -1, "Sonarr season number")
	trackerID := fs.String("id", "", "verified tracker ID")
	title := fs.String("title", "", "verified tracker title")
	configPath := fs.String("config", config.DefaultPath(), "configuration file")
	if err := fs.Parse(args[2:]); err != nil || fs.NArg() != 0 {
		return fmt.Errorf("%s", usage)
	}
	manager, err := mediaManager(*managerValue)
	if err != nil {
		return err
	}
	if *sourceID <= 0 || strings.TrimSpace(*trackerID) == "" || strings.TrimSpace(*title) == "" {
		return fmt.Errorf("--source-id, --id, and --title are required")
	}
	scope := "media"
	if manager == arr.ManagerSonarr {
		if *season <= 0 {
			return fmt.Errorf("Sonarr tracker mappings require an explicit positive --season")
		}
		scope = "season"
	} else if *season != -1 {
		return fmt.Errorf("--season is only valid for Sonarr")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer db.Close()
	unit, err := db.MediaUnit(string(manager), *sourceID, scope, *season)
	if err != nil {
		return fmt.Errorf("find resolved %s unit: %w; run events retry first", titleManager(manager), err)
	}
	if err := db.ConfirmMapping(store.TrackerMapping{MediaUnitID: unit.ID, Tracker: trackerName, TrackerID: *trackerID, TrackerTitle: strings.TrimSpace(*title)}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Confirmed %s ID %s for %s %s. No tracker watch state was changed.\n", titleTracker(trackerName), *trackerID, unit.Title, unit.Scope)
	return nil
}

func runTUI(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", config.DefaultPath(), "configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return tui.Run(*configPath, os.Stdin, stdout)
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `%s — local VLC watch tracking, safe by default

Usage:
  %s                 Open the TUI
  %s version         Print version and build provenance
  %s setup           Create an example configuration
  %s watch --once    Poll VLC once and record a completed local watch event
                      (runs explicitly enabled monitored-status updates)
  %s events                    Show watches needing attention and recent completions
  %s events recent|history     Show completed watches or a larger history
  %s events needs-attention    Show only watches requiring action
  %s events retry              Retry pending, unmatched, and failed events
  %s events prune              Preview or remove old fully reconciled events
  %s mappings confirm
                      Save a human-verified tracker ID for a resolved item
  %s integrations test <sonarr|radarr>
                      Test a media-manager endpoint and API key without writing
  %s secret set [vlc|sonarr|radarr]
                      Store a configured secret in the system keyring

Sonarr and Radarr monitored-status writes are disabled unless explicitly enabled.
`, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName)
}

package tracker

import (
	"context"
	"fmt"

	"github.com/Cyberlane/vlc-media-watcher/internal/config"
	"github.com/Cyberlane/vlc-media-watcher/internal/credentials"
	"github.com/Cyberlane/vlc-media-watcher/internal/secrets"
	"github.com/Cyberlane/vlc-media-watcher/internal/store"
	"github.com/Cyberlane/vlc-media-watcher/internal/watch"
)

// SyncAniListEvent applies an opt-in progress update for one exact Sonarr
// episode event. It records every result locally so a successful event is not
// sent twice, and an unsafe progress gap is visible for review rather than
// guessed away.
func SyncAniListEvent(ctx context.Context, cfg config.TrackerConfig, db *store.Store, event watch.Event) (store.TrackerSyncJob, error) {
	return syncAniListEvent(ctx, cfg, db, event, nil)
}

// SyncAniListEventWithProgress reports the user-visible stages of a manual
// tracker sync without exposing credentials or API payloads.
func SyncAniListEventWithProgress(ctx context.Context, cfg config.TrackerConfig, db *store.Store, event watch.Event, report func(string)) (store.TrackerSyncJob, error) {
	return syncAniListEvent(ctx, cfg, db, event, report)
}

func syncAniListEvent(ctx context.Context, cfg config.TrackerConfig, db *store.Store, event watch.Event, report func(string)) (store.TrackerSyncJob, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !cfg.Enabled || !cfg.SyncProgress {
		return store.TrackerSyncJob{}, nil
	}
	if db == nil {
		return store.TrackerSyncJob{}, fmt.Errorf("tracker sync store is unavailable")
	}
	if event.Manager != "sonarr" || event.SourceID <= 0 || event.SeasonNumber <= 0 {
		return store.TrackerSyncJob{}, nil
	}
	if report != nil {
		report("Checking the confirmed AniList season mapping…")
	}
	unit, err := db.MediaUnit(event.Manager, event.SourceID, "season", event.SeasonNumber)
	if err != nil {
		return store.TrackerSyncJob{}, fmt.Errorf("find season mapping target: %w", err)
	}
	mappings, err := db.MappingsForUnit(unit.ID)
	if err != nil {
		return store.TrackerSyncJob{}, err
	}
	var mapping store.TrackerMapping
	for _, candidate := range mappings {
		if candidate.Tracker == string(AniList) {
			mapping = candidate
			break
		}
	}
	if mapping.TrackerID == "" {
		return store.TrackerSyncJob{}, nil
	}
	if existing, found, err := db.TrackerSyncJob(event.MediaPath, string(AniList)); err != nil {
		return store.TrackerSyncJob{}, err
	} else if found && existing.Status == "synced" && existing.TrackerID == mapping.TrackerID {
		return existing, nil
	}
	if report != nil {
		report("Reading the secured AniList account token…")
	}
	resolveCtx, cancelResolve := context.WithTimeout(ctx, secrets.BackgroundResolveTimeout)
	resolution := secrets.ResolveTrackerAccessToken(resolveCtx, string(AniList), cfg)
	cancelResolve()
	if !resolution.Ready() {
		job := store.TrackerSyncJob{EventPath: event.MediaPath, Tracker: string(AniList), TrackerID: mapping.TrackerID, Status: "failed", Detail: trackerCredentialFailureDetail(resolution.State)}
		if err := db.UpsertTrackerSyncJob(job); err != nil {
			return job, fmt.Errorf("record AniList credential failure: %w", err)
		}
		return job, secrets.SafeResolutionError(resolution)
	}
	result, err := syncAniListProgress(ctx, resolution.Value, mapping.TrackerID, event.EpisodeNumbers, report)
	job := store.TrackerSyncJob{EventPath: event.MediaPath, Tracker: string(AniList), TrackerID: mapping.TrackerID, Status: result.Status, Detail: result.Detail, TargetProgress: result.TargetProgress}
	if err != nil {
		job.Status = "failed"
		job.Detail = "AniList progress could not be updated: " + err.Error()
	}
	if saveErr := db.UpsertTrackerSyncJob(job); saveErr != nil {
		return job, saveErr
	}
	return job, err
}

func trackerCredentialFailureDetail(state credentials.State) string {
	switch state {
	case credentials.StateNotConfigured:
		return "AniList access token is not configured; relink the account."
	case credentials.StateProviderUnavailable:
		return "AniList token provider is unavailable; retry after repairing it."
	case credentials.StateNeedsUserAction:
		return "AniList token needs user action; relink the account."
	case credentials.StateCredentialMissing:
		return "AniList access token is missing; relink the account."
	case credentials.StateCredentialDenied:
		return "AniList access-token access was denied; repair the credential binding."
	case credentials.StateCredentialInvalid:
		return "AniList access token is invalid; relink the account."
	default:
		return "AniList access token needs repair; relink the account."
	}
}

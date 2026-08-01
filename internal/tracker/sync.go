package tracker

import (
	"context"
	"fmt"

	"github.com/Cyberlane/vlc-media-watcher/internal/config"
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
	token, err := secrets.ResolveValue(ctx, "AniList access token", cfg.SecretSource, cfg.SecretReference, cfg.AccessTokenEnv)
	if err != nil {
		job := store.TrackerSyncJob{EventPath: event.MediaPath, Tracker: string(AniList), TrackerID: mapping.TrackerID, Status: "failed", Detail: "AniList access token is unavailable; relink the account."}
		_ = db.UpsertTrackerSyncJob(job)
		return job, err
	}
	result, err := syncAniListProgress(ctx, token, mapping.TrackerID, event.EpisodeNumbers, report)
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

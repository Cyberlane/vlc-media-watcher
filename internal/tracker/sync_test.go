package tracker

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cyberlane/vlc-media-watcher/internal/arr"
	"github.com/Cyberlane/vlc-media-watcher/internal/config"
	"github.com/Cyberlane/vlc-media-watcher/internal/store"
	"github.com/Cyberlane/vlc-media-watcher/internal/watch"
)

func TestSyncAniListEventRecordsCredentialFailureWithoutProviderMetadata(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	units, err := db.UpsertIdentity(arr.MediaIdentity{
		Manager:      arr.ManagerSonarr,
		SourceID:     42,
		Kind:         "series",
		Title:        "Example Anime",
		SeasonNumber: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("identity units = %#v, want series and season", units)
	}
	if err := db.ConfirmMapping(store.TrackerMapping{
		MediaUnitID:  units[1].ID,
		Tracker:      string(AniList),
		TrackerID:    "123",
		TrackerTitle: "Example Anime",
	}); err != nil {
		t.Fatal(err)
	}

	const missingEnvironment = "VLC_MEDIA_WATCHER_TEST_MISSING_ANILIST_TOKEN"
	event := watch.Event{
		MediaPath:      "/media/Example.Anime.S01E01.mkv",
		Progress:       0.95,
		WatchedAt:      time.Now().UTC(),
		Status:         "unmonitored",
		Manager:        string(arr.ManagerSonarr),
		SourceID:       42,
		SeasonNumber:   1,
		EpisodeNumbers: []int{1},
	}
	if created, err := db.RecordEvent(event); err != nil || !created {
		t.Fatalf("RecordEvent() = created:%t err:%v", created, err)
	}
	job, err := SyncAniListEvent(context.Background(), config.TrackerConfig{
		Enabled:        true,
		SyncProgress:   true,
		SecretSource:   "environment",
		AccessTokenEnv: missingEnvironment,
	}, db, event)
	if err == nil {
		t.Fatal("SyncAniListEvent() error = nil, want unavailable token error")
	}
	if !strings.Contains(err.Error(), "AniList access token is not available") || strings.Contains(err.Error(), missingEnvironment) {
		t.Fatalf("SyncAniListEvent() error = %q", err)
	}
	if job.Status != "failed" || job.TrackerID != "123" || !strings.Contains(job.Detail, "missing") || strings.Contains(job.Detail, missingEnvironment) {
		t.Fatalf("job = %#v", job)
	}
	stored, found, err := db.TrackerSyncJob(event.MediaPath, string(AniList))
	if err != nil || !found || stored.EventPath != job.EventPath || stored.Tracker != job.Tracker || stored.TrackerID != job.TrackerID || stored.Status != job.Status || stored.Detail != job.Detail || stored.TargetProgress != job.TargetProgress || stored.UpdatedAt.IsZero() {
		t.Fatalf("stored job = %#v, found=%t, err=%v; want persisted %#v", stored, found, err, job)
	}
	incidents, err := db.ActiveCredentialIncidents(10)
	if err != nil || len(incidents) != 1 || incidents[0].CredentialID != "tracker.anilist.access-token" || strings.Contains(incidents[0].Detail, missingEnvironment) {
		t.Fatalf("active incidents = %#v, err=%v", incidents, err)
	}
}

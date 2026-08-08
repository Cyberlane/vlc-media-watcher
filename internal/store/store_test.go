package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/Cyberlane/vlc-media-watcher/internal/arr"
	"github.com/Cyberlane/vlc-media-watcher/internal/credentials"
	"github.com/Cyberlane/vlc-media-watcher/internal/watch"
)

func TestOpenSecuresAndConfiguresDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watcher.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("database mode = %o, want 600", got)
		}
	}
	var foreignKeys, busyTimeout, schemaVersion int
	var journalMode string
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&schemaVersion); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 || busyTimeout != int(databaseBusyTimeout.Milliseconds()) || journalMode != "wal" || schemaVersion != currentSchemaVersion {
		t.Fatalf("database safeguards = foreign_keys:%d busy_timeout:%d journal_mode:%q user_version:%d", foreignKeys, busyTimeout, journalMode, schemaVersion)
	}
}

func TestOpenMigratesLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watcher.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE watched_events (id INTEGER PRIMARY KEY, media_path TEXT NOT NULL UNIQUE, progress REAL NOT NULL, watched_at TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`INSERT INTO watched_events (media_path, progress, watched_at, status, manager, source_id, season_number, episode_numbers) VALUES ('legacy.mkv', .9, '2026-08-01T00:00:00Z', 'local', '', 0, -1, '[]')`); err != nil {
		t.Fatalf("migrated columns unavailable: %v", err)
	}
}

func TestWatcherLeaseIsExclusiveRecoverableAndReleasable(t *testing.T) {
	s := openTestStore(t)
	now := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	if acquired, err := s.AcquireWatcherLease("watch", "first", now, 30*time.Second); err != nil || !acquired {
		t.Fatalf("first lease = %t, %v", acquired, err)
	}
	if acquired, err := s.AcquireWatcherLease("watch", "second", now.Add(time.Second), 30*time.Second); err != nil || acquired {
		t.Fatalf("competing lease = %t, %v", acquired, err)
	}
	if renewed, err := s.RenewWatcherLease("watch", "first", now.Add(2*time.Second), 30*time.Second); err != nil || !renewed {
		t.Fatalf("renew lease = %t, %v", renewed, err)
	}
	if acquired, err := s.AcquireWatcherLease("watch", "second", now.Add(33*time.Second), 30*time.Second); err != nil || !acquired {
		t.Fatalf("recovered lease = %t, %v", acquired, err)
	}
	if renewed, err := s.RenewWatcherLease("watch", "first", now.Add(34*time.Second), 30*time.Second); err != nil || renewed {
		t.Fatalf("stale owner renewal = %t, %v", renewed, err)
	}
	if err := s.ReleaseWatcherLease("watch", "second"); err != nil {
		t.Fatal(err)
	}
	if acquired, err := s.AcquireWatcherLease("watch", "third", now.Add(35*time.Second), 30*time.Second); err != nil || !acquired {
		t.Fatalf("lease after release = %t, %v", acquired, err)
	}
}

func TestRecordEventIsIdempotent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := watch.Event{MediaPath: "/media/example.mkv", Progress: .9, WatchedAt: time.Now().UTC(), Status: "pending"}
	if created, err := s.RecordEvent(e); err != nil || !created {
		t.Fatalf("first record = %v, %v", created, err)
	}
	if created, err := s.RecordEvent(e); err != nil || created {
		t.Fatalf("duplicate record = %v, %v", created, err)
	}
	events, err := s.RecentEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].MediaPath != e.MediaPath {
		t.Fatalf("events = %#v", events)
	}
	if err := s.UpdateEventStatus(e.MediaPath, "unmonitored"); err != nil {
		t.Fatal(err)
	}
	events, err = s.RecentEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Status != "unmonitored" {
		t.Fatalf("status = %q", events[0].Status)
	}
}

func TestCredentialIncidentsPersistDeduplicationAndRecovery(t *testing.T) {
	s := openTestStore(t)
	openedIncident, err := credentials.NewIncident("watcher", credentials.VLCPasswordID, credentials.StateProviderUnavailable)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := s.ObserveCredentialIncident(openedIncident)
	if err != nil || opened.Kind != credentials.IncidentOpened {
		t.Fatalf("opened incident = %#v, err=%v", opened, err)
	}
	duplicate, err := s.ObserveCredentialIncident(openedIncident)
	if err != nil || !duplicate.Empty() {
		t.Fatalf("duplicate incident = %#v, err=%v", duplicate, err)
	}
	changedIncident, err := credentials.NewIncident("watcher", credentials.VLCPasswordID, credentials.StateNeedsUserAction)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := s.ObserveCredentialIncident(changedIncident)
	if err != nil || changed.Kind != credentials.IncidentUpdated {
		t.Fatalf("changed incident = %#v, err=%v", changed, err)
	}
	active, err := s.ActiveCredentialIncidents(10)
	if err != nil || len(active) != 1 || active[0].Detail != changedIncident.Detail || !active[0].Active {
		t.Fatalf("active incidents = %#v, err=%v", active, err)
	}
	recoveredIncident, err := credentials.NewIncident("watcher", credentials.VLCPasswordID, credentials.StateReady)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := s.ObserveCredentialIncident(recoveredIncident)
	if err != nil || recovered.Kind != credentials.IncidentRecovered {
		t.Fatalf("recovered incident = %#v, err=%v", recovered, err)
	}
	active, err = s.ActiveCredentialIncidents(10)
	if err != nil || len(active) != 0 {
		t.Fatalf("active incidents after recovery = %#v, err=%v", active, err)
	}
}

func TestUpdateEventStatusRequiresExistingEvent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.UpdateEventStatus("/missing.mkv", "failed"); err == nil {
		t.Fatal("expected missing event error")
	}
}

func TestSonarrFilenameCacheExpiresAndPurges(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entry := arr.SonarrFilenameCacheEntry{EpisodeFileID: 8, SeriesID: 7, Title: "Show"}
	if err := s.StoreSonarrFilename("https://sonarr.example", "Show S01E03.mkv", entry); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.LoadSonarrFilename("https://sonarr.example", "Show S01E03.mkv")
	if err != nil || !found || got != entry {
		t.Fatalf("entry=%#v found=%t err=%v", got, found, err)
	}
	if _, err := s.db.Exec(`UPDATE sonarr_filename_cache SET expires_at=?`, time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := s.purgeExpiredSonarrFilenameCache(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sonarr_filename_cache`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expired cache rows = %d, want 0", count)
	}
}

func TestUpsertLocalParsedPathCreatesStableTrackerIdentity(t *testing.T) {
	s := openTestStore(t)
	first, found, err := s.UpsertLocalParsedPath("/media/Example.Show.S02E03.mkv")
	if err != nil || !found {
		t.Fatalf("UpsertLocalParsedPath() = %#v, %t, %v", first, found, err)
	}
	if first.Manager != arr.ManagerLocal || first.Kind != "series" || first.Title != "Example Show" || first.SeasonNumber != 2 || !reflect.DeepEqual(first.EpisodeNumbers, []int{3}) {
		t.Fatalf("local identity = %#v", first)
	}
	second, found, err := s.UpsertLocalParsedPath("/media/Example.Show.S01E01.mkv")
	if err != nil || !found || second.SourceID != first.SourceID {
		t.Fatalf("same series identity = %#v, %t, %v; first source ID = %d", second, found, err, first.SourceID)
	}
	units, err := s.RecentMediaUnits(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 3 {
		t.Fatalf("media units = %#v, want series plus two season units", units)
	}
}

func TestUpsertLocalParsedPathRejectsAmbiguousFilename(t *testing.T) {
	s := openTestStore(t)
	identity, found, err := s.UpsertLocalParsedPath("/media/01.mkv")
	if err != nil || found || identity.SourceID != 0 {
		t.Fatalf("UpsertLocalParsedPath() = %#v, %t, %v", identity, found, err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestWatcherHealthAndEventResolutionPersist(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	when := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	if err := s.RecordWatcherHeartbeat(when); err != nil {
		t.Fatal(err)
	}
	heartbeat, found, err := s.LatestWatcherHeartbeat()
	if err != nil || !found || !heartbeat.Equal(when) {
		t.Fatalf("heartbeat = %v, %t, %v", heartbeat, found, err)
	}
	if err := s.RecordIntegrationCheck("sonarr", "success", "connected", when); err != nil {
		t.Fatal(err)
	}
	check, found, err := s.IntegrationCheck("sonarr")
	if err != nil || !found || check.State != "success" || check.Detail != "connected" || !check.CheckedAt.Equal(when) {
		t.Fatalf("check = %#v, %t, %v", check, found, err)
	}
	event := watch.Event{MediaPath: "/media/example.mkv", Progress: .9, WatchedAt: when, Status: "pending"}
	if _, err := s.RecordEvent(event); err != nil {
		t.Fatal(err)
	}
	event.Manager, event.SourceID, event.SeasonNumber, event.EpisodeNumbers = "sonarr", 42, 3, []int{4}
	if err := s.UpdateEventResolution(event); err != nil {
		t.Fatal(err)
	}
	events, err := s.RecentEvents(1)
	if err != nil || len(events) != 1 || events[0].Manager != "sonarr" || events[0].SourceID != 42 || events[0].SeasonNumber != 3 || len(events[0].EpisodeNumbers) != 1 || events[0].EpisodeNumbers[0] != 4 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	job := TrackerSyncJob{EventPath: event.MediaPath, Tracker: "anilist", TrackerID: "123", Status: "review", Detail: "gap", TargetProgress: 4, UpdatedAt: when}
	if err := s.UpsertTrackerSyncJob(job); err != nil {
		t.Fatal(err)
	}
	storedJob, found, err := s.TrackerSyncJob(event.MediaPath, "anilist")
	if err != nil || !found || storedJob.Status != "review" || storedJob.TargetProgress != 4 || !storedJob.UpdatedAt.Equal(when) {
		t.Fatalf("sync job = %#v, %t, %v", storedJob, found, err)
	}
}

func TestRetryableEventsExcludesStableResultsAndKeepsChronologicalOrder(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)
	for _, event := range []watch.Event{
		{MediaPath: "pending.mkv", Progress: .9, WatchedAt: base.Add(2 * time.Minute), Status: "pending"},
		{MediaPath: "stable.mkv", Progress: .9, WatchedAt: base, Status: "unmonitored"},
		{MediaPath: "unmatched.mkv", Progress: .9, WatchedAt: base.Add(time.Minute), Status: "unmatched"},
		{MediaPath: "failed.mkv", Progress: .9, WatchedAt: base.Add(3 * time.Minute), Status: "failed"},
	} {
		if _, err := s.RecordEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.RetryableEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].MediaPath != "unmatched.mkv" || events[1].MediaPath != "pending.mkv" || events[2].MediaPath != "failed.mkv" {
		t.Fatalf("retryable events = %#v", events)
	}
}

func TestCompletedEventsForSeasonKeepsWatchOrderAndSeasonBoundary(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Date(2026, time.July, 26, 0, 0, 0, 0, time.UTC)
	for _, event := range []watch.Event{
		{MediaPath: "season-one-episode-two.mkv", Progress: .9, WatchedAt: base.Add(2 * time.Minute), Status: "unmonitored", Manager: "sonarr", SourceID: 7, SeasonNumber: 1, EpisodeNumbers: []int{2}},
		{MediaPath: "season-two-episode-one.mkv", Progress: .9, WatchedAt: base, Status: "unmonitored", Manager: "sonarr", SourceID: 7, SeasonNumber: 2, EpisodeNumbers: []int{1}},
		{MediaPath: "season-one-episode-one.mkv", Progress: .9, WatchedAt: base.Add(time.Minute), Status: "local", Manager: "sonarr", SourceID: 7, SeasonNumber: 1, EpisodeNumbers: []int{1}},
		{MediaPath: "different-show.mkv", Progress: .9, WatchedAt: base, Status: "unmonitored", Manager: "sonarr", SourceID: 8, SeasonNumber: 1, EpisodeNumbers: []int{1}},
	} {
		if _, err := s.RecordEvent(event); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateEventResolution(event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.CompletedEventsForSeason("sonarr", 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].MediaPath != "season-one-episode-one.mkv" || events[1].MediaPath != "season-one-episode-two.mkv" {
		t.Fatalf("season events = %#v", events)
	}
}

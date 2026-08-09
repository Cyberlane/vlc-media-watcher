package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Cyberlane/vlc-media-watcher/internal/arr"
	"github.com/Cyberlane/vlc-media-watcher/internal/credentials"
	"github.com/Cyberlane/vlc-media-watcher/internal/mediaparse"
	"github.com/Cyberlane/vlc-media-watcher/internal/watch"
)

type Store struct{ db *sql.DB }

const (
	currentSchemaVersion = 3
	databaseBusyTimeout  = 5 * time.Second
)

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	databaseFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create database file: %w", err)
	}
	if err := databaseFile.Close(); err != nil {
		return nil, fmt.Errorf("close database file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secure database file: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Each Store uses one connection so connection-local SQLite safeguards such
	// as foreign_keys and busy_timeout apply consistently. Separate Store
	// instances (the watcher and an on-demand TUI) still coordinate through WAL.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	if err := s.configure(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := secureDatabaseFiles(path); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.checkForeignKeys(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.purgeExpiredSonarrFilenameCache(time.Now().UTC()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) configure() error {
	for _, statement := range []string{
		fmt.Sprintf("PRAGMA busy_timeout = %d", databaseBusyTimeout.Milliseconds()),
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("configure database with %q: %w", statement, err)
		}
	}
	return nil
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read database schema version: %w", err)
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, currentSchemaVersion)
	}
	for version < currentSchemaVersion {
		next := version + 1
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("begin database migration %d: %w", next, err)
		}
		switch next {
		case 1:
			err = migrateInitialSchema(tx)
		case 2:
			err = migrateWatcherLease(tx)
		case 3:
			err = migrateCredentialIncidents(tx)
		default:
			err = fmt.Errorf("unknown database migration %d", next)
		}
		if err == nil {
			_, err = tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, next))
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply database migration %d: %w", next, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit database migration %d: %w", next, err)
		}
		version = next
	}
	return nil
}

func migrateInitialSchema(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS watched_events (
	 id INTEGER PRIMARY KEY, media_path TEXT NOT NULL UNIQUE, progress REAL NOT NULL, watched_at TEXT NOT NULL,
	 status TEXT NOT NULL DEFAULT 'pending', manager TEXT NOT NULL DEFAULT '', source_id INTEGER NOT NULL DEFAULT 0,
	 season_number INTEGER NOT NULL DEFAULT -1, episode_numbers TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS media_units (
 id INTEGER PRIMARY KEY,
 manager TEXT NOT NULL,
 source_id INTEGER NOT NULL,
 scope TEXT NOT NULL,
 season_number INTEGER NOT NULL DEFAULT -1,
 kind TEXT NOT NULL,
 title TEXT NOT NULL,
 year INTEGER NOT NULL DEFAULT 0,
 tvdb_id INTEGER NOT NULL DEFAULT 0,
 tmdb_id INTEGER NOT NULL DEFAULT 0,
 imdb_id TEXT NOT NULL DEFAULT '',
 updated_at TEXT NOT NULL,
 UNIQUE(manager, source_id, scope, season_number)
);
CREATE TABLE IF NOT EXISTS local_media_identities (
 source_id INTEGER PRIMARY KEY,
 identity_key TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS tracker_mappings (
 id INTEGER PRIMARY KEY,
 media_unit_id INTEGER NOT NULL REFERENCES media_units(id) ON DELETE CASCADE,
 tracker TEXT NOT NULL,
 tracker_id TEXT NOT NULL,
 tracker_title TEXT NOT NULL,
 confirmed_at TEXT NOT NULL,
 UNIQUE(media_unit_id, tracker)
);
CREATE TABLE IF NOT EXISTS watcher_heartbeats (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 observed_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS integration_checks (
 service TEXT PRIMARY KEY,
 checked_at TEXT NOT NULL,
 state TEXT NOT NULL,
 detail TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS tracker_sync_jobs (
 event_path TEXT NOT NULL REFERENCES watched_events(media_path) ON DELETE CASCADE,
 tracker TEXT NOT NULL,
 tracker_id TEXT NOT NULL,
 status TEXT NOT NULL,
 detail TEXT NOT NULL DEFAULT '',
 target_progress INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL,
 PRIMARY KEY(event_path, tracker)
);
CREATE TABLE IF NOT EXISTS sonarr_filename_cache (
 endpoint TEXT NOT NULL,
 filename TEXT NOT NULL,
 episode_file_id INTEGER NOT NULL,
 series_id INTEGER NOT NULL,
 title TEXT NOT NULL,
 year INTEGER NOT NULL DEFAULT 0,
 tvdb_id INTEGER NOT NULL DEFAULT 0,
 tmdb_id INTEGER NOT NULL DEFAULT 0,
 imdb_id TEXT NOT NULL DEFAULT '',
	 expires_at TEXT NOT NULL,
	 PRIMARY KEY(endpoint, filename)
);`); err != nil {
		return fmt.Errorf("create initial database schema: %w", err)
	}
	for _, column := range []struct{ name, definition string }{
		{"manager", "TEXT NOT NULL DEFAULT ''"},
		{"source_id", "INTEGER NOT NULL DEFAULT 0"},
		{"season_number", "INTEGER NOT NULL DEFAULT -1"},
		{"episode_numbers", "TEXT NOT NULL DEFAULT '[]'"},
	} {
		if err := ensureWatchedEventColumn(tx, column.name, column.definition); err != nil {
			return err
		}
	}
	return nil
}

func migrateWatcherLease(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS watcher_leases (
	 name TEXT PRIMARY KEY,
	 owner TEXT NOT NULL,
	 expires_at_unix_nano INTEGER NOT NULL
);`)
	return err
}

func migrateCredentialIncidents(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS credential_incidents (
 scope TEXT NOT NULL,
 credential_id TEXT NOT NULL,
 state TEXT NOT NULL,
 detail TEXT NOT NULL,
 active INTEGER NOT NULL,
 first_seen TEXT NOT NULL,
 last_seen TEXT NOT NULL,
 recovered_at TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(scope, credential_id)
);`)
	return err
}

func secureDatabaseFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(candidate, 0o600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("secure database file %q: %w", candidate, err)
		}
	}
	return nil
}

func (s *Store) checkForeignKeys() error {
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check database foreign keys: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowID, foreignKeyID any
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			return fmt.Errorf("read database foreign-key violation: %w", err)
		}
		return fmt.Errorf("database foreign-key violation in table %q referencing %q", table, parent)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("check database foreign keys: %w", err)
	}
	return nil
}

// UpsertLocalParsedPath creates a provisional local identity from a filename.
// It is suitable only for presenting tracker search candidates to the user;
// no tracker mapping or watched-state write is inferred here.
func (s *Store) UpsertLocalParsedPath(path string) (arr.MediaIdentity, bool, error) {
	parsed := mediaparse.Parse(path)
	if !parsed.Trackable() {
		return arr.MediaIdentity{}, false, nil
	}
	key := parsed.IdentityKey()
	if key == "" {
		return arr.MediaIdentity{}, false, nil
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO local_media_identities (identity_key) VALUES (?)`, key); err != nil {
		return arr.MediaIdentity{}, false, fmt.Errorf("create local media identity: %w", err)
	}
	var sourceID int
	if err := s.db.QueryRow(`SELECT source_id FROM local_media_identities WHERE identity_key=?`, key).Scan(&sourceID); err != nil {
		return arr.MediaIdentity{}, false, fmt.Errorf("read local media identity: %w", err)
	}
	identity := arr.MediaIdentity{
		Manager:        arr.ManagerLocal,
		SourceID:       sourceID,
		Kind:           string(parsed.Kind),
		Title:          parsed.Title,
		Year:           parsed.Year,
		SeasonNumber:   parsed.Season,
		EpisodeNumbers: append([]int(nil), parsed.Episodes...),
	}
	if _, err := s.UpsertIdentity(identity); err != nil {
		return arr.MediaIdentity{}, false, err
	}
	return identity, true, nil
}

const sonarrFilenameCacheTTL = 24 * time.Hour

// LoadSonarrFilename returns a still-valid filename entry and deletes it when
// it has expired. Callers must validate the returned file ID with Sonarr
// before using it.
func (s *Store) LoadSonarrFilename(endpoint, filename string) (arr.SonarrFilenameCacheEntry, bool, error) {
	if endpoint == "" || filename == "" {
		return arr.SonarrFilenameCacheEntry{}, false, nil
	}
	var entry arr.SonarrFilenameCacheEntry
	var rawExpiry string
	err := s.db.QueryRow(`SELECT episode_file_id, series_id, title, year, tvdb_id, tmdb_id, imdb_id, expires_at FROM sonarr_filename_cache WHERE endpoint=? AND filename=?`, endpoint, filename).Scan(&entry.EpisodeFileID, &entry.SeriesID, &entry.Title, &entry.Year, &entry.TVDBID, &entry.TMDBID, &entry.IMDbID, &rawExpiry)
	if err == sql.ErrNoRows {
		return arr.SonarrFilenameCacheEntry{}, false, nil
	}
	if err != nil {
		return arr.SonarrFilenameCacheEntry{}, false, fmt.Errorf("read Sonarr filename cache: %w", err)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, rawExpiry)
	if err != nil {
		return arr.SonarrFilenameCacheEntry{}, false, fmt.Errorf("parse Sonarr filename cache expiry: %w", err)
	}
	if !expiresAt.After(time.Now().UTC()) {
		if err := s.DeleteSonarrFilename(endpoint, filename); err != nil {
			return arr.SonarrFilenameCacheEntry{}, false, err
		}
		return arr.SonarrFilenameCacheEntry{}, false, nil
	}
	return entry, true, nil
}

// StoreSonarrFilename writes a verified cache entry with a fixed, short TTL.
// Successful cache use refreshes this TTL, while mismatches are deleted.
func (s *Store) StoreSonarrFilename(endpoint, filename string, entry arr.SonarrFilenameCacheEntry) error {
	if endpoint == "" || filename == "" || entry.EpisodeFileID <= 0 || entry.SeriesID <= 0 || entry.Title == "" {
		return fmt.Errorf("invalid Sonarr filename cache entry")
	}
	now := time.Now().UTC()
	if err := s.purgeExpiredSonarrFilenameCache(now); err != nil {
		return err
	}
	expiresAt := now.Add(sonarrFilenameCacheTTL).Format(time.RFC3339Nano)
	_, err := s.db.Exec(`INSERT INTO sonarr_filename_cache (endpoint, filename, episode_file_id, series_id, title, year, tvdb_id, tmdb_id, imdb_id, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(endpoint, filename) DO UPDATE SET episode_file_id=excluded.episode_file_id, series_id=excluded.series_id, title=excluded.title, year=excluded.year, tvdb_id=excluded.tvdb_id, tmdb_id=excluded.tmdb_id, imdb_id=excluded.imdb_id, expires_at=excluded.expires_at`, endpoint, filename, entry.EpisodeFileID, entry.SeriesID, entry.Title, entry.Year, entry.TVDBID, entry.TMDBID, entry.IMDbID, expiresAt)
	if err != nil {
		return fmt.Errorf("store Sonarr filename cache: %w", err)
	}
	return nil
}

func (s *Store) purgeExpiredSonarrFilenameCache(now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.db.Exec(`DELETE FROM sonarr_filename_cache WHERE expires_at <= ?`, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("purge expired Sonarr filename cache: %w", err)
	}
	return nil
}

// DeleteSonarrFilename invalidates an entry that expired or no longer matches
// Sonarr's current library state.
func (s *Store) DeleteSonarrFilename(endpoint, filename string) error {
	_, err := s.db.Exec(`DELETE FROM sonarr_filename_cache WHERE endpoint=? AND filename=?`, endpoint, filename)
	if err != nil {
		return fmt.Errorf("delete Sonarr filename cache: %w", err)
	}
	return nil
}

func ensureWatchedEventColumn(tx *sql.Tx, name, definition string) error {
	rows, err := tx.Query(`PRAGMA table_info(watched_events)`)
	if err != nil {
		return fmt.Errorf("inspect watched event schema: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var column, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &column, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("read watched event schema: %w", err)
		}
		if column == name {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read watched event schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close watched event schema inspection: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE watched_events ADD COLUMN ` + name + ` ` + definition); err != nil {
		return fmt.Errorf("migrate watched events with %s: %w", name, err)
	}
	return nil
}

// RecordWatcherHeartbeat records a successful VLC status read. It is evidence
// that a watcher process was able to poll VLC; callers must not write it for a
// failed or skipped poll.
func (s *Store) RecordWatcherHeartbeat(when time.Time) error {
	if when.IsZero() {
		when = time.Now().UTC()
	}
	_, err := s.db.Exec(`INSERT INTO watcher_heartbeats (id, observed_at) VALUES (1, ?)
ON CONFLICT(id) DO UPDATE SET observed_at=excluded.observed_at`, when.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("record watcher heartbeat: %w", err)
	}
	return nil
}

// LatestWatcherHeartbeat returns the most recent successful VLC poll.
func (s *Store) LatestWatcherHeartbeat() (time.Time, bool, error) {
	var raw string
	err := s.db.QueryRow(`SELECT observed_at FROM watcher_heartbeats WHERE id=1`).Scan(&raw)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read watcher heartbeat: %w", err)
	}
	when, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse watcher heartbeat: %w", err)
	}
	return when, true, nil
}

// RecordIntegrationCheck preserves the outcome of an explicit manager test
// or successful OAuth handoff. It intentionally stores no credentials.
func (s *Store) RecordIntegrationCheck(service, state, detail string, when time.Time) error {
	if service == "" || state == "" {
		return fmt.Errorf("integration service and state are required")
	}
	if when.IsZero() {
		when = time.Now().UTC()
	}
	_, err := s.db.Exec(`INSERT INTO integration_checks (service, checked_at, state, detail) VALUES (?, ?, ?, ?)
ON CONFLICT(service) DO UPDATE SET checked_at=excluded.checked_at, state=excluded.state, detail=excluded.detail`, service, when.UTC().Format(time.RFC3339Nano), state, detail)
	if err != nil {
		return fmt.Errorf("record integration check: %w", err)
	}
	return nil
}

type IntegrationCheck struct {
	Service   string
	CheckedAt time.Time
	State     string
	Detail    string
}

// CredentialIncident is the persistent, provider-neutral status view used by
// the TUI. It contains only stable IDs and redacted guidance.
type CredentialIncident struct {
	Scope        string
	CredentialID credentials.ID
	State        credentials.State
	Detail       string
	Active       bool
	FirstSeen    time.Time
	LastSeen     time.Time
	RecoveredAt  time.Time
}

// ObserveCredentialIncident records only state transitions. Repeated active
// failures remain de-duplicated across watcher restarts, while one recovery is
// retained when a previously active incident becomes ready.
func (s *Store) ObserveCredentialIncident(incident credentials.Incident) (credentials.IncidentEvent, error) {
	if s == nil || strings.TrimSpace(incident.Scope) == "" || strings.TrimSpace(string(incident.CredentialID)) == "" || strings.TrimSpace(string(incident.State)) == "" || strings.TrimSpace(incident.Detail) == "" {
		return credentials.IncidentEvent{}, fmt.Errorf("invalid credential incident")
	}
	var active bool
	var state string
	err := s.db.QueryRow(`SELECT active, state FROM credential_incidents WHERE scope=? AND credential_id=?`, incident.Scope, string(incident.CredentialID)).Scan(&active, &state)
	found := err == nil
	if err != nil && err != sql.ErrNoRows {
		return credentials.IncidentEvent{}, fmt.Errorf("read credential incident: %w", err)
	}
	now := time.Now().UTC()
	if incident.State == credentials.StateReady {
		if !found || !active {
			return credentials.IncidentEvent{}, nil
		}
		if _, err := s.db.Exec(`UPDATE credential_incidents SET state=?, detail=?, active=0, last_seen=?, recovered_at=? WHERE scope=? AND credential_id=?`, string(incident.State), incident.Detail, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), incident.Scope, string(incident.CredentialID)); err != nil {
			return credentials.IncidentEvent{}, fmt.Errorf("record credential recovery: %w", err)
		}
		return credentials.IncidentEvent{Kind: credentials.IncidentRecovered, Incident: incident}, nil
	}
	if found && active && state == string(incident.State) {
		if _, err := s.db.Exec(`UPDATE credential_incidents SET last_seen=? WHERE scope=? AND credential_id=?`, now.Format(time.RFC3339Nano), incident.Scope, string(incident.CredentialID)); err != nil {
			return credentials.IncidentEvent{}, fmt.Errorf("refresh credential incident: %w", err)
		}
		return credentials.IncidentEvent{}, nil
	}
	if found {
		if _, err := s.db.Exec(`UPDATE credential_incidents SET state=?, detail=?, active=1, last_seen=?, recovered_at='' WHERE scope=? AND credential_id=?`, string(incident.State), incident.Detail, now.Format(time.RFC3339Nano), incident.Scope, string(incident.CredentialID)); err != nil {
			return credentials.IncidentEvent{}, fmt.Errorf("update credential incident: %w", err)
		}
		return credentials.IncidentEvent{Kind: credentials.IncidentUpdated, Incident: incident}, nil
	}
	if _, err := s.db.Exec(`INSERT INTO credential_incidents (scope, credential_id, state, detail, active, first_seen, last_seen, recovered_at) VALUES (?, ?, ?, ?, 1, ?, ?, '')`, incident.Scope, string(incident.CredentialID), string(incident.State), incident.Detail, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return credentials.IncidentEvent{}, fmt.Errorf("open credential incident: %w", err)
	}
	return credentials.IncidentEvent{Kind: credentials.IncidentOpened, Incident: incident}, nil
}

// ActiveCredentialIncidents returns redacted incidents in most-recent-first
// order. The result is intentionally limited so a stale provider cannot flood
// the TUI.
func (s *Store) ActiveCredentialIncidents(limit int) ([]CredentialIncident, error) {
	if s == nil || limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT scope, credential_id, state, detail, active, first_seen, last_seen, recovered_at FROM credential_incidents WHERE active=1 ORDER BY last_seen DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("read active credential incidents: %w", err)
	}
	defer rows.Close()
	incidents := make([]CredentialIncident, 0)
	for rows.Next() {
		var incident CredentialIncident
		var firstSeen, lastSeen, recoveredAt string
		if err := rows.Scan(&incident.Scope, &incident.CredentialID, &incident.State, &incident.Detail, &incident.Active, &firstSeen, &lastSeen, &recoveredAt); err != nil {
			return nil, fmt.Errorf("scan active credential incident: %w", err)
		}
		var err error
		if incident.FirstSeen, err = time.Parse(time.RFC3339Nano, firstSeen); err != nil {
			return nil, fmt.Errorf("parse credential incident first seen: %w", err)
		}
		if incident.LastSeen, err = time.Parse(time.RFC3339Nano, lastSeen); err != nil {
			return nil, fmt.Errorf("parse credential incident last seen: %w", err)
		}
		if recoveredAt != "" {
			if incident.RecoveredAt, err = time.Parse(time.RFC3339Nano, recoveredAt); err != nil {
				return nil, fmt.Errorf("parse credential incident recovery: %w", err)
			}
		}
		incidents = append(incidents, incident)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active credential incidents: %w", err)
	}
	return incidents, nil
}

// TrackerSyncJob records one externally visible tracker-progress outcome for
// a watched local file. It is intentionally separate from a title mapping: a
// confirmed mapping does not authorize a write until the tracker sync setting
// is explicitly enabled.
type TrackerSyncJob struct {
	EventPath      string
	Tracker        string
	TrackerID      string
	Status         string
	Detail         string
	TargetProgress int
	UpdatedAt      time.Time
}

func (s *Store) UpsertTrackerSyncJob(job TrackerSyncJob) error {
	if job.EventPath == "" || job.Tracker == "" || job.TrackerID == "" || job.Status == "" {
		return fmt.Errorf("invalid tracker sync job")
	}
	when := job.UpdatedAt
	if when.IsZero() {
		when = time.Now().UTC()
	}
	_, err := s.db.Exec(`INSERT INTO tracker_sync_jobs (event_path, tracker, tracker_id, status, detail, target_progress, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(event_path, tracker) DO UPDATE SET tracker_id=excluded.tracker_id, status=excluded.status, detail=excluded.detail, target_progress=excluded.target_progress, updated_at=excluded.updated_at`, job.EventPath, job.Tracker, job.TrackerID, job.Status, job.Detail, job.TargetProgress, when.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save tracker sync job: %w", err)
	}
	return nil
}

func (s *Store) TrackerSyncJob(eventPath, tracker string) (TrackerSyncJob, bool, error) {
	var job TrackerSyncJob
	var raw string
	err := s.db.QueryRow(`SELECT event_path, tracker, tracker_id, status, detail, target_progress, updated_at FROM tracker_sync_jobs WHERE event_path=? AND tracker=?`, eventPath, tracker).Scan(&job.EventPath, &job.Tracker, &job.TrackerID, &job.Status, &job.Detail, &job.TargetProgress, &raw)
	if err == sql.ErrNoRows {
		return TrackerSyncJob{}, false, nil
	}
	if err != nil {
		return TrackerSyncJob{}, false, fmt.Errorf("read tracker sync job: %w", err)
	}
	job.UpdatedAt, err = time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return TrackerSyncJob{}, false, fmt.Errorf("parse tracker sync job: %w", err)
	}
	return job, true, nil
}

func (s *Store) IntegrationCheck(service string) (IntegrationCheck, bool, error) {
	var check IntegrationCheck
	var raw string
	err := s.db.QueryRow(`SELECT service, checked_at, state, detail FROM integration_checks WHERE service=?`, service).Scan(&check.Service, &raw, &check.State, &check.Detail)
	if err == sql.ErrNoRows {
		return IntegrationCheck{}, false, nil
	}
	if err != nil {
		return IntegrationCheck{}, false, fmt.Errorf("read integration check: %w", err)
	}
	check.CheckedAt, err = time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return IntegrationCheck{}, false, fmt.Errorf("parse integration check: %w", err)
	}
	return check, true, nil
}

// MediaUnit is the local identity scope used to store a single tracker ID.
// Series have both a show-level unit and, when known, a season-level unit so
// AniList and MyAnimeList never accidentally receive another season's ID.
type MediaUnit struct {
	ID           int64
	Manager      string
	SourceID     int
	Scope        string
	SeasonNumber int
	Kind         string
	Title        string
	Year         int
	TVDBID       int
	TMDBID       int
	IMDbID       string
}

type TrackerMapping struct {
	MediaUnitID  int64
	Tracker      string
	TrackerID    string
	TrackerTitle string
	ConfirmedAt  time.Time
}

// UpsertIdentity records the deterministic library identity. It creates a
// show-level unit plus an independent season-level unit for Sonarr items.
func (s *Store) UpsertIdentity(identity arr.MediaIdentity) ([]MediaUnit, error) {
	if identity.Manager == "" || identity.SourceID <= 0 || identity.Kind == "" || identity.Title == "" {
		return nil, fmt.Errorf("invalid media identity")
	}
	scopes := []struct {
		name   string
		season int
	}{{"media", -1}}
	if identity.Kind == "series" {
		scopes[0].name = "series"
		if identity.SeasonNumber > 0 {
			scopes = append(scopes, struct {
				name   string
				season int
			}{"season", identity.SeasonNumber})
		}
	}
	units := make([]MediaUnit, 0, len(scopes))
	for _, scope := range scopes {
		_, err := s.db.Exec(`INSERT INTO media_units (manager, source_id, scope, season_number, kind, title, year, tvdb_id, tmdb_id, imdb_id, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(manager, source_id, scope, season_number) DO UPDATE SET kind=excluded.kind, title=excluded.title, year=excluded.year, tvdb_id=excluded.tvdb_id, tmdb_id=excluded.tmdb_id, imdb_id=excluded.imdb_id, updated_at=excluded.updated_at`,
			string(identity.Manager), identity.SourceID, scope.name, scope.season, identity.Kind, identity.Title, identity.Year, identity.TVDBID, identity.TMDBID, identity.IMDbID, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return nil, fmt.Errorf("upsert media identity: %w", err)
		}
		unit, err := s.readMediaUnit(string(identity.Manager), identity.SourceID, scope.name, scope.season)
		if err != nil {
			return nil, fmt.Errorf("read media identity: %w", err)
		}
		units = append(units, unit)
	}
	return units, nil
}

func (s *Store) RecentMediaUnits(limit int) ([]MediaUnit, error) {
	rows, err := s.db.Query(`SELECT id, manager, source_id, scope, season_number, kind, title, year, tvdb_id, tmdb_id, imdb_id FROM media_units ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var units []MediaUnit
	for rows.Next() {
		var unit MediaUnit
		if err := rows.Scan(&unit.ID, &unit.Manager, &unit.SourceID, &unit.Scope, &unit.SeasonNumber, &unit.Kind, &unit.Title, &unit.Year, &unit.TVDBID, &unit.TMDBID, &unit.IMDbID); err != nil {
			return nil, err
		}
		units = append(units, unit)
	}
	return units, rows.Err()
}

// MediaUnit finds one deterministic library identity scope. It is used by
// both the TUI and the non-interactive confirmation command, so mappings can
// be repaired without replaying a media file.
func (s *Store) MediaUnit(manager string, sourceID int, scope string, seasonNumber int) (MediaUnit, error) {
	unit, err := s.readMediaUnit(manager, sourceID, scope, seasonNumber)
	if err == sql.ErrNoRows {
		return MediaUnit{}, fmt.Errorf("media unit not found")
	}
	if err != nil {
		return MediaUnit{}, fmt.Errorf("read media unit: %w", err)
	}
	return unit, nil
}

func (s *Store) readMediaUnit(manager string, sourceID int, scope string, seasonNumber int) (MediaUnit, error) {
	var unit MediaUnit
	err := s.db.QueryRow(`SELECT id, manager, source_id, scope, season_number, kind, title, year, tvdb_id, tmdb_id, imdb_id
FROM media_units WHERE manager=? AND source_id=? AND scope=? AND season_number=?`, manager, sourceID, scope, seasonNumber).
		Scan(&unit.ID, &unit.Manager, &unit.SourceID, &unit.Scope, &unit.SeasonNumber, &unit.Kind, &unit.Title, &unit.Year, &unit.TVDBID, &unit.TMDBID, &unit.IMDbID)
	return unit, err
}

// ConfirmMapping is deliberately the only mapping write. Discovery/search may
// suggest candidates, but they cannot be used automatically until this call.
func (s *Store) ConfirmMapping(mapping TrackerMapping) error {
	if mapping.MediaUnitID <= 0 || mapping.Tracker == "" || mapping.TrackerID == "" || mapping.TrackerTitle == "" {
		return fmt.Errorf("invalid tracker mapping")
	}
	when := mapping.ConfirmedAt
	if when.IsZero() {
		when = time.Now().UTC()
	}
	_, err := s.db.Exec(`INSERT INTO tracker_mappings (media_unit_id, tracker, tracker_id, tracker_title, confirmed_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(media_unit_id, tracker) DO UPDATE SET tracker_id=excluded.tracker_id, tracker_title=excluded.tracker_title, confirmed_at=excluded.confirmed_at`, mapping.MediaUnitID, mapping.Tracker, mapping.TrackerID, mapping.TrackerTitle, when.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("confirm tracker mapping: %w", err)
	}
	return nil
}

func (s *Store) MappingsForUnit(mediaUnitID int64) ([]TrackerMapping, error) {
	rows, err := s.db.Query(`SELECT media_unit_id, tracker, tracker_id, tracker_title, confirmed_at FROM tracker_mappings WHERE media_unit_id=? ORDER BY tracker`, mediaUnitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mappings []TrackerMapping
	for rows.Next() {
		var mapping TrackerMapping
		var confirmedAt string
		if err := rows.Scan(&mapping.MediaUnitID, &mapping.Tracker, &mapping.TrackerID, &mapping.TrackerTitle, &confirmedAt); err != nil {
			return nil, err
		}
		var err error
		mapping.ConfirmedAt, err = time.Parse(time.RFC3339Nano, confirmedAt)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, mapping)
	}
	return mappings, rows.Err()
}

// DeleteMapping removes one locally confirmed tracker target. It never makes
// a remote tracker write; the user can confirm a replacement later.
func (s *Store) DeleteMapping(mediaUnitID int64, tracker string) error {
	result, err := s.db.Exec(`DELETE FROM tracker_mappings WHERE media_unit_id=? AND tracker=?`, mediaUnitID, tracker)
	if err != nil {
		return fmt.Errorf("delete tracker mapping: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted mapping count: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("tracker mapping not found")
	}
	return nil
}

// AcquireWatcherLease atomically claims the continuous watcher role for one
// database. Expired leases can be recovered after a crash; an active lease
// prevents two watcher processes from racing the same local and remote writes.
func (s *Store) AcquireWatcherLease(name, owner string, now time.Time, ttl time.Duration) (bool, error) {
	if name == "" || owner == "" || ttl <= 0 {
		return false, fmt.Errorf("watcher lease name, owner, and positive TTL are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	expiresAt := now.Add(ttl).UnixNano()
	result, err := s.db.Exec(`INSERT INTO watcher_leases (name, owner, expires_at_unix_nano) VALUES (?, ?, ?)
ON CONFLICT(name) DO UPDATE SET owner=excluded.owner, expires_at_unix_nano=excluded.expires_at_unix_nano
WHERE watcher_leases.expires_at_unix_nano <= ? OR watcher_leases.owner=excluded.owner`, name, owner, expiresAt, now.UnixNano())
	if err != nil {
		return false, fmt.Errorf("acquire watcher lease: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read watcher lease result: %w", err)
	}
	return changed == 1, nil
}

// RenewWatcherLease extends a lease only while it is still owned by this
// process. A false result means another process recovered the lease.
func (s *Store) RenewWatcherLease(name, owner string, now time.Time, ttl time.Duration) (bool, error) {
	if name == "" || owner == "" || ttl <= 0 {
		return false, fmt.Errorf("watcher lease name, owner, and positive TTL are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := s.db.Exec(`UPDATE watcher_leases SET expires_at_unix_nano=? WHERE name=? AND owner=?`, now.Add(ttl).UnixNano(), name, owner)
	if err != nil {
		return false, fmt.Errorf("renew watcher lease: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read watcher lease renewal result: %w", err)
	}
	return changed == 1, nil
}

func (s *Store) ReleaseWatcherLease(name, owner string) error {
	if name == "" || owner == "" {
		return fmt.Errorf("watcher lease name and owner are required")
	}
	if _, err := s.db.Exec(`DELETE FROM watcher_leases WHERE name=? AND owner=?`, name, owner); err != nil {
		return fmt.Errorf("release watcher lease: %w", err)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) RecordEvent(e watch.Event) (bool, error) {
	result, err := s.db.Exec(`INSERT OR IGNORE INTO watched_events (media_path, progress, watched_at, status) VALUES (?, ?, ?, ?)`, e.MediaPath, e.Progress, e.WatchedAt.Format(time.RFC3339Nano), e.Status)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *Store) UpdateEventStatus(mediaPath, status string) error {
	result, err := s.db.Exec(`UPDATE watched_events SET status = ? WHERE media_path = ?`, status, mediaPath)
	if err != nil {
		return fmt.Errorf("update event status: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated event count: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("event not found for %q", mediaPath)
	}
	return nil
}

// UpdateEventResolution links an already-recorded watch to a deterministic
// Sonarr or Radarr identity after reconciliation succeeds.
func (s *Store) UpdateEventResolution(event watch.Event) error {
	if event.Manager == "" || event.SourceID <= 0 {
		return fmt.Errorf("event resolution requires manager and source ID")
	}
	episodeNumbers, err := json.Marshal(event.EpisodeNumbers)
	if err != nil {
		return fmt.Errorf("encode event episode numbers: %w", err)
	}
	result, err := s.db.Exec(`UPDATE watched_events SET manager=?, source_id=?, season_number=?, episode_numbers=? WHERE media_path=?`, event.Manager, event.SourceID, event.SeasonNumber, string(episodeNumbers), event.MediaPath)
	if err != nil {
		return fmt.Errorf("update event resolution: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read event resolution update count: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("event not found for %q", event.MediaPath)
	}
	return nil
}

func (s *Store) RecentEvents(limit int) ([]watch.Event, error) {
	return s.listEvents(``, nil, limit)
}

// AttentionEvents returns unresolved watches, newest first.
func (s *Store) AttentionEvents(limit int) ([]watch.Event, error) {
	return s.listEvents(`WHERE status IN ('pending', 'unmatched', 'failed')`, nil, limit)
}

// CompletedEvents returns terminal watches, newest first. Internal terminal
// states remain separate for auditing, but callers can present them as one
// successful user-facing outcome.
func (s *Store) CompletedEvents(limit int) ([]watch.Event, error) {
	return s.listEvents(`WHERE status IN ('unmonitored', 'already-unmonitored', 'local')`, nil, limit)
}

// CompletedEventsForSeason returns the completed watches for one resolved
// Sonarr season in watch order. It lets an explicitly confirmed season mapping
// catch up historical watches without guessing across a different season.
func (s *Store) CompletedEventsForSeason(manager string, sourceID, seasonNumber int) ([]watch.Event, error) {
	return s.listEventsOrdered(`WHERE status IN ('unmonitored', 'already-unmonitored', 'local') AND manager=? AND source_id=? AND season_number=?`, []any{manager, sourceID, seasonNumber}, 0, "ASC")
}

// PrunableEvents returns completed events before cutoff. The caller must also
// verify any configured tracker-sync requirements before deleting them.
func (s *Store) PrunableEvents(cutoff time.Time) ([]watch.Event, error) {
	return s.listEvents(`WHERE status IN ('unmonitored', 'already-unmonitored', 'local') AND watched_at < ?`, []any{cutoff.UTC().Format(time.RFC3339Nano)}, 0)
}

func (s *Store) listEvents(where string, arguments []any, limit int) ([]watch.Event, error) {
	return s.listEventsOrdered(where, arguments, limit, "DESC")
}

func (s *Store) listEventsOrdered(where string, arguments []any, limit int, direction string) ([]watch.Event, error) {
	if direction != "ASC" && direction != "DESC" {
		return nil, fmt.Errorf("invalid event order %q", direction)
	}
	query := `SELECT media_path, progress, watched_at, status, manager, source_id, season_number, episode_numbers FROM watched_events ` + where + ` ORDER BY watched_at ` + direction
	if limit > 0 {
		query += ` LIMIT ?`
		arguments = append(arguments, limit)
	}
	rows, err := s.db.Query(query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []watch.Event
	for rows.Next() {
		var e watch.Event
		var watchedAt string
		var episodeNumbers string
		if err := rows.Scan(&e.MediaPath, &e.Progress, &watchedAt, &e.Status, &e.Manager, &e.SourceID, &e.SeasonNumber, &episodeNumbers); err != nil {
			return nil, err
		}
		e.WatchedAt, err = time.Parse(time.RFC3339Nano, watchedAt)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(episodeNumbers), &e.EpisodeNumbers); err != nil {
			return nil, fmt.Errorf("parse event episode numbers: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// EventTrackerStates combines confirmed mappings with the most recent
// per-event sync outcome. It is display data only; mappings remain separate
// durable identity records.
type EventTrackerState struct {
	Tracker string
	Status  string
	Detail  string
}

func (s *Store) EventTrackerStates(event watch.Event) ([]EventTrackerState, error) {
	states := make(map[string]EventTrackerState)
	rows, err := s.db.Query(`SELECT tracker, status, detail FROM tracker_sync_jobs WHERE event_path=?`, event.MediaPath)
	if err != nil {
		return nil, fmt.Errorf("read event tracker sync jobs: %w", err)
	}
	for rows.Next() {
		var state EventTrackerState
		if err := rows.Scan(&state.Tracker, &state.Status, &state.Detail); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read event tracker sync job: %w", err)
		}
		states[state.Tracker] = state
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read event tracker sync jobs: %w", err)
	}
	rows.Close()

	if event.Manager != "" && event.SourceID > 0 {
		mappingRows, err := s.db.Query(`SELECT DISTINCT tm.tracker FROM media_units mu JOIN tracker_mappings tm ON tm.media_unit_id=mu.id
WHERE mu.manager=? AND mu.source_id=? AND ((mu.scope='series') OR (mu.scope='season' AND mu.season_number=?) OR mu.scope='media')`, event.Manager, event.SourceID, event.SeasonNumber)
		if err != nil {
			return nil, fmt.Errorf("read event tracker mappings: %w", err)
		}
		for mappingRows.Next() {
			var trackerName string
			if err := mappingRows.Scan(&trackerName); err != nil {
				mappingRows.Close()
				return nil, fmt.Errorf("read event tracker mapping: %w", err)
			}
			if _, exists := states[trackerName]; !exists {
				states[trackerName] = EventTrackerState{Tracker: trackerName, Status: "mapped"}
			}
		}
		if err := mappingRows.Err(); err != nil {
			mappingRows.Close()
			return nil, fmt.Errorf("read event tracker mappings: %w", err)
		}
		mappingRows.Close()
	}

	result := make([]EventTrackerState, 0, len(states))
	for _, state := range states {
		result = append(result, state)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Tracker < result[j].Tracker })
	return result, nil
}

// DeleteEvents removes selected watch-history rows and their dependent
// per-event tracker-sync jobs. Confirmed tracker mappings are never deleted.
func (s *Store) DeleteEvents(paths []string) (int, error) {
	deleted := 0
	for _, path := range paths {
		result, err := s.db.Exec(`DELETE FROM watched_events WHERE media_path=?`, path)
		if err != nil {
			return deleted, fmt.Errorf("delete watched event %q: %w", path, err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return deleted, fmt.Errorf("read deleted watched event count: %w", err)
		}
		deleted += int(count)
	}
	return deleted, nil
}

// RetryableEvents returns events for which a prior reconciliation did not
// reach a stable result. It is ordered oldest first to preserve watch order.
func (s *Store) RetryableEvents(limit int) ([]watch.Event, error) {
	rows, err := s.db.Query(`SELECT media_path, progress, watched_at, status, manager, source_id, season_number, episode_numbers FROM watched_events
WHERE status IN ('pending', 'unmatched', 'failed') ORDER BY watched_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []watch.Event
	for rows.Next() {
		var event watch.Event
		var watchedAt string
		var episodeNumbers string
		if err := rows.Scan(&event.MediaPath, &event.Progress, &watchedAt, &event.Status, &event.Manager, &event.SourceID, &event.SeasonNumber, &episodeNumbers); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(episodeNumbers), &event.EpisodeNumbers); err != nil {
			return nil, fmt.Errorf("parse event episode numbers: %w", err)
		}
		var err error
		event.WatchedAt, err = time.Parse(time.RFC3339Nano, watchedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

# Changelog

This project follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.2.0] - 2026-08-09

### Added

- Provider-neutral credential requirements, bindings, typed resolution states, and compatibility mapping for VLC, media managers, and trackers.
- Foreground Keychain and 1Password binding, test, repair, and explicit rebind controls that never show or copy a credential value.
- Durable, redacted credential incidents in the dashboard and service logs, with recovery transitions and API authentication classification.

### Changed

- A watcher whose VLC credential needs repair now remains alive in a paused degraded state with bounded retries instead of exiting into the Homebrew restart loop.
- Optional Sonarr, Radarr, and tracker credential failures are isolated from local watching and unrelated integrations.

## [0.1.0] - 2026-08-02

### Added

- Local VLC completion tracking with a native terminal UI.
- Fail-closed Sonarr and Radarr matching with off-by-default unmonitoring.
- Explicit tracker mappings and optional, off-by-default AniList progress sync.
- Owner-only local SQLite storage with WAL, migrations, foreign-key checks, and continuous-watcher leasing.
- Graceful background-service operation, Homebrew packaging, cross-platform archives, checksums, SBOMs, and release attestations.

[Unreleased]: https://github.com/Cyberlane/vlc-media-watcher/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/Cyberlane/vlc-media-watcher/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/Cyberlane/vlc-media-watcher/releases/tag/v0.1.0

# Privacy and local data

VLC Media Watcher is local-first, but its database is personal data.

## What stays on the machine

The default application directory contains:

- `config.toml`: endpoints, thresholds, non-secret credential references, integration switches, and path mappings;
- `watcher.db`: full media paths, playback results, matching evidence, confirmed tracker IDs, and integration outcomes;
- SQLite `-wal` and `-shm` sidecars while the database is open.

The repository, release archives, and Homebrew package do not contain or copy these files. On POSIX systems the application repairs them to owner-only mode (`0600`) whenever it opens them.

Credentials and OAuth access tokens are not stored in those files. They are retrieved from the selected system keyring, 1Password reference, or environment variable. The configuration stores only the reference or variable name.

## Network access

The core watcher contacts the VLC HTTP endpoint in the configuration. Optional features contact only the endpoints a user configures or enables: Sonarr, Radarr, tracker APIs, OAuth authorization pages, and the AniDB title-data source. OAuth callbacks listen on loopback at `127.0.0.1` for the duration of account linking.

The project has no hosted account, telemetry collector, analytics endpoint, or cloud database.

## Logs and diagnostics

Continuous logs show media basenames by default. `watch --verbose`, `events`, TUI detail views, Sonarr/Radarr diagnostics, and database rows can reveal full paths or library titles.

Before opening an issue:

- reproduce with default non-verbose service logging when possible;
- replace usernames, hostnames, titles, paths, IDs, and endpoint addresses with synthetic values;
- never attach `config.toml`, `watcher.db`, keyring exports, OAuth callbacks, or raw environment output;
- never paste API keys, passwords, access tokens, client secrets, or 1Password references.

## Backups and deletion

Stop the service before copying the application directory. Copy the database together with any present `-wal` and `-shm` files, or use a SQLite-aware backup tool. Keep backups private and owner-readable only.

Uninstalling the binary does not delete the application directory. This is intentional: package removal must not erase personal history. If you choose to delete the data, first verify and back up the exact platform-specific directory shown in the README, then remove only that directory.

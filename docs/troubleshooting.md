# Troubleshooting

## VLC cannot be reached

- Confirm VLC is running and its web/HTTP interface is enabled.
- Keep the endpoint on loopback unless remote access is deliberately secured.
- Re-store the VLC password with `vlc-media-watcher secret set vlc`.
- Stop the background service before running `watch --once` repeatedly.

## The keyring is unavailable

Run commands as the same desktop user that owns the credentials. Homebrew services can find helpers installed under the Homebrew prefix; helpers installed elsewhere need an explicit service environment. Linux services also need a usable Secret Service session, which a system-level unit commonly lacks. As a deliberate automation fallback, choose an environment-variable secret source and expose that variable only to the user service.

## A file remains unmatched

Use the TUI's Sonarr/Radarr diagnostics or `events --verbose` locally. Check:

- whether VLC reported a full path or only a basename;
- whether both path-prefix fields are either filled or blank;
- whether the manager knows that exact file;
- whether a basename appears more than once.

Ambiguous basenames are intentionally not resolved. Correct the library/path mapping, then run `vlc-media-watcher events retry`.

## Another watcher is already running

Only one continuous watcher may use a database. Check `brew services info vlc-media-watcher`, stop any manually launched `watch` process, and retry. After a crash, the renewable lease expires in about 30 seconds.

## Service logs repeat or reveal too much

The default service logger suppresses identical warnings for 15 minutes and shows only basenames. If full paths appear, check whether the service command was customized with `--verbose`. Treat old logs as personal data even after changing the setting.

## Database errors

Stop the service before restoring or moving a database. Preserve `watcher.db`, `watcher.db-wal`, and `watcher.db-shm` together when they exist. Do not edit schema versions or lease rows by hand. Keep the original files until the restored copy passes `vlc-media-watcher events`.

When reporting a reproducible database bug, create a minimal synthetic database instead of sharing the personal one.

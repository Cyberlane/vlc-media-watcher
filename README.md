# VLC Media Watcher

VLC Media Watcher is a local, compiled watcher that records completed VLC playback in SQLite. It can optionally identify the exact file in Sonarr or Radarr, unmonitor that file after it is watched, and maintain explicitly confirmed tracker mappings.

It is not a hosted web application. The recommended operating model is:

- run `vlc-media-watcher watch` continuously as a per-user background service;
- open the terminal UI only when you want to configure, review, repair, or confirm something;
- keep the configuration, database, media paths, and credentials on your own machine.

Remote writes are off by default. Local recording happens before any enabled integration is attempted, and ambiguous file matches fail closed.

## Install

### Homebrew (recommended on macOS)

```sh
brew install Cyberlane/tap/vlc-media-watcher
vlc-media-watcher version
```

The formula builds the released source locally and includes a `brew services` definition. Do not use `sudo`.

### Release archive

Download the archive for your platform from [GitHub Releases](https://github.com/Cyberlane/vlc-media-watcher/releases). Each release includes checksums, an SBOM, and GitHub artifact attestations. Direct archive binaries are not Apple-notarized or platform code-signed; Homebrew is the recommended macOS installation.

### Build with Go

Go 1.26 or newer is required:

```sh
go install github.com/Cyberlane/vlc-media-watcher/cmd/vlc-media-watcher@latest
```

This installs the command only. It does not register a background service.

## First run

1. Enable VLC's HTTP interface, bind it to loopback, and set a password.
2. Create a private per-user configuration and store the VLC password in the system keyring:

   ```sh
   vlc-media-watcher setup
   vlc-media-watcher secret set vlc
   ```

3. Start VLC, play something, and validate one poll:

   ```sh
   vlc-media-watcher watch --once
   vlc-media-watcher events
   ```

4. Open the TUI when you want to configure optional integrations:

   ```sh
   vlc-media-watcher
   ```

`watch --once` can run an explicitly enabled integration write, so leave Sonarr/Radarr unmonitoring and AniList progress sync off until their read-only setup checks pass.

## Run it as a service

After the one-poll check succeeds:

```sh
brew services start vlc-media-watcher
brew services info vlc-media-watcher
```

The service runs as your user, starts at login, and does not occupy a terminal or browser tab. Restart it after changing settings:

```sh
brew services restart vlc-media-watcher
```

Stop it before troubleshooting interactively:

```sh
brew services stop vlc-media-watcher
```

Only one continuous watcher may own a database at a time. Service logs use UTC timestamps, redact directory paths by default, suppress repeated identical warnings, repair regular log files to owner-only mode (`0600`), and are written to:

```sh
$(brew --prefix)/var/log/vlc-media-watcher.log
```

See [Background service](docs/service.md) for lifecycle, logging, upgrades, Linux notes, and recovery.

## What is implemented

| Area | v0.1 behavior | External write |
| --- | --- | --- |
| VLC | Local HTTP polling, completion thresholds, deduplicated local history | No |
| Sonarr | Exact path or uniquely verified basename matching; optional unmonitor-after-watch | Off by default |
| Radarr | Exact path or uniquely verified basename matching; optional unmonitor-after-watch | Off by default |
| AniList | Catalog search, OAuth linking, confirmed season mappings, optional progress advancement | Off by default |
| AniDB | Locally cached title reference and confirmed season mappings | No account or write |
| MyAnimeList | Catalog search, PKCE OAuth linking, confirmed season mappings | No progress write |
| Trakt | Catalog search, OAuth authorization-code linking, confirmed series/movie mappings | No progress write |
| SIMKL | PKCE OAuth linking and manual exact-ID confirmation | No catalog search or progress write |

Tracker account linking does not enable progress sync. In v0.1 only AniList has a watched-progress writer, and that setting is independent and off by default.

## Safety and privacy

- The configuration and SQLite database are created outside the repository in the operating system's per-user configuration directory.
- On macOS and Linux, the application creates and repairs the config, database, WAL, and shared-memory files to owner-only mode (`0600`).
- Secrets are resolved from the system keyring, 1Password CLI, or named environment variables. Secret values are never written to TOML or SQLite.
- The database necessarily contains local watch history and full media paths. Treat it as personal data and do not attach it to bug reports.
- Continuous service logs show only media basenames unless `watch --verbose` is explicitly used. Interactive diagnostic commands may show full paths.
- SQLite uses WAL mode, a busy timeout, foreign-key checks, versioned migrations, and a renewable single-watcher lease.
- Sonarr/Radarr changes require one exact, unambiguous library-file match. Zero or multiple matches make no remote change.
- Filename-derived tracker identities are provisional search hints. They never become mappings or drive tracker writes without human confirmation.

Read [Privacy and local data](docs/privacy.md) before sharing diagnostics or moving a database between machines.

## Configuration and data locations

| Platform | Default directory |
| --- | --- |
| macOS | `~/Library/Application Support/vlc-media-watcher/` |
| Linux | `$XDG_CONFIG_HOME/vlc-media-watcher/`, or `~/.config/vlc-media-watcher/` |
| Windows | `%AppData%\vlc-media-watcher\` |

The default directory contains `config.toml` and `watcher.db`. Pass `--config <path>` to use another configuration. The database location can be changed in the TUI.

## Optional integrations

Sonarr and Radarr do not record watched history; this project can optionally change their **Monitored** flag after a completed watch. AniList progress sync is a separate optional action. Setup, path-mapping rules, matching evidence, OAuth details, and tracker confirmation are documented in [Integrations](docs/integrations.md).

## Common commands

```text
vlc-media-watcher                         Open the TUI
vlc-media-watcher version                 Print version and build provenance
vlc-media-watcher setup                   Create the local configuration
vlc-media-watcher secret set <name>       Store a configured secret in the keyring
vlc-media-watcher watch                   Run continuously
vlc-media-watcher watch --once            Poll once and exit
vlc-media-watcher events                  Show attention items and recent watches
vlc-media-watcher events retry            Retry unresolved manager matches
vlc-media-watcher events prune ...        Preview or prune safe historical rows
vlc-media-watcher integrations test ...   Test Sonarr/Radarr without writing
vlc-media-watcher mappings confirm ...    Save a human-reviewed tracker mapping
```

Run `vlc-media-watcher --help` for the command overview. See [Troubleshooting](docs/troubleshooting.md) when the watcher cannot reach VLC, the keyring is unavailable, or a file remains unmatched.

## Upgrade and uninstall

```sh
brew update
brew upgrade vlc-media-watcher
brew services restart vlc-media-watcher
```

To remove the application while keeping your personal history:

```sh
brew services stop vlc-media-watcher
brew uninstall vlc-media-watcher
```

Homebrew does not remove the per-user configuration or database. If you also want to erase that data, first back up and inspect the platform-specific directory above, then remove that exact `vlc-media-watcher` directory yourself.

## Project status

macOS with Homebrew is the primary supported installation for v0.1. Linux and Windows archives are built and tested in CI, but their native background-service setup is not packaged yet. See [Support](SUPPORT.md) for the current platform tiers.

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md), and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

Licensed under the [MIT License](LICENSE).

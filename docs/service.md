# Background service

The continuous watcher is designed to be a per-user service. The TUI is a control and review surface, not the long-running process.

## Homebrew lifecycle

Complete `setup`, store the VLC password, and pass `watch --once` before starting the service.

```sh
brew services start vlc-media-watcher
brew services info vlc-media-watcher
brew services restart vlc-media-watcher
brew services stop vlc-media-watcher
```

Do not use `sudo`. A user service needs access to that user's configuration and credential vault.

Homebrew starts `vlc-media-watcher watch`, keeps it alive, and records both output streams in `$(brew --prefix)/var/log/vlc-media-watcher.log`. The watcher handles interrupt and termination signals, releases its database lease, and exits cleanly.

## Service behavior

- One continuous process can own a given database. A second process exits instead of racing the first.
- A lease is renewed while the process is healthy. An expired lease can be recovered after a crash.
- VLC connection failures do not terminate the service. Identical warnings are summarized instead of being logged every poll.
- Logs use UTC RFC 3339 timestamps and basenames by default. Do not add `--verbose` to a shared or collected service log unless full media paths are acceptable.
- Configuration changes are read on process start. Restart the service after saving settings in the TUI.

## Upgrades

```sh
brew update
brew upgrade vlc-media-watcher
brew services restart vlc-media-watcher
vlc-media-watcher version
```

Database migrations are applied transactionally the next time the upgraded application opens the database. A migration failure stops the process; it does not silently start with a partially migrated schema. Back up the application directory before major upgrades.

## Linux

The Homebrew formula can provide a service on Linuxbrew systems, but keyring access depends on an available Secret Service session. Release archives do not install a systemd unit in v0.1. If you create one, run it as the desktop user, use the normal per-user configuration path, and ensure its session can access the chosen secret source.

## Windows

Windows release archives include the continuous `watch` command and Credential Manager support, but v0.1 does not install a Windows Service or Scheduled Task. Run `watch` in a user session or configure a user-scoped task only after the one-poll check passes.

## Recovery

If `brew services info vlc-media-watcher` reports a failure:

1. Stop the service.
2. Read the last sanitized log lines.
3. Run `vlc-media-watcher watch --once` in a terminal.
4. Correct the VLC, secret, or integration setting.
5. Restart the service and check its status.

If the command says another watcher is active, check `brew services info` and your running processes. Do not delete SQLite lease rows manually; stop the active process or allow the 30-second lease to expire after a crash.

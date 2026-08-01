# Support

## Platform tiers for v0.1

| Tier | Platform | Commitment |
| --- | --- | --- |
| Primary | Current Homebrew-supported macOS on Apple silicon and Intel | Formula install, per-user service, Keychain, TUI, tests, and release artifacts |
| Secondary | Linux amd64/arm64 | Build and automated tests; Secret Service and Linuxbrew service behavior depend on the desktop session |
| Preview | Windows amd64/arm64 | Build and automated tests; Credential Manager is supported, but no Windows Service installer is included |

VLC, Sonarr, Radarr, and tracker providers are independent projects. Their API or authentication changes can require a VLC Media Watcher update.

## Ask for help

Search existing issues, then use the appropriate GitHub issue template. Include:

- `vlc-media-watcher version` output;
- operating system and installation method;
- the expected and actual behavior;
- minimal reproduction steps;
- sanitized, non-verbose log excerpts.

Do not attach configuration, database, keyring, or personal media-path data. Read [Privacy and local data](docs/privacy.md) before sharing diagnostics.

Security reports must follow [SECURITY.md](SECURITY.md) and remain private.

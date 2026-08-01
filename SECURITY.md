# Security policy

## Supported versions

Security fixes are provided for the latest released minor line. During the v0.x period, upgrade to the newest release before reporting a problem unless the vulnerability prevents upgrading.

## Report privately

Do not open a public issue for a suspected vulnerability. Use a [private GitHub security advisory](https://github.com/Cyberlane/vlc-media-watcher/security/advisories/new).

Include the affected version and platform, the security impact, minimal reproduction steps, and a synthetic proof of concept when possible. Remove personal media names and paths. Never send a real configuration, database, password, API key, access token, OAuth callback, client secret, 1Password reference, or environment dump.

You should receive an acknowledgement within seven days. Validation, remediation, and disclosure timing depend on severity and reproducibility. Please allow a coordinated fix and release before public disclosure.

## Scope

Security-sensitive areas include credential handling, OAuth state and loopback callbacks, path matching that could cause a remote write, database privacy and migrations, release provenance, and service privilege boundaries.

The project's fail-closed matching rules are security and safety invariants. A report that demonstrates an ambiguous or unverified file causing a Sonarr, Radarr, or tracker write is especially important.

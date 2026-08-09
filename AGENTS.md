# VLC Media Watcher Agent Instructions

## Operating Baseline

- Be concise, direct, and candid. Separate verified facts, assumptions, and uncertainty.
- Preserve the user's original goal and constraints; keep changes focused and simple.
- Ask questions only when a decision is materially ambiguous, risky, or needs explicit approval.
- Verify observable behavior before claiming completion, and report blockers, outcomes, and evidence.
- Preserve unrelated work. Do not take destructive, service-changing, or external account actions beyond what the user authorized.

## Project Context

VLC Media Watcher is a local Go application that records completed VLC playback in SQLite and can optionally write to Sonarr, Radarr, AniList, or other trackers. Local recording happens before any optional integration write, and ambiguous remote matches must fail closed.

## Safety And Privacy

- Treat the local database, config files, media paths, tracker IDs, and service logs as personal data.
- Never commit or print secrets, OAuth tokens, API keys, tracker credentials, database files, or full private media paths unless the user explicitly needs them.
- Do not start, stop, restart, install, uninstall, or modify background services unless the user explicitly asks.
- Do not enable Sonarr/Radarr unmonitoring or tracker progress writes unless the user explicitly asks for that exact integration behavior.
- Prefer dry-run/read-only diagnostics for remote services. Remote writes require one exact, unambiguous match and explicit user authorization.

## Verification

For code changes, run the focused package tests first, then the broader gate if practical:

```sh
go test ./...
go build ./cmd/vlc-media-watcher
```

For watcher or integration behavior, use read-only or `--once` checks only when the target state is understood. Report when external integrations were not exercised.

## Structural Similarity Gate

- Before implementing new behavior, search existing packages, integrations, and command paths for a contract that already provides it.
- At the start of source-changing work, compare `.mori-version` with `gh api repos/Cyberlane/mori/releases/latest --jq .tag_name` when network access is available. If a newer release exists, update the verified binary, project skill, configuration, and any baseline in a separate reviewed change before continuing.
- Before committing source changes, read `.agents/skills/mori-review-similarity/SKILL.md`, verify that `mori version` matches `.mori-version`, and run `mori scan --changed-since HEAD --format json .`.
- Inspect every focused Mori group in both source locations, with particular attention to credential, tracker, and remote-write behavior. Scores are structural review leads, never proof of equivalent behavior.
- Resolve likely duplication by reusing or extracting an appropriately scoped implementation. If similarity is intentional, record the reason in the work summary.
- Do not create or update a Mori baseline, weaken scope or thresholds, or enable statement-block scans merely to make the gate pass. Treat warnings, insufficient coverage, and truncation as incomplete evidence.

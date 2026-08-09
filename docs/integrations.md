# Integrations

Every integration is optional. The local event is recorded first, and no integration failure removes it.

## Sonarr and Radarr

Sonarr and Radarr writes are disabled independently by default. VLC Media Watcher changes only the **Monitored** flag; it does not record watched history or delete media. Enabling **Unmonitor after watch** tells the manager not to consider that episode or movie for future grabs or upgrades.

For each manager:

1. In its settings, copy the API key without placing it in `config.toml`.
2. In the VLC Media Watcher TUI, enter the complete base URL, including any URL Base, but do not append `/api/v3`.
3. Store the key with `vlc-media-watcher secret set sonarr` or `secret set radarr`.
4. Leave both path-prefix fields blank when VLC and the manager report the same path. Otherwise map the VLC prefix to the manager prefix; both values are required together.
5. Run the read-only check:

   ```sh
   vlc-media-watcher integrations test sonarr
   vlc-media-watcher integrations test radarr
   ```

6. Enable **Use for tracker metadata** for read-only identity lookup, or enable **Unmonitor after watch** only when the write is wanted.

Full normalized paths are preferred. If VLC exposes only a basename, Sonarr is asked to parse it and the returned local file must have exactly the same basename. If that fast path cannot be verified, the manager library is scanned and the basename is accepted only when it identifies one file. Successful Sonarr filename entries are cached for 24 hours and revalidated. Zero or multiple matches fail closed.

Sonarr multi-episode files update every episode attached to the exact file. Use `events retry` to reprocess only pending, unmatched, and failed records with the current safety rules:

```sh
vlc-media-watcher events retry
```

## Tracker linking and mappings

Tracker identities are separate from Sonarr/Radarr identities. A human-confirmed mapping is stored per tracker and per movie, show, or season.

Register this callback URL in each OAuth application:

```text
http://127.0.0.1:8789/callback
```

Add the client ID in the Trackers TUI view and press **Shift+L** to link. AniList and Trakt also need an application client secret; store it with `secret set anilist-client-secret` or `secret set trakt-client-secret`. OAuth access tokens are written directly to the system keyring and are never displayed in the terminal.

| Tracker | Search and confirmation | Account link | Progress write |
| --- | --- | --- | --- |
| AniList | Catalog search; season mapping | Authorization code | Optional and off by default |
| AniDB | Cached title dump; season/AID mapping | None | None |
| MyAnimeList | Catalog search; season mapping | Authorization code with PKCE | None |
| Trakt | Catalog search; show/movie mapping | Authorization code | None |
| SIMKL | Manual exact-ID confirmation | Authorization code with PKCE | None |

Open **Matches**, inspect the manager identity and tracker candidate, open its provider page, and confirm it explicitly. A title, filename, external ID, or mapping from another tracker never auto-confirms a target.

For anime, a Sonarr series has a separate unit for each season. Confirm AniList, AniDB, or MyAnimeList on the season unit so a later season cannot inherit the first season's target.

The CLI can save the same reviewed mapping:

```sh
vlc-media-watcher mappings confirm anilist \
  --manager sonarr --source-id 123 --season 2 \
  --id 456 --title 'Reviewed tracker title'
```

This records a local mapping only; it does not update tracker watch state.

## AniList progress

AniList is currently the only tracker with a progress writer. **Sync watched progress** is separate from linking and mapping, and is off by default. An exact Sonarr episode can advance progress through that episode. The watcher does not lower progress, exceed a verified season length, or complete a season before its verified final episode.

Every attempted sync records a success, failure, or review-needed state. Retry a selected item from Tracking only after reviewing its identity and mapping.

## Secret sources

- **System keyring:** recommended. Uses Keychain on macOS, Credential Manager on Windows, and Secret Service on Linux.
- **1Password:** store the secret in an item password field and put only an `op://` reference in the configuration.
- **Environment:** put only the variable name in the configuration. The service environment must actually contain the value.

Tracker OAuth access tokens always use the system keyring. The alternative sources apply to VLC passwords, manager keys, and supported OAuth client secrets.

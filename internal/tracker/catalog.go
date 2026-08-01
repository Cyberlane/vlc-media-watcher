// Package tracker defines the services VLC Media Watcher can link.  It is a
// catalog only: credentials and confirmed title mappings remain local.
package tracker

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Service is a tracker identifier used in configuration and SQLite.
type Service string

const (
	AniList     Service = "anilist"
	AniDB       Service = "anidb"
	MyAnimeList Service = "myanimelist"
	Trakt       Service = "trakt"
	SIMKL       Service = "simkl"
)

// Definition describes the media a tracker can represent and the identity
// shape a user should expect while confirming a match.
type Definition struct {
	Service      Service
	Name         string
	Media        string
	MappingScope string
	LinkMethod   string
	Notes        string
}

var definitions = []Definition{
	{AniList, "AniList", "Anime", "season", "OAuth 2.0", "Anime seasons are distinct media entries; confirm each season separately."},
	{AniDB, "AniDB", "Anime", "season", "No account required", "AniDB AIDs identify individual anime entries; confirm the correct season entry separately."},
	{MyAnimeList, "MyAnimeList", "Anime", "season", "OAuth 2.0 (PKCE)", "Anime seasons are distinct entries; keep a confirmed ID per season."},
	{Trakt, "Trakt", "TV and movies", "series/movie", "OAuth 2.0 authorization code", "Uses TVDB/TMDB-style identities; one show mapping can cover its episodes."},
	{SIMKL, "SIMKL", "Anime, TV and movies", "series/movie", "OAuth 2.0 authorization code (PKCE)", "One mapping normally covers a TV show or movie; anime can be confirmed by season."},
}

// SupportsUnit reports whether this tracker can be mapped to the selected
// local identity. It prevents season-only services from being confirmed
// against a Sonarr show-level unit.
func (d Definition) SupportsUnit(scope, kind string) bool {
	switch d.MappingScope {
	case "season":
		return scope == "season"
	case "series/movie":
		return (kind == "series" && scope == "series") || (kind == "movie" && scope == "media")
	default:
		return false
	}
}

// URLForID returns the provider's canonical review URL for a verified ID.
func URLForID(service Service, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("a tracker ID is required")
	}
	switch service {
	case AniDB:
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			return "", fmt.Errorf("AniDB AID must be a number")
		}
		return "https://anidb.net/a" + id, nil
	case AniList:
		return "https://anilist.co/anime/" + url.PathEscape(id), nil
	case MyAnimeList:
		return "https://myanimelist.net/anime/" + url.PathEscape(id), nil
	case Trakt:
		return "https://trakt.tv/search?query=" + url.QueryEscape(id), nil
	case SIMKL:
		return "https://simkl.com/search/?q=" + url.QueryEscape(id), nil
	default:
		return "", fmt.Errorf("unsupported tracker %q", service)
	}
}

// All returns the supported tracker catalog in the stable UI order.
func All() []Definition { return append([]Definition(nil), definitions...) }

// Lookup returns a definition by its stable identifier.
func Lookup(service string) (Definition, bool) {
	for _, definition := range definitions {
		if string(definition.Service) == service {
			return definition, true
		}
	}
	return Definition{}, false
}

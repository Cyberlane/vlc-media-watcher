package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Candidate is a small, human-reviewable result. It never creates a mapping
// by itself; the TUI has to receive an explicit confirmation.
type Candidate struct {
	ID       string
	Title    string
	Aliases  []string
	Year     int
	Kind     string
	Episodes int
}

// Search finds title candidates using a tracker's documented public/search
// endpoint. clientID is a public application client identifier, never a user
// access token. SIMKL intentionally falls back to manual ID confirmation
// until its current desktop-search endpoint can be pinned in an adapter.
func Search(ctx context.Context, service Service, title, clientID string) ([]Candidate, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("a title is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	switch service {
	case AniList:
		return searchAniList(ctx, title)
	case AniDB:
		return searchAniDB(ctx, title)
	case MyAnimeList:
		return searchMyAnimeList(ctx, title, clientID)
	case Trakt:
		return searchTrakt(ctx, title, clientID)
	default:
		return nil, fmt.Errorf("SIMKL search is not available in the client yet; confirm its exact ID manually")
	}
}

func searchAniList(ctx context.Context, title string) ([]Candidate, error) {
	payload := map[string]any{"query": "query ($search: String) { Page(page: 1, perPage: 10) { media(search: $search, type: ANIME) { id title { userPreferred romaji english } seasonYear format episodes } } }", "variables": map[string]string{"search": title}}
	body, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://graphql.anilist.co", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	var response struct {
		Data struct {
			Page struct {
				Media []struct {
					ID    int `json:"id"`
					Title struct {
						UserPreferred string `json:"userPreferred"`
						Romaji        string `json:"romaji"`
						English       string `json:"english"`
					} `json:"title"`
					SeasonYear int    `json:"seasonYear"`
					Format     string `json:"format"`
					Episodes   int    `json:"episodes"`
				} `json:"media"`
			} `json:"Page"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := doJSON(request, &response); err != nil {
		return nil, err
	}
	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("AniList search: %s", response.Errors[0].Message)
	}
	candidates := make([]Candidate, 0, len(response.Data.Page.Media))
	for _, item := range response.Data.Page.Media {
		aliases := uniqueNonBlank(item.Title.UserPreferred, item.Title.English, item.Title.Romaji)
		candidates = append(candidates, Candidate{ID: fmt.Sprint(item.ID), Title: firstNonBlank(item.Title.UserPreferred, item.Title.English, item.Title.Romaji), Aliases: aliases, Year: item.SeasonYear, Kind: item.Format, Episodes: item.Episodes})
	}
	return candidates, nil
}

func searchMyAnimeList(ctx context.Context, title, clientID string) ([]Candidate, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, fmt.Errorf("MyAnimeList client ID is required to search")
	}
	endpoint := "https://api.myanimelist.net/v2/anime?q=" + url.QueryEscape(title) + "&limit=10&fields=media_type,start_season"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-MAL-CLIENT-ID", clientID)
	var response struct {
		Data []struct {
			Node struct {
				ID          int    `json:"id"`
				Title       string `json:"title"`
				MediaType   string `json:"media_type"`
				StartSeason struct {
					Year int `json:"year"`
				} `json:"start_season"`
			} `json:"node"`
		} `json:"data"`
	}
	if err := doJSON(request, &response); err != nil {
		return nil, err
	}
	candidates := make([]Candidate, 0, len(response.Data))
	for _, item := range response.Data {
		candidates = append(candidates, Candidate{ID: fmt.Sprint(item.Node.ID), Title: item.Node.Title, Aliases: []string{item.Node.Title}, Year: item.Node.StartSeason.Year, Kind: item.Node.MediaType})
	}
	return candidates, nil
}

func uniqueNonBlank(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func searchTrakt(ctx context.Context, title, clientID string) ([]Candidate, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, fmt.Errorf("Trakt client ID is required to search")
	}
	endpoint := "https://api.trakt.tv/search/movie,show?query=" + url.QueryEscape(title) + "&limit=10"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("trakt-api-version", "2")
	request.Header.Set("trakt-api-key", clientID)
	var response []struct {
		Type  string `json:"type"`
		Movie *struct {
			Title string `json:"title"`
			Year  int    `json:"year"`
			IDs   struct {
				Trakt int `json:"trakt"`
			} `json:"ids"`
		} `json:"movie"`
		Show *struct {
			Title string `json:"title"`
			Year  int    `json:"year"`
			IDs   struct {
				Trakt int `json:"trakt"`
			} `json:"ids"`
		} `json:"show"`
	}
	if err := doJSON(request, &response); err != nil {
		return nil, err
	}
	candidates := make([]Candidate, 0, len(response))
	for _, item := range response {
		if item.Movie != nil {
			candidates = append(candidates, Candidate{ID: fmt.Sprint(item.Movie.IDs.Trakt), Title: item.Movie.Title, Year: item.Movie.Year, Kind: "movie"})
		}
		if item.Show != nil {
			candidates = append(candidates, Candidate{ID: fmt.Sprint(item.Show.IDs.Trakt), Title: item.Show.Title, Year: item.Show.Year, Kind: "show"})
		}
	}
	return candidates, nil
}

func doJSON(request *http.Request, destination any) error {
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("tracker search returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(response.Body).Decode(destination)
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "Untitled"
}

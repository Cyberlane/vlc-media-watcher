package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

var aniListGraphQLEndpoint = "https://graphql.anilist.co"
var aniListHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// ProgressResult is the complete, auditable outcome of one requested AniList
// progress update. Review means that the evidence was insufficient to safely
// assume skipped episodes; it is not an API failure.
type ProgressResult struct {
	Status         string // synced, already_synced, review, failed
	Detail         string
	TargetProgress int
}

// SyncAniListProgress advances an explicitly confirmed season mapping through
// its exact watched Sonarr episode(s). A watched later episode is affirmative
// evidence that earlier episodes were watched too; progress is never reduced
// and a season is only completed at its verified final episode.
func SyncAniListProgress(ctx context.Context, accessToken, trackerID string, watchedEpisodes []int) (ProgressResult, error) {
	return syncAniListProgress(ctx, accessToken, trackerID, watchedEpisodes, nil)
}

func syncAniListProgress(ctx context.Context, accessToken, trackerID string, watchedEpisodes []int, report func(string)) (ProgressResult, error) {
	mediaID, err := strconv.Atoi(strings.TrimSpace(trackerID))
	if err != nil || mediaID <= 0 {
		return ProgressResult{}, fmt.Errorf("AniList mapping ID must be a positive number")
	}
	episodes, err := normalizedEpisodes(watchedEpisodes)
	if err != nil {
		return ProgressResult{Status: "review", Detail: err.Error()}, nil
	}
	if strings.TrimSpace(accessToken) == "" {
		return ProgressResult{}, fmt.Errorf("AniList access token is unavailable; relink the account")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if report != nil {
		report("Reading current AniList progress…")
	}
	state, err := aniListProgressState(ctx, accessToken, mediaID)
	if err != nil {
		return ProgressResult{}, err
	}
	target := episodes[len(episodes)-1]
	if target <= state.Progress {
		return ProgressResult{Status: "already_synced", TargetProgress: state.Progress, Detail: fmt.Sprintf("AniList is already at episode %d or later; no progress was reduced.", state.Progress)}, nil
	}
	if state.Episodes > 0 && target > state.Episodes {
		return ProgressResult{Status: "review", TargetProgress: target, Detail: fmt.Sprintf("Watched episode %d exceeds AniList's verified season length of %d; progress was not changed.", target, state.Episodes)}, nil
	}
	status := "CURRENT"
	if state.Episodes > 0 && target == state.Episodes {
		status = "COMPLETED"
	}
	if report != nil {
		report(fmt.Sprintf("Advancing AniList progress through watched episode %d…", target))
	}
	if err := saveAniListProgress(ctx, accessToken, mediaID, target, status); err != nil {
		return ProgressResult{}, err
	}
	detail := fmt.Sprintf("AniList progress advanced from episode %d through watched episode %d.", state.Progress, target)
	if status == "COMPLETED" {
		detail = "AniList season marked completed after its verified final episode."
	}
	return ProgressResult{Status: "synced", TargetProgress: target, Detail: detail}, nil
}

func normalizedEpisodes(values []int) ([]int, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("no exact episode number was recorded; progress was not changed")
	}
	episodes := append([]int(nil), values...)
	sort.Ints(episodes)
	for index, episode := range episodes {
		if episode <= 0 {
			return nil, fmt.Errorf("invalid episode number; progress was not changed")
		}
		if index > 0 && episode != episodes[index-1]+1 {
			return nil, fmt.Errorf("the file covers non-contiguous episodes; progress was not changed")
		}
	}
	return episodes, nil
}

type aniListState struct {
	Episodes int
	Progress int
}

func aniListProgressState(ctx context.Context, token string, mediaID int) (aniListState, error) {
	const query = `query ($id: Int!) { Media(id: $id, type: ANIME) { episodes mediaListEntry { progress } } }`
	var response struct {
		Data struct {
			Media struct {
				Episodes       int `json:"episodes"`
				MediaListEntry *struct {
					Progress int `json:"progress"`
				} `json:"mediaListEntry"`
			} `json:"Media"`
		} `json:"data"`
		Errors []aniListError `json:"errors"`
	}
	if err := aniListRequest(ctx, token, query, map[string]any{"id": mediaID}, &response); err != nil {
		return aniListState{}, err
	}
	if len(response.Errors) > 0 {
		return aniListState{}, fmt.Errorf("AniList progress lookup: %s", response.Errors[0].Message)
	}
	state := aniListState{Episodes: response.Data.Media.Episodes}
	if response.Data.Media.MediaListEntry != nil {
		state.Progress = response.Data.Media.MediaListEntry.Progress
	}
	return state, nil
}

func saveAniListProgress(ctx context.Context, token string, mediaID, progress int, status string) error {
	const mutation = `mutation ($mediaId: Int!, $progress: Int!, $status: MediaListStatus!) { SaveMediaListEntry(mediaId: $mediaId, progress: $progress, status: $status) { id progress status } }`
	var response struct {
		Errors []aniListError `json:"errors"`
	}
	if err := aniListRequest(ctx, token, mutation, map[string]any{"mediaId": mediaID, "progress": progress, "status": status}, &response); err != nil {
		return err
	}
	if len(response.Errors) > 0 {
		return fmt.Errorf("AniList progress update: %s", response.Errors[0].Message)
	}
	return nil
}

type aniListError struct {
	Message string `json:"message"`
}

type aniListHTTPStatusError struct{ statusCode int }

func (e aniListHTTPStatusError) Error() string {
	return fmt.Sprintf("AniList request returned HTTP %d", e.statusCode)
}
func (e aniListHTTPStatusError) HTTPStatusCode() int { return e.statusCode }

func aniListRequest(ctx context.Context, token, query string, variables map[string]any, destination any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return fmt.Errorf("encode AniList request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, aniListGraphQLEndpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create AniList request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := aniListHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("AniList request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("read AniList response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return aniListHTTPStatusError{statusCode: response.StatusCode}
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode AniList response: %w", err)
	}
	return nil
}

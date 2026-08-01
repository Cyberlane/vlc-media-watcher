package arr

import (
	"context"
	"fmt"
	"net/url"
)

// RadarrClient reconciles local movie files with Radarr movies.
type RadarrClient struct {
	api *apiClient
}

// NewRadarrClient creates a Radarr v3 API client. endpoint is the address used
// to open Radarr and may include a reverse-proxy URL Base, but not /api/v3.
func NewRadarrClient(endpoint, apiKey string) (*RadarrClient, error) {
	api, err := newAPIClient(ManagerRadarr, endpoint, apiKey)
	if err != nil {
		return nil, err
	}
	return &RadarrClient{api: api}, nil
}

// Check verifies the connection and returns Radarr instance/version details.
// A successful result is cached for subsequent Find calls.
func (c *RadarrClient) Check(ctx context.Context) (Instance, error) {
	return c.api.check(ctx)
}

type radarrMovieFile struct {
	ID           int    `json:"id"`
	MovieID      int    `json:"movieId"`
	RelativePath string `json:"relativePath"`
	Path         string `json:"path"`
}

type radarrMovie struct {
	ID        int              `json:"id"`
	Title     string           `json:"title"`
	Year      int              `json:"year"`
	TMDBID    int              `json:"tmdbId"`
	IMDbID    string           `json:"imdbId"`
	Path      string           `json:"path"`
	Monitored bool             `json:"monitored"`
	MovieFile *radarrMovieFile `json:"movieFile"`
}

// Find resolves localMediaPath to exactly one Radarr movie file. No match
// returns found=false and a nil error; duplicate exact paths return
// ErrAmbiguousMatch.
func (c *RadarrClient) Find(ctx context.Context, localMediaPath string, mapping *PathMapping) (Match, bool, error) {
	instance, err := c.Check(ctx)
	if err != nil {
		return Match{}, false, err
	}
	mediaPath, err := remoteMediaPath(localMediaPath, mapping, c.api.localWindows, instance.IsWindows)
	if err != nil {
		return Match{}, false, fmt.Errorf("find Radarr movie: %w", err)
	}
	targetPath := normalizeRemotePath(mediaPath, instance.IsWindows)
	if targetPath == "" {
		return Match{}, false, nil
	}

	var movies []radarrMovie
	query := make(url.Values)
	query.Set("excludeLocalCovers", "true")
	if err := c.api.getJSON(ctx, "/api/v3/movie", query, &movies); err != nil {
		return Match{}, false, fmt.Errorf("find Radarr movie: %w", err)
	}

	filenameOnly := isBareFilename(targetPath)
	var movie radarrMovie
	matchedPath := ""
	count := 0
	for _, item := range movies {
		if item.ID <= 0 || item.MovieFile == nil {
			continue
		}
		filePath := item.MovieFile.Path
		if filePath == "" && item.Path != "" && item.MovieFile.RelativePath != "" {
			filePath = joinPortablePath(item.Path, item.MovieFile.RelativePath, instance.IsWindows)
		}
		matches := normalizeRemotePath(filePath, instance.IsWindows) == targetPath
		if filenameOnly {
			matches = sameBasename(filePath, targetPath, instance.IsWindows)
		}
		if !matches {
			continue
		}
		if item.MovieFile.MovieID != 0 && item.MovieFile.MovieID != item.ID {
			return Match{}, false, fmt.Errorf("find Radarr movie: file %d belongs to an unexpected movie", item.MovieFile.ID)
		}
		movie = item
		matchedPath = filePath
		count++
	}
	if count > 1 {
		return Match{}, false, fmt.Errorf("%w: multiple Radarr movie files have path %q", ErrAmbiguousMatch, targetPath)
	}
	if count == 0 {
		return Match{}, false, nil
	}

	return Match{
		Manager:    ManagerRadarr,
		Title:      movie.Title,
		MediaPath:  matchedPath,
		Resolution: resolutionFor(filenameOnly),
		Targets:    []Target{{ID: movie.ID, Monitored: movie.Monitored}},
		Identity:   MediaIdentity{Manager: ManagerRadarr, SourceID: movie.ID, Kind: "movie", Title: movie.Title, Year: movie.Year, TMDBID: movie.TMDBID, IMDbID: movie.IMDbID},
	}, true, nil
}

// SetMonitored updates the matched movie when its state differs from desired.
// It is a no-op when the movie is already in the desired state.
func (c *RadarrClient) SetMonitored(ctx context.Context, match Match, desired bool) error {
	ids, err := changedTargetIDs(match, ManagerRadarr, desired)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	payload := struct {
		MovieIDs  []int `json:"movieIds"`
		Monitored bool  `json:"monitored"`
	}{MovieIDs: ids, Monitored: desired}
	if err := c.api.putJSON(ctx, "/api/v3/movie/editor", payload); err != nil {
		return fmt.Errorf("set Radarr movie monitored: %w", err)
	}
	return nil
}

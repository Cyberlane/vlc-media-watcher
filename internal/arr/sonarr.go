package arr

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"sync"
)

// maxBareFilenameLookups bounds the library scan used only when VLC exposes
// a filename without its directory. It avoids turning a large Sonarr library
// into a long serial sequence while keeping the unique-filename guard.
const maxBareFilenameLookups = 8

// SonarrClient reconciles local episode files with Sonarr episodes.
type SonarrClient struct {
	api           *apiClient
	filenameCache SonarrFilenameCache
}

// SonarrFilenameCache retains a verified Sonarr episode-file ID for a bare
// VLC filename. Implementations own expiration and purging; entries are
// revalidated against Sonarr before they are used.
type SonarrFilenameCache interface {
	LoadSonarrFilename(endpoint, filename string) (SonarrFilenameCacheEntry, bool, error)
	StoreSonarrFilename(endpoint, filename string, entry SonarrFilenameCacheEntry) error
	DeleteSonarrFilename(endpoint, filename string) error
}

// SonarrFilenameCacheEntry contains stable Sonarr identity facts captured
// after an exact episode-file basename check. It intentionally contains no
// credentials or user-specific tracker data.
type SonarrFilenameCacheEntry struct {
	EpisodeFileID int
	SeriesID      int
	Title         string
	Year          int
	TVDBID        int
	TMDBID        int
	IMDbID        string
}

// NewSonarrClient creates a Sonarr v3 API client. endpoint is the address used
// to open Sonarr and may include a reverse-proxy URL Base, but not /api/v3.
func NewSonarrClient(endpoint, apiKey string) (*SonarrClient, error) {
	api, err := newAPIClient(ManagerSonarr, endpoint, apiKey)
	if err != nil {
		return nil, err
	}
	return &SonarrClient{api: api}, nil
}

// SetSonarrFilenameCache enables an optional persistent cache for validated
// bare filenames. A nil cache disables caching.
func (c *SonarrClient) SetSonarrFilenameCache(cache SonarrFilenameCache) {
	if c != nil {
		c.filenameCache = cache
	}
}

// Check verifies the connection and returns Sonarr instance/version details.
// A successful result is cached for subsequent Find calls.
func (c *SonarrClient) Check(ctx context.Context) (Instance, error) {
	return c.api.check(ctx)
}

type sonarrSeries struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Path   string `json:"path"`
	Year   int    `json:"year"`
	TVDBID int    `json:"tvdbId"`
	TMDBID int    `json:"tmdbId"`
	IMDbID string `json:"imdbId"`
}

type sonarrEpisodeFile struct {
	ID           int    `json:"id"`
	SeriesID     int    `json:"seriesId"`
	RelativePath string `json:"relativePath"`
	Path         string `json:"path"`
}

type sonarrEpisode struct {
	ID            int    `json:"id"`
	SeriesID      int    `json:"seriesId"`
	EpisodeFileID int    `json:"episodeFileId"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"episodeNumber"`
	Title         string `json:"title"`
	Monitored     bool   `json:"monitored"`
}

type sonarrParseResult struct {
	Series   sonarrSeries    `json:"series"`
	Episodes []sonarrEpisode `json:"episodes"`
}

// Find resolves localMediaPath to exactly one Sonarr episode file. A single
// file may yield multiple episode Targets. No match returns found=false and a
// nil error; duplicate exact paths return ErrAmbiguousMatch.
func (c *SonarrClient) Find(ctx context.Context, localMediaPath string, mapping *PathMapping) (Match, bool, error) {
	instance, err := c.Check(ctx)
	if err != nil {
		return Match{}, false, err
	}
	mediaPath, err := remoteMediaPath(localMediaPath, mapping, c.api.localWindows, instance.IsWindows)
	if err != nil {
		return Match{}, false, fmt.Errorf("find Sonarr episode: %w", err)
	}
	targetPath := normalizeRemotePath(mediaPath, instance.IsWindows)
	if targetPath == "" {
		return Match{}, false, nil
	}

	filenameOnly := isBareFilename(targetPath)
	var candidate sonarrSeries
	var file sonarrEpisodeFile
	if filenameOnly {
		var resolved bool
		candidate, file, resolved, err = c.findParsedBareFilename(ctx, targetPath, instance.IsWindows)
		if err != nil {
			return Match{}, false, err
		}
		if !resolved {
			var found bool
			candidate, file, found, err = c.findFromLibrary(ctx, targetPath, true, instance.IsWindows)
			if err != nil {
				return Match{}, false, err
			}
			if !found {
				return Match{}, false, nil
			}
		}
	} else {
		var found bool
		candidate, file, found, err = c.findFromLibrary(ctx, targetPath, false, instance.IsWindows)
		if err != nil {
			return Match{}, false, err
		}
		if !found {
			return Match{}, false, nil
		}
	}

	query := make(url.Values)
	query.Set("episodeFileId", strconv.Itoa(file.ID))
	var episodes []sonarrEpisode
	if err := c.api.getJSON(ctx, "/api/v3/episode", query, &episodes); err != nil {
		return Match{}, false, fmt.Errorf("find Sonarr episodes: %w", err)
	}
	if len(episodes) == 0 {
		return Match{}, false, fmt.Errorf("find Sonarr episodes: episode file %d has no episodes", file.ID)
	}

	targets := make([]Target, 0, len(episodes))
	episodeNumbers := make([]int, 0, len(episodes))
	seasonNumber := 0
	seen := make(map[int]struct{}, len(episodes))
	for _, episode := range episodes {
		if episode.ID <= 0 {
			return Match{}, false, fmt.Errorf("find Sonarr episodes: response contains an invalid episode ID")
		}
		if episode.EpisodeFileID != file.ID {
			return Match{}, false, fmt.Errorf("find Sonarr episodes: episode %d belongs to an unexpected file", episode.ID)
		}
		if episode.SeriesID != candidate.ID {
			return Match{}, false, fmt.Errorf("find Sonarr episodes: episode %d belongs to an unexpected series", episode.ID)
		}
		if _, exists := seen[episode.ID]; exists {
			continue
		}
		seen[episode.ID] = struct{}{}
		targets = append(targets, Target{ID: episode.ID, Monitored: episode.Monitored})
		episodeNumbers = append(episodeNumbers, episode.EpisodeNumber)
		if seasonNumber == 0 {
			seasonNumber = episode.SeasonNumber
		} else if seasonNumber != episode.SeasonNumber {
			seasonNumber = 0
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	sort.Ints(episodeNumbers)

	return Match{
		Manager:    ManagerSonarr,
		Title:      candidate.Title,
		MediaPath:  file.Path,
		Resolution: resolutionFor(filenameOnly),
		Targets:    targets,
		Identity:   MediaIdentity{Manager: ManagerSonarr, SourceID: candidate.ID, Kind: "series", Title: candidate.Title, Year: candidate.Year, SeasonNumber: seasonNumber, EpisodeNumbers: episodeNumbers, TVDBID: candidate.TVDBID, TMDBID: candidate.TMDBID, IMDbID: candidate.IMDbID},
	}, true, nil
}

func (c *SonarrClient) findFromLibrary(ctx context.Context, targetPath string, filenameOnly, windows bool) (sonarrSeries, sonarrEpisodeFile, bool, error) {
	var series []sonarrSeries
	if err := c.api.getJSON(ctx, "/api/v3/series", nil, &series); err != nil {
		return sonarrSeries{}, sonarrEpisodeFile{}, false, fmt.Errorf("find Sonarr series: %w", err)
	}
	candidates := findSeriesCandidates(series, targetPath, windows)
	if filenameOnly {
		// A bare VLC filename has no series-directory prefix to select from.
		// Scan the manager library and accept it only if one file has precisely
		// this basename. This is intentionally a rare fallback; a full path
		// continues to use the much cheaper exact-path path.
		candidates = append([]sonarrSeries(nil), series...)
	}
	if len(candidates) == 0 {
		return sonarrSeries{}, sonarrEpisodeFile{}, false, nil
	}
	candidate, file, fileMatches, err := c.findEpisodeFileMatches(ctx, candidates, targetPath, filenameOnly, windows)
	if err != nil {
		return sonarrSeries{}, sonarrEpisodeFile{}, false, err
	}
	if fileMatches > 1 {
		return sonarrSeries{}, sonarrEpisodeFile{}, false, fmt.Errorf("%w: multiple Sonarr episode files have path %q", ErrAmbiguousMatch, targetPath)
	}
	return candidate, file, fileMatches == 1, nil
}

// findParsedBareFilename asks Sonarr to parse the release name, then accepts
// the answer only after the referenced library file has the exact same
// basename. The parser is an accelerator, not a fallback identity guess.
func (c *SonarrClient) findParsedBareFilename(ctx context.Context, filename string, windows bool) (sonarrSeries, sonarrEpisodeFile, bool, error) {
	if c.filenameCache != nil {
		entry, found, cacheErr := c.filenameCache.LoadSonarrFilename(c.api.baseURL.String(), filename)
		if cacheErr == nil && found {
			candidate := seriesFromCacheEntry(entry)
			file, fileErr := c.episodeFileByID(ctx, entry.EpisodeFileID)
			if fileErr == nil && fileBelongsToSeries(file, candidate, filename, windows) {
				_ = c.filenameCache.StoreSonarrFilename(c.api.baseURL.String(), filename, entry)
				return candidate, file, true, nil
			}
			_ = c.filenameCache.DeleteSonarrFilename(c.api.baseURL.String(), filename)
			if ctx.Err() != nil {
				return sonarrSeries{}, sonarrEpisodeFile{}, false, ctx.Err()
			}
		}
	}

	query := make(url.Values)
	query.Set("title", filename)
	var parsed sonarrParseResult
	if err := c.api.getJSON(ctx, "/api/v3/parse", query, &parsed); err != nil {
		// Parse is a version-dependent optimization. Keep the proven library
		// scan as the correctness fallback when it is unavailable.
		if ctx.Err() != nil {
			return sonarrSeries{}, sonarrEpisodeFile{}, false, ctx.Err()
		}
		return sonarrSeries{}, sonarrEpisodeFile{}, false, nil
	}

	episodeFileID, valid := parsedEpisodeFileID(parsed)
	if !valid {
		return sonarrSeries{}, sonarrEpisodeFile{}, false, nil
	}
	file, err := c.episodeFileByID(ctx, episodeFileID)
	if err != nil {
		if ctx.Err() != nil {
			return sonarrSeries{}, sonarrEpisodeFile{}, false, ctx.Err()
		}
		return sonarrSeries{}, sonarrEpisodeFile{}, false, nil
	}
	if !fileBelongsToSeries(file, parsed.Series, filename, windows) {
		return sonarrSeries{}, sonarrEpisodeFile{}, false, nil
	}
	if c.filenameCache != nil {
		_ = c.filenameCache.StoreSonarrFilename(c.api.baseURL.String(), filename, cacheEntryForSeries(file.ID, parsed.Series))
	}
	return parsed.Series, file, true, nil
}

func (c *SonarrClient) episodeFileByID(ctx context.Context, id int) (sonarrEpisodeFile, error) {
	if id <= 0 {
		return sonarrEpisodeFile{}, fmt.Errorf("invalid Sonarr episode-file ID")
	}
	var file sonarrEpisodeFile
	if err := c.api.getJSON(ctx, "/api/v3/episodefile/"+strconv.Itoa(id), nil, &file); err != nil {
		return sonarrEpisodeFile{}, err
	}
	if file.ID != id {
		return sonarrEpisodeFile{}, fmt.Errorf("find Sonarr episode file: response returned unexpected file ID")
	}
	return file, nil
}

func parsedEpisodeFileID(parsed sonarrParseResult) (int, bool) {
	if parsed.Series.ID <= 0 || len(parsed.Episodes) == 0 {
		return 0, false
	}
	fileID := 0
	for _, episode := range parsed.Episodes {
		if episode.SeriesID != parsed.Series.ID || episode.EpisodeFileID <= 0 {
			return 0, false
		}
		if fileID == 0 {
			fileID = episode.EpisodeFileID
		} else if fileID != episode.EpisodeFileID {
			return 0, false
		}
	}
	return fileID, true
}

func fileBelongsToSeries(file sonarrEpisodeFile, series sonarrSeries, filename string, windows bool) bool {
	return file.ID > 0 && series.ID > 0 && file.SeriesID == series.ID && sameBasename(file.Path, filename, windows)
}

func cacheEntryForSeries(fileID int, series sonarrSeries) SonarrFilenameCacheEntry {
	return SonarrFilenameCacheEntry{EpisodeFileID: fileID, SeriesID: series.ID, Title: series.Title, Year: series.Year, TVDBID: series.TVDBID, TMDBID: series.TMDBID, IMDbID: series.IMDbID}
}

func seriesFromCacheEntry(entry SonarrFilenameCacheEntry) sonarrSeries {
	return sonarrSeries{ID: entry.SeriesID, Title: entry.Title, Year: entry.Year, TVDBID: entry.TVDBID, TMDBID: entry.TMDBID, IMDbID: entry.IMDbID}
}

type sonarrEpisodeFileLookup struct {
	index  int
	series sonarrSeries
	files  []sonarrEpisodeFile
	err    error
}

func (c *SonarrClient) findEpisodeFileMatches(ctx context.Context, candidates []sonarrSeries, targetPath string, filenameOnly, windows bool) (sonarrSeries, sonarrEpisodeFile, int, error) {
	if !filenameOnly {
		return c.findEpisodeFileMatchesSequentially(ctx, candidates, targetPath, false, windows)
	}

	workers := min(maxBareFilenameLookups, len(candidates))
	jobs := make(chan int)
	results := make(chan sonarrEpisodeFileLookup, len(candidates))
	var workerGroup sync.WaitGroup
	for range workers {
		workerGroup.Add(1)
		go func() {
			defer workerGroup.Done()
			for index := range jobs {
				files, err := c.matchEpisodeFiles(ctx, candidates[index], targetPath, true, windows)
				results <- sonarrEpisodeFileLookup{index: index, series: candidates[index], files: files, err: err}
			}
		}()
	}
	go func() {
		for index := range candidates {
			jobs <- index
		}
		close(jobs)
		workerGroup.Wait()
	}()

	var candidate sonarrSeries
	var file sonarrEpisodeFile
	fileMatches := 0
	firstMatchIndex := len(candidates)
	var lookupErr error
	for range candidates {
		result := <-results
		if result.err != nil {
			if lookupErr == nil {
				lookupErr = result.err
			}
			continue
		}
		fileMatches += len(result.files)
		if len(result.files) > 0 && result.index < firstMatchIndex {
			candidate = result.series
			file = result.files[0]
			firstMatchIndex = result.index
		}
	}
	if lookupErr != nil {
		return sonarrSeries{}, sonarrEpisodeFile{}, 0, lookupErr
	}
	return candidate, file, fileMatches, nil
}

func (c *SonarrClient) findEpisodeFileMatchesSequentially(ctx context.Context, candidates []sonarrSeries, targetPath string, filenameOnly, windows bool) (sonarrSeries, sonarrEpisodeFile, int, error) {
	var candidate sonarrSeries
	var file sonarrEpisodeFile
	fileMatches := 0
	for _, possibleSeries := range candidates {
		files, err := c.matchEpisodeFiles(ctx, possibleSeries, targetPath, filenameOnly, windows)
		if err != nil {
			return sonarrSeries{}, sonarrEpisodeFile{}, 0, err
		}
		if len(files) == 0 {
			continue
		}
		candidate = possibleSeries
		file = files[0]
		fileMatches += len(files)
	}
	return candidate, file, fileMatches, nil
}

func (c *SonarrClient) matchEpisodeFiles(ctx context.Context, series sonarrSeries, targetPath string, filenameOnly, windows bool) ([]sonarrEpisodeFile, error) {
	query := make(url.Values)
	query.Set("seriesId", strconv.Itoa(series.ID))
	var files []sonarrEpisodeFile
	if err := c.api.getJSON(ctx, "/api/v3/episodefile", query, &files); err != nil {
		return nil, fmt.Errorf("find Sonarr episode file for series %d: %w", series.ID, err)
	}

	matches := make([]sonarrEpisodeFile, 0, 1)
	for index := range files {
		if files[index].Path == "" && series.Path != "" && files[index].RelativePath != "" {
			files[index].Path = joinPortablePath(series.Path, files[index].RelativePath, windows)
		}
		matched := normalizeRemotePath(files[index].Path, windows) == targetPath
		if filenameOnly {
			matched = sameBasename(files[index].Path, targetPath, windows)
		}
		if files[index].ID <= 0 || !matched {
			continue
		}
		if files[index].SeriesID != series.ID {
			return nil, fmt.Errorf("find Sonarr episode file: file %d belongs to an unexpected series", files[index].ID)
		}
		matches = append(matches, files[index])
	}
	return matches, nil
}

func resolutionFor(filenameOnly bool) string {
	if filenameOnly {
		return "unique_filename"
	}
	return "exact_path"
}

func findSeriesCandidates(series []sonarrSeries, targetPath string, windows bool) []sonarrSeries {
	candidates := make([]sonarrSeries, 0, 1)
	for _, item := range series {
		seriesPath := normalizeRemotePath(item.Path, windows)
		if item.ID <= 0 || !sameOrDescendant(targetPath, seriesPath) {
			continue
		}
		candidates = append(candidates, item)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := normalizeRemotePath(candidates[i].Path, windows)
		right := normalizeRemotePath(candidates[j].Path, windows)
		return len(left) > len(right)
	})
	return candidates
}

// SetMonitored updates every target whose state differs from desired. It is a
// no-op when all targets are already in the desired state.
func (c *SonarrClient) SetMonitored(ctx context.Context, match Match, desired bool) error {
	ids, err := changedTargetIDs(match, ManagerSonarr, desired)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	payload := struct {
		EpisodeIDs []int `json:"episodeIds"`
		Monitored  bool  `json:"monitored"`
	}{EpisodeIDs: ids, Monitored: desired}
	if err := c.api.putJSON(ctx, "/api/v3/episode/monitor", payload); err != nil {
		return fmt.Errorf("set Sonarr episodes monitored: %w", err)
	}
	return nil
}

func changedTargetIDs(match Match, manager Manager, desired bool) ([]int, error) {
	if match.Manager != manager {
		return nil, fmt.Errorf("%s client cannot update a %s match", manager, match.Manager)
	}
	if len(match.Targets) == 0 {
		return nil, fmt.Errorf("%s match has no targets", manager)
	}
	ids := make([]int, 0, len(match.Targets))
	seen := make(map[int]struct{}, len(match.Targets))
	for _, target := range match.Targets {
		if target.ID <= 0 {
			return nil, fmt.Errorf("%s match contains an invalid target ID", manager)
		}
		if target.Monitored == desired {
			continue
		}
		if _, exists := seen[target.ID]; exists {
			continue
		}
		seen[target.ID] = struct{}{}
		ids = append(ids, target.ID)
	}
	sort.Ints(ids)
	return ids, nil
}

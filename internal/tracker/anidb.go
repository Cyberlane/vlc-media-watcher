package tracker

import (
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const aniDBTitleDumpURL = "https://anidb.net/api/anime-titles.xml.gz"

// searchAniDB searches AniDB's official title dump. The dump is cached for a
// full day, matching AniDB's request policy, so opening the Match screen or
// refining a query never repeatedly downloads its catalog.
func searchAniDB(ctx context.Context, query string) ([]Candidate, error) {
	cachePath, err := aniDBCachePath()
	if err != nil {
		return nil, err
	}
	if err := refreshAniDBCache(ctx, cachePath); err != nil {
		return nil, err
	}
	file, err := os.Open(cachePath)
	if err != nil {
		return nil, fmt.Errorf("open AniDB title cache: %w", err)
	}
	defer file.Close()
	return parseAniDBTitles(file, query)
}

func aniDBCachePath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate AniDB cache directory: %w", err)
	}
	return filepath.Join(base, "vlc-media-watcher", "anidb-anime-titles.xml.gz"), nil
}

func refreshAniDBCache(ctx context.Context, cachePath string) error {
	if info, err := os.Stat(cachePath); err == nil && time.Since(info.ModTime()) < 24*time.Hour {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		return fmt.Errorf("create AniDB cache directory: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, aniDBTitleDumpURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "vlc-media-watcher/1.0 title-cache")
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download AniDB title cache: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download AniDB title cache: HTTP %d", response.StatusCode)
	}
	temporary, err := os.CreateTemp(filepath.Dir(cachePath), ".anidb-titles-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, io.LimitReader(response.Body, 256<<20)); err != nil {
		temporary.Close()
		return fmt.Errorf("save AniDB title cache: %w", err)
	}
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, cachePath); err != nil {
		return fmt.Errorf("install AniDB title cache: %w", err)
	}
	return nil
}

type aniDBTitle struct {
	Title string `xml:",chardata"`
	Type  string `xml:"type,attr"`
}

type aniDBAnime struct {
	AID    string       `xml:"aid,attr"`
	Titles []aniDBTitle `xml:"title"`
}

// parseAniDBTitles streams the compressed dump rather than loading it into
// memory. It returns deterministic, title-first candidates only; confirmation
// remains a separate, explicit action in the UI.
func parseAniDBTitles(reader io.Reader, query string) ([]Candidate, error) {
	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return nil, fmt.Errorf("read AniDB title cache: %w", err)
	}
	defer gzipReader.Close()
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, fmt.Errorf("an AniDB search title is required")
	}
	decoder := xml.NewDecoder(gzipReader)
	result := make([]Candidate, 0, 10)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse AniDB title cache: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "anime" {
			continue
		}
		var anime aniDBAnime
		if err := decoder.DecodeElement(&anime, &start); err != nil {
			return nil, fmt.Errorf("parse AniDB anime entry: %w", err)
		}
		candidate, matches := aniDBCandidate(anime, query)
		if matches {
			result = append(result, candidate)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Title) < strings.ToLower(result[j].Title)
	})
	if len(result) > 10 {
		result = result[:10]
	}
	return result, nil
}

func aniDBCandidate(anime aniDBAnime, query string) (Candidate, bool) {
	aliases := make([]string, 0, len(anime.Titles))
	main := ""
	for _, title := range anime.Titles {
		value := strings.TrimSpace(title.Title)
		if value == "" {
			continue
		}
		aliases = append(aliases, value)
		if main == "" || title.Type == "main" {
			main = value
		}
	}
	aliases = uniqueNonBlank(aliases...)
	if main == "" || !containsTitle(aliases, query) {
		return Candidate{}, false
	}
	return Candidate{ID: anime.AID, Title: main, Aliases: aliases, Kind: "AniDB anime"}, true
}

func containsTitle(titles []string, query string) bool {
	for _, title := range titles {
		if strings.Contains(strings.ToLower(title), query) {
			return true
		}
	}
	return false
}

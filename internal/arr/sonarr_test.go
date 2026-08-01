package arr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"
	"time"
)

func TestSonarrFindAndSetMultiEpisode(t *testing.T) {
	t.Parallel()
	const apiKey = "sonarr-secret"
	statusRequests := 0
	episodeFileRequests := 0
	putRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertRequest(t, r, r.Method, r.URL.Path, apiKey)
		switch r.URL.Path {
		case "/sonarr/api/v3/system/status":
			if r.Method != http.MethodGet {
				t.Errorf("status method = %s", r.Method)
			}
			statusRequests++
			writeJSON(w, http.StatusOK, `{"appName":"Sonarr","instanceName":"TV","version":"4.0.15","isLinux":true}`)
		case "/sonarr/api/v3/series":
			if r.Method != http.MethodGet || r.URL.RawQuery != "" {
				t.Errorf("series request = %s %s", r.Method, r.URL.RequestURI())
			}
			writeJSON(w, http.StatusOK, `[
				{"id":1,"title":"TV Root","path":"/tv"},
				{"id":12,"title":"The Show","path":"/tv/The Show"},
				{"id":13,"title":"Not The Show","path":"/tv/The Showcase"}
			]`)
		case "/sonarr/api/v3/episodefile":
			episodeFileRequests++
			seriesID := r.URL.Query().Get("seriesId")
			if r.Method != http.MethodGet || (seriesID != "12" && seriesID != "1") || len(r.URL.Query()) != 1 {
				t.Errorf("episodefile request = %s %s", r.Method, r.URL.RequestURI())
			}
			if seriesID == "1" {
				writeJSON(w, http.StatusOK, `[]`)
				return
			}
			writeJSON(w, http.StatusOK, `[
				{"id":43,"seriesId":12,"relativePath":"Season 01/Other.mkv","path":"/tv/The Show/Season 01/Other.mkv"},
				{"id":44,"seriesId":12,"relativePath":"Season 01/The Show S01E01-E02.mkv","path":"/tv/The Show/Season 01/The Show S01E01-E02.mkv"}
			]`)
		case "/sonarr/api/v3/episode":
			if r.Method != http.MethodGet || r.URL.Query().Get("episodeFileId") != "44" || len(r.URL.Query()) != 1 {
				t.Errorf("episode request = %s %s", r.Method, r.URL.RequestURI())
			}
			writeJSON(w, http.StatusOK, `[
				{"id":102,"seriesId":12,"episodeFileId":44,"seasonNumber":1,"episodeNumber":2,"title":"Second","monitored":true},
				{"id":101,"seriesId":12,"episodeFileId":44,"seasonNumber":1,"episodeNumber":1,"title":"First","monitored":true}
			]`)
		case "/sonarr/api/v3/episode/monitor":
			if r.Method != http.MethodPut {
				t.Errorf("monitor method = %s", r.Method)
			}
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q", got)
			}
			putRequests++
			var payload map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode monitor payload: %v", err)
			}
			if len(payload) != 2 {
				t.Errorf("monitor payload fields = %v", payload)
			}
			var ids []int
			var monitored bool
			_ = json.Unmarshal(payload["episodeIds"], &ids)
			_ = json.Unmarshal(payload["monitored"], &monitored)
			if !reflect.DeepEqual(ids, []int{101, 102}) || monitored {
				t.Errorf("monitor payload IDs=%v monitored=%t", ids, monitored)
			}
			writeJSON(w, http.StatusAccepted, `[]`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewSonarrClient(server.URL+"/sonarr", apiKey)
	if err != nil {
		t.Fatal(err)
	}
	mapping := &PathMapping{LocalPrefix: "/Volumes/Media/TV", RemotePrefix: "/tv"}
	match, found, err := client.Find(context.Background(), "/Volumes/Media/TV/The Show/Season 01/The Show S01E01-E02.mkv", mapping)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("Find did not find the episode file")
	}
	wantTargets := []Target{{ID: 101, Monitored: true}, {ID: 102, Monitored: true}}
	if match.Manager != ManagerSonarr || match.Title != "The Show" || match.MediaPath != "/tv/The Show/Season 01/The Show S01E01-E02.mkv" || !reflect.DeepEqual(match.Targets, wantTargets) {
		t.Fatalf("match = %#v", match)
	}
	if match.AllMonitored(false) || !match.AllMonitored(true) {
		t.Fatalf("unexpected already-state result for %#v", match.Targets)
	}
	if err := client.SetMonitored(context.Background(), match, false); err != nil {
		t.Fatal(err)
	}
	if statusRequests != 1 || episodeFileRequests != 2 || putRequests != 1 {
		t.Fatalf("status requests=%d episode-file requests=%d put requests=%d", statusRequests, episodeFileRequests, putRequests)
	}
}

func TestSonarrSetMonitoredOnlySendsChangedUniqueTargets(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		assertRequest(t, r, http.MethodPut, "/api/v3/episode/monitor", "key")
		var payload struct {
			EpisodeIDs []int `json:"episodeIds"`
			Monitored  bool  `json:"monitored"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if !reflect.DeepEqual(payload.EpisodeIDs, []int{2, 3}) || !payload.Monitored {
			t.Errorf("payload = %#v", payload)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewSonarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	match := Match{Manager: ManagerSonarr, Targets: []Target{
		{ID: 1, Monitored: true},
		{ID: 3, Monitored: false},
		{ID: 2, Monitored: false},
		{ID: 2, Monitored: false},
	}}
	if err := client.SetMonitored(context.Background(), match, true); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestSonarrSetMonitoredSkipsAlreadyDesired(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	client, err := NewSonarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	match := Match{Manager: ManagerSonarr, Targets: []Target{{ID: 1}, {ID: 2}}}
	if !match.AllMonitored(false) {
		t.Fatal("match should already have the desired state")
	}
	if err := client.SetMonitored(context.Background(), match, false); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func TestSonarrFindWindowsPath(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Sonarr","version":"4.0.15","isWindows":true}`)
		case "/api/v3/series":
			writeJSON(w, http.StatusOK, `[{"id":7,"title":"Show","path":"D:\\TV\\Show"}]`)
		case "/api/v3/episodefile":
			writeJSON(w, http.StatusOK, `[{"id":8,"seriesId":7,"path":"d:\\tv\\show\\Season 01\\EPISODE.MKV"}]`)
		case "/api/v3/episode":
			writeJSON(w, http.StatusOK, `[{"id":9,"seriesId":7,"episodeFileId":8,"monitored":false}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewSonarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	client.api.localWindows = true
	mapping := &PathMapping{LocalPrefix: `C:\Media\TV`, RemotePrefix: `D:\TV`}
	match, found, err := client.Find(context.Background(), `c:\media\tv\SHOW\season 01\episode.mkv`, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if !found || len(match.Targets) != 1 || match.Targets[0].ID != 9 {
		t.Fatalf("found=%t match=%#v", found, match)
	}
}

func TestSonarrFindNoExactFileMatch(t *testing.T) {
	t.Parallel()
	episodeRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Sonarr","version":"4.0.15","isLinux":true}`)
		case "/api/v3/series":
			writeJSON(w, http.StatusOK, `[{"id":7,"title":"Show","path":"/tv/Show"}]`)
		case "/api/v3/episodefile":
			writeJSON(w, http.StatusOK, `[{"id":8,"seriesId":7,"path":"/tv/Show/Other/Episode.mkv"}]`)
		case "/api/v3/episode":
			episodeRequests++
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewSonarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	match, found, err := client.Find(context.Background(), "/tv/Show/Season 01/Episode.mkv", nil)
	if err != nil {
		t.Fatal(err)
	}
	if found || !reflect.DeepEqual(match, Match{}) || episodeRequests != 0 {
		t.Fatalf("found=%t match=%#v episode requests=%d", found, match, episodeRequests)
	}
}

func TestSonarrFindUniqueBareFilename(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Sonarr","version":"4.0.15","isLinux":true}`)
		case "/api/v3/series":
			writeJSON(w, http.StatusOK, `[{"id":7,"title":"Show","path":"/tv/Show"}]`)
		case "/api/v3/episodefile":
			writeJSON(w, http.StatusOK, `[{"id":8,"seriesId":7,"path":"/tv/Show/Season 01/Episode.mkv"}]`)
		case "/api/v3/episode":
			writeJSON(w, http.StatusOK, `[{"id":9,"seriesId":7,"episodeFileId":8,"seasonNumber":1,"episodeNumber":3,"monitored":true}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewSonarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	match, found, err := client.Find(context.Background(), "Episode.mkv", nil)
	if err != nil || !found || match.Resolution != "unique_filename" || match.Identity.SeasonNumber != 1 || !reflect.DeepEqual(match.Identity.EpisodeNumbers, []int{3}) {
		t.Fatalf("found=%t match=%#v error=%v", found, match, err)
	}
}

func TestSonarrFindUsesParsedBareFilenameWithoutLibraryScan(t *testing.T) {
	t.Parallel()
	seriesRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Sonarr","version":"4.0.15","isLinux":true}`)
		case "/api/v3/parse":
			if got, want := r.URL.Query().Get("title"), "Show S01E03.mkv"; got != want {
				t.Errorf("parse title = %q, want %q", got, want)
			}
			writeJSON(w, http.StatusOK, `{"series":{"id":7,"title":"Show","path":"/tv/Show"},"episodes":[{"id":9,"seriesId":7,"episodeFileId":8}]}`)
		case "/api/v3/episodefile/8":
			writeJSON(w, http.StatusOK, `{"id":8,"seriesId":7,"path":"/tv/Show/Season 01/Show S01E03.mkv"}`)
		case "/api/v3/episode":
			writeJSON(w, http.StatusOK, `[{"id":9,"seriesId":7,"episodeFileId":8,"seasonNumber":1,"episodeNumber":3,"monitored":true}]`)
		case "/api/v3/series":
			seriesRequests++
			http.Error(w, "library scan should not run", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewSonarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	match, found, err := client.Find(context.Background(), "Show S01E03.mkv", nil)
	if err != nil || !found || match.Title != "Show" || match.Targets[0].ID != 9 {
		t.Fatalf("found=%t match=%#v error=%v", found, match, err)
	}
	if seriesRequests != 0 {
		t.Fatalf("series requests = %d, want 0", seriesRequests)
	}
}

func TestSonarrFindUsesAndRefreshesFilenameCache(t *testing.T) {
	t.Parallel()
	cache := &fakeSonarrFilenameCache{entry: SonarrFilenameCacheEntry{EpisodeFileID: 8, SeriesID: 7, Title: "Show"}, found: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Sonarr","version":"4.0.15","isLinux":true}`)
		case "/api/v3/episodefile/8":
			writeJSON(w, http.StatusOK, `{"id":8,"seriesId":7,"path":"/tv/Show/Season 01/Show S01E03.mkv"}`)
		case "/api/v3/episode":
			writeJSON(w, http.StatusOK, `[{"id":9,"seriesId":7,"episodeFileId":8,"seasonNumber":1,"episodeNumber":3,"monitored":true}]`)
		case "/api/v3/parse", "/api/v3/series":
			http.Error(w, "cache hit should not parse or scan", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewSonarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	client.SetSonarrFilenameCache(cache)
	_, found, err := client.Find(context.Background(), "Show S01E03.mkv", nil)
	if err != nil || !found {
		t.Fatalf("found=%t error=%v", found, err)
	}
	if cache.loads != 1 || cache.stores != 1 || cache.deletes != 0 {
		t.Fatalf("cache calls = loads:%d stores:%d deletes:%d", cache.loads, cache.stores, cache.deletes)
	}
}

func TestSonarrFindInvalidatesStaleFilenameCacheBeforeParsing(t *testing.T) {
	t.Parallel()
	cache := &fakeSonarrFilenameCache{entry: SonarrFilenameCacheEntry{EpisodeFileID: 8, SeriesID: 7, Title: "Show"}, found: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Sonarr","version":"4.0.15","isLinux":true}`)
		case "/api/v3/episodefile/8":
			writeJSON(w, http.StatusOK, `{"id":8,"seriesId":7,"path":"/tv/Show/Season 01/Different.mkv"}`)
		case "/api/v3/parse":
			writeJSON(w, http.StatusOK, `{"series":{"id":7,"title":"Show","path":"/tv/Show"},"episodes":[{"id":9,"seriesId":7,"episodeFileId":10}]}`)
		case "/api/v3/episodefile/10":
			writeJSON(w, http.StatusOK, `{"id":10,"seriesId":7,"path":"/tv/Show/Season 01/Show S01E03.mkv"}`)
		case "/api/v3/episode":
			writeJSON(w, http.StatusOK, `[{"id":9,"seriesId":7,"episodeFileId":10,"seasonNumber":1,"episodeNumber":3,"monitored":true}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewSonarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	client.SetSonarrFilenameCache(cache)
	match, found, err := client.Find(context.Background(), "Show S01E03.mkv", nil)
	if err != nil || !found || match.MediaPath != "/tv/Show/Season 01/Show S01E03.mkv" {
		t.Fatalf("found=%t match=%#v error=%v", found, match, err)
	}
	if cache.deletes != 1 || cache.stores != 1 || cache.entry.EpisodeFileID != 10 {
		t.Fatalf("cache = %#v, deletes=%d stores=%d", cache.entry, cache.deletes, cache.stores)
	}
}

type fakeSonarrFilenameCache struct {
	entry   SonarrFilenameCacheEntry
	found   bool
	loads   int
	stores  int
	deletes int
}

func (c *fakeSonarrFilenameCache) LoadSonarrFilename(_, _ string) (SonarrFilenameCacheEntry, bool, error) {
	c.loads++
	return c.entry, c.found, nil
}

func (c *fakeSonarrFilenameCache) StoreSonarrFilename(_, _ string, entry SonarrFilenameCacheEntry) error {
	c.stores++
	c.entry = entry
	c.found = true
	return nil
}

func (c *fakeSonarrFilenameCache) DeleteSonarrFilename(_, _ string) error {
	c.deletes++
	c.found = false
	return nil
}

func TestSonarrFindRejectsDuplicateBareFilename(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Sonarr","version":"4.0.15","isLinux":true}`)
		case "/api/v3/series":
			writeJSON(w, http.StatusOK, `[{"id":1,"path":"/tv/One"},{"id":2,"path":"/tv/Two"}]`)
		case "/api/v3/episodefile":
			writeJSON(w, http.StatusOK, fmt.Sprintf(`[{"id":%s,"seriesId":%s,"path":"/tv/Show/Episode.mkv"}]`, r.URL.Query().Get("seriesId"), r.URL.Query().Get("seriesId")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewSonarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := client.Find(context.Background(), "Episode.mkv", nil)
	if found || !errors.Is(err, ErrAmbiguousMatch) {
		t.Fatalf("found=%t error=%v", found, err)
	}
}

func TestSonarrFindBoundsBareFilenameLookupConcurrency(t *testing.T) {
	t.Parallel()
	var active atomic.Int32
	var maximum atomic.Int32
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Sonarr","version":"4.0.15","isLinux":true}`)
		case "/api/v3/series":
			series := make([]sonarrSeries, maxBareFilenameLookups*2)
			for index := range series {
				series[index].ID = index + 1
			}
			if err := json.NewEncoder(w).Encode(series); err != nil {
				t.Error(err)
			}
		case "/api/v3/episodefile":
			requests.Add(1)
			current := active.Add(1)
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
			active.Add(-1)
			writeJSON(w, http.StatusOK, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewSonarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := client.Find(context.Background(), "not-present.mkv", nil)
	if err != nil || found {
		t.Fatalf("found=%t error=%v", found, err)
	}
	if got, want := requests.Load(), int32(maxBareFilenameLookups*2); got != want {
		t.Fatalf("episode-file requests = %d, want %d", got, want)
	}
	if got := maximum.Load(); got < 2 || got > maxBareFilenameLookups {
		t.Fatalf("maximum concurrent lookups = %d, want 2..%d", got, maxBareFilenameLookups)
	}
}

func TestSonarrFindFallsBackToSeriesAndRelativePath(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Sonarr","version":"4.0.15","isLinux":true}`)
		case "/api/v3/series":
			writeJSON(w, http.StatusOK, `[{"id":7,"title":"Show","path":"/tv/Show"}]`)
		case "/api/v3/episodefile":
			writeJSON(w, http.StatusOK, `[{"id":8,"seriesId":7,"relativePath":"Season 01/Episode.mkv"}]`)
		case "/api/v3/episode":
			writeJSON(w, http.StatusOK, `[{"id":9,"seriesId":7,"episodeFileId":8,"monitored":true}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewSonarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	match, found, err := client.Find(context.Background(), "/tv/Show/Season 01/Episode.mkv", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !found || match.MediaPath != "/tv/Show/Season 01/Episode.mkv" || match.Targets[0].ID != 9 {
		t.Fatalf("found=%t match=%#v", found, match)
	}
}

func TestSonarrFindRejectsAmbiguousMatches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		series string
		files  string
	}{
		{
			name:   "series",
			series: `[{"id":1,"path":"/tv/Show"},{"id":2,"path":"/tv/Show"}]`,
			files:  `[{"id":3,"path":"/tv/Show/Episode.mkv"}]`,
		},
		{
			name:   "episode files",
			series: `[{"id":1,"path":"/tv/Show"}]`,
			files:  `[{"id":2,"seriesId":1,"path":"/tv/Show/Episode.mkv"},{"id":3,"seriesId":1,"path":"/tv/Show/Episode.mkv"}]`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v3/system/status":
					writeJSON(w, http.StatusOK, `{"appName":"Sonarr","version":"4.0.15","isLinux":true}`)
				case "/api/v3/series":
					writeJSON(w, http.StatusOK, test.series)
				case "/api/v3/episodefile":
					files := test.files
					if test.name == "series" {
						files = fmt.Sprintf(`[{"id":3,"seriesId":%s,"path":"/tv/Show/Episode.mkv"}]`, r.URL.Query().Get("seriesId"))
					}
					writeJSON(w, http.StatusOK, files)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := NewSonarrClient(server.URL, "key")
			if err != nil {
				t.Fatal(err)
			}
			_, found, err := client.Find(context.Background(), "/tv/Show/Episode.mkv", nil)
			if found || !errors.Is(err, ErrAmbiguousMatch) {
				t.Fatalf("found=%t error=%v", found, err)
			}
		})
	}
}

func TestSonarrFindRequiresEpisodesForMatchedFile(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Sonarr","version":"4.0.15"}`)
		case "/api/v3/series":
			writeJSON(w, http.StatusOK, `[{"id":1,"path":"/tv/Show"}]`)
		case "/api/v3/episodefile":
			writeJSON(w, http.StatusOK, `[{"id":2,"seriesId":1,"path":"/tv/Show/Episode.mkv"}]`)
		case "/api/v3/episode":
			writeJSON(w, http.StatusOK, `[]`)
		}
	}))
	defer server.Close()
	client, err := NewSonarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := client.Find(context.Background(), "/tv/Show/Episode.mkv", nil)
	if found || err == nil {
		t.Fatalf("found=%t error=%v", found, err)
	}
}

func TestSonarrFindRejectsMismatchedRelationships(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		file     string
		episodes string
	}{
		{
			name: "file belongs to another series",
			file: `[{"id":2,"seriesId":99,"path":"/tv/Show/Episode.mkv"}]`,
		},
		{
			name:     "episode belongs to another series",
			file:     `[{"id":2,"seriesId":1,"path":"/tv/Show/Episode.mkv"}]`,
			episodes: `[{"id":3,"seriesId":99,"episodeFileId":2,"monitored":true}]`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v3/system/status":
					writeJSON(w, http.StatusOK, `{"appName":"Sonarr","version":"4.0.15","isLinux":true}`)
				case "/api/v3/series":
					writeJSON(w, http.StatusOK, `[{"id":1,"path":"/tv/Show"}]`)
				case "/api/v3/episodefile":
					writeJSON(w, http.StatusOK, test.file)
				case "/api/v3/episode":
					writeJSON(w, http.StatusOK, test.episodes)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := NewSonarrClient(server.URL, "key")
			if err != nil {
				t.Fatal(err)
			}
			_, found, err := client.Find(context.Background(), "/tv/Show/Episode.mkv", nil)
			if found || err == nil {
				t.Fatalf("found=%t error=%v", found, err)
			}
		})
	}
}

func TestSetMonitoredValidatesMatch(t *testing.T) {
	t.Parallel()
	client, err := NewSonarrClient("http://localhost:8989", "key")
	if err != nil {
		t.Fatal(err)
	}
	tests := []Match{
		{},
		{Manager: ManagerRadarr, Targets: []Target{{ID: 1, Monitored: true}}},
		{Manager: ManagerSonarr},
		{Manager: ManagerSonarr, Targets: []Target{{ID: 0, Monitored: true}}},
	}
	for _, match := range tests {
		if err := client.SetMonitored(context.Background(), match, false); err == nil {
			t.Fatalf("SetMonitored(%#v) returned nil", match)
		}
	}
}

func TestChangedTargetIDsSorts(t *testing.T) {
	t.Parallel()
	match := Match{Manager: ManagerSonarr, Targets: []Target{{ID: 9}, {ID: 2}, {ID: 5}}}
	ids, err := changedTargetIDs(match, ManagerSonarr, true)
	if err != nil {
		t.Fatal(err)
	}
	if !sort.IntsAreSorted(ids) {
		t.Fatalf("IDs not sorted: %v", ids)
	}
}

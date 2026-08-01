package arr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRadarrFindAndSetMonitored(t *testing.T) {
	t.Parallel()
	const apiKey = "radarr-secret"
	statusRequests := 0
	putRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertRequest(t, r, r.Method, r.URL.Path, apiKey)
		switch r.URL.Path {
		case "/radarr/api/v3/system/status":
			statusRequests++
			writeJSON(w, http.StatusOK, `{"appName":"Radarr","instanceName":"Movies","version":"6.1.1","isLinux":true}`)
		case "/radarr/api/v3/movie":
			if r.Method != http.MethodGet || r.URL.Query().Get("excludeLocalCovers") != "true" || len(r.URL.Query()) != 1 {
				t.Errorf("movie request = %s %s", r.Method, r.URL.RequestURI())
			}
			writeJSON(w, http.StatusOK, `[
				{"id":20,"title":"Other","path":"/movies/Other (2024)","monitored":true,"movieFile":{"id":200,"movieId":20,"relativePath":"Other.mkv","path":"/movies/Other (2024)/Other.mkv"}},
				{"id":21,"title":"Film","path":"/movies/Film (2025)","monitored":true,"movieFile":{"id":201,"movieId":21,"relativePath":"Film.mkv","path":"/movies/Film (2025)/Film.mkv"}},
				{"id":22,"title":"Missing","path":"/movies/Missing (2025)","monitored":true,"movieFile":null}
			]`)
		case "/radarr/api/v3/movie/editor":
			if r.Method != http.MethodPut {
				t.Errorf("editor method = %s", r.Method)
			}
			putRequests++
			var payload map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode editor payload: %v", err)
			}
			if len(payload) != 2 {
				t.Errorf("editor payload fields = %v", payload)
			}
			var ids []int
			var monitored bool
			_ = json.Unmarshal(payload["movieIds"], &ids)
			_ = json.Unmarshal(payload["monitored"], &monitored)
			if !reflect.DeepEqual(ids, []int{21}) || monitored {
				t.Errorf("editor payload IDs=%v monitored=%t", ids, monitored)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewRadarrClient(server.URL+"/radarr/", apiKey)
	if err != nil {
		t.Fatal(err)
	}
	mapping := &PathMapping{LocalPrefix: "/Volumes/Movies", RemotePrefix: "/movies"}
	match, found, err := client.Find(context.Background(), "/Volumes/Movies/Film (2025)/Film.mkv", mapping)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("Find did not find the movie")
	}
	want := Match{
		Manager:    ManagerRadarr,
		Title:      "Film",
		MediaPath:  "/movies/Film (2025)/Film.mkv",
		Resolution: "exact_path",
		Targets:    []Target{{ID: 21, Monitored: true}},
		Identity:   MediaIdentity{Manager: ManagerRadarr, SourceID: 21, Kind: "movie", Title: "Film"},
	}
	if !reflect.DeepEqual(match, want) {
		t.Fatalf("match = %#v, want %#v", match, want)
	}
	if err := client.SetMonitored(context.Background(), match, false); err != nil {
		t.Fatal(err)
	}
	if statusRequests != 1 || putRequests != 1 {
		t.Fatalf("status requests=%d put requests=%d", statusRequests, putRequests)
	}
}

func TestRadarrFindFallsBackToMovieAndRelativePath(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Radarr","version":"6.1.1","isLinux":true}`)
		case "/api/v3/movie":
			writeJSON(w, http.StatusOK, `[{"id":21,"title":"Film","path":"/movies/Film","monitored":false,"movieFile":{"id":201,"movieId":21,"relativePath":"Film.mkv"}}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewRadarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	match, found, err := client.Find(context.Background(), "/movies/Film/Film.mkv", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !found || match.MediaPath != "/movies/Film/Film.mkv" || !match.AllMonitored(false) {
		t.Fatalf("found=%t match=%#v", found, match)
	}
}

func TestRadarrFindWindowsPath(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Radarr","version":"6.1.1","isWindows":true}`)
		case "/api/v3/movie":
			writeJSON(w, http.StatusOK, `[{"id":21,"title":"Film","monitored":true,"movieFile":{"id":201,"movieId":21,"path":"D:\\MOVIES\\Film\\FILM.MKV"}}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewRadarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	client.api.localWindows = true
	mapping := &PathMapping{LocalPrefix: `C:\Media\Movies`, RemotePrefix: `D:\Movies`}
	match, found, err := client.Find(context.Background(), `/c:/media/movies/film/film.mkv`, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if !found || match.Targets[0].ID != 21 {
		t.Fatalf("found=%t match=%#v", found, match)
	}
}

func TestRadarrFindNoAndAmbiguousMatches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		movies    string
		wantError bool
		localPath string
	}{
		{
			name:      "different full path is not a basename fallback",
			localPath: "/movies/Film/Film.mkv",
			movies:    `[{"id":1,"movieFile":{"path":"/other/Film.mkv"}}]`,
		},
		{
			name:      "ambiguous exact path",
			localPath: "/movies/Film/Film.mkv",
			movies:    `[{"id":1,"movieFile":{"path":"/movies/Film/Film.mkv"}},{"id":2,"movieFile":{"path":"/movies/Film/Film.mkv"}}]`,
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v3/system/status":
					writeJSON(w, http.StatusOK, `{"appName":"Radarr","version":"6.1.1","isLinux":true}`)
				case "/api/v3/movie":
					writeJSON(w, http.StatusOK, test.movies)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := NewRadarrClient(server.URL, "key")
			if err != nil {
				t.Fatal(err)
			}
			match, found, err := client.Find(context.Background(), test.localPath, nil)
			if test.wantError {
				if found || !errors.Is(err, ErrAmbiguousMatch) {
					t.Fatalf("found=%t error=%v", found, err)
				}
				return
			}
			if err != nil || found || !reflect.DeepEqual(match, Match{}) {
				t.Fatalf("found=%t match=%#v error=%v", found, match, err)
			}
		})
	}
}

func TestRadarrFindUniqueBareFilename(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Radarr","version":"6.1.1","isLinux":true}`)
		case "/api/v3/movie":
			writeJSON(w, http.StatusOK, `[{"id":21,"title":"Film","monitored":true,"movieFile":{"id":201,"movieId":21,"path":"/movies/Film/Film.mkv"}}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewRadarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	match, found, err := client.Find(context.Background(), "Film.mkv", nil)
	if err != nil || !found || match.Resolution != "unique_filename" || match.MediaPath != "/movies/Film/Film.mkv" {
		t.Fatalf("found=%t match=%#v error=%v", found, match, err)
	}
}

func TestRadarrFindRejectsDuplicateBareFilename(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Radarr","version":"6.1.1","isLinux":true}`)
		case "/api/v3/movie":
			writeJSON(w, http.StatusOK, `[{"id":1,"movieFile":{"id":2,"movieId":1,"path":"/one/Film.mkv"}},{"id":3,"movieFile":{"id":4,"movieId":3,"path":"/two/Film.mkv"}}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewRadarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := client.Find(context.Background(), "Film.mkv", nil)
	if found || !errors.Is(err, ErrAmbiguousMatch) {
		t.Fatalf("found=%t error=%v", found, err)
	}
}

func TestRadarrSetMonitoredSkipsAlreadyDesired(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	client, err := NewRadarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	match := Match{Manager: ManagerRadarr, Targets: []Target{{ID: 5, Monitored: false}}}
	if err := client.SetMonitored(context.Background(), match, false); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func TestRadarrSetMonitoredRejectsSonarrMatch(t *testing.T) {
	t.Parallel()
	client, err := NewRadarrClient("http://localhost:7878", "key")
	if err != nil {
		t.Fatal(err)
	}
	match := Match{Manager: ManagerSonarr, Targets: []Target{{ID: 5, Monitored: true}}}
	if err := client.SetMonitored(context.Background(), match, false); err == nil {
		t.Fatal("SetMonitored returned a nil error")
	}
}

func TestRadarrFindRejectsMismatchedMovieFile(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeJSON(w, http.StatusOK, `{"appName":"Radarr","version":"6.1.1","isLinux":true}`)
		case "/api/v3/movie":
			writeJSON(w, http.StatusOK, `[{"id":1,"movieFile":{"id":2,"movieId":99,"path":"/movies/Film.mkv"}}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewRadarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := client.Find(context.Background(), "/movies/Film.mkv", nil)
	if found || err == nil {
		t.Fatalf("found=%t error=%v", found, err)
	}
}

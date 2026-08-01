package tracker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSyncAniListProgressAdvancesThroughTheExactWatchedEpisode(t *testing.T) {
	var mutation map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("authorization = %q", got)
		}
		var request struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(request.Query, "SaveMediaListEntry") {
			mutation = request.Variables
			_, _ = w.Write([]byte(`{"data":{"SaveMediaListEntry":{"id":1,"progress":4,"status":"CURRENT"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"Media":{"episodes":12,"mediaListEntry":{"progress":3}}}}`))
	}))
	defer server.Close()
	previous := aniListGraphQLEndpoint
	aniListGraphQLEndpoint = server.URL
	defer func() { aniListGraphQLEndpoint = previous }()

	result, err := SyncAniListProgress(context.Background(), "token", "197715", []int{4})
	if err != nil || result.Status != "synced" || result.TargetProgress != 4 {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if mutation["progress"] != float64(4) || mutation["status"] != "CURRENT" {
		t.Fatalf("mutation = %#v", mutation)
	}
}

func TestSyncAniListProgressCatchesUpThroughALaterWatchedEpisode(t *testing.T) {
	var mutation map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(request.Query, "SaveMediaListEntry") {
			mutation = request.Variables
			_, _ = w.Write([]byte(`{"data":{"SaveMediaListEntry":{"id":1,"progress":3,"status":"CURRENT"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"Media":{"episodes":12,"mediaListEntry":{"progress":1}}}}`))
	}))
	defer server.Close()
	previous := aniListGraphQLEndpoint
	aniListGraphQLEndpoint = server.URL
	defer func() { aniListGraphQLEndpoint = previous }()

	result, err := SyncAniListProgress(context.Background(), "token", "197715", []int{3})
	if err != nil || result.Status != "synced" || result.TargetProgress != 3 {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if mutation["progress"] != float64(3) {
		t.Fatalf("mutation = %#v", mutation)
	}
}

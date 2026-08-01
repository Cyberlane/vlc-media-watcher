package vlc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusReadsFileURI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/requests/status.json" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "" || password != "secret" {
			t.Fatal("missing basic auth")
		}
		_, _ = w.Write([]byte(`{"state":"playing","position":0.91,"length":1440,"information":{"category":{"meta":{"uri":"file:///Volumes/iPad_Media/Show%20Name/S01E01.mkv"}}}}`))
	}))
	defer server.Close()
	status, err := NewClient(server.URL, "secret").Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.MediaPath != "/Volumes/iPad_Media/Show Name/S01E01.mkv" {
		t.Fatalf("path = %q", status.MediaPath)
	}
	if status.Position != .91 || status.LengthSeconds != 1440 {
		t.Fatalf("status = %#v", status)
	}
}

func TestStatusFallsBackToFilename(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"state":"playing","position":0.91,"length":1440,"information":{"category":{"meta":{"filename":"Show.S01E01.mkv"}}}}`))
	}))
	defer server.Close()
	status, err := NewClient(server.URL, "").Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.MediaPath != "Show.S01E01.mkv" {
		t.Fatalf("path = %q", status.MediaPath)
	}
}

func TestMediaPathPreservesUNCFileHost(t *testing.T) {
	path, err := mediaPath("file://media-server/TV/Show%20Name/S01E01.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if path != "//media-server/TV/Show Name/S01E01.mkv" {
		t.Fatalf("path = %q", path)
	}
}
